package clip

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClip is copyAPI on an in-memory clipboard, a fake keyboard and a
// virtual clock: nothing real is touched and nothing sleeps.
type fakeClip struct {
	t       *testing.T
	clock   time.Time
	fg      uintptr
	content []savedFormat
	seqNo   uint32
	opened  bool
	readTxt bool

	held      map[uint16]time.Time // key held until (zero: forever)
	sent      [][]keyEvent         // inserted events, per send call
	sendLimit int                  // events inserted per call; <0: all
	onCopy    func()               // runs when C down is inserted
	events    []fakeEvent          // scheduled clipboard writes
	getDelay  map[uint32]time.Duration
	noData    map[uint32]bool
	setErr    error
	afterRead func() // runs when the clipboard is closed after the text was read
	onListen  func()
}

type fakeEvent struct {
	at time.Time
	fn func()
}

const (
	fakeHwnd = 0x1234
	// Registered marker formats of the fake.
	fmtExclude = 0xC001
	fmtHistory = 0xC002
	fmtCloud   = 0xC003
	fmtPrivate = 0xC100
)

func newFake(t *testing.T, content ...savedFormat) *fakeClip {
	return &fakeClip{
		t:         t,
		clock:     time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		fg:        fakeHwnd,
		content:   content,
		seqNo:     100,
		held:      map[uint16]time.Time{},
		sendLimit: -1,
	}
}

func (f *fakeClip) mustOpen(op string) {
	f.t.Helper()
	if !f.opened {
		f.t.Fatalf("%s without an open clipboard", op)
	}
}

func (f *fakeClip) foreground(h uintptr) bool { return h == f.fg }

func (f *fakeClip) open() error {
	if f.opened {
		f.t.Fatal("clipboard opened twice")
	}
	f.opened = true
	return nil
}

func (f *fakeClip) close() {
	f.mustOpen("close")
	f.opened = false
	if f.readTxt && f.afterRead != nil {
		fn := f.afterRead
		f.afterRead = nil
		fn()
	}
	f.readTxt = false
}

func (f *fakeClip) enumFormats() []uint32 {
	f.mustOpen("enum")
	var out []uint32
	for _, it := range f.content {
		out = append(out, it.format)
	}
	return out
}

func (f *fakeClip) get(format uint32, limit int) ([]byte, error) {
	f.mustOpen("get")
	f.clock = f.clock.Add(f.getDelay[format])
	if format == cfUnicodeText {
		f.readTxt = true
	}
	for _, it := range f.content {
		if it.format != format {
			continue
		}
		switch {
		case f.noData[format]:
			return nil, errNoData
		case len(it.data) > limit:
			return nil, errTooLarge
		}
		return bytes.Clone(it.data), nil
	}
	return nil, errNoData
}

func (f *fakeClip) empty() error {
	f.mustOpen("empty")
	f.content = nil
	f.seqNo++
	return nil
}

func (f *fakeClip) set(format uint32, data []byte) error {
	f.mustOpen("set")
	if f.setErr != nil {
		return f.setErr
	}
	f.content = append(f.content, savedFormat{format, bytes.Clone(data)})
	f.seqNo++
	return nil
}

func (f *fakeClip) seq() uint32 { return f.seqNo }

// fmtWebCustom is the fake number of Chromium's web custom data format.
const fmtWebCustom = 0xC0FF

func (f *fakeClip) format(name string) uint32 {
	if name == webCustomFormat {
		return fmtWebCustom
	}
	return 0
}

func (f *fakeClip) markers() []uint32 { return []uint32{fmtExclude, fmtHistory, fmtCloud} }

func (f *fakeClip) keyHeld(vk uint16) bool {
	until, ok := f.held[vk]
	return ok && (until.IsZero() || f.clock.Before(until))
}

func (f *fakeClip) send(keys []keyEvent) (int, error) {
	n := len(keys)
	if f.sendLimit >= 0 {
		n = min(n, f.sendLimit)
	}
	f.sent = append(f.sent, slices.Clone(keys[:n]))
	if slices.Contains(keys[:n], keyEvent{vkC, false}) && f.onCopy != nil {
		f.onCopy()
	}
	if n < len(keys) {
		return n, errors.New("fake: input blocked")
	}
	return n, nil
}

