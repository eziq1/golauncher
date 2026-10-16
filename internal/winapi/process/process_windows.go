package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/eziq1/golauncher/internal/winapi/shell"
)

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemTimes       = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procIsProcessCritical    = kernel32.NewProc("IsProcessCritical")
	procGetPackageFullName   = kernel32.NewProc("GetPackageFullName")

	user32                    = windows.NewLazySystemDLL("user32.dll")
	procGetWindow             = user32.NewProc("GetWindow")
	procGetWindowLongPtrW     = user32.NewProc("GetWindowLongPtrW")
	procInternalGetWindowText = user32.NewProc("InternalGetWindowText")
	procIsHungAppWindow       = user32.NewProc("IsHungAppWindow")
	procFindWindowW           = user32.NewProc("FindWindowW")
	procPostMessageW          = user32.NewProc("PostMessageW")
	procIsIconic              = user32.NewProc("IsIconic")
	procShowWindow            = user32.NewProc("ShowWindow")
	procSetForegroundWindow   = user32.NewProc("SetForegroundWindow")

	shell32             = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteExW = shell32.NewProc("ShellExecuteExW")
)

const (
	queryLimited = windows.PROCESS_QUERY_LIMITED_INFORMATION

	errorCancelled         = windows.Errno(1223)  // ERROR_CANCELLED
	appmodelErrorNoPackage = windows.Errno(15700) // APPMODEL_ERROR_NO_PACKAGE

	gwOwner         = 4
	gwlExStyle      = -20
	wsExToolWindow  = 0x80
	wmClose         = 0x10
	swHide          = 0
	seeMaskNoClose  = 0x40 // SEE_MASK_NOCLOSEPROCESS
	snapshotInitial = 1 << 20
	snapshotSlack   = 64 << 10
	snapshotRetries = 3
	maxWaitObjects  = 64 // MAXIMUM_WAIT_OBJECTS
)

// native converts an address returned by Windows to a pointer without
// tripping vet's unsafeptr check; the memory is not managed by Go.
func native(addr uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&addr)) }

// filetime converts a FILETIME to 100 ns since 1601.
func filetime(f windows.Filetime) int64 {
	return int64(f.HighDateTime)<<32 | int64(f.LowDateTime)
}

// --- Snapshots ----------------------------------------------------------------

// Sampler takes snapshots; it reuses its buffer between them. It is safe
// for concurrent use.
type Sampler struct {
	mu  sync.Mutex
	buf []byte
	n   int // number of processes last time, to size the next slice
}

// alignedBuf allocates n bytes aligned for SYSTEM_PROCESS_INFORMATION.
func alignedBuf(n int) []byte {
	words := make([]uint64, (n+7)/8)
	return unsafe.Slice((*byte)(unsafe.Pointer(&words[0])), len(words)*8)
}

// Snapshot reads every process with one NtQuerySystemInformation call,
// with the system's CPU times and memory.
func (s *Sampler) Snapshot() (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.buf) == 0 {
		s.buf = alignedBuf(snapshotInitial)
	}
	var used uint32
	for i := 0; ; i++ {
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&s.buf[0]), uint32(len(s.buf)), &used)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) || i == snapshotRetries {
			return nil, fmt.Errorf("process: NtQuerySystemInformation: %w", err)
		}
		s.buf = alignedBuf(max(int(used), len(s.buf)) + snapshotSlack)
	}
	snap := &Snapshot{Time: time.Now()}
	var now windows.Filetime
	windows.GetSystemTimeAsFileTime(&now)
	snap.Stamp = filetime(now)
	snap.Procs = parseProcesses(s.buf[:min(int(used), len(s.buf))], s.n)
	s.n = len(snap.Procs)
	snap.CPUs = int(windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS))
	var idle, kernel, user windows.Filetime
	if r, _, _ := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); r != 0 {
		snap.SysIdle = filetime(idle)
		// The kernel time includes the idle time.
		snap.SysBusy = max(filetime(kernel)+filetime(user)-snap.SysIdle, 0)
	}
	ms := memoryStatusEx{}
	ms.length = uint32(unsafe.Sizeof(ms))
	if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms))); r != 0 {
		snap.MemTotal, snap.MemAvail = ms.totalPhys, ms.availPhys
	}
	return snap, nil
}

