package scoop

import (
	"encoding/json/jsontext"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/tasks"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/pkg/plugin/bg"
)

const fixtureRoot = "../../testdata/fixtures/scoop"

type env struct {
	t      *testing.T
	p      *Plugin
	h      *hostfake.Host
	runner *tasks.FakeRunner // the host's: commands end at once
}

// newEnv starts the plugin on the Scoop root dir. Its operations are tasks
// of the fake host, whose runner records them.
func newEnv(t *testing.T, root string, o Options) *env {
	t.Helper()
	e := &env{t: t}
	o.Root, o.NoDetect = root, true
	if o.Shell == "" {
		o.Shell = "powershell.exe"
	}
	if o.Global == "" && root != "" {
		o.Global = filepath.Join(root, "global")
	}
	e.p = NewWithOptions(o)
	h, err := hostfake.New(e.p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.h, e.runner = h, h.Runner
	if err := e.p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		h.Tasks.Close()
		e.p.Close()
	})
	return e
}

// commands returns the command lines run since the first n commands.
func (e *env) commands(n int) []string {
	var out []string
	for _, c := range e.runner.Commands()[n:] {
		out = append(out, c.CommandLine)
	}
	return out
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
	p := NewWithOptions(Options{Sync: true,
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
		p := NewWithOptions(Options{Root: root, NoDetect: true, Sync: true})
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
		name  string
		do    plugin.ActionDo
		want  string
		title string
	}{
		{"install", action(t, e.item(search, "pkg/main/lazygit"), "install").Do, "scoop install main/lazygit", "Install lazygit"},
		{"update", action(t, e.item(search, "pkg/main/git"), "update").Do, "scoop update git", "Update git"},
		{"uninstall", action(t, e.item(inst, "app/7zip"), "uninstall").Do, "scoop uninstall 7zip", "Uninstall 7zip"},
		{"hold", action(t, e.item(inst, "app/7zip"), "hold").Do, "scoop hold 7zip", "Hold 7zip"},
		{"unhold", action(t, e.item(inst, "app/nodejs"), "unhold").Do, "scoop unhold nodejs", "Unhold nodejs"},
		{"update all", action(t, e.item(e.view(cmdUpdates, ""), "update-all"), "update-all").Do, "scoop update && scoop update *", "Update All Apps"},
		{"update all command", plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdUpdateAll}, "scoop update && scoop update *", "Update All Apps"},
		{"update buckets", plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdUpdateBuckets}, "scoop update", "Update Buckets"},
		{"add known bucket", action(t, e.item(buckets, "known/versions"), "add-bucket").Do, "scoop bucket add versions", "Add Bucket versions"},
		{"add custom bucket", custom, "scoop bucket add mine https://github.com/me/my-bucket", "Add Bucket mine"},
		{"remove bucket", action(t, e.item(buckets, "bucket/extras"), "remove-bucket").Do, "scoop bucket rm extras", "Remove Bucket extras"},
		{"cleanup", action(t, e.item(e.view(cmdCleanup, ""), "cleanup"), "cleanup").Do, "scoop cleanup * && scoop cache rm *", "Clean Up"},
	}
	for _, st := range steps {
		before := len(e.runner.Commands())
		res := e.run(st.do)
		// The action returns at once with a toast; the operation is a task.
		if res.Push != nil || res.Toast == nil || res.Toast.Style != plugin.ToastInfo ||
			res.Toast.Title != st.title+": running in the background" {
			t.Fatalf("%s: result %+v %+v", st.name, res, res.Toast)
		}
		e.h.WaitTasks()
		if got := e.commands(before); !slices.Equal(got, []string{st.want}) {
			t.Errorf("%s: ran %q, want %q", st.name, got, st.want)
		}
		list := e.h.Tasks.List()
		task := list[0]
		if task.Title != st.title || task.Queue != "scoop" || task.Notify != plugin.NotifyAlways || task.Plugin != ID ||
			task.State != plugin.TaskSucceeded || task.Icon.Name != "package" {
			t.Errorf("%s: task %+v", st.name, task)
		}
		cmd := e.runner.Commands()[before]
		if len(cmd.Argv) != 7 || cmd.Argv[0] != "powershell.exe" || cmd.Argv[5] != "-EncodedCommand" {
			t.Errorf("%s: argv %q", st.name, cmd.Argv)
		}
	}

	e.setPref(prefUpdateBucketsFirst, false)
	before := len(e.runner.Commands())
	e.run(plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdUpdateAll})
	e.h.WaitTasks()
	if got := e.commands(before); !slices.Equal(got, []string{"scoop update *"}) {
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
		t.Errorf("ran %+v", c)
	}
	if l := e.h.Tasks.List(); len(l) != 0 {
		t.Errorf("tasks %+v", l)
	}
}

