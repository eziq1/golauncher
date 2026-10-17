package core

// Files handed to the root search (R97, docs/root-files.md): dropped onto
// the window, pasted with Ctrl+V, chosen with Ctrl+O or an ActionChooseFiles
// of a root item. The Controller keeps them for this show (never on disk,
// never logged but their number), shows them as chips, and the actions the
// plugins declaring plugin.PermFiles offer for them
// (plugin.FileActionsProvider) form the "Files" section at the top of the
// root search.

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// KeyChooseFiles opens the open dialog in the root search, unless the
// selected entry has an action with this shortcut.
const KeyChooseFiles = "Ctrl+O"

// Limits of the files handed to the root search.
const (
	// maxRootFiles is how many files are kept; more are left out.
	maxRootFiles = 100
	// maxFileRows is the number of rows of the Files section while nothing
	// is typed.
	maxFileRows = 12
)

// pastedGrace is how long a pasted image's temporary file is kept after the
// files were dropped (the launcher hid), so that an app it was just opened
// in can still read it. Tests set it to 0.
var pastedGrace = time.Minute

// pastedStale is the age at which leftover pasted images (the launcher
// ended before deleting them) are removed when a new one is pasted.
const pastedStale = 24 * time.Hour

// rootFiles are the files handed to the root search.
type rootFiles struct {
	files  []plugin.FileInfo
	source plugin.FilesSource
	// temp are the directories the host wrote for them (a pasted image),
	// deleted when the files are dropped.
	temp []string
}