// memoryStatusEx is MEMORYSTATUSEX.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

// parseProcesses reads the SYSTEM_PROCESS_INFORMATION entries of buf,
// following their NextEntryOffset chain. Image names are read only from
// inside buf; hint is the expected number of processes.
func parseProcesses(buf []byte, hint int) []Proc {
	const size = int(unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{}))
	out := make([]Proc, 0, max(hint, 64)+16)
	if len(buf) == 0 {
		return out
	}
	base := uintptr(unsafe.Pointer(&buf[0]))
	for off := 0; off+size <= len(buf); {
		e := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buf[off]))
		p := Proc{
			ID:                ID{PID: uint32(e.UniqueProcessID), Created: e.CreateTime},
			Parent:            uint32(e.InheritedFromUniqueProcessID),
			Session:           e.SessionID,
			Threads:           e.NumberOfThreads,
			Handles:           e.HandleCount,
			Priority:          e.BasePriority,
			CPU:               e.UserTime + e.KernelTime,
			PrivateWorkingSet: uint64(max(e.WorkingSetPrivateSize, 0)),
			WorkingSet:        uint64(e.WorkingSetSize),
			Commit:            uint64(e.PrivatePageCount),
			PeakWorkingSet:    uint64(e.PeakWorkingSetSize),
			IO:                uint64(max(e.ReadTransferCount, 0)) + uint64(max(e.WriteTransferCount, 0)),
		}
		if p.PID == 0 {
			p.Name = "System Idle Process"
		} else if n := int(e.ImageName.Length) / 2; n > 0 && e.ImageName.Buffer != nil {
			start := uintptr(unsafe.Pointer(e.ImageName.Buffer)) - base
			if start%2 == 0 && start < uintptr(len(buf)) && start+uintptr(n)*2 <= uintptr(len(buf)) {
				p.Name = windows.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&buf[start])), n))
			}
		}
		out = append(out, p)
		next := int(e.NextEntryOffset)
		if next <= 0 {
			break
		}
		off += next
	}
	return out
}

// --- Per-process information -------------------------------------------------

// openProcess opens pid with access and checks that it is the instance
// created at created (when not 0). It returns ErrGone for an exited or
// reused PID and ErrNeedsAdmin when access is denied.
func openProcess(pid uint32, access uint32, created int64) (windows.Handle, error) {
	h, err := windows.OpenProcess(access, false, pid)
	if err != nil {
		return 0, openError(err)
	}
	if created != 0 {
		c, err := createdOf(h)
		if err != nil {
			windows.CloseHandle(h)
			return 0, fmt.Errorf("process: GetProcessTimes: %w", err)
		}
		if c != created {
			windows.CloseHandle(h)
			return 0, ErrGone
		}
	}
	return h, nil
}

// openError maps the error of OpenProcess.
func openError(err error) error {
	switch {
	case errors.Is(err, windows.ERROR_INVALID_PARAMETER):
		return ErrGone
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return ErrNeedsAdmin
	}
	return fmt.Errorf("process: OpenProcess: %w", err)
}

// createdOf returns the creation time of process h.
func createdOf(h windows.Handle) (int64, error) {
	var created, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exit, &kernel, &user); err != nil {
		return 0, err
	}
	return filetime(created), nil
}

// imagePath returns the full path of the executable of process h, or "".
func imagePath(h windows.Handle) string {
	for _, n := range []uint32{windows.MAX_PATH + 1, windows.MAX_LONG_PATH} {
		buf := make([]uint16, n)
		if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err == nil {
			return windows.UTF16ToString(buf[:n])
		} else if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
			return ""
		}
	}
	return ""
}

// isCritical reports IsProcessCritical of process h.
func isCritical(h windows.Handle) bool {
	if procIsProcessCritical.Find() != nil {
		return false
	}
	var b int32
	r, _, _ := procIsProcessCritical.Call(uintptr(h), uintptr(unsafe.Pointer(&b)))
	return r != 0 && b != 0
}

