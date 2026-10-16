//go:build windows

package clip

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procSendMessageW     = user32.NewProc("SendMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
)

// The helper's EDIT control holds helperText with helperSelection selected.
const (
	helperText      = "golauncher clip test: alpha beta 世界 gamma"
	helperSelection = "beta 世界"
)

// runEditHelper is the helper process of TestCopySelectionReal: a top-level
// EDIT window holding fixed fake text with part of it selected, brought to
// the foreground. It prints the window handle and exits when stdin closes.
func runEditHelper() {
	runtime.LockOSThread()
	const (
		wsOverlappedWindow = 0x00CF0000
		wsVisible          = 0x10000000
		esAutoHScroll      = 0x0080
		emSetSel           = 0x00B1
	)
	inst, _, _ := procGetModuleHandleW.Call(0)
	cls, _ := windows.UTF16PtrFromString("EDIT")
	title, _ := windows.UTF16PtrFromString(helperText)
	h, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)),
		wsOverlappedWindow|wsVisible|esAutoHScroll, 200, 200, 520, 90, 0, 0, inst, 0)
	if h == 0 {
		fmt.Fprintln(os.Stderr, "CreateWindowEx:", err)
		os.Exit(1)
	}
	// EM_SETSEL counts UTF-16 code units.
	start := len(utf16.Encode([]rune(helperText[:strings.Index(helperText, helperSelection)])))
	end := start + len(utf16.Encode([]rune(helperSelection)))
	procSendMessageW.Call(h, emSetSel, uintptr(start), uintptr(end))
	_ = activate(h)
	fmt.Println(h)
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}()
	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			os.Exit(0)
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// TestCopySelectionReal copies the selection of its own helper window with
// the real clipboard and keyboard, 20 times, and logs the timings. Keys are
// only sent while the helper window is the foreground window.
func TestCopySelectionReal(t *testing.T) {
	realClipboard(t)
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), helperEnv+"=edit")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stdin.Close()
		_ = cmd.Wait()
	}()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("helper: %v", err)
	}
	h, err := strconv.ParseUint(strings.TrimSpace(line), 10, 64)
	if err != nil || h == 0 {
		t.Fatalf("helper printed %q", line)
	}
	hwnd := uintptr(h)

	fired := make(chan struct{}, 100)
	stop, err := Watch(func() { fired <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	f := formats()
	private := uint32(registerFormat("golauncher.test.private"))
	content := []clipData{
		{cfUnicodeText, utf16Bytes("clipboard content before the copy")},
		{uintptr(private), []byte{1, 2, 3, 4, 5}},
	}
	wantAfter := []uint32{cfUnicodeText, private, uint32(f.exclude), uint32(f.history), uint32(f.cloud)}

	const runs = 20
	var save, wait, rest, total []time.Duration
	for i := range runs {
		if err := write(content...); err != nil {
			t.Fatal(err)
		}
		before, err := snapshot()
		if err != nil {
			t.Fatal(err)
		}
		// Let other clipboard listeners (Windows clipboard history, clipboard
		// managers) read the new content first: the EDIT control does not
		// retry when it finds the clipboard open, and its copy is then lost.
		time.Sleep(150 * time.Millisecond)
		if err := activate(hwnd); err != nil || ForegroundWindow() != hwnd {
			t.Skipf("helper window could not be brought to the foreground: %v", err)
		}
		seq := sequenceNumber()
		start := time.Now()
		res, err := CopySelection(hwnd, CopyOptions{MaxChars: 100})
		took := time.Since(start)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if res.Text != helperSelection || res.Truncated || !res.Restored {
			t.Fatalf("run %d: %+v", i, res)
		}
		after, err := snapshot()
		if err != nil {
			t.Fatal(err)
		}
		// Byte-for-byte what was there, plus the markers.
		var got []uint32
		for _, it := range after {
			got = append(got, it.format)
			if j := slices.IndexFunc(before, func(b savedFormat) bool { return b.format == it.format }); j >= 0 &&
				string(before[j].data) != string(it.data) {
				t.Fatalf("run %d: format %#x changed", i, it.format)
			}
		}
		if !slices.Equal(got, wantAfter) {
			t.Fatalf("run %d: formats after restore %#x, want %#x", i, got, wantAfter)
		}
		if s := sequenceNumber(); s == seq {
			t.Fatalf("run %d: sequence number unchanged", i)
		}
		save, wait, rest, total = append(save, res.Save), append(wait, res.Wait), append(rest, res.Restore), append(total, took)
	}
	select {
	case <-fired:
		t.Error("Watch fired for the temporary copy or the restore")
	case <-time.After(3 * debounceDelay):
	}
	for _, s := range []struct {
		name string
		d    []time.Duration
	}{{"save", save}, {"wait", wait}, {"restore", rest}, {"total", total}} {
		slices.Sort(s.d)
		t.Logf("%-7s median %v  p95 %v  max %v", s.name, s.d[len(s.d)/2], s.d[len(s.d)*95/100], s.d[len(s.d)-1])
	}
}
