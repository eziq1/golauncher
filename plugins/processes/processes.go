// Package processes is the built-in process manager (docs/process-manager.md).
//
// "ps " (with the space), "kill" and "进程" in the root search list the
// running apps and processes with their CPU and memory use, grouped by app
// as in Task Manager; the Processes command opens a live view with details.
// Processes can be switched to, ended (End task asks an app to close; Force
// end and End process tree ask for confirmation), and elevated processes
// are ended through a one-off UAC prompt. The list is sampled only while it
// shows: once a second, refreshed with Host.Invalidate (root search) and
// Host.Render (views). Critical system processes and golauncher itself
// have no end action.
package processes

import (
	"context"
	"embed"
	"encoding/json/v2"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/eziq1/golauncher/internal/winapi/process"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// ID is the plugin's manifest ID.
const ID = "builtin.processes"

// Commands and views.
const (
	cmdProcesses       = "processes"
	cmdHung            = "hung"
	cmdRestartExplorer = "restart-explorer"
	viewGroup          = "group"
)

// Preferences.
const (
	prefPrefixes       = "prefixes"
	prefInterval       = "interval"
	prefCPUMode        = "cpuMode"
	prefGroupByApp     = "groupByApp"
	prefShowWindows    = "showWindowsProcesses"
	prefDefaultSort    = "defaultSort"
	prefCloseTimeout   = "closeTimeout"
	prefRedact         = "redactCommandLine"
	prefForegroundQuit = "foregroundQuit"
)

// KV keys.
const (
	kvSort  = "sort"
	kvGroup = "group"
)

// Callbacks.
const (
	cbEndTask    = "end-task"
	cbForce      = "force-end"
	cbTree       = "end-tree"
	cbElevate    = "end-elevated"
	cbSwitch     = "switch"
	cbCopyCmd    = "copy-command-line"
	cbSort       = "sort"
	cbGroup      = "toggle-group"
	cbRefresh    = "refresh"
	cbRestart    = "restart-explorer"
	cbEndAllHung = "end-all-hung"
)

const (
	// rootRows bounds the groups listed in the root search.
	rootRows = 8
	// iconSize is the size of the drawn process icons.
	iconSize = 32
)

var defaultPrefixes = []string{"ps", "kill", "进程"}

// killPrefix is the prefix whose Enter ends the task.
const killPrefix = "kill"

//go:embed locales/*.json
var locales embed.FS

// Options configure the plugin; tests and fake environments replace them.
type Options struct {
	// Backend reads and acts on the system (default: System).
	Backend Backend
	// After returns a channel that receives once d has passed (default
	// time.After): the waits between samples and the release of the cache.
	After func(d time.Duration) <-chan time.Time
	// Sync takes both samples in the first call and never refreshes (glctl's
	// fake environment: deterministic output).
	Sync bool
}

// viewState is an open view: what to render for it.
type viewState struct {
	id    string // cmdProcesses, cmdHung or viewGroup
	query string
	root  process.ID // viewGroup
}

// Plugin is the process manager plugin.
type Plugin struct {
	opts Options
	b    Backend

	h       plugin.Host
	unsub   func()
	wg      sync.WaitGroup
	running atomic.Int32 // sampling loops running, for tests
	bg      context.Context
	stopBG  context.CancelFunc

	mu         sync.Mutex
	closed     bool
	st         sampleState
	samples    int
	stop       chan struct{} // closes to stop the loop; nil when stopped
	idle       chan struct{} // closes to cancel the cache release
	rootActive bool
	pulled     bool
	misses     int
	views      map[string]viewState
	needsAdmin map[process.ID]bool // ending failed for lack of rights
	fg         *fgCache
	icons      map[string]string // exe path -> PNG ("" while drawing or failed)
}

var (
	_ plugin.Plugin          = (*Plugin)(nil)
	_ plugin.RootProvider    = (*Plugin)(nil)
	_ plugin.ContextProvider = (*Plugin)(nil)
	_ plugin.ViewProvider    = (*Plugin)(nil)
	_ plugin.ActionHandler   = (*Plugin)(nil)
	_ plugin.LocaleProvider  = (*Plugin)(nil)
	_ plugin.ViewCloser      = (*Plugin)(nil)
)

// New returns the plugin for the real system.
func New() *Plugin { return NewWithOptions(Options{}) }

// NewFake returns the plugin for a fake environment, reading the fixture
// file (testdata/fixtures/processes.json; "" for no processes).
func NewFake(fixture string) *Plugin {
	return NewWithOptions(Options{Backend: LoadFixture(fixture), Sync: true})
}

// NewDryRun returns the plugin reading the real system without ending,
// closing or elevating anything (glctl --real without --exec).
func NewDryRun() *Plugin { return NewWithOptions(Options{Backend: &DryRun{}}) }

// NewWithOptions returns the plugin with o.
func NewWithOptions(o Options) *Plugin {
	if o.Backend == nil {
		o.Backend = &System{}
	}
	if o.After == nil {
		o.After = time.After
	}
	bg, stop := context.WithCancel(context.Background())
	return &Plugin{opts: o, b: o.Backend, views: map[string]viewState{}, needsAdmin: map[process.ID]bool{},
		icons: map[string]string{}, bg: bg, stopBG: stop}
}

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	opt := func(v, title string) plugin.Option { return plugin.Option{Value: v, Title: title} }
	mac := p.b.Mac()
	cpuDefault := "total"
	if mac {
		cpuDefault = "perCore" // as Activity Monitor
	}
	m := plugin.Manifest{
		ID:            ID,
		Name:          "%plugin.name%",
		Description:   "%plugin.description%",
		Version:       "1.0.0",
		Icon:          plugin.Icon{Name: "activity"},
		DefaultLocale: "en",
		Permissions:   []plugin.Permission{plugin.PermClipboard, plugin.PermStorage},
		Commands: []plugin.Command{
			{Name: cmdProcesses, Title: "%command.processes.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView,
				Icon: plugin.Icon{Name: "activity"}, Keywords: []string{"%command.processes.keywords%", "进程", "任务管理器", "rwglq", "jc"}},
			{Name: cmdHung, Title: "%command.hung.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView,
				Icon: plugin.Icon{Name: "circle-stop"}, Keywords: []string{"%command.hung.keywords%", "未响应", "卡死", "wxy"}},
			{Name: cmdRestartExplorer, Title: "%command.restartExplorer.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeNoView,
				Icon: plugin.Icon{Name: "rotate-cw"}, Keywords: []string{"%command.restartExplorer.keywords%", "任务栏", "资源管理器", "cqzyglq", "zyglq"},
				Destructive: true},
		},
		Preferences: []plugin.Preference{
			{Name: prefPrefixes, Type: plugin.PrefStringList, Title: "%pref.prefixes.title%", Description: "%pref.prefixes.description%",
				Default: plugin.MustPayload(defaultPrefixes)},
			{Name: prefInterval, Type: plugin.PrefSelect, Title: "%pref.interval.title%", Default: plugin.MustPayload("1"),
				Options: []plugin.Option{opt("1", "%pref.interval.1%"), opt("2", "%pref.interval.2%"), opt("5", "%pref.interval.5%")}},
			{Name: prefCPUMode, Type: plugin.PrefSelect, Title: "%pref.cpuMode.title%", Description: "%pref.cpuMode.description%",
				Default: plugin.MustPayload(cpuDefault), Options: []plugin.Option{opt("total", "%pref.cpuMode.total%"), opt("perCore", "%pref.cpuMode.perCore%")}},
			{Name: prefGroupByApp, Type: plugin.PrefBool, Title: "%pref.groupByApp.title%", Default: plugin.MustPayload(true)},
			{Name: prefShowWindows, Type: plugin.PrefBool, Title: "%pref.showWindows.title%", Default: plugin.MustPayload(true)},
			{Name: prefDefaultSort, Type: plugin.PrefSelect, Title: "%pref.defaultSort.title%", Default: plugin.MustPayload(sortCPU),
				Options: []plugin.Option{opt(sortCPU, "%sort.cpu%"), opt(sortMemory, "%sort.memory%"), opt(sortIO, "%sort.io%"), opt(sortName, "%sort.name%")}},
			{Name: prefCloseTimeout, Type: plugin.PrefNumber, Title: "%pref.closeTimeout.title%", Description: "%pref.closeTimeout.description%",
				Default: plugin.MustPayload(5)},
			{Name: prefRedact, Type: plugin.PrefBool, Title: "%pref.redact.title%", Description: "%pref.redact.description%",
				Default: plugin.MustPayload(true)},
			{Name: prefForegroundQuit, Type: plugin.PrefBool, Title: "%pref.foregroundQuit.title%", Description: "%pref.foregroundQuit.description%",
				Default: plugin.MustPayload(true)},
		},
	}
	if mac {
		// macOS tells no window that is not responding, and has no Windows
		// Explorer to restart.
		m.Commands = slices.DeleteFunc(m.Commands, func(c plugin.Command) bool {
			return c.Name == cmdHung || c.Name == cmdRestartExplorer
		})
	}
	return m
}

