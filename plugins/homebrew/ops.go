package homebrew

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Operation kinds (opRequest.Kind).
const (
	opInstall    = "install"
	opUninstall  = "uninstall"
	opZap        = "zap" // uninstall a cask and remove its app data
	opUpgrade    = "upgrade"
	opUpgradeAll = "upgrade-all"
	opUpdate     = "update"
)

// taskQueue serializes Homebrew operations in the host's task manager:
// brew takes a lock and refuses to run two changes at once.
const taskQueue = "homebrew"

// opRequest describes a brew operation; it is the Payload of the "op"
// callback.
type opRequest struct {
	Kind string `json:"kind"`
	// Type is KindFormula or KindCask.
	Type string `json:"type,omitempty"`
	// Name is the formula or cask, with its tap for other taps
	// ("user/tap/name").
	Name string `json:"name,omitempty"`
	// Title is the package as shown in the task title.
	Title string `json:"title,omitempty"`
}

// nameRe is what formula names and cask tokens look like, optionally with
// their tap. Not starting with "-" keeps them from being taken as options.
var nameRe = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*/)?[A-Za-z0-9][A-Za-z0-9@._+-]{0,127}$`)

// opEnv is added to the environment of operations. HOMEBREW_NO_AUTO_UPDATE
// follows the autoUpdate preference.
func (p *Plugin) opEnv() []string {
	env := []string{"HOMEBREW_NO_ENV_HINTS=1", "HOMEBREW_NO_EMOJI=1"}
	if !p.h.Preferences().Bool(prefAutoUpdate) {
		env = append(env, "HOMEBREW_NO_AUTO_UPDATE=1")
	}
	return env
}

// plan returns the task title and the brew arguments of req, or an error
// for a request that is not valid.
func (p *Plugin) plan(req opRequest) (string, []string, error) {
	t := p.h.I18n()
	title := req.Title
	if title == "" {
		title = req.Name
	}
	target := func() ([]string, error) {
		if !nameRe.MatchString(req.Name) {
			return nil, errors.New(t.T("error.badName", "name", req.Name))
		}
		switch req.Type {
		case KindFormula:
			return []string{"--formula", req.Name}, nil
		case KindCask:
			return []string{"--cask", req.Name}, nil
		}
		return nil, fmt.Errorf("homebrew: unknown package type %q", req.Type)
	}
	switch req.Kind {
	case opInstall, opUpgrade, opUninstall:
		tg, err := target()
		if err != nil {
			return "", nil, err
		}
		var msg string
		switch req.Kind {
		case opInstall:
			msg = t.T("op.install", "name", title)
		case opUpgrade:
			msg = t.T("op.upgrade", "name", title)
		default:
			msg = t.T("op.uninstall", "name", title)
		}
		return msg, append([]string{req.Kind}, tg...), nil
	case opZap:
		if req.Type != KindCask {
			return "", nil, fmt.Errorf("homebrew: only casks can be zapped")
		}
		tg, err := target()
		if err != nil {
			return "", nil, err
		}
		return t.T("op.zap", "name", title), []string{"uninstall", "--cask", "--zap", tg[1]}, nil
	case opUpgradeAll:
		return t.T("op.upgradeAll"), []string{"upgrade"}, nil
	case opUpdate:
		return t.T("op.update"), []string{"update"}, nil
	}
	return "", nil, fmt.Errorf("homebrew: unknown operation %q", req.Kind)
}

// CommandLine is how brew arguments are shown: "brew install --cask
// firefox". Valid requests have nothing that needs quoting.
func CommandLine(args []string) string {
	return strings.Join(append([]string{"brew"}, args...), " ")
}

// installCommand is the command that installs a package, for copying.
func installCommand(kind, name string) string {
	flag := "--formula"
	if kind == KindCask {
		flag = "--cask"
	}
	return CommandLine([]string{"install", flag, name})
}

// start runs the operation req as a background task of the host (queue
// "homebrew", so operations run one after the other) and returns at once
// with a toast. The installed packages are listed again when it ended.
func (p *Plugin) start(req opRequest) (plugin.Result, error) {
	t := p.h.I18n()
	exe := p.brew()
	if exe == "" {
		return failure(t.T("toast.cannotRun"), t.T("notInstalled.title")), nil
	}
	title, args, err := p.plan(req)
	if err != nil {
		return failure(t.T("toast.cannotRun"), err.Error()), nil
	}
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
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
	}, plugin.ProcessSpec{Argv: append([]string{exe}, args...), Env: p.opEnv(), CommandLine: line})
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: t.T("toast.started", "title", title), Message: t.T("toast.startedHint")}}, nil
}

// opDone runs after an operation ended: the installed packages may have
// changed, and after an update (or an install that auto-updated) the
// index too.
func (p *Plugin) opDone(string) {
	p.mu.Lock()
	closed := p.closed
	clear(p.shows)
	p.mu.Unlock()
	if closed {
		return
	}
	if p.opts.Sync {
		p.refreshIndex(true)
		p.refreshInstalled(true)
		return
	}
	p.scheduleIndex()
	p.scheduleInstalled()
}
