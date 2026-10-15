// Package terminal is the builtin.terminal plugin: the profiles of Windows
// Terminal and the WSL distributions in the root search, with actions to
// open them (in a new window, a new tab, as administrator, in the folder of
// the context), and the WSL commands (set the default, stop a distribution,
// shut down or update WSL). See docs/terminal-wsl.md.
//
// The plugin never starts anything itself to show its items: profiles come
// from settings.json and the fragment files, distributions from the
// registry, and only the running state is asked from `wsl --list
// --verbose`, which does not start WSL. Opening a terminal is an ActionOpen
// carried out by the host; the WSL commands go through Options.Run.
package terminal

import (
	"context"
	"embed"
	"encoding/json/v2"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// ID is the plugin ID.
const ID = "builtin.terminal"

// Callbacks.
const (
	cbSetDefault = "setDefault"
	cbTerminate  = "terminate"
	cbShutdown   = "shutdown"
)

// listTTL is how long the running states of `wsl --list --verbose` are
// reused (docs/terminal-wsl.md §2).
const listTTL = 5 * time.Second

// listTimeout bounds `wsl --list --verbose`.
const listTimeout = 5 * time.Second

// wslExe is the WSL command line tool, found on PATH in System32.
const wslExe = "wsl.exe"

//go:embed locales/*.json
var locales embed.FS

// Runner runs a console program without a window and returns its output.
type Runner func(ctx context.Context, argv ...string) ([]byte, error)

// Options configure the plugin. Zero fields use the real system.
type Options struct {
	// Getenv reads environment variables (default os.Getenv).
	Getenv func(string) string
	// LocalAppData and ProgramData are the folders settings.json and the
	// fragments are found in (default: %LOCALAPPDATA% and %ProgramData%).
	LocalAppData string
	ProgramData  string
	// Which finds "wt" (wt.exe); "" when Windows Terminal is not installed.
	Which func(name string) string
	// Registry reads the WSL distributions (default: the Lxss registry key).
	Registry func() (Registry, error)
	// Run runs wsl.exe (default: hidden, without a console window). Tests
	// and glctl pass a fake: nothing must be started or stopped for real.
	Run Runner
	// Sync refreshes on the calling goroutine instead of in the background,
	// so results are deterministic (glctl's fake environment).
	Sync bool
}

// Plugin is the terminal plugin.
type Plugin struct {
	o  Options
	h  plugin.Host
	wg sync.WaitGroup // background refreshes (Host.Go)

	ctx    context.Context
	cancel context.CancelFunc
	unsub  func()

	mu       sync.Mutex
	closed   bool
	hasWT    bool
	wt       string // the wt.exe to open
	sig      string // signature of the files the profiles were read from
	term     Terminal
	reg      Registry
	states   map[string]State // nil until listed
	listedAt time.Time
	listing  bool
}

var (
	_ plugin.Plugin         = (*Plugin)(nil)
	_ plugin.RootProvider   = (*Plugin)(nil)
	_ plugin.ActionHandler  = (*Plugin)(nil)
	_ plugin.LocaleProvider = (*Plugin)(nil)
)

// New returns the plugin for this computer.
func New() *Plugin { return NewWithOptions(Options{}) }

// NewWithOptions returns the plugin configured by o.
func NewWithOptions(o Options) *Plugin {
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.LocalAppData == "" {
		o.LocalAppData = o.Getenv("LOCALAPPDATA")
	}
	if o.ProgramData == "" {
		o.ProgramData = o.Getenv("ProgramData")
	}
	if o.Which == nil {
		o.Which = systemWhich(o.LocalAppData)
	}
	if o.Registry == nil {
		o.Registry = readRegistry
	}
	if o.Run == nil {
		o.Run = runHidden
	}
	return &Plugin{o: o}
}

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:            ID,
		Name:          "%plugin.name%",
		Description:   "%plugin.description%",
		Version:       "1.0.0",
		Icon:          plugin.Icon{Name: "terminal"},
		DefaultLocale: "en",
		Permissions:   []plugin.Permission{plugin.PermClipboard, plugin.PermExec},
	}
}

