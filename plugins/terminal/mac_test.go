package terminal

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/winapi/macterm"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// macEnv is the plugin in its macOS mode with fake apps: nothing is ever
// started, the launches are recorded.
type macEnv struct {
	p        *Plugin
	h        *hostfake.Host
	mu       sync.Mutex
	apps     map[string]string // bundle id → path
	launches []macterm.Launch
	fail     error
	shared   []string // SetPreferred calls
}

func newMacEnv(t *testing.T, bundles ...string) *macEnv {
	t.Helper()
	e := &macEnv{apps: map[string]string{}}
	for _, b := range bundles {
		e.apps[b] = "/Applications/" + b + ".app"
	}
	e.p = NewWithOptions(Options{
		Mac:  true,
		Sync: true,
		AppPath: func(id string) string {
			e.mu.Lock()
			defer e.mu.Unlock()
			return e.apps[id]
		},
		Launch: func(l macterm.Launch) error {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.launches = append(e.launches, l)
			return e.fail
		},
		Home:  "/Users/me",
		Shell: "/bin/zsh",
		SetPreferred: func(id string) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.shared = append(e.shared, id)
		},
		// The Windows side must not be looked at.
		Registry: func() (Registry, error) { t.Error("registry read on macOS"); return Registry{}, nil },
		Which:    func(string) string { t.Error("wt looked for on macOS"); return "" },
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

func (e *macEnv) items(t *testing.T, folder string) []plugin.Item {
	t.Helper()
	q := plugin.Query{}
	if folder != "" {
		q.Context = &plugin.Context{Kind: plugin.ContextExplorer, Folder: folder}
	}
	items, err := e.p.RootItems(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// do runs action id of item and waits for the launch it starts.
func (e *macEnv) do(t *testing.T, it plugin.Item, id string, values map[string]jsontext.Value) plugin.Result {
	t.Helper()
	i := slices.IndexFunc(it.Actions, func(a plugin.Action) bool { return a.ID == id })
	if i < 0 {
		t.Fatalf("%s has no action %q: %v", it.ID, id, actionIDs(it))
	}
	a := it.Actions[i].Do
	a.Values = values
	r, err := e.p.HandleAction(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	e.p.wg.Wait()
	return r
}

func (e *macEnv) last(t *testing.T) []string {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.launches) == 0 {
		t.Fatal("nothing launched")
	}
	return e.launches[len(e.launches)-1].Argv
}

func actionIDs(it plugin.Item) []string {
	var out []string
	for _, a := range it.Actions {
		out = append(out, a.ID)
	}
	return out
}

func itemIDs(items []plugin.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func macItem(t *testing.T, items []plugin.Item, id string) plugin.Item {
	t.Helper()
	i := slices.IndexFunc(items, func(it plugin.Item) bool { return it.ID == id })
	if i < 0 {
		t.Fatalf("no item %q in %v", id, itemIDs(items))
	}
	return items[i]
}

func TestMacUnavailable(t *testing.T) {
	p := NewWithOptions(Options{Mac: true, Sync: true, AppPath: func(string) string { return "" },
		Launch: func(macterm.Launch) error { return errors.New("launched") }})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); !errors.Is(err, plugin.ErrUnavailable) || !strings.Contains(err.Error(), "No terminal app") {
		t.Errorf("Init = %v", err)
	}
}

func TestMacManifest(t *testing.T) {
	e := newMacEnv(t, "com.apple.Terminal", "com.googlecode.iterm2", "dev.warp.Warp-Stable")
	m := e.p.Manifest()
	if m.Name != "%mac.plugin.name%" || len(m.Preferences) != 1 || m.Preferences[0].Name != prefTerminal || !m.Preferences[0].DynamicOptions {
		t.Errorf("manifest %+v", m)
	}
	opts, err := e.p.PreferenceOptions(t.Context(), prefTerminal)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range opts {
		got = append(got, o.Value+"="+o.Title)
	}
	if want := []string{"=Automatic (iTerm2)", "terminal=Terminal", "iterm2=iTerm2", "warp=Warp"}; !slices.Equal(got, want) {
		t.Errorf("options %q, want %q", got, want)
	}
	// The Windows manifest is unchanged.
	if w := NewWithOptions(Options{}).Manifest(); w.Name != "%plugin.name%" || len(w.Preferences) != 0 {
		t.Errorf("Windows manifest %+v", w)
	}
}

func TestMacItems(t *testing.T) {
	e := newMacEnv(t, "com.apple.Terminal", "com.mitchellh.ghostty", "dev.warp.Warp-Stable")
	items := e.items(t, "")
	if got, want := itemIDs(items), []string{"app:terminal", "app:ghostty", "app:warp", "run-command"}; !slices.Equal(got, want) {
		t.Fatalf("items %v, want %v", got, want)
	}
	// No WSL distributions or commands, no Windows Terminal profile.
	gh := macItem(t, items, "app:ghostty")
	if gh.Accessory != "Default" || !slices.Equal(actionIDs(gh), []string{"open"}) {
		t.Errorf("Ghostty (default, no tabs): %q %v", gh.Accessory, actionIDs(gh))
	}
	tm := macItem(t, items, "app:terminal")
	if tm.Accessory != "" || !slices.Equal(actionIDs(tm), []string{"open", "new-tab", "set-default"}) || tm.Subtitle != "Terminal app" {
		t.Errorf("Terminal: %q %v %q", tm.Accessory, actionIDs(tm), tm.Subtitle)
	}
	run := macItem(t, items, "run-command")
	if run.Subtitle != "Run a shell command in Ghostty" || len(run.Arguments) != 1 || !run.Arguments[0].Required ||
		!slices.Equal(actionIDs(run), []string{"run"}) {
		t.Errorf("run: %+v", run)
	}

	items = e.items(t, "/Users/me/My Project")
	if got := actionIDs(macItem(t, items, "app:terminal")); !slices.Equal(got, []string{"open", "new-tab", "open-here", "set-default"}) {
		t.Errorf("Terminal with a folder: %v", got)
	}
	if got := actionIDs(macItem(t, items, "run-command")); !slices.Equal(got, []string{"run", "run-here"}) {
		t.Errorf("run with a folder: %v", got)
	}

	// Warp as the default cannot run commands: Terminal runs them.
	e.h.Prefs.Set(prefTerminal, plugin.MustPayload("warp"))
	items = e.items(t, "")
	if r := macItem(t, items, "run-command"); r.Subtitle != "Run a shell command in Terminal" || !slices.Equal(actionIDs(r), []string{"run", "run-tab"}) {
		t.Errorf("run with Warp the default: %q %v", r.Subtitle, actionIDs(r))
	}
	if macItem(t, items, "app:warp").Accessory != "Default" {
		t.Error("Warp is not marked as the default")
	}
}

func TestMacOpen(t *testing.T) {
	e := newMacEnv(t, "com.apple.Terminal", "com.googlecode.iterm2")
	e.h.Prefs.Set(prefTerminal, plugin.MustPayload("terminal"))
	items := e.items(t, "/Users/me/My Project")

	tm := macItem(t, items, "app:terminal")
	if r := e.do(t, tm, "open", nil); !r.Hide || r.Toast != nil {
		t.Errorf("result %+v", r)
	}
	if got := e.last(t); !slices.Equal(got, []string{"/usr/bin/open", "-a", "/Applications/com.apple.Terminal.app", "/Users/me"}) {
		t.Errorf("Terminal window at home: %q", got)
	}
	e.do(t, tm, "open-here", nil)
	if got := e.last(t); got[len(got)-1] != "/Users/me/My Project" {
		t.Errorf("Terminal here: %q", got)
	}
	e.do(t, tm, "new-tab", nil)
	if got := strings.Join(e.last(t), "\n"); !strings.Contains(got, "keystroke \"t\" using command down") || !strings.Contains(got, `do script "cd /Users/me && clear" in front window`) {
		t.Errorf("Terminal tab:\n%s", got)
	}

	it := macItem(t, items, "app:iterm2")
	e.do(t, it, "open-here", nil)
	if got := strings.Join(e.last(t), "\n"); !strings.Contains(got, `write text "cd '/Users/me/My Project' && clear"`) {
		t.Errorf("iTerm2 here:\n%s", got)
	}

	run := macItem(t, items, "run-command")
	e.do(t, run, "run-here", map[string]jsontext.Value{argCommand: plugin.MustPayload(`  echo "hi" && ls  `)})
	if got := strings.Join(e.last(t), "\n"); !strings.Contains(got, `do script "cd '/Users/me/My Project' && echo \"hi\" && ls"`) {
		t.Errorf("run here:\n%s", got)
	}
	n := len(e.launches)
	if r := e.do(t, run, "run", nil); r.Toast == nil || r.Toast.Style != plugin.ToastFailure || len(e.launches) != n {
		t.Errorf("run without a command: %+v", r)
	}
}

func TestMacLaunchFailure(t *testing.T) {
	e := newMacEnv(t, "com.googlecode.iterm2")
	e.fail = errors.New("osascript: execution error: Not authorized to send Apple events to iTerm. (-1743)")
	if r := e.do(t, macItem(t, e.items(t, ""), "app:iterm2"), "open", nil); !r.Hide {
		t.Errorf("result %+v", r)
	}
	toasts := e.h.Toasts()
	if len(toasts) != 1 || toasts[0].Style != plugin.ToastFailure || toasts[0].Title != "Could not open iTerm2" ||
		!strings.Contains(toasts[0].Message, "Privacy & Security › Automation") {
		t.Errorf("toasts %+v", toasts)
	}
}

func TestMacSetDefault(t *testing.T) {
	e := newMacEnv(t, "com.apple.Terminal", "com.googlecode.iterm2")
	if !slices.Equal(e.shared, []string{""}) {
		t.Errorf("shared at start %q", e.shared)
	}
	r := e.do(t, macItem(t, e.items(t, ""), "app:terminal"), "set-default", nil)
	var v string
	if err := json.Unmarshal(r.Preferences[prefTerminal], &v); err != nil || v != "terminal" || r.Toast == nil {
		t.Errorf("result %+v", r)
	}
	if e.shared[len(e.shared)-1] != "terminal" {
		t.Errorf("shared %q", e.shared)
	}
	// The host stores the preference and announces the change.
	e.h.Prefs.Set(prefTerminal, plugin.MustPayload("iterm2"))
	e.h.Bus.PublishSync(plugin.EventConfigChanged)
	if e.shared[len(e.shared)-1] != "iterm2" {
		t.Errorf("shared after the settings changed: %q", e.shared)
	}
}

func TestMacRedetect(t *testing.T) {
	e := newMacEnv(t, "com.apple.Terminal")
	e.mu.Lock()
	e.apps["net.kovidgoyal.kitty"] = "/Applications/kitty.app"
	e.mu.Unlock()
	e.h.Bus.PublishSync(plugin.EventWindowShown)
	if got := itemIDs(e.items(t, "")); !slices.Contains(got, "app:kitty") {
		t.Errorf("kitty installed meanwhile: %v", got)
	}
	if len(e.h.Invalidated()) == 0 {
		t.Error("no invalidation after a new terminal")
	}
}
