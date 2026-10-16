package processes

import (
	"cmp"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/mozillazg/go-pinyin"

	"github.com/eziq1/golauncher/internal/winapi/process"
)

// Sections of the list (docs/process-manager.md §3.1), in display order.
const (
	secApps = iota
	secBackground
	secWindows
)

// proc is a process of the current sample with what is known about it.
type proc struct {
	process.Proc
	info    *pinfo
	usage   process.Usage
	windows []process.Window // app windows (not the shell's)
	shell   bool             // owns the taskbar or the desktop
	class   process.Class
	note    string // warning of a ClassComponent
	ports   []string
	task    bool // a background task of golauncher (a child of it)
}

// pinfo is the cached on-demand data of a process instance.
type pinfo struct {
	process.Info
	pinyin  string // pinyin of the display name, "" when it has no Chinese
	initial string // pinyin initials
	cmdRead bool
	cmd     string
	cmdErr  error
}

// display is the name shown for p: its version description, else its
// executable name without ".exe".
func (p *proc) display() string {
	if d := strings.TrimSpace(p.info.Description); d != "" {
		return d
	}
	return strings.TrimSuffix(strings.TrimSuffix(p.Name, ".exe"), ".EXE")
}

func (p *proc) hung() bool {
	return slices.ContainsFunc(p.windows, func(w process.Window) bool { return w.Hung })
}

// row is a line of the list: an app with its processes, or one process.
type row struct {
	root    *proc
	members []*proc // the root first, then the others in tree order
	depth   map[process.ID]int
	section int
	cpu     float64 // -1 when unknown
	io      float64
	mem     uint64
}

func (r *row) id() string { return itemID(r.root.ID) }

func itemID(id process.ID) string {
	return "p:" + strconv.FormatUint(uint64(id.PID), 10) + ":" + strconv.FormatInt(id.Created, 10)
}

func (r *row) windows() []process.Window {
	var out []process.Window
	for _, m := range r.members {
		out = append(out, m.windows...)
	}
	return out
}

func (r *row) hung() bool { return slices.ContainsFunc(r.members, (*proc).hung) }

func (r *row) ids() []process.ID {
	out := make([]process.ID, len(r.members))
	for i, m := range r.members {
		out[i] = m.ID
	}
	return out
}

// endable reports whether every member may be ended and the root is not
// the shell (which is restarted instead).
func (r *row) endable() bool {
	return !slices.ContainsFunc(r.members, func(m *proc) bool { return !m.class.Endable() })
}

// model is everything built from one sample, before search and sorting.
type model struct {
	snap   *process.Snapshot
	cpu    float64 // system CPU %, -1 unknown
	procs  map[process.ID]*proc
	rows   []*row
	byRoot map[process.ID]*row
	byPID  map[uint32]*proc
}

// buildModel groups the processes of the sample.
func (p *Plugin) buildModel(st *sampleState, group bool) *model {
	snap := st.cur
	m := &model{snap: snap, cpu: process.SystemCPU(st.prev, snap), procs: map[process.ID]*proc{},
		byRoot: map[process.ID]*row{}, byPID: map[uint32]*proc{}}
	usage := process.Rates(st.prev, snap, p.cpuMode())
	wins := map[uint32][]process.Window{}
	shells := map[uint32]bool{}
	for _, w := range st.windows {
		if w.Shell {
			shells[w.PID] = true
		} else {
			wins[w.PID] = append(wins[w.PID], w)
		}
	}
	self := p.b.Self()
	sysRoot := p.b.SystemRoot()
	var procs []process.Proc
	for _, sp := range snap.Procs {
		if sp.PID == 0 {
			continue // System Idle Process
		}
		procs = append(procs, sp)
		pr := &proc{Proc: sp, info: st.info[sp.ID], usage: usage[sp.ID], windows: wins[sp.PID], shell: shells[sp.PID], ports: st.ports[sp.PID]}
		if pr.info == nil {
			pr.info = &pinfo{}
		}
		pr.class, pr.note = process.Classify(process.Facts{ID: sp.ID, Name: sp.Name, Path: pr.info.Path, Critical: pr.info.Critical,
			Shell: pr.shell, Self: self, SystemRoot: sysRoot})
		m.procs[sp.ID] = pr
		m.byPID[sp.PID] = pr
	}
	for _, pr := range m.procs {
		if parent, ok := process.ParentOf(procs, pr.Proc); ok && parent.PID == self && self != 0 {
			pr.task = true
		}
	}
	path := func(id process.ID) string {
		if pr := m.procs[id]; pr != nil {
			return pr.info.Path
		}
		return ""
	}
	pkg := func(id process.ID) string {
		if pr := m.procs[id]; pr != nil {
			return pr.info.Package
		}
		return ""
	}
	roots := map[process.ID]process.ID{}
	if group {
		roots = process.GroupRoots(procs, path, pkg)
	}
	for _, sp := range procs {
		rootID := sp.ID
		if r, ok := roots[sp.ID]; ok && group {
			rootID = r
		}
		// golauncher and the shell stay rows of their own: they have
		// different actions from their group.
		if pr := m.procs[sp.ID]; pr.class == process.ClassSelf || pr.class == process.ClassShell || pr.task {
			rootID = sp.ID
		}
		r := m.byRoot[rootID]
		if r == nil {
			r = &row{root: m.procs[rootID], depth: map[process.ID]int{}}
			m.byRoot[rootID] = r
			m.rows = append(m.rows, r)
		}
		if pr := m.procs[sp.ID]; pr != r.root {
			r.members = append(r.members, pr)
		}
	}
	for _, r := range m.rows {
		p.finishRow(m, r, procs)
	}
	return m
}

