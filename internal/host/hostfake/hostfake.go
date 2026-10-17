// Package hostfake provides an in-memory plugin.Host for tests and for
// glctl --fake.
package hostfake

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/host"
	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/internal/tasks"
	"github.com/eziq1/golauncher/internal/winapi/clip"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Host is a fake plugin.Host. Its exported fields may be changed by tests.
type Host struct {
	Log   *slog.Logger
	Clock func() time.Time
	KV    *KV
	Prefs *host.Prefs
	Clip  *Clipboard
	Bus   *host.Bus
	I18N  *i18n.Manager
	// Tasks runs RunProcess and RunTask; Runner is its fake process runner,
	// whose commands end at once (see SetTaskRunner).
	Tasks  *tasks.Manager
	Runner *tasks.FakeRunner
	ns     string
	dir    string
	perms  []plugin.Permission

	mu          sync.Mutex
	invalidated []string
	rendered    []Rendered
	toasts      []plugin.Toast
}

var _ plugin.Host = (*Host)(nil)

// Fixed is the default fake time: 2026-10-14 10:00 UTC.
var Fixed = time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

// New returns a fake host for plugin p with its data under dir (a test's
// t.TempDir()). The plugin's catalogs are registered and the locale is "en".
// The host is not passed to p.Init; call that yourself.
func New(p plugin.Plugin, dir string) (*Host, error) {
	m := p.Manifest()
	mgr := i18n.NewManager(i18n.Source, nil)
	if err := mgr.Register(i18n.CoreNamespace, i18n.CoreLocales()); err != nil {
		return nil, err
	}
	if lp, ok := p.(plugin.LocaleProvider); ok {
		if err := mgr.Register(m.ID, lp.Locales()); err != nil {
			return nil, err
		}
	}
	mgr.SetLocale(i18n.Source)
	for _, sub := range []string{"data", "cache"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	h := &Host{
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock: func() time.Time { return Fixed },
		KV:    &KV{m: map[string][]byte{}},
		Prefs: host.NewPrefs(m.Preferences, nil),
		Clip:  &Clipboard{},
		Bus:   host.NewBus(),
		I18N:  mgr,
		ns:    m.ID,
		dir:   dir,
		perms: m.Permissions,
	}
	h.Runner = &tasks.FakeRunner{}
	h.SetTaskRunner(h.Runner)
	return h, nil
}

// SetTaskRunner replaces the task manager by one whose processes r runs
// (e.g. tasks.ProcessRunner{} to run real programs). Call it before the
// plugin starts tasks. The log directory is dir/tasks. As in the real
// host, a plugin without the exec permission cannot run processes, and
// plugin.EventTaskFinished is published on Bus after each task.
func (h *Host) SetTaskRunner(r tasks.Runner) {
	if h.Tasks != nil {
		h.Tasks.Close()
	}
	h.Tasks = tasks.New(tasks.Options{
		Dir:     filepath.Join(h.dir, "tasks"),
		Runner:  r,
		Now:     func() time.Time { return h.Clock() },
		Log:     h.Log,
		Loc:     h.I18N.Localizer(i18n.CoreNamespace),
		Allowed: func(string) bool { return slices.Contains(h.perms, plugin.PermExec) },
		OnFinished: func(tasks.Info) {
			h.Bus.Publish(plugin.EventTaskFinished)
		},
	})
}

// RunProcess implements plugin.Host with Tasks.
func (h *Host) RunProcess(spec plugin.TaskSpec, cmd plugin.ProcessSpec) string {
	return h.Tasks.RunProcess(h.ns, spec, cmd)
}

// RunTask implements plugin.Host with Tasks.
func (h *Host) RunTask(spec plugin.TaskSpec, fn plugin.TaskFunc) string {
	return h.Tasks.RunTask(h.ns, spec, fn)
}

// WaitTasks waits until every task ended and its OnDone returned.
func (h *Host) WaitTasks() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_ = h.Tasks.Wait(ctx)
}

// Toast implements plugin.Host and records the toast.
func (h *Host) Toast(t plugin.Toast) {
	h.mu.Lock()
	h.toasts = append(h.toasts, t)
	h.mu.Unlock()
}

