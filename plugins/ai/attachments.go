package ai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/attach"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// Attachments (R91, docs/ai.md §24): files the user attaches to the next
// message of a chat, by dropping them onto the launcher, pasting them (Ctrl+V
// with files or an image on the clipboard) or choosing them (Ctrl+O). They
// are checked and prepared (attach.PrepareFile) and copied into the chat's
// directory under the plugin's data directory; the draft shows them as chips
// above the footer. A message is sent only when every attachment can be sent
// by the chat's backend. Nothing is ever attached on its own: the host reads
// the clipboard only for Ctrl+V in the chat. Logs have counts, kinds and
// sizes, never names or contents.

// Attachment limits (docs/ai.md §24); attach.DefaultLimits has those of a
// single file.
const (
	// maxAttachments is how many files one message can have.
	maxAttachments = 10
	// maxAttachedBytes is the total size of the prepared copies of one
	// message's attachments (base64 makes it about 21 MB on the wire).
	maxAttachedBytes = 16 << 20
)

// attachmentsOnlyPrompt is the text sent to the model for a message that has
// attachments but no text (written for the model: not translated).
const attachmentsOnlyPrompt = "Please take a look at the attached files."

// reasonTotal is the plugin's own reason (beside the attach.Reason codes):
// the attachments of the draft would be larger than maxAttachedBytes.
const reasonTotal attach.Reason = "totalTooLarge"

// Attached is an attachment as stored with a message.
type Attached struct {
	ID string `json:"id"`
	// Name is the file's name as the user knows it.
	Name string `json:"name"`
	// Kind is backend.AttachImage or backend.AttachText.
	Kind      backend.AttachmentKind `json:"kind"`
	MediaType string                 `json:"mediaType,omitempty"`
	// Size is the size of the copy.
	Size int64 `json:"size"`
	// File and Thumb are the base names of the copy and of an image's
	// thumbnail in the chat's attachment directory.
	File  string `json:"file"`
	Thumb string `json:"thumb,omitempty"`
	// Width and Height are an image's (of the copy); Resized marks one that
	// was scaled down.
	Width   int  `json:"width,omitzero"`
	Height  int  `json:"height,omitzero"`
	Resized bool `json:"resized,omitzero"`
}

// draftAttachment is an attachment of the next message.
type draftAttachment struct {
	Attached
	// reason tells why the file cannot be sent at all ("" when it can, then
	// File holds the copy).
	reason attach.Reason
}

// attachRoot is the directory with the chats' attachment directories.
func (p *Plugin) attachRoot() string { return filepath.Join(p.h.DataDir(), "attachments") }

// attachDir is the directory of the chat with id ("" for an id that cannot
// be a directory name).
func (p *Plugin) attachDir(id string) string {
	if id == "" || strings.ContainsFunc(id, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}) {
		return ""
	}
	return filepath.Join(p.attachRoot(), id)
}

// removeAttachments deletes the attachments of the chat with id.
func (p *Plugin) removeAttachments(id string) {
	if dir := p.attachDir(id); dir != "" {
		if err := os.RemoveAll(dir); err != nil {
			p.h.Logger().Warn("delete attachments", "err", err)
		}
	}
}

// orphanAge is how long the attachment directory of a chat that was never
// sent (a draft that was dropped) is kept.
const orphanAge = 24 * time.Hour

// pruneAttachments deletes the attachment directories of chats that no
// longer exist (deleted while the plugin was off, or drafts never sent) once
// they are older than orphanAge.
func (p *Plugin) pruneAttachments() {
	entries, err := os.ReadDir(p.attachRoot())
	if err != nil {
		return // none yet
	}
	list, err := p.st.Sessions()
	if err != nil {
		return
	}
	known := map[string]bool{}
	for _, s := range list {
		known[s.ID] = true
	}
	now := time.Now()
	removed := 0
	for _, e := range entries {
		id := e.Name()
		p.mu.Lock()
		_, live := p.chats[id]
		p.mu.Unlock()
		if known[id] || live || !e.IsDir() {
			continue
		}
		if info, err := e.Info(); err != nil || now.Sub(info.ModTime()) < orphanAge {
			continue
		}
		if os.RemoveAll(filepath.Join(p.attachRoot(), id)) == nil {
			removed++
		}
	}
	if removed > 0 {
		p.h.Logger().Info("pruned attachments of chats that no longer exist", "count", removed)
	}
}

// newAttachmentID returns a random ID for an attachment's files.
func newAttachmentID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// prepared is a file checked and prepared for the draft, not yet stored.
type prepared struct {
	name string
	p    attach.Prepared
}

