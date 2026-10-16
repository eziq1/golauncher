// Package process reads and ends Windows processes, for the process manager
// plugin (docs/process-manager.md) and the port owners of the network
// tools (docs/network-tools.md).
//
// One NtQuerySystemInformation(SystemProcessInformation) call gives every
// process's CPU time, memory, I/O, handles, threads, parent and creation
// time (Snapshot). Fields it lacks (path, user, elevation, package, version
// description) are read once per process instance (ReadInfo). A process
// instance is identified by its PID and creation time (ID): ending a
// process checks the creation time on the handle used to end it, so a PID
// reused by another process is never ended by mistake.
//
// This file holds the platform-independent part: the types, the CPU and I/O
// rate arithmetic, grouping, process trees, the classification of
// processes that must not be ended, command line redaction and the
// arguments of the elevated helper. It is tested with fixtures on any
// platform. Note that internal/winapi/proc (running background programs) is
// a different package.
package process

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrGone reports that the process has exited, or that its PID now
	// belongs to another process (the creation time differs).
	ErrGone = errors.New("process: the process has exited")
	// ErrNeedsAdmin reports that ending or reading the process needs
	// administrator rights.
	ErrNeedsAdmin = errors.New("process: needs administrator rights")
	// ErrProtected reports a process that must not or cannot be ended: the
	// kernel, a critical system process, golauncher itself, or a protected
	// process (PPL) that refuses even an administrator.
	ErrProtected = errors.New("process: protected process")
	// ErrCancelled reports that the user cancelled the UAC prompt.
	ErrCancelled = errors.New("process: cancelled")
)

// ID identifies a process instance. A PID is reused after the process
// exits; a PID with its creation time is not.
type ID struct {
	PID uint32 `json:"pid"`
	// Created is the creation time as a FILETIME (100 ns since 1601).
	Created int64 `json:"created"`
}

func (id ID) String() string { return fmt.Sprintf("%d:%d", id.PID, id.Created) }

// Proc is a process in a Snapshot.
type Proc struct {
	ID
	// Parent is the PID of the process that created this one. It may have
	// exited and the PID been reused: see ParentOf.
	Parent   uint32
	Name     string // executable name, e.g. "fabrikam.exe"
	Session  uint32
	Threads  uint32
	Handles  uint32
	Priority int32 // base priority
	// CPU is the processor time used so far (user + kernel), in 100 ns.
	CPU int64
	// Memory, in bytes: the private working set (Task Manager's "Memory"),
	// the working set, the commit size and the peak working set.
	PrivateWorkingSet uint64
	WorkingSet        uint64
	Commit            uint64
	PeakWorkingSet    uint64
	// IO is the bytes read and written so far (including cached I/O and
	// named pipes, so it is not disk I/O).
	IO uint64
}

// Snapshot is the state of every process at one time.
type Snapshot struct {
	// Time is when the snapshot was taken (with a monotonic reading on the
	// real system); rates divide by the difference of two Times.
	Time time.Time
	// Stamp is the same time as a FILETIME: a process with Created after the
	// Stamp of the previous snapshot started between the two.
	Stamp int64
	Procs []Proc
	// CPUs is the number of logical processors (all processor groups).
	CPUs int
	// SysIdle and SysBusy are the system's idle and busy processor time so
	// far (GetSystemTimes: kernel + user − idle), in 100 ns.
	SysIdle, SysBusy int64
	// MemTotal and MemAvail are the physical memory, in bytes.
	MemTotal, MemAvail uint64
}

// Find returns the process with id.
func (s *Snapshot) Find(id ID) (Proc, bool) {
	for _, p := range s.Procs {
		if p.ID == id {
			return p, true
		}
	}
	return Proc{}, false
}

// Tri is a yes/no answer that may be unknown.
type Tri int8

const (
	Unknown Tri = iota
	No
	Yes
)

