// Package winselection reads the text selected in the window that was in
// the foreground when the launcher hotkey was pressed
// (docs/selected-text.md): through UI Automation's TextPattern, without side
// effects, and — when the user turned it on — with a simulated Ctrl+C for
// apps UI Automation cannot read (the clipboard restored afterwards, see
// clip.CopySelection).
//
// A capture has two phases. Begin runs on the GUI thread before the
// launcher takes the focus: it finds the focused element (at most
// FocusBudget) and decides the method. Read runs later in the background and
// reads the selected text. The text is never logged: logs carry only the
// method, the app name, lengths, timings and error kinds.
package winselection

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/winapi/clip"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// FocusBudget is the most time Begin waits for the focused element.
const FocusBudget = 50 * time.Millisecond

// copyTimeout is how long the clipboard method waits for the window to
// write the clipboard after Ctrl+C.
const copyTimeout = 200 * time.Millisecond

// uiaIdle is how long the UI Automation thread stays alive without work.
const uiaIdle = 30 * time.Second

// chromiumRetry is the pause before the second GetSelection when the first
// returned no ranges (Chromium builds its accessibility tree on the first
// query).
const chromiumRetry = 30 * time.Millisecond

// Options configure a capture; they are filled from config.SelectionConfig.
type Options struct {
	// MaxChars truncates the text to this many characters (runes); 0 is
	// config.DefaultSelectionMaxChars.
	MaxChars int
	// ExcludeApps are executables whose selection is never read, compared
	// case-insensitively by base name (entries may be full paths).
	ExcludeApps []string
	// Clipboard falls back to the clipboard method (a simulated Ctrl+C)
	// when UI Automation cannot read the app.
	Clipboard bool
	// ClipboardApps are executables where the clipboard method is used
	// directly; only with Clipboard on.
	ClipboardApps []string
	// Log receives what was done (method, app name, lengths, timings, error
	// kinds) — never the text. It may be nil.
	Log *slog.Logger
}

func (o Options) maxChars() int {
	if o.MaxChars <= 0 {
		return config.DefaultSelectionMaxChars
	}
	return o.MaxChars
}

// method is how the selection of a capture is read.
type method int

const (
	methodNone method = iota // nothing is read
	methodUIA                // UI Automation TextPattern
	methodCopy               // simulated Ctrl+C
)

func (m method) String() string {
	switch m {
	case methodUIA:
		return plugin.SelectionUIA
	case methodCopy:
		return plugin.SelectionClipboard
	}
	return "none"
}

// probe holds what phase 1 learned about the target.
type probe struct {
	// App is the executable name of the process owning the focused element
	// (or the window when no element was found).
	App string
	// Own: the window or the focused element belongs to this process.
	Own bool
	// Elevated: the window's process runs at a higher integrity level than
	// this one (or could not be opened), so simulated keys would be dropped.
	Elevated bool
	// Element: a focused element was found in time.
	Element bool
	// Password: the focused element is a password field.
	Password bool
	// TextPattern: the focused element supports the TextPattern.
	TextPattern bool
	// Edit: the focused element is a text field (Windows; false on macOS).
	Edit bool
	// Class is the window class of the top-level window (on macOS, the
	// app's bundle identifier).
	Class string
}

// neverCopyApps never get a simulated Ctrl+C: in terminals it interrupts
// the running program when nothing is selected; in Excel it leaves the
// "copy mode" marquee and the clipboard data is delay-rendered.
var neverCopyApps = []string{
	"WindowsTerminal.exe", "OpenConsole.exe", "conhost.exe", "cmd.exe",
	"powershell.exe", "pwsh.exe", "wt.exe", "wezterm-gui.exe", "alacritty.exe",
	"mintty.exe", "putty.exe", "kitty.exe", "Tabby.exe", "Hyper.exe",
	"ConEmu.exe", "ConEmu64.exe", "MobaXterm.exe",
	"EXCEL.EXE",
}

// neverCopyClasses are console window classes, never sent a Ctrl+C, and on
// macOS (where the class is the app's bundle identifier) Excel, for the
// reason above.
var neverCopyClasses = []string{"ConsoleWindowClass", "CASCADIA_HOSTING_WINDOW_CLASS", "PseudoConsoleWindow", "com.microsoft.Excel"}