// prepareFiles checks and prepares files (and a pasted image) for a draft.
// It reads the files, so it runs without Plugin.mu.
func (p *Plugin) prepareFiles(paths []string, image []byte) []prepared {
	lim := attach.DefaultLimits()
	var out []prepared
	for _, path := range paths {
		out = append(out, prepared{name: filepath.Base(path), p: attach.PrepareFile(path, lim)})
	}
	if len(image) > 0 {
		pi := attach.PrepareImage(image, lim)
		ext := pi.Ext
		if ext == "" {
			ext = ".png"
		}
		out = append(out, prepared{name: p.h.I18n().T("attach.pastedName") + ext, p: pi})
	}
	return out
}

// addDraft stores prepared files in the chat's directory and adds them to
// its draft; it returns how many did not fit (maxAttachments). Plugin.mu
// must not be held: it writes the files, then takes the lock.
func (p *Plugin) addDraft(ch *chat, files []prepared) (refused int) {
	p.mu.Lock()
	n := len(ch.draft)
	var size int64
	for _, d := range ch.draft {
		size += d.Size
	}
	id := ch.sess.ID
	p.mu.Unlock()
	dir := p.attachDir(id)
	var add []draftAttachment
	for _, f := range files {
		if n+len(add) >= maxAttachments {
			refused++
			continue
		}
		d := draftAttachment{Attached: Attached{ID: newAttachmentID(), Name: f.name, Kind: f.p.Kind, MediaType: f.p.MediaType,
			Size: int64(len(f.p.Data)), Width: f.p.Width, Height: f.p.Height, Resized: f.p.Resized}, reason: f.p.Reason}
		if d.reason == "" && size+d.Size > maxAttachedBytes {
			d.reason = reasonTotal
		}
		if d.reason == "" && dir == "" {
			d.reason = attach.ReasonUnreadable
		}
		if d.reason == "" {
			if err := p.storeAttachment(dir, &d, f.p); err != nil {
				p.h.Logger().Warn("store attachment", "err", err)
				d.reason = attach.ReasonUnreadable
			}
		}
		if d.reason != "" {
			d.Size = f.p.SourceSize
		} else {
			size += d.Size
		}
		add = append(add, d)
	}
	p.mu.Lock()
	ch.draft = append(ch.draft, add...)
	p.mu.Unlock()
	images, texts, bad := 0, 0, 0
	for _, d := range add {
		switch {
		case d.reason != "":
			bad++
		case d.Kind == backend.AttachImage:
			images++
		default:
			texts++
		}
	}
	p.h.Logger().Info("attachments added", "images", images, "texts", texts, "unsupported", bad, "refused", refused, "bytes", size)
	return refused
}

// storeAttachment writes the copy (and thumbnail) of d into dir.
func (p *Plugin) storeAttachment(dir string, d *draftAttachment, pr attach.Prepared) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	ext := pr.Ext
	if ext == "" {
		ext = ".bin"
	}
	d.File = d.ID + ext
	if err := os.WriteFile(filepath.Join(dir, d.File), pr.Data, 0o600); err != nil {
		return err
	}
	if len(pr.Thumb) > 0 {
		d.Thumb = d.ID + ".thumb.png"
		if err := os.WriteFile(filepath.Join(dir, d.Thumb), pr.Thumb, 0o600); err != nil {
			d.Thumb = ""
		}
	}
	return nil
}

// deleteDraftFiles deletes the files of draft attachments (removed from the
// draft, or the draft dropped). Plugin.mu need not be held.
func (p *Plugin) deleteDraftFiles(chatID string, draft []draftAttachment) {
	dir := p.attachDir(chatID)
	if dir == "" {
		return
	}
	for _, d := range draft {
		for _, f := range []string{d.File, d.Thumb} {
			if f != "" {
				_ = os.Remove(filepath.Join(dir, f))
			}
		}
	}
}

// discardDraft drops the draft attachments of a chat leaving memory.
// Plugin.mu must be held.
func (p *Plugin) discardDraft(ch *chat) {
	if len(ch.draft) == 0 {
		return
	}
	draft := ch.draft
	ch.draft = nil
	p.deleteDraftFiles(ch.sess.ID, draft)
}

// ReceiveFiles implements plugin.FileReceiver: files dropped onto a chat,
// pasted or chosen there join its draft; files dropped onto the root search
// open a new chat with them (with AI off, its prompt to turn AI on, which
// keeps them).
func (p *Plugin) ReceiveFiles(_ context.Context, f plugin.Files) (plugin.Result, error) {
	if len(f.Paths) == 0 && len(f.Image) == 0 {
		return plugin.Result{}, nil
	}
	if f.Instance == "" {
		if f.Source != plugin.FilesDropped {
			return plugin.Result{}, plugin.ErrFilesNotTaken
		}
		ch := &chat{sess: p.newSession()}
		files := p.prepareFiles(f.Paths, nil)
		p.mu.Lock()
		p.chats[ch.sess.ID] = ch
		p.mu.Unlock()
		refused := p.addDraft(ch, files)
		r := plugin.Result{Push: &plugin.ViewRequest{ID: viewChat, Payload: plugin.MustPayload(chatPayload{Session: ch.sess.ID})}}
		r.Toast = p.refusedToast(refused)
		return r, nil
	}
	p.mu.Lock()
	ch := p.chats[p.views[f.Instance]]
	p.mu.Unlock()
	if ch == nil {
		return plugin.Result{}, plugin.ErrFilesNotTaken
	}
	refused := p.addDraft(ch, p.prepareFiles(f.Paths, f.Image))
	return plugin.Result{Refresh: true, Toast: p.refusedToast(refused)}, nil
}

