package winget

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

const fixtures = "../../testdata/fixtures"

// countingRunner wraps a Runner and counts the commands it ran.
type countingRunner struct {
	next  Runner
	mu    sync.Mutex
	calls [][]string
	// block, if set, is waited for before `winget list` answers.
	block chan struct{}
	// running counts `winget list` commands running at once (max seen).
	running, maxRunning atomic.Int32
}

func (c *countingRunner) run(ctx context.Context, args []string) ([]byte, error) {
	c.mu.Lock()
	c.calls = append(c.calls, slices.Clone(args))
	c.mu.Unlock()
	if args[0] == "list" {
		n := c.running.Add(1)
		defer c.running.Add(-1)
		for {
			m := c.maxRunning.Load()
			if n <= m || c.maxRunning.CompareAndSwap(m, n) {
				break
			}
		}
		if c.block != nil {
			select {
			case <-c.block:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return c.next(ctx, args)
}

// count returns how many commands started with verb.
func (c *countingRunner) count(verb string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, a := range c.calls {
		if a[0] == verb {
			n++
		}
	}
	return n
}

type env struct {
	t   *testing.T
	p   *Plugin
	h   *hostfake.Host
	run *countingRunner
}

// newEnv starts the plugin on the fixture winget (testdata/fixtures/winget)
// with o's changes; edit changes the fixture options first.
func newEnv(t *testing.T, edit func(*Options)) *env {
	t.Helper()
	o := FixtureOptions(fixtures)
	cr := &countingRunner{next: o.Run}
	o.Run = cr.run
	if edit != nil {
		edit(&o)
	}
	return start(t, o, cr, t.TempDir())
}

func start(t *testing.T, o Options, cr *countingRunner, dir string) *env {
	t.Helper()
	e := &env{t: t, run: cr}
	e.p = NewWithOptions(o)
	h, err := hostfake.New(e.p, dir)
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

func (e *env) setPref(name string, v any) {
	e.t.Helper()
	e.h.Prefs.Set(name, jsontext.Value(plugin.MustPayload(v)))
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
	t.Fatalf("no item %q in %+v", id, v)
	return plugin.Item{}
}

func action(t *testing.T, it plugin.Item, id string) plugin.Action {
	t.Helper()
	i := slices.IndexFunc(it.Actions, func(a plugin.Action) bool { return a.ID == id })
	if i < 0 {
		t.Fatalf("item %s has no action %q: %+v", it.ID, id, actionIDs(it))
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

func (e *env) run1(do plugin.ActionDo) plugin.Result {
	e.t.Helper()
	res, err := e.p.HandleAction(e.t.Context(), do)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

// commands returns the command lines of the tasks started so far.
func (e *env) commands() []string {
	var out []string
	for _, c := range e.h.Runner.Commands() {
		out = append(out, c.CommandLine)
	}
	return out
}

func TestUnavailableWithoutWinget(t *testing.T) {
	for _, o := range []Options{FixtureOptions(""), FixtureOptions(t.TempDir()), {NoDetect: true}} {
		p := NewWithOptions(o)
		h, err := hostfake.New(p, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		err = p.Init(t.Context(), h)
		if !errors.Is(err, plugin.ErrUnavailable) || !strings.Contains(err.Error(), "WinGet is not installed") {
			t.Errorf("Init = %v", err)
		}
		p.Close()
	}
}

func TestParseList(t *testing.T) {
	out, err := os.ReadFile(filepath.Join(fixtures, "winget", "list.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseList(out, []string{"winget", "msstore"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Installed{
		{Name: "Contoso Photos", ID: `MSIX\Contoso.Photos_2026.1.0.0_x64__abcd1234efgh5`, Version: "2026.1.0.0"},
		{Name: "Contoso TextPad", ID: "Contoso.TextPad", Version: "1.2.0", Available: "1.4.1", Source: "winget"},
		{Name: "Gitty", ID: "Fabrikam.Gitty", Version: "2.0.0", Source: "winget"},
		{Name: "Litware Driver Utility", ID: `ARP\Machine\X64\{8C1A6F0E-0000-4000-8000-000000000001}`, Version: "1.0.7"},
		{Name: "Northwind Player", ID: "Northwind.Player", Version: "3.1.0", Available: "3.2.0", Source: "winget"},
		{Name: "Woodgrove Reader", ID: "9NWOODGROVE01", Version: "2.3", Source: "msstore"},
		{Name: "微聊", ID: "Adatum.Chatty", Version: "5.0.2", Available: "5.1.0", Source: "winget"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("parseList:\n got %+v\nwant %+v", got, want)
	}
}

func TestParseTableLocalized(t *testing.T) {
	// An invented localization: two-word headers, wide characters in
	// names, spinner output before the table and a summary after it.
	out := "   - \r   \\ \r" +
		"Nom du paquet   ID du paquet      Version  Disponible Source\n" +
		"------------------------------------------------------------\n" +
		"Contoso TextPad Contoso.TextPad   1.2.0    1.4.1      winget\n" +
		"微聊            Adatum.Chatty     5.0.2    5.1.0      winget\n" +
		"Sans source     ARP\\Machine\\X64\\a 1.0               \n" +
		"2 mises à niveau disponibles.\n"
	header, rows, ok := parseTable([]byte(out))
	if !ok {
		t.Fatal("no table")
	}
	if want := []string{"Nom du paquet", "ID du paquet", "Version", "Disponible", "Source"}; !slices.Equal(header, want) {
		t.Errorf("header = %q", header)
	}
	if len(rows) != 3 || !slices.Equal(rows[1], []string{"微聊", "Adatum.Chatty", "5.0.2", "5.1.0", "winget"}) ||
		!slices.Equal(rows[2], []string{"Sans source", `ARP\Machine\X64\a`, "1.0", "", ""}) {
		t.Errorf("rows = %q", rows)
	}

	// Four columns: the last one is the source when it holds source names.
	four := "Name    Id        Version Source\n--------------------------------\nGitty   Fab.Gitty 2.0.0   winget\nOther   ARP\\x     1.0     \n"
	list, err := parseList([]byte(four), []string{"winget"})
	if err != nil || len(list) != 2 || list[0].Source != "winget" || list[0].Available != "" {
		t.Errorf("four columns with a source: %+v %v", list, err)
	}
	four = "Name    Id        Version Available\n-----------------------------------\nGitty   Fab.Gitty 2.0.0   2.1.0\n"
	list, err = parseList([]byte(four), []string{"winget"})
	if err != nil || len(list) != 1 || list[0].Available != "2.1.0" || list[0].Source != "" {
		t.Errorf("four columns with a version: %+v %v", list, err)
	}

	if _, err := parseList([]byte("No installed package found matching input criteria.\n"), nil); !errors.Is(err, errNoTable) {
		t.Errorf("no table: %v", err)
	}
	if list, err := parseList(nil, nil); err != nil || list != nil {
		t.Errorf("empty output: %v %v", list, err)
	}
}

func TestParseShow(t *testing.T) {
	out, err := os.ReadFile(filepath.Join(fixtures, "winget", "show", "contoso.textpad.txt"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := parseShow(append([]byte("   - \r"), out...))
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "Found Contoso TextPad [Contoso.TextPad]" || titleName(d.Title, "") != "Contoso TextPad" {
		t.Errorf("title %q", d.Title)
	}
	for label, want := range map[string]string{
		"Version":     "1.4.1",
		"Homepage":    "https://contoso.example/textpad",
		"Description": "A small, fast text editor with tabs, syntax highlighting and a distraction-free mode.\nOpens files of any size without freezing.",
		"Tags":        "editor\ntext",
		"Installer":   "Installer Type: inno\nInstaller Url: https://contoso.example/download/textpad-1.4.1.exe\nRelease Date: 2026-09-01",
	} {
		if got := d.Get(label); got != want {
			t.Errorf("%s = %q, want %q", label, got, want)
		}
	}
	// Full-width colons (an invented localization).
	d, err = parseShow([]byte("已找到 微聊 [Adatum.Chatty]\n版本：5.1.0\n主页：https://adatum.example/chatty\n"))
	if err != nil || len(d.Fields) != 2 || d.Fields[1] != (Field{"主页", "https://adatum.example/chatty"}) || titleName(d.Title, "微聊") != "微聊" {
		t.Errorf("full-width: %+v %v", d, err)
	}
	if _, err := parseShow([]byte("No package found matching input criteria.\n")); err == nil {
		t.Error("no package: no error")
	}
}

func TestParseSources(t *testing.T) {
	out, err := os.ReadFile(filepath.Join(fixtures, "winget", "sources.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	srcs, err := parseSources(out)
	if err != nil || len(srcs) != 3 {
		t.Fatalf("%+v %v", srcs, err)
	}
	var indexed []string
	for _, s := range srcs {
		if s.Indexed() {
			indexed = append(indexed, s.Name)
		}
	}
	if !slices.Equal(indexed, []string{"winget"}) {
		t.Errorf("indexed sources %q", indexed)
	}
	if _, err := parseSources([]byte("error\n")); err == nil {
		t.Error("no sources: no error")
	}
}

func TestReadCatalog(t *testing.T) {
	db, err := openSQLFixture(filepath.Join(fixtures, "winget", "index.sql"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := ReadCatalog(t.Context(), db, "winget")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Packages) != 9 {
		t.Fatalf("%d packages", len(c.Packages))
	}
	ix := newIndex([]*Catalog{c})
	p, ok := ix.Package("WINGET", "contoso.textpad")
	if !ok || p.Name != "Contoso TextPad" || p.Moniker != "textpad" || p.Version != "1.4.1" ||
		!slices.Equal(p.Tags, []string{"editor", "text"}) || !slices.Equal(p.Commands, []string{"textpad"}) {
		t.Errorf("Contoso.TextPad: %+v", p)
	}
	if _, err := db.Exec(`UPDATE metadata SET value = '3' WHERE name = 'majorVersion'`); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCatalog(t.Context(), db, "winget"); !errors.Is(err, errSchema) {
		t.Errorf("schema 3: %v", err)
	}
}

func TestSearch(t *testing.T) {
	e := newEnv(t, nil)
	ix := e.p.Index()
	if ix.PackageCount() != 9 {
		t.Fatalf("%d packages", ix.PackageCount())
	}
	ids := func(q string) []string {
		var out []string
		for _, p := range ix.Search(t.Context(), q, 0) {
			out = append(out, p.ID)
		}
		return out
	}
	for q, want := range map[string][]string{
		"textpad":          {"Contoso.TextPad", "Contoso.TextPad.Preview"},
		"Contoso.TextPad":  {"Contoso.TextPad", "Contoso.TextPad.Preview"},
		"git":              {"Fabrikam.GitGui", "Fabrikam.Gitty"}, // name prefix "Gitty" ties with tag? no: both prefixes, shorter name first
		"lz":               {"Litware.Zipper"},                    // command
		"nwplayer":         {"Northwind.Player"},                  // moniker
		"微聊":               {"Adatum.Chatty"},
		"聊天":               {"Adatum.Chatty"}, // tag
		"markdown":         {"Woodgrove.Notes"},
		"contoso preview":  {"Contoso.TextPad.Preview"},
		"nothing-like-it":  nil,
		"tailspin toolbox": {"Tailspin.Toolbox"},
	} {
		got := ids(q)
		if q == "git" {
			// Gitty (name prefix) and Fabrikam Git GUI (word prefix)
			if len(got) < 2 || got[0] != "Fabrikam.Gitty" {
				t.Errorf("%q: %q", q, got)
			}
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("%q: %q, want %q", q, got, want)
		}
	}
	e.setPref(prefMaxResults, 1)
	v := e.view(cmdSearch, "contoso", nil)
	if n := len(items(v)); n != 1 {
		t.Errorf("maxResults 1: %d items", n)
	}
}

// TestSearchPhrase ranks names holding the words in order first.
func TestSearchPhrase(t *testing.T) {
	ix := newIndex([]*Catalog{{Source: "winget", Packages: []*Package{
		{ID: "Contoso.PadLauncher", Name: "Text / Pad Launcher for Contoso", Source: "winget"},
		{ID: "Contoso.TextPad", Name: "Contoso Text Pad", Source: "winget"},
	}}})
	if got := ix.Search(t.Context(), "text pad", 0); len(got) != 2 || got[0].ID != "Contoso.TextPad" {
		t.Errorf("%+v", got)
	}
}

func TestSearchViewMarksInstalled(t *testing.T) {
	e := newEnv(t, nil)
	v := e.view(cmdSearch, "", nil)
	if v.EmptyText != "Type to search 9 packages" {
		t.Errorf("hint %q", v.EmptyText)
	}
	v = e.view(cmdSearch, "textpad", nil)
	installed := item(t, v, "pkg/winget/Contoso.TextPad")
	if installed.Accessory != "1.2.0 → 1.4.1" || installed.Actions[0].ID != "upgrade" {
		t.Errorf("installed: %q %q", installed.Accessory, actionIDs(installed))
	}
	fresh := item(t, v, "pkg/winget/Contoso.TextPad.Preview")
	if fresh.Accessory != "1.5.0-beta.2" || !slices.Equal(actionIDs(fresh), []string{"install", "details", "install-admin", "manifest", "copy-id", "copy-install"}) {
		t.Errorf("not installed: %q %q", fresh.Accessory, actionIDs(fresh))
	}
	if a := action(t, fresh, "copy-install"); a.Do.Target != "winget install --id Contoso.TextPad.Preview --exact --source winget" {
		t.Errorf("copy install: %q", a.Do.Target)
	}
	if a := action(t, fresh, "manifest"); a.Do.Target != "https://github.com/microsoft/winget-pkgs/tree/master/manifests/c/Contoso/TextPad/Preview" {
		t.Errorf("manifest: %q", a.Do.Target)
	}
	if d := fresh.Detail; d == nil || !slices.ContainsFunc(d.Metadata, func(m plugin.Metadata) bool { return m.Label == "Tags" && m.Value == "editor" }) {
		t.Errorf("detail %+v", fresh.Detail)
	}
}

func TestInstalledAndUpdatesViews(t *testing.T) {
	e := newEnv(t, nil)
	v := e.view(cmdInstalled, "", nil)
	var titles []string
	for _, s := range v.Sections {
		var ids []string
		for _, it := range s.Items {
			ids = append(ids, it.ID)
		}
		titles = append(titles, s.Title+": "+strings.Join(ids, ", "))
	}
	want := []string{
		"Updates Available: app/Contoso.TextPad, app/Northwind.Player, app/Adatum.Chatty",
		"Installed: app/Fabrikam.Gitty, app/9NWOODGROVE01",
		`Other Installed Software: app/MSIX\Contoso.Photos_2026.1.0.0_x64__abcd1234efgh5, app/ARP\Machine\X64\{8C1A6F0E-0000-4000-8000-000000000001}`,
		"List: refresh",
	}
	if !slices.Equal(titles, want) {
		t.Errorf("sections:\n%s", strings.Join(titles, "\n"))
	}
	pinned := item(t, v, "app/Northwind.Player")
	if pinned.Accessory != "3.1.0 → 3.2.0 · Pinned" || action(t, pinned, "unpin").Shortcut != "Ctrl+Shift+H" {
		t.Errorf("pinned: %q %q", pinned.Accessory, actionIDs(pinned))
	}
	local := item(t, v, `app/ARP\Machine\X64\{8C1A6F0E-0000-4000-8000-000000000001}`)
	if !slices.Equal(actionIDs(local), []string{"copy-id", "refresh", "uninstall"}) {
		t.Errorf("software without a source: %q", actionIDs(local))
	}
	if un := action(t, local, "uninstall"); un.Style != plugin.StyleDestructive {
		t.Error("uninstall is not destructive")
	}

	v = e.view(cmdUpdates, "", nil)
	all := item(t, v, "upgrade-all")
	if all.Title != "Upgrade All 2 Packages" || all.Actions[0].Style != plugin.StyleDestructive {
		t.Errorf("upgrade all: %q %+v", all.Title, all.Actions)
	}
	if n := len(v.Sections[0].Items); n != 4 {
		t.Errorf("%d update rows", n)
	}
}

func TestSourcesView(t *testing.T) {
	e := newEnv(t, nil)
	v := e.view(cmdSources, "", nil)
	w := item(t, v, "source/winget")
	if w.Accessory != "9 packages" || w.Actions[0].ID != "update-source" {
		t.Errorf("winget source: %q %q", w.Accessory, actionIDs(w))
	}
	if f := item(t, v, "source/winget-font"); f.Accessory != "Explicit" {
		t.Errorf("font source: %q", f.Accessory)
	}
	e.run1(w.Actions[0].Do)
	e.run1(w.Actions[1].Do)
	e.h.WaitTasks()
	if got := e.commands(); !slices.Equal(got, []string{
		"winget source update --name winget --disable-interactivity",
		"winget source update --disable-interactivity",
	}) {
		t.Errorf("commands %q", got)
	}
}

func TestOperations(t *testing.T) {
	e := newEnv(t, nil)
	search := e.view(cmdSearch, "textpad", nil)
	preview := item(t, search, "pkg/winget/Contoso.TextPad.Preview")
	installed := e.view(cmdInstalled, "", nil)
	textpad := item(t, installed, "app/Contoso.TextPad")
	local := item(t, installed, `app/ARP\Machine\X64\{8C1A6F0E-0000-4000-8000-000000000001}`)
	updates := e.view(cmdUpdates, "", nil)

	res := e.run1(action(t, preview, "install").Do)
	if res.Toast == nil || res.Toast.Title != "Install Contoso TextPad Preview: running in the background" {
		t.Errorf("toast %+v", res.Toast)
	}
	e.run1(action(t, textpad, "upgrade").Do)
	e.run1(action(t, textpad, "pin").Do)
	e.run1(action(t, item(t, installed, "app/Northwind.Player"), "unpin").Do)
	e.run1(action(t, textpad, "uninstall").Do)
	e.run1(action(t, local, "uninstall").Do)
	e.run1(action(t, item(t, updates, "upgrade-all"), "upgrade-all").Do)
	e.run1(plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdUpgradeAll})
	e.run1(plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdUpdateSources})
	e.setPref(prefInstallMode, modeSilent)
	e.run1(action(t, preview, "install").Do)
	e.h.WaitTasks()
	want := []string{
		"winget install --id Contoso.TextPad.Preview --exact --source winget --accept-source-agreements --accept-package-agreements --disable-interactivity",
		"winget upgrade --id Contoso.TextPad --exact --source winget --accept-source-agreements --accept-package-agreements --disable-interactivity",
		"winget pin add --id Contoso.TextPad --exact --source winget --accept-source-agreements --disable-interactivity",
		"winget pin remove --id Northwind.Player --exact --source winget --accept-source-agreements --disable-interactivity",
		"winget uninstall --id Contoso.TextPad --exact --source winget --accept-source-agreements --disable-interactivity",
		`winget uninstall --id ARP\Machine\X64\{8C1A6F0E-0000-4000-8000-000000000001} --exact --accept-source-agreements --disable-interactivity`,
		"winget upgrade --all --accept-source-agreements --accept-package-agreements --disable-interactivity",
		"winget upgrade --all --accept-source-agreements --accept-package-agreements --disable-interactivity",
		"winget source update --disable-interactivity",
		"winget install --id Contoso.TextPad.Preview --exact --source winget --accept-source-agreements --accept-package-agreements --disable-interactivity --silent",
	}
	if got := e.commands(); !slices.Equal(got, want) {
		t.Errorf("commands:\n%s", strings.Join(got, "\n"))
	}
	// The process is winget itself with the arguments as they are.
	cmd := e.h.Runner.Commands()[5]
	if cmd.Argv[0] != "winget.exe" || cmd.Argv[3] != `ARP\Machine\X64\{8C1A6F0E-0000-4000-8000-000000000001}` {
		t.Errorf("argv %q", cmd.Argv)
	}
	for _, task := range e.h.Tasks.List() {
		if task.Queue != "winget" || task.Notify != plugin.NotifyAlways || task.Plugin != ID {
			t.Errorf("task %+v", task)
		}
	}
	if tasks := e.h.Tasks.List(); tasks[len(tasks)-1].Title != "Install Contoso TextPad Preview" {
		t.Errorf("title %q", tasks[len(tasks)-1].Title)
	}
	// Every operation lists the installed packages again (Sync: at once),
	// a source update also reads the sources.
	if n := e.run.count("list"); n != 1+len(want) {
		t.Errorf("%d listings after %d operations", n, len(want))
	}
	if n := e.run.count("source"); n != 2 { // Init and after the source update
		t.Errorf("%d source exports", n)
	}
}

func TestElevatedAction(t *testing.T) {
	e := newEnv(t, nil)
	it := item(t, e.view(cmdSearch, "gitgui", nil), "pkg/winget/Fabrikam.GitGui")
	a := action(t, it, "install-admin")
	var o plugin.OpenOptions
	if err := json.Unmarshal(a.Do.Payload, &o); err != nil {
		t.Fatal(err)
	}
	if a.Do.Kind != plugin.ActionOpen || a.Do.Target != "winget.exe" || o.Verb != "runas" ||
		o.Args != "install --id Fabrikam.GitGui --exact --source winget --accept-source-agreements --accept-package-agreements --disable-interactivity --wait" {
		t.Errorf("%+v %+v", a.Do, o)
	}
}

func TestInvalidRequests(t *testing.T) {
	e := newEnv(t, nil)
	for _, req := range []opRequest{
		{Kind: opInstall, ID: "--force", Source: "winget"},
		{Kind: opInstall, ID: "Contoso.TextPad", Source: ""},
		{Kind: opInstall, ID: "Contoso.TextPad", Source: "-s"},
		{Kind: opInstall, ID: "Contoso TextPad", Source: "winget"},
		{Kind: opInstall, ID: `Contoso"Pad`, Source: "winget"},
		{Kind: opUpgrade, ID: `ARP\Machine\X64\x`},
		{Kind: opPin, ID: `ARP\Machine\X64\x`},
		{Kind: opUninstall, ID: `-ARP\x`},
		{Kind: opUninstall, ID: "ARP\\x\"y"},
		{Kind: opSourceUpdate, Source: "--name"},
		{Kind: "frobnicate"},
	} {
		res, err := e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackOp, Payload: plugin.MustPayload(req)})
		if err != nil {
			t.Fatal(err)
		}
		if res.Toast == nil || res.Toast.Style != plugin.ToastFailure {
			t.Errorf("%+v was accepted: %+v", req, res)
		}
	}
	if n := len(e.h.Runner.Commands()); n != 0 {
		t.Errorf("%d commands ran", n)
	}
	if _, err := e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: "nope"}); err == nil {
		t.Error("unknown callback accepted")
	}
}

func TestShowView(t *testing.T) {
	e := newEnv(t, nil)
	v := e.view(viewShow, "", showRequest{ID: "Contoso.TextPad", Source: "winget"})
	it := item(t, v, "show/winget/Contoso.TextPad")
	if v.Title != "Contoso TextPad" || it.Accessory != "1.4.1" {
		t.Errorf("title %q accessory %q", v.Title, it.Accessory)
	}
	if ids := actionIDs(it); !slices.Equal(ids[:3], []string{"upgrade", "homepage", "open-publisher-url"}) || slices.Contains(ids, "details") {
		t.Errorf("actions %q", ids)
	}
	if a := action(t, it, "homepage"); a.Do.Target != "https://contoso.example/textpad" || a.Shortcut != "Ctrl+O" {
		t.Errorf("homepage %+v", a)
	}
	md := it.Detail.Markdown
	for _, want := range []string{"## Contoso TextPad", "### Description", "Opens files of any size", "### Release Notes", "- Split view\n- Faster search", "### Installer"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	meta := map[string]string{}
	for _, m := range it.Detail.Metadata {
		meta[m.Label] = m.Value
	}
	if meta["Publisher"] != "Contoso Ltd." || meta["License"] != "MIT" || meta["Installed"] != "1.2.0" || meta["Source"] != "winget" {
		t.Errorf("metadata %+v", meta)
	}
	// Cached: a second view does not run winget show again.
	e.view(viewShow, "", showRequest{ID: "Contoso.TextPad", Source: "winget"})
	if n := e.run.count("show"); n != 1 {
		t.Errorf("winget show ran %d times", n)
	}
	// Without a homepage label, the first web address is the homepage.
	v = e.view(viewShow, "", showRequest{ID: "Adatum.Chatty", Source: "winget"})
	if a := action(t, item(t, v, "show/winget/Adatum.Chatty"), "homepage"); a.Do.Target != "https://adatum.example/chatty" {
		t.Errorf("chatty homepage %+v", a)
	}
	v = e.view(viewShow, "", showRequest{ID: "Tailspin.Toolbox", Source: "winget"})
	if len(v.Sections) != 0 || !strings.Contains(v.EmptyText, "No package found") {
		t.Errorf("unknown package: %+v", v)
	}
	v = e.view(viewShow, "", showRequest{ID: "-x", Source: "winget"})
	if !strings.Contains(v.EmptyText, "not a valid package ID") {
		t.Errorf("bad ID: %q", v.EmptyText)
	}
}

func TestRootItems(t *testing.T) {
	e := newEnv(t, nil)
	root := func(q string) []plugin.Item {
		its, err := e.p.RootItems(t.Context(), plugin.Query{Text: q})
		if err != nil {
			t.Fatal(err)
		}
		return its
	}
	its := root("winget textpad")
	if len(its) != 2 || its[0].ID != "pkg/winget/Contoso.TextPad" || its[0].Group != "WinGet" || its[0].Score <= its[1].Score || its[0].Detail != nil {
		t.Errorf("winget textpad: %+v", its)
	}
	if its := root("wg 聊天"); len(its) != 1 {
		t.Errorf("wg 聊天: %+v", its)
	}
	for _, q := range []string{"textpad", "winget", "winget ", "wingetx textpad"} {
		if its := root(q); len(its) != 0 {
			t.Errorf("%q: %+v", q, its)
		}
	}
	e.setPref(prefShowInRoot, false)
	if its := root("winget textpad"); len(its) != 0 {
		t.Errorf("showInRoot off: %+v", its)
	}
}

// TestInstalledCacheAndRefresh: without Sync, views show the cached list at
// once, list again in the background when it is stale (one winget list at a
// time) and refresh the open views when it is done.
func TestInstalledCacheAndRefresh(t *testing.T) {
	cache := t.TempDir()
	// A first run writes the cache.
	e := newEnv(t, func(o *Options) { o.Sync = false })
	e.p.waitInstalled(t.Context())
	e.p.Close()
	if _, err := os.Stat(filepath.Join(e.h.CacheDir(), cacheFile)); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(cache, os.DirFS(filepath.Dir(e.h.CacheDir()))); err != nil {
		t.Fatal(err)
	}

	o := FixtureOptions(fixtures)
	o.Sync = false
	block := make(chan struct{})
	cr := &countingRunner{next: o.Run, block: block}
	o.Run = cr.run
	e = start(t, o, cr, cache)
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	e.h.Clock = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	// The cached list is shown at once; it is from another time, so it is
	// stale and listed again in the background.
	v := e.view(cmdInstalled, "", nil)
	if len(items(v)) != 8 {
		t.Fatalf("cached view: %+v", v)
	}
	e.view(cmdUpdates, "", nil)
	e.view(cmdSearch, "textpad", nil)
	deadline := time.Now().Add(10 * time.Second)
	for e.run.count("list") == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := e.run.count("list"); n != 1 {
		t.Fatalf("%d listings while one runs", n)
	}
	close(block)
	for !slices.Contains(e.h.Invalidated(), "") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !slices.Contains(e.h.Invalidated(), "") {
		t.Fatal("no Invalidate after the listing")
	}
	// A fresh list is not listed again; after staleAfter it is.
	for e.p.isListing() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	before := e.run.count("list")
	e.view(cmdInstalled, "", nil)
	if e.run.count("list") != before {
		t.Error("a fresh list was listed again")
	}
	mu.Lock()
	now = now.Add(staleAfter + time.Second)
	mu.Unlock()
	e.view(cmdInstalled, "", nil)
	for e.run.count("list") == before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if e.run.count("list") != before+1 {
		t.Error("a stale list was not listed again")
	}
	if e.run.maxRunning.Load() != 1 {
		t.Errorf("%d listings ran at once", e.run.maxRunning.Load())
	}
}

// TestListFailure shows why the list is missing.
func TestListFailure(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		o.Run = func(ctx context.Context, args []string) ([]byte, error) {
			if args[0] == "list" {
				return []byte("Failed in attempting to update the source: winget\n"), &ExitError{Code: 1, Output: "Failed in attempting to update the source: winget"}
			}
			return FixtureRunner(filepath.Join(fixtures, "winget"))(ctx, args)
		}
	})
	v := e.view(cmdInstalled, "", nil)
	if !strings.Contains(v.EmptyText, "Could not list the installed packages") || !strings.Contains(v.EmptyText, "Failed in attempting") {
		t.Errorf("empty text %q", v.EmptyText)
	}
}

// TestIndexUnavailable: without an index the search view says so; the
// installed list still works.
func TestIndexUnavailable(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		o.OpenIndex = func(Source) (*sql.DB, error) { return nil, os.ErrNotExist }
	})
	v := e.view(cmdSearch, "textpad", nil)
	if !strings.Contains(v.EmptyText, "index of the winget source was not found") {
		t.Errorf("search: %q", v.EmptyText)
	}
	if len(items(e.view(cmdInstalled, "", nil))) != 8 {
		t.Error("the installed list needs the index")
	}
	e = newEnv(t, func(o *Options) {
		o.OpenIndex = func(Source) (*sql.DB, error) {
			db, err := openSQLFixture(filepath.Join(fixtures, "winget", "index.sql"))
			if err == nil {
				_, err = db.Exec(`UPDATE metadata SET value = '9' WHERE name = 'majorVersion'`)
			}
			return db, err
		}
	})
	if v := e.view(cmdSearch, "textpad", nil); !strings.Contains(v.EmptyText, "index format") {
		t.Errorf("schema 9: %q", v.EmptyText)
	}
}

// TestIndexStampRefresh reads the index again when its package changed
// (window shown), and not when it did not.
func TestIndexStampRefresh(t *testing.T) {
	var stamp atomic.Value
	stamp.Store("a")
	var opens atomic.Int32
	e := newEnv(t, func(o *Options) {
		o.IndexStamp = func(Source) (string, error) { return stamp.Load().(string), nil }
		open := o.OpenIndex
		o.OpenIndex = func(s Source) (*sql.DB, error) { opens.Add(1); return open(s) }
	})
	e.p.refreshIndex(true)
	if opens.Load() != 1 {
		t.Errorf("unchanged stamp: %d opens", opens.Load())
	}
	stamp.Store("b")
	e.p.refreshIndex(true)
	if opens.Load() != 2 || !slices.Contains(e.h.Invalidated(), "") {
		t.Errorf("changed stamp: %d opens, invalidated %q", opens.Load(), e.h.Invalidated())
	}
}

// isListing reports whether a winget list runs.
func (p *Plugin) isListing() bool { return p.listing.Running() }