// lineCopyApps copy the whole current line on Ctrl+C when nothing is
// selected, so the clipboard method would take that line for the selection.
// They are only copied from when listed in Options.ClipboardApps.
var lineCopyApps = []string{
	"Code.exe", "Code - Insiders.exe", "Cursor.exe", "idea64.exe", "pycharm64.exe",
	"goland64.exe", "webstorm64.exe", "rider64.exe", "clion64.exe", "devenv.exe",
	"sublime_text.exe", "notepad++.exe",
}

// lineCopyBundles are the macOS editors of lineCopyApps, by bundle
// identifier (VS Code's executable is "Electron"); an entry ending in "."
// is a prefix.
var lineCopyBundles = []string{
	"com.microsoft.VSCode", "com.microsoft.VSCodeInsiders", "com.todesktop.230313mzl4w4u92", // Cursor
	"com.sublimetext.3", "com.sublimetext.4", "com.jetbrains.", "com.google.android.studio",
}

// lineMarkApps and lineMarkBundles are the editors of lineCopyApps and
// lineCopyBundles that mark the copy of their line (VS Code and its forks:
// "isFromEmptySelection" in their editor data, R132): they are copied from,
// and a marked copy counts as no selection. Their panels that show web
// pages (Claude Code's, a Markdown preview) keep UI Automation from the
// selection, so the clipboard is the only way there.
var (
	lineMarkApps    = []string{"Code.exe", "Code - Insiders.exe", "Cursor.exe", "VSCodium.exe"}
	lineMarkBundles = []string{"com.microsoft.VSCode", "com.microsoft.VSCodeInsiders", "com.todesktop.230313mzl4w4u92", "com.vscodium"}
)

// marksLine reports whether p's app marks the copy of its line.
func marksLine(p probe) bool {
	return listed(p.App, lineMarkApps) || p.Class != "" && slices.Contains(lineMarkBundles, p.Class)
}

// lineCopyBundle reports whether bundle is one of lineCopyBundles.
func lineCopyBundle(bundle string) bool {
	return bundle != "" && slices.ContainsFunc(lineCopyBundles, func(b string) bool {
		if strings.HasSuffix(b, ".") {
			return strings.HasPrefix(bundle, b)
		}
		return bundle == b
	})
}

// uiaUnsureClasses are the windows, by top-level class or macOS bundle
// identifier (prefixes), whose UI Automation may report an empty selection
// over selected text: the web pages of Chromium (Chrome, Edge, Electron
// apps) and Firefox, whose accessibility tree is built late and often
// leaves the page's selection out. The first capture there fell back to
// the clipboard (the focus probe timed out while the tree was built), the
// next ones found a TextPattern and read nothing (R130). There an empty
// UI Automation read falls back to the clipboard method when it is on.
var uiaUnsureClasses = []string{
	"Chrome_WidgetWin_", "MozillaWindowClass",
	"com.google.Chrome", "com.microsoft.edgemac", "com.brave.Browser", "org.mozilla.firefox",
}

// uiaUnsure reports whether class is one of uiaUnsureClasses.
func uiaUnsure(class string) bool {
	return class != "" && slices.ContainsFunc(uiaUnsureClasses, func(c string) bool { return strings.HasPrefix(class, c) })
}

// uiaCheckBudget bounds reading the UI Automation selection in phase 1 for
// a window of uiaUnsureClasses, to fall back to the clipboard before the
// launcher takes the focus.
const uiaCheckBudget = 150 * time.Millisecond

// baseName returns the file name of an executable name or path, for
// case-insensitive comparison.
func baseName(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexAny(s, `\/`); i >= 0 {
		s = s[i+1:]
	}
	return filepath.Base(s)
}

// listed reports whether app is in list, comparing base names
// case-insensitively.
func listed(app string, list []string) bool {
	if app == "" {
		return false
	}
	app = baseName(app)
	return slices.ContainsFunc(list, func(e string) bool { return strings.EqualFold(baseName(e), app) })
}