func TestScriptQuoting(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Scoop is Windows-only (Windows paths in the PowerShell script)")
	}
	s := Script(`C:\my scoop`, [][]string{{"install", "it's", "a b", "$(evil)", "‘x’"}, {"update", "*"}})
	for _, want := range []string{
		`& scoop 'install' 'it''s' 'a b' '$(evil)' '‘‘x’’'`,
		`$env:SCOOP = 'C:\my scoop'`,
		`$env:PATH = 'C:\my scoop\shims;' + $env:PATH`,
		"Write-Output '> scoop update *'",
		"if ($global:LASTEXITCODE -ne 0) { exit $global:LASTEXITCODE }",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script:\n%s\nwant %s", s, want)
		}
	}
	if i, j := strings.Index(s, "'install'"), strings.Index(s, "'update' '*'"); i < 0 || j < i {
		t.Error("steps out of order")
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

// TestOperationsRunOneAtATime: operations share the host's "scoop" queue,
// so they run one after the other; the index is read again after each.
func TestOperationsRunOneAtATime(t *testing.T) {
	release := make(chan struct{})
	var running, maxRunning atomic.Int32
	e := newEnv(t, fixtureRoot, Options{})
	e.runner.Hook = func(cmd plugin.ProcessSpec, out io.Writer, stop <-chan struct{}) int {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			m := maxRunning.Load()
			if n <= m || maxRunning.CompareAndSwap(m, n) {
				break
			}
		}
		io.WriteString(out, "working on "+cmd.CommandLine+"\n")
		select {
		case <-release:
			return 0
		case <-stop:
			return 1
		}
	}
	waitFor(t, "the index", func() bool { return e.p.Index() != nil })
	for _, req := range []opRequest{{Kind: opInstall, Name: "lazygit", Bucket: "main"}, {Kind: opUpdate, Name: "git"}, {Kind: opHold, Name: "7zip"}} {
		e.run(plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackOp, Payload: plugin.MustPayload(req)})
	}
	states := func() []plugin.TaskState {
		var out []plugin.TaskState
		for _, i := range e.h.Tasks.List() {
			out = append(out, i.State)
		}
		return out
	}
	waitFor(t, "the first operation", func() bool { return running.Load() == 1 })
	if got := states(); !slices.Equal(got, []plugin.TaskState{plugin.TaskRunning, plugin.TaskQueued, plugin.TaskQueued}) {
		t.Fatalf("states %v", got)
	}
	// The views no longer list operations: Background Tasks does.
	for _, s := range e.view(cmdInstalled, "").Sections {
		if strings.Contains(s.Title, "Operations") {
			t.Errorf("section %q", s.Title)
		}
	}
	close(release)
	e.h.WaitTasks()
	if maxRunning.Load() != 1 {
		t.Errorf("%d operations ran at once", maxRunning.Load())
	}
	if got := states(); !slices.Equal(got, []plugin.TaskState{plugin.TaskSucceeded, plugin.TaskSucceeded, plugin.TaskSucceeded}) {
		t.Errorf("states %v", got)
	}
}

func TestFailedOperation(t *testing.T) {
	e := newEnv(t, fixtureRoot, Options{Sync: true})
	e.runner.Results = []tasks.FakeResult{{Match: "scoop update", Output: "ERROR something went wrong", ExitCode: 2}}
	e.run(plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdUpdateAll})
	e.h.WaitTasks()
	task := e.h.Tasks.List()[0]
	if task.State != plugin.TaskFailed || task.ExitCode != 2 || task.LastLine != "ERROR something went wrong" {
		t.Errorf("task %+v", task)
	}
}

