// Package host implements the plugin.Host API on top of the host's
// services. Subpackage hostfake provides an in-memory Host for tests.
package host

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"sync"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Prefs implements plugin.Preferences from user values with fallback to the
// manifest defaults.
type Prefs struct {
	mu       sync.RWMutex
	defaults map[string]jsontext.Value
	values   map[string]jsontext.Value
}

// NewPrefs returns preferences for the declared prefs and user values.
func NewPrefs(declared []plugin.Preference, values map[string]jsontext.Value) *Prefs {
	p := &Prefs{defaults: map[string]jsontext.Value{}}
	for _, d := range declared {
		if len(d.Default) > 0 {
			p.defaults[d.Name] = d.Default
		}
	}
	p.SetValues(values)
	return p
}

// SetValues replaces the user values.
func (p *Prefs) SetValues(values map[string]jsontext.Value) {
	p.mu.Lock()
	p.values = values
	p.mu.Unlock()
}

// Set sets one user value.
func (p *Prefs) Set(name string, v jsontext.Value) {
	p.mu.Lock()
	if p.values == nil {
		p.values = map[string]jsontext.Value{}
	}
	p.values[name] = v
	p.mu.Unlock()
}

// Raw implements plugin.Preferences.
func (p *Prefs) Raw(name string) (jsontext.Value, bool) {
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
