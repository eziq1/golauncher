package clip

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Copying the selection of another window with a simulated Ctrl+C, the
// clipboard saved before and restored after (docs/selected-text.md §2.2).
//
// The flow is written against copyAPI, a small interface over the Win32
// calls it needs, so that it is tested on any platform with an in-memory
// clipboard and fake keyboard input (copy_test.go). copy_windows.go
// implements it for the real clipboard.

// Errors of CopySelection.
var (
	// ErrNoSelection: the window did not put text on the clipboard in time
	// (nothing selected, or it does not copy on Ctrl+C), or what it copied
	// holds no text (e.g. files).
	ErrNoSelection = errors.New("clip: no text was copied")
	// ErrCannotSave: the clipboard holds content that cannot be saved and
	// restored exactly (too large, too slow to render, or a format that is
	// not memory), so no key was sent.
	ErrCannotSave = errors.New("clip: the clipboard content cannot be saved")
	// ErrNotRestored: the text was copied but the saved clipboard content
	// could not be put back. CopyResult.Text is still valid; the caller
	// should tell the user.
	ErrNotRestored = errors.New("clip: the clipboard could not be restored")
)

// CopyOptions configure CopySelection.
type CopyOptions struct {
	// Timeout is how long to wait for the window to write the clipboard
	// after Ctrl+C (default 200ms).
	Timeout time.Duration
	// MaxChars truncates the text, in characters (0: no limit).
	MaxChars int
	// LineMark: the window may copy its current line when nothing is
	// selected and then marks the copy, as VS Code does ("isFromEmptySelection"
	// in Chromium's web custom data, R132). Such a copy is reported in
	// CopyResult.FromEmptySelection.
	LineMark bool
}

// CopyResult is the outcome of CopySelection. It never holds anything but
// the copied text; the timings are for the log.
type CopyResult struct {
	// Text is the copied text, truncated to CopyOptions.MaxChars.
	Text      string
	Truncated bool
	// FromEmptySelection (with CopyOptions.LineMark): the window copied its
	// current line because nothing was selected; Text is not a selection.
	FromEmptySelection bool
	// Restored reports that the saved clipboard content was put back. It is
	// false when nothing needed restoring, or when another program wrote the
	// clipboard after the copy (its content is then left alone).
	Restored bool
	// Timings of the steps, for the log and measurements: saving the
	// clipboard, waiting for the copy (from sending the keys until the
	// clipboard was stable), and restoring it.
	Save, Wait, Restore time.Duration
}

// Limits and delays of the copy flow.
const (
	defaultCopyTimeout = 200 * time.Millisecond
	maxSaveBytes       = 16 << 20
	maxSaveTime        = 50 * time.Millisecond
	keyReleaseWait     = 100 * time.Millisecond
	keyPollInterval    = 5 * time.Millisecond
	settleDelay        = 20 * time.Millisecond // no further change for this long
	maxSettle          = 200 * time.Millisecond

	// maxDataSize bounds any single clipboard format that is read.
	maxDataSize = 256 << 20
)

// Standard clipboard formats.
const (
	cfText            = 1
	cfBitmap          = 2
	cfMetafilePict    = 3
	cfOEMText         = 7
	cfDIB             = 8
	cfPalette         = 9
	cfUnicodeText     = 13
	cfEnhMetafile     = 14
	cfHDROP           = 15
	cfLocale          = 16
	cfDIBV5           = 17
	cfOwnerDisplay    = 0x80
	cfDSPBitmap       = 0x82
	cfDSPMetafilePict = 0x83
	cfDSPEnhMetafile  = 0x8E
	cfPrivateFirst    = 0x200 // CF_PRIVATEFIRST..CF_GDIOBJLAST: any handle
	cfGDIObjLast      = 0x3FF
)

// Virtual-key codes.
const (
	vkShift   = 0x10
	vkControl = 0x11
	vkMenu    = 0x12
	vkSpace   = 0x20
	vkC       = 0x43
	vkV       = 0x56
	vkLWin    = 0x5B
	vkRWin    = 0x5C
)

