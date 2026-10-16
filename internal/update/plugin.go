package update

import (
	"context"
	"errors"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// PluginID is the host plugin with the "Check for Updates" command.
const PluginID = "builtin.update"

// CommandCheck is its command.
const CommandCheck = "check"

// Checker checks for updates as the user asked (Service.Check).
type Checker interface {
	Check(ctx context.Context) (Status, error)
	CanInstall() bool
}

// Localizer is the part of a localizer that Summary needs.
type Localizer interface {
	T(id string, args ...any) string
}

// Summary is the answer to a manual check in words: a title and a
// message, and whether it failed.
func Summary(l Localizer, st Status, err error, mode Mode) (title, message string, failed bool) {
	switch {
	case err != nil:
		return l.T("update.result.failed"), err.Error(), true
	case !st.Available:
		return l.T("update.result.upToDate"), l.T("update.result.upToDateMessage", "version", st.Current), false
	}
	title = l.T("update.result.available", "version", st.Latest)
	switch {
	case st.Disabled != "":
		message = l.T("update.result.availableManual")
	case st.Blocked:
		message = l.T("update.result.availableBlocked")
	case mode == ModeAuto:
		message = l.T("update.result.availableAuto")
	default:
		message = l.T("update.result.availableNotify")
	}
	return title, message, false
}

// Plugin is builtin.update: the "Check for Updates" command, a no-view
// command whose answer shows as a toast. Its strings are in the host's
// catalog.
type Plugin struct {
	checker Checker
	mode    func() Mode
	h       plugin.Host
}

var (
	_ plugin.Plugin        = (*Plugin)(nil)
	_ plugin.ActionHandler = (*Plugin)(nil)
)

// NewPlugin returns the plugin checking with c; mode returns the user's
// choice of automatic updates.
func NewPlugin(c Checker, mode func() Mode) *Plugin {
	return &Plugin{checker: c, mode: mode}
}

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	icon := plugin.Icon{Name: "circle-arrow-down"}
	return plugin.Manifest{
		ID:          PluginID,
		Name:        "%update.plugin.name%",
		Description: "%update.plugin.description%",
		Version:     "1.0.0",
		Icon:        icon,
		Commands: []plugin.Command{{
			Name: CommandCheck, Title: "%update.command.title%", Mode: plugin.ModeNoView, Icon: icon,
			Keywords: []string{"update", "upgrade", "version", "%update.command.keywords%"},
		}},
	}
}

// Init implements plugin.Plugin.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.h = h
	return nil
}

// Close implements plugin.Plugin.
func (p *Plugin) Close() error { return nil }

// HandleAction implements plugin.ActionHandler: it checks and tells the
// answer.
func (p *Plugin) HandleAction(ctx context.Context, a plugin.ActionDo) (plugin.Result, error) {
	if a.Kind != plugin.ActionRunCommand || a.Target != CommandCheck {
		return plugin.Result{}, errors.New("update: unknown action")
	}
	l := p.h.I18n()
	mode := ModeAuto
	if p.mode != nil {
		mode = p.mode()
	}
	st, err := p.checker.Check(ctx)
	title, msg, failed := Summary(l, st, err, mode)
	style := plugin.ToastSuccess
	if failed {
		style = plugin.ToastFailure
	} else if st.Available {
		style = plugin.ToastInfo
	}
	return plugin.Result{Toast: &plugin.Toast{Style: style, Title: title, Message: msg}}, nil
}
