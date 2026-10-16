package processes

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	winnet "github.com/eziq1/golauncher/internal/winapi/network"
	"github.com/eziq1/golauncher/internal/winapi/process"
	"github.com/eziq1/golauncher/internal/winapi/shell"
)

// Backend is everything the plugin reads from and does to the system.
// System is the real one, DryRun reads the real system but ends nothing,
// and Fixture answers from a fixture file (fake environments and tests).
// Every call that ends a process, closes a window or asks for elevation is
// behind this interface, so tests never touch real processes.
type Backend interface {
	Snapshot() (*process.Snapshot, error)
	Windows() ([]process.Window, error)
	Info(id process.ID) process.Info
	CommandLine(id process.ID) (string, error)
	// Ports returns the TCP listeners and bound UDP sockets.
	Ports() ([]winnet.Socket, error)
	// Identify returns the instance running with pid.
	Identify(pid uint32) (process.ID, error)
	Kill(id process.ID) error
	KillTree(root process.ID) process.KillReport
	CloseWindows(hwnds []uintptr) error
	// Wait returns the ids that exited, once all did or ctx is done.
	Wait(ctx context.Context, ids []process.ID) []process.ID
	SwitchTo(hwnd uintptr) error
	// Elevate ends r with administrator rights through a UAC prompt
	// (golauncher's hidden kill helper); process.ErrCancelled when the user
	// declined.
	Elevate(ctx context.Context, r process.HelperRequest) error
	StartExplorer() error
	// Elevated reports whether golauncher itself runs elevated.
	Elevated() bool
	// Self is golauncher's PID.
	Self() uint32
	SystemRoot() string
	// Icon draws the icon of an executable as a size×size PNG.
	Icon(path string, size int) ([]byte, error)
}

// System is the backend of the real system.
type System struct {
	mu      sync.Mutex
	sampler process.Sampler
}

var _ Backend = (*System)(nil)

func (s *System) Snapshot() (*process.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sampler.Snapshot()
}

func (s *System) Windows() ([]process.Window, error)          { return process.Windows() }
func (s *System) Info(id process.ID) process.Info             { return process.ReadInfo(id) }
func (s *System) CommandLine(id process.ID) (string, error)   { return process.CommandLine(id) }
func (s *System) Ports() ([]winnet.Socket, error)             { return winnet.Sockets() }
func (s *System) Identify(pid uint32) (process.ID, error)     { return process.Identify(pid) }
func (s *System) Kill(id process.ID) error                    { return process.Kill(id) }
func (s *System) KillTree(root process.ID) process.KillReport { return process.KillTree(root) }
func (s *System) CloseWindows(hwnds []uintptr) error          { return process.CloseWindows(hwnds) }
func (s *System) SwitchTo(hwnd uintptr) error                 { return process.SwitchTo(hwnd) }
func (s *System) StartExplorer() error                        { return process.StartExplorer() }
func (s *System) Elevated() bool                              { return process.IsElevated() }
func (s *System) Self() uint32                                { return uint32(os.Getpid()) }
func (s *System) SystemRoot() string                          { return os.Getenv("SystemRoot") }

func (s *System) Wait(ctx context.Context, ids []process.ID) []process.ID {
	return process.Wait(ctx, ids)
}

func (s *System) Elevate(ctx context.Context, r process.HelperRequest) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	code, err := process.RunElevated(ctx, exe, r.Args())
	if err != nil {
		return err
	}
	return process.ExitError(code)
}

func (s *System) Icon(path string, size int) ([]byte, error) {
	return shell.IconPNG(shell.App{Path: path}, size)
}

// DryRun reads the real system but only records what it would end, close
// or elevate (glctl --real without --exec).
type DryRun struct {
	System
	rec Recorder
}

var _ Backend = (*DryRun)(nil)

// Calls returns the recorded calls.
func (d *DryRun) Calls() []string { return d.rec.Calls() }

func (d *DryRun) Kill(id process.ID) error {
	d.rec.add("kill", id)
	return nil
}

func (d *DryRun) KillTree(root process.ID) process.KillReport {
	d.rec.add("kill-tree", root)
	return process.KillReport{Ended: []process.ID{root}}
}

func (d *DryRun) CloseWindows(hwnds []uintptr) error {
	d.rec.add("close-windows", len(hwnds))
	return nil
}

func (d *DryRun) Wait(context.Context, []process.ID) []process.ID { return nil }

func (d *DryRun) SwitchTo(hwnd uintptr) error {
	d.rec.add("switch", hwnd)
	return nil
}

func (d *DryRun) Elevate(_ context.Context, r process.HelperRequest) error {
	d.rec.add("elevate", r.Args())
	return nil
}

func (d *DryRun) StartExplorer() error {
	d.rec.add("start-explorer")
	return nil
}

// Recorder records the calls of a backend that does nothing.
type Recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *Recorder) add(what string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := what
	for _, a := range args {
		s += fmt.Sprintf(" %v", a)
	}
	r.calls = append(r.calls, s)
}

// Calls returns the recorded calls.
func (r *Recorder) Calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