func (f *fakeClip) listen() (listener, error) {
	if f.onListen != nil {
		f.onListen()
	}
	return fakeListener{f}, nil
}

func (f *fakeClip) now() time.Time { return f.clock }

func (f *fakeClip) sleep(d time.Duration) { f.clock = f.clock.Add(d) }

// write replaces the clipboard content as another program would.
func (f *fakeClip) write(items ...savedFormat) {
	f.content = items
	f.seqNo++
}

// after schedules fn d after the current virtual time.
func (f *fakeClip) after(d time.Duration, fn func()) {
	f.events = append(f.events, fakeEvent{f.clock.Add(d), fn})
	slices.SortStableFunc(f.events, func(a, b fakeEvent) int { return a.at.Compare(b.at) })
}

// copyText makes the window copy text d after Ctrl+C.
func (f *fakeClip) copyText(d time.Duration, text string) {
	f.onCopy = func() {
		f.after(d, func() { f.write(savedFormat{cfUnicodeText, utf16Bytes(text)}) })
	}
}

type fakeListener struct{ f *fakeClip }

func (l fakeListener) wait(d time.Duration) bool {
	f := l.f
	end := f.clock.Add(d)
	if len(f.events) > 0 && !f.events[0].at.After(end) {
		ev := f.events[0]
		f.events = f.events[1:]
		if ev.at.After(f.clock) {
			f.clock = ev.at
		}
		ev.fn()
		return true
	}
	f.clock = end
	return false
}

func (l fakeListener) close() {}

func (f *fakeClip) run(o CopyOptions) (CopyResult, error) {
	f.t.Helper()
	res, err := copier{api: f, sup: &suppressor{now: f.now}}.copy(fakeHwnd, o)
	if f.opened {
		f.t.Fatal("clipboard left open")
	}
	return res, err
}

func text(s string) savedFormat { return savedFormat{cfUnicodeText, utf16Bytes(s)} }

var (
	plainKeys = []keyEvent{{vkControl, false}, {vkC, false}, {vkC, true}, {vkControl, true}}
	markers   = []savedFormat{{fmtExclude, []byte{0, 0, 0, 0}}, {fmtHistory, []byte{0, 0, 0, 0}}, {fmtCloud, []byte{0, 0, 0, 0}}}
)

func equalContent(a, b []savedFormat) bool {
	return slices.EqualFunc(a, b, func(x, y savedFormat) bool { return x.format == y.format && bytes.Equal(x.data, y.data) })
}

func formatList(c []savedFormat) []uint32 {
	var out []uint32
	for _, it := range c {
		out = append(out, it.format)
	}
	return out
}

