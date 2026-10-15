// Package app wires the launcher together: configuration, database, i18n,
// plugin hosts, engine and controller. The GUI, glctl and tests all build
// their environment through it.
package app

import (
	"cmp"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/host"
	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/internal/store"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Options configure Build.
type Options struct {
	Plugins []plugin.Plugin
	// Fake keeps everything in memory or in a temporary directory: no user
	// configuration, database or clipboard is touched.
	Fake bool
	// DataDir and CacheDir default to %APPDATA%\golauncher and
	// %LOCALAPPDATA%\golauncher (a temporary directory when Fake).
	DataDir  string
	CacheDir string
	// Now fixes the clock (default time.Now).
	Now func() time.Time
	// Locale overrides the configured language when set.
	Locale string
	// System performs action side effects. Required.
	System core.System
	// Clipboard is the system clipboard. Required.
	Clipboard plugin.Clipboard
	// Secrets stores password preferences (default: the Windows Credential
	// Manager, in memory when Fake).
	Secrets host.Secrets
	Log     *slog.Logger
}

// Env is a running launcher environment.
type Env struct {
	Engine     *core.Engine
	Controller *core.Controller
	Services   *host.Services
	Config     *config.Store
	I18n       *i18n.Manager

	tempDir   string
	manifests []plugin.Manifest
}

// DefaultDataDir returns %APPDATA%\golauncher.
func DefaultDataDir() (string, error) {
	d, err := os.UserConfigDir()
	return filepath.Join(d, "golauncher"), err
}

// DefaultCacheDir returns %LOCALAPPDATA%\golauncher.
func DefaultCacheDir() (string, error) {
	d, err := os.UserCacheDir()
	return filepath.Join(d, "golauncher"), err
}

// Build creates the environment and initializes the plugins.
func Build(ctx context.Context, o Options) (*Env, error) {
	env := &Env{}
	log := o.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}

	dataDir, cacheDir := o.DataDir, o.CacheDir
	var err error
	if o.Fake && (dataDir == "" || cacheDir == "") {
		if env.tempDir, err = os.MkdirTemp("", "golauncher-fake-"); err != nil {
			return nil, err
		}
		dataDir = cmp.Or(dataDir, filepath.Join(env.tempDir, "data"))
		cacheDir = cmp.Or(cacheDir, filepath.Join(env.tempDir, "cache"))
	}
	if dataDir == "" {
		if dataDir, err = DefaultDataDir(); err != nil {
			return nil, err
		}
	}
	if cacheDir == "" {
		if cacheDir, err = DefaultCacheDir(); err != nil {
			return nil, err
		}
	}
	for _, d := range []string{dataDir, cacheDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}

	var db *store.DB
	if o.Fake {
		env.Config = config.NewMemory(config.Default())
		db, err = store.Open(":memory:")
	} else {
		if env.Config, err = config.Open(filepath.Join(dataDir, "config.json")); err != nil {
			return nil, err
		}
		db, err = store.Open(filepath.Join(dataDir, "golauncher.db"))
	}
	if err != nil {
		return nil, err
	}

	for _, p := range o.Plugins {
		env.manifests = append(env.manifests, p.Manifest())
	}
	secrets := o.Secrets
	switch {
	case secrets != nil:
	case o.Fake:
		secrets = host.NewMemorySecrets()
	default:
		secrets = host.CredentialSecrets{}
	}
	if !o.Fake {
		env.migrateSecrets(secrets, log)
	}

	cfg := env.Config.Get()
	env.I18n = i18n.NewManager(i18n.Source, log)
	if err := env.I18n.Register(i18n.CoreNamespace, i18n.CoreLocales()); err != nil {
		db.Close()
		return nil, err
	}
	env.I18n.SetLocale(cmp.Or(o.Locale, cfg.Language))

	bus := host.NewBus()
	env.Services = &host.Services{
		Log:       log,
		Now:       now,
		DB:        db,
		Bus:       bus,
		I18n:      env.I18n,
		Clipboard: o.Clipboard,
		Secrets:   secrets,
		DataRoot:  filepath.Join(dataDir, "plugins"),
		CacheRoot: filepath.Join(cacheDir, "plugins"),
	}
	env.I18n.OnChange(func() { bus.Publish(plugin.EventLocaleChanged) })
	if o.Locale == "" {
		env.Config.OnChange(func(c config.Config) { env.I18n.SetLocale(c.Language) })
	}

	env.Engine, err = core.New(ctx, core.Options{
		Plugins:  o.Plugins,
		Services: env.Services,
		Config:   env.Config,
		System:   o.System,
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	env.Controller = core.NewController(env.Engine)
	return env, nil
}

// Close shuts the plugins down and releases resources.
func (e *Env) Close() error {
	err := errors.Join(e.Engine.Close(), e.Services.DB.Close())
	if e.tempDir != "" {
		err = errors.Join(err, os.RemoveAll(e.tempDir))
	}
	return err
}
