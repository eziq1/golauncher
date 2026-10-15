package app

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestMoveLegacyData(t *testing.T) {
	oldDir, newDir := t.TempDir(), filepath.Join(t.TempDir(), "data")
	os.WriteFile(filepath.Join(oldDir, "golauncher.db"), []byte("db"), 0o644)
	os.WriteFile(filepath.Join(oldDir, "config.json"), []byte("{}"), 0o644)
	os.MkdirAll(filepath.Join(oldDir, "plugins", "x"), 0o755)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	moveLegacyData(oldDir, newDir, log)
	for _, p := range []string{"golauncher.db", filepath.Join("plugins", "x")} {
		if _, err := os.Stat(filepath.Join(newDir, p)); err != nil {
			t.Errorf("%s not moved: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(oldDir, "config.json")); err != nil {
		t.Error("config.json must stay in the config dir")
	}
	// A second run with data in place is a no-op.
	os.WriteFile(filepath.Join(oldDir, "golauncher.db"), []byte("old"), 0o644)
	moveLegacyData(oldDir, newDir, log)
	if b, _ := os.ReadFile(filepath.Join(newDir, "golauncher.db")); string(b) != "db" {
		t.Fatalf("existing data was overwritten: %q", b)
	}
}