// fileKinds are the kinds and media types of extensions.
var fileKinds = map[string]struct {
	kind  plugin.FileKind
	media string
}{
	".png": {plugin.FileImage, "image/png"}, ".jpg": {plugin.FileImage, "image/jpeg"}, ".jpeg": {plugin.FileImage, "image/jpeg"},
	".gif": {plugin.FileImage, "image/gif"}, ".webp": {plugin.FileImage, "image/webp"}, ".bmp": {plugin.FileImage, "image/bmp"},
	".tif": {plugin.FileImage, "image/tiff"}, ".tiff": {plugin.FileImage, "image/tiff"}, ".heic": {plugin.FileImage, "image/heic"},
	".heif": {plugin.FileImage, "image/heif"}, ".avif": {plugin.FileImage, "image/avif"}, ".svg": {plugin.FileImage, "image/svg+xml"},
	".ico": {plugin.FileImage, "image/x-icon"},

	".txt": {plugin.FileText, "text/plain"}, ".md": {plugin.FileText, "text/markdown"}, ".csv": {plugin.FileText, "text/csv"},
	".json": {plugin.FileText, "application/json"}, ".xml": {plugin.FileText, "application/xml"}, ".yaml": {plugin.FileText, "application/yaml"},
	".yml": {plugin.FileText, "application/yaml"}, ".toml": {plugin.FileText, "application/toml"}, ".log": {plugin.FileText, "text/plain"},
	".ini": {plugin.FileText, "text/plain"}, ".html": {plugin.FileText, "text/html"}, ".htm": {plugin.FileText, "text/html"},
	".css": {plugin.FileText, "text/css"}, ".js": {plugin.FileText, "text/javascript"}, ".ts": {plugin.FileText, "text/plain"},
	".go": {plugin.FileText, "text/x-go"}, ".py": {plugin.FileText, "text/x-python"}, ".rs": {plugin.FileText, "text/plain"},
	".c": {plugin.FileText, "text/x-c"}, ".h": {plugin.FileText, "text/x-c"}, ".cpp": {plugin.FileText, "text/plain"},
	".cs": {plugin.FileText, "text/plain"}, ".java": {plugin.FileText, "text/plain"}, ".sh": {plugin.FileText, "text/plain"},
	".ps1": {plugin.FileText, "text/plain"}, ".bat": {plugin.FileText, "text/plain"}, ".cmd": {plugin.FileText, "text/plain"},
	".sql": {plugin.FileText, "text/plain"},

	".pdf": {plugin.FileDocument, "application/pdf"}, ".doc": {plugin.FileDocument, "application/msword"},
	".docx": {plugin.FileDocument, "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	".xls":  {plugin.FileDocument, "application/vnd.ms-excel"},
	".xlsx": {plugin.FileDocument, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
	".ppt":  {plugin.FileDocument, "application/vnd.ms-powerpoint"},
	".pptx": {plugin.FileDocument, "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
	".odt":  {plugin.FileDocument, "application/vnd.oasis.opendocument.text"}, ".rtf": {plugin.FileDocument, "application/rtf"},

	".zip": {plugin.FileArchive, "application/zip"}, ".7z": {plugin.FileArchive, "application/x-7z-compressed"},
	".rar": {plugin.FileArchive, "application/vnd.rar"}, ".tar": {plugin.FileArchive, "application/x-tar"},
	".gz": {plugin.FileArchive, "application/gzip"}, ".tgz": {plugin.FileArchive, "application/gzip"},
	".xz": {plugin.FileArchive, "application/x-xz"}, ".bz2": {plugin.FileArchive, "application/x-bzip2"},

	".mp3": {plugin.FileAudio, "audio/mpeg"}, ".wav": {plugin.FileAudio, "audio/wav"}, ".flac": {plugin.FileAudio, "audio/flac"},
	".m4a": {plugin.FileAudio, "audio/mp4"}, ".ogg": {plugin.FileAudio, "audio/ogg"}, ".aac": {plugin.FileAudio, "audio/aac"},

	".mp4": {plugin.FileVideo, "video/mp4"}, ".mov": {plugin.FileVideo, "video/quicktime"}, ".mkv": {plugin.FileVideo, "video/x-matroska"},
	".webm": {plugin.FileVideo, "video/webm"}, ".avi": {plugin.FileVideo, "video/x-msvideo"}, ".m4v": {plugin.FileVideo, "video/mp4"},
}

// describeFile is the FileInfo of path from its metadata and name, without
// reading it; false when it does not exist.
func describeFile(path string) (plugin.FileInfo, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return plugin.FileInfo{}, false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	info := plugin.FileInfo{Path: abs, Name: filepath.Base(abs), Kind: plugin.FileOther}
	if st.IsDir() {
		info.Dir, info.Kind = true, plugin.FileFolder
		return info, true
	}
	info.Size = st.Size()
	if k, ok := fileKinds[strings.ToLower(filepath.Ext(abs))]; ok {
		info.Kind, info.MediaType = k.kind, k.media
	}
	return info, true
}

// receiveRoot keeps files handed to the root search in place of the ones
// before, and shows the Files section. A pasted image is written to a
// temporary file first. Without a provider of file actions, or without a
// file that exists, a toast says they cannot be used.
func (c *Controller) receiveRoot(ctx context.Context, f plugin.Files) (Outcome, error) {
	notTaken := func() (Outcome, error) {
		t := &plugin.Toast{Style: plugin.ToastFailure, Title: c.eng.core.T("toast.filesNotTaken")}
		c.ShowToast(*t)
		return Outcome{Toast: t}, nil
	}
	if len(c.eng.fileProviders(c.eng.cfg.Get())) == 0 {
		return notTaken()
	}
	rf := &rootFiles{source: f.Source}
	if len(f.Image) > 0 {
		info, dir, err := c.eng.writePasted(f.Image)
		if err != nil {
			return c.applyOutcome(ctx, Outcome{}, err)
		}
		rf.files, rf.temp = append(rf.files, info), append(rf.temp, dir)
	}
	for _, p := range f.Paths {
		if len(rf.files) == maxRootFiles {
			break
		}
		if info, ok := describeFile(p); ok && !slices.ContainsFunc(rf.files, func(x plugin.FileInfo) bool { return x.Path == info.Path }) {
			rf.files = append(rf.files, info)
		}
	}
	if len(rf.files) == 0 {
		return notTaken()
	}
	c.eng.log.Info("root files", "source", f.Source, "files", len(rf.files), "image", len(f.Image) > 0)
	c.mu.Lock()
	old := c.files
	c.files = rf
	top := c.top()
	top.sel, top.selKey, top.moved = 0, "", false
	c.mu.Unlock()
	c.eng.dropFiles(old)
	c.refresh(ctx)
	return Outcome{}, nil
}

// clearFiles drops the files of the root search. c.mu must not be held.
func (c *Controller) clearFiles() bool {
	c.mu.Lock()
	old := c.files
	c.files = nil
	c.mu.Unlock()
	c.eng.dropFiles(old)
	return old != nil
}

// removeRootFile drops the file with chip id ("file:<i>") from the root
// search, and all of them with the last.
func (c *Controller) removeRootFile(ctx context.Context, id string) (Outcome, error) {
	i, err := strconv.Atoi(strings.TrimPrefix(id, fileChipPrefix))
	c.mu.Lock()
	rf := c.files
	if rf == nil || err != nil || !strings.HasPrefix(id, fileChipPrefix) || i < 0 || i >= len(rf.files) {
		c.mu.Unlock()
		return Outcome{}, errNoChip(id)
	}
	if len(rf.files) == 1 {
		c.mu.Unlock()
		c.clearFiles()
		c.refresh(ctx)
		return Outcome{}, nil
	}
	gone := rf.files[i]
	nf := &rootFiles{files: slices.Delete(slices.Clone(rf.files), i, i+1), source: rf.source}
	var drop *rootFiles
	for _, d := range rf.temp {
		if gone.Pasted && strings.HasPrefix(gone.Path, d+string(filepath.Separator)) {
			drop = &rootFiles{temp: []string{d}}
			continue
		}
		nf.temp = append(nf.temp, d)
	}
	c.files = nf
	c.mu.Unlock()
	c.eng.dropFiles(drop)
	c.refresh(ctx)
	return Outcome{}, nil
}

// fileChipPrefix starts the IDs of the root search's file chips.
const fileChipPrefix = "file:"

// fileChips are the chips of the root search's files. c.mu must be held.
func (c *Controller) fileChips() []plugin.Chip {
	if c.files == nil {
		return nil
	}
	core := c.eng.core
	out := make([]plugin.Chip, 0, len(c.files.files))
	for i, f := range c.files.files {
		ch := plugin.Chip{ID: fileChipPrefix + strconv.Itoa(i), Title: f.Name, Icon: plugin.Icon{Name: fileIcon(f)}, Tooltip: f.Name,
			Remove: &plugin.Action{ID: "remove", Title: core.T("ui.chip.remove", "name", f.Name)}}
		if f.Dir {
			ch.Subtitle = core.T("files.folder")
		} else {
			ch.Subtitle = sizeText(core, f.Size)
		}
		if f.Pasted {
			// The host's own file: the only one read for a thumbnail.
			ch.Image = f.Path
		}
		out = append(out, ch)
	}
	return out
}

// fileIcon is the icon of a file's kind.
func fileIcon(f plugin.FileInfo) string {
	switch f.Kind {
	case plugin.FileImage:
		return "image"
	case plugin.FileFolder:
		return "folder"
	case plugin.FileArchive:
		return "archive"
	case plugin.FileText:
		return "text"
	}
	return "file"
}

// localizer is what sizeText needs of a localizer.
type localizer interface {
	T(key string, args ...any) string
}

// sizeText is a file size for people: "512 B", "12 KB", "3.4 MB".
func sizeText(l localizer, n int64) string {
	switch {
	case n < 1024:
		return l.T("files.size.bytes", "n", strconv.FormatInt(n, 10))
	case n < 1024*1024:
		return l.T("files.size.kb", "n", strconv.FormatInt((n+512)/1024, 10))
	case n < 1024*1024*1024:
		return l.T("files.size.mb", "n", strconv.FormatFloat(float64(n)/(1024*1024), 'f', 1, 64))
	}
	return l.T("files.size.gb", "n", strconv.FormatFloat(float64(n)/(1024*1024*1024), 'f', 1, 64))
}

// fileProviders are the plugins that are on, declare plugin.PermFiles,
// implement plugin.FileActionsProvider and run in this process.
func (e *Engine) fileProviders(cfg config.Config) []*loaded {
	var out []*loaded
	for _, l := range e.enabled(cfg) {
		if _, ok := l.p.(plugin.FileActionsProvider); !ok || !slices.Contains(l.m.Permissions, plugin.PermFiles) {
			continue
		}
		if _, external := l.p.(ProcessPlugin); external {
			continue
		}
		out = append(out, l)
	}
	return out
}

// fileEntries collects the items of the file action providers for rf and
// text, the root search's text. The entries are never recorded, never shown
// to AI (Sensitive) and carry the section title; while nothing is typed the
// clear hits (Score) come first.
func (e *Engine) fileEntries(ctx context.Context, cfg config.Config, rf *rootFiles, text string) []Entry {
	if rf == nil || len(rf.files) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, e.rootTimeout)
	defer cancel()
	group := e.core.T("section.files")
	var (
		mu  sync.Mutex
		out []Entry
		wg  sync.WaitGroup
	)
	for _, l := range e.fileProviders(cfg) {
		fp := l.p.(plugin.FileActionsProvider)
		q := plugin.FilesQuery{Files: slices.Clone(rf.files), Source: rf.source, Text: text}
		wg.Go(func() {
			done := make(chan []plugin.Item, 1)
			go func() {
				items, err := guard(e, l.m.ID, func() ([]plugin.Item, error) { return fp.FileItems(ctx, q) })
				if err != nil {
					e.log.Warn("file items", "plugin", l.m.ID, "err", err)
				}
				done <- items
			}()
			select {
			case items := <-done:
				mu.Lock()
				for _, it := range items {
					it.Group = group
					it.Sensitive, it.Transient = true, true
					ent := newEntry(l.m.ID, it)
					ent.Context, ent.Files = true, true
					out = append(out, ent)
				}
				mu.Unlock()
			case <-ctx.Done():
				e.log.Warn("file items timed out", "plugin", l.m.ID)
			}
		})
	}
	wg.Wait()
	order := map[string]int{}
	for i, l := range e.plugins {
		order[l.m.ID] = i
	}
	slices.SortStableFunc(out, func(a, b Entry) int {
		if text == "" {
			if c := cmp.Compare(b.Score, a.Score); c != 0 {
				return c
			}
		}
		return cmp.Compare(order[a.Plugin], order[b.Plugin])
	})
	if text == "" {
		return out[:min(len(out), maxFileRows)]
	}
	// With text typed: those matching it, the best first.
	var matched []Entry
	for _, ent := range out {
		m := ent.Score
		if m == 0 {
			m = entryMatch(text, ent)
		}
		if m > 0 {
			ent.Rank = m
			matched = append(matched, ent)
		}
	}
	slices.SortStableFunc(matched, func(a, b Entry) int { return cmp.Compare(b.Rank, a.Rank) })
	return matched
}

// pastedRoot is the directory of the pasted images' temporary files: in the
// cache directory, next to the plugins' caches.
func (e *Engine) pastedRoot() string {
	if e.svc.CacheRoot != "" {
		return filepath.Join(filepath.Dir(e.svc.CacheRoot), "pasted")
	}
	return filepath.Join(os.TempDir(), "golauncher-pasted")
}

// writePasted writes a pasted PNG image to a new temporary directory and
// describes it. Leftovers of earlier runs are removed first.
func (e *Engine) writePasted(png []byte) (plugin.FileInfo, string, error) {
	root := e.pastedRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return plugin.FileInfo{}, "", err
	}
	e.pruneStalePasted(root)
	var b [6]byte
	_, _ = rand.Read(b[:])
	dir := filepath.Join(root, hex.EncodeToString(b[:]))
	if err := os.Mkdir(dir, 0o700); err != nil {
		return plugin.FileInfo{}, "", err
	}
	name := safeFileName(e.core.T("files.pastedName")) + ".png"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, png, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return plugin.FileInfo{}, "", err
	}
	return plugin.FileInfo{Path: path, Name: name, Size: int64(len(png)), Kind: plugin.FileImage, MediaType: "image/png", Pasted: true}, dir, nil
}