// Locales implements plugin.LocaleProvider.
func (p *Plugin) Locales() fs.FS {
	sub, err := fs.Sub(locales, "locales")
	if err != nil {
		panic(err)
	}
	return sub
}

// systemWhich finds Windows Terminal on this computer: on PATH, or as the
// app execution alias in %LOCALAPPDATA%\Microsoft\WindowsApps, which
// LookPath may not resolve.
func systemWhich(localAppData string) func(string) string {
	return func(name string) string {
		if p, err := exec.LookPath(name + ".exe"); err == nil {
			return p
		}
		if localAppData != "" {
			p := filepath.Join(localAppData, "Microsoft", "WindowsApps", name+".exe")
			if _, err := os.Lstat(p); err == nil {
				return p
			}
		}
		return ""
	}
}

// Init implements plugin.Plugin. The plugin is unavailable when neither
// Windows Terminal nor a WSL distribution is installed.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.h = h
	wt := p.o.Which("wt")
	settings := findSettings(p.o.LocalAppData)
	reg, err := p.o.Registry()
	if err != nil {
		h.Logger().Warn("terminal: reading the WSL distributions", "err", err)
	}
	if wt == "" && settings == "" && len(reg.Distros) == 0 {
		return fmt.Errorf("%s: %w", h.I18n().T("unavailable.notInstalled"), plugin.ErrUnavailable)
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.hasWT = wt != "" || settings != ""
	p.wt = wt
	if p.wt == "" {
		p.wt = "wt.exe"
	}
	p.reg = reg
	p.reloadSettings()
	if p.o.Sync {
		p.listStates(p.ctx, true)
	} else {
		p.spawn(func() { p.refresh(p.ctx, true) })
	}
	p.unsub = h.Events().Subscribe(plugin.EventWindowShown, p.onShown)
	return nil
}

// Close implements plugin.Plugin: it stops and waits for background work.
func (p *Plugin) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	if p.unsub != nil {
		p.unsub()
	}
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	return nil
}

// spawn runs fn through Host.Go unless the plugin is closed.
func (p *Plugin) spawn(fn func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.wg.Add(1)
	p.h.Go(func() {
		defer p.wg.Done()
		fn()
	})
}

// onShown checks for changes each time the launcher is shown: a newer
// settings.json or fragment, a new distribution, a changed running state.
func (p *Plugin) onShown() {
	if p.o.Sync {
		p.refresh(p.ctx, false)
		return
	}
	p.spawn(func() { p.refresh(p.ctx, false) })
}

// refresh reloads what changed and asks the host to pull the root results
// again if anything did. forceList lists the running states even when the
// last listing is recent.
func (p *Plugin) refresh(ctx context.Context, forceList bool) {
	changed := p.reloadSettings()
	if p.reloadRegistry() {
		changed = true
	}
	if p.listStates(ctx, forceList) {
		changed = true
	}
	if changed {
		p.h.Invalidate("")
	}
}

