//go:build darwin

package process

// The macOS backend. One sysctl (kern.proc.all) lists every process with
// its parent, owner and start time, which with the PID identifies it as
// on Windows (ID.Created holds the start time as a FILETIME). libproc
// (proc_pid_rusage, proc_pidinfo) gives the processor time, memory
// (phys_footprint: Activity Monitor's "Memory"), disk I/O and threads of
// the user's own processes; macOS refuses those calls for other users'
// processes (root's daemons), whose processor time and resident size come
// from ps (setuid root) every few seconds instead. The "windows" are the
// regular apps of NSWorkspace (the apps in the Dock): ending such an app
// asks it to quit, as Quit in the Dock does; Force End sends SIGKILL.
// Everything is called through purego, without cgo.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"golang.org/x/sys/unix"

	"github.com/eziq1/golauncher/internal/darwin/ns"
)

// psEvery is how often ps is run for the processes libproc cannot read.
const psEvery = 3 * time.Second

type libSystem struct {
	pidinfo     func(pid int32, flavor int32, arg uint64, buf unsafe.Pointer, size int32) int32
	pidpath     func(pid int32, buf unsafe.Pointer, size uint32) int32
	rusage      func(pid int32, flavor int32, buf unsafe.Pointer) int32
	hostSelf    func() uint32
	hostStats   func(host uint32, flavor int32, info unsafe.Pointer, count *uint32) int32
	hostStats64 func(host uint32, flavor int32, info unsafe.Pointer, count *uint32) int32
	timebase    func(info unsafe.Pointer) int32
	getpwuidR   func(uid uint32, pwd unsafe.Pointer, buf unsafe.Pointer, size uintptr, result *uintptr) int32

	host         uint32
	numer, denom uint64
}

var loadLib = sync.OnceValues(func() (*libSystem, error) {
	h, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("process: loading libSystem: %w", err)
	}
	l := &libSystem{}
	purego.RegisterLibFunc(&l.pidinfo, h, "proc_pidinfo")
	purego.RegisterLibFunc(&l.pidpath, h, "proc_pidpath")
	purego.RegisterLibFunc(&l.rusage, h, "proc_pid_rusage")
	purego.RegisterLibFunc(&l.hostSelf, h, "mach_host_self")
	purego.RegisterLibFunc(&l.hostStats, h, "host_statistics")
	purego.RegisterLibFunc(&l.hostStats64, h, "host_statistics64")
	purego.RegisterLibFunc(&l.timebase, h, "mach_timebase_info")
	purego.RegisterLibFunc(&l.getpwuidR, h, "getpwuid_r")
	l.host = l.hostSelf()
	var tb [2]uint32
	if l.timebase(unsafe.Pointer(&tb[0])) == 0 && tb[0] != 0 && tb[1] != 0 {
		l.numer, l.denom = uint64(tb[0]), uint64(tb[1])
	} else {
		l.numer, l.denom = 1, 1
	}
	return l, nil
})

// nanos converts mach absolute time units to nanoseconds without
// overflowing.
func (l *libSystem) nanos(t uint64) uint64 {
	return t/l.denom*l.numer + t%l.denom*l.numer/l.denom
}

// path is the executable of pid ("" when it cannot be read).
func (l *libSystem) path(pid uint32) string {
	var buf [4096]byte // PROC_PIDPATHINFO_MAXSIZE
	n := l.pidpath(int32(pid), unsafe.Pointer(&buf[0]), uint32(len(buf)))
	if n <= 0 {
		return ""
	}
	return string(buf[:n])
}

// Flavors and layouts of <libproc.h>, <sys/resource.h> and <mach/host_info.h>.
const (
	rusageInfoV4      = 4  // RUSAGE_INFO_V4: struct rusage_info_v4, 296 bytes
	procPIDTaskInfo   = 4  // PROC_PIDTASKINFO: struct proc_taskinfo, 96 bytes
	hostCPULoadInfo   = 3  // HOST_CPU_LOAD_INFO: 4 natural_t ticks
	hostVMInfo64      = 4  // HOST_VM_INFO64: struct vm_statistics64
	hostVMInfo64Count = 38 // HOST_VM_INFO64_COUNT, in natural_t
	taskInfoSize      = 96
	rusageV4Words     = 37 // 296 bytes
	killWait          = 2 * time.Second
)

