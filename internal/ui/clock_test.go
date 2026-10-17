package ui_test

import (
	"testing"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// TestToastExpiresOnTesterClock checks that a footer toast goes by the
// window's clock, which MyGo's Tester only moves with Advance (MyGo v0.5.5):
// it stays however long the test takes, and goes once its 2.5 s are up.
func TestToastExpiresOnTesterClock(t *testing.T) {
	lt := newLauncher(t, "en")
	lt.Controller.ShowToast(plugin.Toast{Title: "Copied to clipboard"})
	lt.step()
	lt.wantText("Copied to clipboard")

	lt.tt.Advance(2400 * time.Millisecond)
	lt.step()
	lt.wantText("Copied to clipboard")
	if lt.Controller.Snapshot().Toast == nil {
		t.Fatal("the toast was cleared before its time")
	}

	lt.tt.Advance(200 * time.Millisecond)
	lt.step()
	if lt.tt.HasText("Copied to clipboard") || lt.Controller.Snapshot().Toast != nil {
		t.Fatalf("the toast outlived its time: %q", lt.tt.Texts())
	}
}
