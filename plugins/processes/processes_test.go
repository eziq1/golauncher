package processes

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/winapi/process"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// The fixture's processes are invented (testdata/fixtures/processes.json).
const fixturePath = "../../testdata/fixtures/processes.json"

// fakeTicks replaces time.After: a wait ends only when the test calls tick.
type fakeTicks struct {
	ch chan time.Time

	mu       sync.Mutex
	waits    []time.Duration
	consumed int
}

func (f *fakeTicks) after(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	f.waits = append(f.waits, d)
	f.mu.Unlock()
	return f.ch
}

// tick lets one waiting loop run.
func (f *fakeTicks) tick(t *testing.T) {
	t.Helper()
	eventually(t, "a loop waits", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.waits) > f.consumed
	})
	f.mu.Lock()
	f.consumed = len(f.waits)
	f.mu.Unlock()
	select {
	case f.ch <- time.Time{}:
	case <-time.After(2 * time.Second):
		t.Fatal("nothing waits for a tick")
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

type env struct {
	p     *Plugin
	h     *hostfake.Host
	b     *FixtureBackend
	ticks *fakeTicks
}

// newEnv runs the plugin on the fixture. With sync, both samples are taken
// at once and nothing refreshes (as in glctl); otherwise the refresh loop
// runs on fake ticks.
func newEnv(t *testing.T, sync bool) *env {
	t.Helper()
	b := LoadFixture(fixturePath)
	if b.Err != nil {
		t.Fatal(b.Err)
	}
	ticks := &fakeTicks{ch: make(chan time.Time)}
	p := NewWithOptions(Options{Backend: b, Sync: sync, After: ticks.after})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.Clock = func() time.Time { return time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC) }
	h.Clip = &hostfake.Clipboard{}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	return &env{p: p, h: h, b: b, ticks: ticks}
}

