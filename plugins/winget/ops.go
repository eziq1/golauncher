package winget

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Operation kinds (opRequest.Kind).
const (
	opInstall      = "install"
	opUninstall    = "uninstall"
	opUpgrade      = "upgrade"
	opUpgradeAll   = "upgrade-all"
	opPin          = "pin"
	opUnpin        = "unpin"
	opSourceUpdate = "source-update"
)

// taskQueue serializes winget operations in the host's task manager.
const taskQueue = "winget"

// opRequest describes a winget operation; it is the Payload of the "op"
// callback.
type opRequest struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
	// Name is the package name shown in the task title.
	Name string `json:"name,omitempty"`
	// Source is the source of the package, or the source to update; ""
	// for software no source knows (only uninstall is possible) or for all
	// sources.
	Source string `json:"source,omitempty"`
}

var (
	// idRe is what package identifiers of a source look like (the manifest
	// schema's PackageIdentifier: parts without blanks and path characters,
	// separated by dots; Microsoft Store IDs have one part). Not starting
	// with "-" keeps them from being taken as options.
	idRe = regexp.MustCompile(`^[^.\s\\/:*?"<>|\x00-\x1f-][^.\s\\/:*?"<>|\x00-\x1f]{0,127}(\.[^.\s\\/:*?"<>|\x00-\x1f]{1,128}){0,7}$`)
	// localIDRe is what IDs of software no source knows look like
	// (ARP\Machine\X64\{GUID}, MSIX\Contoso.App_1.0.0.0_x64__8wekyb3d8bbwe).
	localIDRe = regexp.MustCompile(`^[^\s"\x00-\x1f-][^"\x00-\x1f]{0,511}$`)
	// sourceRe is what source names look like.
	sourceRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// modeArgs are the arguments of the installMode preference.
func (p *Plugin) modeArgs() []string {
	switch p.h.Preferences().String(prefInstallMode) {
	case modeSilent:
		return []string{"--silent"}
	case modeInteractive:
		return []string{"--interactive"}
	}
	return nil
}

// plan returns the task title and the winget arguments of req, or an error
// for a request that is not valid.
func (p *Plugin) plan(req opRequest) (string, []string, error) {
	t := p.h.I18n()
	name := req.Name
	if name == "" {
		name = req.ID
	}
	needSource := func() error {
		if !sourceRe.MatchString(req.Source) {
			return errors.New(t.T("error.badSource", "source", req.Source))
		}
		return nil
	}
	needID := func(local bool) error {
		if req.Source == "" && local {
			if !localIDRe.MatchString(req.ID) {
				return errors.New(t.T("error.badID", "id", req.ID))
			}
			return nil
		}
		if !idRe.MatchString(req.ID) {
			return errors.New(t.T("error.badID", "id", req.ID))
		}
		return needSource()
	}
	target := func() []string {
		args := []string{"--id", req.ID, "--exact"}
		if req.Source != "" {
			args = append(args, "--source", req.Source)
		}
		return args
	}
	accept := []string{"--accept-source-agreements", "--accept-package-agreements", "--disable-interactivity"}
	switch req.Kind {
	case opInstall:
		if err := needID(false); err != nil {
			return "", nil, err
		}
		args := append(append(append([]string{"install"}, target()...), accept...), p.modeArgs()...)
		return t.T("op.install", "name", name), args, nil
	case opUpgrade:
		if err := needID(false); err != nil {
			return "", nil, err
		}
		args := append(append(append([]string{"upgrade"}, target()...), accept...), p.modeArgs()...)
		return t.T("op.upgrade", "name", name), args, nil
	case opUninstall:
		if err := needID(true); err != nil {
			return "", nil, err
		}
		args := append(append([]string{"uninstall"}, target()...), "--accept-source-agreements", "--disable-interactivity")
		return t.T("op.uninstall", "name", name), append(args, p.modeArgs()...), nil
	case opUpgradeAll:
		args := append(append([]string{"upgrade", "--all"}, accept...), p.modeArgs()...)
		return t.T("op.upgradeAll"), args, nil
	case opPin, opUnpin:
		if err := needID(false); err != nil {
			return "", nil, err
		}
		verb, title := "add", t.T("op.pin", "name", name)
		if req.Kind == opUnpin {
			verb, title = "remove", t.T("op.unpin", "name", name)
		}
		args := append(append([]string{"pin", verb}, target()...), "--accept-source-agreements", "--disable-interactivity")
		return title, args, nil
	case opSourceUpdate:
		args := []string{"source", "update"}
		title := t.T("op.sourceUpdateAll")
		if req.Source != "" {
			if err := needSource(); err != nil {
				return "", nil, err
			}
			args = append(args, "--name", req.Source)
			title = t.T("op.sourceUpdate", "source", req.Source)
		}
		return title, append(args, "--disable-interactivity"), nil
	}
	return "", nil, fmt.Errorf("winget: unknown operation %q", req.Kind)
}

// quoteArg quotes an argument for a Windows command line when it needs it.
// Valid requests have no quotes, so wrapping is enough.
func quoteArg(a string) string {
	if a == "" || strings.ContainsAny(a, " \t") {
		return `"` + a + `"`
	}
	return a
}

// CommandLine is how winget arguments are shown: "winget install --id ...".
func CommandLine(args []string) string {
	q := make([]string, 0, len(args)+1)
	q = append(q, "winget")
	for _, a := range args {
		q = append(q, quoteArg(a))
	}
	return strings.Join(q, " ")
}

// start runs the operation req as a background task of the host (queue
// "winget", so operations run one after the other) and returns at once
// with a toast. The installed packages are listed again when it ended.
func (p *Plugin) start(req opRequest) (plugin.Result, error) {
	t := p.h.I18n()
	title, args, err := p.plan(req)
	if err != nil {
		return failure(t.T("toast.cannotRun"), err.Error()), nil
	}
	if p.work.Closed() {
		return failure(t.T("toast.cannotRun"), t.T("error.closed")), nil
	}
	line := CommandLine(args)
	p.h.RunProcess(plugin.TaskSpec{
		Title:    title,
		Subtitle: line,
		Icon:     plugin.Icon{Name: "package"},
		Queue:    taskQueue,
		Notify:   plugin.NotifyAlways,
		OnDone:   func(plugin.TaskResult) { p.opDone(req.Kind) },
	}, plugin.ProcessSpec{Argv: append([]string{p.exe}, args...), CommandLine: line})
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: t.T("toast.started", "title", title), Message: t.T("toast.startedHint")}}, nil
}