// usage fills p's counters from libproc; false when macOS refuses (another
// user's process) or the process exited.
func (l *libSystem) usage(p *Proc) bool {
	var ri [rusageV4Words]uint64
	if l.rusage(int32(p.PID), rusageInfoV4, unsafe.Pointer(&ri[0])) != 0 {
		return false
	}
	// ri_uuid takes two words; then user and system time (mach units).
	p.CPU = int64(l.nanos(ri[2]+ri[3]) / 100)
	p.WorkingSet = ri[8]        // ri_resident_size
	p.PrivateWorkingSet = ri[9] // ri_phys_footprint
	p.IO = ri[18] + ri[19]      // ri_diskio_bytesread + written
	p.PeakWorkingSet = ri[30]   // ri_lifetime_max_phys_footprint
	var ti [taskInfoSize / 4]int32
	if l.pidinfo(int32(p.PID), procPIDTaskInfo, 0, unsafe.Pointer(&ti[0]), taskInfoSize) == taskInfoSize {
		p.Threads = uint32(max(ti[84/4], 0)) // pti_threadnum
		p.Priority = ti[92/4]                // pti_priority
	}
	return true
}

// cpuLoad is the system's busy and idle processor ticks.
func (l *libSystem) cpuLoad() (idle, busy int64) {
	var t [4]uint32 // user, system, idle, nice
	n := uint32(len(t))
	if l.hostStats(l.host, hostCPULoadInfo, unsafe.Pointer(&t[0]), &n) != 0 {
		return 0, 0
	}
	return int64(t[2]), int64(t[0]) + int64(t[1]) + int64(t[3])
}

// memory is the physical memory and what is not used by apps, the kernel
// (wired) and the compressor: Activity Monitor's "Memory Used" is the
// rest.
func (l *libSystem) memory() (total, avail uint64) {
	total, _ = unix.SysctlUint64("hw.memsize")
	page, err := unix.SysctlUint32("hw.pagesize")
	if err != nil || total == 0 {
		return total, 0
	}
	var vm [hostVMInfo64Count]uint32
	n := uint32(len(vm))
	if l.hostStats64(l.host, hostVMInfo64, unsafe.Pointer(&vm[0]), &n) != 0 {
		return total, 0
	}
	wired, purgeable, compressor, internal := uint64(vm[3]), uint64(vm[22]), uint64(vm[32]), uint64(vm[35])
	used := (internal - min(purgeable, internal) + wired + compressor) * uint64(page)
	if used > total {
		return total, 0
	}
	return total, total - used
}

var (
	usersMu sync.Mutex
	users   = map[uint32]string{}
)

// userName is the name of uid (its number when it has none). os/user
// cannot be used without cgo: it reads only /etc/passwd, which lacks the
// users of Directory Services.
func userName(uid uint32) string {
	usersMu.Lock()
	defer usersMu.Unlock()
	if n, ok := users[uid]; ok {
		return n
	}
	name := fmt.Sprint(uid)
	if l, err := loadLib(); err == nil {
		var pwd [128]byte // struct passwd is 72 bytes
		buf := make([]byte, 4096)
		var res uintptr
		if l.getpwuidR(uid, unsafe.Pointer(&pwd[0]), unsafe.Pointer(&buf[0]), uintptr(len(buf)), &res) == 0 && res != 0 {
			if p := *(*uintptr)(unsafe.Pointer(&pwd[0])); p != 0 { // pw_name, inside buf
				off := p - uintptr(unsafe.Pointer(&buf[0]))
				if off < uintptr(len(buf)) {
					if i := bytes.IndexByte(buf[off:], 0); i > 0 {
						name = string(buf[off : int(off)+i])
					}
				}
			}
		}
		runtime.KeepAlive(buf)
	}
	users[uid] = name
	return name
}

// kinfo returns the kernel's record of pid.
func kinfo(pid uint32) (*unix.KinfoProc, bool) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", int(pid))
	if err != nil || k == nil || uint32(k.Proc.P_pid) != pid || k.Proc.P_stat == sZomb {
		return nil, false
	}
	return k, true
}