func TestCopyRestoresFormatsInOrder(t *testing.T) {
	orig := []savedFormat{
		text("original"),
		{cfText, []byte("original\x00")},      // synthesized from CF_UNICODETEXT
		{cfLocale, []byte{4, 8, 0, 0}},        // synthesized
		{fmtPrivate, []byte{1, 2, 3, 0, 255}}, // a program's private format
		{cfHDROP, dropFiles([]string{`C:\x\a.txt`})},
		{cfDIB, imageToDIB(testImage(2, 2))},
		{cfBitmap, []byte{9}},  // synthesized from the DIB
		{cfPalette, []byte{9}}, // synthesized from the DIB
		{cfOEMText, []byte("original\x00")},
	}
	f := newFake(t, orig...)
	f.copyText(8*time.Millisecond, "selected 世界 🎉")
	res, err := f.run(CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "selected 世界 🎉" || res.Truncated || !res.Restored {
		t.Fatalf("result %+v", res)
	}
	want := append([]savedFormat{orig[0], orig[3], orig[4], orig[5]}, markers...)
	if !equalContent(f.content, want) {
		t.Fatalf("restored formats %#x, want %#x", formatList(f.content), formatList(want))
	}
	if len(f.sent) != 1 || !slices.Equal(f.sent[0], plainKeys) {
		t.Fatalf("keys %v, want %v", f.sent, plainKeys)
	}
	if res.Wait < 8*time.Millisecond+settleDelay {
		t.Errorf("Wait = %v, want at least the copy plus %v", res.Wait, settleDelay)
	}
}

func TestCopyKeepsExistingMarkers(t *testing.T) {
	orig := []savedFormat{text("secret"), {fmtHistory, []byte{1, 0, 0, 0}}, {fmtExclude, []byte{7}}}
	f := newFake(t, orig...)
	f.copyText(time.Millisecond, "x")
	if _, err := f.run(CopyOptions{}); err != nil {
		t.Fatal(err)
	}
	want := append(slices.Clone(orig), markers[2])
	if !equalContent(f.content, want) {
		t.Fatalf("restored %v, want %v", f.content, want)
	}
}

// TestCopyNotesOwnRestore: the sequence number of a restore that marked the
// user's own copy is kept, so that a paste into the launcher still takes it
// (R96); not when the copy was marked already, or nothing was restored.
func TestCopyNotesOwnRestore(t *testing.T) {
	defer restoredSeq.Store(0)
	for _, tc := range []struct {
		name string
		orig []savedFormat
		want bool
	}{
		{"image", []savedFormat{{cfDIB, imageToDIB(testImage(2, 2))}}, true},
		{"marked already", []savedFormat{text("secret"), {fmtExclude, []byte{7}}}, false},
		{"empty", nil, false},
	} {
		restoredSeq.Store(0)
		f := newFake(t, tc.orig...)
		f.copyText(time.Millisecond, "x")
		if _, err := f.run(CopyOptions{}); err != nil {
			t.Fatal(err)
		}
		if got := ownRestore(f.seq()); got != tc.want {
			t.Errorf("%s: ownRestore = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCopyEmptyClipboard(t *testing.T) {
	f := newFake(t)
	f.copyText(time.Millisecond, "x")
	res, err := f.run(CopyOptions{})
	if err != nil || res.Text != "x" || !res.Restored {
		t.Fatalf("res %+v, err %v", res, err)
	}
	if len(f.content) != 0 {
		t.Fatalf("clipboard %v, want empty", f.content)
	}
}

func TestCopyCannotSave(t *testing.T) {
	big := make([]byte, maxSaveBytes/2+1)
	for name, content := range map[string][]savedFormat{
		"enhanced metafile":    {text("a"), {cfEnhMetafile, []byte{1}}},
		"metafile picture":     {{cfMetafilePict, []byte{1}}},
		"owner display":        {text("a"), {cfOwnerDisplay, nil}},
		"display bitmap":       {{cfDSPBitmap, []byte{1}}},
		"private GDI object":   {{0x300, []byte{1}}},
		"private handle":       {{cfPrivateFirst, []byte{1}}},
		"bitmap without DIB":   {{cfBitmap, []byte{1}}},
		"palette without DIB":  {{cfPalette, []byte{1}}},
		"one format too large": {{fmtPrivate, make([]byte, maxSaveBytes+1)}},
		"too large in total":   {{fmtPrivate, big}, {fmtPrivate + 1, big}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, content...)
			f.copyText(time.Millisecond, "x")
			_, err := f.run(CopyOptions{})
			if !errors.Is(err, ErrCannotSave) {
				t.Fatalf("err = %v, want ErrCannotSave", err)
			}
			if len(f.sent) != 0 {
				t.Fatalf("keys were sent: %v", f.sent)
			}
			if f.seqNo != 100 {
				t.Fatal("clipboard was written")
			}
		})
	}
}

func TestCopySaveTimeout(t *testing.T) {
	f := newFake(t, text("a"), savedFormat{fmtPrivate, []byte{1}})
	f.getDelay = map[uint32]time.Duration{fmtPrivate: maxSaveTime + time.Millisecond} // delayed rendering
	f.copyText(time.Millisecond, "x")
	res, err := f.run(CopyOptions{})
	if !errors.Is(err, ErrCannotSave) || len(f.sent) != 0 {
		t.Fatalf("err = %v, keys %v; want ErrCannotSave and no keys", err, f.sent)
	}
	if res.Save <= maxSaveTime {
		t.Errorf("Save = %v", res.Save)
	}
}

func TestCopySkipsFormatsWithoutData(t *testing.T) {
	f := newFake(t, text("a"), savedFormat{fmtPrivate, []byte{1}})
	f.noData = map[uint32]bool{fmtPrivate: true}
	f.copyText(time.Millisecond, "x")
	if _, err := f.run(CopyOptions{}); err != nil {
		t.Fatal(err)
	}
	if want := append([]savedFormat{text("a")}, markers...); !equalContent(f.content, want) {
		t.Fatalf("restored %v", f.content)
	}
}

func TestCopyNothingCopied(t *testing.T) {
	orig := []savedFormat{text("keep")}
	f := newFake(t, orig...)
	res, err := f.run(CopyOptions{Timeout: 150 * time.Millisecond})
	if !errors.Is(err, ErrNoSelection) {
		t.Fatalf("err = %v, want ErrNoSelection", err)
	}
	if res.Restored || f.seqNo != 100 || !equalContent(f.content, orig) {
		t.Fatalf("clipboard touched: restored=%v seq=%d %v", res.Restored, f.seqNo, f.content)
	}
	if res.Wait != 150*time.Millisecond {
		t.Errorf("Wait = %v, want the timeout", res.Wait)
	}
	if !slices.Equal(f.sent[0], plainKeys) {
		t.Errorf("keys %v", f.sent)
	}
}

func TestCopyDefaultTimeout(t *testing.T) {
	f := newFake(t)
	res, err := f.run(CopyOptions{})
	if !errors.Is(err, ErrNoSelection) || res.Wait != defaultCopyTimeout {
		t.Fatalf("err %v, Wait %v", err, res.Wait)
	}
	// A copy after the timeout is too late.
	f = newFake(t)
	f.copyText(defaultCopyTimeout+time.Millisecond, "late")
	if _, err := f.run(CopyOptions{}); !errors.Is(err, ErrNoSelection) {
		t.Fatalf("late copy: err %v", err)
	}
}

func TestCopyWaitsUntilStable(t *testing.T) {
	f := newFake(t, text("orig"))
	f.onCopy = func() {
		for i, s := range []string{"a", "ab", "abc"} {
			f.after(time.Duration(5+12*i)*time.Millisecond, func() { f.write(text(s)) })
		}
	}
	res, err := f.run(CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "abc" || !res.Restored {
		t.Fatalf("res %+v", res)
	}
	if want := 29*time.Millisecond + settleDelay; res.Wait != want {
		t.Errorf("Wait = %v, want %v", res.Wait, want)
	}
	if want := append([]savedFormat{text("orig")}, markers...); !equalContent(f.content, want) {
		t.Fatalf("restored %v", f.content)
	}
}

func TestCopySettleIsBounded(t *testing.T) {
	f := newFake(t)
	f.onCopy = func() {
		for i := range 100 { // writes every 10ms for a second
			f.after(time.Duration(10*(i+1))*time.Millisecond, func() { f.write(text("x")) })
		}
	}
	res, err := f.run(CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Wait > 10*time.Millisecond+maxSettle+settleDelay {
		t.Fatalf("Wait = %v, settling not bounded", res.Wait)
	}
}

func TestCopyNotRestoredAfterOtherWrite(t *testing.T) {
	f := newFake(t, text("orig"))
	f.copyText(time.Millisecond, "sel")
	other := text("someone else")
	copyFn := f.onCopy
	f.onCopy = func() {
		copyFn()
		f.afterRead = func() { f.write(other) }
	}
	res, err := f.run(CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "sel" || res.Restored {
		t.Fatalf("res %+v", res)
	}
	if !equalContent(f.content, []savedFormat{other}) {
		t.Fatalf("clipboard %v, want the other program's content", f.content)
	}
}

func TestCopyNoText(t *testing.T) {
	f := newFake(t, text("orig"))
	f.onCopy = func() {
		f.after(time.Millisecond, func() { f.write(savedFormat{cfHDROP, dropFiles([]string{`C:\a`})}) })
	}
	res, err := f.run(CopyOptions{})
	if !errors.Is(err, ErrNoSelection) || res.Text != "" || !res.Restored {
		t.Fatalf("res %+v, err %v", res, err)
	}
	if want := append([]savedFormat{text("orig")}, markers...); !equalContent(f.content, want) {
		t.Fatalf("restored %v", f.content)
	}
}

func TestCopyTruncates(t *testing.T) {
	f := newFake(t)
	f.copyText(time.Millisecond, "a🎉bcd")
	res, err := f.run(CopyOptions{MaxChars: 3})
	if err != nil || res.Text != "a🎉b" || !res.Truncated {
		t.Fatalf("res %+v, err %v", res, err)
	}
	for _, tc := range []struct {
		s    string
		n    int
		want string
		cut  bool
	}{
		{"", 3, "", false},
		{"abc", 0, "abc", false},
		{"abc", 3, "abc", false},
		{"abcd", 3, "abc", true},
		{"世界你好", 2, "世界", true},
	} {
		if got, cut := truncateRunes(tc.s, tc.n); got != tc.want || cut != tc.cut {
			t.Errorf("truncateRunes(%q, %d) = %q, %v", tc.s, tc.n, got, cut)
		}
	}
}

func TestCopyRestoreFails(t *testing.T) {
	f := newFake(t, text("orig"))
	f.onCopy = func() {
		f.after(time.Millisecond, func() {
			f.write(text("sel"))
			f.setErr = errors.New("fake: out of memory")
		})
	}
	res, err := f.run(CopyOptions{})
	if !errors.Is(err, ErrNotRestored) || res.Text != "sel" || res.Restored {
		t.Fatalf("res %+v, err %v", res, err)
	}
}

func TestCopyNotForeground(t *testing.T) {
	f := newFake(t, text("orig"))
	f.fg = 0x999
	if _, err := f.run(CopyOptions{}); !errors.Is(err, ErrNotForeground) {
		t.Fatalf("err %v", err)
	}
	if len(f.sent) != 0 {
		t.Fatal("keys sent")
	}
}

func TestCopyClipboardChangedBeforeKeys(t *testing.T) {
	f := newFake(t, text("orig"))
	f.onListen = func() { f.write(text("other")) }
	if _, err := f.run(CopyOptions{}); !errors.Is(err, ErrCannotSave) {
		t.Fatalf("err %v", err)
	}
	if len(f.sent) != 0 {
		t.Fatal("keys sent")
	}
}

func TestCopyPartialSendInput(t *testing.T) {
	t.Run("before C", func(t *testing.T) {
		orig := []savedFormat{text("orig")}
		f := newFake(t, orig...)
		f.sendLimit = 1
		f.copyText(time.Millisecond, "sel")
		res, err := f.run(CopyOptions{})
		if err == nil || errors.Is(err, ErrNoSelection) {
			t.Fatalf("err %v", err)
		}
		want := [][]keyEvent{{{vkControl, false}}, {{vkC, true}}}
		if !slices.EqualFunc(f.sent, want, slices.Equal) {
			t.Fatalf("sent %v, want %v", f.sent, want)
		}
		if res.Restored || f.seqNo != 100 || !equalContent(f.content, orig) {
			t.Fatal("clipboard touched")
		}
	})
	t.Run("after C", func(t *testing.T) {
		f := newFake(t, text("orig"))
		f.sendLimit = 3 // Ctrl down, C down, C up
		f.copyText(time.Millisecond, "sel")
		res, err := f.run(CopyOptions{})
		if err == nil || res.Text != "" {
			t.Fatalf("res %+v, err %v", res, err)
		}
		if len(f.sent) < 2 || !slices.Equal(f.sent[1], []keyEvent{{vkC, true}, {vkControl, true}}) {
			t.Fatalf("key-ups not sent: %v", f.sent)
		}
		if !res.Restored {
			t.Fatal("copy that happened anyway was not restored")
		}
		if want := append([]savedFormat{text("orig")}, markers...); !equalContent(f.content, want) {
			t.Fatalf("restored %v", f.content)
		}
	})
}

func TestCopyKeysWithModifiersHeld(t *testing.T) {
	f := newFake(t)
	for _, vk := range []uint16{vkMenu, vkSpace, vkShift, vkLWin, vkRWin} {
		f.held[vk] = time.Time{} // never released
	}
	f.copyText(time.Millisecond, "x")
	start := f.clock
	res, err := f.run(CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []keyEvent{
		{vkControl, false},
		{vkMenu, true}, {vkShift, true}, {vkLWin, true}, {vkRWin, true},
		{vkC, false}, {vkC, true},
		{vkControl, true},
	}
	if !slices.Equal(f.sent[0], want) {
		t.Fatalf("keys %v, want %v", f.sent[0], want)
	}
	if waited := f.clock.Sub(start) - res.Save - res.Wait; waited < keyReleaseWait || waited > keyReleaseWait+keyPollInterval {
		t.Errorf("waited %v for the keys, want about %v", waited, keyReleaseWait)
	}
}

func TestCopyWaitsForKeyRelease(t *testing.T) {
	f := newFake(t)
	f.held[vkMenu] = f.clock.Add(30 * time.Millisecond)
	f.held[vkSpace] = f.clock.Add(20 * time.Millisecond)
	f.copyText(time.Millisecond, "x")
	start := f.clock
	res, err := f.run(CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.sent[0], plainKeys) {
		t.Fatalf("keys %v, want %v", f.sent[0], plainKeys)
	}
	if waited := f.clock.Sub(start) - res.Wait; waited < 30*time.Millisecond || waited > 40*time.Millisecond {
		t.Errorf("waited %v", waited)
	}
}

func TestShortcutKeys(t *testing.T) {
	none := func(uint16) bool { return false }
	if got := shortcutKeys(vkV, none); !slices.Equal(got, []keyEvent{{vkControl, false}, {vkV, false}, {vkV, true}, {vkControl, true}}) {
		t.Fatalf("Ctrl+V = %v", got)
	}
	shift := func(vk uint16) bool { return vk == vkShift }
	want := []keyEvent{{vkControl, false}, {vkShift, true}, {vkC, false}, {vkC, true}, {vkControl, true}}
	if got := shortcutKeys(vkC, shift); !slices.Equal(got, want) {
		t.Fatalf("Ctrl+C with Shift = %v", got)
	}
}

func TestClassify(t *testing.T) {
	text := []uint32{cfUnicodeText, cfText, cfOEMText, cfLocale}
	for _, tc := range []struct {
		f       uint32
		present []uint32
		want    formatKind
	}{
		{cfText, text, formatSynthesized},
		{cfText, []uint32{cfText}, formatMemory},
		{cfLocale, []uint32{cfLocale}, formatMemory},
		{cfBitmap, []uint32{cfBitmap, cfDIB}, formatSynthesized},
		{cfBitmap, []uint32{cfBitmap, cfDIBV5}, formatSynthesized},
		{cfPalette, []uint32{cfDIB, cfPalette}, formatSynthesized},
		{cfBitmap, []uint32{cfBitmap}, formatHandle},
		{cfEnhMetafile, nil, formatHandle},
		{cfMetafilePict, nil, formatHandle},
		{cfDSPEnhMetafile, nil, formatHandle},
		{0x3FF, nil, formatHandle},
		{0x400, nil, formatMemory},
		{cfHDROP, nil, formatMemory},
		{cfDIB, nil, formatMemory},
		{0xC0FF, nil, formatMemory},
	} {
		if got := classify(tc.f, tc.present); got != tc.want {
			t.Errorf("classify(%#x, %v) = %v, want %v", tc.f, tc.present, got, tc.want)
		}
	}
}

func TestSuppressor(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := &suppressor{now: func() time.Time { return now }}
	if s.covers(5) || s.holding() {
		t.Fatal("suppressed before begin")
	}
	end := s.begin(10)
	if s.covers(10) {
		t.Error("the content before the copy is suppressed")
	}
	if !s.covers(11) || !s.covers(30) || !s.holding() {
		t.Error("change during the copy not suppressed")
	}
	// A nested copy starting later keeps the earlier start.
	end2 := s.begin(20)
	if !s.covers(15) {
		t.Error("nested copy narrowed the range")
	}
	end2()
	now = now.Add(time.Second)
	if !s.covers(31) {
		t.Error("suppression ended while a copy is running")
	}
	end()
	end() // idempotent
	now = now.Add(debounceDelay - time.Millisecond)
	if !s.covers(32) || !s.holding() {
		t.Error("suppression ended before the debounce")
	}
	now = now.Add(time.Millisecond)
	if s.covers(33) || s.holding() {
		t.Error("a normal copy after the suppression is hidden")
	}
	// Sequence numbers wrap around.
	end = s.begin(^uint32(0))
	if !s.covers(1) {
		t.Error("wrapped sequence number not suppressed")
	}
	end()
}

func TestCopySuppressesOwnChanges(t *testing.T) {
	f := newFake(t, text("orig"))
	sup := &suppressor{now: f.now}
	var during []bool
	f.onCopy = func() {
		f.after(time.Millisecond, func() {
			f.write(text("sel"))
			during = append(during, sup.covers(f.seqNo))
		})
	}
	if _, err := (copier{api: f, sup: sup}).copy(fakeHwnd, CopyOptions{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(during, []bool{true}) {
		t.Fatalf("temporary copy suppressed: %v", during)
	}
	if !sup.covers(f.seqNo) || !sup.holding() {
		t.Fatal("restore not suppressed")
	}
	f.clock = f.clock.Add(debounceDelay)
	f.write(text("user copy"))
	if sup.covers(f.seqNo) {
		t.Fatal("a copy after the suppression is hidden")
	}
}

func TestDebouncerHold(t *testing.T) {
	var held atomic.Bool
	held.Store(true)
	fired := make(chan struct{}, 10)
	d := &debouncer{delay: 10 * time.Millisecond, fn: func() { fired <- struct{}{} }, hold: held.Load}
	d.trigger()
	select {
	case <-fired:
		t.Fatal("fired while held")
	case <-time.After(50 * time.Millisecond):
	}
	held.Store(false)
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("did not fire after the hold")
	}
	select {
	case <-fired:
		t.Fatal("fired twice")
	case <-time.After(40 * time.Millisecond):
	}
	d.stop()
}

// vscodeData is Chromium's web custom data as VS Code leaves it after a
// copy in its editor: its "vscode-editor-data" JSON among other bytes, in
// UTF-16.
func vscodeData(empty bool) []byte {
	b := append([]byte{0x40, 0, 0, 0}, utf16Bytes("vscode-editor-data")...)
	return append(b, utf16Bytes(fmt.Sprintf(`{"version":1,"isFromEmptySelection":%v,"multicursorText":null,"mode":"go"}`, empty))...)
}

// TestCopyLineMark (R132): with LineMark, a copy VS Code marks as made with
// nothing selected (the current line) is reported as such; a marked
// selection is not; without LineMark the data is not looked at.
func TestCopyLineMark(t *testing.T) {
	for _, tc := range []struct {
		name      string
		lineMark  bool
		data      []byte
		fromEmpty bool
	}{
		{"the current line", true, vscodeData(true), true},
		{"a selection", true, vscodeData(false), false},
		{"no editor data", true, nil, false},
		{"not asked", false, vscodeData(true), false},
	} {
		f := newFake(t, text("original"))
		f.onCopy = func() {
			f.after(5*time.Millisecond, func() {
				fs := []savedFormat{{cfUnicodeText, utf16Bytes("func main() {}\r\n")}}
				if tc.data != nil {
					fs = append(fs, savedFormat{fmtWebCustom, tc.data})
				}
				f.write(fs...)
			})
		}
		res, err := f.run(CopyOptions{LineMark: tc.lineMark})
		if err != nil || res.Text != "func main() {}\r\n" || res.FromEmptySelection != tc.fromEmpty {
			t.Errorf("%s: %+v %v", tc.name, res, err)
		}
	}
}