// Locales implements plugin.LocaleProvider.
func (p *Plugin) Locales() fs.FS {
	sub, err := fs.Sub(locales, "locales")
	if err != nil {
		panic(err)
	}
	return sub
}

// Init implements plugin.Plugin. Nothing is sampled until the list shows.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.h = h
	hidden := h.Events().Subscribe(plugin.EventWindowHidden, p.windowHidden)
	shown := h.Events().Subscribe(plugin.EventWindowShown, p.windowShown)
	p.unsub = func() { hidden(); shown() }
	if _, real := p.b.(interface{ realSystem() }); real && p.b.Mac() {
		p.goBackground(warm)
	}
	return nil
}

// Close implements plugin.Plugin: it stops sampling and waits for
// background work.
func (p *Plugin) Close() error {
	p.mu.Lock()
	p.closed = true
	p.stopLocked()
	p.cancelIdleLocked()
	p.mu.Unlock()
	p.stopBG()
	if p.unsub != nil {
		p.unsub()
	}
	p.wg.Wait()
	return nil
}

// goBackground runs fn with Host.Go, counted for Close.
func (p *Plugin) goBackground(fn func()) {
	p.wg.Add(1)
	p.h.Go(func() {
		defer p.wg.Done()
		fn()
	})
}

// --- Preferences --------------------------------------------------------------

