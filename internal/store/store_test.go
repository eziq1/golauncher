package store

import (
	"math"
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
