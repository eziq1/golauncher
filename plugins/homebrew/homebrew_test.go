package homebrew

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// The fixtures in testdata are trimmed copies of what Homebrew 7.0 wrote
// and printed on a Mac: cache/ is a Homebrew cache directory with the
// internal API index, public/ one with the public API files, and the
// info_*.json files are `brew info --json=v2` output.
const testdata = "testdata"

var now = time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testdata, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func pkgByName(t *testing.T, pkgs []*Package, kind, name string) *Package {
	t.Helper()
	for _, p := range pkgs {
		if p.Kind == kind && p.Name == name {
			return p
		}
	}
	t.Fatalf("no %s %s", kind, name)
	return nil
}

func TestParseInternal(t *testing.T) {
	pkgs, err := ParseInternal(readFile(t, "cache/api/internal/packages.sequoia.jws.json"), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 13 {
		t.Errorf("%d packages", len(pkgs))
	}
	wget := pkgByName(t, pkgs, KindFormula, "wget")
	if wget.Version != "1.25.0_2" || wget.Desc != "Internet file retriever" || wget.License != "GPL-3.0-or-later" ||
		wget.Homepage != "https://www.gnu.org/software/wget/" || !slices.Equal(wget.Executables, []string{"wget"}) || wget.Tap != "homebrew/core" {
		t.Errorf("wget %+v", wget)
	}
	if rg := pkgByName(t, pkgs, KindFormula, "ripgrep"); !slices.Equal(rg.Aliases, []string{"rg"}) {
		t.Errorf("ripgrep aliases %q", rg.Aliases)
	}
	if node := pkgByName(t, pkgs, KindFormula, "node"); !slices.Equal(node.Aliases, []string{"node.js", "node@26", "nodejs", "npm"}) {
		t.Errorf("node aliases %q", node.Aliases)
	}
	if git := pkgByName(t, pkgs, KindFormula, "git"); git.License != "BSD-3-Clause and GPL-2.0-only and GPL-2.0-or-later and LGPL-2.1-or-later and MIT" &&
		git.License != "GPL-2.0-only and GPL-2.0-or-later and LGPL-2.1-or-later and BSD-3-Clause and MIT" {
		t.Errorf("git license %q", git.License)
	}
	// python@3.13 is deprecated from 2029-11-01 on.
	if py := pkgByName(t, pkgs, KindFormula, "python@3.13"); py.Deprecated {
		t.Errorf("python@3.13 deprecated before its date")
	}
	later, _ := ParseInternal(readFile(t, "cache/api/internal/packages.sequoia.jws.json"), time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	if py := pkgByName(t, later, KindFormula, "python@3.13"); !py.Deprecated {
		t.Errorf("python@3.13 not deprecated after its date")
	}
	wechat := pkgByName(t, pkgs, KindCask, "wechat")
	if wechat.Title() != "WeChat for Mac" || !slices.Contains(wechat.Names, "微信 Mac 版") || wechat.Version != "4.1.15.55,270135" ||
		!wechat.AutoUpdates || wechat.Tap != "homebrew/cask" {
		t.Errorf("wechat %+v", wechat)
	}
}

func TestParsePublic(t *testing.T) {
	pkgs, err := ParsePublic(readFile(t, "public/api/formula.jws.json"), readFile(t, "public/api/cask.jws.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 5 {
		t.Errorf("%d packages", len(pkgs))
	}
	if wget := pkgByName(t, pkgs, KindFormula, "wget"); wget.Version != "1.25.0_2" || wget.License != "GPL-3.0-or-later" {
		t.Errorf("wget %+v", wget)
	}
	if code := pkgByName(t, pkgs, KindCask, "visual-studio-code"); code.Title() != "Microsoft Visual Studio Code" || code.Version != "1.142.0" {
		t.Errorf("code %+v", code)
	}
	if _, err := ParsePublic([]byte(`{"payload": "[{\"name\": 1}]"}`), nil); err == nil {
		t.Error("bad payload accepted")
	}
	if _, err := ParsePublic([]byte(`{"signature": "x"}`), nil); err == nil {
		t.Error("missing payload accepted")
	}
}

func TestParseNames(t *testing.T) {
	pkgs := ParseNames([]byte("wget\ngit\n\n"), []byte("firefox\r\n"))
	if len(pkgs) != 3 || pkgs[2].Kind != KindCask || pkgs[2].Name != "firefox" {
		t.Errorf("%+v", pkgs)
	}
}

func TestLicenseText(t *testing.T) {
	for in, want := range map[string]string{
		`null`:                               "",
		`"MIT"`:                              "MIT",
		`{":any_of": ["MIT", "Apache-2.0"]}`: "MIT or Apache-2.0",
		`{"all_of": ["GPL-2.0-only", {"GPL-2.0-only": {"with": "Classpath"}}]}`: "GPL-2.0-only and GPL-2.0-only",
		`:public_domain`:   "",
		`":public_domain"`: "public_domain",
	} {
		if got := licenseText(jsontext.Value(in)); got != want {
			t.Errorf("licenseText(%s) = %q, want %q", in, got, want)
		}
	}
}

// copyTree copies the fixture directory src to a new temporary directory.
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestFindIndex(t *testing.T) {
	if _, err := findIndex(t.TempDir()); !errors.Is(err, errNoIndex) {
		t.Errorf("empty cache: %v", err)
	}
	dir := copyTree(t, filepath.Join(testdata, "cache"))
	f, err := findIndex(dir)
	if err != nil || f.format != FormatInternal {
		t.Fatalf("%+v %v", f, err)
	}
	// With newer public files as well, they win (an older brew ran last).
	pub := filepath.Join(dir, "api")
	for _, n := range []string{"formula.jws.json", "cask.jws.json"} {
		if err := os.WriteFile(filepath.Join(pub, n), readFile(t, "public/api/"+n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(pub, "internal", "packages.sequoia.jws.json"), old, old)
	f2, err := findIndex(dir)
	if err != nil || f2.format != FormatPublic || f2.stamp == f.stamp {
		t.Fatalf("%+v %v", f2, err)
	}
	ix, err := readIndex(f2, now)
	if err != nil || ix.Format != FormatPublic {
		t.Fatal(ix, err)
	}
	// Only the name lists.
	os.RemoveAll(filepath.Join(pub, "internal"))
	os.Remove(filepath.Join(pub, "formula.jws.json"))
	os.Remove(filepath.Join(pub, "cask.jws.json"))
	f3, err := findIndex(dir)
	if err != nil || f3.format != FormatNames {
		t.Fatalf("%+v %v", f3, err)
	}
	ix, err = readIndex(f3, now)
	if err != nil {
		t.Fatal(err)
	}
	if fo, c := ix.Counts(); fo != 8 || c != 5 {
		t.Errorf("counts %d %d", fo, c)
	}
}

func fixtureIndex(t *testing.T) *Index {
	t.Helper()
	f, err := findIndex(filepath.Join(testdata, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	ix, err := readIndex(f, now)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func TestSearch(t *testing.T) {
	ix := fixtureIndex(t)
	for q, want := range map[string]string{
		"wget":       "formula/wget",
		"rg":         "formula/ripgrep",
		"微信":         "cask/wechat",
		"vs code":    "cask/visual-studio-code",
		"firefox":    "cask/firefox",
		"FireFox":    "cask/firefox",
		"chrome":     "cask/google-chrome",
		"npm":        "formula/node",
		"retriever":  "formula/wget",
		"python":     "formula/python@3.13",
		"git":        "formula/git",
		"terminal":   "cask/iterm2",
		"scalar":     "formula/git", // a command of git
		"nosuchpkg1": "",
	} {
		got := ix.Search(context.Background(), q, 5)
		switch {
		case want == "" && len(got) > 0:
			t.Errorf("%q: %d results", q, len(got))
		case want != "" && (len(got) == 0 || got[0].Kind+"/"+got[0].Name != want):
			var names []string
			for _, p := range got {
				names = append(names, p.Kind+"/"+p.Name)
			}
			t.Errorf("%q: got %q, want %s first", q, names, want)
		}
	}
	if got := ix.Search(context.Background(), "", 5); got != nil {
		t.Errorf("empty query: %v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := ix.Search(ctx, "wget", 5); got != nil {
		t.Errorf("cancelled: %v", got)
	}
}

func TestParseInstalled(t *testing.T) {
	items, err := ParseInstalled(readFile(t, "info_installed.json"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Installed{}
	for _, in := range items {
		got[in.Kind+"/"+in.Name] = in
	}
	if len(got) != 6 {
		t.Fatalf("%d items: %+v", len(got), items)
	}
	if w := got["formula/wget"]; w.Version != "1.25.0" || w.Latest != "1.25.0_2" || !w.Outdated || !w.OnRequest || w.FullName != "" {
		t.Errorf("wget %+v", w)
	}
	if a := got["formula/abseil"]; a.OnRequest || !a.Outdated {
		t.Errorf("abseil %+v", a)
	}
	if f := got["cask/firefox"]; f.Version != "132.0.2" || f.Latest != "158.0" || f.Outdated || !f.AutoUpdates || f.Title() != "Mozilla Firefox" {
		t.Errorf("firefox %+v", f)
	}
	if w := got["cask/wechat"]; w.Version != "latest" || !w.Outdated {
		t.Errorf("wechat %+v", w)
	}
	// A warning before the JSON is skipped; garbage is an error.
	if _, err := ParseInstalled(append([]byte("Warning: something\n"), readFile(t, "info_installed.json")...)); err != nil {
		t.Error(err)
	}
	if _, err := ParseInstalled([]byte("Error: no")); err == nil {
		t.Error("garbage accepted")
	}
}

func TestParseDetails(t *testing.T) {
	d, err := ParseDetails(readFile(t, "info_wget.json"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != KindFormula || d.Version != "1.25.0_2" || !slices.Contains(d.Deps, "openssl@4") ||
		!slices.Equal(d.BuildDeps, []string{"pkgconf"}) || !slices.Equal(d.Installed, []string{"1.25.0"}) || !d.Outdated {
		t.Errorf("wget %+v", d)
	}
	c, err := ParseDetails(readFile(t, "info_firefox.json"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != KindCask || !slices.Equal(c.Apps, []string{"Firefox.app"}) || !slices.Contains(c.Conflicts, "firefox@esr") ||
		!c.AutoUpd || !slices.Equal(c.Installed, []string{"132.0.2"}) || c.Names[0] != "Mozilla Firefox" {
		t.Errorf("firefox %+v", c)
	}
	if _, err := ParseDetails([]byte(`{"formulae": [], "casks": []}`)); err == nil {
		t.Error("empty info accepted")
	}
}

func TestFindBrew(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("execute bits of Unix paths; macOS and Unix only")
	}
	noPath := func(string) (string, error) { return "", errors.New("not found") }
	if got := FindBrew(func(p string) bool { return p == "/usr/local/bin/brew" }, noPath); got != "/usr/local/bin/brew" {
		t.Errorf("Intel: %q", got)
	}
	both := func(p string) bool { return p == "/usr/local/bin/brew" || p == "/opt/homebrew/bin/brew" }
	if got := FindBrew(both, noPath); got != "/opt/homebrew/bin/brew" {
		t.Errorf("both: %q", got)
	}
	onPath := func(string) (string, error) { return "/Users/me/homebrew/bin/brew", nil }
	if got := FindBrew(func(string) bool { return false }, onPath); got != "/Users/me/homebrew/bin/brew" {
		t.Errorf("PATH: %q", got)
	}
	if got := FindBrew(func(string) bool { return false }, noPath); got != "" {
		t.Errorf("none: %q", got)
	}
}

// ---- the plugin ----

// fakeBrew answers the read-only brew commands from the fixtures.
type fakeBrew struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeBrew) run(_ context.Context, args []string) ([]byte, error) {
	line := strings.Join(args, " ")
	f.mu.Lock()
	f.calls = append(f.calls, line)
	f.mu.Unlock()
	file := map[string]string{
		"info --json=v2 --installed":    "info_installed.json",
		"info --json=v2 --formula wget": "info_wget.json",
		"info --json=v2 --cask firefox": "info_firefox.json",
	}[line]
	if file == "" {
		return nil, &ExitError{Code: 1, Output: "Error: No available formula with the name \"" + args[len(args)-1] + "\".\n"}
	}
	return os.ReadFile(filepath.Join(testdata, file))
}

func (f *fakeBrew) count(line string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == line {
			n++
		}
	}
	return n
}

type env struct {
	t    *testing.T
	p    *Plugin
	h    *hostfake.Host
	brew *fakeBrew
}

func newEnv(t *testing.T, edit func(*Options)) *env {
	t.Helper()
	fb := &fakeBrew{}
	o := Options{Exe: "/opt/homebrew/bin/brew", NoDetect: true, Cache: filepath.Join(testdata, "cache"), Run: fb.run, Sync: true,
		Getenv: func(string) string { return "" }}
	if edit != nil {
		edit(&o)
	}
	e := &env{t: t, brew: fb, p: NewWithOptions(o)}
	h, err := hostfake.New(e.p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.h = h
	if err := e.p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		h.Tasks.Close()
		e.p.Close()
	})
	return e
}

func (e *env) view(id, text string, payload any) plugin.View {
	e.t.Helper()
	req := plugin.ViewRequest{ID: id, Query: plugin.Query{Text: text}}
	if payload != nil {
		req.Payload = plugin.MustPayload(payload)
	}
	v, err := e.p.View(e.t.Context(), req)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (e *env) run1(do plugin.ActionDo) plugin.Result {
	e.t.Helper()
	res, err := e.p.HandleAction(e.t.Context(), do)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *env) commands() []string {
	var out []string
	for _, c := range e.h.Runner.Commands() {
		out = append(out, c.CommandLine)
	}
	return out
}

func items(v plugin.View) []plugin.Item {
	var out []plugin.Item
	for _, s := range v.Sections {
		out = append(out, s.Items...)
	}
	return out
}

func item(t *testing.T, v plugin.View, id string) plugin.Item {
	t.Helper()
	for _, it := range items(v) {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no item %s in %+v", id, ids(v))
	return plugin.Item{}
}

func ids(v plugin.View) []string {
	var out []string
	for _, it := range items(v) {
		out = append(out, it.ID)
	}
	return out
}

func actionIDs(it plugin.Item) []string {
	var out []string
	for _, a := range it.Actions {
		out = append(out, a.ID)
	}
	return out
}

func action(t *testing.T, it plugin.Item, id string) plugin.Action {
	t.Helper()
	i := slices.IndexFunc(it.Actions, func(a plugin.Action) bool { return a.ID == id })
	if i < 0 {
		t.Fatalf("item %s has no action %q: %q", it.ID, id, actionIDs(it))
	}
	return it.Actions[i]
}

func TestSearchView(t *testing.T) {
	e := newEnv(t, nil)
	if v := e.view(cmdSearch, "", nil); v.EmptyText != "Type to search 8 formulae and 5 casks" {
		t.Errorf("hint %q", v.EmptyText)
	}
	v := e.view(cmdSearch, "firefox", nil)
	ff := item(t, v, "cask/firefox")
	if ff.Title != "Mozilla Firefox" || ff.Subtitle != "Cask · Web browser" || ff.Accessory != "Installed 132.0.2" {
		t.Errorf("firefox %q %q %q", ff.Title, ff.Subtitle, ff.Accessory)
	}
	if got := actionIDs(ff); !slices.Equal(got, []string{"details", "homepage", "copy-name", "refresh", "uninstall", "zap"}) {
		t.Errorf("firefox actions %q", got)
	}
	gh := item(t, e.view(cmdSearch, "gh", nil), "formula/gh")
	if gh.Accessory != "2.102.0" || !strings.HasPrefix(gh.Subtitle, "Formula · ") {
		t.Errorf("gh %q %q", gh.Accessory, gh.Subtitle)
	}
	if got := actionIDs(gh); !slices.Equal(got, []string{"install", "details", "homepage", "copy-install", "copy-name"}) {
		t.Errorf("gh actions %q", got)
	}
	if a := action(t, gh, "copy-install"); a.Do.Target != "brew install --formula gh" {
		t.Errorf("copy %q", a.Do.Target)
	}
	wget := item(t, e.view(cmdSearch, "wget", nil), "formula/wget")
	if wget.Accessory != "1.25.0 → 1.25.0_2" || wget.Actions[0].ID != "upgrade" {
		t.Errorf("wget %q %q", wget.Accessory, actionIDs(wget))
	}
	if v := e.view(cmdSearch, "nosuchpackage", nil); len(v.Sections) != 0 || v.EmptyText == "" {
		t.Errorf("no results: %+v", v)
	}
}

func TestSearchWithoutIndex(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Cache = t.TempDir() })
	v := e.view(cmdSearch, "wget", nil)
	if !strings.Contains(v.EmptyText, "Run Update Homebrew") || item(t, v, "update").Actions[0].ID != "update" {
		t.Errorf("%+v", v)
	}
}

func TestIndexChanges(t *testing.T) {
	dir := copyTree(t, filepath.Join(testdata, "cache"))
	e := newEnv(t, func(o *Options) { o.Cache = dir })
	if f, _ := e.p.Index().Counts(); f != 8 {
		t.Fatalf("%d formulae", f)
	}
	// brew update replaced the index with the names only (as if).
	os.RemoveAll(filepath.Join(dir, "api", "internal"))
	os.WriteFile(filepath.Join(dir, "api", "formula_names.txt"), []byte("wget\n"), 0o644)
	e.p.refreshIndex(true)
	if f, c := e.p.Index().Counts(); f != 1 || c != 5 {
		t.Errorf("after the change: %d %d", f, c)
	}
	// Unchanged files are not read again.
	ix := e.p.Index()
	e.p.refreshIndex(true)
	if e.p.Index() != ix {
		t.Error("index read again")
	}
	// A broken index keeps the last good one.
	os.WriteFile(filepath.Join(dir, "api", "formula_names.txt"), nil, 0o644)
	os.Remove(filepath.Join(dir, "api", "cask_names.txt"))
	os.Remove(filepath.Join(dir, "api", "formula_names.txt"))
	e.p.refreshIndex(true)
	if e.p.Index() != ix {
		t.Error("index dropped")
	}
}

func TestInstalledView(t *testing.T) {
	e := newEnv(t, nil)
	v := e.view(cmdInstalled, "", nil)
	var titles []string
	for _, s := range v.Sections {
		titles = append(titles, s.Title+": "+strings.Join(func() []string {
			var out []string
			for _, it := range s.Items {
				out = append(out, it.ID)
			}
			return out
		}(), ","))
	}
	want := []string{
		"Updates Available: formula/abseil,cask/visual-studio-code,formula/ripgrep,cask/wechat,formula/wget",
		"Apps (Casks): cask/firefox",
		"List: refresh",
	}
	if !slices.Equal(titles, want) {
		t.Errorf("sections:\n%s", strings.Join(titles, "\n"))
	}
	code := item(t, v, "cask/visual-studio-code")
	if code.Title != "Microsoft Visual Studio Code" || code.Accessory != "1.34.0 → 1.142.0" {
		t.Errorf("code %q %q", code.Title, code.Accessory)
	}
	if !slices.Contains(code.Keywords, "VS Code") {
		t.Errorf("keywords %q", code.Keywords)
	}
	abseil := item(t, v, "formula/abseil")
	if !slices.ContainsFunc(abseil.Detail.Metadata, func(m plugin.Metadata) bool { return m.Value == "Installed as a dependency" }) {
		t.Errorf("abseil detail %+v", abseil.Detail.Metadata)
	}
	// The list is cached for the next start.
	if _, err := os.Stat(filepath.Join(e.h.CacheDir(), cacheFile)); err != nil {
		t.Error(err)
	}
	if n := e.brew.count("info --json=v2 --installed"); n != 1 {
		t.Errorf("%d listings", n)
	}
}

func TestInstalledCache(t *testing.T) {
	dir := t.TempDir()
	st := &installedState{Items: []Installed{{Kind: KindFormula, Name: "jq", Version: "1.8", OnRequest: true}}, At: now}
	if err := saveInstalledCache(dir, st); err != nil {
		t.Fatal(err)
	}
	got, err := loadInstalledCache(dir)
	if err != nil || len(got.Items) != 1 || got.Items[0].Name != "jq" || !got.At.Equal(now) {
		t.Errorf("%+v %v", got, err)
	}
}

func TestOutdatedView(t *testing.T) {
	e := newEnv(t, nil)
	v := e.view(cmdOutdated, "", nil)
	got := ids(v)
	want := []string{"upgrade-all", "formula/abseil", "cask/visual-studio-code", "formula/ripgrep", "cask/wechat", "formula/wget", "refresh"}
	if !slices.Equal(got, want) {
		t.Errorf("items %q", got)
	}
	all := item(t, v, "upgrade-all")
	if all.Title != "Upgrade All 5 Packages" || all.Actions[0].Style != plugin.StyleDestructive {
		t.Errorf("upgrade all %q %+v", all.Title, all.Actions[0])
	}
}

func TestInfoView(t *testing.T) {
	e := newEnv(t, nil)
	v := e.view(viewInfo, "", infoRequest{Kind: KindFormula, Name: "wget"})
	it := item(t, v, "info/formula/wget")
	if !strings.Contains(it.Detail.Markdown, "### Dependencies\n\nlibidn2, libpsl, openssl@4, gettext, libunistring") ||
		!strings.Contains(it.Detail.Markdown, "### Build Dependencies\n\npkgconf") {
		t.Errorf("markdown %q", it.Detail.Markdown)
	}
	if got := actionIDs(it); !slices.Equal(got, []string{"upgrade", "homepage", "copy-name", "refresh", "uninstall"}) {
		t.Errorf("actions %q", got)
	}
	ff := item(t, e.view(viewInfo, "", infoRequest{Kind: KindCask, Name: "firefox"}), "info/cask/firefox")
	meta := map[string]string{}
	for _, m := range ff.Detail.Metadata {
		meta[m.Label] = m.Value
	}
	if meta["Apps"] != "Firefox.app" || !strings.Contains(meta["Conflicts With"], "firefox@beta") || meta["Status"] != "Updates itself" {
		t.Errorf("firefox meta %v", meta)
	}
	// Details are cached.
	e.view(viewInfo, "", infoRequest{Kind: KindFormula, Name: "wget"})
	if n := e.brew.count("info --json=v2 --formula wget"); n != 1 {
		t.Errorf("%d brew info", n)
	}
	// brew's error is shown.
	bad := e.view(viewInfo, "", infoRequest{Kind: KindFormula, Name: "nosuch"})
	if !strings.Contains(bad.EmptyText, `No available formula with the name "nosuch"`) {
		t.Errorf("error %q", bad.EmptyText)
	}
	if bad := e.view(viewInfo, "", infoRequest{Kind: KindFormula, Name: "--force"}); !strings.Contains(bad.EmptyText, "not a valid") {
		t.Errorf("option as name: %q", bad.EmptyText)
	}
}

func TestOperations(t *testing.T) {
	e := newEnv(t, nil)
	search := e.view(cmdSearch, "gh", nil)
	gh := item(t, search, "formula/gh")
	installed := e.view(cmdInstalled, "", nil)
	wget := item(t, installed, "formula/wget")
	ff := item(t, installed, "cask/firefox")
	code := item(t, installed, "cask/visual-studio-code")

	res := e.run1(action(t, gh, "install").Do)
	if res.Toast == nil || res.Toast.Title != "Install gh: running in the background" {
		t.Errorf("toast %+v", res.Toast)
	}
	e.run1(action(t, item(t, e.view(cmdSearch, "iterm", nil), "cask/iterm2"), "install").Do)
	e.run1(action(t, wget, "upgrade").Do)
	e.run1(action(t, code, "upgrade").Do)
	e.run1(action(t, wget, "uninstall").Do)
	e.run1(action(t, ff, "uninstall").Do)
	e.run1(action(t, ff, "zap").Do)
	e.run1(action(t, item(t, e.view(cmdOutdated, "", nil), "upgrade-all"), "upgrade-all").Do)
	e.run1(plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdUpgradeAll})
	e.run1(plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdUpdate})
	e.h.Prefs.Set(prefAutoUpdate, jsontext.Value("false"))
	e.run1(action(t, gh, "install").Do)
	e.h.WaitTasks()
	want := []string{
		"brew install --formula gh",
		"brew install --cask iterm2",
		"brew upgrade --formula wget",
		"brew upgrade --cask visual-studio-code",
		"brew uninstall --formula wget",
		"brew uninstall --cask firefox",
		"brew uninstall --cask --zap firefox",
		"brew upgrade",
		"brew upgrade",
		"brew update",
		"brew install --formula gh",
	}
	if got := e.commands(); !slices.Equal(got, want) {
		t.Errorf("commands:\n%s", strings.Join(got, "\n"))
	}
	cmds := e.h.Runner.Commands()
	if a := cmds[0].Argv; !slices.Equal(a, []string{"/opt/homebrew/bin/brew", "install", "--formula", "gh"}) {
		t.Errorf("argv %q", a)
	}
	if slices.Contains(cmds[0].Env, "HOMEBREW_NO_AUTO_UPDATE=1") || !slices.Contains(cmds[len(cmds)-1].Env, "HOMEBREW_NO_AUTO_UPDATE=1") {
		t.Errorf("env %q / %q", cmds[0].Env, cmds[len(cmds)-1].Env)
	}
	for _, task := range e.h.Tasks.List() {
		if task.Queue != "homebrew" || task.Notify != plugin.NotifyAlways || task.Plugin != ID {
			t.Errorf("task %+v", task)
		}
	}
	// Every operation lists the installed packages again (Sync: at once).
	if n := e.brew.count("info --json=v2 --installed"); n != 1+len(want) {
		t.Errorf("%d listings after %d operations", n, len(want))
	}
}

func TestBadRequests(t *testing.T) {
	e := newEnv(t, nil)
	for _, req := range []opRequest{
		{Kind: opInstall, Type: KindFormula, Name: "--force"},
		{Kind: opInstall, Type: KindFormula, Name: "a b"},
		{Kind: opInstall, Type: KindFormula, Name: "x;rm"},
		{Kind: opInstall, Type: "bottle", Name: "wget"},
		{Kind: opZap, Type: KindFormula, Name: "wget"},
		{Kind: "rm", Type: KindFormula, Name: "wget"},
	} {
		res := e.run1(plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackOp, Payload: plugin.MustPayload(req)})
		if res.Toast == nil || res.Toast.Style != plugin.ToastFailure {
			t.Errorf("%+v: %+v", req, res.Toast)
		}
	}
	// Names of other taps are fine.
	if _, args, err := e.p.plan(opRequest{Kind: opInstall, Type: KindFormula, Name: "user/tap/tool@2"}); err != nil ||
		!slices.Equal(args, []string{"install", "--formula", "user/tap/tool@2"}) {
		t.Errorf("tap name: %q %v", args, err)
	}
	e.h.WaitTasks()
	if n := len(e.commands()); n != 0 {
		t.Errorf("%d tasks", n)
	}
	if _, err := e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: "nope"}); err == nil {
		t.Error("unknown action accepted")
	}
}

func TestRootItems(t *testing.T) {
	e := newEnv(t, nil)
	got, err := e.p.RootItems(t.Context(), plugin.Query{Text: "brew firefox"})
	if err != nil || len(got) == 0 || got[0].ID != "cask/firefox" || got[0].Group != "Homebrew" || got[0].Score != rootScore || got[0].Detail != nil {
		t.Fatalf("%+v %v", got, err)
	}
	for _, q := range []string{"firefox", "brew", "brew ", "brewfirefox"} {
		if got, _ := e.p.RootItems(t.Context(), plugin.Query{Text: q}); len(got) != 0 {
			t.Errorf("%q: %d items", q, len(got))
		}
	}
	e.h.Prefs.Set(prefShowInRoot, jsontext.Value("false"))
	if got, _ := e.p.RootItems(t.Context(), plugin.Query{Text: "brew firefox"}); len(got) != 0 {
		t.Errorf("showInRoot off: %d items", len(got))
	}
}

func TestNotInstalled(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Exe = "" })
	for _, id := range []string{cmdSearch, cmdInstalled, cmdOutdated} {
		v := e.view(id, "wget", nil)
		it := item(t, v, "not-installed")
		if it.Title != "Homebrew is not installed" || it.Actions[0].Do.Target != "https://brew.sh" || it.Actions[0].Do.Kind != plugin.ActionOpen {
			t.Errorf("%s: %+v", id, it)
		}
		if !strings.Contains(it.Detail.Markdown, "install.sh") {
			t.Errorf("detail %q", it.Detail.Markdown)
		}
	}
	got, _ := e.p.RootItems(t.Context(), plugin.Query{Text: "brew wget"})
	if len(got) != 1 || got[0].ID != "not-installed" {
		t.Errorf("root %+v", got)
	}
	res := e.run1(plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdUpgradeAll})
	if res.Toast == nil || res.Toast.Style != plugin.ToastFailure {
		t.Errorf("toast %+v", res.Toast)
	}
	e.h.WaitTasks()
	if len(e.commands()) != 0 || len(e.brew.calls) != 0 {
		t.Errorf("ran %q %q", e.commands(), e.brew.calls)
	}
}

func TestLocales(t *testing.T) {
	read := func(name string) map[string]jsontext.Value {
		b, err := locales.ReadFile("locales/" + name)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]jsontext.Value
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	en, zh := read("en.json"), read("zh-CN.json")
	for k := range en {
		if _, ok := zh[k]; !ok {
			t.Errorf("zh-CN lacks %s", k)
		}
	}
	for k := range zh {
		if _, ok := en[k]; !ok {
			t.Errorf("zh-CN has extra %s", k)
		}
	}
}