func (p *Plugin) interval() time.Duration {
	n, err := strconv.Atoi(p.h.Preferences().String(prefInterval))
	if err != nil || n < 1 || n > 60 {
		n = 1
	}
	return time.Duration(n) * time.Second
}

func (p *Plugin) cpuMode() process.CPUMode {
	if p.h.Preferences().String(prefCPUMode) == "perCore" {
		return process.CPUPerCore
	}
	return process.CPUTotal
}

// grouping reports whether processes are grouped by app: the choice made
// in the action panel, else the preference.
func (p *Plugin) grouping() bool {
	if b, ok, err := p.h.Storage().Get(kvGroup); err == nil && ok {
		return string(b) == "true"
	}
	return p.h.Preferences().Bool(prefGroupByApp)
}

// sortOrder is the sort order: the choice made in the action panel, else
// the preference.
func (p *Plugin) sortOrder() string {
	if b, ok, err := p.h.Storage().Get(kvSort); err == nil && ok && validSort(string(b)) {
		return string(b)
	}
	if s := p.h.Preferences().String(prefDefaultSort); validSort(s) {
		return s
	}
	return sortCPU
}

func validSort(s string) bool {
	for _, o := range sortOrders {
		if o == s {
			return true
		}
	}
	return false
}

func (p *Plugin) closeTimeout() time.Duration {
	n := p.h.Preferences().Int(prefCloseTimeout)
	if n <= 0 || n > 120 {
		n = 5
	}
	return time.Duration(n) * time.Second
}