// packageName returns the package full name of process h, or "".
func packageName(h windows.Handle) string {
	if procGetPackageFullName.Find() != nil {
		return ""
	}
	n := uint32(128)
	for range 2 {
		buf := make([]uint16, n)
		r, _, _ := procGetPackageFullName.Call(uintptr(h), uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&buf[0])))
		switch windows.Errno(r) {
		case 0:
			return windows.UTF16ToString(buf[:min(int(n), len(buf))])
		case windows.ERROR_INSUFFICIENT_BUFFER:
			continue
		case appmodelErrorNoPackage:
			return ""
		default:
			return ""
		}
	}
	return ""
}

var (
	cacheMu sync.Mutex
	// accounts caches "DOMAIN\name" by SID string.
	accounts = map[string]string{}
	// descriptions caches FileDescription by lower-case path.
	descriptions = map[string]string{}
)

// maxCached bounds each cache; a full cache is emptied.
const maxCached = 4096

// description returns the FileDescription of exe, cached.
func description(exe string) string {
	key := strings.ToLower(exe)
	cacheMu.Lock()
	d, ok := descriptions[key]
	cacheMu.Unlock()
	if ok {
		return d
	}
	d = shell.FileDescription(exe)
	cacheMu.Lock()
	if len(descriptions) >= maxCached {
		clear(descriptions)
	}
	descriptions[key] = d
	cacheMu.Unlock()
	return d
}