// decide picks the method for a capture from the facts of phase 1, and
// the reason (for the log).
func decide(p probe, o Options) (method, string) {
	switch {
	case listed(p.App, o.ExcludeApps):
		return methodNone, "excluded"
	case p.Own:
		return methodNone, "own-process"
	case p.Password:
		return methodNone, "password"
	}
	neverCopy := neverCopies(p)
	copyAllowed := !neverCopy && !p.Elevated && !p.Password
	if o.Clipboard && copyAllowed && listed(p.App, o.ClipboardApps) {
		return methodCopy, "clipboard-app"
	}
	switch {
	case p.TextPattern:
		return methodUIA, "text-pattern"
	case !o.Clipboard:
		if !p.Element {
			return methodNone, "no-element"
		}
		return methodNone, "unsupported"
	case neverCopy:
		return methodNone, "never-copy"
	case p.Elevated:
		return methodNone, "elevated"
	case copiesLine(p):
		return methodNone, "line-copy-editor"
	}
	return methodCopy, "fallback"
}

// neverCopies reports whether p's app never gets a simulated Ctrl+C.
func neverCopies(p probe) bool {
	return listed(p.App, neverCopyApps) || slices.Contains(neverCopyClasses, p.Class)
}

// copiesLine reports whether a Ctrl+C in p's app may take more than the
// selection when nothing is selected: an editor of lineCopyApps, unless it
// marks the copy of its line (marksLine) and the focus is not in a text
// field. VS Code's code editor and terminal take the focus in hidden text
// fields: with nothing selected, Ctrl+C copies the line in the editor and
// interrupts the running program in the terminal, so neither is copied
// from; its panels showing web pages (Claude Code's) are.
func copiesLine(p probe) bool {
	if !listed(p.App, lineCopyApps) && !lineCopyBundle(p.Class) {
		return false
	}
	return !marksLine(p) || p.Edit
}

// copyFallback reports whether the clipboard method may stand in for an
// empty UI Automation read in p's window (R130): it is on, keys reach the
// app, and a Ctrl+C there copies a selection only.
func copyFallback(p probe, o Options) bool {
	return o.Clipboard && !neverCopies(p) && !p.Elevated && !p.Password && !copiesLine(p)
}

// elevatedThan reports whether a target process of integrity level
// target (a mandatory label RID) runs above our level own; err is the
// error of reading the target's level: a process that cannot be read is
// treated as elevated.
func elevatedThan(target, own uint32, err error) bool {
	return err != nil || target > own
}

// Truncate shortens s to at most limit characters (runes), never splitting
// one, and reports whether it did. limit <= 0 means no limit.
func Truncate(s string, limit int) (string, bool) {
	if limit <= 0 {
		return s, false
	}
	n := 0
	for i := range s {
		if n == limit {
			return s[:i], true
		}
		n++
	}
	return s, false
}

// joinRanges joins the texts of a multi-range selection with newlines,
// skipping empty ranges, and truncates the result to limit characters.
func joinRanges(parts []string, limit int) (string, bool) {
	parts = slices.DeleteFunc(slices.Clone(parts), func(s string) bool { return s == "" })
	return Truncate(strings.Join(parts, "\n"), limit)
}

// utf16Text converts UTF-16 text read from another process to a string. A
// high surrogate left alone at the end (the text was cut at a length
// limit) is dropped instead of becoming U+FFFD.
func utf16Text(u []uint16) string {
	if n := len(u); n > 0 && utf16.IsSurrogate(rune(u[n-1])) && u[n-1] < 0xDC00 {
		u = u[:n-1]
	}
	return string(utf16.Decode(u))
}

// readUnits is the most UTF-16 units to request for limit characters: enough
// for limit+1 characters even when all are outside the BMP, so that
// truncation is detected exactly.
func readUnits(limit int) int { return 2*limit + 2 }

// windowInfo describes the target window, read on the caller's thread.
type windowInfo struct {
	Valid    bool
	PID      int
	Path     string
	Class    string // class of the top-level window
	Elevated bool
}

// focusInfo describes the focused element found by phase 1. elem is
// non-nil when the element supports the TextPattern; it must be used and
// released on the UI Automation thread only.
type focusInfo struct {
	PID         int
	Password    bool
	TextPattern bool
	// Edit: the element is a text field (UI Automation's Edit control type).
	Edit bool
	elem element
}

