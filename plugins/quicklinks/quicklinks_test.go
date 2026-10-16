package quicklinks

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/winapi/shell"
	"github.com/eziq1/golauncher/pkg/plugin"
)

type env struct {
	t   *testing.T
	p   *Plugin
	h   *hostfake.Host
	dir string
}

func newEnv(t *testing.T, o Options) *env {
	t.Helper()
	dir := t.TempDir()
	if o.Browsers == nil {
		o.Browsers = func() []shell.Browser { return nil }
	}
	if o.Client == nil && !o.Offline {
		o.Offline = true
	}
	o.Sync = true
	p := NewWithOptions(o)
	h, err := hostfake.New(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	h.Clock = func() time.Time { return fixedNow }
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return &env{t: t, p: p, h: h, dir: dir}
}

// reopen returns a new plugin instance on the same storage.
func (e *env) reopen() *env {
	e.t.Helper()
	p := NewWithOptions(Options{Offline: true, Sync: true, Browsers: func() []shell.Browser { return nil }})
	if err := p.Init(e.t.Context(), e.h); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { p.Close() })
	return &env{t: e.t, p: p, h: e.h, dir: e.dir}
}

func (e *env) root(text string) []plugin.Item {
	e.t.Helper()
	items, err := e.p.RootItems(e.t.Context(), plugin.Query{Text: text})
	if err != nil {
		e.t.Fatal(err)
	}
	return items
}

func (e *env) fallback(text string) []plugin.Item {
	e.t.Helper()
	items, err := e.p.FallbackItems(e.t.Context(), plugin.Query{Text: text})
	if err != nil {
		e.t.Fatal(err)
	}
	return items
}

func (e *env) act(do plugin.ActionDo, vals map[string]any) plugin.Result {
	e.t.Helper()
	if len(vals) > 0 {
		do.Values = map[string]jsontext.Value{}
		for k, v := range vals {
			do.Values[k] = plugin.MustPayload(v)
		}
	}
	r, err := e.p.HandleAction(e.t.Context(), do)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) create(name, link, keyword string) Quicklink {
	e.t.Helper()
	r := e.act(callback(cbSave, idPayload{}), map[string]any{fieldName: name, fieldLink: link, fieldKeyword: keyword})
	if len(r.FieldErrors) > 0 {
		e.t.Fatalf("create %s: %v", name, r.FieldErrors)
	}
	links := e.p.linkList()
	return links[len(links)-1]
}

