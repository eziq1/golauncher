package host

import (
	"encoding/json/jsontext"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/internal/store"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Services are the shared host services that per-plugin Hosts are built on.
type Services struct {
	Log       *slog.Logger
	Now       func() time.Time
	DB        *store.DB
	Bus       *Bus
	I18n      *i18n.Manager
	Clipboard plugin.Clipboard
	// Secrets holds the values of password preferences; nil keeps them in
	// memory only.
	Secrets Secrets
	// OnInvalidate and OnRender receive Host.Invalidate and Host.Render calls
	// (set by the engine; nil drops them).
	OnInvalidate func(pluginID, viewID string)
	OnRender     func(pluginID, instance string, v plugin.View)
	// DataRoot and CacheRoot contain one subdirectory per plugin.
	DataRoot  string
	CacheRoot string
}

// Host is the plugin.Host given to one plugin.
type Host struct {
	s     *Services
	id    string
	prefs *Prefs
	data  string
	cache string
}

var _ plugin.Host = (*Host)(nil)

// NewHost returns the Host for the plugin described by m, creating its data
// and cache directories.
func (s *Services) NewHost(m plugin.Manifest, prefs map[string]jsontext.Value) (*Host, error) {
	h := &Host{
		s:     s,
		id:    m.ID,
		prefs: NewPrefs(m.Preferences, prefs),
		data:  filepath.Join(s.DataRoot, m.ID),
		cache: filepath.Join(s.CacheRoot, m.ID),
	}
	if s.Secrets != nil {
		h.prefs = newSecretPrefs(m.ID, m.Preferences, prefs, s.Secrets, s.Log)
	}
	for _, d := range []string{h.data, h.cache} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	return h, nil
}

// Prefs returns the plugin's preferences, so the host can update them when
// the configuration changes.
func (h *Host) Prefs() *Prefs { return h.prefs }

func (h *Host) Logger() *slog.Logger            { return h.s.Log.With("plugin", h.id) }
func (h *Host) Now() time.Time                  { return h.s.Now() }
func (h *Host) Storage() plugin.KV              { return h.s.DB.KV(h.id) }
func (h *Host) Preferences() plugin.Preferences { return h.prefs }
func (h *Host) Clipboard() plugin.Clipboard     { return h.s.Clipboard }
func (h *Host) Events() plugin.Events           { return h.s.Bus }
func (h *Host) I18n() plugin.Localizer          { return h.s.I18n.Localizer(h.id) }
func (h *Host) DataDir() string                 { return h.data }
func (h *Host) CacheDir() string                { return h.cache }

// Go implements plugin.Host: fn runs on a new goroutine with panics contained.
func (h *Host) Go(fn func()) { Go(h.Logger(), "plugin "+h.id, fn) }

// Invalidate implements plugin.Host.
func (h *Host) Invalidate(viewID string) {
	if fn := h.s.OnInvalidate; fn != nil {
		fn(h.id, viewID)
	}
}

// Render implements plugin.Host.
func (h *Host) Render(instance string, v plugin.View) {
	if fn := h.s.OnRender; fn != nil {
		fn(h.id, instance, v)
	}
}
