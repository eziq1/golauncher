package clipboard

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"image"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

type env struct {
	t   *testing.T
	p   *Plugin
	h   *hostfake.Host
	now time.Time
}

func setup(t *testing.T) *env {
	t.Helper()
	p := New()
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, p: p, h: h, now: hostfake.Fixed}
	h.Clock = func() time.Time { return e.now }
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return e
}

// copy simulates another app copying c, one second after the previous copy.
func (e *env) copy(c plugin.ClipboardContent) {
	e.now = e.now.Add(time.Second)
	e.h.Clip.Set(c)
	e.h.Bus.PublishSync(plugin.EventClipboardChanged)
}

func (e *env) copyText(texts ...string) {
	for _, s := range texts {
		e.copy(plugin.ClipboardContent{Text: s})
	}
}

func (e *env) pref(name string, v any) {
	e.h.Prefs.Set(name, jsontext.Value(plugin.MustPayload(v)))
}

func (e *env) view(query string) plugin.View {
	e.t.Helper()
	v, err := e.p.View(e.t.Context(), plugin.ViewRequest{ID: "history", Query: plugin.Query{Text: query}})
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

// items returns all items of a view, in order.
func items(v plugin.View) []plugin.Item {
	var out []plugin.Item
	for _, s := range v.Sections {
		out = append(out, s.Items...)
	}
	return out
}

func (e *env) titles(query string) []string {
	var out []string
	for _, it := range items(e.view(query)) {
		out = append(out, it.Title)
	}
	return out
}

func (e *env) action(it plugin.Item, id string) plugin.Action {
	e.t.Helper()
	for _, a := range it.Actions {
		if a.ID == id {
			return a
		}
	}
	e.t.Fatalf("item %q has no action %q", it.Title, id)
	return plugin.Action{}
}

func (e *env) run(a plugin.ActionDo) plugin.Result {
	e.t.Helper()
	r, err := e.p.HandleAction(e.t.Context(), a)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	img.Pix[0] = byte(w + h) // make images of the same size differ
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func meta(it plugin.Item, label string) string {
	for _, m := range it.Detail.Metadata {
		if m.Label == label {
			return m.Value
		}
	}
	return ""
}

func TestRecordText(t *testing.T) {
	e := setup(t)
	e.copy(plugin.ClipboardContent{Text: "\n  hello world  \nsecond line with ``` fence", SourceApp: "notepad.exe"})
	v := e.view("")
	if v.Kind != plugin.ViewList || !v.ShowDetail || v.Placeholder == "" || v.EmptyText == "" {
		t.Fatalf("bad view: %+v", v)
	}
	if len(v.Sections) != 1 || v.Sections[0].Title != "Recent" || len(v.Sections[0].Items) != 1 {
		t.Fatalf("sections = %+v", v.Sections)
	}
	it := v.Sections[0].Items[0]
	if it.Title != "hello world" || it.Icon.Name != "text" || it.Subtitle != "" || it.Accessory != "Just now" {
		t.Fatalf("item = %+v", it)
	}
	if !strings.HasPrefix(it.Detail.Markdown, "````\n") || !strings.Contains(it.Detail.Markdown, "second line") {
		t.Fatalf("markdown = %q", it.Detail.Markdown)
	}
	if got := meta(it, "Source"); got != "notepad.exe" {
		t.Errorf("Source = %q", got)
	}
	if got := meta(it, "Characters"); got != "43" {
		t.Errorf("Characters = %q", got)
	}
	if got := meta(it, "Copied"); got != "Just now" {
		t.Errorf("Copied = %q", got)
	}
	e.now = e.now.Add(5 * time.Minute)
	if got := items(e.view(""))[0].Accessory; got != "5 minutes ago" {
		t.Errorf("Accessory after 5m = %q", got)
	}
}

func TestLongTitleTruncated(t *testing.T) {
	e := setup(t)
	e.copyText(strings.Repeat("字", 100))
	title := items(e.view(""))[0].Title
	if n := len([]rune(title)); n != maxTitleRunes || !strings.HasSuffix(title, "…") {
		t.Fatalf("title %q has %d runes", title, n)
	}
}

func TestRecordImage(t *testing.T) {
	e := setup(t)
	data := pngBytes(t, 4, 3)
	e.copy(plugin.ClipboardContent{Image: data})
	it := items(e.view(""))[0]
	if it.Title != "Image (4×3)" || it.Icon.Name != "image" {
		t.Fatalf("item = %+v", it)
	}
	path := it.Detail.Image
	if filepath.Dir(path) != filepath.Join(e.h.DataDir(), "images") || !strings.HasSuffix(path, ".png") {
		t.Fatalf("image path %q", path)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("image file: %v", err)
	}
	if got := meta(it, "Dimensions"); got != "4 × 3" {
		t.Errorf("Dimensions = %q", got)
	}
	if got := meta(it, "Size"); !strings.HasSuffix(got, "bytes") {
		t.Errorf("Size = %q", got)
	}
	paste := e.action(it, "paste").Do
	var pc plugin.PasteContent
	if err := json.Unmarshal(paste.Payload, &pc); err != nil || paste.Kind != plugin.ActionPaste || paste.Target != "" || pc.ImagePath != path {
		t.Fatalf("paste = %+v (%v)", paste, err)
	}
	if c := e.action(it, "copy").Do; c.Kind != plugin.ActionCopyImage || c.Target != path {
		t.Fatalf("copy = %+v", c)
	}
	// Invalid image data is ignored.
	e.copy(plugin.ClipboardContent{Image: []byte("garbage")})
	if n := len(items(e.view(""))); n != 1 {
		t.Fatalf("%d items after invalid image", n)
	}
}

func TestRecordFiles(t *testing.T) {
	e := setup(t)
	files := []string{`C:\work\report_v2.pdf`, `D:\图片\cat.png`}
	e.copy(plugin.ClipboardContent{Files: files, Text: "ignored when files are present"})
	it := items(e.view(""))[0]
	if it.Title != "report_v2.pdf, cat.png" || it.Icon.Name != "file" {
		t.Fatalf("item = %+v", it)
	}
	if want := "- C:\\\\work\\\\report\\_v2.pdf\n- D:\\\\图片\\\\cat.png\n"; it.Detail.Markdown != want {
		t.Fatalf("markdown = %q, want %q", it.Detail.Markdown, want)
	}
	if got := meta(it, "Files"); got != "2" {
		t.Errorf("Files = %q", got)
	}
	paste := e.action(it, "paste").Do
	var pc plugin.PasteContent
	if err := json.Unmarshal(paste.Payload, &pc); err != nil || !slices.Equal(pc.Files, files) || pc.ImagePath != "" {
		t.Fatalf("paste = %+v (%v)", paste, err)
	}
	c := e.action(it, "copy").Do
	var got []string
	if err := json.Unmarshal(c.Payload, &got); err != nil || c.Kind != plugin.ActionCopyFiles || !slices.Equal(got, files) {
		t.Fatalf("copy = %+v (%v)", c, err)
	}
}

func TestTextActions(t *testing.T) {
	e := setup(t)
	e.copyText("hello")
	it := items(e.view(""))[0]
	ids := make([]string, len(it.Actions))
	for i, a := range it.Actions {
		ids[i] = a.ID
	}
	if !slices.Equal(ids, []string{"paste", "copy", "pin", "delete"}) {
		t.Fatalf("actions = %v", ids)
	}
	if a := it.Actions[0]; a.Do.Kind != plugin.ActionPaste || a.Do.Target != "hello" || a.Title != "Paste" {
		t.Errorf("paste = %+v", a)
	}
	if a := it.Actions[1]; a.Do.Kind != plugin.ActionCopyText || a.Do.Target != "hello" || a.Shortcut != "Ctrl+Enter" {
		t.Errorf("copy = %+v", a)
	}
	if a := it.Actions[2]; a.Do.Kind != plugin.ActionCallback || a.Do.Target != "pin" || a.Shortcut != "Ctrl+Shift+P" ||
		string(a.Do.Payload) != `{"id":`+it.ID+`}` {
		t.Errorf("pin = %+v", a)
	}
	if a := it.Actions[3]; a.Do.Kind != plugin.ActionCallback || a.Do.Target != "delete" || a.Shortcut != "Ctrl+X" || a.Style != plugin.StyleDestructive {
		t.Errorf("delete = %+v", a)
	}
}

func TestDedupe(t *testing.T) {
	e := setup(t)
	e.copyText("a", "b", "a")
	if got := e.titles(""); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("titles = %v", got)
	}
	e.copy(plugin.ClipboardContent{Image: pngBytes(t, 2, 2)})
	e.copy(plugin.ClipboardContent{Image: pngBytes(t, 2, 2)})
	e.copy(plugin.ClipboardContent{Files: []string{`C:\x`}})
	e.copy(plugin.ClipboardContent{Files: []string{`C:\x`}})
	if n := len(items(e.view(""))); n != 4 {
		t.Fatalf("%d items, want 4", n)
	}
	// The same text as a file name is a different clip.
	e.copyText(`C:\x`)
	if n := len(items(e.view(""))); n != 5 {
		t.Fatalf("%d items, want 5", n)
	}
}

func TestSkipped(t *testing.T) {
	e := setup(t)
	e.copy(plugin.ClipboardContent{Text: "secret", Exclude: true})
	e.copy(plugin.ClipboardContent{Text: "password", SourceApp: "keepassxc.EXE"})
	e.copy(plugin.ClipboardContent{Text: "   \r\n\t"})
	e.copy(plugin.ClipboardContent{})
	e.pref("recordImages", false)
	e.copy(plugin.ClipboardContent{Image: pngBytes(t, 1, 1)})
	e.pref("ignoreApps", []string{"Slack.exe"})
	e.copy(plugin.ClipboardContent{Text: "from slack", SourceApp: "slack.exe"})
	e.copy(plugin.ClipboardContent{Text: "keepass is fine now", SourceApp: "KeePassXC.exe"})
	if got := e.titles(""); !slices.Equal(got, []string{"keepass is fine now"}) {
		t.Fatalf("titles = %v", got)
	}
	entries, _ := os.ReadDir(filepath.Join(e.h.DataDir(), "images"))
	if len(entries) != 0 {
		t.Fatalf("image files written: %v", entries)
	}
}

func TestPruneMaxItems(t *testing.T) {
	e := setup(t)
	e.pref("maxItems", 3)
	e.copy(plugin.ClipboardContent{Image: pngBytes(t, 1, 1)})
	imgPath := items(e.view(""))[0].Detail.Image
	e.copyText("keep")
	keep := items(e.view(""))[0]
	e.run(e.action(keep, "pin").Do)
	e.copyText("1", "2", "3", "4")
	if got := e.titles(""); !slices.Equal(got, []string{"keep", "4", "3", "2"}) {
		t.Fatalf("titles = %v", got)
	}
	if _, err := os.Stat(imgPath); !os.IsNotExist(err) {
		t.Fatalf("pruned image file still exists: %v", err)
	}
	// A repeated copy only refreshes; it does not prune.
	e.copyText("2")
	if got := e.titles(""); !slices.Equal(got, []string{"keep", "2", "4", "3"}) {
		t.Fatalf("titles = %v", got)
	}
}

func TestPruneMaxDays(t *testing.T) {
	e := setup(t)
	e.pref("maxDays", 2)
	e.copyText("old")
	e.now = e.now.Add(3 * 24 * time.Hour)
	e.copyText("new")
	if got := e.titles(""); !slices.Equal(got, []string{"new"}) {
		t.Fatalf("titles = %v", got)
	}
	e.pref("maxDays", 0) // forever
	e.copyText("older")
	e.now = e.now.Add(1000 * 24 * time.Hour)
	e.copyText("newest")
	if got := e.titles(""); !slices.Equal(got, []string{"newest", "older", "new"}) {
		t.Fatalf("titles = %v", got)
	}
	// Dates beyond a week are shown as dates.
	if acc := items(e.view(""))[1].Accessory; !strings.Contains(acc, ",") {
		t.Errorf("accessory = %q", acc)
	}
}

func TestSearch(t *testing.T) {
	e := setup(t)
	e.copyText("The quick brown fox", "你好世界", "hello world", "100% sure", "a_b")
	e.copy(plugin.ClipboardContent{Files: []string{`C:\docs\Annual Report.pdf`}})
	e.copy(plugin.ClipboardContent{Image: pngBytes(t, 2, 1)})
	for _, tc := range []struct {
		q    string
		want []string
	}{
		{"", []string{"Image (2×1)", "Annual Report.pdf", "a_b", "100% sure", "hello world", "你好世界", "The quick brown fox"}},
		{"quick", []string{"The quick brown fox"}},
		{"QUICK", []string{"The quick brown fox"}},
		{"uic", []string{"The quick brown fox"}},
		{"世界", []string{"你好世界"}},
		{"你好世界", []string{"你好世界"}},
		{"hello wor", []string{"hello world"}},
		{"wor hello", []string{"hello world"}},
		{"o", []string{"Annual Report.pdf", "hello world", "The quick brown fox"}},
		{"report", []string{"Annual Report.pdf"}},
		{`docs\`, []string{"Annual Report.pdf"}},
		{"%", []string{"100% sure"}},
		{"_", []string{"a_b"}},
		{`"quick"`, nil},
		{`qu"ick`, nil},
		{"zzz", nil},
	} {
		if got := e.titles(tc.q); !slices.Equal(got, tc.want) {
			t.Errorf("search %q = %q, want %q", tc.q, got, tc.want)
		}
	}
	if v := e.view("zzz"); v.EmptyText != "No matching items" {
		t.Errorf("EmptyText = %q", v.EmptyText)
	}
}

func TestPinnedSection(t *testing.T) {
	e := setup(t)
	e.copyText("a", "b", "c")
	byTitle := func(title string) plugin.Item {
		for _, it := range items(e.view("")) {
			if it.Title == title {
				return it
			}
		}
		t.Fatalf("no item %q", title)
		return plugin.Item{}
	}
	r := e.run(e.action(byTitle("a"), "pin").Do)
	if !r.Refresh || r.Toast == nil || r.Toast.Title != "Pinned" {
		t.Fatalf("pin result = %+v", r)
	}
	e.run(e.action(byTitle("b"), "pin").Do)
	v := e.view("")
	if len(v.Sections) != 2 || v.Sections[0].Title != "Pinned" || v.Sections[1].Title != "Recent" {
		t.Fatalf("sections = %+v", v.Sections)
	}
	if got := e.titles(""); !slices.Equal(got, []string{"b", "a", "c"}) {
		t.Fatalf("titles = %v", got)
	}
	pinned := v.Sections[0].Items[0]
	if a := pinned.Actions[2]; a.ID != "unpin" || a.Do.Target != "unpin" {
		t.Fatalf("pinned item action = %+v", a)
	}
	// Copying a pinned item again moves it up within the pinned section.
	e.copyText("a")
	if got := e.titles(""); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("titles = %v", got)
	}
	// Search keeps the sections.
	if v := e.view("a"); len(v.Sections) != 2 || len(v.Sections[1].Items) != 0 {
		t.Fatalf("search sections = %+v", v.Sections)
	}
	r = e.run(e.action(byTitle("a"), "unpin").Do)
	if r.Toast.Title != "Unpinned" {
		t.Fatalf("unpin toast = %+v", r.Toast)
	}
	e.run(e.action(byTitle("b"), "unpin").Do)
	if v := e.view(""); len(v.Sections) != 1 {
		t.Fatalf("sections after unpin = %+v", v.Sections)
	}
}

func TestDelete(t *testing.T) {
	e := setup(t)
	e.copy(plugin.ClipboardContent{Image: pngBytes(t, 3, 3)})
	e.copyText("text")
	all := items(e.view(""))
	img := all[1]
	r := e.run(e.action(img, "delete").Do)
	if !r.Refresh || r.Toast == nil || r.Toast.Title != "Deleted from history" {
		t.Fatalf("delete result = %+v", r)
	}
	if _, err := os.Stat(img.Detail.Image); !os.IsNotExist(err) {
		t.Fatalf("image file not removed: %v", err)
	}
	if got := e.titles(""); !slices.Equal(got, []string{"text"}) {
		t.Fatalf("titles = %v", got)
	}
	// Deleting again reports the item is gone.
	r = e.run(e.action(img, "delete").Do)
	if r.Toast == nil || r.Toast.Style != plugin.ToastFailure {
		t.Fatalf("second delete = %+v", r)
	}
	// The deleted text can be recorded again and is searchable.
	e.run(e.action(items(e.view(""))[0], "delete").Do)
	e.copyText("text")
	if got := e.titles("text"); !slices.Equal(got, []string{"text"}) {
		t.Fatalf("titles = %v", got)
	}
}

func TestClear(t *testing.T) {
	e := setup(t)
	e.copyText("pinned", "a")
	e.copy(plugin.ClipboardContent{Image: pngBytes(t, 5, 5)})
	img := items(e.view(""))[0].Detail.Image
	e.run(e.action(items(e.view("pinned"))[0], "pin").Do)
	r := e.run(plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: "clear"})
	if !r.PopToRoot || r.Toast == nil || r.Toast.Title != "Cleared 2 items" {
		t.Fatalf("clear result = %+v", r)
	}
	if got := e.titles(""); !slices.Equal(got, []string{"pinned"}) {
		t.Fatalf("titles = %v", got)
	}
	if _, err := os.Stat(img); !os.IsNotExist(err) {
		t.Fatalf("image file not removed: %v", err)
	}
	r = e.run(plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: "clear"})
	if r.Toast.Title != "Cleared 0 items" {
		t.Fatalf("toast = %q", r.Toast.Title)
	}
	e.copyText("x")
	r = e.run(plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: "clear"})
	if r.Toast.Title != "Cleared 1 item" {
		t.Fatalf("toast = %q", r.Toast.Title)
	}
}