// The clipboard format (Windows) and pasteboard type (macOS) in which
// Chromium, and so Electron apps such as VS Code, keep the data a page put
// on the clipboard under its own types: VS Code's "vscode-editor-data" goes
// there, a JSON in UTF-16 among the other types.
const (
	webCustomFormat = "Chromium Web Custom MIME Data Format"
	webCustomType   = "org.chromium.web-custom-data"
)

// emptySelectionMark is how VS Code marks the copy of the current line made
// with nothing selected, in its editor data (JSON.stringify: no spaces), in
// UTF-16 without the terminating NUL.
var emptySelectionMark = func() []byte {
	b := utf16Bytes(`"isFromEmptySelection":true`)
	return b[:len(b)-2]
}()

// emptySelectionMarked reports whether web custom data b holds VS Code's
// mark of a copy made with nothing selected.
func emptySelectionMarked(b []byte) bool { return len(b) > 0 && bytes.Contains(b, emptySelectionMark) }

// Errors of copyAPI.get.
var (
	errTooLarge = errors.New("clip: clipboard data too large")
	errNoData   = errors.New("clip: no clipboard data")
)

// copyAPI is what the copy flow needs from the system. open locks the
// calling goroutine to its OS thread until close; listen does the same until
// the listener is closed.
type copyAPI interface {
	// foreground reports whether hwnd is a window and the foreground one.
	foreground(hwnd uintptr) bool
	open() error
	close()
	// enumFormats lists the formats on the open clipboard, in order.
	enumFormats() []uint32
	// get copies the data of format f from the open clipboard: errTooLarge
	// when it is larger than limit bytes, errNoData when there is none.
	get(f uint32, limit int) ([]byte, error)
	empty() error
	set(f uint32, data []byte) error
	seq() uint32
	// format returns the number of the registered format name (0 if none).
	format(name string) uint32
	// markers are the registered formats that ask monitors, clipboard
	// history and cloud clipboard to ignore the content (0 when missing).
	markers() []uint32
	keyHeld(vk uint16) bool
	// send injects keys and returns how many were inserted.
	send(keys []keyEvent) (int, error)
	// listen starts listening for clipboard changes.
	listen() (listener, error)
	now() time.Time
	sleep(d time.Duration)
}

// listener receives clipboard change notifications.
type listener interface {
	// wait blocks until the clipboard changes or d elapses and reports
	// whether a change notification arrived.
	wait(d time.Duration) bool
	close()
}

// keyEvent is one simulated key press or release.
type keyEvent struct {
	vk uint16
	up bool
}

// shortcutKeys returns the events of Ctrl+vk. Modifiers still held from a
// hotkey (Alt, Shift, Win) are released after Ctrl is pressed, so releasing
// Alt does not activate a menu bar and the shortcut is not seen as e.g.
// Ctrl+Shift+C (developer tools in Chrome).
func shortcutKeys(vk uint16, held func(vk uint16) bool) []keyEvent {
	ev := []keyEvent{{vkControl, false}}
	for _, m := range []uint16{vkMenu, vkShift, vkLWin, vkRWin} {
		if held(m) {
			ev = append(ev, keyEvent{m, true})
		}
	}
	return append(ev, keyEvent{vk, false}, keyEvent{vk, true}, keyEvent{vkControl, true})
}

// savedFormat is one saved clipboard format.
type savedFormat struct {
	format uint32
	data   []byte
}

// saveLimits bound saveClipboard.
type saveLimits struct {
	// strict fails on formats that cannot be saved instead of skipping them.
	strict   bool
	maxBytes int
	maxTime  time.Duration
}

// formatKind classifies a clipboard format for saving.
type formatKind int

const (
	formatMemory      formatKind = iota // global memory: copied as bytes
	formatSynthesized                   // recreated by Windows from another format
	formatHandle                        // a GDI or other handle: cannot be saved
)

