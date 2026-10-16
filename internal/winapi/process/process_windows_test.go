package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// These tests end only processes they start themselves (PING.EXE, cmd.exe)
// and close only a window they create. Tests reading the real system are
// opt-in and log only durations and counts, never names, paths or users.

// realTests skips tests that read every process of the real system.
func realTests(t testing.TB) {
	t.Helper()
	if os.Getenv("GOLAUNCHER_PROCESS_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_PROCESS_TESTS=1 to run")
	}
}

// --- Parsing ------------------------------------------------------------------

// putEntry writes a SYSTEM_PROCESS_INFORMATION at off with its name at
// nameOff, and returns the offset after the name, aligned to 8.
func putEntry(buf []byte, off, nameOff int, name string, fill func(e *windows.SYSTEM_PROCESS_INFORMATION)) int {
	e := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[off]))
	fill(e)
	end := nameOff
	if name != "" {
		u := windows.StringToUTF16(name)
		u = u[:len(u)-1]
		copy(unsafe.Slice((*uint16)(unsafe.Pointer(&buf[nameOff])), len(u)), u)
		e.ImageName = windows.NTUnicodeString{Length: uint16(len(u) * 2), MaximumLength: uint16(len(u) * 2), Buffer: (*uint16)(unsafe.Pointer(&buf[nameOff]))}
		end += len(u) * 2
	}
	return (end + 7) &^ 7
}

func TestParseProcesses(t *testing.T) {
	size := int(unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{}))
	buf := alignedBuf(4 * (size + 64))
	off1 := putEntry(buf, 0, size, "", func(e *windows.SYSTEM_PROCESS_INFORMATION) {
		e.UniqueProcessID = 0
		e.NumberOfThreads = 8
		e.KernelTime = 5000
	})
	off2 := putEntry(buf, off1, off1+size, "contoso.exe", func(e *windows.SYSTEM_PROCESS_INFORMATION) {
		e.UniqueProcessID = 1234
		e.InheritedFromUniqueProcessID = 600
		e.CreateTime = 133700000000000000
		e.SessionID = 1
		e.NumberOfThreads = 12
		e.HandleCount = 345
		e.BasePriority = 8
		e.UserTime, e.KernelTime = 300, 200
		e.WorkingSetPrivateSize = 10 << 20
		e.WorkingSetSize = 30 << 20
		e.PrivatePageCount = 40 << 20
		e.PeakWorkingSetSize = 50 << 20
		e.ReadTransferCount, e.WriteTransferCount, e.OtherTransferCount = 1000, 24, 999
	})
	end := putEntry(buf, off2, off2+size, "Fabrikam Сервис.exe", func(e *windows.SYSTEM_PROCESS_INFORMATION) {
		e.UniqueProcessID = 4321
		e.InheritedFromUniqueProcessID = 1234
		e.CreateTime = 133700000000000001
		e.NumberOfThreads = 1
	})
	(*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[0])).NextEntryOffset = uint32(off1)
	(*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[off1])).NextEntryOffset = uint32(off2 - off1)

	got := parseProcesses(buf[:end], 0)
	want := []Proc{
		{ID: ID{0, 0}, Name: "System Idle Process", Threads: 8, CPU: 5000},
		{ID: ID{1234, 133700000000000000}, Parent: 600, Name: "contoso.exe", Session: 1, Threads: 12, Handles: 345, Priority: 8,
			CPU: 500, PrivateWorkingSet: 10 << 20, WorkingSet: 30 << 20, Commit: 40 << 20, PeakWorkingSet: 50 << 20, IO: 1024},
		{ID: ID{4321, 133700000000000001}, Parent: 1234, Name: "Fabrikam Сервис.exe", Threads: 1},
	}
	if !slices.Equal(got, want) {
		t.Errorf("parsed\n%+v\nwant\n%+v", got, want)
	}

	// The last name runs past a cut buffer: it is left empty, nothing is
	// read outside.
	if got := parseProcesses(buf[:off2+size+4], 0); len(got) != 3 || got[2].Name != "" || got[1].Name != "contoso.exe" {
		t.Errorf("cut = %+v", got)
	}
	// A buffer shorter than an entry has no processes.
	if got := parseProcesses(buf[:size-1], 0); len(got) != 0 {
		t.Errorf("short = %+v", got)
	}
	if got := parseProcesses(nil, 0); len(got) != 0 {
		t.Errorf("empty = %+v", got)
	}
	// An offset past the end stops the chain.
	(*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[off1])).NextEntryOffset = 1 << 20
	if got := parseProcesses(buf[:end], 0); len(got) != 2 {
		t.Errorf("bad offset = %d processes", len(got))
	}
	// A name pointing outside the buffer is not read.
	outside := windows.StringToUTF16("northwind.exe")
	(*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[off1])).ImageName.Buffer = &outside[0]
	if got := parseProcesses(buf[:end], 0); got[1].Name != "" {
		t.Errorf("outside name = %q", got[1].Name)
	}
}

