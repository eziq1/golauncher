package winselection

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The real UI Automation tests read the selection of windows that a helper
// child process creates (helper_windows_test.go), so they need a desktop
// and take the foreground for a moment. They only run when asked for:
//
//	GOLAUNCHER_SELECTION_TESTS=1 go test -run . -v ./internal/winapi/selection
//
// They never send keys, never touch a window they did not create and never
// use the clipboard method.
const selectionTestsEnv = "GOLAUNCHER_SELECTION_TESTS"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		runHelper()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// helperProc is the running helper process.
type helperProcess struct {
	t     *testing.T
	cmd   *exec.Cmd
	in    io.WriteCloser
	out   *bufio.Scanner
	hwnd  uintptr
	pid   int
	lines chan string
}

func startHelper(t *testing.T) *helperProcess {
	t.Helper()
	if os.Getenv(selectionTestsEnv) != "1" {
		t.Skipf("set %s=1 to run the real UI Automation tests (they show a test window)", selectionTestsEnv)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	procAllowSetForeground.Call(uintptr(cmd.Process.Pid)) // allowed when the test itself has the foreground
	h := &helperProcess{t: t, cmd: cmd, in: in, pid: cmd.Process.Pid, lines: make(chan string, 16)}
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			h.lines <- sc.Text()
		}
		close(h.lines)
	}()
	t.Cleanup(func() {
		fmt.Fprintln(in, "quit")
		in.Close()
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
		}
	})
	line := h.line()
	v, ok := strings.CutPrefix(line, "ready ")
	if !ok {
		t.Fatalf("helper: %q", line)
	}
	hwnd, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	h.hwnd = uintptr(hwnd)
	return h
}

func (h *helperProcess) line() string {
	h.t.Helper()
	select {
	case l, ok := <-h.lines:
		if !ok {
			h.t.Fatal("helper exited")
		}
		return l
	case <-time.After(10 * time.Second):
		h.t.Fatal("helper did not answer")
	}
	return ""
}

// selectIn selects [start, end) in a control of the helper, focuses it and
// waits until the helper's window is the foreground window; it skips the
// test when Windows does not let it take the foreground.
func (h *helperProcess) selectIn(control string, start, end int) {
	h.t.Helper()
	fmt.Fprintf(h.in, "sel %s %d %d\n", control, start, end)
	if l := h.line(); !strings.HasPrefix(l, "ok") {
		h.t.Fatalf("helper: %q", l)
	}
	for range 20 {
		if fg, _, _ := procGetForegroundWindow.Call(); fg == h.hwnd {
			return
		}
		time.Sleep(50 * time.Millisecond)
		fmt.Fprintln(h.in, "fg")
		h.line()
	}
	h.t.Skip("the helper window could not take the foreground (Windows focus rules); run the test from a foreground terminal")
}

// capture runs both phases against the helper window. It refuses to read
// when the focused element is not the helper's: the tests never read other
// apps' selections.
func (h *helperProcess) capture(o Options) (p *Pending, begin, read time.Duration, text string, editable, ok bool) {
	h.t.Helper()
	if fg, _, _ := procGetForegroundWindow.Call(); fg != h.hwnd {
		h.t.Fatal("helper window lost the foreground")
	}
	start := time.Now()
	p = Begin(h.hwnd, o)
	begin = time.Since(start)
	if p.NeedsCopy() {
		h.t.Fatal("the clipboard method was chosen")
	}
	if p.method == methodUIA && p.app.PID != h.pid {
		<-uia.do(p.elem.release)
		h.t.Fatalf("focused element of another process (pid %d), not read", p.app.PID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	start = time.Now()
	sel, ok := p.Read(ctx)
	read = time.Since(start)
	return p, begin, read, sel.Text, sel.Editable, ok
}

// utf16Offsets converts rune positions in s to UTF-16 positions, as
// EM_SETSEL takes them.
func utf16Offsets(s string, runes ...int) []int {
	var out []int
	for _, r := range runes {
		n := 0
		for i, c := range []rune(s) {
			if i == r {
				break
			}
			n++
			if c > 0xFFFF {
				n++
			}
		}
		out = append(out, n)
	}
	return out
}

func TestSelectionReal(t *testing.T) {
	h := startHelper(t)
	richRunes := []rune(helperRichText)
	richSel := utf16Offsets(helperRichText, 12, 18) // "你好世界 😀"
	tests := []struct {
		name       string
		control    string
		start, end int
		maxChars   int
		want       string
		ok         bool
		editable   bool
		truncated  bool
	}{
		{"edit", "edit", 4, 19, 0, "quick brown fox", true, true, false},
		{"edit no selection", "edit", 3, 3, 0, "", false, false, false},
		{"edit truncated", "edit", 4, 19, 5, "quick", true, true, true},
		{"rich", "rich", richSel[0], richSel[1], 0, string(richRunes[12:18]), true, false, false},
		{"rich truncated before emoji", "rich", richSel[0], richSel[1], 5, string(richRunes[12:17]), true, false, true},
		{"rich no selection", "rich", 0, 0, 0, "", false, false, false},
		{"password", "password", 0, 5, 0, "", false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h.t = t
			h.selectIn(tt.control, tt.start, tt.end)
			p, begin, read, text, editable, ok := h.capture(Options{MaxChars: tt.maxChars})
			t.Logf("method=%s begin=%v read=%v ok=%v editable=%v", p.Method(), begin, read, ok, editable)
			if ok != tt.ok || text != tt.want {
				t.Errorf("Read = %q, %v; want %q, %v", text, ok, tt.want, tt.ok)
			}
			if tt.control == "password" && p.Method() != "none" {
				t.Errorf("password method = %s", p.Method())
			}
			if tt.control == "edit" && tt.ok && !editable {
				t.Error("EDIT not editable")
			}
			if tt.ok && p.sel.Truncated != tt.truncated {
				t.Errorf("Truncated = %v", p.sel.Truncated)
			}
		})
	}
}

func TestSelectionLatencyReal(t *testing.T) {
	h := startHelper(t)
	const runs = 20
	cases := []struct {
		control    string
		start, end int
	}{
		{"edit", 4, 19},
		{"rich", 0, 11},
		{"password", 0, 5},
	}
	// The first capture includes starting the UI Automation thread and
	// creating the client.
	h.selectIn("edit", 4, 19)
	_, coldBegin, coldRead, _, _, _ := h.capture(Options{})
	t.Logf("cold (first capture): begin=%v read=%v", coldBegin, coldRead)
	for _, c := range cases {
		h.t = t
		h.selectIn(c.control, c.start, c.end)
		var begins, reads []time.Duration
		for range runs {
			_, b, r, _, _, _ := h.capture(Options{})
			begins = append(begins, b)
			reads = append(reads, r)
		}
		t.Logf("%-8s phase 1 (Begin): %s", c.control, stats(begins))
		t.Logf("%-8s phase 2 (Read):  %s", c.control, stats(reads))
	}
}

func stats(d []time.Duration) string {
	s := slices.Clone(d)
	slices.Sort(s)
	p95 := s[min(len(s)-1, (len(s)*95+99)/100-1)]
	return fmt.Sprintf("median=%v p95=%v max=%v", s[len(s)/2], p95, s[len(s)-1])
}