// element is the focused element kept from phase 1 for phase 2. Its
// methods run on the UI Automation thread.
type element interface {
	// read returns the texts of the selected ranges, each at most units
	// UTF-16 units, and whether the control is editable.
	read(ctx context.Context, units int) (ranges []string, editable bool, err error)
	// release frees the element; later reads fail.
	release()
}

// Replaceable for tests; set by the platform files.
var (
	// windowFacts describes hwnd; it runs on the caller's thread and must
	// be fast (no cross-process calls).
	windowFacts = func(hwnd uintptr) windowInfo { return windowInfo{} }
	// processFacts describes process pid (path).
	processFacts = func(pid int) plugin.AppInfo { return plugin.AppInfo{PID: pid} }
	// focusProbe finds the focused element; it runs on the UI Automation
	// thread.
	focusProbe = func() (focusInfo, error) { return focusInfo{}, errors.ErrUnsupported }
	// copySelection runs the clipboard method.
	copySelection = clip.CopySelection
	// permission reports why other apps cannot be read at all (macOS: the
	// Accessibility permission is missing), or nil.
	permission = func() error { return nil }
	// ownPID is this process.
	ownPID = os.Getpid()
	// uia is the UI Automation thread.
	uia = &worker{idle: uiaIdle}
)

// errReleased is returned by an element read after it was released.
var errReleased = errors.New("winselection: element released")

// Pending is a capture between its two phases.
type Pending struct {
	hwnd   uintptr
	opts   Options
	log    *slog.Logger
	method method
	app    plugin.AppInfo
	elem   element // methodUIA only
	// lineMark: the app marks the copy of its line (marksLine), which Copy
	// then does not take for a selection.
	lineMark bool

	err error // permission() of Begin

	mu      sync.Mutex
	copied  bool  // Copy ran
	copyErr error // error of Copy
	done    bool  // Read ran or Copy produced the result
	sel     plugin.Selection
	ok      bool
}

// Begin is phase 1, called on the GUI thread right after the hotkey and
// before the launcher takes the focus. It finds the focused UI Automation
// element of hwnd's process within FocusBudget (on the package's UI
// Automation thread; a late result is discarded), checks it is not this
// process or a password field and whether it supports the TextPattern, and
// decides the method.
func Begin(hwnd uintptr, o Options) *Pending {
	start := time.Now()
	p := &Pending{hwnd: hwnd, opts: o, log: o.Log}
	if p.log == nil {
		p.log = slog.New(slog.DiscardHandler)
	}
	if hwnd == 0 {
		return p
	}
	w := windowFacts(hwnd)
	if !w.Valid {
		p.log.Debug("selection skipped", "reason", "no-window")
		return p
	}
	p.app = processFacts(w.PID)
	pr := probe{App: p.app.Name, Own: w.PID == ownPID, Elevated: w.Elevated, Class: w.Class}
	if m, reason := decide(pr, o); pr.Own || listed(pr.App, o.ExcludeApps) {
		p.log.Debug("selection skipped", "reason", reason, "method", m, "app", pr.App)
		return p
	}
	if p.err = permission(); p.err != nil {
		p.log.Debug("selection skipped", "reason", "no-permission", "app", pr.App)
		return p
	}
	fi, ok := probeFocus(FocusBudget)
	if ok {
		pr.Element = true
		pr.Password = fi.Password
		pr.Edit = fi.Edit
		pr.TextPattern = fi.TextPattern && fi.elem != nil
		if fi.PID == ownPID {
			pr.Own = true
		} else if fi.PID != 0 && fi.PID != w.PID {
			p.app = processFacts(fi.PID)
			pr.App = p.app.Name
		}
	}
	var reason string
	p.method, reason = decide(pr, o)
	p.lineMark = marksLine(pr)
	if p.method == methodUIA {
		p.elem = fi.elem
	} else if fi.elem != nil {
		elem := fi.elem
		uia.do(elem.release)
	}
	if p.method == methodUIA && uiaUnsure(pr.Class) && copyFallback(pr, o) {
		// Read the selection now, while the window still has the focus: if
		// UI Automation finds none, the clipboard method looks (R130).
		ctx, cancel := context.WithTimeout(context.Background(), uiaCheckBudget)
		_, ok := p.Read(ctx)
		cancel()
		if !ok {
			p.mu.Lock()
			p.method, p.done = methodCopy, false
			p.mu.Unlock()
			reason = "uia-empty"
		}
	}
	p.log.Debug("selection begin", "method", p.method, "reason", reason, "app", pr.App,
		"element", pr.Element, "took", time.Since(start))
	return p
}

