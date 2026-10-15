// Package system is the builtin.system plugin (docs/system-commands.md):
// system commands (lock, sleep, hibernate, restart, shut down, sign out,
// empty the Recycle Bin, toggle dark mode, volume, turn off the display,
// show the desktop) and shortcuts to about 150 Windows Settings pages and
// the classic administrative tools, all as root search items.
//
// Every call that changes the machine goes through the System interface, so
// tests and glctl use a Fake that only records them.
package system

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"time"

	winsys "github.com/eziq1/golauncher/internal/winapi/system"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// ID is the plugin ID.
const ID = "builtin.system"

// Names of the system commands: the callback targets and item IDs.
const (
	cmdLock        = "lock"
	cmdSleep       = "sleep"
	cmdHibernate   = "hibernate"
	cmdRestart     = "restart"
	cmdShutDown    = "shutdown"
	cmdSignOut     = "signout"
	cmdEmptyBin    = "emptyBin"
	cmdDarkMode    = "darkMode"
	cmdMute        = "mute"
	cmdVolumeUp    = "volumeUp"
	cmdVolumeDown  = "volumeDown"
	cmdDisplayOff  = "displayOff"
	cmdShowDesktop = "showDesktop"
)

const (
	// defaultDelay lets the launcher hide (and the Enter key be released)
	// before the computer sleeps or the display turns off.
	defaultDelay = 500 * time.Millisecond
	// volumeSteps is the number of key presses per volume change (2% each).
	volumeSteps = 5
	// showDesktopCLSID is the shell's "Show desktop" command (ToggleDesktop).
	showDesktopCLSID = "shell:::{3080F90D-D7AD-11D9-BD98-0000947B0257}"
)

//go:embed locales/*.json
var locales embed.FS

// Options configure the plugin.
type Options struct {
	// System is the machine to act on; nil for this computer.
	System System
	// Sync reads the Recycle Bin and the theme in Init rather than in the
	// background, so the first results are complete (glctl, tests).
	Sync bool
}

// Plugin is the System Commands & Settings plugin.
type Plugin struct {
	sys   System
	sync  bool
	delay time.Duration
	pages []page

	h       plugin.Host
	build   int
	edition string
	hiber   bool
	unsub   func()
	wg      sync.WaitGroup
	stop    chan struct{}

	mu    sync.Mutex
	state state
	cache *itemCache
}

// state is what the subtitles show; it is read in the background.
type state struct {
	bin    winsys.RecycleBin
	binOK  bool
	dark   bool
	darkOK bool
}

// itemCache holds the items that change only with the locale.
type itemCache struct {
	locale   string
	commands map[string]plugin.Item
	static   []plugin.Item // settings pages and tools
}

var (
	_ plugin.Plugin         = (*Plugin)(nil)
	_ plugin.RootProvider   = (*Plugin)(nil)
	_ plugin.ActionHandler  = (*Plugin)(nil)
	_ plugin.LocaleProvider = (*Plugin)(nil)
)

// New returns the plugin acting on this computer.
func New() *Plugin { return NewWithOptions(Options{}) }

// NewWithOptions returns the plugin configured by o.
func NewWithOptions(o Options) *Plugin {
	sys := o.System
	if sys == nil {
		sys = realSystem{}
	}
	return &Plugin{sys: sys, sync: o.Sync, delay: defaultDelay}
}

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:            ID,
		Name:          "%plugin.name%",
		Description:   "%plugin.description%",
		Version:       "1.0.0",
		Icon:          plugin.Icon{Name: "settings"},
		DefaultLocale: "en",
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

// Init implements plugin.Plugin. It reads the Windows version, edition and
// whether hibernation is enabled, and reads the Recycle Bin and the theme
// now and whenever the launcher is shown.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	pages, err := loadCatalog()
	if err != nil {
		return err
	}
	p.h, p.pages, p.stop = h, pages, make(chan struct{})
	p.build, p.edition, p.hiber = p.sys.OSBuild(), p.sys.EditionID(), p.sys.HibernateAvailable()
	if p.sync {
		p.refresh()
	} else {
		p.refreshLater()
	}
	p.unsub = h.Events().Subscribe(plugin.EventWindowShown, p.refreshLater)
	return nil
}

// Close implements plugin.Plugin. Commands waiting to run (sleep, display
// off) are dropped.
func (p *Plugin) Close() error {
	if p.unsub != nil {
		p.unsub()
	}
	if p.stop != nil {
		close(p.stop)
	}
	p.wg.Wait()
	return nil
}