const sZomb = 5 // SZOMB: exited, not yet reaped

func idOf(k *unix.KinfoProc) ID {
	st := k.Proc.P_starttime
	return ID{PID: uint32(k.Proc.P_pid), Created: unixFiletime(int64(st.Sec)*1e6 + int64(st.Usec))}
}

func commName(k *unix.KinfoProc) string {
	c := k.Proc.P_comm[:]
	if i := bytes.IndexByte(c, 0); i >= 0 {
		c = c[:i]
	}
	return string(c)
}

// known is what a snapshot learned about a process instance, for ReadInfo
// (which then makes no system call for it).
type known struct {
	path string
	uid  uint32
	comm string
}

var (
	knownMu sync.Mutex
	knownBy = map[ID]known{}
)

// Sampler takes snapshots. It keeps per-process state between them (which
// processes libproc refuses, and their ps readings), so the plugin uses
// one Sampler. ps runs in the background, at most every psEvery: a
// snapshot never waits for it, and the processes it has not read yet have
// NoCPU set.
type Sampler struct {
	denied map[ID]bool
	rss    map[ID]uint64
	smooth cpuSmoother
	psAt   time.Time // when the last ps run started

	mu        sync.Mutex // guards the fields below, written by the ps run
	psRunning bool
	psRows    map[uint32]psRow
	psTime    time.Time // when psRows were read
	psFresh   bool      // psRows were not applied yet
}

// Snapshot returns the state of every process.
func (s *Sampler) Snapshot() (*Snapshot, error) { return s.snapshot(true) }

// snapshot returns the state of every process, without the counters when
// usage is false (only the tree is needed).
func (s *Sampler) snapshot(usage bool) (*Snapshot, error) {
	l, err := loadLib()
	if err != nil {
		return nil, err
	}
	ks, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("process: kern.proc.all: %w", err)
	}
	if s.denied == nil {
		s.denied, s.rss = map[ID]bool{}, map[ID]uint64{}
	}
	now := time.Now()
	snap := &Snapshot{Time: now, Stamp: unixFiletime(now.UnixMicro()), CPUs: runtime.NumCPU()}
	if usage {
		snap.SysIdle, snap.SysBusy = l.cpuLoad()
		snap.MemTotal, snap.MemAvail = l.memory()
	}
	alive := make(map[ID]bool, len(ks))
	var others []int // indexes in snap.Procs of processes libproc refuses
	knownMu.Lock()
	for i := range ks {
		k := &ks[i]
		if k.Proc.P_stat == sZomb {
			continue
		}
		id := idOf(k)
		alive[id] = true
		p := Proc{ID: id, Parent: uint32(k.Eproc.Ppid), Session: 1}
		kn, ok := knownBy[id]
		if !ok {
			kn = known{path: l.path(id.PID), uid: k.Eproc.Ucred.Uid, comm: commName(k)}
			knownBy[id] = kn
		}
		p.Name = kn.comm
		if kn.path != "" {
			p.Name = filepath.Base(kn.path)
		}
		if usage && (s.denied[id] || !l.usage(&p)) {
			s.denied[id] = true
			others = append(others, len(snap.Procs))
		}
		snap.Procs = append(snap.Procs, p)
	}
	for id := range knownBy {
		if !alive[id] {
			delete(knownBy, id)
		}
	}
	knownMu.Unlock()
	if !usage {
		return snap, nil
	}
	if len(others) > 0 {
		s.applyPS(snap, others, now)
		for _, i := range others {
			p := &snap.Procs[i]
			var ok bool
			p.CPU, ok = s.smooth.value(p.ID, now)
			p.NoCPU = !ok
			p.PrivateWorkingSet, p.WorkingSet = s.rss[p.ID], s.rss[p.ID]
		}
	}
	for id := range s.denied {
		if !alive[id] {
			delete(s.denied, id)
			delete(s.rss, id)
		}
	}
	s.smooth.keep(alive)
	return snap, nil
}

