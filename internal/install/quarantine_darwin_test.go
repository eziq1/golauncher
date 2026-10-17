package install

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestClearQuarantine(t *testing.T) {
	app := makeBundle(t, t.TempDir(), "0.1.0")
	marked := []string{app, BundleExe(app), filepath.Join(app, "Contents", "Info.plist")}
	for _, p := range marked {
		if err := unix.Setxattr(p, QuarantineAttr, []byte("0083;00000000;Safari;"), 0); err != nil {
			t.Skipf("cannot set extended attributes here: %v", err)
		}
	}
	if err := ClearQuarantine(app); err != nil {
		t.Fatal(err)
	}
	for _, p := range marked {
		if _, err := unix.Getxattr(p, QuarantineAttr, nil); !errors.Is(err, unix.ENOATTR) {
			t.Errorf("%s still quarantined: %v", p, err)
		}
	}
	// Files without the attribute are fine.
	if err := ClearQuarantine(app); err != nil {
		t.Fatal(err)
	}
	if err := ClearQuarantine(filepath.Join(app, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
}