// refresh reads the Recycle Bin and the theme (read-only calls).
func (p *Plugin) refresh() {
	bin, binErr := p.sys.RecycleBin()
	dark, darkErr := p.sys.DarkMode()
	st := state{bin: bin, binOK: binErr == nil, dark: dark, darkOK: darkErr == nil}
	p.mu.Lock()
	changed := p.state != st
	p.state = st
	p.mu.Unlock()
	if binErr != nil {
		p.h.Logger().Debug("system: recycle bin", "err", binErr)
	}
	if changed {
		p.h.Invalidate("")
	}
}

// refreshLater runs refresh in the background.
func (p *Plugin) refreshLater() {
	select {
	case <-p.stop:
		return // closed
	default:
	}
	p.wg.Add(1)
	p.h.Go(func() {
		defer p.wg.Done()
		p.refresh()
	})
}

// RootItems implements plugin.RootProvider: all commands, settings pages and
// tools, matched by the host.
func (p *Plugin) RootItems(_ context.Context, _ plugin.Query) ([]plugin.Item, error) {
	l := p.h.I18n()
	p.mu.Lock()
	c := p.cache
	if c == nil || c.locale != l.Locale() {
		c = p.buildCache(l)
		p.cache = c
	}
	st := p.state
	p.mu.Unlock()

	order := []string{cmdLock, cmdSleep, cmdHibernate, cmdRestart, cmdShutDown, cmdSignOut, cmdEmptyBin,
		cmdDarkMode, cmdMute, cmdVolumeUp, cmdVolumeDown, cmdDisplayOff, cmdShowDesktop}
	out := make([]plugin.Item, 0, len(order)+len(c.static))
	for _, name := range order {
		if name == cmdHibernate && !p.hiber {
			continue
		}
		it := c.commands[name]
		switch {
		case name == cmdEmptyBin && st.binOK:
			it.Subtitle = binSummary(l, st.bin)
		case name == cmdDarkMode && st.darkOK:
			it.Subtitle = l.T("dark.isLight")
			if st.dark {
				it.Subtitle = l.T("dark.isDark")
			}
		}
		out = append(out, it)
	}
	return append(out, c.static...), nil
}

// command describes a system command.
type command struct {
	name     string
	title    string
	keywords string
	icon     string
	// destructive commands ask first; ends marks those ending the session.
	destructive, ends bool
	// do overrides the callback (Show Desktop opens a shell command).
	do *plugin.ActionDo
}

// commands returns the system commands in the current locale.
func commands(l plugin.Localizer) []command {
	return []command{
		{name: cmdLock, title: l.T("cmd.lock.title"), keywords: l.T("cmd.lock.keywords"), icon: "lock"},
		{name: cmdSleep, title: l.T("cmd.sleep.title"), keywords: l.T("cmd.sleep.keywords"), icon: "moon"},
		{name: cmdHibernate, title: l.T("cmd.hibernate.title"), keywords: l.T("cmd.hibernate.keywords"), icon: "snowflake"},
		{name: cmdRestart, title: l.T("cmd.restart.title"), keywords: l.T("cmd.restart.keywords"), icon: "rotate-cw", destructive: true, ends: true},
		{name: cmdShutDown, title: l.T("cmd.shutdown.title"), keywords: l.T("cmd.shutdown.keywords"), icon: "power", destructive: true, ends: true},
		{name: cmdSignOut, title: l.T("cmd.signout.title"), keywords: l.T("cmd.signout.keywords"), icon: "log-out", destructive: true, ends: true},
		{name: cmdEmptyBin, title: l.T("cmd.emptyBin.title"), keywords: l.T("cmd.emptyBin.keywords"), icon: "trash", destructive: true},
		{name: cmdDarkMode, title: l.T("cmd.darkMode.title"), keywords: l.T("cmd.darkMode.keywords"), icon: "contrast"},
		{name: cmdMute, title: l.T("cmd.mute.title"), keywords: l.T("cmd.mute.keywords"), icon: "volume-x"},
		{name: cmdVolumeUp, title: l.T("cmd.volumeUp.title"), keywords: l.T("cmd.volumeUp.keywords"), icon: "volume-2"},
		{name: cmdVolumeDown, title: l.T("cmd.volumeDown.title"), keywords: l.T("cmd.volumeDown.keywords"), icon: "volume-1"},
		{name: cmdDisplayOff, title: l.T("cmd.displayOff.title"), keywords: l.T("cmd.displayOff.keywords"), icon: "monitor-off"},
		{name: cmdShowDesktop, title: l.T("cmd.showDesktop.title"), keywords: l.T("cmd.showDesktop.keywords"), icon: "monitor",
			do: &plugin.ActionDo{Kind: plugin.ActionOpen, Target: "explorer.exe", Payload: plugin.MustPayload(plugin.OpenOptions{Args: showDesktopCLSID})}},
	}
}