// classify returns how format f is saved, given all formats present.
func classify(f uint32, present []uint32) formatKind {
	has := func(fs ...uint32) bool {
		return slices.ContainsFunc(fs, func(x uint32) bool { return slices.Contains(present, x) })
	}
	switch f {
	case cfText, cfOEMText, cfLocale:
		if has(cfUnicodeText) {
			return formatSynthesized
		}
	case cfBitmap, cfPalette:
		if has(cfDIB, cfDIBV5) {
			return formatSynthesized
		}
		return formatHandle
	case cfMetafilePict, cfEnhMetafile, cfOwnerDisplay, cfDSPBitmap, cfDSPMetafilePict, cfDSPEnhMetafile:
		return formatHandle
	}
	if f >= cfPrivateFirst && f <= cfGDIObjLast {
		return formatHandle
	}
	return formatMemory
}

// saveClipboard copies every format on the clipboard, in order, and returns
// them with the sequence number of that content. Synthesized formats are
// skipped, as are formats whose data cannot be retrieved (nobody could paste
// them either).
func saveClipboard(a copyAPI, lim saveLimits) (items []savedFormat, seq uint32, err error) {
	if err := a.open(); err != nil {
		return nil, 0, err
	}
	defer a.close()
	start := a.now()
	seq = a.seq()
	present := a.enumFormats()
	total := 0
	for _, f := range present {
		switch classify(f, present) {
		case formatSynthesized:
			continue
		case formatHandle:
			if lim.strict {
				return nil, 0, fmt.Errorf("%w: format %#x is not memory", ErrCannotSave, f)
			}
			continue
		}
		b, err := a.get(f, lim.maxBytes-total)
		switch {
		case errors.Is(err, errTooLarge):
			return nil, 0, fmt.Errorf("%w: larger than %d MB", ErrCannotSave, lim.maxBytes>>20)
		case err != nil:
			continue
		}
		total += len(b)
		items = append(items, savedFormat{f, b})
		if lim.maxTime > 0 && a.now().Sub(start) > lim.maxTime {
			return nil, 0, fmt.Errorf("%w: rendering took longer than %v", ErrCannotSave, lim.maxTime)
		}
	}
	return items, seq, nil
}

// restoreClipboard replaces the clipboard content with items, with the
// marker formats added when markers is set (unless already saved). With
// ifSeq set, it does nothing and returns false unless the sequence number,
// read with the clipboard open, is still want. Every format is attempted;
// the first error is returned.
func restoreClipboard(a copyAPI, items []savedFormat, markers, ifSeq bool, want uint32) (bool, error) {
	if err := a.open(); err != nil {
		return false, err
	}
	defer a.close()
	if ifSeq && a.seq() != want {
		return false, nil
	}
	if err := a.empty(); err != nil {
		return false, err
	}
	var first error
	keep := func(err error) {
		if first == nil {
			first = err
		}
	}
	for _, it := range items {
		if err := a.set(it.format, it.data); err != nil {
			keep(err)
		}
	}
	if markers && len(items) > 0 {
		for _, m := range a.markers() {
			if m == 0 || slices.ContainsFunc(items, func(it savedFormat) bool { return it.format == m }) {
				continue
			}
			if err := a.set(m, []byte{0, 0, 0, 0}); err != nil {
				keep(err)
			}
		}
	}
	return first == nil, first
}

// truncateRunes cuts s to at most n characters (n <= 0: no limit).
func truncateRunes(s string, n int) (string, bool) {
	if n <= 0 {
		return s, false
	}
	i := 0
	for j := range s {
		if i == n {
			return s[:j], true
		}
		i++
	}
	return s, false
}

// copier runs the copy flow against api, hiding changes from Watch with sup.
type copier struct {
	api copyAPI
	sup *suppressor
}

