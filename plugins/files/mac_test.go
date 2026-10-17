package files

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/winapi/macterm"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// macEnv is the plugin in its macOS mode with fake apps; launches are
// recorded, nothing is started.
type macEnv struct {
	p         *Plugin
	h         *hostfake.Host
	mu        sync.Mutex
	launches  []macterm.Launch
	fail      error
	preferred string
}

func newMacPlugin(t *testing.T, bundles ...string) *macEnv {
	t.Helper()
	e := &macEnv{}
	apps := map[string]string{}
	for _, b := range bundles {
		apps[b] = "/Applications/" + b + ".app"
	}
	e.p = NewWithOptions(Options{
		Mac:               true,
		Which:             func(name string) string { t.Errorf("looked for Windows program %q", name); return "" },
		AppPath:           func(id string) string { return apps[id] },
		PreferredTerminal: func() string { return e.preferred },
		Launch: func(l macterm.Launch) error {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.launches = append(e.launches, l)
			return e.fail
		},
		Shell: "/bin/zsh",
	})
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

func (e *macEnv) do(t *testing.T, it plugin.Item, id string) plugin.Result {
	t.Helper()
	i := slices.IndexFunc(it.Actions, func(a plugin.Action) bool { return a.ID == id })
	if i < 0 {
		t.Fatalf("%s has no action %q", it.ID, id)
	}
	r, err := e.p.HandleAction(t.Context(), it.Actions[i].Do)
	if err != nil {
		t.Fatal(err)
	}
	e.p.wg.Wait()
	return r
}

func (e *macEnv) last(t *testing.T) macterm.Launch {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.launches) == 0 {
		t.Fatal("nothing launched")
	}
	return e.launches[len(e.launches)-1]
}

func actionIDs(it plugin.Item) []string {
	var out []string
	for _, a := range it.Actions {
		out = append(out, a.ID)
	}
	return out
}

func TestMacFolderItems(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Mac paths (AppleScript, open) of a real folder; macOS and Unix only")
	}
	dir := filepath.Join(t.TempDir(), "My Project")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	e := newMacPlugin(t, "com.apple.Terminal", "com.googlecode.iterm2", "com.mitchellh.ghostty", "com.microsoft.VSCode", "dev.zed.Zed")
	items, _ := e.p.ContextItems(t.Context(), folderCtx(dir))
	if got, want := ids(items), []string{"terminal", "vscode", "zed", "reveal", "new-folder", "new-file", "copy-path"}; !slices.Equal(got, want) {
		t.Fatalf("items %v, want %v", got, want)
	}
	if e.p.Manifest().Description != "%mac.plugin.description%" {
		t.Error("Windows description on macOS")
	}

	// The automatic default is iTerm2; the others are further actions.
	term := find(t, items, "terminal")
	if term.Title != "Open in iTerm2" || !slices.Equal(actionIDs(term), []string{"open", "new-tab", "open-terminal", "open-ghostty", "copy-path"}) {
		t.Errorf("terminal: %q %v", term.Title, actionIDs(term))
	}
	if r := e.do(t, term, "new-tab"); !r.Hide {
		t.Errorf("result %+v", r)
	}
	if got := strings.Join(e.last(t).Argv, "\n"); !strings.Contains(got, `tell application id "com.googlecode.iterm2"`) ||
		!strings.Contains(got, `write text "cd '`+dir+`' && clear"`) || !strings.Contains(got, "create tab") {
		t.Errorf("iTerm2 tab:\n%s", got)
	}
	e.do(t, term, "open-ghostty")
	if got := e.last(t).Argv; !slices.Equal(got, []string{"/usr/bin/open", "-na", "/Applications/com.mitchellh.ghostty.app", "--args", "--working-directory=" + dir}) {
		t.Errorf("Ghostty: %q", got)
	}

	// The terminal chosen in builtin.terminal.
	e.preferred = "terminal"
	items, _ = e.p.ContextItems(t.Context(), folderCtx(dir))
	term = find(t, items, "terminal")
	if term.Title != "Open in Terminal" {
		t.Errorf("preferred terminal: %q", term.Title)
	}
	e.do(t, term, "open")
	if got := e.last(t).Argv; !slices.Equal(got, []string{"/usr/bin/open", "-a", "/Applications/com.apple.Terminal.app", dir}) {
		t.Errorf("Terminal window: %q", got)
	}

	vs := find(t, items, "vscode")
	if vs.Title != "Open Folder in VS Code" {
		t.Errorf("vscode title %q", vs.Title)
	}
	e.do(t, vs, "open")
	if got := e.last(t).Argv; !slices.Equal(got, []string{"/usr/bin/open", "-b", "com.microsoft.VSCode", dir}) {
		t.Errorf("VS Code: %q", got)
	}
	e.do(t, find(t, items, "zed"), "open")
	if got := e.last(t).Argv; !slices.Equal(got, []string{"/usr/bin/open", "-b", "dev.zed.Zed", dir}) {
		t.Errorf("Zed: %q", got)
	}

	rv := find(t, items, "reveal").Actions[0]
	if rv.Do.Kind != plugin.ActionOpen || rv.Do.Target != dir || !openOpts(t, rv).Reveal || rv.Title != "Show in Finder" {
		t.Errorf("reveal: %+v", rv)
	}
}

