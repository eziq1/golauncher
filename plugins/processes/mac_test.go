package processes

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/winapi/process"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// macFixture is an invented Mac (Backend.Mac): apps have one window each
// without a title, as the macOS backend reports them.
func macFixture() *FixtureBackend {
	const gb = 1 << 30
	app := func(title string) []FixWindow { return []FixWindow{{Title: title}} }
	procs := []FixProcess{
		{PID: 0, Created: 100, Name: "kernel_task", CPU: [2]int64{0, 2e6}, Memory: 1 << 20, Info: FixInfo{User: "root", SystemAccount: true}},
		{PID: 1, Created: 101, Name: "launchd", CPU: [2]int64{0, 1e5}, Info: FixInfo{Path: "/sbin/launchd", User: "root", SystemAccount: true, Critical: true}},
		{PID: 150, Created: 150, Parent: 1, Name: "WindowServer", CPU: [2]int64{0, 3e6}, Memory: 400 << 20,
			Info: FixInfo{Path: "/System/Library/PrivateFrameworks/SkyLight.framework/Versions/A/Resources/WindowServer", User: "_windowserver", SystemAccount: true, Critical: true}},
		{PID: 400, Created: 400, Parent: 1, Name: "Finder", Memory: 90 << 20, Windows: app(""),
			Info: FixInfo{Path: "/System/Library/CoreServices/Finder.app/Contents/MacOS/Finder", Description: "Finder", User: "someone", Package: "com.apple.finder"}},
		{PID: 450, Created: 450, Parent: 1, Name: "Contoso Notes", Memory: 60 << 20, Windows: app(""), Closes: true,
			Info: FixInfo{Path: "/Applications/Contoso Notes.app/Contents/MacOS/Contoso Notes", Description: "Contoso Notes", User: "someone", Package: "com.contoso.notes"}},
		{PID: 410, Created: 410, Parent: 1, Name: "contosod", Memory: 8 << 20,
			Info: FixInfo{Path: "/usr/libexec/contosod", User: "someone"}},
		{PID: 500, Created: 500, Parent: 1, Name: "Fabrikam Browser", CPU: [2]int64{0, 5e6}, Memory: 300 << 20, Windows: app(""), Closes: true,
			Info: FixInfo{Path: "/Applications/Fabrikam Browser.app/Contents/MacOS/Fabrikam Browser", Description: "Fabrikam Browser", User: "someone", Package: "com.fabrikam.browser"}},
		{PID: 501, Created: 501, Parent: 500, Name: "Fabrikam Helper (GPU)", CPU: [2]int64{0, 1e6}, Memory: 100 << 20,
			Info: FixInfo{Path: "/Applications/Fabrikam Browser.app/Contents/Frameworks/Helpers/Fabrikam Helper (GPU).app/Contents/MacOS/Fabrikam Helper (GPU)",
				Description: "Fabrikam Helper (GPU)", User: "someone", Package: "com.fabrikam.browser"}},
		{PID: 600, Created: 600, Parent: 1, Name: "node", Memory: 50 << 20, Threads: 7,
			Info: FixInfo{Path: "/usr/local/bin/node", User: "someone"}},
		{PID: 700, Created: 700, Parent: 1, Name: "golauncher", Windows: app(""),
			Info: FixInfo{Path: "/Applications/golauncher.app/Contents/MacOS/golauncher", Description: "golauncher", User: "someone", Package: "com.example.golauncher"}},
		{PID: 800, Created: 800, Parent: 1, Name: "syncd", Denied: true,
			Info: FixInfo{Path: "/Library/Northwind/syncd", User: "root", SystemAccount: true}},
	}
	for i := range procs {
		procs[i].Session = 1 // as the macOS backend reports every process
	}
	return &FixtureBackend{F: Fixture{CPUs: 8, MemTotal: 16 * gb, MemAvail: 6 * gb, Self: 700, Mac: true,
		Time: time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC), SysIdle: [2]int64{0, 70}, SysBusy: [2]int64{0, 30}, Processes: procs}}
}

func newMacEnv(t *testing.T) *env {
	t.Helper()
	b := macFixture()
	p := NewWithOptions(Options{Backend: b, Sync: true})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.Clock = func() time.Time { return time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC) }
	h.Clip = &hostfake.Clipboard{}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return &env{p: p, h: h, b: b}
}

func TestMacManifest(t *testing.T) {
	e := newMacEnv(t)
	m := e.p.Manifest()
	var cmds []string
	for _, c := range m.Commands {
		cmds = append(cmds, c.Name)
	}
	if !slices.Equal(cmds, []string{cmdProcesses}) {
		t.Errorf("commands = %v", cmds)
	}
	for _, pr := range m.Preferences {
		if pr.Name == prefCPUMode && string(pr.Default) != `"perCore"` {
			t.Errorf("CPU default = %s", pr.Default)
		}
	}
	// Windows keeps all three and Task Manager's CPU.
	w := NewWithOptions(Options{Backend: LoadFixture(fixturePath)}).Manifest()
	if len(w.Commands) != 3 {
		t.Errorf("windows commands = %d", len(w.Commands))
	}
}