func (c copier) copy(hwnd uintptr, o CopyOptions) (CopyResult, error) {
	var res CopyResult
	a := c.api
	if o.Timeout <= 0 {
		o.Timeout = defaultCopyTimeout
	}
	if hwnd == 0 || !a.foreground(hwnd) {
		return res, ErrNotForeground
	}

	// 1. Save.
	t := a.now()
	saved, s0, err := saveClipboard(a, saveLimits{strict: true, maxBytes: maxSaveBytes, maxTime: maxSaveTime})
	res.Save = a.now().Sub(t)
	if err != nil {
		if !errors.Is(err, ErrCannotSave) {
			err = fmt.Errorf("%w: %w", ErrCannotSave, err)
		}
		return res, err
	}

	// 2. Mark: hide every change from s0 on from Watch until the restore has
	// settled.
	defer c.sup.begin(s0)()

	l, err := a.listen()
	if err != nil {
		return res, err
	}
	defer l.close()

	// 3. Keys.
	c.waitRelease()
	if a.seq() != s0 {
		return res, fmt.Errorf("%w: the clipboard changed while it was saved", ErrCannotSave)
	}
	if !a.foreground(hwnd) {
		return res, ErrNotForeground
	}
	keys := shortcutKeys(vkC, a.keyHeld)
	sent := a.now()
	if n, err := a.send(keys); err != nil || n < len(keys) {
		// Do not leave Ctrl or C stuck down.
		_, _ = a.send([]keyEvent{{vkC, true}, {vkControl, true}})
		if err == nil {
			err = fmt.Errorf("clip: SendInput inserted %d of %d events", n, len(keys))
		}
		if n <= len(keys)-3 { // C was not pressed: the clipboard is untouched
			return res, err
		}
		// C was pressed, so the window may copy after all: put the
		// clipboard back if it does.
		if seq, ok := c.waitCopy(l, s0, sent.Add(o.Timeout)); ok {
			restored, rerr := restoreClipboard(a, saved, true, true, seq)
			res.Restored = restored
			c.noteRestored(saved, restored && rerr == nil)
			if rerr != nil {
				err = errors.Join(err, fmt.Errorf("%w: %w", ErrNotRestored, rerr))
			}
		}
		return res, err
	}

	// 4. Wait.
	seq, ok := c.waitCopy(l, s0, sent.Add(o.Timeout))
	res.Wait = a.now().Sub(sent)
	if !ok {
		return res, ErrNoSelection
	}

	// 5. Read only the text: other formats could make the window render
	// them (delayed rendering). An editor that marks the copy of its line
	// (LineMark) is asked for its web custom data too.
	text, readErr := c.readText()
	if o.LineMark {
		res.FromEmptySelection = c.lineMarked()
	}

	// 6. Restore, unless someone else wrote the clipboard meanwhile.
	t = a.now()
	restored, rerr := restoreClipboard(a, saved, true, true, seq)
	res.Restore = a.now().Sub(t)
	res.Restored = restored
	c.noteRestored(saved, restored && rerr == nil)

	res.Text, res.Truncated = truncateRunes(text, o.MaxChars)
	var errs []error
	switch {
	case readErr != nil:
		errs = append(errs, readErr)
	case res.Text == "":
		errs = append(errs, ErrNoSelection)
	}
	if rerr != nil {
		errs = append(errs, fmt.Errorf("%w: %w", ErrNotRestored, rerr))
	}
	return res, errors.Join(errs...)
}

// restoredSeq is the clipboard sequence number right after CopySelection
// put back content that did not carry the history markers before (0: none):
// the markers on it are ours, added so that clipboard history does not
// record the user's own copy a second time. A paste into the launcher
// (ReadOptions.Paste, HasFilesOrImage) does not take them as a request to
// leave the content alone (R96).
var restoredSeq atomic.Uint32

