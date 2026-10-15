package system

import "testing"

// These tests call only the read-only functions on the real system. The
// functions that change state (Lock, Suspend, ExitWindows, EmptyRecycleBin,
// SetDarkMode, PressMediaKey, DisplayOff) are never called by tests.

func TestQueryRecycleBin(t *testing.T) {
	bin, err := QueryRecycleBin()
	if err != nil {
		t.Fatal(err)
	}
	if bin.Items < 0 || bin.Size < 0 || (bin.Items == 0 && bin.Size != 0) {
		t.Fatalf("recycle bin = %+v", bin)
	}
}

func TestDarkMode(t *testing.T) {
	if _, err := DarkMode(); err != nil {
		t.Fatal(err)
	}
}

func TestVersionAndEdition(t *testing.T) {
	if b := OSBuild(); b < 10240 {
		t.Fatalf("build = %d", b)
	}
	if EditionID() == "" {
		t.Fatal("no edition")
	}
	HibernateAvailable() // must not crash
}