func TestUnicodeIn(t *testing.T) {
	buf := alignedBuf(256)
	hdr := int(unsafe.Sizeof(windows.NTUnicodeString{}))
	u := windows.StringToUTF16(`contoso.exe --flag`)
	u = u[:len(u)-1]
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(&buf[hdr])), len(u)), u)
	*(*windows.NTUnicodeString)(unsafe.Pointer(&buf[0])) = windows.NTUnicodeString{Length: uint16(2 * len(u)), MaximumLength: uint16(2 * len(u)), Buffer: (*uint16)(unsafe.Pointer(&buf[hdr]))}
	if got := unicodeIn(buf); got != `contoso.exe --flag` {
		t.Errorf("got %q", got)
	}
	if got := unicodeIn(buf[:hdr+4]); got != "" {
		t.Errorf("cut = %q", got)
	}
}

func TestIntegrityName(t *testing.T) {
	for rid, want := range map[uint32]string{0: "", 0x1000: "Low", 0x2000: "Medium", 0x2100: "Medium", 0x3000: "High", 0x4000: "System", 0x5000: "System"} {
		if got := integrityName(rid); got != want {
			t.Errorf("%#x = %q, want %q", rid, got, want)
		}
	}
}

// --- Processes started by the tests --------------------------------------------

// child is a process started by a test.
type child struct {
	cmd  *exec.Cmd
	id   ID
	done chan struct{}
}

// start starts exe with args, and ends it when the test ends.
func start(t *testing.T, exe string, args ...string) *child {
	t.Helper()
	cmd := exec.Command(exe, args...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &child{cmd: cmd, done: make(chan struct{})}
	go func() {
		cmd.Wait()
		close(c.done)
	}()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-c.done
	})
	id, err := Identify(uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	c.id = id
	return c
}