// buildCache builds the items that depend only on the locale. Called with
// p.mu held.
func (p *Plugin) buildCache(l plugin.Localizer) *itemCache {
	c := &itemCache{locale: l.Locale(), commands: map[string]plugin.Item{}}
	group := l.T("group.system")
	for _, cmd := range commands(l) {
		a := plugin.Action{ID: "run", Title: cmd.title, EndsSession: cmd.ends,
			Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: cmd.name}}
		if cmd.destructive {
			a.Style = plugin.StyleDestructive
		}
		if cmd.do != nil {
			a.Do = *cmd.do
		}
		c.commands[cmd.name] = plugin.Item{
			ID:       "system:" + cmd.name,
			Title:    cmd.title,
			Icon:     plugin.Icon{Name: cmd.icon},
			Keywords: withPinyin(splitKeywords(cmd.keywords)),
			Group:    group,
			Actions:  []plugin.Action{a},
		}
	}

	group = l.T("group.settings")
	cats := categories(l)
	zh := strings.HasPrefix(l.Locale(), "zh")
	openTitle, copyTitle := l.T("action.open"), l.T("action.copyURI")
	for _, pg := range p.pages {
		if !pg.availableOn(p.build) {
			continue
		}
		title, other := pg.En, pg.Zh
		if zh {
			title, other = pg.Zh, pg.En
		}
		// Only the Chinese title is spelled in pinyin: pinyin initials of
		// every keyword would match almost any short query.
		kw := append([]string{other, pinyinOf(pg.Zh)}, pg.Keywords...)
		c.static = append(c.static, plugin.Item{
			ID:        "settings:" + strings.TrimPrefix(pg.URI, settingsPrefix),
			Title:     title,
			Accessory: cats[pg.Category],
			Icon:      plugin.Icon{Name: "settings"},
			Keywords:  slices.DeleteFunc(kw, func(s string) bool { return s == "" }),
			Group:     group,
			Actions: []plugin.Action{
				{ID: "open", Title: openTitle, Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: pg.URI}},
				{ID: "copy-uri", Title: copyTitle, Shortcut: "Ctrl+Shift+C", Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: pg.URI}},
			},
		})
	}

	runAsTitle := l.T("action.runAs")
	for _, t := range tools(l) {
		if t.pro && winsys.HomeEdition(p.edition) {
			continue
		}
		open := plugin.ActionDo{Kind: plugin.ActionOpen, Target: t.target}
		elevated := plugin.ActionDo{Kind: plugin.ActionOpen, Target: t.target,
			Payload: plugin.MustPayload(plugin.OpenOptions{Args: t.args, Verb: "runas"})}
		if t.args != "" {
			open.Payload = plugin.MustPayload(plugin.OpenOptions{Args: t.args})
		}
		actions := []plugin.Action{
			{ID: "open", Title: openTitle, Do: open},
			{ID: "runas", Title: runAsTitle, Shortcut: "Ctrl+Shift+Enter", Do: elevated},
		}
		if t.admin {
			actions = []plugin.Action{{ID: "runas", Title: runAsTitle, Do: elevated}}
		}
		c.static = append(c.static, plugin.Item{
			ID:        "tool:" + t.id,
			Title:     t.title,
			Subtitle:  t.shown,
			Accessory: cats["tools"],
			Icon:      plugin.Icon{Name: t.icon},
			Keywords:  withTitlePinyin(splitKeywords(t.keywords)),
			Group:     group,
			Actions:   actions,
		})
	}
	return c
}

// binSummary describes the content of the Recycle Bin: "3 items · 1.5 MB".
func binSummary(l plugin.Localizer, bin winsys.RecycleBin) string {
	if bin.Items == 0 {
		return l.T("bin.empty")
	}
	return l.T("bin.summary", "items", l.N("bin.items", int(bin.Items)), "size", formatSize(l, bin.Size))
}

