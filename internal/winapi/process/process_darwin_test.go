//go:build darwin

package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// These tests end only processes they start themselves (sleep); the rest
// only read. Those reading other processes or the running apps need
// GOLAUNCHER_PROCESS_TESTS=1.

// startSleep starts `sleep 30` (or the shell command sh) and returns its
// ID; it is ended when the test ends.
func startSleep(t *testing.T, sh string) (*exec.Cmd, ID) {
	t.Helper()
	cmd := exec.Command("/bin/sleep", "30")
	if sh != "" {
		cmd = exec.Command("/bin/sh", "-c", sh)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-done
	})
	id, err := Identify(uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	return cmd, id
}

func TestSnapshotHasSelf(t *testing.T) {
	var s Sampler
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	self, err := Identify(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := snap.Find(self)
	if !ok {
		t.Fatal("this process is not in the snapshot")
	}
	if p.Parent != uint32(os.Getppid()) || p.PrivateWorkingSet == 0 || p.WorkingSet == 0 || p.Threads == 0 || p.Name == "" {
		t.Errorf("self = %+v", p)
	}
	if snap.CPUs < 1 || snap.MemTotal == 0 || snap.MemAvail == 0 || snap.MemAvail > snap.MemTotal || snap.SysIdle+snap.SysBusy == 0 {
		t.Errorf("system: cpus %d mem %d/%d ticks %d/%d", snap.CPUs, snap.MemAvail, snap.MemTotal, snap.SysIdle, snap.SysBusy)
	}
	if _, ok := snap.Find(ID{PID: 1}); ok {
		t.Error("launchd found with a zero start time")
	}
	launchd := false
	for _, q := range snap.Procs {
		launchd = launchd || q.PID == 1 && q.Name == "launchd"
	}
	if !launchd {
		t.Error("no launchd")
	}
	// A second snapshot gives CPU use; this process is busy enough.
	busy := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(busy) {
	}
	snap2, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if u := Rates(snap, snap2, CPUPerCore)[self]; u.CPU <= 0 {
		t.Errorf("CPU of a busy loop: %+v", u)
	}
}

func TestReadChild(t *testing.T) {
	_, id := startSleep(t, "")
	info := ReadInfo(id)
	if info.Path != "/bin/sleep" || info.User == "" || info.Critical || info.Package != "" {
		t.Errorf("info = %+v", info)
	}
	if info.User != userName(uint32(os.Getuid())) || strings.Trim(info.User, "0123456789") == "" {
		t.Errorf("user = %q, want a name", info.User)
	}
	cmd, err := CommandLine(id)
	if err != nil || cmd != "/bin/sleep 30" {
		t.Errorf("command line = %q, %v", cmd, err)
	}
	stale := ID{PID: id.PID, Created: id.Created - 1}
	if (ReadInfo(stale) != Info{}) {
		t.Error("ReadInfo of a stale ID")
	}
	if _, err := CommandLine(stale); !errors.Is(err, ErrGone) {
		t.Errorf("CommandLine of a stale ID: %v", err)
	}
}

func TestKillChild(t *testing.T) {
	_, id := startSleep(t, "")
	// The wrong start time: a reused PID is never ended.
	if err := Kill(ID{PID: id.PID, Created: id.Created + 10}); !errors.Is(err, ErrGone) {
		t.Fatalf("stale ID: %v", err)
	}
	if _, err := Identify(id.PID); err != nil {
		t.Fatal("the child died of a stale kill")
	}
	if err := Kill(id); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if got := Wait(ctx, []ID{id}); len(got) != 1 {
		t.Errorf("Wait = %v", got)
	}
	if err := Kill(id); !errors.Is(err, ErrGone) {
		t.Errorf("second kill: %v", err)
	}
}

func TestKillRefused(t *testing.T) {
	self, _ := Identify(uint32(os.Getpid()))
	launchd, err := Identify(1)
	if err != nil {
		t.Fatal(err)
	}
	// Refused before any signal is sent. (No test signals a process it
	// did not start, so ErrNeedsAdmin for root's daemons is not tested.)
	for _, id := range []ID{self, launchd, {PID: 0}} {
		if err := Kill(id); !errors.Is(err, ErrProtected) {
			t.Errorf("%v: %v", id, err)
		}
	}
}

func TestKillTreeChild(t *testing.T) {
	_, root := startSleep(t, "/bin/sleep 30 & /bin/sleep 31; wait")
	var kids []ID
	deadline := time.Now().Add(5 * time.Second)
	for len(kids) < 2 && time.Now().Before(deadline) {
		var s Sampler
		snap, err := s.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		kids = Tree(snap.Procs, root)[1:]
		time.Sleep(20 * time.Millisecond)
	}
	if len(kids) != 2 {
		t.Fatalf("children: %v", kids)
	}
	r := KillTree(root)
	if len(r.Ended) != 3 {
		t.Errorf("report = %+v", r)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if got := Wait(ctx, append([]ID{root}, kids...)); len(got) != 3 {
		t.Errorf("still running: %v", got)
	}
}

func TestWaitTimesOut(t *testing.T) {
	_, id := startSleep(t, "")
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	if got := Wait(ctx, []ID{id}); len(got) != 0 {
		t.Errorf("Wait = %v", got)
	}
}

func TestRunElevatedRefusedInTests(t *testing.T) {
	if _, err := RunElevated(t.Context(), "/usr/bin/true", nil); err == nil {
		t.Fatal("RunElevated ran in a test")
	}
}

func TestRealSystem(t *testing.T) {
	if os.Getenv("GOLAUNCHER_PROCESS_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_PROCESS_TESTS=1 to read the running apps and system processes")
	}
	var s Sampler
	start := time.Now()
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	first := time.Since(start)
	start = time.Now()
	if _, err := s.Snapshot(); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d processes; first snapshot %v (with ps), next %v", len(snap.Procs), first, time.Since(start))
	wins, err := Windows()
	if err != nil {
		t.Fatal(err)
	}
	finder := false
	for _, w := range wins {
		if id, err := Identify(w.PID); err == nil {
			if info := ReadInfo(id); info.Package == "com.apple.finder" {
				finder = true
				if info.Description == "" {
					t.Error("Finder has no name")
				}
			}
		}
	}
	if !finder {
		t.Error("Finder is not among the regular apps")
	}
	launchd, _ := Identify(1)
	if info := ReadInfo(launchd); !info.Critical || !info.SystemAccount || info.User != "root" {
		t.Errorf("launchd = %+v", info)
	}
	if _, err := CommandLine(launchd); !errors.Is(err, ErrNeedsAdmin) && os.Geteuid() != 0 {
		t.Errorf("launchd's command line: %v", err)
	}
	t.Logf("%d regular apps", len(wins))
}
