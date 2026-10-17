package winselection

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/winapi/clip"
	"github.com/eziq1/golauncher/pkg/plugin"
)

func TestDecide(t *testing.T) {
	on := Options{Clipboard: true}
	off := Options{}
	tests := []struct {
		name   string
		p      probe
		o      Options
		want   method
		reason string
	}{
		{"uia", probe{App: "notepad.exe", Element: true, TextPattern: true}, off, methodUIA, "text-pattern"},
		{"uia with clipboard on", probe{App: "notepad.exe", Element: true, TextPattern: true}, on, methodUIA, "text-pattern"},
		{"unsupported, clipboard on", probe{App: "chat.exe", Element: true}, on, methodCopy, "fallback"},
		{"unsupported, clipboard off", probe{App: "chat.exe", Element: true}, off, methodNone, "unsupported"},
		{"phase 1 timeout, clipboard on", probe{App: "chat.exe"}, on, methodCopy, "fallback"},
		{"phase 1 timeout, clipboard off", probe{App: "chat.exe"}, off, methodNone, "no-element"},
		{"terminal by exe", probe{App: "WindowsTerminal.exe", Element: true}, on, methodNone, "never-copy"},
		{"terminal by exe, other case", probe{App: "PWSH.EXE"}, on, methodNone, "never-copy"},
		{"terminal by class", probe{App: "some-shell.exe", Class: "ConsoleWindowClass"}, on, methodNone, "never-copy"},
		{"cascadia class", probe{App: "x.exe", Class: "CASCADIA_HOSTING_WINDOW_CLASS"}, on, methodNone, "never-copy"},
		{"terminal with text pattern", probe{App: "WindowsTerminal.exe", Element: true, TextPattern: true}, on, methodUIA, "text-pattern"},
		{"excel", probe{App: "excel.exe", Element: true}, on, methodNone, "never-copy"},
		{"excel in clipboard apps", probe{App: "EXCEL.EXE"}, Options{Clipboard: true, ClipboardApps: []string{"excel.exe"}}, methodNone, "never-copy"},
		{"elevated", probe{App: "chat.exe", Elevated: true}, on, methodNone, "elevated"},
		{"elevated with text pattern", probe{App: "chat.exe", Elevated: true, Element: true, TextPattern: true}, on, methodUIA, "text-pattern"},
		{"password", probe{App: "chat.exe", Element: true, Password: true, TextPattern: true}, on, methodNone, "password"},
		{"password in clipboard app", probe{App: "chat.exe", Element: true, Password: true}, Options{Clipboard: true, ClipboardApps: []string{"chat.exe"}}, methodNone, "password"},
		{"excluded", probe{App: "KeePassXC.exe", Element: true, TextPattern: true}, Options{ExcludeApps: []string{"keepassxc.EXE"}}, methodNone, "excluded"},
		{"excluded by path", probe{App: "KeePass.exe", TextPattern: true}, Options{ExcludeApps: []string{`C:\Program Files\KeePass Password Safe 2\keepass.exe`}}, methodNone, "excluded"},
		{"own process", probe{App: "golauncher.exe", Own: true, Element: true, TextPattern: true}, on, methodNone, "own-process"},
		{"clipboard app forces copy", probe{App: "Chat.exe", Element: true, TextPattern: true}, Options{Clipboard: true, ClipboardApps: []string{`D:\apps\chat.exe`}}, methodCopy, "clipboard-app"},
		{"clipboard app ignored when off", probe{App: "chat.exe", Element: true, TextPattern: true}, Options{ClipboardApps: []string{"chat.exe"}}, methodUIA, "text-pattern"},
		{"clipboard app, elevated", probe{App: "chat.exe", Elevated: true}, Options{Clipboard: true, ClipboardApps: []string{"chat.exe"}}, methodNone, "elevated"},
		{"line-copy editor skipped", probe{App: "Code.exe", Element: true}, on, methodNone, "line-copy-editor"},
		{"line-copy editor, other case", probe{App: "NOTEPAD++.EXE"}, on, methodNone, "line-copy-editor"},
		{"line-copy editor listed", probe{App: "Code.exe", Element: true}, Options{Clipboard: true, ClipboardApps: []string{"code.exe"}}, methodCopy, "clipboard-app"},
		{"line-copy editor with text pattern", probe{App: "Code.exe", Element: true, TextPattern: true}, on, methodUIA, "text-pattern"},
		{"unknown app", probe{App: ""}, on, methodCopy, "fallback"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := decide(tt.p, tt.o)
			if got != tt.want || reason != tt.reason {
				t.Errorf("decide = %v (%s), want %v (%s)", got, reason, tt.want, tt.reason)
			}
		})
	}
}