// Info is what a snapshot lacks about a process, read once per instance.
// Fields that cannot be read are left empty.
type Info struct {
	Path        string // full path of the executable
	Description string // FileDescription of its version information
	User        string // "DOMAIN\name"
	// SystemAccount is set for SYSTEM, LOCAL SERVICE and NETWORK SERVICE.
	SystemAccount bool
	Elevated      Tri
	Integrity     string // "Low", "Medium", "High", "System", or ""
	Package       string // package full name of a packaged (UWP/MSIX) app
	// Critical is IsProcessCritical: ending the process stops Windows.
	Critical bool
	// Denied is set when the process could not be opened even for limited
	// queries (a protected process).
	Denied bool
}

// Window is a top-level window that the taskbar would show (visible,
// unowned, not a tool window, with a title), or the shell's taskbar.
type Window struct {
	HWND uintptr
	// PID owns the window; for a UWP app's frame (ApplicationFrameHost) it
	// is the app's own process.
	PID   uint32
	Title string
	// Hung is IsHungAppWindow: the window has not answered messages for a
	// few seconds ("Not responding").
	Hung bool
	// Shell marks the taskbar (Shell_TrayWnd) or the desktop (Progman): its
	// explorer.exe is restarted rather than ended.
	Shell bool
}

// CPUMode is how a process's CPU use is expressed.
type CPUMode int

const (
	// CPUTotal is a share of all processors: all processes add up to at most
	// 100%, as in Task Manager.
	CPUTotal CPUMode = iota
	// CPUPerCore is a share of one processor, up to CPUs × 100%, as in top.
	CPUPerCore
)

// Usage is what a process used between two snapshots.
type Usage struct {
	// CPU is the processor use in percent; -1 when unknown (the first
	// snapshot of a process).
	CPU float64
	// IO is the bytes per second read and written; -1 when unknown.
	IO float64
}

// Rates computes each process's usage between prev and cur. With prev nil
// (the first snapshot) every value is unknown.
func Rates(prev, cur *Snapshot, mode CPUMode) map[ID]Usage {
	out := make(map[ID]Usage, len(cur.Procs))
	if prev == nil {
		for _, p := range cur.Procs {
			out[p.ID] = Usage{CPU: -1, IO: -1}
		}
		return out
	}
	dt := cur.Time.Sub(prev.Time).Seconds()
	before := make(map[ID]*Proc, len(prev.Procs))
	for i := range prev.Procs {
		before[prev.Procs[i].ID] = &prev.Procs[i]
	}
	cpus := float64(max(cur.CPUs, 1))
	limit := 100.0
	if mode == CPUPerCore {
		limit *= cpus
	}
	for _, p := range cur.Procs {
		u := Usage{CPU: -1, IO: -1}
		if dt > 0 {
			var dCPU int64
			var dIO uint64
			known := true
			if q, ok := before[p.ID]; ok {
				dCPU = max(p.CPU-q.CPU, 0) // counters do not go back; be safe
				if p.IO >= q.IO {
					dIO = p.IO - q.IO
				}
			} else if prev.Stamp != 0 && p.Created >= prev.Stamp {
				// Started between the snapshots: all it used is new.
				dCPU, dIO = p.CPU, p.IO
			} else {
				known = false
			}
			if known {
				cpu := float64(dCPU) / 1e7 / dt * 100
				if mode == CPUTotal {
					cpu /= cpus
				}
				u = Usage{CPU: min(cpu, limit), IO: float64(dIO) / dt}
			}
		}
		out[p.ID] = u
	}
	return out
}

// SystemCPU is the share of all processors in use between prev and cur, in
// percent; -1 when unknown.
func SystemCPU(prev, cur *Snapshot) float64 {
	if prev == nil {
		return -1
	}
	idle, busy := cur.SysIdle-prev.SysIdle, cur.SysBusy-prev.SysBusy
	if idle < 0 || busy < 0 || idle+busy <= 0 {
		return -1
	}
	return min(100, float64(busy)*100/float64(idle+busy))
}

// MemoryUsed is the share of physical memory in use, in percent.
func (s *Snapshot) MemoryUsed() float64 {
	if s.MemTotal == 0 || s.MemAvail > s.MemTotal {
		return 0
	}
	return float64(s.MemTotal-s.MemAvail) * 100 / float64(s.MemTotal)
}

