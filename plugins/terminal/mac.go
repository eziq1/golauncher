package terminal

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"

	"github.com/eziq1/golauncher/internal/winapi/macterm"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// On macOS (Options.Mac) the plugin lists the installed terminal apps
// (Terminal, iTerm2, Ghostty, WezTerm, Alacritty, kitty, Warp; found by
// bundle identifier) instead of Windows Terminal profiles and WSL
// distributions: open one in a new window or tab, in the folder of the
// context, run a command in it, and choose the default terminal, which
// builtin.files also opens (docs/terminal-wsl.md §5).

// prefTerminal is the preference holding the default terminal's ID ("" for
// the automatic choice).
const prefTerminal = "defaultTerminal"

// Callbacks of the macOS mode.
const (
	cbMacOpen       = "macOpen"
	cbMacSetDefault = "macSetDefault"
)

// argCommand is the argument of "Run in Terminal".
const argCommand = "command"

// macPayload is the data of the macOS callbacks.
type macPayload struct {
	Terminal string `json:"terminal"`
	Dir      string `json:"dir,omitempty"`
	Tab      bool   `json:"tab,omitzero"`
	// Run takes the command to run from the argument argCommand.
	Run bool `json:"run,omitzero"`
}

// initMac starts the macOS mode: unavailable without any terminal app.
func (p *Plugin) initMac(h plugin.Host) error {
	terms := macterm.Detect(p.o.AppPath)
	if len(terms) == 0 {
		return fmt.Errorf("%s: %w", h.I18n().T("mac.unavailable.notInstalled"), plugin.ErrUnavailable)
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.mu.Lock()
	p.terms = terms
	p.mu.Unlock()
	p.share()
	ev := h.Events()
	unsubs := []func(){
		ev.Subscribe(plugin.EventWindowShown, p.onShownMac),
		ev.Subscribe(plugin.EventConfigChanged, func() {
			p.share()
			p.h.Invalidate("")
		}),
	}
	p.unsub = func() {
		for _, u := range unsubs {
			u()
		}
	}
	return nil
}

// share tells builtin.files which terminal is the default.
func (p *Plugin) share() {
	p.o.SetPreferred(p.h.Preferences().String(prefTerminal))
}

// onShownMac looks for terminals installed or removed since.
func (p *Plugin) onShownMac() {
	redetect := func() {
		terms := macterm.Detect(p.o.AppPath)
		p.mu.Lock()
		same := slices.Equal(terms, p.terms)
		p.terms = terms
		p.mu.Unlock()
		if !same {
			p.h.Invalidate("")
		}
	}
	if p.o.Sync {
		redetect()
		return
	}
	p.spawn(redetect)
}

// macTerminals returns the installed terminals and the default one.
func (p *Plugin) macTerminals() ([]macterm.Installed, macterm.Installed) {
	p.mu.Lock()
	terms := slices.Clone(p.terms)
	p.mu.Unlock()
	def, _ := macterm.Pick(terms, p.h.Preferences().String(prefTerminal))
	return terms, def
}

// macManifest changes the manifest for macOS: other texts, and the
// default terminal as a preference.
func (p *Plugin) macManifest(m plugin.Manifest) plugin.Manifest {
	m.Name = "%mac.plugin.name%"
	m.Description = "%mac.plugin.description%"
	m.Preferences = []plugin.Preference{{
		Name: prefTerminal, Type: plugin.PrefSelect, Title: "%mac.pref.terminal.title%",
		Description: "%mac.pref.terminal.description%", Default: plugin.MustPayload(""), DynamicOptions: true,
	}}
	return m
}

// PreferenceOptions implements plugin.PreferenceOptionsProvider: the
// installed terminals, after the automatic choice.
func (p *Plugin) PreferenceOptions(_ context.Context, name string) ([]plugin.Option, error) {
	if name != prefTerminal || !p.o.Mac {
		return nil, nil
	}
	l := p.h.I18n()
	terms, _ := p.macTerminals()
	auto, _ := macterm.Auto(terms)
	opts := []plugin.Option{{Value: "", Title: l.T("mac.pref.terminal.auto", "app", auto.Name)}}
	for _, t := range terms {
		opts = append(opts, plugin.Option{Value: t.ID, Title: t.Name})
	}
	return opts, nil
}

func macCallback(id, title, shortcut, target string, pl macPayload) plugin.Action {
	return plugin.Action{ID: id, Title: title, Shortcut: shortcut,
		Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: target, Payload: plugin.MustPayload(pl)}}
}