// cutPrefix returns the prefix that starts text and the rest. An ASCII
// prefix must be followed by a space ("ps" alone is someone looking for
// PowerShell or Photoshop: docs/process-manager.md §1.1); "kill" may also
// end the text; a prefix ending in a Chinese character may be followed by
// anything ("进程chrome").
func (p *Plugin) cutPrefix(text string) (prefix, rest string, ok bool) {
	text = strings.TrimLeft(text, " ")
	best := -1
	for _, pfx := range p.h.Preferences().Strings(prefPrefixes) {
		pfx = strings.TrimSpace(pfx)
		if pfx == "" || len(pfx) > len(text) || !strings.EqualFold(text[:len(pfx)], pfx) || len(pfx) <= best {
			continue
		}
		r := text[len(pfx):]
		last, _ := utf8.DecodeLastRuneInString(pfx)
		if last < utf8.RuneSelf {
			if r == "" && !strings.EqualFold(pfx, killPrefix) {
				continue
			}
			if r != "" && r[0] != ' ' {
				continue
			}
		}
		prefix, rest, ok, best = strings.ToLower(pfx), strings.TrimSpace(r), true, len(pfx)
	}
	return prefix, rest, ok
}

// --- Root search --------------------------------------------------------------

// RootItems implements plugin.RootProvider. Prefixed queries list
// processes (scored here); other text gets only the "Quit <foreground
// app>" item, which the host matches.
func (p *Plugin) RootItems(ctx context.Context, q plugin.Query) ([]plugin.Item, error) {
	prefix, rest, ok := p.cutPrefix(cmpOr(q.Raw, q.Text))
	if !ok {
		p.stopRoot()
		if strings.TrimSpace(q.Text) == "" || q.Context == nil {
			return nil, nil
		}
		return p.foregroundItems(q.Context, false), nil
	}
	p.rootPulled()
	m := p.current()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l := p.h.I18n()
	kill := prefix == killPrefix
	var items []plugin.Item
	if rest == "" && !kill {
		items = append(items, p.summaryItem(m))
	}
	var rows []*row
	if rest == "" {
		// The overview: the busiest, whatever their section.
		rows = slicesClone(m.rows)
		sortBy(rows, byCPU)
	} else {
		rows = p.visibleRows(m, rest)
	}
	for i, r := range rows {
		if i == rootRows {
			break
		}
		it := p.rowItem(m, r, ctxRoot)
		if kill {
			it = p.killItem(m, r)
		}
		items = append(items, it)
	}
	if rest != "" && !kill && len(rows) > 0 {
		items = append(items, plugin.Item{ID: "open-view", Title: l.T("root.openView", "query", rest), Icon: plugin.Icon{Name: "activity"},
			Transient: true,
			Actions: []plugin.Action{{ID: "open", Title: l.T("action.openView"),
				Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: cmdProcesses, Payload: plugin.MustPayload(viewPayload{Query: rest})}}}})
	}
	if len(items) == 0 {
		items = append(items, plugin.Item{ID: "none", Title: l.T("root.none", "query", rest), Icon: plugin.Icon{Name: "info", Tint: plugin.TintMuted}})
	}
	group := l.T("group")
	for i := range items {
		items[i].Group = group
		items[i].Score = 1 - float64(0.001*float64(i))
	}
	return items, nil
}