// probeFocus runs focusProbe on the UI Automation thread and waits at most
// budget. A result arriving later is released there.
func probeFocus(budget time.Duration) (focusInfo, bool) {
	var (
		mu        sync.Mutex
		abandoned bool
		res       focusInfo
		err       error
	)
	done := uia.do(func() {
		fi, e := focusProbe()
		mu.Lock()
		defer mu.Unlock()
		if abandoned {
			if fi.elem != nil {
				fi.elem.release()
			}
			return
		}
		res, err = fi, e
	})
	t := time.NewTimer(budget)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
		mu.Lock()
		defer mu.Unlock()
		select {
		case <-done: // finished meanwhile
		default:
			abandoned = true
			return focusInfo{}, false
		}
	}
	return res, err == nil
}

// NeedsCopy reports that the clipboard method was chosen: the caller must
// call Copy before it shows the launcher (off the GUI thread).
func (p *Pending) NeedsCopy() bool { return p != nil && p.method == methodCopy }

// Copy runs the clipboard method: a simulated Ctrl+C in the window, the
// clipboard restored afterwards. It blocks for about 50–300ms. It does
// nothing unless NeedsCopy.
func (p *Pending) Copy() {
	if !p.NeedsCopy() {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.copied {
		return
	}
	p.copied = true
	start := time.Now()
	limit := p.opts.maxChars()
	r, err := copySelection(p.hwnd, clip.CopyOptions{Timeout: copyTimeout, MaxChars: limit, LineMark: p.lineMark})
	took := time.Since(start)
	p.copyErr = err
	if r.FromEmptySelection {
		// The editor copied its current line: nothing was selected.
		p.done = true
		p.log.Debug("selection empty", "reason", "line-copy", "method", methodCopy, "app", p.app.Name, "took", took)
		return
	}
	if err != nil {
		p.log.Debug("selection copy error", "reason", copyErrorKind(err), "method", methodCopy, "app", p.app.Name, "took", took)
		if r.Text == "" {
			return
		}
		// Text with an error (e.g. the clipboard was not restored): the
		// text is still the selection.
	}
	text, truncated := Truncate(r.Text, limit)
	p.done = true
	if strings.TrimSpace(text) == "" {
		p.log.Debug("selection empty", "method", methodCopy, "app", p.app.Name, "took", took)
		return
	}
	p.sel = plugin.Selection{Text: text, App: p.app, Source: plugin.SelectionClipboard, Truncated: truncated || r.Truncated}
	p.ok = true
	p.log.Debug("selection captured", "source", methodCopy, "app", p.app.Name, "chars", utf8.RuneCountInString(text),
		"truncated", p.sel.Truncated, "restored", r.Restored, "save", r.Save, "wait", r.Wait, "restore", r.Restore, "took", took)
}

// Err reports why Begin could not read other apps at all (on macOS,
// ax.ErrNotTrusted: the Accessibility permission is missing), so that the
// user can be told; nil otherwise. It may be called on a nil *Pending.
func (p *Pending) Err() error {
	if p == nil {
		return nil
	}
	return p.err
}

// CopyErr returns the error of Copy, as returned by clip.CopySelection: nil
// when Copy did not run or succeeded. It may be called on a nil *Pending.
func (p *Pending) CopyErr() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.copyErr
}

// Method returns the method chosen by Begin, for logging: "none", "uia"
// (plugin.SelectionUIA) or "clipboard" (plugin.SelectionClipboard). It may
// be called on a nil *Pending.
func (p *Pending) Method() string {
	if p == nil {
		return methodNone.String()
	}
	return p.method.String()
}