// byPID indexes procs by PID.
func byPID(procs []Proc) map[uint32]*Proc {
	m := make(map[uint32]*Proc, len(procs))
	for i := range procs {
		m[procs[i].PID] = &procs[i]
	}
	return m
}

// parentOf returns p's parent: the process with p.Parent as PID, created
// before p. A parent that exited may have had its PID reused by a newer,
// unrelated process, which is not taken for the parent.
func parentOf(idx map[uint32]*Proc, p *Proc) *Proc {
	if p.Parent == 0 || p.Parent == p.PID {
		return nil
	}
	q, ok := idx[p.Parent]
	if !ok || q.Created > p.Created {
		return nil
	}
	return q
}

// ParentOf returns p's parent in procs (see parentOf), or false.
func ParentOf(procs []Proc, p Proc) (Proc, bool) {
	q := parentOf(byPID(procs), &p)
	if q == nil {
		return Proc{}, false
	}
	return *q, true
}

// GroupRoots returns the root of each process's group, as Task Manager
// groups processes under an app: processes of a packaged app (pkg not
// empty) under the oldest one of the package; other processes under the
// topmost ancestor reached through parents with the same executable path
// (a browser's child processes under its main process; a node.exe started
// by an editor stays apart). path and pkg return "" when unknown; a
// process without a path is a group of its own.
func GroupRoots(procs []Proc, path, pkg func(ID) string) map[ID]ID {
	idx := byPID(procs)
	out := make(map[ID]ID, len(procs))
	pkgRoot := map[string]ID{}
	for _, p := range procs {
		if k := pkg(p.ID); k != "" {
			if r, ok := pkgRoot[k]; !ok || p.Created < r.Created || p.Created == r.Created && p.PID < r.PID {
				pkgRoot[k] = p.ID
			}
		}
	}
	for i := range procs {
		p := &procs[i]
		if k := pkg(p.ID); k != "" {
			out[p.ID] = pkgRoot[k]
			continue
		}
		root := p
		pp := path(p.ID)
		for range len(procs) { // bounded: a cycle cannot loop forever
			q := parentOf(idx, root)
			if pp == "" || q == nil || !strings.EqualFold(path(q.ID), pp) || pkg(q.ID) != "" {
				break
			}
			root = q
		}
		out[p.ID] = root.ID
	}
	return out
}