// --- Fixture ---------------------------------------------------------------

// Fixture is the content of a fixture file (testdata/fixtures/processes.json):
// invented processes with the counters of two samples one second apart, so
// that CPU percentages are deterministic.
type Fixture struct {
	CPUs       int    `json:"cpus"`
	MemTotal   uint64 `json:"memTotal"`
	MemAvail   uint64 `json:"memAvail"`
	Self       uint32 `json:"self"`
	SystemRoot string `json:"systemRoot"`
	Elevated   bool   `json:"elevated"`
	// Time is when the first sample was taken; the second is a second later.
	Time time.Time `json:"time"`
	// SysIdle and SysBusy are the system's counters at the two samples.
	SysIdle   [2]int64     `json:"sysIdle"`
	SysBusy   [2]int64     `json:"sysBusy"`
	Processes []FixProcess `json:"processes"`
	Sockets   []struct {
		Proto string `json:"proto"`
		Local string `json:"local"`
		PID   uint32 `json:"pid"`
	} `json:"sockets"`
}

// FixProcess is an invented process.
type FixProcess struct {
	PID     uint32      `json:"pid"`
	Created int64       `json:"created"`
	Parent  uint32      `json:"parent"`
	Name    string      `json:"name"`
	Session uint32      `json:"session"`
	Threads uint32      `json:"threads"`
	Handles uint32      `json:"handles"`
	Memory  uint64      `json:"memory"` // private working set
	WorkSet uint64      `json:"workingSet"`
	Commit  uint64      `json:"commit"`
	Peak    uint64      `json:"peak"`
	CPU     [2]int64    `json:"cpu"` // 100 ns, at the two samples
	IO      [2]uint64   `json:"io"`
	Info    FixInfo     `json:"info"`
	Command string      `json:"commandLine"`
	Windows []FixWindow `json:"windows"`
	// Denied: ending it needs administrator rights (an elevated process).
	Denied bool `json:"denied"`
	// Closes: it exits when asked to close its windows.
	Closes bool `json:"closes"`
}

// FixInfo is process.Info of an invented process.
type FixInfo struct {
	Path          string `json:"path"`
	Description   string `json:"description"`
	User          string `json:"user"`
	SystemAccount bool   `json:"systemAccount"`
	Elevated      bool   `json:"elevated"`
	Integrity     string `json:"integrity"`
	Package       string `json:"package"`
	Critical      bool   `json:"critical"`
	Denied        bool   `json:"denied"`
}

func (f FixInfo) info() process.Info {
	i := process.Info{Path: f.Path, Description: f.Description, User: f.User, SystemAccount: f.SystemAccount,
		Integrity: f.Integrity, Package: f.Package, Critical: f.Critical, Denied: f.Denied, Elevated: process.No}
	if f.Elevated {
		i.Elevated = process.Yes
	}
	if f.Denied {
		i.Elevated = process.Unknown
	}
	return i
}

// FixWindow is a window of an invented process.
type FixWindow struct {
	Title string `json:"title"`
	Hung  bool   `json:"hung"`
	Shell bool   `json:"shell"`
}

// FixtureBackend answers from a Fixture and records what it is asked to do.
type FixtureBackend struct {
	F   Fixture
	Err error // returned by Snapshot when set (a broken fixture file)
	Recorder

	mu      sync.Mutex
	samples int
	closed  map[uintptr]bool
}

var _ Backend = (*FixtureBackend)(nil)

// LoadFixture reads a fixture file; path "" gives an empty fixture.
func LoadFixture(path string) *FixtureBackend {
	b := &FixtureBackend{}
	if path == "" {
		b.F.CPUs = 1
		return b
	}
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &b.F)
	}
	if err != nil {
		b.Err = fmt.Errorf("processes fixture %s: %w", path, err)
	}
	return b
}

// Snapshot returns the next sample.
func (b *FixtureBackend) Snapshot() (*process.Snapshot, error) {
	if b.Err != nil {
		return nil, b.Err
	}
	b.mu.Lock()
	i := b.samples
	b.samples++
	b.mu.Unlock()
	// Sample i is i seconds after the first, the counters going on at the
	// pace of the two given samples.
	at := func(v [2]int64) int64 { return v[0] + int64(i)*(v[1]-v[0]) }
	t := b.F.Time.Add(time.Duration(i) * time.Second)
	s := &process.Snapshot{Time: t, Stamp: filetime(t), CPUs: max(b.F.CPUs, 1),
		SysIdle: at(b.F.SysIdle), SysBusy: at(b.F.SysBusy), MemTotal: b.F.MemTotal, MemAvail: b.F.MemAvail}
	for _, p := range b.F.Processes {
		s.Procs = append(s.Procs, process.Proc{ID: process.ID{PID: p.PID, Created: p.Created}, Parent: p.Parent, Name: p.Name,
			Session: p.Session, Threads: p.Threads, Handles: p.Handles, CPU: at(p.CPU), PrivateWorkingSet: p.Memory,
			WorkingSet: p.WorkSet, Commit: p.Commit, PeakWorkingSet: p.Peak, IO: uint64(at([2]int64{int64(p.IO[0]), int64(p.IO[1])}))})
	}
	return s, nil
}