func copyErrorKind(err error) string {
	switch {
	case errors.Is(err, clip.ErrNoSelection):
		return "no-selection"
	case errors.Is(err, clip.ErrCannotSave):
		return "cannot-save"
	case errors.Is(err, errors.ErrUnsupported):
		return "unsupported"
	}
	return "copy-error"
}

// Read is phase 2, run in the background: it reads the text of the
// selected ranges of the element found by Begin through the TextPattern,
// within ctx, or returns the text Copy got. ok is false when there is
// nothing: an empty or whitespace-only selection, an unsupported, excluded
// or password control, or an error. Read may be called on a nil *Pending;
// later calls return the first result.
func (p *Pending) Read(ctx context.Context) (plugin.Selection, bool) {
	if p == nil {
		return plugin.Selection{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return p.sel, p.ok
	}
	p.done = true
	switch p.method {
	case methodCopy:
		p.log.Debug("selection skipped", "reason", "copy-not-run", "app", p.app.Name)
		return plugin.Selection{}, false
	case methodUIA:
	default:
		return plugin.Selection{}, false
	}
	start := time.Now()
	limit := p.opts.maxChars()
	elem := p.elem
	p.elem = nil
	var (
		parts    []string
		editable bool
		err      error
	)
	done := uia.do(func() {
		defer elem.release()
		if err = ctx.Err(); err != nil {
			return
		}
		parts, editable, err = elem.read(ctx, readUnits(limit))
	})
	select {
	case <-done:
	case <-ctx.Done():
		p.log.Debug("selection skipped", "reason", "timeout", "method", methodUIA, "app", p.app.Name, "took", time.Since(start))
		return plugin.Selection{}, false
	}
	took := time.Since(start)
	if err != nil {
		p.log.Debug("selection skipped", "reason", "read-error", "method", methodUIA, "app", p.app.Name, "took", took)
		return plugin.Selection{}, false
	}
	text, truncated := joinRanges(parts, limit)
	if strings.TrimSpace(text) == "" {
		p.log.Debug("selection empty", "method", methodUIA, "app", p.app.Name, "ranges", len(parts), "took", took)
		return plugin.Selection{}, false
	}
	p.sel = plugin.Selection{Text: text, App: p.app, Source: plugin.SelectionUIA, Truncated: truncated, Editable: editable}
	p.ok = true
	p.log.Debug("selection captured", "source", methodUIA, "app", p.app.Name, "chars", utf8.RuneCountInString(text),
		"ranges", len(parts), "truncated", truncated, "took", took)
	return p.sel, true
}

// worker runs functions one at a time on one locked OS thread, started on
// demand and ended after idle without work. init, when set, prepares the
// thread (COM) and returns its teardown.
type worker struct {
	idle time.Duration
	init func() (teardown func())

	mu      sync.Mutex
	running bool
	queue   []func()
	wake    chan struct{}
}

// do queues fn and returns a channel closed after it ran.
func (w *worker) do(fn func()) <-chan struct{} {
	done := make(chan struct{})
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.wake == nil {
		w.wake = make(chan struct{}, 1)
	}
	w.queue = append(w.queue, func() {
		defer close(done)
		fn()
	})
	if !w.running {
		w.running = true
		go w.loop()
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
	return done
}

// loop is the worker's thread. It stays locked to its OS thread when it
// returns, so the thread ends with it.
func (w *worker) loop() {
	runtime.LockOSThread()
	if w.init != nil {
		if teardown := w.init(); teardown != nil {
			defer teardown()
		}
	}
	t := time.NewTimer(w.idle)
	defer t.Stop()
	for {
		w.mu.Lock()
		if len(w.queue) > 0 {
			fn := w.queue[0]
			w.queue[0] = nil
			w.queue = w.queue[1:]
			w.mu.Unlock()
			fn()
			t.Reset(w.idle)
			continue
		}
		w.mu.Unlock()
		select {
		case <-w.wake:
		case <-t.C:
			w.mu.Lock()
			if len(w.queue) == 0 {
				w.running = false
				w.mu.Unlock()
				return
			}
			w.mu.Unlock()
			t.Reset(w.idle)
		}
	}
}