func TestMacSelectionItems(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "notes.txt")
	img := filepath.Join(dir, "a b.png")
	os.WriteFile(doc, []byte("x"), 0o644)
	os.WriteFile(img, []byte("x"), 0o644)
	e := newMacPlugin(t, "com.apple.Terminal", "com.todesktop.230313mzl4w4u92")
	c := folderCtx(dir)
	c.Selected = []string{doc, img}
	items, _ := e.p.ContextItems(t.Context(), c)
	want := []string{"copy-selected", "cursor-selected", "quick-look", "reveal-selected", "zip"}
	if got := ids(items); !slices.Equal(got[:len(want)], want) {
		t.Fatalf("items %v, want %v first", got, want)
	}
	if slices.Contains(ids(items), "run-admin") {
		t.Error("run as administrator on macOS")
	}
	if cs := find(t, items, "copy-selected"); cs.Actions[0].Do.Target != doc+"\n"+img {
		t.Errorf("copy paths %q", cs.Actions[0].Do.Target)
	}
	e.do(t, find(t, items, "quick-look"), "preview")
	if l := e.last(t); !slices.Equal(l.Argv, []string{"/usr/bin/qlmanage", "-p", doc, img}) || !l.Detach {
		t.Errorf("Quick Look: %+v", l)
	}
	e.do(t, find(t, items, "cursor-selected"), "open")
	if got := e.last(t).Argv; !slices.Equal(got, []string{"/usr/bin/open", "-b", "com.todesktop.230313mzl4w4u92", doc, img}) {
		t.Errorf("Cursor: %q", got)
	}
	if rv := find(t, items, "reveal-selected").Actions[0]; rv.Do.Target != doc || !openOpts(t, rv).Reveal {
		t.Errorf("reveal: %+v", rv.Do)
	}

	// One file: its checksum, still no administrator.
	c.Selected = []string{doc}
	items, _ = e.p.ContextItems(t.Context(), c)
	if !slices.Contains(ids(items), "sha256") {
		t.Errorf("one file: %v", ids(items))
	}
}

func TestMacNoApps(t *testing.T) {
	dir := t.TempDir()
	e := newMacPlugin(t)
	items, _ := e.p.ContextItems(t.Context(), folderCtx(dir))
	if got, want := ids(items), []string{"reveal", "new-folder", "new-file", "copy-path"}; !slices.Equal(got, want) {
		t.Errorf("items %v, want %v", got, want)
	}
}

func TestMacLaunchFailure(t *testing.T) {
	dir := t.TempDir()
	e := newMacPlugin(t, "com.apple.Terminal")
	e.preferred = "terminal"
	e.fail = errors.New("osascript: execution error: System Events got an error: osascript is not allowed to send keystrokes. (1002)")
	items, _ := e.p.ContextItems(t.Context(), folderCtx(dir))
	e.do(t, find(t, items, "terminal"), "new-tab")
	toasts := e.h.Toasts()
	if len(toasts) != 1 || toasts[0].Title != "Could not open Terminal" || !strings.Contains(toasts[0].Message, "Accessibility") {
		t.Errorf("toasts %+v", toasts)
	}
}