func TestBadRequests(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	if _, err := e.p.View(ctx, plugin.ViewRequest{ID: "nope"}); err == nil {
		t.Error("unknown view accepted")
	}
	for _, a := range []plugin.ActionDo{
		{Kind: plugin.ActionCallback, Target: "pin", Payload: jsontext.Value(`nope`)},
		{Kind: plugin.ActionCallback, Target: "explode", Payload: jsontext.Value(`{"id":1}`)},
		{Kind: plugin.ActionRunCommand, Target: "history"},
	} {
		if _, err := e.p.HandleAction(ctx, a); err == nil {
			t.Errorf("action %+v accepted", a)
		}
	}
	e.p.Close()
	if _, err := e.p.View(ctx, plugin.ViewRequest{ID: "history"}); err == nil {
		t.Error("View after Close succeeded")
	}
	e.copyText("after close") // must not panic
}

func TestReopen(t *testing.T) {
	dir := t.TempDir()
	p := New()
	h, err := hostfake.New(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	h.Clip.Set(plugin.ClipboardContent{Text: "persisted"})
	h.Bus.PublishSync(plugin.EventClipboardChanged)
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	// Events after Close are not recorded.
	h.Clip.Set(plugin.ClipboardContent{Text: "lost"})
	h.Bus.PublishSync(plugin.EventClipboardChanged)

	p2 := New()
	if err := p2.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	defer p2.Close()
	v, err := p2.View(t.Context(), plugin.ViewRequest{ID: "history", Query: plugin.Query{Text: "persisted"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := items(v); len(got) != 1 || got[0].Title != "persisted" {
		t.Fatalf("items = %+v", got)
	}
	var version int
	if err := p2.db.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != len(migrations)-1 {
		t.Fatalf("user_version = %d (%v)", version, err)
	}
	if err := p2.db.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestConcurrentRecordAndView(t *testing.T) {
	e := setup(t)
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			for j := range 10 {
				e.h.Clip.Set(plugin.ClipboardContent{Text: "item " + strconv.Itoa(i*10+j)})
				e.h.Bus.PublishSync(plugin.EventClipboardChanged)
			}
		})
		wg.Go(func() {
			for range 10 {
				if _, err := e.p.View(t.Context(), plugin.ViewRequest{ID: "history", Query: plugin.Query{Text: "item"}}); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if n := len(items(e.view("item"))); n < 1 || n > 40 {
		t.Fatalf("%d items", n)
	}
}

// TestI18nKeys checks that every message key used in the code and manifest
// exists in en.json and that en.json has no unused keys.
func TestI18nKeys(t *testing.T) {
	data, err := fs.ReadFile(New().Locales(), "en.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog map[string]jsontext.Value
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	callRe := regexp.MustCompile(`\.[TN]\("([^"]+)"`)
	manifestRe := regexp.MustCompile(`"%([^%" ]+)%"`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, re := range []*regexp.Regexp{callRe, manifestRe} {
			for _, m := range re.FindAllSubmatch(src, -1) {
				used[string(m[1])] = true
			}
		}
	}
	if len(used) < 20 {
		t.Fatalf("found only %d keys; is the scan broken?", len(used))
	}
	for k := range used {
		if _, ok := catalog[k]; !ok {
			t.Errorf("key %q is missing from en.json", k)
		}
	}
	for k := range catalog {
		if !used[k] {
			t.Errorf("key %q in en.json is unused", k)
		}
	}
}

func TestManifest(t *testing.T) {
	m := New().Manifest()
	if m.ID != "builtin.clipboard" || m.Icon.Name != "clipboard" || len(m.Commands) != 2 {
		t.Fatalf("manifest = %+v", m)
	}
	if c := m.Commands[0]; c.Name != "history" || c.Mode != plugin.ModeView || c.DefaultHotkey != "Ctrl+Shift+V" {
		t.Errorf("history command = %+v", c)
	}
	if c := m.Commands[1]; c.Name != "clear" || c.Mode != plugin.ModeNoView {
		t.Errorf("clear command = %+v", c)
	}
	e := setup(t)
	prefs := e.h.Preferences()
	if prefs.Int("maxItems") != 1000 || prefs.Int("maxDays") != 90 || !prefs.Bool("recordImages") ||
		!slices.Contains(prefs.Strings("ignoreApps"), "1Password.exe") {
		t.Errorf("preference defaults wrong")
	}
	l := e.h.I18n()
	if got := l.T("plugin.name"); got != "Clipboard History" {
		t.Errorf("plugin.name = %q", got)
	}
}