func TestMacSections(t *testing.T) {
	e := newMacEnv(t)
	v := e.view(t, cmdProcesses, "", "", nil)
	sec := map[string][]string{}
	for _, s := range v.Sections[1:] {
		for _, it := range s.Items {
			sec[s.Title] = append(sec[s.Title], it.Title)
		}
	}
	has := func(title, name string) bool {
		return slices.ContainsFunc(sec[title], func(s string) bool { return strings.HasPrefix(s, name) })
	}
	for _, c := range []struct{ section, name string }{
		{"Apps (4)", "Finder"}, {"Apps (4)", "Fabrikam Browser (2)"}, {"Apps (4)", "golauncher"},
		{"Background processes (1)", "node"},
		{"Windows processes (5)", "kernel_task"}, {"Windows processes (5)", "launchd"}, {"Windows processes (5)", "WindowServer"},
		{"Windows processes (5)", "contosod"}, {"Windows processes (5)", "syncd"},
	} {
		if !has(c.section, c.name) {
			t.Errorf("%s not in %q: %v", c.name, c.section, sec)
		}
	}
}

func TestMacActionsAndDetail(t *testing.T) {
	e := newMacEnv(t)
	items := allItems(e.view(t, cmdProcesses, "", "", nil))
	browser, ok := find(items, itemID(process.ID{PID: 500, Created: 500}))
	if !ok {
		t.Fatal("no browser row")
	}
	// Enter switches to the app; ending it asks it to quit, without asking.
	if browser.Actions[0].ID != "switch" {
		t.Errorf("Enter = %s", browser.Actions[0].ID)
	}
	notes, _ := find(items, itemID(process.ID{PID: 450, Created: 450}))
	end, _ := action(notes, "end-task")
	if end.Style != "" {
		t.Errorf("end task of an app asks first: %+v", end)
	}
	r := e.do(t, end)
	if r.Toast == nil || r.Toast.Title != "Ended Contoso Notes" {
		t.Errorf("end task: %+v", r.Toast)
	}
	if calls := e.b.Calls(); len(calls) == 0 || calls[0] != "close-windows 1" {
		t.Errorf("calls = %v", calls)
	}
	// Activity Monitor instead of Task Manager and Resource Monitor.
	am, ok := action(browser, "task-manager")
	if !ok || am.Do.Target != activityMonitor {
		t.Errorf("task manager = %+v", am)
	}
	if _, ok := action(browser, "resource-monitor"); ok {
		t.Error("Resource Monitor on a Mac")
	}
	// No handles, commit size or titleless windows in the detail.
	node, _ := find(items, itemID(process.ID{PID: 600, Created: 600}))
	for _, it := range []plugin.Item{browser, node} {
		for _, md := range it.Detail.Metadata {
			switch md.Label {
			case "Handles", "Commit size", "Peak working set", "Windows":
				t.Errorf("%s: %s = %q", it.Title, md.Label, md.Value)
			}
		}
	}
	if !slices.ContainsFunc(node.Detail.Metadata, func(md plugin.Metadata) bool { return md.Label == "Threads" && md.Value == "7" }) {
		t.Errorf("node threads: %+v", node.Detail.Metadata)
	}
	// kernel_task, launchd and WindowServer have no end action.
	for _, pid := range []uint32{0, 1, 150} {
		it, ok := find(items, itemID(process.ID{PID: pid, Created: map[uint32]int64{0: 100, 1: 101, 150: 150}[pid]}))
		if !ok {
			t.Errorf("%d not listed", pid)
			continue
		}
		for _, a := range it.Actions {
			if a.ID == "end-task" || a.ID == "force-end" || a.ID == "end-tree" {
				t.Errorf("%s has %s", it.Title, a.ID)
			}
		}
	}
}

func TestMacForegroundFinder(t *testing.T) {
	e := newMacEnv(t)
	ctx := &plugin.Context{App: plugin.AppInfo{Name: "Finder", PID: 400}}
	if items, _ := e.p.RootItems(context.Background(), plugin.Query{Text: "quit", Context: ctx}); len(items) != 0 {
		t.Errorf("Quit Finder offered: %v", ids(items))
	}
	ctx = &plugin.Context{App: plugin.AppInfo{Name: "Fabrikam Browser", PID: 500}}
	items, _ := e.p.RootItems(context.Background(), plugin.Query{Text: "quit", Context: ctx})
	if len(items) != 1 || items[0].Title != "Quit Fabrikam Browser" {
		t.Errorf("quit browser: %v", items)
	}
}

func TestMacSystemPath(t *testing.T) {
	for p, want := range map[string]bool{
		"/System/Library/CoreServices/Dock.app/Contents/MacOS/Dock": true,
		"/usr/libexec/contosod":                     true,
		"/usr/sbin/cupsd":                           true,
		"/sbin/launchd":                             true,
		"/Library/Apple/System/Library/x":           true,
		"/usr/local/bin/node":                       false,
		"/opt/homebrew/bin/node":                    false,
		"/Applications/Fabrikam.app/Contents/MacOS": false,
		"": false,
	} {
		if got := macSystemPath(p); got != want {
			t.Errorf("%q = %v", p, got)
		}
	}
}