// finishRow orders a row's members as a tree, sums its usage and picks its
// section.
func (p *Plugin) finishRow(m *model, r *row, procs []process.Proc) {
	members := append([]*proc{r.root}, r.members...)
	in := map[process.ID]*proc{}
	for _, x := range members {
		in[x.ID] = x
	}
	var ordered []*proc
	seen := map[process.ID]bool{}
	var walk func(x *proc, depth int)
	walk = func(x *proc, depth int) {
		if seen[x.ID] {
			return
		}
		seen[x.ID] = true
		ordered = append(ordered, x)
		r.depth[x.ID] = depth
		var kids []*proc
		for _, y := range members {
			if parent, ok := process.ParentOf(procs, y.Proc); ok && parent.ID == x.ID && !seen[y.ID] {
				kids = append(kids, y)
			}
		}
		slices.SortFunc(kids, func(a, b *proc) int { return cmp.Compare(a.PID, b.PID) })
		for _, k := range kids {
			walk(k, depth+1)
		}
	}
	walk(r.root, 0)
	rest := slices.DeleteFunc(slices.Clone(members), func(x *proc) bool { return seen[x.ID] })
	slices.SortFunc(rest, func(a, b *proc) int { return cmp.Compare(a.PID, b.PID) })
	for _, x := range rest {
		walk(x, 1)
	}
	r.members = ordered
	r.cpu, r.io = 0, 0
	known := false
	for _, x := range r.members {
		if x.usage.CPU >= 0 {
			r.cpu += x.usage.CPU
			known = true
		}
		if x.usage.IO > 0 {
			r.io += x.usage.IO
		}
		r.mem += x.PrivateWorkingSet
	}
	if !known {
		r.cpu = -1
	}
	r.section = p.sectionOf(m, r)
}

// sectionOf is the section of a row (docs/process-manager.md §3.1).
func (p *Plugin) sectionOf(m *model, r *row) int {
	if len(r.windows()) > 0 || r.root.class == process.ClassShell {
		return secApps
	}
	root := r.root
	if root.class == process.ClassKernel || root.class == process.ClassCritical || root.Session == 0 {
		return secWindows
	}
	// Programs of Windows itself, or of system accounts.
	if inDir(root.info.Path, p.b.SystemRoot()) || root.info.SystemAccount || root.info.Denied && root.info.Path == "" {
		return secWindows
	}
	return secBackground
}

// inDir reports whether path is under dir.
func inDir(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && !strings.HasPrefix(rel, "..")
}

// --- Sorting -----------------------------------------------------------------

// Sort orders.
const (
	sortCPU    = "cpu"
	sortMemory = "memory"
	sortIO     = "io"
	sortName   = "name"
)

var sortOrders = []string{sortCPU, sortMemory, sortIO, sortName}

// sortKey is the value a row is sorted by (larger first), or its name.
func sortKey(r *row, order string) float64 {
	switch order {
	case sortMemory:
		return float64(r.mem)
	case sortIO:
		return r.io
	}
	return r.cpu
}

