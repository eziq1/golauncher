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
	"slices"
	"sync/atomic"
	"time"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/host"
	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/internal/store"
	"github.com/eziq1/golauncher/internal/tasks"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Options configure Build.
type Options struct {
	Plugins []plugin.Plugin
	// Fake keeps everything in memory or in a temporary directory: no user
	// configuration, database or clipboard is touched.
	Fake bool
	// ConfigDir holds config.json (default %APPDATA%\golauncher, which may
	// roam). DataDir holds the databases and plugin data (default
	// %LOCALAPPDATA%\golauncher\data: clipboard history must never roam, and
	// SQLite WAL does not work on network shares). CacheDir holds rebuildable
	// data (default %LOCALAPPDATA%\golauncher). ConfigDir defaults to DataDir
	// when only DataDir is given; Fake uses a temporary directory.
	ConfigDir string
	DataDir   string
	CacheDir  string
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
	// TaskRunner starts the programs of background tasks (default: for
	// real, or a tasks.FakeRunner when Fake).
	TaskRunner tasks.Runner
	// TasksDir keeps the logs of background tasks (default
	// %LOCALAPPDATA%\golauncher\tasks, i.e. CacheDir\tasks).
	TasksDir string
	// TasksReadOnly reads the task history but writes no logs (glctl --real
	// without --exec).
	TasksReadOnly bool
}

// Env is a running launcher environment.
type Env struct {
	Engine     *core.Engine
	Controller *core.Controller
	Services   *host.Services
	Config     *config.Store
	I18n       *i18n.Manager
	// Tasks is the background task manager; the builtin.tasks plugin shows
	// its tasks.
	Tasks *tasks.Manager

	tempDir   string
	manifests []plugin.Manifest
	engine    atomic.Pointer[core.Engine] // for the tasks' notifications
}

// DefaultConfigDir returns %APPDATA%\golauncher.
func DefaultConfigDir() (string, error) {
	d, err := os.UserConfigDir()
	return filepath.Join(d, "golauncher"), err
}

// DefaultDataDir returns %LOCALAPPDATA%\golauncher\data.
func DefaultDataDir() (string, error) {
	d, err := os.UserCacheDir()
	return filepath.Join(d, "golauncher", "data"), err
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

	configDir, dataDir, cacheDir := cmp.Or(o.ConfigDir, o.DataDir), o.DataDir, o.CacheDir
	var err error
	if o.Fake && (dataDir == "" || cacheDir == "") {
		if env.tempDir, err = os.MkdirTemp("", "golauncher-fake-"); err != nil {
			return nil, err
		}
		dataDir = cmp.Or(dataDir, filepath.Join(env.tempDir, "data"))
		cacheDir = cmp.Or(cacheDir, filepath.Join(env.tempDir, "cache"))
	}
	if configDir == "" && !o.Fake {
		if configDir, err = DefaultConfigDir(); err != nil {
			return nil, err
		}
	}
	if dataDir == "" {
		if dataDir, err = DefaultDataDir(); err != nil {
			return nil, err
		}
		if !o.Fake {
			moveLegacyData(configDir, dataDir, log)
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
		if env.Config, err = config.Open(filepath.Join(configDir, "config.json")); err != nil {
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
	bus.Log = log
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

	env.Tasks = env.newTasks(o, cacheDir, now, log)
	env.Services.Tasks = env.Tasks
	plugins := append(slices.Clone(o.Plugins), tasks.NewPlugin(env.Tasks))
	env.Engine, err = core.New(ctx, core.Options{
		Plugins:     plugins,
		Services:    env.Services,
		Config:      env.Config,
		System:      o.System,
		Tasks:       env.Tasks,
		RootTimeout: rootTimeout(o.Fake),
	})
	if err != nil {
		env.Tasks.Close()
		db.Close()
		return nil, err
	}
	env.engine.Store(env.Engine)
	env.Controller = core.NewController(env.Engine)
	return env, nil
}

// newTasks creates the background task manager. A finished task publishes
// plugin.EventTaskFinished and is notified according to its policy.
func (e *Env) newTasks(o Options, cacheDir string, now func() time.Time, log *slog.Logger) *tasks.Manager {
	runner := o.TaskRunner
	if runner == nil {
		if o.Fake {
			runner = &tasks.FakeRunner{}
		} else {
			runner = tasks.ProcessRunner{}
		}
	}
	dir := o.TasksDir
	if dir == "" {
		dir = filepath.Join(cacheDir, "tasks")
	}
	exec := map[string]bool{tasks.PluginID: true}
	for _, m := range e.manifests {
		exec[m.ID] = slices.Contains(m.Permissions, plugin.PermExec)
	}
	loc := e.I18n.Localizer(i18n.CoreNamespace)
	return tasks.New(tasks.Options{
		Dir:      dir,
		ReadOnly: o.TasksReadOnly,
		Runner:   runner,
		Now:      now,
		Log:      log,
		Loc:      loc,
		Allowed:  func(id string) bool { return exec[id] },
		OnFinished: func(info tasks.Info) {
			e.Services.Bus.Publish(plugin.EventTaskFinished)
			if eng := e.engine.Load(); eng != nil && tasks.ShouldNotify(info) {
				eng.Notify(core.Notice{Toast: tasks.FinishToast(loc, info), Plugin: info.Plugin, TaskID: info.ID})
			}
		},
	})
}

// Close stops the background tasks, shuts the plugins down and releases
// resources.
func (e *Env) Close() error {
	err := errors.Join(e.Tasks.Close(), e.Engine.Close(), e.Services.DB.Close())
	if e.tempDir != "" {
		err = errors.Join(err, os.RemoveAll(e.tempDir))
	}
	return err
}

// rootTimeout is the time each plugin gets for root results. The real
// launcher keeps it short so a slow plugin cannot stall typing (core's
// default). Fake environments (glctl, tests) must give the same results on
// any machine, so a plugin that is slow to warm up (e.g. the world clock
// loading its time zones on a busy CI runner) is waited for.
func rootTimeout(fake bool) time.Duration {
	if fake {
		return 10 * time.Second
	}
	return 0 // core's default
}
