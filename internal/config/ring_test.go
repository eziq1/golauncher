package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	if !r.Enabled || !r.Context || !r.TrackpadTap || r.EffectiveHoldMS() != 300 || len(r.Fixed) != 4 || r.Fixed[0].Key != RingLauncher ||
		r.Fixed[3].Key != RingLock || !slices.Contains(r.Exceptions, "mstsc.exe") || !slices.Contains(r.Exceptions, "com.vmware.fusion") {
		t.Errorf("defaults %+v", r)
	}
	c = load(`{"schemaVersion": 1, "ring": {"enabled": false}}`)
	if c.Ring.Enabled || !c.Ring.Context || !c.Ring.TrackpadTap || len(c.Ring.Fixed) != 4 || len(c.Ring.Exceptions) != len(DefaultRingExceptions) {
		t.Errorf("partial %+v", c.Ring)
	}
	c = load(`{"schemaVersion": 1, "ring": {"fixed": [{"key": "builtin.ai/cmd:chat", "title": "Chat"}], "exceptions": [], "holdMs": 5000}}`)
	if len(c.Ring.Fixed) != 1 || c.Ring.Fixed[0].Title != "Chat" || len(c.Ring.Exceptions) != 0 || c.Ring.EffectiveHoldMS() != MaxRingHoldMS {
		t.Errorf("stored %+v", c.Ring)
	}
	if c = load(`{"schemaVersion": 1, "ring": {"trackpadTap": false}}`); c.Ring.TrackpadTap || !c.Ring.Enabled {
		t.Errorf("trackpad tap off %+v", c.Ring)
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

func act(key string) RingAction { return RingAction{Key: key} }

func slotKeys(slots []RingSlot) []string {
	var out []string
	for _, s := range slots {
		switch {
		case s.IsFolder():
			k := s.Name + "["
			for i, a := range s.Actions {
				if i > 0 {
					k += " "
				}
				k += a.Key
			}
			out = append(out, k+"]")
		case s.IsEmpty():
			out = append(out, "-")
		default:
			out = append(out, s.Key)
		}
	}
	return out
}

// TestRingFolders: an older list of up to eight actions reads as slots,
// the ones past the fourth in a folder in the last slot; folders are
// checked as they are read (empty ones dropped, one of a single action
// turned into it, at most six, keys once); a folder is written and read
// back; Clone copies the folders.
func TestRingFolders(t *testing.T) {
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
	// The old format: an action list.
	c := load(`{"schemaVersion": 1, "ring": {"fixed": [{"key": "a", "title": "A", "icon": "x"}, {"key": "b"}]}}`)
	if got := slotKeys(c.Ring.Fixed); !slices.Equal(got, []string{"a", "b"}) || c.Ring.Fixed[0].Title != "A" || c.Ring.Fixed[0].Icon != "x" {
		t.Errorf("old list %q %+v", got, c.Ring.Fixed)
	}
	c = load(`{"schemaVersion": 1, "ring": {"fixed": [{"key": "a"}, {"key": "b"}, {"key": "c"}, {"key": "d"}, {"key": "e"}, {"key": "f"}, {"key": "g"}, {"key": "h"}]}}`)
	if got := slotKeys(c.Ring.Fixed); !slices.Equal(got, []string{"a", "b", "c", "[d e f g h]"}) || c.Ring.Fixed[3].Name != "" {
		t.Errorf("eight actions %q", got)
	}
	c = load(`{"schemaVersion": 1, "ring": {"fixed": [{"key": "a"}, {"key": "b"}, {"key": "c"}, {}, {"key": "e"}]}}`)
	if got := slotKeys(c.Ring.Fixed); !slices.Equal(got, []string{"a", "b", "c", "e"}) {
		t.Errorf("five with a gap %q", got)
	}

	// Validated.
	for _, tc := range []struct {
		in   []RingSlot
		want []string
	}{
		{[]RingSlot{FolderSlot("Empty"), {Key: "a"}}, []string{"-", "a"}},
		{[]RingSlot{FolderSlot("One", act("a")), {Key: "b"}}, []string{"a", "b"}},
		{[]RingSlot{FolderSlot("Many", act("1"), act("2"), act("3"), act("4"), act("5"), act("6"), act("7"))}, []string{"Many[1 2 3 4 5 6]"}},
		{[]RingSlot{{Key: "a"}, FolderSlot("Dup", act("a"), act("b"), act("b"), act(""))}, []string{"a", "b"}},
		{[]RingSlot{{Key: "a", Name: "stray"}, {}, {}, {}}, []string{"a"}},
		{[]RingSlot{{}, {Key: "x"}}, []string{"-", "x"}},
		{[]RingSlot{{Key: "a", Actions: []RingAction{act("b"), act("c")}, Name: "F"}}, []string{"F[b c]"}},
	} {
		got := NormalizeRingFixed(tc.in)
		if !slices.Equal(slotKeys(got), tc.want) {
			t.Errorf("%+v: %q, want %q", tc.in, slotKeys(got), tc.want)
		}
		if len(got) > 0 && got[0].Key == "a" && got[0].Name != "" {
			t.Errorf("an action keeps a folder's name: %+v", got[0])
		}
	}
	if NormalizeRingFixed(nil) == nil || len(NormalizeRingFixed(nil)) != 0 {
		t.Error("nil")
	}

	// Written and read back, folders and an empty slot in the middle.
	p := filepath.Join(dir, "folders.json")
	cfg := Default()
	cfg.Ring.Fixed = []RingSlot{{Key: RingLauncher}, {}, FolderSlot("Tools", RingAction{Key: "p/x", Title: "X", Icon: "app"}, act(RingScreenshot)), {Key: RingLock}}
	if err := Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	back, err := Load(p)
	if err != nil || !slices.Equal(slotKeys(back.Ring.Fixed), []string{RingLauncher, "-", "Tools[p/x ring:screenshot]", RingLock}) ||
		back.Ring.Fixed[2].Actions[0].Title != "X" {
		t.Errorf("round trip %+v, %v", back.Ring.Fixed, err)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), `"name": "Tools"`) || strings.Contains(string(data), `"key": ""`) {
		t.Errorf("written:\n%s", data)
	}
	b := cfg.Clone()
	b.Ring.Fixed[2].Actions[0].Key = "changed"
	if cfg.Ring.Fixed[2].Actions[0].Key != "p/x" {
		t.Error("Clone shares a folder's actions")
	}
	// Restore Defaults: four actions, no folder.
	for _, s := range DefaultRingFixed() {
		if s.IsFolder() || s.IsEmpty() {
			t.Errorf("default %+v", s)
		}
	}
}