// applyPS takes in the latest ps readings for the processes at others,
// and starts a new ps run when one is due.
func (s *Sampler) applyPS(snap *Snapshot, others []int, now time.Time) {
	s.mu.Lock()
	rows, at, fresh := s.psRows, s.psTime, s.psFresh
	s.psFresh = false
	due := !s.psRunning && (s.psAt.IsZero() || now.Sub(s.psAt) >= psEvery)
	if due {
		s.psRunning, s.psAt = true, now
	}
	s.mu.Unlock()
	if fresh {
		for _, i := range others {
			p := &snap.Procs[i]
			if r, ok := rows[p.PID]; ok {
				s.smooth.update(p.ID, r.CPU, at)
				s.rss[p.ID] = r.RSS
			}
		}
	}
	if due {
		go s.runPS()
	}
}

// runPS reads the processor time and resident size of every process from
// ps (setuid root, so it sees other users' processes).
func (s *Sampler) runPS() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,rss=,time=").Output()
	at := time.Now()
	rows := map[uint32]psRow{}
	if err == nil {
		for _, r := range parsePS(out) {
			rows[r.PID] = r
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.psRunning = false
	if err == nil {
		s.psRows, s.psTime, s.psFresh = rows, at, true
	}
}

// --- Running apps -----------------------------------------------------------

// app is a running app of NSWorkspace.
type app struct {
	name    string
	regular bool // activationPolicy regular: in the Dock, with windows
}

var (
	appsMu sync.Mutex
	apps   map[uint32]app
	appsAt time.Time
)

// runningApps returns the running apps by PID, read at most once a second.
func runningApps() map[uint32]app {
	appsMu.Lock()
	defer appsMu.Unlock()
	if apps != nil && time.Since(appsAt) < time.Second {
		return apps
	}
	out := map[uint32]app{}
	ns.Pool(func() {
		ws := ns.Send(ns.Class("NSWorkspace"), "sharedWorkspace")
		list := ns.Send(ws, "runningApplications")
		for i := range ns.SendInt(list, "count") {
			a := ns.Send(list, "objectAtIndex:", i)
			if ns.SendBool(a, "isTerminated") {
				continue
			}
			pid := objc.Send[int32](a, ns.Sel("processIdentifier"))
			if pid <= 0 {
				continue
			}
			out[uint32(pid)] = app{name: ns.GoString(ns.Send(a, "localizedName")), regular: ns.SendInt(a, "activationPolicy") == 0}
		}
	})
	apps, appsAt = out, time.Now()
	return out
}

// runningApp calls fn with the NSRunningApplication of pid, in an
// autorelease pool; false when pid is not an app.
func runningApp(pid uint32, fn func(a ns.ID)) bool {
	found := false
	ns.Pool(func() {
		a := ns.Send(ns.Class("NSRunningApplication"), "runningApplicationWithProcessIdentifier:", int32(pid))
		if a != 0 {
			found = true
			fn(a)
		}
	})
	return found
}

var (
	bundlesMu sync.Mutex
	bundles   = map[string][2]string{} // bundle path -> name, identifier
)

// bundleInfo returns the name and identifier of the bundle at dir.
func bundleInfo(dir string) (name, ident string) {
	bundlesMu.Lock()
	defer bundlesMu.Unlock()
	if v, ok := bundles[dir]; ok {
		return v[0], v[1]
	}
	ns.Pool(func() {
		d := ns.Send(ns.Class("NSDictionary"), "dictionaryWithContentsOfFile:", ns.String(filepath.Join(dir, "Contents", "Info.plist")))
		get := func(k string) string {
			v := ns.Send(d, "objectForKey:", ns.String(k))
			if v == 0 || !ns.SendBool(v, "isKindOfClass:", ns.Class("NSString")) {
				return ""
			}
			return ns.GoString(v)
		}
		name = get("CFBundleDisplayName")
		if name == "" {
			name = get("CFBundleName")
		}
		ident = get("CFBundleIdentifier")
	})
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(dir), ".app")
	}
	bundles[dir] = [2]string{name, ident}
	return name, ident
}

// --- Reading ------------------------------------------------------------------