func TestElevatedThan(t *testing.T) {
	const medium, high, low = 0x2000, 0x3000, 0x1000
	for _, tt := range []struct {
		target, own uint32
		err         error
		want        bool
	}{
		{medium, medium, nil, false},
		{low, medium, nil, false},
		{high, medium, nil, true},
		{high, high, nil, false},
		{0, medium, errors.New("access denied"), true},
	} {
		if got := elevatedThan(tt.target, tt.own, tt.err); got != tt.want {
			t.Errorf("elevatedThan(%#x, %#x, %v) = %v", tt.target, tt.own, tt.err, got)
		}
	}
}

func TestTruncate(t *testing.T) {
	for _, tt := range []struct {
		s     string
		limit int
		want  string
		trunc bool
	}{
		{"hello", 10, "hello", false},
		{"hello", 5, "hello", false},
		{"hello", 4, "hell", true},
		{"hello", 0, "hello", false},
		{"", 3, "", false},
		{"你好世界", 2, "你好", true},
		{"你好世界", 4, "你好世界", false},
		{"a😀b", 2, "a😀", true},
		{"a😀b", 1, "a", true},
		{"😀😀", 1, "😀", true},
	} {
		got, trunc := Truncate(tt.s, tt.limit)
		if got != tt.want || trunc != tt.trunc {
			t.Errorf("Truncate(%q, %d) = %q, %v; want %q, %v", tt.s, tt.limit, got, trunc, tt.want, tt.trunc)
		}
	}
}

func TestJoinRanges(t *testing.T) {
	for _, tt := range []struct {
		parts []string
		limit int
		want  string
		trunc bool
	}{
		{nil, 10, "", false},
		{[]string{"one"}, 10, "one", false},
		{[]string{"one", "two"}, 10, "one\ntwo", false},
		{[]string{"one", "", "two"}, 10, "one\ntwo", false},
		{[]string{"one", "two"}, 5, "one\nt", true},
		{[]string{"一二", "三四"}, 4, "一二\n三", true},
	} {
		got, trunc := joinRanges(tt.parts, tt.limit)
		if got != tt.want || trunc != tt.trunc {
			t.Errorf("joinRanges(%q, %d) = %q, %v; want %q, %v", tt.parts, tt.limit, got, trunc, tt.want, tt.trunc)
		}
	}
}

func TestUTF16Text(t *testing.T) {
	// "a😀" is a, D83D, DE00; cut after the high surrogate.
	if got := utf16Text([]uint16{'a', 0xD83D}); got != "a" {
		t.Errorf("cut pair = %q", got)
	}
	if got := utf16Text([]uint16{'a', 0xD83D, 0xDE00}); got != "a😀" {
		t.Errorf("pair = %q", got)
	}
	if got := utf16Text(nil); got != "" {
		t.Errorf("nil = %q", got)
	}
	// readUnits leaves room for limit+1 astral characters.
	if readUnits(3) < 2*4 {
		t.Errorf("readUnits(3) = %d", readUnits(3))
	}
}

func TestBaseNameListed(t *testing.T) {
	if !listed("notepad.exe", []string{`C:\Windows\System32\NOTEPAD.EXE`}) {
		t.Error("path entry not matched")
	}
	if !listed(`C:\x\Notepad.exe`, []string{"notepad.exe"}) {
		t.Error("path app not matched")
	}
	if listed("", []string{""}) || listed("note.exe", []string{"notepad.exe"}) {
		t.Error("unexpected match")
	}
}