// refusedToast says that some files did not fit into the draft.
func (p *Plugin) refusedToast(refused int) *plugin.Toast {
	if refused == 0 {
		return nil
	}
	l := p.h.I18n()
	return &plugin.Toast{Style: plugin.ToastFailure, Title: l.T("attach.tooMany", "max", maxAttachments),
		Message: l.N("attach.tooManyLeft", refused)}
}

// detachPayload is the payload of the "detach" callback (a chip's ×).
type detachPayload struct {
	Instance string `json:"instance"`
	ID       string `json:"id"`
}

// detach removes an attachment from the draft of the chat in a view.
func (p *Plugin) detach(a plugin.ActionDo) (plugin.Result, error) {
	var dp detachPayload
	if err := json.Unmarshal(a.Payload, &dp); err != nil {
		return plugin.Result{}, err
	}
	p.mu.Lock()
	ch := p.chats[p.views[dp.Instance]]
	var gone []draftAttachment
	if ch != nil {
		ch.draft = slices.DeleteFunc(ch.draft, func(d draftAttachment) bool {
			if d.ID == dp.ID {
				gone = append(gone, d)
				return true
			}
			return false
		})
	}
	p.mu.Unlock()
	if ch != nil {
		p.deleteDraftFiles(ch.sess.ID, gone)
	}
	return plugin.Result{Refresh: true}, nil
}

// draftBackend is the backend the draft of ch goes to: the chat's own, or
// for a chat without messages the one chosen now (it takes that one with
// its first message). Plugin.mu must be held.
func (p *Plugin) draftBackend(ch *chat) (string, backend.Backend) {
	sess := ch.sess
	if len(ch.msgs) == 0 {
		sess.BackendID = p.be.id()
	}
	return p.backendOf(sess)
}

// draftProblem is why an attachment of the draft cannot be sent by the
// backend of the chat ("" when it can). Plugin.mu must be held.
func (p *Plugin) draftProblem(ch *chat, d draftAttachment) string {
	l := p.h.I18n()
	id, be := p.draftBackend(ch)
	sess := ch.sess
	sess.BackendID = id
	name := p.backendName(sess)
	if d.reason != "" {
		return reasonText(l, d.reason, name)
	}
	if !backend.AttachmentsOf(be).Accepts(d.Kind, d.MediaType) {
		if d.Kind == backend.AttachImage {
			return l.T("attach.reason.noImages", "backend", name)
		}
		return l.T("attach.reason.noText", "backend", name)
	}
	return ""
}

// reasonText words why a file cannot be attached.
func reasonText(l plugin.Localizer, r attach.Reason, backendName string) string {
	switch r {
	case attach.ReasonFolder:
		return l.T("attach.reason.folder")
	case attach.ReasonEmpty:
		return l.T("attach.reason.empty")
	case attach.ReasonUnreadable:
		return l.T("attach.reason.unreadable")
	case attach.ReasonPDF:
		return l.T("attach.reason.pdf")
	case attach.ReasonOffice:
		return l.T("attach.reason.office")
	case attach.ReasonArchive:
		return l.T("attach.reason.archive")
	case attach.ReasonHEIC:
		return l.T("attach.reason.heic")
	case attach.ReasonImageInvalid:
		return l.T("attach.reason.imageInvalid")
	case attach.ReasonVideo:
		return l.T("attach.reason.video", "backend", backendName)
	case attach.ReasonAudio:
		return l.T("attach.reason.audio", "backend", backendName)
	case attach.ReasonTextTooLarge:
		return l.T("attach.reason.textTooLarge", "max", sizeText(l, attach.DefaultLimits().MaxTextBytes))
	case attach.ReasonImageTooLarge:
		lim := attach.DefaultLimits()
		return l.T("attach.reason.imageTooLarge", "max", sizeText(l, lim.MaxSourceImageBytes), "pixels", l.Number(float64(lim.MaxImagePixels/1_000_000)))
	case reasonTotal:
		return l.T("attach.reason.totalTooLarge", "max", sizeText(l, maxAttachedBytes))
	}
	return l.T("attach.reason.binary")
}

