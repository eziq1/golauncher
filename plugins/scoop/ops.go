package scoop

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"weak"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Operation kinds (opRequest.Kind).
const (
	opInstall       = "install"
	opUninstall     = "uninstall"
	opUpdate        = "update"
	opHold          = "hold"
	opUnhold        = "unhold"
	opUpdateAll     = "update-all"
	opUpdateBuckets = "update-buckets"
	opBucketAdd     = "bucket-add"
	opBucketRemove  = "bucket-rm"
	opCleanup       = "cleanup"
)

// taskQueue serializes Scoop operations in the host's task manager: Scoop
// must not run two at once.
const taskQueue = "scoop"

// opRequest describes a Scoop operation; it is the Payload of the "op"
// callback.
type opRequest struct {
	Kind   string `json:"kind"`
	Name   string `json:"name,omitempty"`
	Bucket string `json:"bucket,omitempty"`
	URL    string `json:"url,omitempty"`
}

var (
	// nameRe is what app and bucket names look like. It keeps names from
	// being taken as options ("-g").
	nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+@-]*$`)
	urlRe  = regexp.MustCompile(`^(https?://|ssh://|git@)[^\s"'` + "`" + `]+$`)
)

// plan returns the title and the scoop commands of req, or an error for a
// request that is not valid.
func (p *Plugin) plan(req opRequest) (string, [][]string, error) {
	t := p.h.I18n()
	needName := func() error {
		if !nameRe.MatchString(req.Name) {
			return errors.New(t.T("error.badName", "name", req.Name))
		}
		if a, ok := p.installed(req.Name); ok && a.Global && req.Kind != opInstall {
			return errors.New(t.T("error.global", "name", req.Name))
		}
		return nil
	}
	switch req.Kind {
	case opInstall:
		if err := needName(); err != nil {
			return "", nil, err
		}
		target := req.Name
		if req.Bucket != "" {
			if !nameRe.MatchString(req.Bucket) {
				return "", nil, errors.New(t.T("error.badName", "name", req.Bucket))
			}
			target = req.Bucket + "/" + req.Name
		}
		return t.T("op.install", "name", req.Name), [][]string{{"install", target}}, nil
	case opUninstall:
		if err := needName(); err != nil {
			return "", nil, err
		}
		return t.T("op.uninstall", "name", req.Name), [][]string{{"uninstall", req.Name}}, nil
	case opUpdate:
		if err := needName(); err != nil {
			return "", nil, err
		}
		return t.T("op.update", "name", req.Name), [][]string{{"update", req.Name}}, nil
	case opHold:
		if err := needName(); err != nil {
			return "", nil, err
		}
		return t.T("op.hold", "name", req.Name), [][]string{{"hold", req.Name}}, nil
	case opUnhold:
		if err := needName(); err != nil {
			return "", nil, err
		}
		return t.T("op.unhold", "name", req.Name), [][]string{{"unhold", req.Name}}, nil
	case opUpdateAll:
		var steps [][]string
		if p.h.Preferences().Bool(prefUpdateBucketsFirst) {
			steps = append(steps, []string{"update"})
		}
		return t.T("op.updateAll"), append(steps, []string{"update", "*"}), nil
	case opUpdateBuckets:
		return t.T("op.updateBuckets"), [][]string{{"update"}}, nil
	case opBucketAdd:
		if err := needName(); err != nil {
			return "", nil, err
		}
		args := []string{"bucket", "add", req.Name}
		if req.URL != "" {
			if !urlRe.MatchString(req.URL) {
				return "", nil, errors.New(t.T("error.badURL", "url", req.URL))
			}
			args = append(args, req.URL)
		}
		return t.T("op.bucketAdd", "name", req.Name), [][]string{args}, nil
	case opBucketRemove:
		if err := needName(); err != nil {
			return "", nil, err
		}
		return t.T("op.bucketRemove", "name", req.Name), [][]string{{"bucket", "rm", req.Name}}, nil
	case opCleanup:
		return t.T("op.cleanup"), [][]string{{"cleanup", "*"}, {"cache", "rm", "*"}}, nil
	}
	return "", nil, fmt.Errorf("scoop: unknown operation %q", req.Kind)
}

// installed returns the installed app called name, if any.
func (p *Plugin) installed(name string) (*App, bool) {
	ix := p.Index()
	if ix == nil {
		return nil, false
	}
	return ix.App(name)
}

// start runs the operation req as a background task of the host (queue
// "scoop", so operations run one after the other) and returns at once with
// a toast. The output is in Background Tasks; the index is read again when
// the operation ended.
func (p *Plugin) start(req opRequest) (plugin.Result, error) {
	t := p.h.I18n()
	title, steps, err := p.plan(req)
	if err != nil {
		return failure(t.T("toast.cannotRun"), err.Error()), nil
	}
	if p.work.Closed() {
		return failure(t.T("toast.cannotRun"), t.T("error.closed")), nil
	}
	cmd := p.command(steps)
	p.h.RunProcess(plugin.TaskSpec{
		Title:    title,
		Subtitle: cmd.CommandLine,
		Icon:     plugin.Icon{Name: "package"},
		Queue:    taskQueue,
		Notify:   plugin.NotifyAlways,
		OnDone:   p.opDone,
	}, cmd)
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: t.T("toast.started", "title", title), Message: t.T("toast.startedHint")}}, nil
}

// opDone runs after an operation ended: installed apps and buckets may have
// changed, so the index is read again (which also refreshes open views).
func (p *Plugin) opDone(plugin.TaskResult) {
	p.mu.Lock()
	p.reclaimIx = weak.Pointer[Index]{} // folders and the cache may have changed
	p.mu.Unlock()
	if p.work.Closed() {
		return
	}
	if p.opts.Sync {
		p.refresh(true)
	} else {
		p.scheduleRefresh()
	}
}

// commandLines is how the steps are shown: "scoop update && scoop update *".
func commandLines(steps [][]string) string {
	lines := make([]string, len(steps))
	for i, s := range steps {
		lines[i] = CommandLine(s)
	}
	return strings.Join(lines, " && ")
}
