package scoop

import (
	"encoding/json/jsontext"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

const fixtureRoot = "../../testdata/fixtures/scoop"

type env struct {
	t      *testing.T
	p      *Plugin
	h      *hostfake.Host
	runner *FakeRunner
}

// newEnv starts the plugin on the Scoop root dir with a fake runner.
func newEnv(t *testing.T, root string, o Options) *env {
	t.Helper()
	e := &env{t: t, runner: &FakeRunner{}}
	if o.Runner == nil {
		o.Runner = e.runner
	} else if f, ok := o.Runner.(*FakeRunner); ok {
		e.runner = f
	}
	o.Root, o.NoDetect = root, true
	if o.Global == "" && root != "" {
		o.Global = filepath.Join(root, "global")
	}
	e.p = NewWithOptions(o)
	h, err := hostfake.New(e.p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.h = h
	if err := e.p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.p.Close() })
	return e
}

func (e *env) setPref(name string, v any) {
	e.t.Helper()
	e.h.Prefs.Set(name, jsontext.Value(plugin.MustPayload(v)))
}

func (e *env) view(id, text string) plugin.View {
	e.t.Helper()
	v, err := e.p.View(e.t.Context(), plugin.ViewRequest{ID: id, Query: plugin.Query{Text: text}})
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

// item returns the item with id in view v.
func (e *env) item(v plugin.View, id string) plugin.Item {
	e.t.Helper()
	for _, s := range v.Sections {
		for _, it := range s.Items {
			if it.ID == id {
				return it
			}
		}
	}
	e.t.Fatalf("no item %q in %+v", id, v)
	return plugin.Item{}
}

func action(t *testing.T, it plugin.Item, id string) plugin.Action {
	t.Helper()
	i := slices.IndexFunc(it.Actions, func(a plugin.Action) bool { return a.ID == id })
	if i < 0 {
		t.Fatalf("item %s has no action %q: %+v", it.ID, id, it.Actions)
	}
	return it.Actions[i]
}

func actionIDs(it plugin.Item) []string {
	var ids []string
	for _, a := range it.Actions {
		ids = append(ids, a.ID)
	}
	return ids
}

func (e *env) run(do plugin.ActionDo) plugin.Result {
	e.t.Helper()
	res, err := e.p.HandleAction(e.t.Context(), do)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func TestDetectRoot(t *testing.T) {
	mk := func() string {
		d := t.TempDir()
		if err := os.MkdirAll(filepath.Join(d, "apps", "scoop", "current"), 0o755); err != nil {
			t.Fatal(err)
		}
		return d
	}
	pref, envRoot, home := mk(), mk(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "scoop", "apps", "scoop", "current"), 0o755); err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{"SCOOP": envRoot, "USERPROFILE": home, "MYDIR": pref}
	getenv := func(k string) string { return vars[k] }

	if got, bad := DetectRoot(pref, getenv); got != pref || bad {
		t.Errorf("preference: %q %v", got, bad)
	}
	if got, _ := DetectRoot(`%MYDIR%`, getenv); got != pref {
		t.Errorf("preference with %%VAR%%: %q", got)
	}
	if got, bad := DetectRoot(t.TempDir(), getenv); got != envRoot || !bad {
		t.Errorf("bad preference falls back to $SCOOP: %q %v", got, bad)
	}
	vars["SCOOP"] = ""
	if got, _ := DetectRoot("", getenv); got != filepath.Join(home, "scoop") {
		t.Errorf("default: %q", got)
	}
	vars["USERPROFILE"] = t.TempDir()
	if got, _ := DetectRoot("", getenv); got != "" {
		t.Errorf("no Scoop: %q", got)
	}

	global := t.TempDir()
	os.MkdirAll(filepath.Join(global, "apps"), 0o755)
	if got := DetectGlobal(func(k string) string { return map[string]string{"ProgramData": filepath.Dir(global)}[k] }); got != "" {
		t.Errorf("ProgramData without scoop: %q", got)
	}
	if got := DetectGlobal(func(k string) string { return map[string]string{"SCOOP_GLOBAL": global}[k] }); got != global {
		t.Errorf("SCOOP_GLOBAL: %q", got)
	}
}

func TestUnavailableWithoutScoop(t *testing.T) {
	for name, o := range map[string]Options{
		"fake without root": {NoDetect: true},
		"nothing detected":  {Getenv: func(string) string { return "" }},
	} {
		t.Run(name, func(t *testing.T) {
			p := NewWithOptions(o)
			h, err := hostfake.New(p, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			err = p.Init(t.Context(), h)
			if !errors.Is(err, plugin.ErrUnavailable) || !strings.Contains(err.Error(), "Scoop is not installed") {
				t.Fatalf("Init = %v", err)
			}
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDetectedRootIsUsed(t *testing.T) {
	root, _ := filepath.Abs(fixtureRoot)
	p := NewWithOptions(Options{Runner: &FakeRunner{}, Sync: true,
		Getenv: func(k string) string { return map[string]string{"SCOOP": root}[k] }})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Root() != root || p.Index().PackageCount() == 0 {
		t.Fatalf("root %q, %d packages", p.Root(), p.Index().PackageCount())
	}
}

func TestParseManifest(t *testing.T) {
	tests := []struct {
		name, src string
		want      Package
	}{
		{"strings", `{"version":"1.0","description":"d","homepage":"https://h","license":"MIT","bin":"bin\\tool.exe","notes":"n"}`,
			Package{Version: "1.0", Description: "d", Homepage: "https://h", License: "MIT", Bin: []string{"tool"}, Notes: "n"}},
		{"arrays", `{"version":"2","description":["a","b"],"license":{"identifier":"GPL-3.0","url":"u"},"notes":["x","y"],
			"bin":["a.exe",["b\\b.exe","bee","--flag"],["c.ps1"]]}`,
			Package{Version: "2", Description: "a b", License: "GPL-3.0", Notes: "x\ny", Bin: []string{"a", "bee", "c"}}},
		{"architecture bins", `{"version":"3","architecture":{"32bit":{"bin":"x86.exe"},"64bit":{"bin":["x64.exe"]},"arm64":{"bin":"x64.exe"}}}`,
			Package{Version: "3", Bin: []string{"x64", "x86"}}},
		{"bom, duplicate keys, number version", "\xef\xbb\xbf" + `{"version":1.5,"description":"x","description":"y","bin":42}`,
			Package{Version: "1.5", Description: "y"}},
		{"wrong types are ignored", `{"version":"1","homepage":{"x":1},"license":["MIT"],"notes":{"a":1},"architecture":[]}`,
			Package{Version: "1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseManifest("app", []byte(tt.src))
			if err != nil {
				t.Fatal(err)
			}
			tt.want.Name = "app"
			if got.Version != tt.want.Version || got.Description != tt.want.Description || got.Homepage != tt.want.Homepage ||
				got.License != tt.want.License || got.Notes != tt.want.Notes || !slices.Equal(got.Bin, tt.want.Bin) {
				t.Errorf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
	if _, err := ParseManifest("x", []byte("not json")); err == nil {
		t.Error("garbage parsed")
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0", "1.0", 0},
		{"1.2.0", "1.10.0", -1},
		{"2.47.0", "2.46.0", 1},
		{"1.0", "1.0.1", -1},
		{"1.0-beta", "1.0", -1},
		{"1.0-rc1", "1.0-rc2", -1},
		{"1.0-beta", "1.0-alpha", 1},
		{"24.08", "24.8", 0},
		{"2.56.0.2", "2.56.0.10", -1},
		{"10.20261006", "10.20250101", 1},
		{"1.0a", "1.0b", -1},
		{"1.2", "1.2a", 1}, // a letter suffix is a pre-release
		{"99999999999999999999.1", "99999999999999999999.2", -1},
		{"V1.0", "v1.0", 0},
	}
	for _, tt := range tests {
		if got := CompareVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
		if got := CompareVersions(tt.b, tt.a); got != -tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.b, tt.a, got, -tt.want)
		}
	}
	if outdated("nightly", "1.0") || outdated("", "1") || outdated("2.0", "1.0") || !outdated("1.0", "1.1") {
		t.Error("outdated")
	}
}

func TestFixtureIndex(t *testing.T) {
	e := newEnv(t, fixtureRoot, Options{Sync: true})
	ix := e.p.Index()
	var names []string
	for _, a := range ix.Apps {
		names = append(names, a.Name)
	}
	if want := []string{"7zip", "git", "handmade", "nodejs", "vscode", "vcredist2022"}; !slices.Equal(names, want) {
		t.Errorf("apps = %v, want %v (no scoop, no app without current)", names, want)
	}
	git, _ := ix.App("git")
	if v, ok := ix.Outdated(git); !ok || v != "2.47.0" || git.Bucket != "main" {
		t.Errorf("git: %+v outdated %q %v", git, v, ok)
	}
	sz, _ := ix.App("7zip")
	if sz.Bucket != "main" || sz.Version != "24.08" || !strings.HasSuffix(sz.Info.File, "scoop-manifest.json") {
		t.Errorf("7zip (newer install file names): %+v", sz)
	}
	if _, ok := ix.Outdated(sz); ok {
		t.Error("7zip is up to date")
	}
	node, _ := ix.App("nodejs")
	if !node.Held {
		t.Error("nodejs is held")
	}
	hm, _ := ix.App("handmade")
	if hm.URL == "" || hm.Bucket != "" {
		t.Errorf("handmade: %+v", hm)
	}
	if _, ok := ix.Outdated(hm); ok {
		t.Error("an app installed from a URL has no update")
	}
	vc, _ := ix.App("vcredist2022")
	if !vc.Global {
		t.Error("vcredist2022 is global")
	}
	if p, ok := ix.Lookup("local", "mytool"); !ok || p.Version != "1.0.0" {
		t.Error("bucket with top-level manifests")
	}
	if _, ok := ix.Lookup("", "config"); ok {
		t.Error("a JSON file without a version is not a manifest")
	}
	var buckets []string
	for _, b := range ix.Buckets {
		buckets = append(buckets, b.Name)
	}
	if want := []string{"main", "extras", "local"}; !slices.Equal(buckets, want) {
		t.Errorf("buckets = %v, want %v", buckets, want)
	}
}

// copyDir copies the directory tree src to dst.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIndexCache(t *testing.T) {
	root := t.TempDir()
	copyDir(t, fixtureRoot, root)
	cache := t.TempDir()
	newPlugin := func() *Plugin {
		p := NewWithOptions(Options{Root: root, NoDetect: true, Runner: &FakeRunner{}, Sync: true})
		h, err := hostfake.New(p, cache)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Init(t.Context(), h); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { p.Close() })
		return p
	}
	p := newPlugin()
	if _, err := os.Stat(filepath.Join(cache, "cache", "index.json")); err != nil {
		t.Fatalf("no cache written: %v", err)
	}
	main := filepath.Join(root, "buckets", "main", "bucket")
	st, _ := os.Stat(main)

	// The cache is used while a bucket's stamp is unchanged: a manifest
	// removed without changing the directory time is still listed.
	if err := os.Remove(filepath.Join(main, "ripgrep.json")); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(main, st.ModTime(), st.ModTime())
	p2 := newPlugin()
	if _, ok := p2.Index().Lookup("main", "ripgrep"); !ok {
		t.Fatal("the cached bucket was read again although its stamp did not change")
	}

	// A fetch (FETCH_HEAD) invalidates the bucket.
	os.MkdirAll(filepath.Join(root, "buckets", "main", ".git"), 0o755)
	os.WriteFile(filepath.Join(root, "buckets", "main", ".git", "FETCH_HEAD"), []byte("x"), 0o644)
	extrasBefore := p2.Index().Buckets[1]
	p2.refresh(true)
	ix := p2.Index()
	if _, ok := ix.Lookup("main", "ripgrep"); ok {
		t.Error("main was not read again after a fetch")
	}
	if ix.Buckets[1] != extrasBefore {
		t.Error("the unchanged bucket extras was read again")
	}
	if !slices.Contains(p2.h.(*hostfake.Host).Invalidated(), "") {
		t.Error("no Invalidate after the index changed")
	}

	// A new manifest in a bucket without git: the directory time changes.
	os.WriteFile(filepath.Join(root, "buckets", "local", "newtool.json"), []byte(`{"version":"0.1"}`), 0o644)
	future := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(root, "buckets", "local"), future, future)
	p2.refresh(false)
	if _, ok := p2.Index().Lookup("local", "newtool"); !ok {
		t.Error("new manifest not found")
	}

	// Installed apps are always read again.
	os.WriteFile(filepath.Join(root, "apps", "git", "current", "manifest.json"), []byte(`{"version":"2.47.0"}`), 0o644)
	p2.refresh(false)
	git, _ := p2.Index().App("git")
	if _, out := p2.Index().Outdated(git); out {
		t.Error("git was updated")
	}

	// A removed bucket disappears.
	os.RemoveAll(filepath.Join(root, "buckets", "extras"))
	p2.refresh(false)
	if _, ok := p2.Index().Lookup("extras", "vscode"); ok {
		t.Error("removed bucket still indexed")
	}
	_ = p
}

func TestCacheOfAnotherRootIsIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := saveCache(dir, `C:\elsewhere`, []*Bucket{{Name: "main"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCache(dir, `C:\scoop`); err == nil {
		t.Error("cache of another root loaded")
	}
	if b, err := loadCache(dir, `c:\ELSEWHERE`); err != nil || len(b) != 1 {
		t.Errorf("loadCache = %v, %v", b, err)
	}
}

func TestSearch(t *testing.T) {
	e := newEnv(t, fixtureRoot, Options{Sync: true})
	ix := e.p.Index()
	names := func(q string, limit int) []string {
		var out []string
		for _, m := range ix.Search(t.Context(), q, limit) {
			out = append(out, m.Pkg.Bucket+"/"+m.Pkg.Name)
		}
		return out
	}
	tests := []struct {
		q    string
		want []string
	}{
		// exact name, then name prefixes, then names containing it, then
		// descriptions mentioning it.
		{"git", []string{"main/git", "extras/git-up", "main/lazygit", "main/gh"}},
		{"rg", []string{"main/ripgrep"}},           // command (bin) name
		{"code", []string{"extras/vscode"}},        // alias of a bin
		{"微信", []string{"extras/wechat"}},          // Chinese description
		{"GIT terminal", []string{"main/lazygit"}}, // every word must match
		{"archiver", []string{"main/7zip"}},
		{"zzzz", nil},
		{"  ", nil},
	}
	for _, tt := range tests {
		if got := names(tt.q, 0); !slices.Equal(got, tt.want) {
			t.Errorf("search %q = %v, want %v", tt.q, got, tt.want)
		}
	}
	if got := names("git", 2); len(got) != 2 {
		t.Errorf("limit: %v", got)
	}
	e.setPref(prefMaxResults, 1)
	v := e.view(cmdSearch, "git")
	if n := len(v.Sections[0].Items); n != 1 {
		t.Errorf("maxResults: %d items", n)
	}
	if v := e.view(cmdSearch, ""); len(v.Sections) != 0 || !strings.Contains(v.EmptyText, "10 packages in 3 buckets") {
		t.Errorf("empty search: %+v", v)
	}
	if v := e.view(cmdSearch, "nothing-like-this"); len(v.Sections) != 0 || v.EmptyText == "" {
		t.Errorf("no results: %+v", v)
	}
}

func TestItemsAndActions(t *testing.T) {
	e := newEnv(t, fixtureRoot, Options{Sync: true})
	search := e.view(cmdSearch, "git")
	git := e.item(search, "pkg/main/git")
	if git.Accessory != "2.46.0 → 2.47.0" {
		t.Errorf("git accessory %q", git.Accessory)
	}
	if got := actionIDs(git); !slices.Equal(got, []string{"update", "open-folder", "homepage", "hold", "copy-install", "reveal-manifest", "uninstall"}) {
		t.Errorf("installed outdated actions %v", got)
	}
	if a := action(t, git, "uninstall"); a.Style != plugin.StyleDestructive || a.Shortcut != "Ctrl+X" {
		t.Errorf("uninstall %+v", a)
	}
	if a := action(t, git, "copy-install"); a.Do.Kind != plugin.ActionCopyText || a.Do.Target != "scoop install main/git" {
		t.Errorf("copy-install %+v", a.Do)
	}
	lazy := e.item(search, "pkg/main/lazygit")
	if got := actionIDs(lazy); !slices.Equal(got, []string{"install", "homepage", "copy-install", "reveal-manifest"}) {
		t.Errorf("not installed actions %v", got)
	}
	if lazy.Accessory != "0.44.1 · main" || lazy.Detail == nil {
		t.Errorf("lazygit %+v", lazy)
	}
	d := git.Detail
	if !strings.Contains(d.Markdown, "### Notes") || !strings.Contains(d.Markdown, "install-associations.reg") {
		t.Errorf("git detail markdown %q", d.Markdown)
	}
	meta := map[string]string{}
	for _, m := range d.Metadata {
		meta[m.Label] = m.Value
	}
	if meta["Installed"] != "2.46.0" || meta["Latest"] != "2.47.0" || meta["License"] != "GPL-2.0-only" ||
		meta["Commands"] != "sh, bash, git, vi" || meta["Bucket"] != "main" {
		t.Errorf("git metadata %v", meta)
	}

	inst := e.view(cmdInstalled, "")
	var titles []string
	for _, s := range inst.Sections {
		titles = append(titles, s.Title)
	}
	if !slices.Equal(titles, []string{"Updates Available", "Installed", "Installed for All Users"}) || !inst.FilterLocally {
		t.Errorf("installed sections %v", titles)
	}
	if ids := []string{inst.Sections[0].Items[0].ID, inst.Sections[0].Items[1].ID}; !slices.Equal(ids, []string{"app/git", "app/nodejs"}) {
		t.Errorf("updates first: %v", ids)
	}
	node := e.item(inst, "app/nodejs")
	if node.Accessory != "22.0.0 → 23.1.0 · Held" || slices.Contains(actionIDs(node), "update") || !slices.Contains(actionIDs(node), "unhold") {
		t.Errorf("held nodejs %q %v", node.Accessory, actionIDs(node))
	}
	vc := e.item(inst, "global/vcredist2022")
	if got := actionIDs(vc); !slices.Equal(got, []string{"open-folder", "homepage", "copy-install", "reveal-manifest"}) {
		t.Errorf("global app actions are read-only: %v", got)
	}
	hm := e.item(inst, "app/handmade")
	if a := action(t, hm, "copy-install"); a.Do.Target != "scoop install https://example.invalid/handmade.json" {
		t.Errorf("handmade copy-install %q", a.Do.Target)
	}

	up := e.view(cmdUpdates, "")
	if ids := []string{up.Sections[0].Items[0].ID, up.Sections[0].Items[0].Title}; ids[0] != "update-all" || ids[1] != "Update 1 App" {
		t.Errorf("updates: %v", ids)
	}
}

func TestActionCommands(t *testing.T) {
	e := newEnv(t, fixtureRoot, Options{Sync: true})
	search := e.view(cmdSearch, "git")
	inst := e.view(cmdInstalled, "")
	buckets := e.view(cmdBuckets, "")

	custom := action(t, e.item(buckets, "custom"), "add-bucket").Do
	custom.Values = map[string]jsontext.Value{
		"name": jsontext.Value(`"mine"`),
		"url":  jsontext.Value(`"https://github.com/me/my-bucket"`),
	}
	steps := []struct {
		name string
		do   plugin.ActionDo
		want []string
	}{
		{"install", action(t, e.item(search, "pkg/main/lazygit"), "install").Do, []string{"install main/lazygit"}},
		{"update", action(t, e.item(search, "pkg/main/git"), "update").Do, []string{"update git"}},
		{"uninstall", action(t, e.item(inst, "app/7zip"), "uninstall").Do, []string{"uninstall 7zip"}},
		{"hold", action(t, e.item(inst, "app/7zip"), "hold").Do, []string{"hold 7zip"}},
		{"unhold", action(t, e.item(inst, "app/nodejs"), "unhold").Do, []string{"unhold nodejs"}},
		{"update all", action(t, e.item(e.view(cmdUpdates, ""), "update-all"), "update-all").Do, []string{"update", "update *"}},
		{"update all command", plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdUpdateAll}, []string{"update", "update *"}},
		{"update buckets", plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdUpdateBuckets}, []string{"update"}},
		{"add known bucket", action(t, e.item(buckets, "known/versions"), "add-bucket").Do, []string{"bucket add versions"}},
		{"add custom bucket", custom, []string{"bucket add mine https://github.com/me/my-bucket"}},
		{"remove bucket", action(t, e.item(buckets, "bucket/extras"), "remove-bucket").Do, []string{"bucket rm extras"}},
		{"cleanup", action(t, e.item(e.view(cmdCleanup, ""), "cleanup"), "cleanup").Do, []string{"cleanup *", "cache rm *"}},
	}
	for _, st := range steps {
		before := len(e.runner.Commands())
		res := e.run(st.do)
		if res.Push == nil || res.Push.ID != viewOutput {
			t.Fatalf("%s: result %+v, want the output view", st.name, res)
		}
		var got []string
		for _, c := range e.runner.Commands()[before:] {
			got = append(got, strings.Join(c, " "))
		}
		if !slices.Equal(got, st.want) {
			t.Errorf("%s: ran %q, want %q", st.name, got, st.want)
		}
		out := e.p.View
		v, err := out(t.Context(), *res.Push)
		if err != nil {
			t.Fatal(err)
		}
		it := v.Sections[0].Items[0]
		if it.Title != "Done" || !strings.Contains(it.Detail.Markdown, "> scoop "+st.want[0]) {
			t.Errorf("%s: output view %q %q", st.name, it.Title, it.Detail.Markdown)
		}
	}

	e.setPref(prefUpdateBucketsFirst, false)
	before := len(e.runner.Commands())
	e.run(plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdUpdateAll})
	if got := e.runner.Commands()[before:]; len(got) != 1 || strings.Join(got[0], " ") != "update *" {
		t.Errorf("update all without updating buckets: %q", got)
	}
}

func TestBadRequestsAreRefused(t *testing.T) {
	e := newEnv(t, fixtureRoot, Options{Sync: true})
	for _, req := range []opRequest{
		{Kind: opInstall, Name: "-g"},
		{Kind: opInstall, Name: "git", Bucket: "main/../x"},
		{Kind: opUninstall, Name: "a b"},
		{Kind: opUninstall, Name: "vcredist2022"}, // global
		{Kind: opBucketAdd, Name: "x", URL: "--help"},
		{Kind: opBucketAdd, Name: "x", URL: "https://a/b'; rm"},
		{Kind: "format-disk"},
	} {
		res := e.run(plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackOp, Payload: plugin.MustPayload(req)})
		if res.Toast == nil || res.Toast.Style != plugin.ToastFailure || res.Push != nil {
			t.Errorf("%+v: %+v", req, res)
		}
	}
	if c := e.runner.Commands(); len(c) != 0 {
		t.Errorf("ran %q", c)
	}
}

func TestScriptQuoting(t *testing.T) {
	s := Script([]string{"install", "it's", "a b", "$(evil)", "‘x’"})
	want := `& scoop 'install' 'it''s' 'a b' '$(evil)' '‘‘x’’'`
	if !strings.Contains(s, want) {
		t.Errorf("script:\n%s\nwant %s", s, want)
	}
	if CommandLine([]string{"update", "*"}) != "scoop update *" {
		t.Error("CommandLine")
	}
	enc := encodeCommand("a€")
	if enc != "YQCsIA==" { // UTF-16LE 61 00 AC 20
		t.Errorf("encodeCommand = %q", enc)
	}
}

func TestRootItems(t *testing.T) {
	e := newEnv(t, fixtureRoot, Options{Sync: true})
	items, _ := e.p.RootItems(t.Context(), plugin.Query{Text: "scoop git"})
	if len(items) != 0 {
		t.Fatal("root results while showInRoot is off")
	}
	e.setPref(prefShowInRoot, true)
	for _, q := range []string{"scoop git", "SC git", "sc  git "} {
		items, _ = e.p.RootItems(t.Context(), plugin.Query{Text: q})
		if len(items) != 4 || items[0].Title != "git" || items[0].Score <= items[1].Score || items[0].Group != "Scoop" {
			t.Errorf("%q: %+v", q, items)
		}
	}
	for _, q := range []string{"git", "scoop", "sc ", "scoopgit"} {
		if items, _ := e.p.RootItems(t.Context(), plugin.Query{Text: q}); len(items) != 0 {
			t.Errorf("%q: %d items", q, len(items))
		}
	}
}

// blockingRunner is a FakeRunner whose commands run until released or
// stopped, counting how many run at once.
func blockingRunner(release <-chan struct{}, running, maxRunning *atomic.Int32) *FakeRunner {
	return &FakeRunner{Hook: func(args []string, out io.Writer, stop <-chan struct{}) int {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			m := maxRunning.Load()
			if n <= m || maxRunning.CompareAndSwap(m, n) {
				break
			}
		}
		io.WriteString(out, "working on "+strings.Join(args, " ")+"\n")
		select {
		case <-release:
			io.WriteString(out, "finished\n")
			return 0
		case <-stop:
			return 1
		}
	}}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestQueueRunsOneAtATime(t *testing.T) {
	release := make(chan struct{})
	var running, maxRunning atomic.Int32
	e := newEnv(t, fixtureRoot, Options{Runner: blockingRunner(release, &running, &maxRunning)})
	waitFor(t, "the index", func() bool { return e.p.Index() != nil })

	start := func(req opRequest) *op {
		res := e.run(plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackOp, Payload: plugin.MustPayload(req)})
		if res.Push == nil {
			t.Fatalf("%+v", res)
		}
		e.p.mu.Lock()
		defer e.p.mu.Unlock()
		return e.p.ops["op"+strconv.Itoa(e.p.opSeq)]
	}
	a := start(opRequest{Kind: opInstall, Name: "lazygit", Bucket: "main"})
	b := start(opRequest{Kind: opUpdate, Name: "git"})
	c := start(opRequest{Kind: opHold, Name: "7zip"})
	waitFor(t, "the first operation to run", func() bool { return a.state().status == opRunning })
	if b.state().status != opQueued || c.state().status != opQueued {
		t.Fatal("operations run concurrently")
	}

	// The output view of a running operation streams output and offers Stop;
	// views list the queued and running operations.
	waitFor(t, "output", func() bool { return strings.Contains(a.out.String(), "working on install main/lazygit") })
	v, _ := e.p.View(t.Context(), plugin.ViewRequest{ID: viewOutput, Payload: plugin.MustPayload(stopPayload{Op: a.id})})
	it := v.Sections[0].Items[0]
	if it.Title != "Running…" || it.Actions[0].ID != "stop" || !strings.Contains(it.Detail.Markdown, "working on") {
		t.Errorf("running output view: %q %v %q", it.Title, actionIDs(it), it.Detail.Markdown)
	}
	waitFor(t, "an output refresh", func() bool { return slices.Contains(e.h.Invalidated(), viewOutput) })
	inst := e.view(cmdInstalled, "")
	if inst.Sections[0].Title != "Scoop Operations" || len(inst.Sections[0].Items) != 3 {
		t.Errorf("operations section: %+v", inst.Sections[0])
	}
	bv, _ := e.p.View(t.Context(), plugin.ViewRequest{ID: viewOutput, Payload: plugin.MustPayload(stopPayload{Op: b.id})})
	if got := bv.Sections[0].Items[0].Title; got != "Waiting for another Scoop operation…" {
		t.Errorf("queued status %q", got)
	}

	// Stopping a queued operation drops it; stopping the running one ends it.
	e.run(plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackStop, Payload: plugin.MustPayload(stopPayload{Op: b.id})})
	if s := b.state(); s.status != opDone || !s.stopped {
		t.Errorf("stopped queued op: %+v", s)
	}
	e.run(plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackStop, Payload: plugin.MustPayload(stopPayload{Op: a.id})})
	if s := a.state(); s.status != opDone || !s.stopped {
		t.Errorf("stopped running op: %+v", s)
	}
	v, _ = e.p.View(t.Context(), plugin.ViewRequest{ID: viewOutput, Payload: plugin.MustPayload(stopPayload{Op: a.id})})
	if it := v.Sections[0].Items[0]; it.Title != "Stopped" || slices.Contains(actionIDs(it), "stop") {
		t.Errorf("stopped view: %q %v", it.Title, actionIDs(it))
	}

	// The last one runs and succeeds.
	waitFor(t, "the third operation", func() bool { return c.state().status == opRunning })
	close(release)
	c.wait(t.Context())
	if s := c.state(); !s.succeeded() {
		t.Errorf("third op: %+v", s)
	}
	var got []string
	for _, cmd := range e.runner.Commands() {
		got = append(got, strings.Join(cmd, " "))
	}
	if !slices.Equal(got, []string{"install main/lazygit", "hold 7zip"}) {
		t.Errorf("commands %q (the dropped one must not run)", got)
	}
	if maxRunning.Load() != 1 {
		t.Errorf("%d commands ran at once", maxRunning.Load())
	}
}