// Tree returns root followed by its descendants in procs, level by level:
// the order in which KillTree ends them (the root first, so that it cannot
// start new children). It is empty when root is not in procs.
func Tree(procs []Proc, root ID) []ID {
	idx := byPID(procs)
	if r, ok := idx[root.PID]; !ok || r.ID != root {
		return nil
	}
	children := map[uint32][]ID{}
	for i := range procs {
		p := &procs[i]
		if q := parentOf(idx, p); q != nil {
			children[q.PID] = append(children[q.PID], p.ID)
		}
	}
	for _, c := range children {
		slices.SortFunc(c, func(a, b ID) int { return int(int64(a.PID) - int64(b.PID)) })
	}
	out := []ID{root}
	seen := map[ID]bool{root: true}
	for i := 0; i < len(out); i++ {
		for _, c := range children[out[i].PID] {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// KillReport is the outcome of ending a process tree.
type KillReport struct {
	Ended      []ID
	Gone       []ID // exited already
	NeedsAdmin []ID
	Protected  []ID // refused: critical, kernel, golauncher itself, PPL
	Failed     []ID // other errors
}

// Add files the outcome err of ending id.
func (r *KillReport) Add(id ID, err error) {
	switch {
	case err == nil:
		r.Ended = append(r.Ended, id)
	case errors.Is(err, ErrGone):
		r.Gone = append(r.Gone, id)
	case errors.Is(err, ErrNeedsAdmin):
		r.NeedsAdmin = append(r.NeedsAdmin, id)
	case errors.Is(err, ErrProtected):
		r.Protected = append(r.Protected, id)
	default:
		r.Failed = append(r.Failed, id)
	}
}

// Class is what may be done to a process (docs/process-manager.md §5.3).
type Class int

const (
	// ClassNormal processes can be ended.
	ClassNormal Class = iota
	// ClassKernel is the idle process, System and the kernel's pseudo
	// processes: no end action.
	ClassKernel
	// ClassCritical is a critical system process: ending it crashes Windows
	// or signs out. No end action.
	ClassCritical
	// ClassSelf is golauncher itself: no end action.
	ClassSelf
	// ClassShell is the explorer.exe owning the taskbar: it is restarted
	// rather than ended.
	ClassShell
	// ClassComponent is a Windows component that can be ended, with a
	// warning about what stops working (Facts.Note).
	ClassComponent
)

// Endable reports whether processes of class c may be ended.
func (c Class) Endable() bool { return c == ClassNormal || c == ClassComponent }

// Facts are what Classify looks at.
type Facts struct {
	ID
	Name string // executable name
	Path string // full path; "" when unknown
	// Critical is IsProcessCritical.
	Critical bool
	// Shell is set when the process owns the taskbar or the desktop window.
	Shell bool
	// Self is the PID of this program.
	Self uint32
	// SystemRoot is %SystemRoot%, e.g. C:\Windows.
	SystemRoot string
}

// pseudoNames are the kernel's pseudo processes, which have no executable.
var pseudoNames = []string{"system", "registry", "memory compression", "secure system", "system idle process", "lsaiso.exe"}

// criticalNames are critical when they run from System32.
var criticalNames = []string{"csrss.exe", "smss.exe", "wininit.exe", "winlogon.exe", "services.exe", "lsass.exe"}

// componentNotes are Windows components whose end the user is warned
// about, by executable name; the value names the warning.
var componentNotes = map[string]string{
	"svchost.exe":                 "svchost",
	"dwm.exe":                     "dwm",
	"ctfmon.exe":                  "ctfmon",
	"sihost.exe":                  "sihost",
	"startmenuexperiencehost.exe": "startmenu",
	"shellexperiencehost.exe":     "shellexperience",
	"searchhost.exe":              "search",
	"textinputhost.exe":           "textinput",
	"audiodg.exe":                 "audio",
	"spoolsv.exe":                 "spooler",
}

// Classify tells what may be done to the process described by f, and for
// ClassComponent which warning applies ("svchost", "dwm", …; "" for none).
// Critical names count only from System32 (or when the path cannot be
// read, which a same-named program of the user's never prevents), so a
// look-alike elsewhere is not protected.
func Classify(f Facts) (Class, string) {
	name := strings.ToLower(f.Name)
	switch {
	case f.PID == 0 || f.PID == 4 || f.Path == "" && slices.Contains(pseudoNames, name):
		return ClassKernel, ""
	case f.Self != 0 && f.PID == f.Self:
		return ClassSelf, ""
	case f.Critical:
		return ClassCritical, ""
	}
	inSystem32 := f.Path == "" || f.SystemRoot != "" && strings.EqualFold(winDir(f.Path), winClean(f.SystemRoot)+`\System32`)
	if slices.Contains(criticalNames, name) && inSystem32 {
		return ClassCritical, ""
	}
	if name == "lsaiso.exe" && inSystem32 {
		return ClassKernel, ""
	}
	if f.Shell && name == "explorer.exe" {
		return ClassShell, ""
	}
	if note, ok := componentNotes[name]; ok && note != "" && inWindows(f) {
		return ClassComponent, note
	}
	return ClassNormal, ""
}

// inWindows reports whether f runs from under %SystemRoot% (or its path is
// unknown).
func inWindows(f Facts) bool {
	if f.Path == "" || f.SystemRoot == "" {
		return true
	}
	root := strings.ToLower(winClean(f.SystemRoot)) + `\`
	return strings.HasPrefix(strings.ToLower(winClean(f.Path)), root)
}

// winClean turns the slashes of a Windows path into backslashes and drops
// trailing ones. Paths are handled as Windows paths on every platform, so
// that fixtures classify the same everywhere.
func winClean(p string) string {
	return strings.TrimRight(strings.ReplaceAll(p, "/", `\`), `\`)
}

// winDir returns the directory of a Windows path ("" when it has none).
func winDir(p string) string {
	p = winClean(p)
	if i := strings.LastIndexByte(p, '\\'); i >= 0 {
		return p[:i]
	}
	return ""
}

// --- Command lines --------------------------------------------------------

// secretFlag matches option names whose value is a secret.
var secretFlag = regexp.MustCompile(`(?i)^(?:[a-z0-9]+[-_])*(?:pass|password|passwd|pwd|token|secret|key|api[-_]?key|apikey|auth|credentials?)$`)

// urlCreds matches the user:password@ of a URL.
var urlCreds = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s:@"]+:[^/\s@"]+@`)

// Redacted replaces secrets is a command line by "•••".
const Redacted = "•••"

// RedactCommandLine hides the values of options that look like secrets
// (--password=…, --token …, --api-key:…, -p …, /password:…) and the
// user:password@ of URLs. It keeps the rest as typed.
func RedactCommandLine(s string) string {
	s = urlCreds.ReplaceAllString(s, "${1}"+Redacted+"@")
	toks := tokenize(s)
	var b strings.Builder
	last := 0
	hideNext := false
	for _, t := range toks {
		text := s[t.start:t.end]
		if hideNext {
			hideNext = false
			if !strings.HasPrefix(unquote(text), "-") {
				b.WriteString(s[last:t.start])
				b.WriteString(Redacted)
				last = t.end
				continue
			}
		}
		word := unquote(text)
		name, hasValue, sep := optionName(word)
		if name == "" {
			continue
		}
		if !(secretFlag.MatchString(name) || name == "p" && strings.HasPrefix(word, "-") && !strings.HasPrefix(word, "--")) {
			continue
		}
		if !hasValue {
			hideNext = true
			continue
		}
		// Hide what follows the separator, inside the token.
		i := strings.Index(text, sep)
		b.WriteString(s[last : t.start+i+1])
		b.WriteString(Redacted)
		if strings.HasPrefix(text, `"`) && strings.HasSuffix(text, `"`) && len(text) > 1 {
			b.WriteString(`"`)
		}
		last = t.end
	}
	b.WriteString(s[last:])
	return b.String()
}

// optionName returns the name of an option word ("--db-password=x" ->
// "db-password", "=", true); "" when the word is not an option.
func optionName(word string) (name string, hasValue bool, sep string) {
	var rest string
	switch {
	case strings.HasPrefix(word, "--"):
		rest = word[2:]
	case strings.HasPrefix(word, "-"), strings.HasPrefix(word, "/") && len(word) > 1 && !strings.Contains(word[1:], "/"):
		rest = word[1:]
	default:
		return "", false, ""
	}
	if i := strings.IndexAny(rest, "=:"); i >= 0 {
		return rest[:i], true, rest[i : i+1]
	}
	return rest, false, ""
}

func unquote(s string) string {
	return strings.ReplaceAll(s, `"`, "")
}

type span struct{ start, end int }

// tokenize splits a command line at spaces outside double quotes.
func tokenize(s string) []span {
	var out []span
	start, quoted := -1, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			quoted = !quoted
			if start < 0 {
				start = i
			}
		case (c == ' ' || c == '\t') && !quoted:
			if start >= 0 {
				out = append(out, span{start, i})
				start = -1
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		out = append(out, span{start, len(s)})
	}
	return out
}

// --- The elevated helper ---------------------------------------------------

// HelperCommand is the hidden subcommand of golauncher.exe that ends one
// process (or a process tree) with administrator rights: the launcher runs
// it through a UAC prompt when ending a process needs them
// (docs/process-manager.md §5.5).
const HelperCommand = "kill"

// HelperRequest is what the helper is asked to end.
type HelperRequest struct {
	ID   ID
	Tree bool
}

// Args returns the command line arguments of the helper for r, after the
// program name.
func (r HelperRequest) Args() []string {
	args := []string{HelperCommand, "--pid", strconv.FormatUint(uint64(r.ID.PID), 10), "--created", strconv.FormatInt(r.ID.Created, 10)}
	if r.Tree {
		args = append(args, "--tree")
	}
	return args
}

// Exit codes of the helper.
const (
	ExitOK         = 0
	ExitFailed     = 1
	ExitUsage      = 2
	ExitGone       = 3
	ExitProtected  = 4
	ExitNeedsAdmin = 5
	// ExitPartial: a tree was ended in part.
	ExitPartial = 6
)

// ParseHelperArgs parses the helper's arguments strictly: "kill", then
// --pid with a decimal PID above 4, --created with a positive decimal
// FILETIME, each exactly once, and optionally --tree. Nothing else is
// accepted: the helper runs with administrator rights.
func ParseHelperArgs(args []string) (HelperRequest, error) {
	var r HelperRequest
	if len(args) == 0 || args[0] != HelperCommand {
		return r, errors.New("process: not the helper command")
	}
	seen := map[string]bool{}
	for i := 1; i < len(args); i++ {
		a := args[i]
		if seen[a] {
			return r, fmt.Errorf("process: %s given twice", a)
		}
		seen[a] = true
		switch a {
		case "--tree":
			r.Tree = true
		case "--pid", "--created":
			if i+1 >= len(args) {
				return r, fmt.Errorf("process: %s needs a value", a)
			}
			i++
			v := args[i]
			if v == "" || len(v) > 20 || strings.TrimLeft(v, "0123456789") != "" {
				return r, fmt.Errorf("process: %s: %q is not a decimal number", a, v)
			}
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return r, fmt.Errorf("process: %s: %w", a, err)
			}
			if a == "--pid" {
				if n <= 4 || n > 1<<32-1 {
					return r, fmt.Errorf("process: --pid %d is not allowed", n)
				}
				r.ID.PID = uint32(n)
			} else {
				if n == 0 || n > 1<<63-1 {
					return r, fmt.Errorf("process: --created %d is not allowed", n)
				}
				r.ID.Created = int64(n)
			}
		default:
			return r, fmt.Errorf("process: unknown argument %q", a)
		}
	}
	if !seen["--pid"] || !seen["--created"] {
		return r, errors.New("process: --pid and --created are required")
	}
	return r, nil
}

// ExitCode is the helper's exit code for the outcome err of ending one
// process.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, ErrGone):
		return ExitGone
	case errors.Is(err, ErrProtected):
		return ExitProtected
	case errors.Is(err, ErrNeedsAdmin):
		return ExitNeedsAdmin
	}
	return ExitFailed
}