// reloadSettings reads the profiles again when settings.json or a fragment
// changed (by modification time and size). It reports whether they did.
func (p *Plugin) reloadSettings() bool {
	settings := findSettings(p.o.LocalAppData)
	fragments := fragmentFiles(fragmentRoots(p.o.LocalAppData, p.o.ProgramData))
	sig := signature(settings, fragments)
	p.mu.Lock()
	same := sig == p.sig
	p.mu.Unlock()
	if same {
		return false
	}
	t, err := loadTerminal(settings, fragments, p.o.Getenv)
	for _, e := range t.Problems {
		p.h.Logger().Warn("terminal: reading a fragment", "err", e)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sig = sig
	if err != nil {
		// A file being saved, or a typo: keep the profiles read before. The
		// next save changes the signature and is read again.
		p.h.Logger().Warn("terminal: reading the Windows Terminal settings", "err", err)
		return false
	}
	if settings != "" {
		p.hasWT = true
	}
	p.term = t
	return true
}

// reloadRegistry reads the distributions again and reports whether they
// changed.
func (p *Plugin) reloadRegistry() bool {
	reg, err := p.o.Registry()
	if err != nil {
		p.h.Logger().Warn("terminal: reading the WSL distributions", "err", err)
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if reflect.DeepEqual(reg, p.reg) {
		return false
	}
	p.reg = reg
	return true
}

// listStates runs `wsl --list --verbose` for the running states unless they
// were listed less than listTTL ago (or force), and reports whether they
// changed. Without distributions nothing is run.
func (p *Plugin) listStates(ctx context.Context, force bool) bool {
	now := p.h.Now()
	p.mu.Lock()
	if len(p.reg.Distros) == 0 {
		changed := p.states != nil
		p.states = nil
		p.mu.Unlock()
		return changed
	}
	if p.listing || (!force && !p.listedAt.IsZero() && now.Sub(p.listedAt) < listTTL && now.Sub(p.listedAt) >= 0) {
		p.mu.Unlock()
		return false
	}
	p.listing = true
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	out, err := p.o.Run(ctx, wslExe, "--list", "--verbose")
	cancel()
	states := ParseListVerbose(DecodeOutput(out))

	p.mu.Lock()
	defer p.mu.Unlock()
	p.listing = false
	p.listedAt = now
	if err != nil && len(states) == 0 {
		p.h.Logger().Warn("terminal: wsl --list --verbose", "err", err, "output", firstLine(DecodeOutput(out)))
		return false
	}
	if p.states != nil && maps.Equal(states, p.states) {
		return false
	}
	p.states = states
	return true
}

// snapshot is a consistent copy of the plugin's data for building items.
type snapshot struct {
	hasWT  bool
	wt     string
	term   Terminal
	reg    Registry
	states map[string]State
}

func (p *Plugin) snapshot() snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return snapshot{hasWT: p.hasWT, wt: p.wt, term: p.term, reg: p.reg, states: p.states}
}

// RootItems implements plugin.RootProvider: a profile per Windows Terminal
// profile, a distribution per WSL distribution and the WSL commands, built
// from what was read before (no I/O here).
func (p *Plugin) RootItems(_ context.Context, q plugin.Query) ([]plugin.Item, error) {
	folder := ""
	if q.Context != nil {
		folder = q.Context.Folder
	}
	return buildItems(p.h.I18n(), p.snapshot(), folder), nil
}

// callbackPayload is the data of the WSL callbacks.
type callbackPayload struct {
	Name string `json:"name,omitempty"`
}

// HandleAction implements plugin.ActionHandler for the WSL commands.
func (p *Plugin) HandleAction(ctx context.Context, a plugin.ActionDo) (plugin.Result, error) {
	if a.Kind != plugin.ActionCallback {
		return plugin.Result{}, fmt.Errorf("terminal: unknown action %s %q", a.Kind, a.Target)
	}
	var pl callbackPayload
	if len(a.Payload) > 0 {
		if err := json.Unmarshal(a.Payload, &pl); err != nil {
			return plugin.Result{}, fmt.Errorf("terminal: %w", err)
		}
	}
	l := p.h.I18n()
	var (
		argv []string
		done string
	)
	switch a.Target {
	case cbSetDefault:
		argv, done = []string{wslExe, "-s", pl.Name}, l.T("toast.defaultSet", "name", pl.Name)
	case cbTerminate:
		argv, done = []string{wslExe, "-t", pl.Name}, l.T("toast.terminated", "name", pl.Name)
	case cbShutdown:
		argv, done = []string{wslExe, "--shutdown"}, l.T("toast.shutdown")
	default:
		return plugin.Result{}, fmt.Errorf("terminal: unknown callback %q", a.Target)
	}
	if a.Target != cbShutdown && pl.Name == "" {
		return plugin.Result{}, fmt.Errorf("terminal: %s without a distribution", a.Target)
	}
	out, err := p.o.Run(ctx, argv...)
	if err != nil {
		msg := firstLine(DecodeOutput(out))
		if msg == "" {
			msg = err.Error()
		}
		return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastFailure,
			Title: l.T("toast.failed", "command", strings.Join(argv, " ")), Message: msg}}, nil
	}
	p.refresh(ctx, true)
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: done}}, nil
}

// firstLine returns the first non-empty line of s, trimmed.
func firstLine(s string) string {
	for line := range strings.Lines(s) {
		if line = strings.TrimSpace(strings.ReplaceAll(line, "\x00", "")); line != "" {
			return line
		}
	}
	return ""
}