// TestOperationRefreshesIndex: when an operation ended, installed apps are
// read again.
func TestOperationRefreshesIndex(t *testing.T) {
	root := t.TempDir()
	copyDir(t, fixtureRoot, root)
	e := newEnv(t, root, Options{Sync: true})
	e.runner.Hook = func(plugin.ProcessSpec, io.Writer, <-chan struct{}) int {
		os.RemoveAll(filepath.Join(root, "apps", "7zip")) // as scoop uninstall would
		return 0
	}
	e.run(action(t, e.item(e.view(cmdInstalled, ""), "app/7zip"), "uninstall").Do)
	e.h.WaitTasks()
	if _, ok := e.p.Index().App("7zip"); ok {
		t.Error("7zip is still listed after the operation")
	}
	if !slices.Contains(e.h.Invalidated(), "") {
		t.Error("no Invalidate after the operation")
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

// TestIndexOnDemand: the index is not loaded at Init; the background
// refreshes keep the cache current without loading it (and without reading
// it while its stamps match); a root search loads it in the background and
// invalidates; it is dropped after IdleRelease without use and loaded again
// by the next search.
func TestIndexOnDemand(t *testing.T) {
	root := t.TempDir()
	copyDir(t, fixtureRoot, root)
	p := NewWithOptions(Options{Root: root, NoDetect: true, Global: filepath.Join(root, "global"), Shell: "powershell.exe"})
	dir := t.TempDir()
	h, err := hostfake.New(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	var now atomic.Int64
	now.Store(hostfake.Fixed.UnixNano())
	h.Clock = func() time.Time { return time.Unix(0, now.Load()).UTC() }
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Tasks.Close(); p.Close() })
	tick := make(chan time.Time)
	p.index.After = func(time.Duration) <-chan time.Time { return tick }
	h.Prefs.Set(prefShowInRoot, jsontext.Value(plugin.MustPayload(true)))

	cache := filepath.Join(dir, "cache", "index.json")
	refreshed := func(what string) {
		t.Helper()
		waitFor(t, what, func() bool { return !p.refreshing.Running() })
		if p.index.Loaded() {
			t.Fatalf("%s loaded the index", what)
		}
	}
	refreshed("the startup refresh")
	st, err := os.Stat(cache)
	if err != nil || !cacheCurrent(h.CacheDir(), root) {
		t.Fatalf("the startup refresh wrote no current cache: %v", err)
	}
	// Unchanged buckets: the cache is left alone.
	h.Bus.PublishSync(plugin.EventWindowShown)
	refreshed("a refresh")
	if st2, _ := os.Stat(cache); !st2.ModTime().Equal(st.ModTime()) {
		t.Error("the cache was written although no bucket changed")
	}
	// A changed bucket: the cache is written again.
	os.WriteFile(filepath.Join(root, "buckets", "local", "newtool.json"), []byte(`{"version":"0.1"}`), 0o644)
	future := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(root, "buckets", "local"), future, future)
	if cacheCurrent(h.CacheDir(), root) {
		t.Fatal("the cache is current after a bucket changed")
	}
	h.Bus.PublishSync(plugin.EventWindowShown)
	refreshed("the refresh of a changed bucket")
	if !cacheCurrent(h.CacheDir(), root) {
		t.Fatal("the refresh did not update the cache")
	}

	search := func() []plugin.Item {
		t.Helper()
		items, err := p.RootItems(t.Context(), plugin.Query{Text: "scoop newtool"})
		if err != nil {
			t.Fatal(err)
		}
		return items
	}
	for round := 1; round <= 2; round++ {
		if items := search(); len(items) != 0 {
			t.Fatalf("round %d: %d results before the index was loaded", round, len(items))
		}
		waitFor(t, "the index", p.index.Loaded)
		waitFor(t, "Invalidate", func() bool { return len(h.Invalidated()) == round })
		if items := search(); len(items) != 1 || items[0].Title != "newtool" {
			t.Fatalf("round %d: %+v", round, items)
		}
		now.Add(int64(bg.IdleRelease))
		tick <- time.Time{}
		waitFor(t, "the idle release", func() bool { return !p.index.Loaded() })
	}
}