// Toasts returns the toasts shown with Toast so far.
func (h *Host) Toasts() []plugin.Toast {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.toasts)
}

func (h *Host) Logger() *slog.Logger            { return h.Log }
func (h *Host) Now() time.Time                  { return h.Clock() }
func (h *Host) Storage() plugin.KV              { return h.KV }
func (h *Host) Preferences() plugin.Preferences { return h.Prefs }
func (h *Host) Clipboard() plugin.Clipboard     { return h.Clip }
func (h *Host) Events() plugin.Events           { return h.Bus }
func (h *Host) I18n() plugin.Localizer          { return h.I18N.Localizer(h.ns) }
func (h *Host) DataDir() string                 { return filepath.Join(h.dir, "data") }
func (h *Host) CacheDir() string                { return filepath.Join(h.dir, "cache") }

// Go implements plugin.Host. Panics are contained and logged to Log, as in
// the real host.
func (h *Host) Go(fn func()) { host.Go(h.Log, "plugin "+h.ns, fn) }

// KV is an in-memory plugin.KV.
type KV struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (k *KV) Get(key string) ([]byte, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[key]
	return slices.Clone(v), ok, nil
}

func (k *KV) Set(key string, value []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[key] = slices.Clone(value)
	return nil
}

func (k *KV) Delete(key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.m, key)
	return nil
}

// Clipboard is an in-memory plugin.Clipboard. Set Content to simulate a
// copy from another app, then publish plugin.EventClipboardChanged.
type Clipboard struct {
	mu      sync.Mutex
	Content plugin.ClipboardContent
}

func (c *Clipboard) Read() (plugin.ClipboardContent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Content, nil
}

// HasFilesOrImage reports, as the real clipboard's probe does
// (core.FilesClipboard), whether the content has files, or an image with no
// text or text that only describes it (clip.DescribesImage).
func (c *Clipboard) HasFilesOrImage() bool {
	p, _ := c.ReadPaste()
	return len(p.Files) > 0 || len(p.Image) > 0
}

// ReadPaste is Read for a paste into a view that accepts files: the image
// only when no files come with it and its text only describes it.
func (c *Clipboard) ReadPaste() (plugin.ClipboardContent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.Content
	switch {
	case p.Exclude:
		return plugin.ClipboardContent{SourceApp: p.SourceApp, Exclude: true}, nil
	case len(p.Files) > 0 || !clip.DescribesImage(p.Text):
		p.Image = nil
	}
	return p, nil
}

// Set replaces the content, as if another app copied it.
func (c *Clipboard) Set(content plugin.ClipboardContent) {
	c.mu.Lock()
	c.Content = content
	c.mu.Unlock()
}

func (c *Clipboard) WriteText(text string) error {
	c.Set(plugin.ClipboardContent{Text: text})
	return nil
}

// WriteSecret writes text marked as excluded from clipboard history.
func (c *Clipboard) WriteSecret(text string) error {
	c.Set(plugin.ClipboardContent{Text: text, Exclude: true})
	return nil
}

func (c *Clipboard) WriteImage(png []byte) error {
	c.Set(plugin.ClipboardContent{Image: slices.Clone(png)})
	return nil
}

func (c *Clipboard) WriteFiles(paths []string) error {
	c.Set(plugin.ClipboardContent{Files: slices.Clone(paths)})
	return nil
}

// Invalidate implements plugin.Host and records the call.
func (h *Host) Invalidate(viewID string) {
	h.mu.Lock()
	h.invalidated = append(h.invalidated, viewID)
	h.mu.Unlock()
}

// Invalidated returns the view IDs passed to Invalidate so far.
func (h *Host) Invalidated() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.invalidated)
}

// Rendered is a view pushed with Render.
type Rendered struct {
	Instance string
	View     plugin.View
}

// Render implements plugin.Host and records the push.
func (h *Host) Render(instance string, v plugin.View) {
	h.mu.Lock()
	h.rendered = append(h.rendered, Rendered{instance, v})
	h.mu.Unlock()
}

// Renders returns the views pushed with Render so far.
func (h *Host) Renders() []Rendered {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.rendered)
}
