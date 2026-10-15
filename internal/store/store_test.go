package store

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestKV(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, b := db.KV("a"), db.KV("b")
	if err := a.Set("k", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := b.Get("k"); ok {
		t.Fatal("namespaces are not isolated")
	}
	a.Set("k", []byte("2"))
	if v, ok, err := a.Get("k"); err != nil || !ok || string(v) != "2" {
		t.Fatalf("Get = %q %v %v", v, ok, err)
	}
	a.Delete("k")
	if _, ok, _ := a.Get("k"); ok {
		t.Fatal("Delete did not delete")
	}
}

func TestFrecencyDecay(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	t0 := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	db.RecordUse("x", []byte(`{}`), t0)
	db.RecordUse("x", []byte(`{}`), t0)
	db.RecordUse("y", []byte(`{}`), t0.Add(HalfLife))

	f, err := db.Frecency(t0.Add(HalfLife))
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(f["x"]-1) > 1e-9 || math.Abs(f["y"]-1) > 1e-9 {
		t.Fatalf("after one half-life x=%v y=%v, want 1 and 1", f["x"], f["y"])
	}
	recent, _ := db.Recent(10)
	if len(recent) != 2 {
		t.Fatalf("Recent = %d items", len(recent))
	}
}

func TestOpenPathWithSpecialCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a#b%41 c")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "x.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.KV("n").Set("k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database not created at %s: %v", path, err)
	}
}

func TestMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	// A database from before versioning: tables exist, user_version is 0.
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db.KV("n").Set("k", []byte("v"))
	db.db.Exec(`PRAGMA user_version = 0`)
	db.Close()

	db, err = Open(path)
	if err != nil {
		t.Fatalf("reopening an unversioned database: %v", err)
	}
	var v int
	db.db.QueryRow(`PRAGMA user_version`).Scan(&v)
	if v != SchemaVersion {
		t.Fatalf("user_version = %d, want %d", v, SchemaVersion)
	}
	if got, ok, _ := db.KV("n").Get("k"); !ok || string(got) != "v" {
		t.Fatal("migration lost data")
	}
	// A database from a newer golauncher is refused rather than damaged.
	db.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, SchemaVersion+1))
	db.Close()
	if _, err := Open(path); !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("Open newer = %v, want ErrNewerSchema", err)
	}
}