// safeFileName replaces the characters file systems refuse in names.
func safeFileName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(s))
	return cmp.Or(s, "image")
}

// pruneStalePasted removes the pasted images older than pastedStale.
func (e *Engine) pruneStalePasted(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, d := range entries {
		dir := filepath.Join(root, d.Name())
		if e.pendingTemp(dir) {
			continue
		}
		if info, err := d.Info(); err == nil && time.Since(info.ModTime()) > pastedStale {
			_ = os.RemoveAll(dir)
		}
	}
}

// tempFiles are the temporary directories waiting to be deleted.
type tempFiles struct {
	mu      sync.Mutex
	pending map[string]*time.Timer
}

// pendingTemp reports whether dir waits to be deleted.
func (e *Engine) pendingTemp(dir string) bool {
	e.temps.mu.Lock()
	defer e.temps.mu.Unlock()
	_, ok := e.temps.pending[dir]
	return ok
}

// dropFiles deletes the temporary files of rf after pastedGrace.
func (e *Engine) dropFiles(rf *rootFiles) {
	if rf == nil {
		return
	}
	for _, dir := range rf.temp {
		if pastedGrace <= 0 {
			_ = os.RemoveAll(dir)
			continue
		}
		e.temps.mu.Lock()
		if e.temps.pending == nil {
			e.temps.pending = map[string]*time.Timer{}
		}
		e.temps.pending[dir] = time.AfterFunc(pastedGrace, func() {
			e.temps.mu.Lock()
			delete(e.temps.pending, dir)
			e.temps.mu.Unlock()
			_ = os.RemoveAll(dir)
		})
		e.temps.mu.Unlock()
	}
}

// removeTemps deletes the temporary files waiting now (Close).
func (e *Engine) removeTemps() {
	e.temps.mu.Lock()
	pending := e.temps.pending
	e.temps.pending = nil
	e.temps.mu.Unlock()
	for dir, t := range pending {
		t.Stop()
		_ = os.RemoveAll(dir)
	}
}