// filetime converts t to a FILETIME.
func filetime(t time.Time) int64 { return t.UnixNano()/100 + 116444736000000000 }

// hwnd is the invented handle of the i-th window of a process.
func hwnd(pid uint32, i int) uintptr { return uintptr(pid)<<8 | uintptr(i+1) }

func (b *FixtureBackend) Windows() ([]process.Window, error) {
	var out []process.Window
	for _, p := range b.F.Processes {
		for i, w := range p.Windows {
			out = append(out, process.Window{HWND: hwnd(p.PID, i), PID: p.PID, Title: w.Title, Hung: w.Hung, Shell: w.Shell})
		}
	}
	return out, nil
}

func (b *FixtureBackend) find(id process.ID) (FixProcess, bool) {
	for _, p := range b.F.Processes {
		if p.PID == id.PID && p.Created == id.Created {
			return p, true
		}
	}
	return FixProcess{}, false
}

func (b *FixtureBackend) Info(id process.ID) process.Info {
	p, _ := b.find(id)
	return p.Info.info()
}

func (b *FixtureBackend) CommandLine(id process.ID) (string, error) {
	p, ok := b.find(id)
	if !ok {
		return "", process.ErrGone
	}
	if p.Command == "" {
		return "", process.ErrNeedsAdmin
	}
	return p.Command, nil
}

func (b *FixtureBackend) Ports() ([]winnet.Socket, error) {
	var out []winnet.Socket
	for _, s := range b.F.Sockets {
		var sock winnet.Socket
		if err := json.Unmarshal([]byte(`"`+s.Local+`"`), &sock.Local); err != nil {
			return nil, err
		}
		sock.Proto, sock.PID = s.Proto, s.PID
		out = append(out, sock)
	}
	return out, nil
}

func (b *FixtureBackend) Identify(pid uint32) (process.ID, error) {
	for _, p := range b.F.Processes {
		if p.PID == pid {
			return process.ID{PID: pid, Created: p.Created}, nil
		}
	}
	return process.ID{}, process.ErrGone
}

// Kill checks like process.Kill (refusing the kernel, critical processes
// and golauncher itself) and records the call.
func (b *FixtureBackend) Kill(id process.ID) error {
	p, ok := b.find(id)
	if !ok {
		return process.ErrGone
	}
	class, _ := process.Classify(process.Facts{ID: id, Name: p.Name, Path: p.Info.Path, Critical: p.Info.Critical,
		Self: b.F.Self, SystemRoot: b.F.SystemRoot, Shell: slices.ContainsFunc(p.Windows, func(w FixWindow) bool { return w.Shell })})
	if !class.Endable() && class != process.ClassShell {
		return process.ErrProtected
	}
	if p.Denied && !b.F.Elevated {
		return process.ErrNeedsAdmin
	}
	b.add("kill", id.PID)
	return nil
}

func (b *FixtureBackend) KillTree(root process.ID) process.KillReport {
	var r process.KillReport
	snap, err := b.Snapshot()
	if err != nil {
		r.Add(root, err)
		return r
	}
	ids := process.Tree(snap.Procs, root)
	if len(ids) == 0 {
		r.Add(root, process.ErrGone)
	}
	for i, id := range ids {
		err := b.Kill(id)
		r.Add(id, err)
		if i == 0 && errors.Is(err, process.ErrProtected) {
			break // never the descendants of a protected process
		}
	}
	return r
}

func (b *FixtureBackend) CloseWindows(hwnds []uintptr) error {
	b.mu.Lock()
	if b.closed == nil {
		b.closed = map[uintptr]bool{}
	}
	for _, h := range hwnds {
		b.closed[h] = true
	}
	b.mu.Unlock()
	b.add("close-windows", len(hwnds))
	return nil
}

// Wait reports as exited the processes that close when asked and were
// asked, at once.
func (b *FixtureBackend) Wait(_ context.Context, ids []process.ID) []process.ID {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []process.ID
	for _, id := range ids {
		p, ok := b.find(id)
		if !ok {
			out = append(out, id)
			continue
		}
		asked := false
		for i := range p.Windows {
			asked = asked || b.closed[hwnd(p.PID, i)]
		}
		if p.Closes && asked {
			out = append(out, id)
		}
	}
	return out
}

func (b *FixtureBackend) SwitchTo(h uintptr) error {
	b.add("switch", h)
	return nil
}

func (b *FixtureBackend) Elevate(_ context.Context, r process.HelperRequest) error {
	b.add("elevate", r.Args())
	return nil
}

func (b *FixtureBackend) StartExplorer() error {
	b.add("start-explorer")
	return nil
}

func (b *FixtureBackend) Elevated() bool     { return b.F.Elevated }
func (b *FixtureBackend) Self() uint32       { return b.F.Self }
func (b *FixtureBackend) SystemRoot() string { return b.F.SystemRoot }

func (b *FixtureBackend) Icon(string, int) ([]byte, error) { return nil, errors.ErrUnsupported }
