// Package host implements the plugin.Host API on top of the host's
// services. Subpackage hostfake provides an in-memory Host for tests.
package host

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"log/slog"
	"sync"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Prefs implements plugin.Preferences from user values with fallback to the
// manifest defaults. The values of password preferences come from Secrets,
// never from the configuration.
type Prefs struct {
	mu        sync.RWMutex
	defaults  map[string]jsontext.Value
	values    map[string]jsontext.Value
	passwords map[string]bool // names of the password preferences

	// With secrets set, password values are read from and written to it.
	secrets Secrets
	plugin  string
	log     *slog.Logger
}

// NewPrefs returns preferences for the declared prefs and user values.
// Password values are kept in memory only (see Set); hosts read them from
// Secrets instead.
func NewPrefs(declared []plugin.Preference, values map[string]jsontext.Value) *Prefs {
	p := &Prefs{defaults: map[string]jsontext.Value{}, passwords: map[string]bool{}}
	for _, d := range declared {
		if len(d.Default) > 0 {
			p.defaults[d.Name] = d.Default
		}
		if d.Type == plugin.PrefPassword {
			p.passwords[d.Name] = true
		}
	}
	p.SetValues(values)
	return p
}

// newSecretPrefs returns preferences of plugin pluginID whose password values
// live in secrets.
func newSecretPrefs(pluginID string, declared []plugin.Preference, values map[string]jsontext.Value, secrets Secrets, log *slog.Logger) *Prefs {
	p := NewPrefs(declared, values)
	p.plugin, p.secrets, p.log = pluginID, secrets, log
	return p
}

// IsPassword reports whether name is a password preference.
func (p *Prefs) IsPassword(name string) bool { return p.passwords[name] }

// SetValues replaces the user values from the configuration. Values of
// password preferences are ignored: they never come from the configuration.
func (p *Prefs) SetValues(values map[string]jsontext.Value) {
	kept := make(map[string]jsontext.Value, len(values))
	for k, v := range values {
		if !p.passwords[k] {
			kept[k] = v
		}
	}
	p.mu.Lock()
	for k, v := range p.values {
		if p.passwords[k] && p.secrets == nil {
			kept[k] = v // set in memory with Set
		}
	}
	p.values = kept
	p.mu.Unlock()
}

// Set sets one user value. The value of a password preference goes to the
// secrets when there are any.
func (p *Prefs) Set(name string, v jsontext.Value) {
	if p.passwords[name] && p.secrets != nil {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			s = string(v)
		}
		if err := p.secrets.Set(p.plugin, name, s); err != nil && p.log != nil {
			p.log.Error("storing secret", "plugin", p.plugin, "pref", name, "err", err)
		}
		return
	}
	p.mu.Lock()
	if p.values == nil {
		p.values = map[string]jsontext.Value{}
	}
	p.values[name] = v
	p.mu.Unlock()
}

// Raw implements plugin.Preferences.
func (p *Prefs) Raw(name string) (jsontext.Value, bool) {
	if p.passwords[name] && p.secrets != nil {
		s, ok, err := p.secrets.Get(p.plugin, name)
		if err != nil && p.log != nil {
			p.log.Error("reading secret", "plugin", p.plugin, "pref", name, "err", err)
		}
		if ok {
			if v, err := json.Marshal(s); err == nil {
				return v, true
			}
		}
		p.mu.RLock()
		defer p.mu.RUnlock()
		v, ok := p.defaults[name]
		return v, ok
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if v, ok := p.values[name]; ok {
		return v, true
	}
	v, ok := p.defaults[name]
	return v, ok
}

func get[T any](p *Prefs, name string) T {
	var v T
	if raw, ok := p.Raw(name); ok {
		_ = json.Unmarshal(raw, &v)
	}
	return v
}

// String implements plugin.Preferences.
func (p *Prefs) String(name string) string { return get[string](p, name) }

// Int implements plugin.Preferences.
func (p *Prefs) Int(name string) int {
	return int(get[float64](p, name))
}

// Bool implements plugin.Preferences.
func (p *Prefs) Bool(name string) bool { return get[bool](p, name) }

// Strings implements plugin.Preferences.
func (p *Prefs) Strings(name string) []string { return get[[]string](p, name) }
