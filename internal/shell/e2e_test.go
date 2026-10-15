package shell

import (
	"os"
	"path/filepath"
	"testing"

	"golauncher/internal/app"
	"golauncher/internal/control"
)

// TestRunningInstance drives a running golauncher through its control
// channel. It runs only with GOLAUNCHER_E2E=1, against the instance of the
// current user.
func TestRunningInstance(t *testing.T) {
	if os.Getenv("GOLAUNCHER_E2E") == "" {
		t.Skip("set GOLAUNCHER_E2E=1 with golauncher running")
	}
	dir, err := app.DefaultDataDir()
	if err != nil {
		t.Fatal(err)
	}
	c, err := control.Dial(filepath.Join(dir, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	do := func(req control.Request) control.Response {
		t.Helper()
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("%+v: %v", req, err)
		}
		return resp
	}
	if resp := do(control.Request{Cmd: "show"}); !resp.Visible || resp.Snapshot.Depth != 0 || resp.Snapshot.Text != "" {
		t.Fatalf("show: visible %v, snapshot %+v", resp.Visible, resp.Snapshot)
	}
	resp := do(control.Request{Cmd: "type", Text: "12*3+4"})
	if e, ok := resp.Snapshot.SelectedEntry(); !ok || e.Title != "40" {
		t.Fatalf("calculator result: %+v", resp.Snapshot.Sections)
	}
	if resp := do(control.Request{Cmd: "key", Key: "Escape"}); resp.Snapshot.Text != "" || !resp.Visible {
		t.Fatalf("Escape should clear the text: %+v", resp.Snapshot)
	}
	if resp := do(control.Request{Cmd: "key", Key: "Escape"}); resp.Visible {
		t.Fatal("Escape on the empty root should hide the launcher")
	}
}
