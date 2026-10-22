package shell

import (
	"context"
	"encoding/json/v2"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/ring"
	winselection "github.com/eziq1/golauncher/internal/winapi/selection"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// fakeApp is the app whose selection the fake reads: what UI Automation
// reads of it ("" for nothing) and what a copy gets.
type fakeApp struct {
	name, uia, clip string
}

// fakeSelection stands in for winselection.Begin, deciding as it does:
// nothing in an excluded app, UI Automation when it reads something, else a
// copy when the options allow one.
type fakeSelection struct {
	mu     sync.Mutex
	app    fakeApp
	begins []winselection.Options
	copies int
}

func (f *fakeSelection) begin(_ uintptr, o winselection.Options) selCapture {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.begins = append(f.begins, o)
	c := &fakeCapture{f: f}
	switch {
	case slices.ContainsFunc(o.ExcludeApps, func(a string) bool { return strings.EqualFold(a, f.app.name) }):
	case f.app.uia != "":
		c.text, c.ok = f.app.uia, true
	case o.Clipboard:
		c.needsCopy = true
	}
	return c
}

type fakeCapture struct {
	f         *fakeSelection
	needsCopy bool
	text      string
	ok        bool
}

func (c *fakeCapture) NeedsCopy() bool { return c.needsCopy }
func (c *fakeCapture) Copy() {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	c.f.copies++
	if c.f.app.clip != "" {
		c.text, c.ok = c.f.app.clip, true
	}
}
func (c *fakeCapture) Read(context.Context) (plugin.Selection, bool) {
	return plugin.Selection{Text: c.text, App: plugin.AppInfo{Name: c.f.app.name}}, c.ok
}
func (c *fakeCapture) Err() error     { return nil }
func (c *fakeCapture) CopyErr() error { return nil }

// selectionShell is a shell with the selection reader on fake, with the
// selection settings sc; its main thread is the caller's.
func selectionShell(t *testing.T, fake *fakeSelection, aware bool, sc config.SelectionConfig) *Shell {
	cfg := config.Default()
	cfg.ContextAware, cfg.Selection = aware, sc
	r := newSelectionReader(func() config.Config { return cfg }, func(fn func()) { fn() }, slog.New(slog.DiscardHandler), nil)
	r.begin = fake.begin
	r.async = func(fn func()) { fn() }
	return &Shell{ctx: t.Context(), sel: r}
}

// TestSelectionOnePath: the launcher's show and the Action Ring read the
// selected text through the same reader, with the same settings: the ring
// opening without the copy fallback, the ring's action chosen and the
// launcher with it.
func TestSelectionOnePath(t *testing.T) {
	fake := &fakeSelection{app: fakeApp{name: "chrome.exe", clip: "copied"}}
	sc := config.DefaultSelection()
	sc.Clipboard, sc.ClipboardApps, sc.ExcludeApps, sc.MaxChars = true, []string{"chat.exe"}, []string{"KeePass.exe"}, 1234
	s := selectionShell(t, fake, true, sc)

	var got selCapture
	if !s.launcherSelection(1, func(c selCapture) { got = c }) || got == nil {
		t.Fatal("the launcher did not copy")
	}
	if sel := s.ringOpenSelection(1); sel == nil || sel.NeedsCopy() {
		t.Fatal("the ring opening would copy")
	}
	var lazy ringCapture
	s.ringSelection(ringCapture{fg: 1, selHere: true}, func(c ringCapture) { lazy = c })
	if lazy.sel == nil || lazy.sel.Text != "copied" || !lazy.read {
		t.Fatalf("lazy read %+v", lazy)
	}
	if len(fake.begins) != 3 || fake.copies != 2 {
		t.Fatalf("%d begins, %d copies", len(fake.begins), fake.copies)
	}
	for i, o := range fake.begins {
		wantCopy := i != 1 // the ring opening never copies
		if o.Clipboard != wantCopy || slices.Equal(o.ClipboardApps, sc.ClipboardApps) != wantCopy ||
			!slices.Equal(o.ExcludeApps, sc.ExcludeApps) || o.MaxChars != 1234 {
			t.Errorf("begin %d: %+v", i, o)
		}
	}
}

// TestRingSelectionLazy: an action that needs the selected text, chosen
// when UI Automation read nothing: copied (the user's fallback on) and the
// chat is about it; nothing readable: a blank chat; the fallback off, or an
// excluded app: no copy; context awareness off: nothing read at all.
func TestRingSelectionLazy(t *testing.T) {
	on := config.DefaultSelection()
	on.Clipboard = true
	off := config.DefaultSelection()
	off.Clipboard = false
	for _, tc := range []struct {
		name   string
		app    fakeApp
		aware  bool
		sc     config.SelectionConfig
		text   string
		copies int
	}{
		{"UI Automation empty, copied", fakeApp{name: "chrome.exe", clip: "from chrome"}, true, on, "from chrome", 1},
		{"UI Automation", fakeApp{name: "notepad.exe", uia: "from notepad"}, true, on, "from notepad", 0},
		{"nothing readable", fakeApp{name: "chrome.exe"}, true, on, "", 1},
		{"copy fallback off", fakeApp{name: "chrome.exe", clip: "x"}, true, off, "", 0},
		{"excluded app", fakeApp{name: "KeePass.exe", clip: "secret"}, true, on, "", 0},
		{"context awareness off", fakeApp{name: "chrome.exe", clip: "x"}, false, on, "", 0},
	} {
		fake := &fakeSelection{app: tc.app}
		s := selectionShell(t, fake, tc.aware, tc.sc)
		var got ringCapture
		ran := false
		s.ringSelection(ringCapture{fg: 7, selHere: true}, func(c ringCapture) { got, ran = c, true })
		if !ran || !got.read {
			t.Fatalf("%s: not run", tc.name)
		}
		text := ""
		if got.sel != nil {
			text = got.sel.Text
		}
		if text != tc.text || fake.copies != tc.copies {
			t.Errorf("%s: %q, %d copies", tc.name, text, fake.copies)
		}
		if !tc.aware && len(fake.begins) != 0 {
			t.Errorf("%s: read with context awareness off", tc.name)
		}
		// Ask AI: a chat about the text, else a blank chat.
		e, ok := ringAskAIEntry(got.sel, nil)
		if ok != (tc.text != "") {
			t.Errorf("%s: Ask AI with a selection %v", tc.name, ok)
		}
		if ok && (!strings.Contains(string(e.Actions[0].Do.Payload), tc.text) || e.Actions[0].Do.Kind != plugin.ActionAskAI) {
			t.Errorf("%s: Ask AI %+v", tc.name, e.Actions[0].Do)
		}
	}
}

// TestRingSelectionAfterButtonUp: chosen with a middle click, the copy
// waits for the button to go up.
func TestRingSelectionAfterButtonUp(t *testing.T) {
	on := config.DefaultSelection()
	on.Clipboard = true
	fake := &fakeSelection{app: fakeApp{name: "chrome.exe", clip: "text"}}
	s := selectionShell(t, fake, true, on)
	done := make(chan ringCapture, 1)
	s.sel.post = func(fn func()) { fn() }
	m := ring.NewMachine(ring.Options{Hold: time.Hour, Threshold: 8})
	s.ring.machine = m
	m.Handle(ring.Event{Kind: ring.MiddleDown})
	if !m.ButtonDown() {
		t.Fatal("not down")
	}
	s.ringSelection(ringCapture{fg: 7, selHere: true}, func(c ringCapture) { done <- c })
	time.Sleep(3 * ringSelectionPoll)
	fake.mu.Lock()
	copies := fake.copies
	fake.mu.Unlock()
	if copies != 0 {
		t.Fatal("copied while the button is down")
	}
	m.Handle(ring.Event{Kind: ring.MiddleUp})
	select {
	case c := <-done:
		if c.sel == nil || c.sel.Text != "text" {
			t.Errorf("read %+v", c.sel)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("not read after the button went up")
	}
}

// TestRingNeedsSelection: Ask AI and the selection's entries need the
// selected text; others do not.
func TestRingNeedsSelection(t *testing.T) {
	sel := core.Entry{Selection: true}
	for _, tc := range []struct {
		slot ring.Slot
		want bool
	}{
		{ring.Slot{Key: config.RingAskAI}, true},
		{ring.Slot{Key: "builtin.ai/custom", Entry: &sel}, true},
		{ring.Slot{Key: config.RingLauncher}, false},
		{ring.Slot{Key: "builtin.files/terminal", Entry: &core.Entry{Context: true}}, false},
	} {
		if got := ringNeedsSelection(tc.slot); got != tc.want {
			t.Errorf("%q: %v", tc.slot.Key, got)
		}
	}
}

// TestRingAskAIFiles: Ask AI on a file in a folder view opens a chat with
// the file attached (R123), with the selected text as a quote when there is
// one too; without either, a blank chat.
func TestRingAskAIFiles(t *testing.T) {
	files := []string{`C:\work\report.pdf`}
	ctx := &plugin.Context{Kind: plugin.ContextExplorer, Folder: `C:\work`, Selected: files}
	e, ok := ringAskAIEntry(nil, ringFiles(ctx))
	if !ok || e.Selection {
		t.Fatalf("Ask AI on a file: %v %+v", ok, e)
	}
	var q plugin.AskAI
	if err := json.Unmarshal(e.Actions[0].Do.Payload, &q); err != nil || e.Actions[0].Do.Kind != plugin.ActionAskAI ||
		!slices.Equal(q.Files, files) || q.Text != "" {
		t.Fatalf("Ask AI on a file: %+v %v", q, err)
	}
	e, _ = ringAskAIEntry(&plugin.Selection{Text: "abc"}, files)
	if err := json.Unmarshal(e.Actions[0].Do.Payload, &q); err != nil || !e.Selection || q.Text != "abc" || !slices.Equal(q.Files, files) {
		t.Fatalf("Ask AI with text and a file: %+v %v", q, err)
	}
	if _, ok := ringAskAIEntry(nil, ringFiles(nil)); ok {
		t.Fatal("Ask AI without text or files is not a blank chat")
	}
}