// sizeText is a file size for people: "512 B", "12 KB", "3.4 MB".
func sizeText(l plugin.Localizer, n int64) string {
	switch {
	case n < 1024:
		return l.T("attach.size.bytes", "n", l.Number(float64(n)))
	case n < 1024*1024:
		return l.T("attach.size.kb", "n", l.Number(float64((n+512)/1024)))
	}
	mb := float64(n) / (1024 * 1024)
	mb = float64(int64(mb*10+0.5)) / 10
	return l.T("attach.size.mb", "n", l.Number(mb))
}

// chips are the chips of the draft of ch shown in a view instance. Plugin.mu
// must be held.
func (p *Plugin) chips(ch *chat, instance string) []plugin.Chip {
	if len(ch.draft) == 0 {
		return nil
	}
	l := p.h.I18n()
	dir := p.attachDir(ch.sess.ID)
	out := make([]plugin.Chip, 0, len(ch.draft))
	for _, d := range ch.draft {
		c := plugin.Chip{ID: d.ID, Title: d.Name, Icon: plugin.Icon{Name: "file"},
			Remove: &plugin.Action{ID: "detach", Title: l.T("attach.remove"),
				Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "detach", Payload: plugin.MustPayload(detachPayload{Instance: instance, ID: d.ID})}}}
		if d.Kind == backend.AttachImage || d.reason != "" && strings.HasPrefix(d.MediaType, "image/") {
			c.Icon = plugin.Icon{Name: "image"}
		}
		if d.Thumb != "" && dir != "" {
			c.Image = filepath.Join(dir, d.Thumb)
		}
		if problem := p.draftProblem(ch, d); problem != "" {
			c.Error, c.Subtitle = true, problem
			c.Tooltip = d.Name + " · " + problem
		} else {
			c.Subtitle = sizeText(l, d.Size)
			if d.Width > 0 && d.Height > 0 {
				dims := strconv.Itoa(d.Width) + "×" + strconv.Itoa(d.Height)
				if d.Resized {
					c.Subtitle += " · " + l.T("attach.resized", "size", dims)
				} else {
					c.Subtitle += " · " + dims
				}
			}
			c.Tooltip = d.Name
		}
		out = append(out, c)
	}
	return out
}

// draftBlocked returns the names of the draft's attachments that cannot be
// sent, with why, or "" when all can. Plugin.mu must be held.
func (p *Plugin) draftBlocked(ch *chat) string {
	var bad []string
	for _, d := range ch.draft {
		if problem := p.draftProblem(ch, d); problem != "" {
			bad = append(bad, d.Name+" ("+problem+")")
		}
	}
	return strings.Join(bad, ", ")
}

// takeDraft returns the attachments of the draft as stored with the message
// and empties the draft. Plugin.mu must be held.
func (ch *chat) takeDraft() []Attached {
	if len(ch.draft) == 0 {
		return nil
	}
	out := make([]Attached, len(ch.draft))
	for i, d := range ch.draft {
		out[i] = d.Attached
	}
	ch.draft = nil
	return out
}

// backendAttachments are the attachments of a message as the backend reads
// them.
func (p *Plugin) backendAttachments(chatID string, atts []Attached) []backend.Attachment {
	if len(atts) == 0 {
		return nil
	}
	dir := p.attachDir(chatID)
	out := make([]backend.Attachment, len(atts))
	for i, a := range atts {
		out[i] = backend.Attachment{Kind: a.Kind, Name: a.Name, Path: filepath.Join(dir, a.File), MediaType: a.MediaType, Size: a.Size}
	}
	return out
}

// attachmentsMarkdown shows the attachments of a sent message: the images'
// thumbnails in a row, then every file's name and size.
func (p *Plugin) attachmentsMarkdown(chatID string, atts []Attached) string {
	if len(atts) == 0 {
		return ""
	}
	l := p.h.I18n()
	dir := p.attachDir(chatID)
	var thumbs, names []string
	for _, a := range atts {
		if a.Thumb != "" && dir != "" {
			if _, err := os.Stat(filepath.Join(dir, a.Thumb)); err == nil {
				thumbs = append(thumbs, "!["+markdownText(a.Name)+"](<"+filepath.ToSlash(filepath.Join(dir, a.Thumb))+">)")
			}
		}
		names = append(names, markdownText(a.Name)+" ("+sizeText(l, a.Size)+")")
	}
	var b strings.Builder
	if len(thumbs) > 0 {
		b.WriteString(strings.Join(thumbs, " ") + "\n\n")
	}
	b.WriteString("📎 " + strings.Join(names, " · "))
	return b.String()
}

// markdownText escapes the characters of s that Markdown would take as
// formatting.
func markdownText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune("\\`*_[]()<>#!|~", r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