// noteRestored records the sequence number of a restore that added the
// markers to content without them.
func (c copier) noteRestored(saved []savedFormat, ok bool) {
	if !ok || len(saved) == 0 {
		return // no markers were added
	}
	marks := c.api.markers()
	if slices.ContainsFunc(saved, func(s savedFormat) bool { return s.format != 0 && slices.Contains(marks, s.format) }) {
		return // the user's content was marked already
	}
	restoredSeq.Store(c.api.seq())
}

// ownRestore reports whether the clipboard content at sequence number seq
// is the user's own, put back by CopySelection with our markers.
func ownRestore(seq uint32) bool {
	r := restoredSeq.Load()
	return r != 0 && r == seq
}

// waitRelease waits a little for the user to release the keys of the
// hotkey that started the copy.
func (c copier) waitRelease() {
	a := c.api
	held := func() bool {
		return slices.ContainsFunc([]uint16{vkMenu, vkSpace, vkShift, vkLWin, vkRWin}, a.keyHeld)
	}
	deadline := a.now().Add(keyReleaseWait)
	for held() && a.now().Before(deadline) {
		a.sleep(keyPollInterval)
	}
}

// waitCopy waits until the clipboard sequence number differs from s0 (by
// deadline at the latest) and then until it has not changed for
// settleDelay, since some programs write several times for one copy. It
// returns the final sequence number.
func (c copier) waitCopy(l listener, s0 uint32, deadline time.Time) (uint32, bool) {
	a := c.api
	for a.seq() == s0 {
		rem := deadline.Sub(a.now())
		if rem <= 0 {
			return 0, false
		}
		l.wait(rem)
	}
	limit := a.now().Add(maxSettle)
	for {
		seq := a.seq()
		rem := min(settleDelay, limit.Sub(a.now()))
		if rem <= 0 {
			return seq, true
		}
		if !l.wait(rem) && a.seq() == seq {
			return seq, true
		}
	}
}

// readText returns the Unicode text on the clipboard ("" when none).
func (c copier) readText() (string, error) {
	a := c.api
	if err := a.open(); err != nil {
		return "", err
	}
	defer a.close()
	b, err := a.get(cfUnicodeText, maxDataSize)
	if err != nil {
		return "", nil
	}
	return utf16String(b), nil
}

// lineMarked reports whether the clipboard's web custom data marks a copy
// made with nothing selected (CopyOptions.LineMark).
func (c copier) lineMarked() bool {
	a := c.api
	f := a.format(webCustomFormat)
	if f == 0 {
		return false
	}
	if err := a.open(); err != nil {
		return false
	}
	defer a.close()
	b, err := a.get(f, 1<<20)
	return err == nil && emptySelectionMarked(b)
}

// suppressor hides clipboard changes made by CopySelection from Watch: every
// change after the sequence number from, until the copy has ended and
// debounceDelay more has passed.
type suppressor struct {
	now func() time.Time // time.Now when nil

	mu     sync.Mutex
	active int
	from   uint32
	until  time.Time
}

// suppression is the suppressor of this process.
var suppression = &suppressor{}

func (s *suppressor) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// on reports whether suppression is in effect; s.mu is held.
func (s *suppressor) on(now time.Time) bool {
	return s.active > 0 || now.Before(s.until)
}

// begin starts hiding changes after sequence number from. end stops it
// after debounceDelay; it may be called more than once.
func (s *suppressor) begin(from uint32) (end func()) {
	s.mu.Lock()
	if !s.on(s.clock()) || int32(from-s.from) < 0 {
		s.from = from
	}
	s.active++
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.active--
			if u := s.clock().Add(debounceDelay); u.After(s.until) {
				s.until = u
			}
		})
	}
}

// covers reports whether a change that left the clipboard at sequence
// number seq is hidden.
func (s *suppressor) covers(seq uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.on(s.clock()) && int32(seq-s.from) > 0
}

// holding reports whether suppression is in effect. Watch holds back a
// pending notification meanwhile, so that it never reads the temporary
// content.
func (s *suppressor) holding() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.on(s.clock())
}