// opDone runs after an operation ended: the installed packages and pins
// may have changed (and the index, after a source update).
func (p *Plugin) opDone(kind string) {
	p.mu.Lock()
	if kind == opSourceUpdate {
		clear(p.shows)
	}
	p.mu.Unlock()
	if p.work.Closed() {
		return
	}
	if p.opts.Sync {
		if kind == opSourceUpdate {
			p.loadSources()
			p.refreshIndex(true)
		}
		p.refreshInstalled(true)
		return
	}
	if kind == opSourceUpdate {
		p.scheduleIndex(true)
	}
	p.scheduleInstalled()
}

// elevatedAction is an action that runs the winget command of req in a
// console window as administrator (UAC), for packages whose installer needs
// winget itself elevated. --wait keeps the window open at the end.
func (p *Plugin) elevatedAction(t plugin.Localizer, id, title string, req opRequest) (plugin.Action, bool) {
	_, args, err := p.plan(req)
	if err != nil {
		return plugin.Action{}, false
	}
	q := make([]string, 0, len(args)+1)
	for _, a := range args {
		q = append(q, quoteArg(a))
	}
	q = append(q, "--wait")
	return plugin.Action{ID: id, Title: title, Shortcut: "Ctrl+Shift+A",
		Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: p.exe,
			Payload: plugin.MustPayload(plugin.OpenOptions{Verb: "runas", Args: strings.Join(q, " ")})}}, true
}
