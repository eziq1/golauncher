package everything

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/winapi/everything"
	"github.com/eziq1/golauncher/pkg/plugin"
)

const fixturePath = "testdata/files.json"

// fake is a scripted Backend.
type fake struct {
	mu        sync.Mutex
	installed bool
	running   bool
	startErr  error
	starts    []string
	queries   []everything.Query
	results   []everything.Result
	searchErr error
}

func (f *fake) Installed() (string, bool) {
	return `C:\Program Files\Everything\Everything.exe`, f.installed
}

func (f *fake) Running() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

func (f *fake) Start(exe string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, exe)
	if f.startErr == nil {
		f.running = true
	}
	return f.startErr
}

func (f *fake) Search(ctx context.Context, q everything.Query) ([]everything.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	if !f.running {
		return nil, everything.ErrNotRunning
	}
	return slices.Clone(f.results), f.searchErr
}

func (f *fake) startCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.starts)
}

func (f *fake) setRunning(v bool) {
	f.mu.Lock()
	f.running = v
	f.mu.Unlock()
}

var modified = hostfake.Fixed.Add(-2 * time.Hour)

func sample() []everything.Result {
	return []everything.Result{
		{Name: "report.pdf", Path: `C:\Users\demo\Documents\report.pdf`, Size: 1536, Modified: modified},
		{Name: "Projects", Path: `D:\Projects`, IsFolder: true, Size: -1},
		{Name: "C:", Path: `C:\`, IsFolder: true, Size: -1},
	}
}

func start(t *testing.T, b Backend) (*Plugin, *hostfake.Host) {
	t.Helper()
	p := NewWithBackend(b)
	p.home = `C:\Users\demo`
	p.base = newRules(testEnv)
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p, h
}

func view(t *testing.T, p *Plugin, text string) plugin.View {
	t.Helper()
	v, err := p.View(t.Context(), plugin.ViewRequest{ID: cmdSearch, Query: plugin.Query{Text: text}})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func items(v plugin.View) []plugin.Item {
	var out []plugin.Item
	for _, s := range v.Sections {
		out = append(out, s.Items...)
	}
	return out
}

func TestUnavailableWhenNotInstalled(t *testing.T) {
	f := &fake{}
	p := NewWithBackend(f)
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = p.Init(t.Context(), h)
	if !errors.Is(err, plugin.ErrUnavailable) {
		t.Fatalf("Init = %v, want ErrUnavailable", err)
	}
	if want := "Everything is not installed. Get it from voidtools.com.: " + plugin.ErrUnavailable.Error(); err.Error() != want {
		t.Errorf("Init = %q, want %q", err, want)
	}
	if len(f.startCalls()) != 0 {
		t.Error("tried to start a missing Everything")
	}
}

func TestStartsEverythingWhenNotRunning(t *testing.T) {
	f := &fake{installed: true}
	p, h := start(t, f)
	p.wg.Wait()
	if got := f.startCalls(); len(got) != 1 || got[0] != `C:\Program Files\Everything\Everything.exe` {
		t.Fatalf("starts = %v", got)
	}
	// Once it runs, an open search view is pulled again.
	if got := h.Invalidated(); !slices.Equal(got, []string{cmdSearch}) {
		t.Fatalf("invalidated = %q", got)
	}

	// Still not running (e.g. the start failed): a single item to start it.
	f.setRunning(false)
	v := view(t, p, "report")
	its := items(v)
	if len(its) != 1 || its[0].Title != "Everything is not running" || its[0].Subtitle != "Press Enter to start Everything" {
		t.Fatalf("items = %+v", its)
	}
	act := its[0].Actions[0]
	if act.Title != "Start Everything" || act.Do.Kind != plugin.ActionCallback || act.Do.Target != callbackStart {
		t.Fatalf("action = %+v", act)
	}
	if its := items(view(t, p, "")); len(its) != 1 || its[0].ID != "not-running" {
		t.Fatalf("empty query items = %+v", its)
	}

	res, err := p.HandleAction(t.Context(), act.Do)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Refresh || res.Toast == nil || res.Toast.Style != plugin.ToastSuccess || res.Toast.Title != "Everything started" {
		t.Fatalf("result = %+v", res)
	}
	if len(f.startCalls()) != 2 || !f.Running() {
		t.Fatalf("starts = %v", f.startCalls())
	}
	_ = h
}

func TestStartRunningOnInitIsSkipped(t *testing.T) {
	f := &fake{installed: true, running: true}
	p, h := start(t, f)
	p.wg.Wait()
	if len(f.startCalls()) != 0 || len(h.Invalidated()) != 0 {
		t.Fatal("started a running Everything")
	}
	// Starting when already running just refreshes.
	res, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackStart})
	if err != nil || !res.Refresh || len(f.startCalls()) != 0 {
		t.Fatalf("result = %+v, %v", res, err)
	}
}

func TestStartFailure(t *testing.T) {
	f := &fake{installed: true, startErr: errors.New("access denied")}
	p, _ := start(t, f)
	p.wg.Wait()
	res, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackStart})
	if err != nil {
		t.Fatal(err)
	}
	if res.Toast == nil || res.Toast.Style != plugin.ToastFailure || res.Toast.Title != "Could not start Everything" || res.Toast.Message != "access denied" {
		t.Fatalf("result = %+v", res)
	}

	// Started, but the window never appears.
	f.mu.Lock()
	f.startErr = nil
	f.mu.Unlock()
	nf := &neverUp{fake: f}
	p.b = nf
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	res, err = p.HandleAction(ctx, plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackStart})
	if err != nil || res.Toast == nil || res.Toast.Style != plugin.ToastFailure {
		t.Fatalf("result = %+v, %v", res, err)
	}
}

// neverUp is a backend whose Everything never starts running.
type neverUp struct{ *fake }

func (*neverUp) Running() bool { return false }

func TestUnknownAction(t *testing.T) {
	p, _ := start(t, &fake{installed: true, running: true})
	if _, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: "nope"}); err == nil {
		t.Fatal("no error")
	}
	if _, err := p.View(t.Context(), plugin.ViewRequest{ID: "nope"}); err == nil {
		t.Fatal("no error for an unknown view")
	}
}

func TestViewEmptyQuery(t *testing.T) {
	f := &fake{installed: true, running: true, results: sample()}
	p, _ := start(t, f)
	v := view(t, p, "   ")
	if len(items(v)) != 0 || v.EmptyText != "Type to search files with Everything" || v.Title != "Search Files" || !v.ShowDetail {
		t.Fatalf("view = %+v", v)
	}
	if len(f.queries) != 0 {
		t.Fatal("searched for an empty query")
	}
}

func TestViewResults(t *testing.T) {
	f := &fake{installed: true, running: true, results: sample()}
	p, _ := start(t, f)
	v := view(t, p, " report ")
	if q := f.queries[0]; !strings.HasPrefix(q.Text, `report !"c:\windows\" `) || q.Max != poolMax || q.MatchPath {
		t.Fatalf("query = %+v", q)
	}
	if v.EmptyText != "No matching files" || v.Placeholder != "Search files and folders…" {
		t.Errorf("view = %+v", v)
	}
	its := items(v)
	if len(its) != 3 {
		t.Fatalf("items = %+v", its)
	}

	file := its[0]
	if file.ID != `C:\Users\demo\Documents\report.pdf` || file.Title != "report.pdf" || file.Subtitle != `~\Documents` ||
		file.Icon.Name != "file" || file.Accessory != "2 hours ago" || file.Score != 0 {
		t.Errorf("file item = %+v", file)
	}
	wantMeta := []plugin.Metadata{
		{Label: "Path", Value: `C:\Users\demo\Documents\report.pdf`},
		{Label: "Kind", Value: "File"},
		{Label: "Size", Value: "1.5 KB"},
		{Label: "Modified", Value: "2026-10-14 08:00 (2 hours ago)"},
	}
	if file.Detail == nil || !slices.Equal(file.Detail.Metadata, wantMeta) {
		t.Errorf("metadata = %+v", file.Detail)
	}

	type act struct{ id, title, shortcut string }
	var got []act
	for _, a := range file.Actions {
		got = append(got, act{a.ID, a.Title, a.Shortcut})
	}
	want := []act{
		{"open", "Open", ""},
		{"reveal", "Show in File Explorer", "Ctrl+Shift+F"},
		{"copy-path", "Copy Path", "Ctrl+Shift+C"},
		{"copy-file", "Copy File", "Ctrl+Alt+C"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("actions = %v", got)
	}
	a := file.Actions
	if a[0].Do.Kind != plugin.ActionOpen || a[0].Do.Target != file.ID || a[0].Do.Payload != nil {
		t.Errorf("open = %+v", a[0].Do)
	}
	var opts plugin.OpenOptions
	if err := json.Unmarshal(a[1].Do.Payload, &opts); err != nil || !opts.Reveal || a[1].Do.Kind != plugin.ActionOpen || a[1].Do.Target != file.ID {
		t.Errorf("reveal = %+v", a[1].Do)
	}
	if a[2].Do.Kind != plugin.ActionCopyText || a[2].Do.Target != file.ID {
		t.Errorf("copy path = %+v", a[2].Do)
	}
	var files []string
	if err := json.Unmarshal(a[3].Do.Payload, &files); err != nil || a[3].Do.Kind != plugin.ActionCopyFiles || !slices.Equal(files, []string{file.ID}) {
		t.Errorf("copy file = %+v", a[3].Do)
	}

	folder := its[1]
	if folder.Icon.Name != "folder" || folder.Subtitle != `D:\` || folder.Accessory != "" || len(folder.Detail.Metadata) != 2 || folder.Detail.Metadata[1].Value != "Folder" {
		t.Errorf("folder item = %+v", folder)
	}
	if root := its[2]; root.Subtitle != "" || root.ID != `C:\` {
		t.Errorf("root item = %+v", root)
	}
}

func TestViewPreferences(t *testing.T) {
	f := &fake{installed: true, running: true}
	for range 30 {
		f.results = append(f.results, sample()[0])
	}
	p, h := start(t, f)
	h.Prefs.Set(prefMaxResults, jsontext.Value("10"))
	h.Prefs.Set(prefMatchPath, jsontext.Value("true"))
	if its := items(view(t, p, "report")); len(its) != 10 {
		t.Fatalf("got %d items, want 10", len(its))
	}
	if q := f.queries[0]; q.Max != poolMax || !q.MatchPath {
		t.Fatalf("query = %+v", q)
	}
	for v, want := range map[string]int{"0": defaultMax, "-5": defaultMax, "5000": limitMax} {
		h.Prefs.Set(prefMaxResults, jsontext.Value(v))
		if got := p.maxResults(); got != want {
			t.Errorf("maxResults(%s) = %d, want %d", v, got, want)
		}
	}
}

func TestViewSearchError(t *testing.T) {
	f := &fake{installed: true, running: true, searchErr: errors.New("boom")}
	p, _ := start(t, f)
	v := view(t, p, "x")
	if len(items(v)) != 0 || v.EmptyText != "Search failed: boom" {
		t.Fatalf("view = %+v", v)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f.searchErr = context.Canceled
	if _, err := p.View(ctx, plugin.ViewRequest{ID: cmdSearch, Query: plugin.Query{Text: "x"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled view err = %v", err)
	}
}

func TestRootItems(t *testing.T) {
	f := &fake{installed: true, running: true}
	for range 8 {
		f.results = append(f.results, sample()...)
	}
	p, h := start(t, f)
	root := func(text string) []plugin.Item {
		t.Helper()
		its, err := p.RootItems(t.Context(), plugin.Query{Text: text})
		if err != nil {
			t.Fatal(err)
		}
		return its
	}
	if its := root("report"); its != nil {
		t.Fatalf("showInRoot off: %+v", its)
	}
	h.Prefs.Set(prefShowInRoot, jsontext.Value("true"))
	for _, short := range []string{"", "re", " ab ", "文件"} {
		if its := root(short); its != nil {
			t.Errorf("%q: %+v", short, its)
		}
	}
	if len(f.queries) != 0 {
		t.Fatalf("searched for short queries: %v", f.queries)
	}
	its := root("rep")
	if len(its) != rootMax || f.queries[0].Max != rootPool || !strings.Contains(f.queries[0].Text, `!"\node_modules\"`) {
		t.Fatalf("got %d items, query %+v", len(its), f.queries[0])
	}
	for _, it := range its {
		if it.Score != rootScore || it.Group != "Files" || it.Detail != nil || len(it.Actions) != 4 {
			t.Errorf("root item = %+v", it)
		}
	}
	if its := root("项目计划"); len(its) != rootMax {
		t.Errorf("CJK query: %d items", len(its))
	}

	f.setRunning(false)
	if its, err := p.RootItems(t.Context(), plugin.Query{Text: "report"}); err != nil || its != nil {
		t.Errorf("not running: %v, %v", its, err)
	}
	f.setRunning(true)
	f.searchErr = errors.New("boom")
	if _, err := p.RootItems(t.Context(), plugin.Query{Text: "report"}); err == nil {
		t.Error("no error")
	}
}

func TestFixtureBackend(t *testing.T) {
	b := FixtureBackend(fixturePath)
	if exe, ok := b.Installed(); !ok || exe == "" || !b.Running() || b.Start(exe) != nil {
		t.Fatal("fixture backend is not available")
	}
	all, err := b.Search(t.Context(), everything.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 15 {
		t.Fatalf("fixture has %d entries", len(all))
	}
	for _, r := range all {
		if r.Name == "" || !strings.HasSuffix(r.Path, r.Name) && r.Name != "C:" {
			t.Errorf("bad fixture entry %+v", r)
		}
	}
	res, _ := b.Search(t.Context(), everything.Query{Text: "Quarterly report"})
	if len(res) != 2 {
		t.Errorf("Quarterly report: %+v", res)
	}
	res, _ = b.Search(t.Context(), everything.Query{Text: "项目"})
	if len(res) != 1 || res[0].Name != "项目计划书.docx" {
		t.Errorf("项目: %+v", res)
	}
	res, _ = b.Search(t.Context(), everything.Query{Text: `工作\`, MatchPath: true})
	if len(res) != 2 {
		t.Errorf("match path: %+v", res)
	}
	res, _ = b.Search(t.Context(), everything.Query{Text: "e", Max: 3, Offset: 2})
	if len(res) != 3 {
		t.Errorf("max/offset: %d", len(res))
	}

	if _, err := FixtureBackend(filepath.Join(t.TempDir(), "missing.json")).Search(t.Context(), everything.Query{}); err == nil {
		t.Error("missing fixture: no error")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte("{"), 0o644)
	if _, err := FixtureBackend(bad).Search(t.Context(), everything.Query{}); err == nil {
		t.Error("bad fixture: no error")
	}

	// Through the plugin.
	p, _ := start(t, b)
	p.home = `C:\Users\demo`
	its := items(view(t, p, "notepad"))
	if len(its) != 2 || its[0].Subtitle != `C:\Windows\System32` {
		t.Fatalf("items = %+v", its)
	}
	its = items(view(t, p, "工作"))
	if len(its) != 1 || its[0].Icon.Name != "folder" || its[0].Subtitle != `~\Documents` {
		t.Fatalf("items = %+v", its)
	}
}

// TestRealEverything searches with the Everything of this machine.
func TestRealEverything(t *testing.T) {
	if testing.Short() || !everything.Running() {
		t.Skip("Everything is not running")
	}
	p := New()
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	its := items(view(t, p, "notepad.exe"))
	if len(its) == 0 || !strings.EqualFold(its[0].Title, "notepad.exe") || its[0].Detail == nil {
		t.Fatalf("items = %+v", its)
	}
	t.Logf("%d results, first %q in %q", len(its), its[0].Title, its[0].Subtitle)

	// R20: system files rank last.
	its = items(view(t, p, "notepad"))
	if len(its) == 0 || strings.Contains(strings.ToLower(its[0].ID), `\winsxs\`) {
		t.Fatalf("first result for notepad: %+v", its)
	}
	t.Logf("notepad: first %s", its[0].ID)

	// Latency of the view, typing as a user would and searching whole words.
	// Typed one character at a time, and whole words (pasted). The baseline
	// is the single plain query used before R20.
	words := []string{"report", "notepad", "文档", "readme", "main.go", "setup", "invoice", "简历"}
	var typed, pasted, base []time.Duration
	measure := func(q string) time.Duration {
		start := time.Now()
		view(t, p, q)
		return time.Since(start)
	}
	for range 3 {
		for _, w := range words {
			r := []rune(w)
			for i := range r {
				typed = append(typed, measure(string(r[:i+1])))
				start := time.Now()
				if _, err := everything.Search(t.Context(), everything.Query{Text: string(r[:i+1]), Max: defaultMax}); err != nil {
					t.Fatal(err)
				}
				base = append(base, time.Since(start))
			}
		}
		for _, w := range words {
			view(t, p, "zz") // forget the previous query
			pasted = append(pasted, measure(w))
		}
	}
	pct := func(ds []time.Duration) string {
		slices.Sort(ds)
		return fmt.Sprintf("p50 %v, p95 %v (n=%d)", ds[len(ds)/2].Round(100*time.Microsecond), ds[len(ds)*95/100].Round(100*time.Microsecond), len(ds))
	}
	t.Logf("view latency typing: %s; pasted words: %s; one plain query (before R20): %s", pct(typed), pct(pasted), pct(base))
	if typed[len(typed)*95/100] > time.Second {
		t.Errorf("typing p95 %v", typed[len(typed)*95/100])
	}
}

func TestHelpers(t *testing.T) {
	for in, want := range map[string]string{
		`C:\a\b.txt`: `C:\a`,
		`C:\a`:       `C:\`,
		`C:\`:        "",
		`\\srv\s\x`:  `\\srv\s`,
		"x":          "",
	} {
		if got := parentDir(in); got != want {
			t.Errorf("parentDir(%q) = %q, want %q", in, got, want)
		}
	}
	p := &Plugin{home: `C:\Users\Demo\`}
	for in, want := range map[string]string{
		`C:\Users\demo`:    "~",
		`c:\users\demo\x`:  `~\x`,
		`C:\Users\demo2\x`: `C:\Users\demo2\x`,
		`D:\x`:             `D:\x`,
		`C:\Users`:         `C:\Users`,
	} {
		if got := p.shorten(in); got != want {
			t.Errorf("shorten(%q) = %q, want %q", in, got, want)
		}
	}
	if (&Plugin{}).shorten(`C:\x`) != `C:\x` {
		t.Error("shorten without a home")
	}

	_, h := start(t, &fake{installed: true, running: true})
	l := h.I18n()
	for n, want := range map[int64]string{0: "0 bytes", 1: "1 byte", 1023: "1023 bytes", 1536: "1.5 KB", 5 << 20: "5 MB", 3 << 30: "3 GB"} {
		if got := formatSize(l, n); got != want {
			t.Errorf("formatSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestManifest(t *testing.T) {
	p := New()
	m := p.Manifest()
	if m.ID != ID || m.Icon.Name != "file" || !slices.Equal(m.Permissions, []plugin.Permission{plugin.PermExec}) {
		t.Errorf("manifest = %+v", m)
	}
	if len(m.Commands) != 1 || m.Commands[0].Name != cmdSearch || m.Commands[0].Mode != plugin.ModeView || m.Commands[0].DefaultHotkey != "" {
		t.Errorf("commands = %+v", m.Commands)
	}
	var prefs []string
	for _, pr := range m.Preferences {
		prefs = append(prefs, pr.Name+"="+string(pr.Default))
	}
	if !slices.Equal(prefs, []string{"maxResults=100", "matchPath=false", "showInRoot=false", "hideSystemFiles=false", "extraLowPriorityPaths=[]"}) {
		t.Errorf("preferences = %v", prefs)
	}
}

// TestI18nKeys checks that every key used by the code and the manifest
// exists in en.json and that every key there is used.
func TestI18nKeys(t *testing.T) {
	b, err := os.ReadFile("locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog map[string]jsontext.Value
	if err := json.Unmarshal(b, &catalog); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("everything.go")
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`\.(?:T|N)\(\s*"([A-Za-z0-9_.\-]+)"`),
		regexp.MustCompile(`"%([A-Za-z0-9_.\-]+)%"`),
	} {
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			used[m[1]] = true
		}
	}
	for k := range used {
		if _, ok := catalog[k]; !ok {
			t.Errorf("key %q is missing from en.json", k)
		}
	}
	for k := range catalog {
		if !used[k] {
			t.Errorf("key %q is not used", k)
		}
	}
}