// formatSize formats a byte count with the locale's number format.
func formatSize(l plugin.Localizer, n int64) string {
	const kb, mb, gb = 1 << 10, 1 << 20, 1 << 30
	round := func(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }
	switch {
	case n < kb:
		return l.N("size.bytes", int(n))
	case n < mb:
		return l.T("size.kb", "size", l.Number(round(float64(n)/kb)))
	case n < gb:
		return l.T("size.mb", "size", l.Number(round(float64(n)/mb)))
	default:
		return l.T("size.gb", "size", l.Number(round(float64(n)/gb)))
	}
}

// HandleAction implements plugin.ActionHandler: it runs a system command.
func (p *Plugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	if a.Kind != plugin.ActionCallback {
		return plugin.Result{}, fmt.Errorf("system: unexpected action %s", a.Kind)
	}
	l := p.h.I18n()
	hide := plugin.Result{Hide: true}
	var err error
	switch a.Target {
	case cmdLock:
		err = p.sys.Lock()
	case cmdSleep:
		p.later(cmdSleep, p.sys.Sleep)
		return hide, nil
	case cmdHibernate:
		p.later(cmdHibernate, p.sys.Hibernate)
		return hide, nil
	case cmdDisplayOff:
		p.later(cmdDisplayOff, p.sys.DisplayOff)
		return hide, nil
	case cmdRestart:
		err = p.sys.Restart()
	case cmdShutDown:
		err = p.sys.ShutDown()
	case cmdSignOut:
		err = p.sys.SignOut()
	case cmdEmptyBin:
		return p.emptyBin(l), nil
	case cmdDarkMode:
		return p.toggleDarkMode(l), nil
	case cmdMute:
		return p.result(cmdMute, p.sys.PressMediaKey(winsys.VolumeMute, 1), plugin.Result{}), nil
	case cmdVolumeUp:
		return p.result(cmdVolumeUp, p.sys.PressMediaKey(winsys.VolumeUp, volumeSteps), plugin.Result{}), nil
	case cmdVolumeDown:
		return p.result(cmdVolumeDown, p.sys.PressMediaKey(winsys.VolumeDown, volumeSteps), plugin.Result{}), nil
	default:
		return plugin.Result{}, fmt.Errorf("system: unknown command %q", a.Target)
	}
	return p.result(a.Target, err, hide), nil
}

// result returns ok, or a failure toast for err.
func (p *Plugin) result(name string, err error, ok plugin.Result) plugin.Result {
	if err == nil {
		return ok
	}
	p.h.Logger().Warn("system: command failed", "command", name, "err", err)
	return plugin.Result{Toast: p.failure(name, err)}
}

// failure is the toast of a failed command.
func (p *Plugin) failure(name string, err error) *plugin.Toast {
	return &plugin.Toast{Style: plugin.ToastFailure, Title: p.h.I18n().T("toast.failed", "command", p.title(name)), Message: err.Error()}
}

// title returns the title of command name in the current locale.
func (p *Plugin) title(name string) string {
	for _, c := range commands(p.h.I18n()) {
		if c.name == name {
			return c.title
		}
	}
	return name
}

// later runs fn after the launcher had time to hide, unless the plugin is
// closed first. A failure is shown as a toast.
func (p *Plugin) later(name string, fn func() error) {
	p.wg.Add(1)
	p.h.Go(func() {
		defer p.wg.Done()
		if p.delay > 0 {
			t := time.NewTimer(p.delay)
			defer t.Stop()
			select {
			case <-t.C:
			case <-p.stop:
				return
			}
		}
		if err := fn(); err != nil {
			p.h.Logger().Warn("system: command failed", "command", name, "err", err)
			p.h.Toast(*p.failure(name, err))
		}
	})
}

// emptyBin empties the Recycle Bin unless it is already empty.
func (p *Plugin) emptyBin(l plugin.Localizer) plugin.Result {
	if bin, err := p.sys.RecycleBin(); err == nil && bin.Items == 0 {
		return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: l.T("toast.binAlreadyEmpty")}}
	}
	if err := p.sys.EmptyRecycleBin(); err != nil {
		return p.result(cmdEmptyBin, err, plugin.Result{})
	}
	p.refresh()
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("toast.binEmptied")}}
}

// toggleDarkMode switches between the dark and the light theme.
func (p *Plugin) toggleDarkMode(l plugin.Localizer) plugin.Result {
	dark, err := p.sys.DarkMode()
	if err == nil {
		err = p.sys.SetDarkMode(!dark)
	}
	if err != nil {
		return p.result(cmdDarkMode, err, plugin.Result{})
	}
	p.refresh()
	title := l.T("toast.darkOn")
	if dark {
		title = l.T("toast.lightOn")
	}
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: title}}
}