// ReadInfo reads what a snapshot lacks about the process id: its path,
// owner, app name and bundle. Package is the identifier of the outermost
// app bundle it runs from, so an app's helpers group with it.
func ReadInfo(id ID) Info {
	knownMu.Lock()
	kn, ok := knownBy[id]
	knownMu.Unlock()
	if !ok {
		k, ok := kinfo(id.PID)
		if !ok || idOf(k) != id {
			return Info{}
		}
		kn = known{uid: k.Eproc.Ucred.Uid, comm: commName(k)}
		if l, err := loadLib(); err == nil {
			kn.path = l.path(id.PID)
		}
	}
	info := Info{Path: kn.path}
	uid := kn.uid
	info.User = userName(uid)
	info.SystemAccount = uid < 500 && int(uid) != os.Getuid()
	name := kn.comm
	if info.Path != "" {
		name = filepath.Base(info.Path)
	}
	info.Critical = macCritical(id.PID, name, info.Path)
	if a, ok := runningApps()[id.PID]; ok && a.name != "" {
		info.Description = a.name
	} else if b := innermostBundle(info.Path); b != "" {
		info.Description, _ = bundleInfo(b)
	}
	if b := outermostBundle(info.Path); b != "" {
		_, ident := bundleInfo(b)
		info.Package = cmpOr(ident, b)
	}
	return info
}

// ExecutablePath returns the path of the process id ("" when it cannot be
// read or id is gone): ReadInfo without the app name and bundle, which
// need NSWorkspace (slow the first time).
func ExecutablePath(id ID) string {
	knownMu.Lock()
	kn, ok := knownBy[id]
	knownMu.Unlock()
	if ok {
		return kn.path
	}
	if k, ok := kinfo(id.PID); !ok || idOf(k) != id {
		return ""
	}
	l, err := loadLib()
	if err != nil {
		return ""
	}
	return l.path(id.PID)
}

