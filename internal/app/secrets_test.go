package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/host"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// secretPlugin has a password preference and remembers its host.
type secretPlugin struct{ h plugin.Host }

func (*secretPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "sec", Name: "Secret", Version: "1.0.0",
		Preferences: []plugin.Preference{
			{Name: "token", Type: plugin.PrefPassword, Title: "Token"},
			{Name: "user", Type: plugin.PrefText, Title: "User"},
		}}
}

func (p *secretPlugin) Init(_ context.Context, h plugin.Host) error { p.h = h; return nil }
func (*secretPlugin) Close() error                                  { return nil }

func TestMigrateSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	old := `{"schemaVersion": 1, "plugins": {"sec": {"preferences": {"token": "s3cret", "user": "ann"}}}}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	secrets := host.NewMemorySecrets()
	p := &secretPlugin{}
	clip := &hostfake.Clipboard{}
	env, err := Build(t.Context(), Options{
		Plugins:   []plugin.Plugin{p},
		DataDir:   dir,
		CacheDir:  filepath.Join(dir, "cache"),
		System:    &Recorder{Clipboard: clip},
		Clipboard: clip,
		Secrets:   secrets,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()

	if got, ok, _ := secrets.Get("sec", "token"); !ok || got != "s3cret" {
		t.Fatalf("secret = %q, %v", got, ok)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "s3cret") || strings.Contains(string(data), `"token"`) {
		t.Fatalf("config.json still has the password:\n%s", data)
	}
	if !strings.Contains(string(data), `"ann"`) {
		t.Fatalf("config.json lost the other preferences:\n%s", data)
	}
	if got := p.h.Preferences().String("token"); got != "s3cret" {
		t.Fatalf("the plugin reads token = %q", got)
	}
	if got := p.h.Preferences().String("user"); got != "ann" {
		t.Fatalf("the plugin reads user = %q", got)
	}
}

func TestFakeUsesMemorySecrets(t *testing.T) {
	clip := &hostfake.Clipboard{}
	env, err := Build(t.Context(), Options{Fake: true, System: &Recorder{Clipboard: clip}, Clipboard: clip})
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	if _, ok := env.Services.Secrets.(*host.MemorySecrets); !ok {
		t.Fatalf("secrets = %T", env.Services.Secrets)
	}
}