// visibleRows are the rows matching q (all for ""), best match first for a
// search, else in the sort order; the Windows processes section is left
// out when the preference hides it.
func (p *Plugin) visibleRows(m *model, q string) []*row {
	showWin := p.h.Preferences().Bool(prefShowWindows)
	type hit struct {
		r     *row
		score float64
	}
	var hits []hit
	for _, r := range m.rows {
		if r.section == secWindows && !showWin && q == "" {
			continue
		}
		if s := matchRow(r, q); s > 0 {
			hits = append(hits, hit{r, s})
		}
	}
	out := make([]*row, len(hits))
	for i, h := range hits {
		out[i] = h.r
	}
	if q == "" {
		p.mu.Lock()
		prev := p.st.order
		p.mu.Unlock()
		sortRows(out, p.sortOrder(), prev)
		rank := make(map[string]int, len(out))
		for i, r := range out {
			rank[r.id()] = i
		}
		p.mu.Lock()
		p.st.order = rank
		p.mu.Unlock()
		return out
	}
	scores := map[*row]float64{}
	for _, h := range hits {
		scores[h.r] = h.score
	}
	sortBy(out, func(a, b *row) int {
		if scores[a] != scores[b] {
			if scores[a] > scores[b] {
				return -1
			}
			return 1
		}
		return byCPU(a, b)
	})
	return out
}

// --- Views --------------------------------------------------------------------

// viewPayload is the payload of a pushed view.
type viewPayload struct {
	Query string     `json:"query,omitempty"`
	Root  process.ID `json:"root,omitzero"`
}

// View implements plugin.ViewProvider: the Processes view, the processes
// of one app (group view) and the apps not responding.
func (p *Plugin) View(ctx context.Context, req plugin.ViewRequest) (plugin.View, error) {
	v := viewState{id: req.ID, query: req.Query.Text}
	var pl viewPayload
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &pl); err != nil {
			return plugin.View{}, err
		}
	}
	switch req.ID {
	case cmdProcesses, cmdHung:
	case viewGroup:
		v.root = pl.Root
	default:
		return plugin.View{}, fmt.Errorf("processes: unknown view %q", req.ID)
	}
	p.viewPulled(req.Instance, v)
	out, err := p.render(v)
	if err != nil {
		return out, err
	}
	return out, ctx.Err()
}

// render builds the content of an open view from the latest sample.
func (p *Plugin) render(v viewState) (plugin.View, error) {
	switch v.id {
	case viewGroup:
		return p.groupView(v), nil
	case cmdHung:
		return p.hungView(v), nil
	}
	return p.processesView(v), nil
}

// --- Actions ------------------------------------------------------------------

// HandleAction implements plugin.ActionHandler.
func (p *Plugin) HandleAction(ctx context.Context, a plugin.ActionDo) (plugin.Result, error) {
	if a.Kind == plugin.ActionRunCommand {
		if a.Target == cmdRestartExplorer {
			return p.restartExplorer(), nil
		}
		return plugin.Result{}, fmt.Errorf("processes: unknown command %q", a.Target)
	}
	var pl target
	if len(a.Payload) > 0 {
		if err := json.Unmarshal(a.Payload, &pl); err != nil {
			return plugin.Result{}, err
		}
	}
	switch a.Target {
	case cbEndTask:
		return p.endTask(pl), nil
	case cbForce:
		return p.forceEnd(pl), nil
	case cbTree:
		return p.endTree(pl), nil
	case cbElevate:
		return p.endElevated(pl), nil
	case cbSwitch:
		return p.switchTo(pl), nil
	case cbCopyCmd:
		return p.copyCommandLine(pl)
	case cbEndAllHung:
		return p.endAllHung(), nil
	case cbRestart:
		return p.restartExplorer(), nil
	case cbRefresh:
		p.sample()
		return plugin.Result{Refresh: true}, nil
	case cbSort:
		if !validSort(pl.Sort) {
			return plugin.Result{}, fmt.Errorf("processes: unknown sort %q", pl.Sort)
		}
		if err := p.h.Storage().Set(kvSort, []byte(pl.Sort)); err != nil {
			return plugin.Result{}, err
		}
		return plugin.Result{Refresh: true}, nil
	case cbGroup:
		v := "false"
		if !p.grouping() {
			v = "true"
		}
		if err := p.h.Storage().Set(kvGroup, []byte(v)); err != nil {
			return plugin.Result{}, err
		}
		return plugin.Result{Refresh: true}, nil
	}
	return plugin.Result{}, fmt.Errorf("processes: unknown action %q", a.Target)
}

func itoa(n int) string { return strconv.Itoa(n) }

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