// Warm reads the running apps and the bundles of the running processes
// once, so that the first sample of the process manager does not wait for
// NSWorkspace to start and every Info.plist to be read (about 0.2 s in
// all); later processes of the same apps find their bundle cached.
func Warm() {
	runningApps()
	var s Sampler
	snap, err := s.snapshot(false)
	if err != nil {
		return
	}
	for _, p := range snap.Procs {
		ReadInfo(p.ID)
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// CommandLine returns the arguments of the process id (KERN_PROCARGS2),
// quoted where needed. macOS gives them only for the user's own processes:
// ErrNeedsAdmin for the others.
func CommandLine(id ID) (string, error) {
	if k, ok := kinfo(id.PID); !ok || idOf(k) != id {
		return "", ErrGone
	}
	b, err := unix.SysctlRaw("kern.procargs2", int(id.PID))
	if err != nil {
		if _, ok := kinfo(id.PID); !ok {
			return "", ErrGone
		}
		return "", ErrNeedsAdmin
	}
	exe, args, err := parseProcArgs(b)
	if err != nil {
		return "", err
	}
	if len(args) == 0 {
		args = []string{exe}
	}
	return joinArgs(args), nil
}

// Windows returns the regular apps (those in the Dock) as windows: macOS
// gives no window titles without the Screen Recording permission, so each
// app is one window without a title whose handle is its PID. Whether an
// app is not responding is not known (Hung is always false).
func Windows() ([]Window, error) {
	var out []Window
	for pid, a := range runningApps() {
		if a.regular {
			out = append(out, Window{HWND: uintptr(pid), PID: pid})
		}
	}
	return out, nil
}

// Identify returns the instance running with pid.
func Identify(pid uint32) (ID, error) {
	k, ok := kinfo(pid)
	if !ok {
		return ID{}, ErrGone
	}
	return idOf(k), nil
}

// --- Ending -------------------------------------------------------------------

// Kill ends the process id at once (SIGKILL), after checking that the PID
// still belongs to that instance and that it may be ended. Another user's
// process gives ErrNeedsAdmin (ErrProtected when this process is root).
func Kill(id ID) error {
	self := uint32(os.Getpid())
	if id.PID == 0 || id.PID == 1 || id.PID == self {
		return ErrProtected
	}
	k, ok := kinfo(id.PID)
	if !ok || idOf(k) != id {
		return ErrGone
	}
	facts := Facts{ID: id, Name: commName(k), Self: self}
	if l, err := loadLib(); err == nil {
		facts.Path = l.path(id.PID)
	}
	if facts.Path != "" {
		facts.Name = filepath.Base(facts.Path)
	}
	facts.Critical = macCritical(id.PID, facts.Name, facts.Path)
	if class, _ := Classify(facts); !class.Endable() {
		return ErrProtected
	}
	// The check above and the signal are not atomic, but a PID is reused
	// only after many others: the window is a few microseconds.
	switch err := unix.Kill(int(id.PID), unix.SIGKILL); {
	case errors.Is(err, unix.ESRCH):
		return ErrGone
	case errors.Is(err, unix.EPERM):
		if IsElevated() {
			return ErrProtected
		}
		return ErrNeedsAdmin
	case err != nil:
		return fmt.Errorf("process: kill: %w", err)
	}
	deadline := time.Now().Add(killWait)
	for time.Now().Before(deadline) {
		if k, ok := kinfo(id.PID); !ok || idOf(k) != id {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

// KillTree ends root and then its descendants, level by level, each with
// the checks of Kill.
func KillTree(root ID) KillReport {
	var r KillReport
	var s Sampler
	snap, err := s.snapshot(false) // only the tree
	if err != nil {
		r.Add(root, err)
		return r
	}
	ids := Tree(snap.Procs, root)
	if len(ids) == 0 {
		r.Add(root, ErrGone)
		return r
	}
	for i, id := range ids {
		err := Kill(id)
		r.Add(id, err)
		if i == 0 && errors.Is(err, ErrProtected) {
			break // never the descendants of a process that must not be ended
		}
	}
	return r
}

// CloseWindows asks the apps whose PIDs are hwnds to quit, as Quit in the
// Dock does (they may ask to save first).
func CloseWindows(hwnds []uintptr) error {
	var errs []error
	for _, h := range hwnds {
		pid := uint32(h)
		asked := false
		if !runningApp(pid, func(a ns.ID) { asked = ns.SendBool(a, "terminate") }) {
			errs = append(errs, fmt.Errorf("process: %d is not an app", pid))
		} else if !asked {
			errs = append(errs, fmt.Errorf("process: %d did not accept to quit", pid))
		}
	}
	if len(errs) == len(hwnds) {
		return errors.Join(errs...)
	}
	return nil
}

// Wait waits until every process of ids has exited or ctx is done, and
// returns those that have exited (a PID now belonging to another process
// counts as exited), in the order of ids.
func Wait(ctx context.Context, ids []ID) []ID {
	exited := map[ID]bool{}
	for {
		for _, id := range ids {
			if !exited[id] {
				if k, ok := kinfo(id.PID); !ok || idOf(k) != id {
					exited[id] = true
				}
			}
		}
		if len(exited) >= len(ids) || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	var out []ID
	for _, id := range ids {
		if exited[id] {
			out = append(out, id)
		}
	}
	return out
}

// SwitchTo brings the app whose PID is hwnd to the front with all its
// windows.
func SwitchTo(hwnd uintptr) error {
	const activateAllWindows, ignoringOtherApps = 1, 2
	ok := false
	if !runningApp(uint32(hwnd), func(a ns.ID) {
		ns.Send(a, "unhide")
		ok = ns.SendBool(a, "activateWithOptions:", activateAllWindows|ignoringOtherApps)
	}) {
		return ErrGone
	}
	if !ok {
		return errors.New("process: the app could not be activated")
	}
	return nil
}

// RunElevated runs exe with args as root once macOS asked for an
// administrator's password (osascript's "with administrator privileges";
// the prompt's text comes from WithPrompt), and returns its exit code;
// ErrCancelled when the user declined. Tests never show the prompt.
func RunElevated(ctx context.Context, exe string, args []string) (int, error) {
	if testing.Testing() {
		return 0, errors.New("process: refusing to ask for administrator rights in a test")
	}
	argv := elevateArgv(promptOf(ctx), exe, args)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return elevateResult(stdout.String(), stderr.String(), err)
}

// IsElevated reports whether this process runs as root.
func IsElevated() bool { return os.Geteuid() == 0 }

// StartExplorer is only supported on Windows.
func StartExplorer() error { return errors.ErrUnsupported }

// HelperMain runs the elevated helper (as root, see RunElevated).
func HelperMain(args []string) int { return helperMain(args, Kill, KillTree) }