func ids(items []plugin.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func find(t *testing.T, items []plugin.Item, id string) plugin.Item {
	t.Helper()
	for _, it := range items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no item %q in %v", id, ids(items))
	return plugin.Item{}
}

func TestKeywordSearch(t *testing.T) {
	e := newEnv(t, Options{Browsers: func() []shell.Browser { return []shell.Browser{{Name: "Firefox", Path: `C:\ff\firefox.exe`}} }})
	items := e.root("g golang generics")
	it := find(t, items, "search:google")
	if it.Title != "Search Google for “golang generics”" || it.Score != 1 || !it.Transient || it.Group != "Web Search" {
		t.Fatalf("item %+v", it)
	}
	if d := it.Actions[0].Do; d.Kind != plugin.ActionOpen || d.Target != "https://www.google.com/search?q=golang%20generics" || d.Payload != nil {
		t.Errorf("open: %+v", it.Actions[0].Do)
	}
	if it.Actions[1].Do.Kind != plugin.ActionCopyText || it.Actions[1].Do.Target != it.Subtitle {
		t.Errorf("copy: %+v", it.Actions[1])
	}
	if b := it.Actions[2]; b.Title != "Open in Firefox" || b.Do.Target != `C:\ff\firefox.exe` {
		t.Errorf("browser: %+v", b)
	}
	if got := find(t, e.root("bd 北京 天气"), "search:baidu").Subtitle; got != "https://www.baidu.com/s?ie=utf-8&wd=%E5%8C%97%E4%BA%AC%20%E5%A4%A9%E6%B0%94" {
		t.Errorf("baidu: %s", got)
	}
	// Not keywords: no search items.
	for _, q := range []string{"gh", "good morning", "百度网盘", "golang"} {
		for _, it := range e.root(q) {
			if strings.HasPrefix(it.ID, "search:") {
				t.Errorf("%q: %s", q, it.ID)
			}
		}
	}
	// A keyword alone: the engine with an inline argument, matched by the host.
	it = find(t, e.root("g"), "engine:google")
	if it.Score != 0 || len(it.Arguments) != 1 || it.Transient {
		t.Errorf("engine item %+v", it)
	}
	find(t, e.root("wik"), "engine:wikipedia") // by name
	r := e.act(it.Actions[0].Do, map[string]any{argQuery: "c# & go"})
	if r.Do == nil || r.Do.Target != "https://www.google.com/search?q=c%23%20%26%20go" {
		t.Errorf("search with argument: %+v", r.Do)
	}
	if r := e.act(it.Actions[0].Do, nil); r.Do == nil || r.Do.Target != "https://www.google.com/" {
		t.Errorf("empty argument: %+v", r.Do)
	}
	// Chinese UI: Chinese names, zh Wikipedia.
	e.h.I18N.SetLocale("zh-CN")
	it = find(t, e.root("wiki 长城"), "search:wikipedia")
	if it.Title != "用 维基百科 搜索“长城”" || !strings.HasPrefix(it.Subtitle, "https://zh.wikipedia.org/") {
		t.Errorf("zh: %+v", it)
	}
}

func TestTypedURL(t *testing.T) {
	e := newEnv(t, Options{})
	it := find(t, e.root("github.com/foo"), "url")
	if it.Title != "Open github.com/foo" || it.Actions[0].Do.Target != "https://github.com/foo" || !it.Transient || it.Score != 1 {
		t.Errorf("url item %+v", it)
	}
	for _, q := range []string{"1.5", "file.txt", "hello world"} {
		if slices.Contains(ids(e.root(q)), "url") {
			t.Errorf("%q detected as a URL", q)
		}
	}
	e.h.Prefs.Set(prefOpenURLs, plugin.MustPayload(false))
	if slices.Contains(ids(e.root("github.com/foo")), "url") {
		t.Error("URL offered with the preference off")
	}
}

func TestFallback(t *testing.T) {
	e := newEnv(t, Options{})
	items := e.fallback("why is the sky blue")
	if got := ids(items); !slices.Equal(got, []string{"fallback:google"}) {
		t.Fatalf("fallback = %v", got)
	}
	if items[0].Actions[0].Do.Target != "https://www.google.com/search?q=why%20is%20the%20sky%20blue" || items[0].Score != 0 {
		t.Errorf("fallback item %+v", items[0])
	}
	// Chinese UI: Bing.
	e.h.I18N.SetLocale("zh-CN")
	if got := ids(e.fallback("天空")); !slices.Equal(got, []string{"fallback:bing"}) {
		t.Errorf("zh fallback = %v", got)
	}
	e.h.I18N.SetLocale("en")
	// Chosen default and second engine.
	e.h.Prefs.Set(prefDefault, plugin.MustPayload("duckduckgo"))
	e.h.Prefs.Set(prefSecond, plugin.MustPayload("baidu"))
	if got := ids(e.fallback("x")); !slices.Equal(got, []string{"fallback:duckduckgo", "fallback:baidu"}) {
		t.Errorf("chosen = %v", got)
	}
	// The same engine twice is listed once; a disabled default falls back.
	e.h.Prefs.Set(prefSecond, plugin.MustPayload("duckduckgo"))
	e.act(callback(cbToggle, idPayload{ID: "duckduckgo"}), nil)
	if got := ids(e.fallback("x")); !slices.Equal(got, []string{"fallback:google"}) {
		t.Errorf("disabled default = %v", got)
	}
	e.h.Prefs.Set(prefFallback, plugin.MustPayload(false))
	if got := e.fallback("x"); len(got) != 0 {
		t.Errorf("fallback off = %v", ids(got))
	}
}

func TestPreferenceOptions(t *testing.T) {
	e := newEnv(t, Options{})
	opts, err := e.p.PreferenceOptions(t.Context(), prefDefault)
	if err != nil || opts[0] != (plugin.Option{Value: "", Title: "Automatic (Google)"}) || len(opts) != len(builtins)+1 {
		t.Fatalf("options %v %v", opts, err)
	}
	e.act(callback(cbToggle, idPayload{ID: "taobao"}), nil)
	opts, _ = e.p.PreferenceOptions(t.Context(), prefSecond)
	if opts[0].Title != "None" || slices.ContainsFunc(opts, func(o plugin.Option) bool { return o.Value == "taobao" }) {
		t.Errorf("second options %v", opts)
	}
}

func TestQuicklinkCRUD(t *testing.T) {
	e := newEnv(t, Options{})
	q := e.create("Go Standard Library", "https://pkg.go.dev/std", "std")
	if q.ID != "1" || !q.Created.Equal(fixedNow) {
		t.Fatalf("created %+v", q)
	}
	it := find(t, e.root("go standard"), "link:1")
	if it.Group != "Quicklinks" || it.Score != 0 || it.Transient || !slices.Equal(it.Keywords, []string{"std"}) {
		t.Errorf("root item %+v", it)
	}
	if d := it.Actions[0].Do; d.Kind != plugin.ActionOpen || d.Target != "https://pkg.go.dev/std" || d.Payload != nil {
		t.Errorf("open %+v", it.Actions[0].Do)
	}
	if a := it.Actions[4]; a.ID != "delete" || a.Style != plugin.StyleDestructive {
		t.Errorf("delete %+v", a)
	}
	// Validation.
	r := e.act(callback(cbSave, idPayload{}), map[string]any{fieldName: " ", fieldLink: "", fieldKeyword: "g"})
	if r.FieldErrors[fieldName] != "Required" || r.FieldErrors[fieldLink] != "Required" || r.FieldErrors[fieldKeyword] != "“g” is already used by Google" {
		t.Errorf("errors %v", r.FieldErrors)
	}
	r = e.act(callback(cbSave, idPayload{}), map[string]any{fieldName: "x", fieldLink: "y", fieldKeyword: "std"})
	if r.FieldErrors[fieldKeyword] != "“std” is already used by Go Standard Library" {
		t.Errorf("errors %v", r.FieldErrors)
	}
	// Edit (its own keyword is fine).
	r = e.act(callback(cbSave, idPayload{ID: "1"}), map[string]any{fieldName: "Std", fieldLink: "https://pkg.go.dev/std", fieldKeyword: "STD", fieldOpenWith: "firefox.exe"})
	if len(r.FieldErrors) > 0 || !r.PopToRoot {
		t.Fatalf("edit %+v", r)
	}
	q, _ = e.p.findLink("1")
	if q.Name != "Std" || q.Keyword != "std" || q.OpenWith != "firefox.exe" || !q.Created.Equal(fixedNow) {
		t.Errorf("edited %+v", q)
	}
	// Duplicate.
	e.act(callback(cbDuplicate, idPayload{ID: "1"}), nil)
	links := e.p.linkList()
	if len(links) != 2 || links[1].Name != "Std (copy)" || links[1].Keyword != "" || links[1].ID != "2" {
		t.Errorf("duplicate %+v", links)
	}
	// Storage round trip.
	e2 := e.reopen()
	if got := e2.p.linkList(); !slices.EqualFunc(got, links, func(a, b Quicklink) bool { return a.ID == b.ID && a.Name == b.Name && a.Link == b.Link }) {
		t.Errorf("reloaded %+v", got)
	}
	// Delete.
	r = e2.act(callback(cbDelete, idPayload{ID: "1"}), nil)
	if r.Toast == nil || r.Toast.Title != "Deleted Std" || len(e2.p.linkList()) != 1 {
		t.Errorf("delete %+v", r)
	}
	if r := e2.act(callback(cbDelete, idPayload{ID: "1"}), nil); r.Toast.Style != plugin.ToastFailure {
		t.Errorf("delete twice %+v", r)
	}
	e2.create("Third", "https://example.com", "")
	if got := e2.p.linkList(); got[len(got)-1].ID != "3" {
		t.Errorf("IDs are not reused: %+v", got)
	}
}

func TestQuicklinkPlaceholders(t *testing.T) {
	e := newEnv(t, Options{})
	e.h.Clip.Set(plugin.ClipboardContent{Text: "hello world"})
	e.create("Jira", "https://jira.example.com/browse/{query}", "jira")
	e.create("Translate", "https://translate.example.com/?text={clipboard}", "")
	e.create("Diary", `D:\diary\{date:yyyyMMdd}.md`, "")
	// {query}: an inline argument; the open action is a callback.
	it := find(t, e.root("jira"), "link:1")
	if len(it.Arguments) != 1 || !it.Arguments[0].Required || it.Actions[0].Do.Kind != plugin.ActionCallback {
		t.Fatalf("jira %+v", it)
	}
	r := e.act(it.Actions[0].Do, map[string]any{argQuery: "ABC-123"})
	if r.Do == nil || r.Do.Target != "https://jira.example.com/browse/ABC-123" {
		t.Errorf("jira open %+v", r.Do)
	}
	// "jira abc 1": a pre-scored item opening the URL directly.
	it = find(t, e.root("jira abc 1"), "linkquery:1")
	if it.Title != "Jira: abc 1" || it.Score != 1 || !it.Transient || it.Actions[0].Do.Target != "https://jira.example.com/browse/abc%201" {
		t.Errorf("jira query %+v", it)
	}
	// {clipboard} and {date} are filled in when the action runs.
	for id, want := range map[string]string{"link:2": "https://translate.example.com/?text=hello%20world", "link:3": `D:\diary\20260305.md`} {
		it := find(t, e.root("x"), id)
		if it.Actions[0].Do.Kind != plugin.ActionCallback || strings.Contains(string(it.Actions[0].Do.Payload), "hello") {
			t.Errorf("%s: %+v", id, it.Actions[0])
		}
		if r := e.act(it.Actions[0].Do, nil); r.Do == nil || r.Do.Target != want {
			t.Errorf("%s: %+v", id, r.Do)
		}
	}
}

func TestEngines(t *testing.T) {
	e := newEnv(t, Options{})
	// Disable a built-in engine: its keyword stops working.
	e.act(callback(cbToggle, idPayload{ID: "taobao"}), nil)
	if slices.Contains(ids(e.root("tb shoes")), "search:taobao") {
		t.Error("disabled engine searched")
	}
	// Change keywords of a built-in engine.
	r := e.act(callback(cbSaveEngine, idPayload{ID: "google"}), map[string]any{fieldKeywords: "gg, 谷歌"})
	if len(r.FieldErrors) > 0 {
		t.Fatal(r.FieldErrors)
	}
	if slices.Contains(ids(e.root("g x")), "search:google") || !slices.Contains(ids(e.root("gg x")), "search:google") {
		t.Error("keywords not changed")
	}
	// A keyword used elsewhere is refused.
	r = e.act(callback(cbSaveEngine, idPayload{ID: "google"}), map[string]any{fieldKeywords: "gg bd"})
	if r.FieldErrors[fieldKeywords] != "“bd” is already used by Baidu" {
		t.Errorf("errors %v", r.FieldErrors)
	}
	// Custom engine.
	r = e.act(callback(cbSaveEngine, idPayload{}), map[string]any{fieldName: "Go Source", fieldKeywords: "gocs", fieldURL: "https://cs.opensource.google/search?q={query}&ss=go"})
	if len(r.FieldErrors) > 0 {
		t.Fatal(r.FieldErrors)
	}
	it := find(t, e.root("gocs net/http"), "search:custom1")
	if it.Subtitle != "https://cs.opensource.google/search?q=net%2Fhttp&ss=go" {
		t.Errorf("custom %+v", it)
	}
	r = e.act(callback(cbSaveEngine, idPayload{}), map[string]any{fieldName: "Bad", fieldKeywords: "", fieldURL: "https://example.com/search", fieldSuggest: "ftp://x"})
	if r.FieldErrors[fieldURL] == "" || r.FieldErrors[fieldSuggest] == "" {
		t.Errorf("errors %v", r.FieldErrors)
	}
	// Set as default: stored as the preference.
	r = e.act(callback(cbSetDefault, idPayload{ID: "custom1"}), nil)
	if string(r.Preferences[prefDefault]) != `"custom1"` {
		t.Errorf("set default %+v", r)
	}
	// The view: custom first, the default marked.
	e.h.Prefs.Set(prefDefault, plugin.MustPayload("custom1"))
	v, err := e.p.View(t.Context(), plugin.ViewRequest{ID: cmdEngines})
	if err != nil {
		t.Fatal(err)
	}
	if v.Sections[0].Title != "Your Search Engines" || v.Sections[0].Items[0].Accessory != "Default" {
		t.Errorf("view %+v", v.Sections[0])
	}
	if it := find(t, v.Sections[1].Items, "engine:taobao"); it.Accessory != "Disabled" || it.Actions[0].Title != "Enable" {
		t.Errorf("taobao %+v", it)
	}
	// Persisted; reset; delete.
	e2 := e.reopen()
	if g, _ := e2.p.engineByID("google"); !slices.Equal(g.Keywords, []string{"gg", "谷歌"}) {
		t.Errorf("reloaded %+v", g)
	}
	e2.act(callback(cbReset, idPayload{ID: "google"}), nil)
	if g, _ := e2.p.engineByID("google"); !slices.Equal(g.Keywords, []string{"g", "谷歌"}) {
		t.Errorf("reset %+v", g)
	}
	e2.act(callback(cbDeleteEngine, idPayload{ID: "custom1"}), nil)
	if _, ok := e2.p.engineByID("custom1"); ok {
		t.Error("not deleted")
	}
	if b, _, _ := e2.h.KV.Get(keyEngines); !strings.Contains(string(b), `"taobao":{"disabled":true}`) {
		t.Errorf("stored %s", b)
	}
}

func TestImportExport(t *testing.T) {
	e := newEnv(t, Options{})
	e.create("Std", "https://pkg.go.dev/std", "std")
	e.create("Jira", "https://jira.example.com/browse/{query}", "jira")
	e.act(callback(cbToggle, idPayload{ID: "taobao"}), nil)
	e.act(callback(cbSaveEngine, idPayload{}), map[string]any{fieldName: "Go Source", fieldKeywords: "gocs", fieldURL: "https://cs.opensource.google/search?q={query}"})
	path := filepath.Join(e.dir, "out.json")
	r := e.act(callback(cbExport, idPayload{}), map[string]any{fieldFile: path})
	if r.Toast == nil || r.Toast.Style != plugin.ToastSuccess {
		t.Fatalf("export %+v", r)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f exportFile
	if err := json.Unmarshal(data, &f); err != nil || f.Golauncher != "quicklinks" || len(f.Quicklinks) != 2 || len(f.SearchEngines) != 1 ||
		len(f.BuiltinEngines) != 1 || f.BuiltinEngines[0].ID != "taobao" || *f.BuiltinEngines[0].Enabled {
		t.Fatalf("exported %s (%v)", data, err)
	}
	// Import into a fresh plugin: the same data.
	e2 := newEnv(t, Options{})
	r = e2.act(callback(cbImport, idPayload{}), map[string]any{fieldFile: path})
	if r.Toast == nil || r.Toast.Message != "Quicklinks: 2, search engines: 2, skipped: 0." {
		t.Fatalf("import %+v", r)
	}
	if e2.p.exportJSON() != e.p.exportJSON() {
		t.Errorf("round trip:\n%s\n%s", e2.p.exportJSON(), e.p.exportJSON())
	}
	// Importing again skips the same quicklinks and updates the engine.
	r = e2.act(callback(cbImport, idPayload{}), map[string]any{fieldJSON: string(data)})
	if r.Toast.Message != "Quicklinks: 0, search engines: 2, skipped: 2." || len(e2.p.linkList()) != 2 {
		t.Errorf("again %+v", r.Toast)
	}
	// Keywords in use are dropped; Raycast's format is accepted.
	r = e2.act(callback(cbImport, idPayload{}), map[string]any{fieldJSON: `[{"name":"Mail","link":"mailto:{query}","keyword":"g"},{"name":"","link":"x"}]`})
	if r.Toast.Message != "Quicklinks: 1, search engines: 0, skipped: 1. 1 keyword was already in use and was left out." {
		t.Errorf("raycast %+v", r.Toast)
	}
	// Errors change nothing.
	before := e2.p.exportJSON()
	for _, bad := range []string{`{"foo": 1}`, `not json`, `[1, 2]`} {
		r = e2.act(callback(cbImport, idPayload{}), map[string]any{fieldJSON: bad})
		if r.FieldErrors[fieldJSON] != "This is not a quicklinks file" {
			t.Errorf("%s: %+v", bad, r)
		}
	}
	r = e2.act(callback(cbImport, idPayload{}), map[string]any{fieldFile: filepath.Join(e.dir, "missing.json")})
	if !strings.HasPrefix(r.FieldErrors[fieldFile], "Could not read the file") {
		t.Errorf("missing file %+v", r)
	}
	if e2.p.exportJSON() != before {
		t.Error("a failed import changed the data")
	}
}

func TestCorruptStorage(t *testing.T) {
	e := newEnv(t, Options{})
	e.h.KV.Set(keyLinks, []byte("{broken"))
	e.h.KV.Set(keyEngines, []byte("[]"))
	e2 := e.reopen()
	if len(e2.p.linkList()) != 0 || len(e2.p.engineList()) != len(builtins) {
		t.Error("corrupt data not treated as empty")
	}
	if b, _, _ := e2.h.KV.Get(keyLinks); string(b) != "{broken" {
		t.Error("corrupt data overwritten before a change")
	}
	e2.create("A", "https://a.example.com", "")
	if len(e2.reopen().p.linkList()) != 1 {
		t.Error("not saved after a change")
	}
}

func TestViews(t *testing.T) {
	e := newEnv(t, Options{})
	v, err := e.p.View(t.Context(), plugin.ViewRequest{ID: cmdLinks})
	if err != nil || len(v.Sections) != 1 || v.Sections[0].Title != "Manage" {
		t.Fatalf("empty list %+v %v", v, err)
	}
	e.create("Std", "https://pkg.go.dev/std", "")
	v, _ = e.p.View(t.Context(), plugin.ViewRequest{ID: cmdLinks})
	if !v.FilterLocally || v.Sections[0].Items[0].ID != "link:1" || v.Sections[0].Items[0].Group != "" {
		t.Errorf("list %+v", v.Sections[0])
	}
	v, err = e.p.View(t.Context(), plugin.ViewRequest{ID: viewEdit, Payload: plugin.MustPayload(idPayload{ID: "1"})})
	if err != nil || v.Kind != plugin.ViewForm || string(v.Form.Fields[0].Default) != `"Std"` || v.Title != "Edit Std" {
		t.Errorf("edit form %+v %v", v, err)
	}
	if _, err := e.p.View(t.Context(), plugin.ViewRequest{ID: viewEdit, Payload: plugin.MustPayload(idPayload{ID: "9"})}); err == nil {
		t.Error("edit form of a missing quicklink")
	}
	for _, id := range []string{cmdCreate, viewImport, viewExport, viewEngine} {
		if v, err := e.p.View(t.Context(), plugin.ViewRequest{ID: id}); err != nil || v.Kind != plugin.ViewForm {
			t.Errorf("%s: %+v %v", id, v, err)
		}
	}
	v, _ = e.p.View(t.Context(), plugin.ViewRequest{ID: viewEngine, Payload: plugin.MustPayload(idPayload{ID: "google"})})
	if len(v.Form.Fields) != 1 || !strings.Contains(v.Form.Fields[0].Info, "https://www.google.com/search?q={query}") {
		t.Errorf("built-in engine form %+v", v.Form)
	}
}

func TestIcons(t *testing.T) {
	for link, want := range map[string]string{
		"https://example.com":     "globe",
		`C:\Users\Public`:         "folder",
		`C:\notes\todo.txt`:       "file",
		`"C:\Tools\a.exe" --x`:    "app",
		`%USERPROFILE%\Downloads`: "folder",
	} {
		if got := linkIcon(parseLink(link)).Name; got != want {
			t.Errorf("%s: %s, want %s", link, got, want)
		}
	}
}