func TestFailedOperation(t *testing.T) {
	runner := &FakeRunner{Hook: func(args []string, out io.Writer, _ <-chan struct{}) int {
		io.WriteString(out, "ERROR something went wrong\n")
		return 2
	}}
	e := newEnv(t, fixtureRoot, Options{Runner: runner, Sync: true})
	res := e.run(plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdUpdateAll})
	v, _ := e.p.View(t.Context(), *res.Push)
	it := v.Sections[0].Items[0]
	if it.Title != "Failed (exit code 2)" {
		t.Errorf("title %q", it.Title)
	}
	if n := len(runner.Commands()); n != 1 {
		t.Errorf("%d commands ran; the second step must not run after a failure", n)
	}
	if a := action(t, it, "copy-output"); !strings.Contains(a.Do.Target, "something went wrong") {
		t.Errorf("copy output %q", a.Do.Target)
	}
}

func TestCloseStopsOperations(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var running, maxRunning atomic.Int32
	e := newEnv(t, fixtureRoot, Options{Runner: blockingRunner(release, &running, &maxRunning)})
	e.run(plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdUpdateBuckets})
	waitFor(t, "the operation", func() bool { return running.Load() == 1 })
	done := make(chan struct{})
	e.h.Go(func() { e.p.Close(); close(done) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not stop the running operation")
	}
	if res, _ := e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdUpdateBuckets}); res.Toast == nil {
		t.Error("an operation was queued after Close")
	}
}

