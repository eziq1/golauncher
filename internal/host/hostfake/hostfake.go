// Package hostfake provides an in-memory plugin.Host for tests and for
// glctl --fake.
package hostfake

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/host"
	"github.com/eziq1/golauncher/internal/i18n"
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
	ns    string
	dir   string
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
	return &Host{
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock: func() time.Time { return Fixed },
		KV:    &KV{m: map[string][]byte{}},
		Prefs: host.NewPrefs(m.Preferences, nil),
		Clip:  &Clipboard{},
		Bus:   host.NewBus(),
		I18N:  mgr,
		ns:    m.ID,
		dir:   dir,
	}, nil
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

func (c *Clipboard) WriteImage(png []byte) error {
	c.Set(plugin.ClipboardContent{Image: slices.Clone(png)})
	return nil
}

func (c *Clipboard) WriteFiles(paths []string) error {
	c.Set(plugin.ClipboardContent{Files: slices.Clone(paths)})
	return nil
}