// accountName returns "DOMAIN\name" of sid, cached.
func accountName(sid *windows.SID) string {
	key := sid.String()
	cacheMu.Lock()
	name, ok := accounts[key]
	cacheMu.Unlock()
	if ok {
		return name
	}
	account, domain, _, err := sid.LookupAccount("")
	if err == nil {
		name = account
		if domain != "" {
			name = domain + `\` + account
		}
	}
	cacheMu.Lock()
	if len(accounts) >= maxCached {
		clear(accounts)
	}
	accounts[key] = name
	cacheMu.Unlock()
	return name
}

// readToken fills the user, elevation and integrity of process h.
func readToken(h windows.Handle, info *Info) {
	var tok windows.Token
	if windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok) != nil {
		return
	}
	defer tok.Close()
	if u, err := tok.GetTokenUser(); err == nil {
		sid := u.User.Sid
		switch sid.String() {
		case "S-1-5-18", "S-1-5-19", "S-1-5-20":
			info.SystemAccount = true
		}
		info.User = accountName(sid)
	}
	if tok.IsElevated() {
		info.Elevated = Yes
	} else {
		info.Elevated = No
	}
	buf := alignedBuf(64)
	for range 2 {
		var n uint32
		err := windows.GetTokenInformation(tok, windows.TokenIntegrityLevel, &buf[0], uint32(len(buf)), &n)
		if errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) && int(n) > len(buf) {
			buf = alignedBuf(int(n))
			continue
		}
		if err != nil {
			return
		}
		label := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buf[0]))
		if rid, ok := lastSubAuthority(buf[:min(int(n), len(buf))], label.Label.Sid); ok {
			info.Integrity = integrityName(rid)
		}
		return
	}
}

// lastSubAuthority reads the last sub-authority (the RID) of sid, which
// lies inside buf. It reads the SID's bytes itself: GetSidSubAuthority
// returns a pointer into the Go buffer that checkptr rejects.
func lastSubAuthority(buf []byte, sid *windows.SID) (uint32, bool) {
	if len(buf) == 0 || sid == nil {
		return 0, false
	}
	off := uintptr(unsafe.Pointer(sid)) - uintptr(unsafe.Pointer(&buf[0]))
	// SID: revision (1), sub-authority count (1), identifier authority (6),
	// then count little-endian uint32s.
	if off >= uintptr(len(buf)) || off+8 > uintptr(len(buf)) {
		return 0, false
	}
	count := uintptr(buf[off+1])
	if count == 0 || off+8+4*count > uintptr(len(buf)) {
		return 0, false
	}
	b := buf[off+8+4*(count-1):]
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24, true
}

// integrityName names an integrity level RID.
func integrityName(rid uint32) string {
	switch {
	case rid >= 0x4000:
		return "System"
	case rid >= 0x3000:
		return "High"
	case rid >= 0x2000:
		return "Medium"
	case rid >= 0x1000:
		return "Low"
	}
	return ""
}

// ReadInfo reads what a snapshot lacks about process id. An exited process
// or a reused PID gives an empty Info; a process that cannot be opened even
// for limited queries gives Info{Denied: true}.
func ReadInfo(id ID) Info {
	h, err := windows.OpenProcess(queryLimited, false, id.PID)
	if err != nil {
		return Info{Denied: !errors.Is(err, windows.ERROR_INVALID_PARAMETER)}
	}
	defer windows.CloseHandle(h)
	if c, err := createdOf(h); err != nil || c != id.Created {
		return Info{}
	}
	var info Info
	info.Path = imagePath(h)
	if info.Path != "" {
		info.Description = description(info.Path)
	}
	readToken(h, &info)
	info.Package = packageName(h)
	info.Critical = isCritical(h)
	return info
}

// CommandLine reads the command line of process id. It returns ErrGone
// when the process exited and ErrNeedsAdmin when it cannot be read.
func CommandLine(id ID) (string, error) {
	h, err := openProcess(id.PID, queryLimited, id.Created)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	size := uint32(4 << 10)
	for range 4 {
		buf := alignedBuf(int(size))
		var n uint32
		err := windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, unsafe.Pointer(&buf[0]), uint32(len(buf)), &n)
		switch {
		case err == nil:
			return unicodeIn(buf), nil
		case errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH), errors.Is(err, windows.STATUS_BUFFER_TOO_SMALL), errors.Is(err, windows.STATUS_BUFFER_OVERFLOW):
			size = max(n, size*2) + 64
		case errors.Is(err, windows.STATUS_ACCESS_DENIED):
			return "", ErrNeedsAdmin
		default:
			return "", fmt.Errorf("process: NtQueryInformationProcess: %w", err)
		}
	}
	return "", errors.New("process: the command line keeps growing")
}

// unicodeIn reads the UNICODE_STRING at the start of buf, whose characters
// are inside buf.
func unicodeIn(buf []byte) string {
	const hdr = int(unsafe.Sizeof(windows.NTUnicodeString{}))
	if len(buf) < hdr {
		return ""
	}
	u := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0]))
	n := int(u.Length) / 2
	if n == 0 || u.Buffer == nil {
		return ""
	}
	start := uintptr(unsafe.Pointer(u.Buffer)) - uintptr(unsafe.Pointer(&buf[0]))
	if start%2 != 0 || start >= uintptr(len(buf)) || start+uintptr(n)*2 > uintptr(len(buf)) {
		return ""
	}
	return windows.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&buf[start])), n))
}

// Identify returns the ID of the process running as pid now.
func Identify(pid uint32) (ID, error) {
	h, err := openProcess(pid, queryLimited, 0)
	if err != nil {
		return ID{}, err
	}
	defer windows.CloseHandle(h)
	c, err := createdOf(h)
	if err != nil {
		return ID{}, fmt.Errorf("process: GetProcessTimes: %w", err)
	}
	return ID{PID: pid, Created: c}, nil
}

// --- Windows ------------------------------------------------------------------

var (
	// enumMu serializes window enumerations, which collect into enumOut.
	enumMu  sync.Mutex
	enumOut []uintptr
	// enumCallback is the one callback of EnumWindows and EnumChildWindows
	// (callbacks are a limited resource).
	enumCallback = sync.OnceValue(func() uintptr {
		return windows.NewCallback(func(hwnd, _ uintptr) uintptr {
			enumOut = append(enumOut, hwnd)
			return 1
		})
	})
)

// enumWindows returns the top-level windows (parent 0) or the children of
// parent.
func enumWindows(parent uintptr) ([]uintptr, error) {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumOut = enumOut[:0]
	var err error
	if parent == 0 {
		err = windows.EnumWindows(enumCallback(), nil)
	} else {
		windows.EnumChildWindows(windows.HWND(parent), enumCallback(), nil)
	}
	return append([]uintptr(nil), enumOut...), err
}

func className(hwnd uintptr) string {
	var buf [256]uint16
	n, err := windows.GetClassName(windows.HWND(hwnd), &buf[0], int32(len(buf)))
	if err != nil || n <= 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// windowText reads a window's title without sending it a message.
func windowText(hwnd uintptr) string {
	var buf [512]uint16
	n, _, _ := procInternalGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf[:min(int(n), len(buf))])
}

func windowPID(hwnd uintptr) uint32 {
	var pid uint32
	windows.GetWindowThreadProcessId(windows.HWND(hwnd), &pid)
	return pid
}

func isHung(hwnd uintptr) bool {
	r, _, _ := procIsHungAppWindow.Call(hwnd)
	return r != 0
}

func cloaked(hwnd uintptr) bool {
	var v uint32
	if windows.DwmGetWindowAttribute(windows.HWND(hwnd), windows.DWMWA_CLOAKED, unsafe.Pointer(&v), uint32(unsafe.Sizeof(v))) != nil {
		return false
	}
	return v != 0
}

func findWindow(class string) uintptr {
	name, err := windows.UTF16PtrFromString(class)
	if err != nil {
		return 0
	}
	h, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(name)), 0)
	return h
}

// Windows returns the windows the taskbar would show, and the taskbar and
// desktop windows of the shell (Shell set). It sends no messages, so a hung
// window cannot block it.
func Windows() ([]Window, error) {
	hwnds, err := enumWindows(0)
	if err != nil {
		return nil, fmt.Errorf("process: EnumWindows: %w", err)
	}
	var out []Window
	for _, h := range hwnds {
		if !windows.IsWindowVisible(windows.HWND(h)) {
			continue
		}
		if owner, _, _ := procGetWindow.Call(h, gwOwner); owner != 0 {
			continue
		}
		ex, _, _ := procGetWindowLongPtrW.Call(h, gwlExStyleArg())
		if ex&wsExToolWindow != 0 {
			continue
		}
		title := windowText(h)
		if title == "" || cloaked(h) {
			continue
		}
		w := Window{HWND: h, PID: windowPID(h), Title: title}
		if className(h) == "ApplicationFrameWindow" {
			// A UWP app's frame belongs to ApplicationFrameHost; the app's
			// own process owns its CoreWindow child.
			children, _ := enumWindows(h)
			for _, c := range children {
				if className(c) == "Windows.UI.Core.CoreWindow" {
					if pid := windowPID(c); pid != 0 && pid != w.PID {
						w.PID = pid
						break
					}
				}
			}
		}
		w.Hung = isHung(h)
		out = append(out, w)
	}
	for _, class := range []string{"Shell_TrayWnd", "Progman"} {
		h := findWindow(class)
		if h == 0 {
			continue
		}
		found := false
		for i := range out {
			if out[i].HWND == h {
				out[i].Shell, found = true, true
			}
		}
		if !found {
			out = append(out, Window{HWND: h, PID: windowPID(h), Title: windowText(h), Hung: isHung(h), Shell: true})
		}
	}
	return out, nil
}

// gwlExStyleArg passes the negative GWL_EXSTYLE index as a uintptr.
func gwlExStyleArg() uintptr {
	i := gwlExStyle
	return uintptr(i)
}

// CloseWindows asks each window to close by posting WM_CLOSE; it never
// waits for a window to answer.
func CloseWindows(hwnds []uintptr) error {
	var errs []error
	for _, h := range hwnds {
		if r, _, err := procPostMessageW.Call(h, wmClose, 0, 0); r == 0 {
			errs = append(errs, fmt.Errorf("process: PostMessage to window %#x: %w", h, err))
		}
	}
	return errors.Join(errs...)
}

// SwitchTo brings a window to the foreground, restoring it when minimized.
func SwitchTo(hwnd uintptr) error {
	if r, _, _ := procIsIconic.Call(hwnd); r != 0 {
		procShowWindow.Call(hwnd, windows.SW_RESTORE)
	}
	if r, _, err := procSetForegroundWindow.Call(hwnd); r == 0 {
		return fmt.Errorf("process: SetForegroundWindow: %w", err)
	}
	return nil
}

// --- Ending processes ----------------------------------------------------------

// Kill ends process id (TerminateProcess). It refuses the kernel, critical
// system processes and golauncher itself (ErrProtected), checks the
// creation time on the very handle that ends the process (ErrGone for an
// exited process or a reused PID), and returns ErrNeedsAdmin when ending it
// needs administrator rights (ErrProtected when this process is already
// elevated: a protected process refuses administrators too).
func Kill(id ID) error {
	self := uint32(os.Getpid())
	if id.PID == 0 || id.PID == 4 || id.PID == self {
		return ErrProtected
	}
	facts := Facts{ID: id, Self: self, SystemRoot: os.Getenv("SystemRoot")}
	h, err := openProcess(id.PID, queryLimited|windows.SYNCHRONIZE, id.Created)
	switch {
	case err == nil:
		facts.Path = imagePath(h)
		facts.Critical = isCritical(h)
		windows.CloseHandle(h)
		if facts.Path != "" {
			facts.Name = filepath.Base(facts.Path)
		}
	case errors.Is(err, ErrNeedsAdmin):
	default:
		return err
	}
	if facts.Name == "" {
		var s Sampler // not kept: its buffer is 1 MB
		snap, err := s.Snapshot()
		if err != nil {
			return err
		}
		p, ok := snap.Find(id)
		if !ok {
			return ErrGone
		}
		facts.Name = p.Name
	}
	if class, _ := Classify(facts); !class.Endable() && class != ClassShell {
		return ErrProtected
	}
	h, err = openProcess(id.PID, windows.PROCESS_TERMINATE|queryLimited|windows.SYNCHRONIZE, id.Created)
	if err != nil {
		return deniedError(err)
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		if ev, _ := windows.WaitForSingleObject(h, 0); ev == windows.WAIT_OBJECT_0 {
			return ErrGone // it exited meanwhile
		}
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return deniedError(ErrNeedsAdmin)
		}
		return fmt.Errorf("process: TerminateProcess: %w", err)
	}
	windows.WaitForSingleObject(h, 2000)
	return nil
}

// deniedError turns ErrNeedsAdmin into ErrProtected when this process is
// elevated already.
func deniedError(err error) error {
	if errors.Is(err, ErrNeedsAdmin) && IsElevated() {
		return ErrProtected
	}
	return err
}

// KillTree ends root and then its descendants, level by level, each with
// the checks of Kill.
func KillTree(root ID) KillReport {
	var r KillReport
	var s Sampler
	snap, err := s.Snapshot()
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
			// Never the descendants of a process that must not be ended
			// (the services of services.exe, golauncher's own tasks).
			break
		}
	}
	return r
}

// Wait waits until every process of ids has exited or ctx is done, and
// returns those that have exited (a PID now belonging to another process
// counts as exited), in the order of ids.
func Wait(ctx context.Context, ids []ID) []ID {
	type waiting struct {
		id ID
		h  windows.Handle
	}
	exited := map[ID]bool{}
	var handles []waiting
	var unopened []ID // cannot be opened: checked through snapshots
	defer func() {
		for _, w := range handles {
			windows.CloseHandle(w.h)
		}
	}()
	for _, id := range ids {
		if exited[id] {
			continue
		}
		h, err := openProcess(id.PID, windows.SYNCHRONIZE|queryLimited, id.Created)
		switch {
		case err == nil:
			handles = append(handles, waiting{id, h})
		case errors.Is(err, ErrNeedsAdmin):
			unopened = append(unopened, id)
		default:
			exited[id] = true
		}
	}
	var s Sampler
	var lastSnap time.Time
	for {
		pending := handles[:0]
		for _, w := range handles {
			if ev, _ := windows.WaitForSingleObject(w.h, 0); ev == windows.WAIT_OBJECT_0 {
				exited[w.id] = true
				windows.CloseHandle(w.h)
				continue
			}
			pending = append(pending, w)
		}
		handles = pending
		if len(unopened) > 0 && time.Since(lastSnap) >= 500*time.Millisecond {
			lastSnap = time.Now()
			if snap, err := s.Snapshot(); err == nil {
				left := unopened[:0]
				for _, id := range unopened {
					if _, ok := snap.Find(id); ok {
						left = append(left, id)
					} else {
						exited[id] = true
					}
				}
				unopened = left
			}
		}
		if len(handles)+len(unopened) == 0 || ctx.Err() != nil {
			break
		}
		if len(handles) > 0 {
			hs := make([]windows.Handle, 0, min(len(handles), maxWaitObjects))
			for _, w := range handles[:min(len(handles), maxWaitObjects)] {
				hs = append(hs, w.h)
			}
			windows.WaitForMultipleObjects(hs, false, 50)
		} else {
			select {
			case <-ctx.Done():
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	var out []ID
	for _, id := range ids {
		if exited[id] {
			out = append(out, id)
			exited[id] = false // once
		}
	}
	return out
}

// --- Elevation -----------------------------------------------------------------

// shellExecuteInfo is SHELLEXECUTEINFOW.
type shellExecuteInfo struct {
	cbSize        uint32
	mask          uint32
	hwnd          uintptr
	verb          *uint16
	file          *uint16
	parameters    *uint16
	directory     *uint16
	show          int32
	instApp       uintptr
	idList        uintptr
	class         *uint16
	keyClass      uintptr
	hotKey        uint32
	iconOrMonitor uintptr
	process       windows.Handle
}

// RunElevated runs exe with args as administrator through a UAC prompt,
// hidden, and returns its exit code. It returns ErrCancelled when the user
// declines the prompt, and ctx's error when ctx ends first (the program
// keeps running).
func RunElevated(ctx context.Context, exe string, args []string) (int, error) {
	info := shellExecuteInfo{mask: seeMaskNoClose, show: swHide}
	info.cbSize = uint32(unsafe.Sizeof(info))
	var err error
	if info.verb, err = windows.UTF16PtrFromString("runas"); err != nil {
		return 0, err
	}
	if info.file, err = windows.UTF16PtrFromString(exe); err != nil {
		return 0, err
	}
	if info.parameters, err = windows.UTF16PtrFromString(windows.ComposeCommandLine(args)); err != nil {
		return 0, err
	}
	err = func() error {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		// S_OK or S_FALSE (already initialized) both need CoUninitialize.
		if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err == nil || err == windows.Errno(1) {
			defer windows.CoUninitialize()
		}
		if r, _, e := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
			if errors.Is(e, errorCancelled) {
				return ErrCancelled
			}
			return fmt.Errorf("process: ShellExecuteEx: %w", e)
		}
		return nil
	}()
	if err != nil {
		return 0, err
	}
	if info.process == 0 {
		return 0, errors.New("process: ShellExecuteEx returned no process")
	}
	defer windows.CloseHandle(info.process)
	for {
		ev, err := windows.WaitForSingleObject(info.process, 100)
		if err != nil {
			return 0, fmt.Errorf("process: waiting for the helper: %w", err)
		}
		if ev == windows.WAIT_OBJECT_0 {
			break
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.process, &code); err != nil {
		return 0, fmt.Errorf("process: GetExitCodeProcess: %w", err)
	}
	return int(code), nil
}

// IsElevated reports whether this process runs as administrator.
func IsElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

// StartExplorer starts %SystemRoot%\explorer.exe and does not wait for it.
func StartExplorer() error {
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "explorer.exe"))
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("process: starting explorer: %w", err)
	}
	return cmd.Process.Release()
}

// HelperMain runs the elevated helper (args start with HelperCommand) and
// returns its exit code.
func HelperMain(args []string) int { return helperMain(args, Kill, KillTree) }
