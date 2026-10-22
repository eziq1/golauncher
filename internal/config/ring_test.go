package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestRingDefaults: the Action Ring is on by default, holds 300ms, has the
// four default fixed actions, context actions and the seeded exceptions; a
// file without it gets them; a partial one keeps what it leaves out; an
// emptied list stays empty and a stored list replaces the default one; the
// hold is kept within bounds; Clone copies the lists.
func TestRingDefaults(t *testing.T) {
	dir := t.TempDir()
	load := func(body string) Config {
		t.Helper()
		p := filepath.Join(dir, "c.json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		c, err := Load(p)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := load(`{"schemaVersion": 1}`)
	r := c.Ring
	if !r.Enabled || !r.Context || r.EffectiveHoldMS() != 300 || len(r.Fixed) != 4 || r.Fixed[0].Key != RingLauncher ||
		r.Fixed[3].Key != RingLock || !slices.Contains(r.Exceptions, "mstsc.exe") || !slices.Contains(r.Exceptions, "com.vmware.fusion") {
		t.Errorf("defaults %+v", r)
	}
	c = load(`{"schemaVersion": 1, "ring": {"enabled": false}}`)
	if c.Ring.Enabled || !c.Ring.Context || len(c.Ring.Fixed) != 4 || len(c.Ring.Exceptions) != len(DefaultRingExceptions) {
		t.Errorf("partial %+v", c.Ring)
	}
	c = load(`{"schemaVersion": 1, "ring": {"fixed": [{"key": "builtin.ai/cmd:chat", "title": "Chat"}], "exceptions": [], "holdMs": 5000}}`)
	if len(c.Ring.Fixed) != 1 || c.Ring.Fixed[0].Title != "Chat" || len(c.Ring.Exceptions) != 0 || c.Ring.EffectiveHoldMS() != MaxRingHoldMS {
		t.Errorf("stored %+v", c.Ring)
	}
	if (RingConfig{HoldMS: 1}).EffectiveHoldMS() != MinRingHoldMS {
		t.Error("no lower bound")
	}
	a := DefaultRing()
	b := Config{Ring: a}.Clone().Ring
	b.Fixed[0].Key, b.Exceptions[0] = "x", "y"
	if a.Fixed[0].Key != RingLauncher || a.Exceptions[0] != DefaultRingExceptions[0] {
		t.Error("Clone shares the lists")
	}
	// Written and read back.
	p := filepath.Join(dir, "saved.json")
	cfg := Default()
	cfg.Ring.HoldMS, cfg.Ring.Fixed = 450, cfg.Ring.Fixed[:2]
	if err := Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	back, err := Load(p)
	if err != nil || back.Ring.HoldMS != 450 || len(back.Ring.Fixed) != 2 {
		t.Errorf("round trip %+v, %v", back.Ring, err)
	}
}