// ExitError turns a helper exit code back into the error it stands for.
func ExitError(code int) error {
	switch code {
	case ExitOK:
		return nil
	case ExitGone:
		return ErrGone
	case ExitProtected:
		return ErrProtected
	case ExitNeedsAdmin:
		return ErrNeedsAdmin
	case ExitUsage:
		return errors.New("process: the helper refused its arguments")
	case ExitPartial:
		return errors.New("process: some processes of the tree could not be ended")
	}
	return fmt.Errorf("process: the helper failed (exit code %d)", code)
}

// helperMain runs the helper with kill and killTree (the real ones, or
// fakes in tests) and returns its exit code. Classification and the
// creation time check happen inside kill, on the handle that ends the
// process.
func helperMain(args []string, kill func(ID) error, killTree func(ID) KillReport) int {
	r, err := ParseHelperArgs(args)
	if err != nil {
		return ExitUsage
	}
	if !r.Tree {
		return ExitCode(kill(r.ID))
	}
	rep := killTree(r.ID)
	switch {
	case len(rep.Ended) == 0 && len(rep.Protected) > 0:
		return ExitProtected
	case len(rep.Ended) == 0 && len(rep.NeedsAdmin) > 0:
		return ExitNeedsAdmin
	case len(rep.Ended) == 0 && len(rep.Gone) > 0 && len(rep.Failed) == 0:
		return ExitGone
	case len(rep.Ended) == 0:
		return ExitFailed
	case len(rep.NeedsAdmin)+len(rep.Protected)+len(rep.Failed) > 0:
		return ExitPartial
	}
	return ExitOK
}