func startPing(t *testing.T) *child {
	return start(t, filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE"), "-n", "30", "127.0.0.1")
}

func (c *child) running() bool {
	select {
	case <-c.done:
		return false
	case <-time.After(200 * time.Millisecond):
		return true
	}
}

func (c *child) ended(t *testing.T) {
	t.Helper()
	select {
	case <-c.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the child did not end")
	}
}

func TestKillChild(t *testing.T) {
	c := startPing(t)
	if c.id.Created == 0 {
		t.Fatalf("id = %v", c.id)
	}
	// Its command line can be read (it is ours).
	if line, err := CommandLine(c.id); err != nil || !strings.Contains(line, "127.0.0.1") {
		t.Errorf("command line: %q %v", line, err)
	}
	if _, err := CommandLine(ID{c.id.PID, c.id.Created + 1}); !errors.Is(err, ErrGone) {
		t.Errorf("command line of another instance: %v", err)
	}
	if info := ReadInfo(ID{c.id.PID, c.id.Created + 1}); info != (Info{}) {
		t.Errorf("info of another instance: %+v", info)
	}
	// Another instance of the PID: nothing is ended.
	if err := Kill(ID{c.id.PID, c.id.Created + 1}); !errors.Is(err, ErrGone) {
		t.Fatalf("wrong creation time: %v", err)
	}
	if !c.running() {
		t.Fatal("the child ended with a wrong creation time")
	}
	// Waiting for a running process gives up at the deadline.
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	if got := Wait(ctx, []ID{c.id}); len(got) != 0 {
		t.Errorf("wait for a running process = %v", got)
	}
	cancel()
	if err := Kill(c.id); err != nil {
		t.Fatal(err)
	}
	c.ended(t)
	ctx, cancel = context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if got := Wait(ctx, []ID{c.id}); !slices.Equal(got, []ID{c.id}) {
		t.Errorf("wait = %v", got)
	}
	if err := Kill(c.id); !errors.Is(err, ErrGone) {
		t.Errorf("killing it again: %v", err)
	}
}

func TestKillRefusesProtected(t *testing.T) {
	self, err := Identify(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []ID{{0, 0}, {4, 0}, {4, 1}, self, {self.PID, 0}} {
		if err := Kill(id); !errors.Is(err, ErrProtected) {
			t.Errorf("Kill(%v) = %v", id, err)
		}
	}
	// The tree of a protected process is left alone, also its children (a
	// ping this test started).
	c := startPing(t)
	if r := KillTree(self); len(r.Protected) != 1 || len(r.Ended)+len(r.Failed)+len(r.NeedsAdmin) != 0 {
		t.Errorf("KillTree(self) = %+v", r)
	}
	if !c.running() {
		t.Error("KillTree(self) ended a child of the protected root")
	}
}

func TestKillTree(t *testing.T) {
	root := filepath.Join(os.Getenv("SystemRoot"), "System32")
	c := start(t, filepath.Join(root, "cmd.exe"), "/c", filepath.Join(root, "PING.EXE"), "-n", "30", "127.0.0.1")
	var ping ID
	var s Sampler
	for deadline := time.Now().Add(10 * time.Second); ping.PID == 0 && time.Now().Before(deadline); {
		snap, err := s.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range snap.Procs {
			// By name: cmd.exe may also start a conhost.exe, sooner or later.
			if p.Parent == c.id.PID && p.Created >= c.id.Created && strings.EqualFold(p.Name, "PING.EXE") {
				ping = p.ID
			}
		}
		if ping.PID == 0 {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if ping.PID == 0 {
		t.Fatal("the ping child did not start")
	}
	t.Cleanup(func() { Kill(ping) }) // ours: started by our cmd.exe
	r := KillTree(c.id)
	// The tree may hold a conhost.exe too: ping and cmd must both be ended,
	// nothing may fail.
	if !slices.Contains(r.Ended, c.id) || !slices.Contains(r.Ended, ping) || len(r.Failed)+len(r.NeedsAdmin)+len(r.Protected) != 0 {
		t.Errorf("report = %+v", r)
	}
	c.ended(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if got := Wait(ctx, []ID{c.id, ping}); !slices.Equal(got, []ID{c.id, ping}) {
		t.Errorf("wait = %v", got)
	}
	if r := KillTree(c.id); !slices.Equal(r.Gone, []ID{c.id}) {
		t.Errorf("again = %+v", r)
	}
}

func TestHelperMainEndsChild(t *testing.T) {
	c := startPing(t)
	if code := HelperMain([]string{"kill", "--pid", "12a"}); code != ExitUsage {
		t.Errorf("bad args = %d", code)
	}
	if code := HelperMain(HelperRequest{ID: ID{c.id.PID, c.id.Created + 1}}.Args()); code != ExitGone {
		t.Errorf("another instance = %d", code)
	}
	if !c.running() {
		t.Fatal("ended with a wrong creation time")
	}
	if code := HelperMain(HelperRequest{ID: c.id}.Args()); code != ExitOK {
		t.Fatalf("exit code %d", code)
	}
	c.ended(t)
}

// --- Windows ------------------------------------------------------------------

var (
	testUser32          = windows.NewLazySystemDLL("user32.dll")
	procRegisterClassEx = testUser32.NewProc("RegisterClassExW")
	procCreateWindowEx  = testUser32.NewProc("CreateWindowExW")
	procDefWindowProc   = testUser32.NewProc("DefWindowProcW")
	procDestroyWindow   = testUser32.NewProc("DestroyWindow")
	procGetMessage      = testUser32.NewProc("GetMessageW")
	procDispatchMessage = testUser32.NewProc("DispatchMessageW")
	procPostQuitMessage = testUser32.NewProc("PostQuitMessage")
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
	Private uint32
}

var gotClose atomic.Bool

var testClass = sync.OnceValues(func() (*uint16, error) {
	name, _ := windows.UTF16PtrFromString("golauncher.process.test")
	wc := wndClassEx{ClassName: name, WndProc: windows.NewCallback(func(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
		switch m {
		case wmClose:
			gotClose.Store(true)
			procDestroyWindow.Call(hwnd)
			return 0
		case 2: // WM_DESTROY
			procPostQuitMessage.Call(0)
			return 0
		}
		r, _, _ := procDefWindowProc.Call(hwnd, uintptr(m), wp, lp)
		return r
	})}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return nil, err
	}
	return name, nil
})

// TestCloseWindows closes a hidden top-level window the test creates.
func TestCloseWindows(t *testing.T) {
	class, err := testClass()
	if err != nil {
		t.Fatal(err)
	}
	gotClose.Store(false)
	hwndc := make(chan uintptr, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		title, _ := windows.UTF16PtrFromString("golauncher process test")
		hwnd, _, _ := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), 0,
			0, 0, 100, 100, 0, 0, 0, 0)
		hwndc <- hwnd
		if hwnd == 0 {
			return
		}
		var m msg
		for {
			r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()
	hwnd := <-hwndc
	if hwnd == 0 {
		t.Fatal("CreateWindowEx failed")
	}
	if err := CloseWindows([]uintptr{hwnd}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the window did not close")
	}
	if !gotClose.Load() {
		t.Error("no WM_CLOSE")
	}
	// The window is gone: posting to it fails.
	if err := CloseWindows([]uintptr{hwnd}); err == nil {
		t.Error("posting to a destroyed window succeeded")
	}
}

// --- The real system (opt-in, read only) -------------------------------------

func TestSnapshotHasSelf(t *testing.T) {
	realTests(t)
	var s Sampler
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var created, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &created, &exit, &kernel, &user); err != nil {
		t.Fatal(err)
	}
	self := ID{uint32(os.Getpid()), filetime(created)}
	p, ok := snap.Find(self)
	if !ok {
		t.Fatal("this process is not in the snapshot")
	}
	if p.PrivateWorkingSet == 0 || p.Threads == 0 || p.WorkingSet == 0 || p.Name == "" {
		t.Errorf("self: threads %d, private %d, working set %d, name empty %v", p.Threads, p.PrivateWorkingSet, p.WorkingSet, p.Name == "")
	}
	if !slices.ContainsFunc(snap.Procs, func(p Proc) bool { return p.PID == 4 }) {
		t.Error("no PID 4")
	}
	if snap.CPUs < 1 || snap.MemTotal == 0 || snap.MemAvail == 0 || snap.SysBusy <= 0 || snap.SysIdle <= 0 || snap.Stamp <= self.Created {
		t.Errorf("system: cpus %d, mem %d/%d, busy %d idle %d", snap.CPUs, snap.MemAvail, snap.MemTotal, snap.SysBusy, snap.SysIdle)
	}
	if id, err := Identify(self.PID); err != nil || id != self {
		t.Errorf("Identify(self) = %v %v", id, err)
	}
	// A second snapshot reuses the buffer and gives rates.
	time.Sleep(50 * time.Millisecond)
	snap2, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if u := Rates(snap, snap2, CPUTotal)[self]; u.CPU < 0 || u.CPU > 100 {
		t.Errorf("own CPU = %v", u.CPU)
	}
	if c := SystemCPU(snap, snap2); c < 0 || c > 100 {
		t.Errorf("system CPU = %v", c)
	}
}

func TestWindowsNoError(t *testing.T) {
	realTests(t)
	ws, err := Windows()
	if err != nil {
		t.Fatal(err)
	}
	shell := 0
	for _, w := range ws {
		if w.HWND == 0 || (w.Title == "" && !w.Shell) {
			t.Errorf("bad window: hwnd %#x, empty title %v", w.HWND, w.Title == "")
		}
		if w.Shell {
			shell++
		}
	}
	t.Logf("%d windows, %d of the shell", len(ws), shell)
}

func TestReadInfoSelf(t *testing.T) {
	realTests(t)
	self, err := Identify(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	info := ReadInfo(self)
	exe, _ := os.Executable()
	sa, err1 := os.Stat(info.Path)
	sb, err2 := os.Stat(exe)
	if err1 != nil || err2 != nil || !os.SameFile(sa, sb) {
		t.Error("the path is not this executable")
	}
	if info.User == "" || info.Elevated == Unknown || info.Integrity == "" || info.Denied || info.Critical {
		t.Errorf("info: user empty %v, elevated %v, integrity %q, denied %v, critical %v",
			info.User == "", info.Elevated, info.Integrity, info.Denied, info.Critical)
	}
	if (info.Elevated == Yes) != IsElevated() {
		t.Error("elevation differs from IsElevated")
	}
	if line, err := CommandLine(self); err != nil || line == "" {
		t.Errorf("own command line: empty %v, %v", line == "", err)
	}
}

func median(ds []time.Duration) time.Duration {
	slices.Sort(ds)
	return ds[len(ds)/2]
}

// TestTiming measures a snapshot, a first full sample and a steady sample
// (docs/process-manager.md §2.6). It logs only durations and counts.
func TestTiming(t *testing.T) {
	realTests(t)
	var s Sampler
	var snap *Snapshot
	var ds []time.Duration
	for range 10 {
		t0 := time.Now()
		var err error
		if snap, err = s.Snapshot(); err != nil {
			t.Fatal(err)
		}
		ds = append(ds, time.Since(t0))
	}
	threads := 0
	for _, p := range snap.Procs {
		threads += int(p.Threads)
	}
	t.Logf("about %d processes, %d threads", (len(snap.Procs)+5)/10*10, (threads+50)/100*100)
	t.Logf("snapshot: median %v", median(ds))

	cacheMu.Lock()
	clear(accounts)
	clear(descriptions)
	cacheMu.Unlock()
	var s2 Sampler
	t0 := time.Now()
	first, err := s2.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Windows(); err != nil {
		t.Fatal(err)
	}
	denied := 0
	for _, p := range first.Procs {
		if ReadInfo(p.ID).Denied {
			denied++
		}
	}
	t.Logf("first full sample (snapshot + windows + info of every process): %v; %d processes denied", time.Since(t0), denied)

	ds = ds[:0]
	nwin := 0
	for range 10 {
		t0 := time.Now()
		if _, err := s2.Snapshot(); err != nil {
			t.Fatal(err)
		}
		ws, err := Windows()
		if err != nil {
			t.Fatal(err)
		}
		nwin = len(ws)
		ds = append(ds, time.Since(t0))
	}
	t.Logf("steady sample (snapshot + windows): median %v; %d windows", median(ds), nwin)
	if m := median(ds); m > 200*time.Millisecond {
		t.Errorf("steady sample too slow: %v", m)
	}
}

func BenchmarkSnapshot(b *testing.B) {
	var s Sampler
	for b.Loop() {
		if _, err := s.Snapshot(); err != nil {
			b.Fatal(err)
		}
	}
}