func (e *env) root(t *testing.T, text string) []plugin.Item {
	t.Helper()
	items, err := e.p.RootItems(t.Context(), plugin.Query{Text: strings.TrimSpace(text), Raw: text})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func (e *env) view(t *testing.T, id, instance, text string, payload any) plugin.View {
	t.Helper()
	req := plugin.ViewRequest{ID: id, Instance: instance, Query: plugin.Query{Text: text}}
	if payload != nil {
		req.Payload = plugin.MustPayload(payload)
	}
	v, err := e.p.View(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (e *env) do(t *testing.T, a plugin.Action) plugin.Result {
	t.Helper()
	r, err := e.p.HandleAction(t.Context(), a.Do)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func ids(items []plugin.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func find(items []plugin.Item, id string) (plugin.Item, bool) {
	i := slices.IndexFunc(items, func(it plugin.Item) bool { return it.ID == id })
	if i < 0 {
		return plugin.Item{}, false
	}
	return items[i], true
}

func action(it plugin.Item, id string) (plugin.Action, bool) {
	i := slices.IndexFunc(it.Actions, func(a plugin.Action) bool { return a.ID == id })
	if i < 0 {
		return plugin.Action{}, false
	}
	return it.Actions[i], true
}

func allItems(v plugin.View) []plugin.Item {
	var out []plugin.Item
	for _, s := range v.Sections {
		out = append(out, s.Items...)
	}
	return out
}

// Item IDs of fixture processes.
const (
	idWriter   = "p:3000:134364438000000000"
	idFabrikam = "p:3100:134364438600000000"
	idNode     = "p:3410:134364439300000000"
	idPaint    = "p:3500:134364440000000000"
	idNotes    = "p:3600:134364440500000000"
	idSelf     = "p:3700:134364384700000000"
	idTask     = "p:3710:134364441000000000"
	idAdmin    = "p:3800:134364442000000000"
	idExplorer = "p:2000:134364384600000000"
	idLsass    = "p:920:134364384060000001"
	idFakeLsa  = "p:4000:134364443000000000"
	idSmss     = "p:600:134364384020000000"
	idSystem   = "p:4:134364384000000000"
	idDWM      = "p:1500:134364384200000000"
	idSync     = "p:3300:134364438050000000"
	idMargie   = "p:4300:134364447000000000"
)

func TestPrefixes(t *testing.T) {
	e := newEnv(t, true)
	for _, text := range []string{"ps", "psx", "photoshop", "kill-all", "进 程"} {
		if items := e.root(t, text); len(items) != 0 {
			t.Errorf("%q: %v", text, ids(items))
		}
	}
	if n := e.b.samples; n != 0 {
		t.Errorf("text without a prefix sampled %d times", n)
	}
	items := e.root(t, "ps ")
	if len(items) < 2 || items[0].ID != "summary" || items[0].Score <= items[1].Score {
		t.Fatalf("ps: %v", ids(items))
	}
	if !strings.Contains(items[0].Title, "CPU 23.0%") || !strings.Contains(items[0].Title, "36 processes") {
		t.Errorf("summary = %q", items[0].Title)
	}
	if got := e.root(t, "进程fab"); len(got) == 0 || got[0].ID != idFabrikam {
		t.Errorf("进程fab: %v", ids(got))
	}
	if got := e.root(t, "kill"); len(got) == 0 || got[0].ID == "summary" {
		t.Errorf("kill: %v", ids(got))
	}
	e.h.Prefs.Set(prefPrefixes, plugin.MustPayload([]string{"top"}))
	if got := e.root(t, "ps fab"); len(got) != 0 {
		t.Errorf("ps after changing the prefixes: %v", ids(got))
	}
	if got := e.root(t, "top fab"); len(got) == 0 || got[0].ID != idFabrikam {
		t.Errorf("top fab: %v", ids(got))
	}
	e.h.Prefs.Set(prefPrefixes, plugin.MustPayload([]string{}))
	if got := e.root(t, "kill fab"); len(got) != 0 {
		t.Errorf("no prefixes: %v", ids(got))
	}
}

func TestSearch(t *testing.T) {
	e := newEnv(t, true)
	tests := []struct{ text, first string }{
		{"ps fab", idFabrikam},
		{"ps fabrikam.exe", idFabrikam},
		{"ps writer", idWriter},
		{"ps quarterly", idWriter}, // a window title
		{"ps nfbj", idNotes},       // pinyin initials of 南风笔记
		{"ps nanfeng", idNotes},
		{"ps 南风", idNotes},
		{"ps 3420", idNode}, // the PID of a member: its group
		{"ps 3000", idWriter},
	}
	for _, tt := range tests {
		got := e.root(t, tt.text)
		if len(got) == 0 || got[0].ID != tt.first {
			t.Errorf("%q: %v, want %s first", tt.text, ids(got), tt.first)
		}
	}
	got := e.root(t, "ps fab")
	if got[0].Title != "Fabrikam Browser (8)" || !got[0].SubtitleSensitive || got[0].Subtitle != "New Tab - Fabrikam" {
		t.Errorf("fab = %q %q %v", got[0].Title, got[0].Subtitle, got[0].SubtitleSensitive)
	}
	if last := got[len(got)-1]; last.ID != "open-view" || !last.Transient {
		t.Errorf("last = %+v", last)
	}
	if got := e.root(t, "ps zzqx"); len(got) != 1 || got[0].ID != "none" {
		t.Errorf("no match: %v", ids(got))
	}
}

func TestBars(t *testing.T) {
	e := newEnv(t, true)
	it, _ := find(e.root(t, "ps fab"), idFabrikam)
	if len(it.Accessories) != 2 {
		t.Fatalf("accessories = %+v", it.Accessories)
	}
	cpu, mem := it.Accessories[0], it.Accessories[1]
	// Main process 1% of 8 processors over 1 s, children 7 times that: 8%/8.
	if cpu.Text != "CPU 0.8%" || cpu.Meter == nil || *cpu.Meter < 0.0079 || *cpu.Meter > 0.0081 || !cpu.Load {
		t.Errorf("cpu = %+v", cpu)
	}
	if mem.Text != "1.2 GB" || mem.Meter == nil || !mem.Load {
		t.Errorf("memory = %+v", mem)
	}
	// Per core: no division by the 8 processors.
	e.h.Prefs.Set(prefCPUMode, plugin.MustPayload("perCore"))
	e.p.mu.Lock()
	e.p.st.model = nil
	e.p.mu.Unlock()
	it, _ = find(e.root(t, "ps fab"), idFabrikam)
	if it.Accessories[0].Text != "CPU 6.4%" {
		t.Errorf("per core: %+v", it.Accessories[0])
	}
}

func TestViewSections(t *testing.T) {
	e := newEnv(t, true)
	v := e.view(t, cmdProcesses, "", "", nil)
	var titles []string
	for _, s := range v.Sections {
		titles = append(titles, s.Title)
	}
	if !slices.Equal(titles, []string{"", "Apps (8)", "Background processes (7)", "Windows processes (13)"}) {
		t.Errorf("sections = %q", titles)
	}
	if v.FilterLocally || !v.ShowDetail {
		t.Errorf("view = %+v", v)
	}
	section := func(id string) string {
		for _, s := range v.Sections {
			if _, ok := find(s.Items, id); ok {
				return s.Title
			}
		}
		return ""
	}
	for id, want := range map[string]string{idExplorer: "Apps (8)", idSync: "Background processes (7)", idLsass: "Windows processes (13)",
		idFakeLsa: "Background processes (7)", idDWM: "Windows processes (13)"} {
		if got := section(id); got != want {
			t.Errorf("%s in %q, want %q", id, got, want)
		}
	}
	// Hiding the Windows processes.
	e.h.Prefs.Set(prefShowWindows, plugin.MustPayload(false))
	if v := e.view(t, cmdProcesses, "", "", nil); len(v.Sections) != 3 {
		t.Errorf("without Windows processes: %d sections", len(v.Sections))
	}
	// A search lists matches in one section.
	if v := e.view(t, cmdProcesses, "", "node", nil); len(v.Sections) != 1 || v.Sections[0].Items[0].ID != idNode {
		t.Errorf("search: %+v", v.Sections)
	}
	// Ungrouped: the eight browser processes are rows of their own.
	if _, err := e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: cbGroup, Payload: plugin.MustPayload(target{})}); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, it := range allItems(e.view(t, cmdProcesses, "", "fabrikam", nil)) {
		if strings.HasPrefix(it.Title, "Fabrikam") {
			n++
		}
	}
	if n != 8 {
		t.Errorf("ungrouped: %d browser rows", n)
	}
}

func TestGroupView(t *testing.T) {
	e := newEnv(t, true)
	it, _ := find(e.root(t, "ps fab"), idFabrikam)
	show, ok := action(it, "show")
	if !ok || show.Do.Kind != plugin.ActionPush || show.Shortcut != "Ctrl+D" {
		t.Fatalf("show = %+v", show)
	}
	v, err := e.p.View(t.Context(), plugin.ViewRequest{ID: show.Do.Target, Payload: show.Do.Payload})
	if err != nil {
		t.Fatal(err)
	}
	items := allItems(v)
	if v.Title != "Fabrikam Browser (8)" || len(items) != 8 || items[0].ID != idFabrikam || !strings.HasPrefix(items[1].Title, "└ ") {
		t.Fatalf("group view %q: %v %q", v.Title, ids(items), items[1].Title)
	}
	// Node and the worker it started.
	v = e.view(t, viewGroup, "", "", viewPayload{Root: process.ID{PID: 3410, Created: 134364439300000000}})
	if items := allItems(v); len(items) != 2 || items[1].Title != "└ Node.js JavaScript Runtime" {
		t.Errorf("node group: %v", allItems(v))
	}
	// Gone.
	if v := e.view(t, viewGroup, "", "", viewPayload{Root: process.ID{PID: 77, Created: 1}}); len(v.Sections) != 0 || v.EmptyText == "" {
		t.Errorf("gone group: %+v", v)
	}
}

// TestEndActions checks which end actions each kind of process has, and
// which of them ask for confirmation (docs/process-manager.md §14: End
// task of an app with windows does not; Force end and End process tree
// do; critical processes and golauncher have none).
func TestEndActions(t *testing.T) {
	e := newEnv(t, true)
	items := allItems(e.view(t, cmdProcesses, "", "", nil))
	get := func(id string) plugin.Item {
		t.Helper()
		it, ok := find(items, id)
		if !ok {
			t.Fatalf("no %s", id)
		}
		return it
	}
	ends := []string{"end-task", "force-end", "end-tree", "end-elevated"}
	for _, id := range []string{idLsass, idSmss, idSystem, idSelf} {
		for _, a := range get(id).Actions {
			if slices.Contains(ends, a.ID) || a.ID == "restart-explorer" || a.Style == plugin.StyleDestructive {
				t.Errorf("%s offers %s", id, a.ID)
			}
		}
	}
	// The look-alike lsass.exe outside System32 can be ended.
	if _, ok := action(get(idFakeLsa), "force-end"); !ok {
		t.Error("the look-alike lsass.exe cannot be ended")
	}
	// Explorer with the taskbar: restart only.
	ex := get(idExplorer)
	if _, ok := action(ex, "end-task"); ok {
		t.Error("explorer can be ended")
	}
	if a, ok := action(ex, "restart-explorer"); !ok || a.Style != plugin.StyleDestructive || a.ConfirmMessage == "" {
		t.Errorf("restart = %+v", a)
	}
	// An app with a window: Enter switches, End task without confirmation.
	w := get(idWriter)
	if w.Actions[0].ID != "switch" {
		t.Errorf("writer Enter = %s", w.Actions[0].ID)
	}
	if a, _ := action(w, "end-task"); a.Style != "" || a.Shortcut != "Ctrl+X" {
		t.Errorf("writer end task = %+v", a)
	}
	if a, _ := action(w, "force-end"); a.Style != plugin.StyleDestructive || a.Shortcut != "Ctrl+Shift+X" || a.ConfirmMessage == "" {
		t.Errorf("writer force = %+v", a)
	}
	// Without a window, or not responding: End task ends at once, and asks.
	for _, id := range []string{idSync, idPaint} {
		if a, _ := action(get(id), "end-task"); a.Style != plugin.StyleDestructive {
			t.Errorf("%s end task = %+v", id, a)
		}
	}
	// A process without windows: Enter shows its processes and detail.
	if a := get(idSync).Actions[0]; a.ID != "show" {
		t.Errorf("sync Enter = %s", a.ID)
	}
	// The tree of the browser: 8 processes.
	if a, _ := action(get(idFabrikam), "end-tree"); a.Shortcut != "Ctrl+Alt+X" || !strings.HasPrefix(a.ConfirmMessage, "8 processes") {
		t.Errorf("tree = %+v", a)
	}
	// Warnings.
	if a, _ := action(get(idDWM), "force-end"); !strings.Contains(a.ConfirmMessage, "flicker") {
		t.Errorf("dwm confirmation = %q", a.ConfirmMessage)
	}
	if a, _ := action(get(idTask), "force-end"); !strings.Contains(a.ConfirmMessage, "background task of golauncher") {
		t.Errorf("task confirmation = %q", a.ConfirmMessage)
	}
	// "kill": Enter ends the task.
	k := e.root(t, "kill writer")
	if len(k) == 0 || k[0].ID != idWriter || k[0].Actions[0].ID != "end-task" {
		t.Errorf("kill writer: %+v", k)
	}
	// Read-only refresh; search online with Google.
	if a, _ := action(w, "refresh"); !a.ReadOnly {
		t.Error("refresh is not read-only")
	}
	if a, _ := action(w, "search-online"); a.Do.Target != "https://www.google.com/search?q=%22writer.exe%22" {
		t.Errorf("search online = %s", a.Do.Target)
	}
}

func TestEndTaskGraceful(t *testing.T) {
	for _, sync := range []bool{true, false} {
		e := newEnv(t, sync)
		items := allItems(e.view(t, cmdProcesses, "", "", nil))
		w, _ := find(items, idWriter)
		end, _ := action(w, "end-task")
		r := e.do(t, end)
		toast := r.Toast
		if !sync {
			if toast == nil || toast.Title != "Asking Contoso Writer to close…" {
				t.Fatalf("toast = %+v", toast)
			}
			eventually(t, "the result toast", func() bool { return len(e.h.Toasts()) == 1 })
			tt := e.h.Toasts()[0]
			toast = &tt
		}
		if toast.Style != plugin.ToastSuccess || toast.Title != "Ended Contoso Writer" {
			t.Errorf("sync %v: toast = %+v", sync, toast)
		}
		if calls := e.b.Calls(); !slices.Equal(calls, []string{"close-windows 1"}) {
			t.Errorf("calls = %v", calls)
		}
		// An app that stays (asking to save): offer Force end.
		m, _ := find(items, idMargie)
		end, _ = action(m, "end-task")
		r = e.do(t, end)
		if !sync {
			eventually(t, "the second toast", func() bool { return len(e.h.Toasts()) == 2 })
			tt := e.h.Toasts()[1]
			r.Toast = &tt
		}
		if r.Toast.Title != "Margie's Travel is still running" || !strings.Contains(r.Toast.Message, "Ctrl+Shift+X") {
			t.Errorf("still running: %+v", r.Toast)
		}
		if slices.ContainsFunc(e.b.Calls(), func(c string) bool { return strings.HasPrefix(c, "kill") }) {
			t.Errorf("ending a task killed: %v", e.b.Calls())
		}
	}
}

func TestForceEnd(t *testing.T) {
	e := newEnv(t, true)
	items := allItems(e.view(t, cmdProcesses, "", "", nil))
	// Not responding: End task ends it at once.
	p, _ := find(items, idPaint)
	end, _ := action(p, "end-task")
	if r := e.do(t, end); r.Toast.Title != "Ended Litware Paint" || !r.Refresh {
		t.Errorf("paint: %+v", r.Toast)
	}
	if !slices.Equal(e.b.Calls(), []string{"kill 3500"}) {
		t.Errorf("calls = %v", e.b.Calls())
	}
	// A group: every member.
	f, _ := find(items, idFabrikam)
	force, _ := action(f, "force-end")
	if r := e.do(t, force); r.Toast.Title != "Ended Fabrikam Browser (8 processes)" {
		t.Errorf("fabrikam: %+v", r.Toast)
	}
	if n := len(e.b.Calls()); n != 9 {
		t.Errorf("%d calls", n)
	}
}

func TestEndTree(t *testing.T) {
	e := newEnv(t, true)
	n, _ := find(allItems(e.view(t, cmdProcesses, "", "", nil)), idNode)
	tree, ok := action(n, "end-tree")
	if !ok {
		t.Fatal("no end tree")
	}
	e.do(t, tree)
	if !slices.Equal(e.b.Calls(), []string{"kill 3410", "kill 3420"}) {
		t.Errorf("calls = %v", e.b.Calls())
	}
}

// TestPIDReuse: an action whose process exited and whose PID now belongs
// to another process ends nothing.
func TestPIDReuse(t *testing.T) {
	e := newEnv(t, true)
	stale := target{ID: process.ID{PID: 3000, Created: 1}, Name: "Old Writer"}
	r := e.do(t, callback("force-end", "", cbForce, stale, ""))
	if r.Toast == nil || r.Toast.Title != "Old Writer has already exited" {
		t.Errorf("toast = %+v", r.Toast)
	}
	if len(e.b.Calls()) != 0 {
		t.Errorf("calls = %v", e.b.Calls())
	}
}

// TestCriticalRefused: even a crafted action cannot end a critical process
// or golauncher itself.
func TestCriticalRefused(t *testing.T) {
	e := newEnv(t, true)
	for _, id := range []process.ID{{PID: 920, Created: 134364384060000001}, {PID: 3700, Created: 134364384700000000}, {PID: 4, Created: 134364384000000000}} {
		r := e.do(t, callback("force-end", "", cbForce, target{ID: id, Name: "x"}, ""))
		if r.Toast == nil || r.Toast.Title != "x is protected and cannot be ended" {
			t.Errorf("%v: %+v", id, r.Toast)
		}
		e.do(t, callback("end-tree", "", cbTree, target{ID: id, Name: "x", Tree: true}, ""))
	}
	if len(e.b.Calls()) != 0 {
		t.Errorf("calls = %v", e.b.Calls())
	}
}

func TestNeedsAdmin(t *testing.T) {
	e := newEnv(t, true)
	a, _ := find(allItems(e.view(t, cmdProcesses, "", "", nil)), idAdmin)
	if _, ok := action(a, "end-elevated"); ok {
		t.Error("End as administrator before a failure")
	}
	force, _ := action(a, "force-end")
	r := e.do(t, force)
	if r.Toast.Style != plugin.ToastFailure || r.Toast.Title != "Ending Tailspin Admin Tool needs administrator rights" ||
		!strings.Contains(r.Toast.Message, "Ctrl+Shift+A") {
		t.Errorf("toast = %+v", r.Toast)
	}
	a, _ = find(allItems(e.view(t, cmdProcesses, "", "", nil)), idAdmin)
	el, ok := action(a, "end-elevated")
	if !ok || el.Shortcut != "Ctrl+Shift+A" {
		t.Fatalf("no End as administrator: %v", a.Actions)
	}
	r = e.do(t, el)
	if r.Toast.Title != "Ended Tailspin Admin Tool" {
		t.Errorf("elevated: %+v", r.Toast)
	}
	want := "elevate [kill --pid 3800 --created 134364442000000000]"
	if calls := e.b.Calls(); len(calls) != 1 || calls[0] != want {
		t.Errorf("calls = %v, want %s", calls, want)
	}
}

func TestHungView(t *testing.T) {
	e := newEnv(t, true)
	v := e.view(t, cmdHung, "", "", nil)
	items := allItems(v)
	if len(items) != 1 || items[0].ID != idPaint || items[0].Actions[0].ID != "end-task" || items[0].Accessory != "Not responding" {
		t.Fatalf("hung view: %+v", items)
	}
}

func TestRestartExplorer(t *testing.T) {
	e := newEnv(t, true)
	r, err := e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdRestartExplorer})
	if err != nil || r.Toast.Title != "Restarting Windows Explorer" {
		t.Fatalf("%+v %v", r.Toast, err)
	}
	// The fixture's taskbar is still there: Windows started it again.
	if !slices.Equal(e.b.Calls(), []string{"kill 2000"}) {
		t.Errorf("calls = %v", e.b.Calls())
	}
}

// TestPrivacy: window titles and command lines are shown, marked so that
// an AI never gets them, and command lines are redacted and copied as a
// secret.
func TestPrivacy(t *testing.T) {
	e := newEnv(t, true)
	n, _ := find(allItems(e.view(t, cmdProcesses, "", "", nil)), idNode)
	var cmd *plugin.Metadata
	for i, m := range n.Detail.Metadata {
		if m.Label == "Command line" {
			cmd = &n.Detail.Metadata[i]
		}
	}
	if cmd == nil || !cmd.Sensitive || strings.Contains(cmd.Value, "abc123") || strings.Contains(cmd.Value, "hunter2") ||
		!strings.Contains(cmd.Value, "--token=•••") {
		t.Fatalf("command line = %+v", cmd)
	}
	for _, it := range allItems(e.view(t, cmdProcesses, "", "", nil)) {
		if it.Detail == nil {
			continue
		}
		for _, m := range it.Detail.Metadata {
			if (m.Label == "Windows" || m.Label == "Command line") && !m.Sensitive {
				t.Errorf("%s: %s not sensitive", it.ID, m.Label)
			}
		}
		if it.Subtitle != "" && !it.SubtitleSensitive && !strings.Contains(it.Subtitle, "PID") {
			t.Errorf("%s: subtitle %q not sensitive", it.ID, it.Subtitle)
		}
	}
	copyCmd, _ := action(n, "copy-command-line")
	if strings.Contains(string(copyCmd.Do.Payload), "server.js") {
		t.Error("the command line is in the action's payload")
	}
	r := e.do(t, copyCmd)
	c, _ := e.h.Clip.Read()
	if !r.Hide || !c.Exclude || !strings.Contains(c.Text, "--token=abc123") {
		t.Errorf("copied %+v (result %+v)", c, r)
	}
	// Not redacted when the user says so.
	e.h.Prefs.Set(prefRedact, plugin.MustPayload(false))
	n, _ = find(allItems(e.view(t, cmdProcesses, "", "", nil)), idNode)
	if !slices.ContainsFunc(n.Detail.Metadata, func(m plugin.Metadata) bool { return strings.Contains(m.Value, "abc123") }) {
		t.Error("redacted despite the preference")
	}
}

func TestForeground(t *testing.T) {
	e := newEnv(t, true)
	ctx := func(name string, pid int) *plugin.Context {
		return &plugin.Context{App: plugin.AppInfo{Name: name, PID: pid}}
	}
	// A responding app: "Quit" in the root, nothing on top.
	items, _ := e.p.RootItems(t.Context(), plugin.Query{Text: "quit", Context: ctx("writer.exe", 3000)})
	if len(items) != 1 || items[0].Title != "Quit Contoso Writer" || !items[0].Transient || items[0].Score != 0 {
		t.Fatalf("quit: %+v", items)
	}
	if a := items[0].Actions[0]; a.ID != "end-task" || a.Style != "" {
		t.Errorf("quit Enter = %+v", a)
	}
	if a := items[0].Actions[1]; a.ID != "force-end" || a.Style != plugin.StyleDestructive {
		t.Errorf("quit force = %+v", a)
	}
	if got, _ := e.p.ContextItems(t.Context(), *ctx("writer.exe", 3000)); len(got) != 0 {
		t.Errorf("context items of a responding app: %v", ids(got))
	}
	// Not responding: "Force quit" on top of the empty search.
	got, _ := e.p.ContextItems(t.Context(), *ctx("paint.exe", 3500))
	if len(got) != 1 || got[0].Title != "Force Quit Litware Paint (Not Responding)" || got[0].Actions[0].Style != plugin.StyleDestructive {
		t.Fatalf("hung: %+v", got)
	}
	// By name only (glctl --context-app).
	items, _ = e.p.RootItems(t.Context(), plugin.Query{Text: "quit", Context: ctx("paint.exe", 0)})
	if len(items) != 1 || items[0].Actions[0].Style != plugin.StyleDestructive {
		t.Errorf("by name: %+v", items)
	}
	// The desktop, Explorer, golauncher, a critical process: nothing.
	for _, c := range []*plugin.Context{ctx("explorer.exe", 2000), ctx("golauncher.exe", 3700), ctx("lsass.exe", 920),
		{App: plugin.AppInfo{Name: "explorer.exe", PID: 2000}, Kind: plugin.ContextDesktop}} {
		if items, _ := e.p.RootItems(t.Context(), plugin.Query{Text: "quit", Context: c}); len(items) != 0 {
			t.Errorf("%s: %v", c.App.Name, ids(items))
		}
	}
	// Turned off.
	e.h.Prefs.Set(prefForegroundQuit, plugin.MustPayload(false))
	e.p.mu.Lock()
	e.p.fg = nil
	e.p.mu.Unlock()
	if items, _ := e.p.RootItems(t.Context(), plugin.Query{Text: "quit", Context: ctx("writer.exe", 3000)}); len(items) != 0 {
		t.Errorf("off: %v", ids(items))
	}
}

func TestSortAndAntiJitter(t *testing.T) {
	mk := func(id uint32, cpu float64) *row {
		return &row{root: &proc{Proc: process.Proc{ID: process.ID{PID: id, Created: 1}}, info: &pinfo{}}, cpu: cpu}
	}
	a, b, c := mk(1, 10), mk(2, 10.5), mk(3, 50)
	rows := []*row{a, b, c}
	sortRows(rows, sortCPU, nil)
	if rows[0] != c || rows[1] != b || rows[2] != a {
		t.Fatalf("order = %d %d %d", rows[0].root.PID, rows[1].root.PID, rows[2].root.PID)
	}
	// a and b are within 10%: they keep their previous order.
	prev := map[string]int{c.id(): 0, a.id(): 1, b.id(): 2}
	rows = []*row{b, a, c}
	sortRows(rows, sortCPU, prev)
	if rows[0] != c || rows[1] != a || rows[2] != b {
		t.Errorf("anti-jitter order = %d %d %d", rows[0].root.PID, rows[1].root.PID, rows[2].root.PID)
	}
	// Sorting is chosen in the action panel and remembered.
	e := newEnv(t, true)
	e.do(t, callback("sort-memory", "", cbSort, target{Sort: sortMemory}, ""))
	v := e.view(t, cmdProcesses, "", "", nil)
	if v.Sections[1].Items[0].ID != idFabrikam { // 1.2 GB
		t.Errorf("by memory: %v", ids(v.Sections[1].Items))
	}
	if _, err := e.p.HandleAction(t.Context(), callback("x", "", cbSort, target{Sort: "bogus"}, "").Do); err == nil {
		t.Error("a bogus sort was accepted")
	}
}

func TestFormat(t *testing.T) {
	for n, want := range map[uint64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 42 << 20: "42.0 MB", 1288490189: "1.2 GB", 310 << 20: "310 MB"} {
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", n, got, want)
		}
	}
	if full, ini := pinyinOf("南风笔记"); full != "nan feng bi ji" || ini != "nfbj" {
		t.Errorf("pinyin = %q %q", full, ini)
	}
	if full, _ := pinyinOf("Fabrikam"); full != "" {
		t.Errorf("pinyin of latin = %q", full)
	}
}
