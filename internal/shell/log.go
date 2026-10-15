package shell

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/eziq1/golauncher/assets"
	"github.com/eziq1/golauncher/internal/app"
)

// maxLogSize is the size past which the log is moved to golauncher.log.old
// at start.
const maxLogSize = 5 << 20

// openLog opens %LOCALAPPDATA%\golauncher\logs\golauncher.log for appending.
func openLog() (*os.File, *slog.LevelVar, error) {
	cache, err := app.DefaultCacheDir()
	if err != nil {
		return nil, nil, err
	}
	dir := filepath.Join(cache, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	path := filepath.Join(dir, "golauncher.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxLogSize {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}
	return f, new(slog.LevelVar), nil
}

// setLevel sets the log level from the configuration ("info" when unknown).
func setLevel(v *slog.LevelVar, level string) {
	switch level {
	case "debug":
		v.Set(slog.LevelDebug)
	case "warn":
		v.Set(slog.LevelWarn)
	case "error":
		v.Set(slog.LevelError)
	default:
		v.Set(slog.LevelInfo)
	}
}

// trayIcon returns the tray icon: the app icon (a magnifying glass in a
// rounded blue tile) as a 32×32 PNG, the same image the executable carries.
func trayIcon() []byte { return assets.Icon32 }
