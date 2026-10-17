package agentrt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// The runtime features of R72: permission bridging, notices, Forget,
// candidates, the login check and adapter files.

// events reads a turn's events until its end, without answering anything.
func events(t *testing.T, ch <-chan backend.Event) []backend.Event {
	t.Helper()
	var out []backend.Event
	timeout := time.After(20 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("closed without a final event")
			}
			out = append(out, ev)
			if ev.Kind == backend.EventDone || ev.Kind == backend.EventError {
				return out
			}
		case <-timeout:
			t.Fatal("turn did not end")
		}
	}
}

func logLines(t *testing.T, log string) []string {
	t.Helper()
	b, _ := os.ReadFile(log)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// TestPermissionTimeout: a permission request nobody answers is rejected
// after PermissionTimeout; Done is closed.
func TestPermissionTimeout(t *testing.T) {
	defer func(d time.Duration) { PermissionTimeout = d }(PermissionTimeout)
	PermissionTimeout = 200 * time.Millisecond
	r, _ := newTest(t, testAdapter{}, t.TempDir())
	c, _ := r.Open(context.Background(), backend.OpenOptions{SessionID: "chat-1"})
	ch, err := c.Send(context.Background(), "perm")
	if err != nil {
		t.Fatal(err)
	}
	var req *backend.PermissionRequest
	var text string
	for _, ev := range events(t, ch) {
		switch ev.Kind {
		case backend.EventPermission:
			req = ev.Permission
		case backend.EventText:
			text += ev.Text
		case backend.EventDone:
		default:
			t.Fatalf("event %+v", ev)
		}
	}
	if req == nil || text != "deny" || req.Done == nil {
		t.Fatalf("request %+v, text %q", req, text)
	}
	select {
	case <-req.Done:
	default:
		t.Fatal("Done not closed")
	}
	req.Respond("allow") // too late: nothing happens
}

// TestPermissionStop: stopping the answer rejects a pending request before
// the interrupt is sent.
func TestPermissionStop(t *testing.T) {
	dir := t.TempDir()
	r, log := newTest(t, testAdapter{interrupt: true}, dir)
	c, _ := r.Open(context.Background(), backend.OpenOptions{SessionID: "chat-1"})
	ch, err := c.Send(context.Background(), "perm")
	if err != nil {
		t.Fatal(err)
	}
	ev := <-ch
	if ev.Kind != backend.EventPermission {
		t.Fatalf("event %+v", ev)
	}
	c.Cancel()
	evs := events(t, ch)
	final := evs[len(evs)-1]
	if final.Kind != backend.EventDone || !final.Cancelled {
		t.Fatalf("final %+v", final)
	}
	<-ev.Permission.Done
	if lines := logLines(t, log); !slices.Contains(lines, "got DENY") {
		t.Fatalf("log %q", lines)
	}
	// The conversation goes on.
	if text, final, _ := send(t, c, "next", ""); text != "echo: next" || final.Kind != backend.EventDone {
		t.Fatalf("%q %+v", text, final)
	}
}

// TestResumeLostNotice: a handshake that could not resume the session makes
// the next turn start with a notice.
func TestResumeLostNotice(t *testing.T) {
	r, _ := newTest(t, testAdapter{loseResume: true}, t.TempDir())
	c, _ := r.Open(context.Background(), backend.OpenOptions{SessionID: "chat-1"})
	if evs := events(t, mustSend(t, c, "one")); evs[0].Kind != backend.EventText {
		t.Fatalf("new session: %+v", evs)
	}
	c.Release()
	evs := events(t, mustSend(t, c, "two"))
	if evs[0].Kind != backend.EventNotice || evs[0].Text != backend.NoticeNewSession || evs[1].Text != "echo: two" {
		t.Fatalf("resumed: %+v", evs)
	}
	if evs := events(t, mustSend(t, c, "three")); evs[0].Kind != backend.EventText {
		t.Fatalf("next turn: %+v", evs)
	}
}

func mustSend(t *testing.T, c backend.Conversation, text string) <-chan backend.Event {
	t.Helper()
	ch, err := c.Send(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

// TestForget: a forgotten chat starts a new session even when asked to
// resume.
func TestForget(t *testing.T) {
	r, log := newTest(t, testAdapter{perTurnOnly: true}, t.TempDir())
	c, _ := r.Open(context.Background(), backend.OpenOptions{SessionID: "chat-1"})
	send(t, c, "hi", "")
	c.Close()
	r.Forget("chat-1")
	r.Forget("unknown")
	c2, _ := r.Open(context.Background(), backend.OpenOptions{SessionID: "chat-1", Resume: true})
	send(t, c2, "again", "")
	if s := starts(t, log); len(s) != 2 || s[1] != "turn" {
		t.Fatalf("starts %q", s)
	}
}

// copyExe copies the test binary to dir/name.
func copyExe(t *testing.T, dir, name string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCandidates: of several CLIs found, Check uses the one with the
// highest version that is new enough; the adapter's reason is reported when
// none is found.
func TestCandidates(t *testing.T) {
	dir := t.TempDir()
	old, mid, high := copyExe(t, dir, "fake-0.9.0.exe"), copyExe(t, dir, "fake-1.2.0.exe"), copyExe(t, dir, "fake-1.5.0.exe")
	newR := func(c func() ([]string, error)) *Runtime {
		r := New(testAdapter{perTurnOnly: true, candidates: c}, Options{Dir: filepath.Join(dir, "work"),
			Env: []string{"AGENTRT_FAKE=1", "AGENTRT_FAKE_LOG=" + filepath.Join(dir, "log.txt")}})
		t.Cleanup(func() { r.Close() })
		return r
	}
	st, err := newR(func() ([]string, error) { return []string{mid, old, high}, nil }).Check(context.Background())
	if err != nil || st.Version != "1.5.0" {
		t.Fatalf("Check = %+v, %v", st, err)
	}
	_, err = newR(func() ([]string, error) { return []string{old}, nil }).Check(context.Background())
	if backend.KindOf(err) != backend.ErrVersion {
		t.Fatalf("too old: %v", err)
	}
	_, err = newR(func() ([]string, error) { return nil, errors.New("only a launcher script was found") }).Check(context.Background())
	if e, ok := errors.AsType[*backend.Error](err); !ok || e.Kind != backend.ErrNotInstalled || e.Message != "only a launcher script was found" {
		t.Fatalf("none: %v", err)
	}
	_, err = newR(func() ([]string, error) { return nil, nil }).Check(context.Background())
	if backend.KindOf(err) != backend.ErrNotInstalled {
		t.Fatalf("nothing: %v", err)
	}
	// The configured path wins over the candidates.
	r := New(testAdapter{perTurnOnly: true, candidates: func() ([]string, error) { return []string{high}, nil }}, Options{Path: mid,
		Dir: filepath.Join(dir, "work"), Env: []string{"AGENTRT_FAKE=1"}})
	defer r.Close()
	if st, err := r.Check(context.Background()); err != nil || st.Version != "1.2.0" {
		t.Fatalf("configured: %+v, %v", st, err)
	}
}

// TestLoginCheck: with Info.LoginArgs, Check reports a CLI that is not
// signed in.
func TestLoginCheck(t *testing.T) {
	r, _ := newTest(t, testAdapter{perTurnOnly: true, login: true}, t.TempDir())
	if _, err := r.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRT_FAKE_LOGIN", "fail")
	_, err := r.Check(context.Background())
	if e, ok := errors.AsType[*backend.Error](err); !ok || e.Kind != backend.ErrNotLoggedIn || e.Message != "Not logged in" {
		t.Fatalf("Check: %v", err)
	}
}

// TestFiles: the adapter's files are written into the working directory
// before the process starts (with the CLI's version), not by a preview.
func TestFiles(t *testing.T) {
	dir := t.TempDir()
	r, _ := newTest(t, testAdapter{perTurnOnly: true, files: true}, dir)
	path := filepath.Join(dir, "work", "fake-settings.json")
	if _, err := r.Command(backend.OpenOptions{SessionID: "chat-1"}, "fake.exe", false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the preview wrote a file")
	}
	c, _ := r.Open(context.Background(), backend.OpenOptions{SessionID: "chat-1"})
	send(t, c, "hi", "")
	if b, err := os.ReadFile(path); err != nil || string(b) != `{"version":"1.2.3"}` {
		t.Fatalf("file %q, %v", b, err)
	}
	if err := writeFiles(dir, map[string][]byte{`..\x.json`: nil}); err == nil {
		t.Fatal("a path outside the directory was accepted")
	}
}
