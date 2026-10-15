package host

import (
	"encoding/json/jsontext"
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
)

var declared = []plugin.Preference{
	{Name: "token", Type: plugin.PrefPassword, Title: "Token", Default: jsontext.Value(`"default-token"`)},
	{Name: "user", Type: plugin.PrefText, Title: "User"},
}

func TestPrefsReadPasswordsFromSecrets(t *testing.T) {
	sec := NewMemorySecrets()
	config := map[string]jsontext.Value{"token": jsontext.Value(`"from-config"`), "user": jsontext.Value(`"ann"`)}
	p := newSecretPrefs("x", declared, config, sec, nil)
	if got := p.String("token"); got != "default-token" {
		t.Fatalf("token without a secret = %q, want the default (never the config value)", got)
	}
	if got := p.String("user"); got != "ann" {
		t.Fatalf("user = %q", got)
	}
	sec.Set("x", "token", "s3cret")
	if got := p.String("token"); got != "s3cret" {
		t.Fatalf("token = %q", got)
	}
	p.SetValues(map[string]jsontext.Value{"token": jsontext.Value(`"other"`)})
	if got := p.String("token"); got != "s3cret" {
		t.Fatalf("token after a config change = %q", got)
	}
	p.Set("token", jsontext.Value(`"set"`))
	if got, _, _ := sec.Get("x", "token"); got != "set" {
		t.Fatalf("Set stored %q in the secrets", got)
	}
	sec.Delete("x", "token")
	if got := p.String("token"); got != "default-token" {
		t.Fatalf("token after Delete = %q", got)
	}
}

func TestPrefsWithoutSecretsIgnoreConfigPasswords(t *testing.T) {
	p := NewPrefs(declared, map[string]jsontext.Value{"token": jsontext.Value(`"from-config"`)})
	if got := p.String("token"); got != "default-token" {
		t.Fatalf("token = %q", got)
	}
	p.Set("token", jsontext.Value(`"in-memory"`))
	p.SetValues(nil)
	if got := p.String("token"); got != "in-memory" {
		t.Fatalf("token set in memory = %q", got)
	}
}

func TestHostUsesServicesSecrets(t *testing.T) {
	sec := NewMemorySecrets()
	sec.Set("x", "token", "abc")
	s := &Services{Secrets: sec, DataRoot: t.TempDir(), CacheRoot: t.TempDir()}
	h, err := s.NewHost(plugin.Manifest{ID: "x", Preferences: declared}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Preferences().String("token"); got != "abc" {
		t.Fatalf("token = %q", got)
	}
}