// buildMacItems returns the root items on macOS: one per installed
// terminal, then "Run in Terminal". folder is the folder of the context
// ("" for none).
func buildMacItems(l plugin.Localizer, terms []macterm.Installed, def macterm.Installed, folder string) []plugin.Item {
	var items []plugin.Item
	for _, t := range terms {
		pl := macPayload{Terminal: t.ID}
		actions := []plugin.Action{macCallback("open", l.T("action.openWindow"), "", cbMacOpen, pl)}
		if t.Tabs {
			tab := pl
			tab.Tab = true
			actions = append(actions, macCallback("new-tab", l.T("action.openTab"), "Ctrl+Enter", cbMacOpen, tab))
		}
		if folder != "" {
			here := pl
			here.Dir = folder
			actions = append(actions, macCallback("open-here", l.T("action.openHere", "folder", folder), "", cbMacOpen, here))
		}
		it := plugin.Item{
			ID:       "app:" + t.ID,
			Title:    t.Name,
			Subtitle: l.T("mac.app.subtitle"),
			Icon:     plugin.Icon{Name: "terminal"},
			Keywords: keywords(l.T("mac.keywords.app")),
			Group:    l.T("group"),
		}
		if t.ID == def.ID {
			it.Accessory = l.T("accessory.default")
		} else {
			actions = append(actions, macCallback("set-default", l.T("mac.action.setDefault"), "", cbMacSetDefault, pl))
		}
		it.Actions = actions
		items = append(items, it)
	}
	// Commands run in the default terminal, or in the first that can run
	// them (Warp cannot).
	runner, ok := def, def.Commands
	if !ok {
		if i := slices.IndexFunc(terms, func(t macterm.Installed) bool { return t.Commands }); i >= 0 {
			runner, ok = terms[i], true
		}
	}
	if ok {
		pl := macPayload{Terminal: runner.ID, Run: true}
		actions := []plugin.Action{macCallback("run", l.T("mac.action.run"), "", cbMacOpen, pl)}
		if runner.Tabs {
			tab := pl
			tab.Tab = true
			actions = append(actions, macCallback("run-tab", l.T("mac.action.runTab"), "Ctrl+Enter", cbMacOpen, tab))
		}
		if folder != "" {
			here := pl
			here.Dir = folder
			actions = append(actions, macCallback("run-here", l.T("mac.action.runHere", "folder", folder), "", cbMacOpen, here))
		}
		items = append(items, plugin.Item{
			ID:        "run-command",
			Title:     l.T("mac.item.run.title"),
			Subtitle:  l.T("mac.item.run.subtitle", "app", runner.Name),
			Icon:      plugin.Icon{Name: "square-terminal"},
			Keywords:  keywords(l.T("mac.keywords.run")),
			Group:     l.T("group"),
			Arguments: []plugin.Argument{{Name: argCommand, Type: plugin.ArgText, Placeholder: l.T("mac.arg.command"), Required: true}},
			Actions:   actions,
		})
	}
	return items
}

// handleMac carries out the macOS callbacks. Terminals are started in the
// background: the launcher hides at once, and a failure (most often a
// missing Automation permission) is shown as a toast.
func (p *Plugin) handleMac(a plugin.ActionDo) (plugin.Result, error) {
	var pl macPayload
	if err := json.Unmarshal(a.Payload, &pl); err != nil {
		return plugin.Result{}, fmt.Errorf("terminal: %w", err)
	}
	l := p.h.I18n()
	terms, _ := p.macTerminals()
	i := slices.IndexFunc(terms, func(t macterm.Installed) bool { return t.ID == pl.Terminal })
	if i < 0 {
		return plugin.Result{}, fmt.Errorf("terminal: %q is not installed", pl.Terminal)
	}
	t := terms[i]
	switch a.Target {
	case cbMacSetDefault:
		p.o.SetPreferred(t.ID)
		p.h.Invalidate("")
		return plugin.Result{
			Preferences: map[string]jsontext.Value{prefTerminal: plugin.MustPayload(t.ID)},
			Toast:       &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("mac.toast.defaultSet", "app", t.Name)},
		}, nil
	case cbMacOpen:
	default:
		return plugin.Result{}, fmt.Errorf("terminal: unknown callback %q", a.Target)
	}
	r := macterm.Request{Dir: pl.Dir, Tab: pl.Tab, Shell: p.o.Shell}
	if r.Dir == "" {
		r.Dir = p.o.Home
	}
	if pl.Run {
		if r.Command = argument(a, argCommand); r.Command == "" {
			return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastFailure, Title: l.T("mac.toast.noCommand")}}, nil
		}
	}
	launch, err := t.Launch(r)
	if err != nil {
		return plugin.Result{Toast: macFailure(l, t.Name, err)}, nil
	}
	p.spawn(func() {
		if err := p.o.Launch(launch); err != nil {
			p.h.Logger().Warn("terminal: opening", "terminal", t.ID, "err", err)
			p.h.Toast(*macFailure(l, t.Name, err))
		}
	})
	return plugin.Result{Hide: true}, nil
}

// macFailure is the toast for a terminal that could not be opened, naming
// the permission to grant when one is missing.
func macFailure(l plugin.Localizer, app string, err error) *plugin.Toast {
	msg := err.Error()
	switch macterm.PermissionProblem(err) {
	case macterm.NeedAutomation:
		msg = l.T("mac.toast.needAutomation", "app", app)
	case macterm.NeedAccessibility:
		msg = l.T("mac.toast.needAccessibility")
	}
	return &plugin.Toast{Style: plugin.ToastFailure, Title: l.T("mac.toast.failed", "app", app), Message: msg}
}

// argument returns the trimmed string value of argument name.
func argument(a plugin.ActionDo, name string) string {
	v, ok := a.Values[name]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}
