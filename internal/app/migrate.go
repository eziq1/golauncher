package app

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// moveLegacyData moves databases and plugin data that versions before 0.1.0
// kept next to config.json (in roaming AppData) to the local data directory.
// It does nothing when the new location already has a database.
func moveLegacyData(oldDir, newDir string, log *slog.Logger) {
	if oldDir == "" || oldDir == newDir {
		return
	}
	if _, err := os.Stat(filepath.Join(newDir, "golauncher.db")); err == nil {
		return
	}
	if _, err := os.Stat(filepath.Join(oldDir, "golauncher.db")); err != nil {
		return
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		log.Warn("moving data: create data dir", "err", err)
		return
	}
	for _, name := range []string{"golauncher.db", "golauncher.db-wal", "golauncher.db-shm", "plugins"} {
		from, to := filepath.Join(oldDir, name), filepath.Join(newDir, name)
		if err := os.Rename(from, to); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Warn("moving data", "from", from, "to", to, "err", err)
			continue
		}
		log.Info("moved data to the local data directory", "from", from, "to", to)
	}
}