func TestReclaim(t *testing.T) {
	root := t.TempDir()
	write := func(path string, n int) {
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, make([]byte, n), 0o644)
	}
	write(filepath.Join(root, "apps", "tool", "1.0", "a.exe"), 1000)
	write(filepath.Join(root, "apps", "tool", "1.0", "sub", "b.dll"), 500)
	write(filepath.Join(root, "apps", "tool", "0.9", "a.exe"), 100)
	write(filepath.Join(root, "apps", "tool", "2.0", "a.exe"), 9999)
	write(filepath.Join(root, "apps", "tool", "current", "a.exe"), 9999)
	write(filepath.Join(root, "apps", "other", "3.0", "x"), 1)
	write(filepath.Join(root, "cache", "tool#1.0#x.zip"), 300)
	write(filepath.Join(root, "cache", "other#3.0#y.zip"), 200)
	apps := []App{
		{Name: "tool", Version: "2.0", Dir: filepath.Join(root, "apps", "tool", "current")},
		{Name: "other", Version: "3.0", Dir: filepath.Join(root, "apps", "other", "current")},
		{Name: "g", Version: "1", Global: true, Dir: filepath.Join(root, "apps", "tool", "current")},
	}
	r := computeReclaim(t.Context(), root, apps)
	if len(r.Apps) != 1 || r.Apps[0].App != "tool" || !slices.Equal(r.Apps[0].Versions, []string{"0.9", "1.0"}) || r.OldSize != 1600 {
		t.Errorf("old versions %+v (%d bytes)", r.Apps, r.OldSize)
	}
	if r.CacheFiles != 2 || r.CacheSize != 500 || r.Total() != 2100 {
		t.Errorf("cache %d files %d bytes, total %d", r.CacheFiles, r.CacheSize, r.Total())
	}

	e := newEnv(t, fixtureRoot, Options{Sync: true})
	v := e.view(cmdCleanup, "")
	it := e.item(v, "cleanup")
	if a := action(t, it, "cleanup"); a.Style != plugin.StyleDestructive {
		t.Error("cleanup is not destructive")
	}
	if it.Subtitle == "" || len(it.Detail.Metadata) != 3 {
		t.Errorf("cleanup item %+v", it)
	}
}

func TestWindowShownRefreshes(t *testing.T) {
	root := t.TempDir()
	copyDir(t, fixtureRoot, root)
	e := newEnv(t, root, Options{Sync: true})
	os.RemoveAll(filepath.Join(root, "apps", "vscode"))
	e.h.Bus.PublishSync(plugin.EventWindowShown)
	waitFor(t, "the refresh", func() bool {
		_, ok := e.p.Index().App("vscode")
		return !ok
	})
	waitFor(t, "Invalidate", func() bool { return slices.Contains(e.h.Invalidated(), "") })
}
