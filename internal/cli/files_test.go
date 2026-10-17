package cli

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// attachPlugin's "chat" view accepts files and shows them as chips (in the
// root search its ReceiveFiles is no longer called, R97). Its state lives in the
// process, but each glctl session invocation replays the steps on a new
// instance: reset clears it.
type attachPlugin struct {
	mu       sync.Mutex
	attached []string
}

func (p *attachPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "test.attach", Name: "Attach Test", Version: "1", Commands: []plugin.Command{
		{Name: "chat", Title: "Attach Chat", Mode: plugin.ModeView},
	}}
}

func (p *attachPlugin) Init(context.Context, plugin.Host) error {
	p.mu.Lock()
	p.attached = nil
	p.mu.Unlock()
	return nil
}
func (p *attachPlugin) Close() error { return nil }

func (p *attachPlugin) View(context.Context, plugin.ViewRequest) (plugin.View, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	payload, _ := json.Marshal(plugin.ChooseFilesOptions{Multiple: true})
	v := plugin.View{Kind: plugin.ViewDetail, Title: "Attach", AcceptFiles: true, Detail: &plugin.Detail{Markdown: "chat"},
		Sections: []plugin.Section{{Items: []plugin.Item{{ID: "chat", Title: "Chat", Actions: []plugin.Action{
			{ID: "attach", Title: "Add Attachment…", Shortcut: "Ctrl+O", Do: plugin.ActionDo{Kind: plugin.ActionChooseFiles, Payload: payload}},
		}}}}}}
	for i, name := range p.attached {
		id := fmt.Sprintf("c%d", i)
		v.Chips = append(v.Chips, plugin.Chip{ID: id, Title: name, Subtitle: "file", Error: strings.HasSuffix(name, ".bin"),
			Remove: &plugin.Action{ID: "remove", Title: "Remove", Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: id}}})
	}
	return v, nil
}

func (p *attachPlugin) ReceiveFiles(_ context.Context, f plugin.Files) (plugin.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, path := range f.Paths {
		p.attached = append(p.attached, filepath.Base(path))
	}
	if f.Instance == "" {
		return plugin.Result{Push: &plugin.ViewRequest{ID: "chat"}}, nil
	}
	return plugin.Result{Refresh: true}, nil
}

func (p *attachPlugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var i int
	if _, err := fmt.Sscanf(a.Target, "c%d", &i); err == nil && i < len(p.attached) {
		p.attached = slices.Delete(p.attached, i, i+1)
	}
	return plugin.Result{Refresh: true}, nil
}

func TestSessionFiles(t *testing.T) {
	fakeExtras = []plugin.Plugin{&attachPlugin{}}
	t.Cleanup(func() { fakeExtras = nil })
	s := filepath.Join(t.TempDir(), "s.json")
	if _, code := glctl(t, "session", "new", "-s", s); code != 0 {
		t.Fatal("session new failed")
	}
	text := func(args ...string) string {
		t.Helper()
		out, code := glctl(t, append(append([]string{"session"}, args[0], "-s", s, "--format", "text"), args[1:]...)...)
		if code != 0 {
			t.Fatalf("%v failed:\n%s", args, out)
		}
		return out
	}
	// Dropped on the view (in the root search they go to its Files section).
	text("type", "attach chat")
	text("key", "Enter")
	out := text("drop", "notes.txt", "data.bin")
	if !strings.Contains(out, "view: Attach (depth 1)") || !strings.Contains(out, "chips: notes.txt (file), data.bin (file) [error]") {
		t.Fatalf("drop:\n%s", out)
	}
	// The open dialog answers with the choose step's files.
	text("choose", "more.txt")
	out = text("key", "Ctrl+O")
	if !strings.Contains(out, "chips: notes.txt (file), data.bin (file) [error], more.txt (file)") {
		t.Fatalf("choose:\n%s", out)
	}
	// Without an answer left, the dialog is cancelled.
	out = text("key", "Ctrl+O")
	if strings.Count(out, "(file)") != 3 {
		t.Fatalf("cancelled dialog:\n%s", out)
	}
	out = text("remove-chip", "c1")
	if !strings.Contains(out, "chips: notes.txt (file), more.txt (file)\n") {
		t.Fatalf("remove-chip:\n%s", out)
	}
	// JSON: the snapshot has the chips and acceptFiles; replaying the steps
	// gives the same state.
	js, code := glctl(t, "session", "show", "-s", s)
	if code != 0 || !strings.Contains(js, `"acceptFiles": true`) || !strings.Contains(js, `"title": "more.txt"`) {
		t.Fatalf("show:\n%s", js)
	}
	if _, code := glctl(t, "session", "drop", "-s", s); code != 2 {
		t.Fatal("drop without files is a usage error")
	}
	if _, code := glctl(t, "session", "remove-chip", "-s", s, "nosuch"); code == 0 {
		t.Fatal("removing an unknown chip succeeded")
	}
}

// TestSessionRootFiles (R97): files dropped onto the root search show as
// chips with the Files section at the top (AI is off in the fake
// environment: its row turns it on); typing filters the section, Ctrl+O
// chooses files for it, a chip's × drops one, Escape drops them all.
func TestSessionRootFiles(t *testing.T) {
	dir := t.TempDir()
	notes, shot := filepath.Join(dir, "notes.txt"), filepath.Join(dir, "shot.png")
	for _, p := range []string{notes, shot} {
		if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := filepath.Join(t.TempDir(), "s.json")
	if _, code := glctl(t, "session", "new", "-s", s); code != 0 {
		t.Fatal("session new failed")
	}
	text := func(args ...string) string {
		t.Helper()
		out, code := glctl(t, append(append([]string{"session"}, args[0], "-s", s, "--format", "text"), args[1:]...)...)
		if code != 0 {
			t.Fatalf("%v failed:\n%s", args, out)
		}
		// The temporary folder differs between runs.
		for _, d := range []string{strings.ReplaceAll(dir, `\`, `\\`), dir} {
			out = strings.ReplaceAll(out, d, "<DIR>")
		}
		return strings.ReplaceAll(out, `\\`, "/")
	}
	out := text("drop", notes)
	golden(t, "session_root_files", out)
	out = text("type", "zip")
	if !strings.Contains(out, "[Files]\n> Compress to ZIP — notes.txt") || strings.Contains(out, "Turn on AI") {
		t.Fatalf("typed:\n%s", out)
	}
	text("type", "")
	text("choose", notes, shot)
	out = text("key", "Ctrl+O")
	if !strings.Contains(out, "chips: notes.txt (5 B), shot.png (5 B)\n") || strings.Contains(out, "Copy Image") {
		t.Fatalf("chosen:\n%s", out)
	}
	out = text("remove-chip", "file:0")
	if !strings.Contains(out, "chips: shot.png (5 B)\n") || !strings.Contains(out, "Copy Image — shot.png") {
		t.Fatalf("removed:\n%s", out)
	}
	out = text("key", "Escape")
	if strings.Contains(out, "chips:") || strings.Contains(out, "[Files]") {
		t.Fatalf("after Escape:\n%s", out)
	}
}