// sortRows sorts rows within their sections. Rows whose values differ by
// less than 10% keep their previous order (prev: rank by item ID), so that
// a list refreshed every second does not jump around.
func sortRows(rows []*row, order string, prev map[string]int) {
	slices.SortStableFunc(rows, func(a, b *row) int {
		if c := cmp.Compare(a.section, b.section); c != 0 {
			return c
		}
		if order == sortName {
			return cmp.Or(cmp.Compare(strings.ToLower(a.root.display()), strings.ToLower(b.root.display())), cmp.Compare(a.root.PID, b.root.PID))
		}
		ka, kb := sortKey(a, order), sortKey(b, order)
		if !close10(ka, kb) {
			return cmp.Compare(kb, ka)
		}
		ra, oka := prev[a.id()]
		rb, okb := prev[b.id()]
		if oka && okb {
			return cmp.Compare(ra, rb)
		}
		return cmp.Or(cmp.Compare(kb, ka), cmp.Compare(strings.ToLower(a.root.display()), strings.ToLower(b.root.display())), cmp.Compare(a.root.PID, b.root.PID))
	})
}

// close10 reports whether a and b differ by less than 10% of the larger.
func close10(a, b float64) bool {
	hi := max(a, b)
	if hi <= 0 {
		return a == b
	}
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < hi*0.1
}

// --- Search -----------------------------------------------------------------

// matchRow scores a row for a search (0: no match). Digits find a PID
// exactly first (score 2); names, executable names, window titles,
// packages and the pinyin of Chinese names match by prefix or substring.
func matchRow(r *row, q string) float64 {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return 1
	}
	if isDigits(q) {
		for _, m := range r.members {
			if strconv.FormatUint(uint64(m.PID), 10) == q {
				return 2
			}
		}
	}
	best := 0.0
	try := func(s string, prefix, contains float64) {
		s = strings.ToLower(s)
		switch {
		case s == "":
		case strings.HasPrefix(s, q):
			best = max(best, prefix)
		case wordPrefix(s, q):
			best = max(best, prefix*0.95)
		case strings.Contains(s, q):
			best = max(best, contains)
		}
	}
	root := r.root
	try(root.display(), 1, 0.8)
	compact := strings.ReplaceAll(q, " ", "")
	if root.info.pinyin != "" {
		if strings.HasPrefix(strings.ReplaceAll(root.info.pinyin, " ", ""), compact) || strings.HasPrefix(root.info.initial, compact) {
			best = max(best, 0.75)
		}
	}
	for _, m := range r.members {
		try(m.Name, 0.9, 0.7)
		try(m.display(), 0.85, 0.65)
		try(m.info.Package, 0.6, 0.5)
		for _, w := range m.windows {
			try(w.Title, 0.6, 0.55)
		}
	}
	return best
}

func isDigits(s string) bool {
	return s != "" && strings.TrimLeft(s, "0123456789") == ""
}

// wordPrefix reports whether a word of s starts with q.
func wordPrefix(s, q string) bool {
	for w := range strings.FieldsFuncSeq(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if strings.HasPrefix(w, q) {
			return true
		}
	}
	return false
}

var pinyinArgs = pinyin.NewArgs()

// pinyinOf spells the Chinese characters of s in pinyin ("记事本" ->
// "ji shi ben", initials "jsb"); "" when s has none.
func pinyinOf(s string) (full, initials string) {
	var words []string
	han := false
	var run strings.Builder
	flush := func() {
		if run.Len() > 0 {
			words = append(words, strings.ToLower(run.String()))
			run.Reset()
		}
	}
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			if py := pinyin.SinglePinyin(r, pinyinArgs); len(py) > 0 {
				flush()
				words = append(words, py[0])
				han = true
				continue
			}
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			run.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	if !han {
		return "", ""
	}
	var ini strings.Builder
	for _, w := range words {
		ini.WriteString(w[:1])
	}
	return strings.Join(words, " "), ini.String()
}

// sortBy sorts rows stably with cmp.
func sortBy(rows []*row, f func(a, b *row) int) { slices.SortStableFunc(rows, f) }

// byCPU orders rows by CPU use, busiest first, then by PID.
func byCPU(a, b *row) int {
	return cmp.Or(cmp.Compare(b.cpu, a.cpu), cmp.Compare(a.root.PID, b.root.PID))
}

func slicesClone(rows []*row) []*row { return slices.Clone(rows) }