// fakeElem is an element returning fixed ranges.
type fakeElem struct {
	ranges   []string
	editable bool
	err      error
	delay    time.Duration
	reads    atomic.Int32
	released atomic.Int32
}

func (e *fakeElem) read(ctx context.Context, units int) ([]string, bool, error) {
	e.reads.Add(1)
	if e.delay > 0 {
		select {
		case <-time.After(e.delay):
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
	return e.ranges, e.editable, e.err
}

func (e *fakeElem) release() { e.released.Add(1) }

const (
	testPID   = 4242
	targetPID = 1234
)

// fakeEnv replaces the platform functions for one test.
type fakeEnv struct {
	window   windowInfo
	apps     map[int]string
	focus    focusInfo
	focusErr error
	delay    time.Duration
	copies   atomic.Int32
	copyRes  clip.CopyResult
	copyErr  error
	permErr  error // permission()
}

func (f *fakeEnv) install(t *testing.T) {
	t.Helper()
	oldW, oldP, oldF, oldC, oldOwn, oldUIA, oldPerm := windowFacts, processFacts, focusProbe, copySelection, ownPID, uia, permission
	w := &worker{idle: 50 * time.Millisecond}
	t.Cleanup(func() {
		<-w.do(func() {}) // abandoned probes finish before the globals change back
		windowFacts, processFacts, focusProbe, copySelection, ownPID, uia, permission = oldW, oldP, oldF, oldC, oldOwn, oldUIA, oldPerm
	})
	permission = func() error { return f.permErr }
	ownPID = testPID
	uia = w
	windowFacts = func(uintptr) windowInfo { return f.window }
	processFacts = func(pid int) plugin.AppInfo {
		name := f.apps[pid]
		return plugin.AppInfo{Name: name, Path: `C:\apps\` + name, PID: pid}
	}
	focusProbe = func() (focusInfo, error) {
		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		return f.focus, f.focusErr
	}
	copySelection = func(hwnd uintptr, o clip.CopyOptions) (clip.CopyResult, error) {
		f.copies.Add(1)
		if o.Timeout != copyTimeout || o.MaxChars <= 0 {
			t.Errorf("CopySelection options %+v", o)
		}
		return f.copyRes, f.copyErr
	}
}

func newEnv(app string) *fakeEnv {
	return &fakeEnv{
		window: windowInfo{Valid: true, PID: targetPID, Class: "AppWindow"},
		apps:   map[int]string{targetPID: app, testPID: "golauncher.exe"},
	}
}

func TestReadNilPending(t *testing.T) {
	var p *Pending
	if _, ok := p.Read(t.Context()); ok {
		t.Error("nil Pending read something")
	}
	if p.NeedsCopy() || p.CopyErr() != nil || p.Method() != "none" {
		t.Error("nil Pending accessors")
	}
	p.Copy() // must not panic
}

func TestBeginUIA(t *testing.T) {
	f := newEnv("notepad.exe")
	e := &fakeElem{ranges: []string{"hello ", "world"}, editable: true}
	f.focus = focusInfo{PID: targetPID, TextPattern: true, elem: e}
	f.install(t)

	p := Begin(1, Options{})
	if p.Method() != plugin.SelectionUIA || p.NeedsCopy() {
		t.Fatalf("method = %s", p.Method())
	}
	sel, ok := p.Read(t.Context())
	if !ok {
		t.Fatal("nothing read")
	}
	want := plugin.Selection{Text: "hello \nworld", App: plugin.AppInfo{Name: "notepad.exe", Path: `C:\apps\notepad.exe`, PID: targetPID},
		Source: plugin.SelectionUIA, Editable: true}
	if sel != want {
		t.Errorf("Read = %+v, want %+v", sel, want)
	}
	if e.released.Load() != 1 {
		t.Errorf("released %d times", e.released.Load())
	}
	// A second Read returns the same result without reading again.
	if sel2, ok := p.Read(t.Context()); !ok || sel2 != sel || e.reads.Load() != 1 {
		t.Errorf("second Read = %+v, %v (reads %d)", sel2, ok, e.reads.Load())
	}
}

func TestBeginUIATruncated(t *testing.T) {
	f := newEnv("notepad.exe")
	f.focus = focusInfo{PID: targetPID, TextPattern: true, elem: &fakeElem{ranges: []string{"abcdef"}}}
	f.install(t)
	sel, ok := Begin(1, Options{MaxChars: 4}).Read(t.Context())
	if !ok || sel.Text != "abcd" || !sel.Truncated {
		t.Errorf("Read = %+v, %v", sel, ok)
	}
}

func TestBeginUIAEmptySelection(t *testing.T) {
	for _, ranges := range [][]string{nil, {""}, {"  \n\t"}} {
		f := newEnv("notepad.exe")
		f.focus = focusInfo{PID: targetPID, TextPattern: true, elem: &fakeElem{ranges: ranges}}
		f.install(t)
		// Clipboard on: an empty selection in a TextPattern control never
		// falls back to Ctrl+C.
		p := Begin(1, Options{Clipboard: true})
		if p.NeedsCopy() {
			t.Fatal("empty TextPattern selection chose the clipboard")
		}
		if sel, ok := p.Read(t.Context()); ok {
			t.Errorf("ranges %q: Read = %+v", ranges, sel)
		}
		if f.copies.Load() != 0 {
			t.Error("copied")
		}
	}
}

func TestBeginUIAReadError(t *testing.T) {
	f := newEnv("notepad.exe")
	e := &fakeElem{err: errors.New("gone")}
	f.focus = focusInfo{PID: targetPID, TextPattern: true, elem: e}
	f.install(t)
	if _, ok := Begin(1, Options{}).Read(t.Context()); ok {
		t.Error("read error returned a selection")
	}
	if e.released.Load() != 1 {
		t.Error("not released")
	}
}

func TestReadTimeout(t *testing.T) {
	f := newEnv("notepad.exe")
	e := &fakeElem{ranges: []string{"late"}, delay: 300 * time.Millisecond}
	f.focus = focusInfo{PID: targetPID, TextPattern: true, elem: e}
	f.install(t)
	p := Begin(1, Options{})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, ok := p.Read(ctx); ok {
		t.Error("late read returned a selection")
	}
	// The element is still released on the UIA thread.
	waitFor(t, func() bool { return e.released.Load() == 1 })
}

func TestBeginPassword(t *testing.T) {
	f := newEnv("notepad.exe")
	e := &fakeElem{ranges: []string{"secret"}}
	f.focus = focusInfo{PID: targetPID, Password: true, TextPattern: true, elem: e}
	f.install(t)
	p := Begin(1, Options{Clipboard: true})
	if p.Method() != "none" {
		t.Errorf("method = %s", p.Method())
	}
	if _, ok := p.Read(t.Context()); ok || e.reads.Load() != 0 {
		t.Error("password field read")
	}
	waitFor(t, func() bool { return e.released.Load() == 1 })
}

func TestBeginOwnElement(t *testing.T) {
	f := newEnv("notepad.exe")
	f.focus = focusInfo{PID: testPID, TextPattern: true, elem: &fakeElem{ranges: []string{"x"}}}
	f.install(t)
	if p := Begin(1, Options{Clipboard: true}); p.Method() != "none" {
		t.Errorf("own element: method = %s", p.Method())
	}
}

func TestBeginOwnWindowAndExcluded(t *testing.T) {
	probed := atomic.Int32{}
	for _, tt := range []struct {
		name string
		pid  int
		o    Options
	}{
		{"own window", testPID, Options{}},
		{"excluded", targetPID, Options{ExcludeApps: []string{"KEEPASS.EXE"}}},
	} {
		f := newEnv("KeePass.exe")
		f.window.PID = tt.pid
		f.install(t)
		focusProbe = func() (focusInfo, error) { probed.Add(1); return focusInfo{}, nil }
		if p := Begin(1, tt.o); p.Method() != "none" {
			t.Errorf("%s: method = %s", tt.name, p.Method())
		}
	}
	if probed.Load() != 0 {
		t.Error("UI Automation was queried")
	}
}

func TestBeginElementOfOtherProcess(t *testing.T) {
	// The focused element belongs to another process than the window (a
	// hosted app): App names the element's process, which is excluded.
	f := newEnv("host.exe")
	f.apps[999] = "Vault.exe"
	e := &fakeElem{ranges: []string{"x"}}
	f.focus = focusInfo{PID: 999, TextPattern: true, elem: e}
	f.install(t)
	if p := Begin(1, Options{ExcludeApps: []string{"vault.exe"}}); p.Method() != "none" {
		t.Errorf("method = %s", p.Method())
	}
	f.apps[999] = "Editor.exe"
	f.focus.elem = &fakeElem{ranges: []string{"x"}}
	sel, ok := Begin(1, Options{}).Read(t.Context())
	if !ok || sel.App.Name != "Editor.exe" || sel.App.PID != 999 {
		t.Errorf("Read = %+v, %v", sel, ok)
	}
}

func TestBeginNoWindow(t *testing.T) {
	f := newEnv("notepad.exe")
	f.window = windowInfo{}
	f.install(t)
	if p := Begin(1, Options{Clipboard: true}); p.Method() != "none" {
		t.Error("invalid window")
	}
	if p := Begin(0, Options{Clipboard: true}); p.Method() != "none" {
		t.Error("no window")
	}
}

func TestBeginTimeout(t *testing.T) {
	f := newEnv("chat.exe")
	e := &fakeElem{ranges: []string{"x"}}
	f.focus = focusInfo{PID: targetPID, TextPattern: true, elem: e}
	f.delay = FocusBudget + 100*time.Millisecond
	f.install(t)

	start := time.Now()
	p := Begin(1, Options{})
	if took := time.Since(start); took > FocusBudget+40*time.Millisecond {
		t.Errorf("Begin took %v", took)
	}
	if p.Method() != "none" {
		t.Errorf("timeout, clipboard off: method = %s", p.Method())
	}
	// The late element is discarded and released.
	waitFor(t, func() bool { return e.released.Load() == 1 })

	// With the clipboard on, a timeout falls back to copying.
	f.focus.elem = &fakeElem{}
	if p := Begin(1, Options{Clipboard: true}); !p.NeedsCopy() {
		t.Errorf("timeout, clipboard on: method = %s", p.Method())
	}
}

func TestCopy(t *testing.T) {
	f := newEnv("chat.exe")
	f.focus = focusInfo{PID: targetPID}
	f.copyRes = clip.CopyResult{Text: "copied text", Restored: true}
	f.install(t)

	p := Begin(1, Options{Clipboard: true})
	if !p.NeedsCopy() || p.Method() != plugin.SelectionClipboard {
		t.Fatalf("method = %s", p.Method())
	}
	p.Copy()
	p.Copy() // once only
	if f.copies.Load() != 1 {
		t.Errorf("copied %d times", f.copies.Load())
	}
	sel, ok := p.Read(t.Context())
	want := plugin.Selection{Text: "copied text", App: plugin.AppInfo{Name: "chat.exe", Path: `C:\apps\chat.exe`, PID: targetPID}, Source: plugin.SelectionClipboard}
	if !ok || sel != want {
		t.Errorf("Read = %+v, %v; want %+v", sel, ok, want)
	}
	if p.CopyErr() != nil {
		t.Errorf("CopyErr = %v", p.CopyErr())
	}
}

func TestCopyTruncated(t *testing.T) {
	f := newEnv("chat.exe")
	f.copyRes = clip.CopyResult{Text: "0123456789"}
	f.focusErr = errNoFocus
	f.install(t)
	p := Begin(1, Options{Clipboard: true, MaxChars: 3})
	p.Copy()
	if sel, ok := p.Read(t.Context()); !ok || sel.Text != "012" || !sel.Truncated {
		t.Errorf("Read = %+v, %v", sel, ok)
	}
}

var errNoFocus = errors.New("no focus")

func TestCopyErrors(t *testing.T) {
	for _, tt := range []struct {
		res  clip.CopyResult
		err  error
		ok   bool
		text string
	}{
		{clip.CopyResult{}, clip.ErrNoSelection, false, ""},
		{clip.CopyResult{}, clip.ErrCannotSave, false, ""},
		{clip.CopyResult{Text: "   "}, nil, false, ""},
		{clip.CopyResult{Text: "kept"}, errors.New("not restored"), true, "kept"},
	} {
		f := newEnv("chat.exe")
		f.copyRes, f.copyErr = tt.res, tt.err
		f.install(t)
		p := Begin(1, Options{Clipboard: true})
		p.Copy()
		sel, ok := p.Read(t.Context())
		if ok != tt.ok || sel.Text != tt.text {
			t.Errorf("%v: Read = %+v, %v", tt.err, sel, ok)
		}
		if !errors.Is(p.CopyErr(), tt.err) {
			t.Errorf("CopyErr = %v, want %v", p.CopyErr(), tt.err)
		}
	}
}

func TestReadWithoutCopy(t *testing.T) {
	f := newEnv("chat.exe")
	f.install(t)
	p := Begin(1, Options{Clipboard: true})
	if !p.NeedsCopy() {
		t.Fatal("no copy")
	}
	if _, ok := p.Read(t.Context()); ok {
		t.Error("Read without Copy returned a selection")
	}
	if f.copies.Load() != 0 {
		t.Error("Read copied")
	}
}

func TestNeverCopyNotCopied(t *testing.T) {
	f := newEnv("WindowsTerminal.exe")
	f.install(t)
	p := Begin(1, Options{Clipboard: true})
	p.Copy()
	if p.NeedsCopy() || f.copies.Load() != 0 {
		t.Error("terminal copied")
	}
}

func TestWorkerIdleRestart(t *testing.T) {
	var inits, teardowns atomic.Int32
	w := &worker{idle: 20 * time.Millisecond, init: func() func() {
		inits.Add(1)
		return func() { teardowns.Add(1) }
	}}
	var order []int
	for i := range 5 {
		w.do(func() { order = append(order, i) })
	}
	<-w.do(func() {})
	if len(order) != 5 {
		t.Fatalf("order = %v", order)
	}
	for i, v := range order {
		if v != i {
			t.Fatalf("order = %v", order)
		}
	}
	waitFor(t, func() bool { return teardowns.Load() == 1 })
	<-w.do(func() {})
	if inits.Load() != 2 {
		t.Errorf("inits = %d", inits.Load())
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("condition not met in time")
}

// Without permission to read other apps (macOS Accessibility) nothing is
// probed or copied, and Err says why.
func TestNoPermission(t *testing.T) {
	f := newEnv("chat.exe")
	f.permErr = errors.New("no Accessibility")
	f.install(t)
	p := Begin(1, Options{Clipboard: true})
	if p.NeedsCopy() || !errors.Is(p.Err(), f.permErr) {
		t.Fatalf("method %s, err %v", p.Method(), p.Err())
	}
	if _, ok := p.Read(t.Context()); ok {
		t.Error("read without permission")
	}
	if (*Pending)(nil).Err() != nil {
		t.Error("nil Pending has an error")
	}
	// An excluded or own app is skipped before permissions matter.
	f.apps[targetPID] = "KeePass.exe"
	if p := Begin(1, Options{ExcludeApps: []string{"KeePass.exe"}}); p.Err() != nil {
		t.Errorf("excluded app: %v", p.Err())
	}
}

// On macOS the window class is the bundle identifier: VS Code (whose
// executable is "Electron") copies a line, Excel is never copied from.
func TestMacBundles(t *testing.T) {
	o := Options{Clipboard: true}
	for _, tt := range []struct {
		bundle string
		want   method
		reason string
	}{
		{"com.microsoft.VSCode", methodNone, "line-copy-editor"},
		{"com.jetbrains.goland", methodNone, "line-copy-editor"},
		{"com.jetbrainsx", methodCopy, "fallback"},
		{"com.microsoft.Excel", methodNone, "never-copy"},
		{"com.apple.Safari", methodCopy, "fallback"},
	} {
		m, reason := decide(probe{App: "Electron", Element: true, Class: tt.bundle}, o)
		if m != tt.want || reason != tt.reason {
			t.Errorf("%s: %v %s", tt.bundle, m, reason)
		}
	}
}
