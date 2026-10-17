package clip

import (
	"fmt"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/darwin/ns"
)

// CopySelection copies the text selected in the app with process
// identifier target (from ForegroundWindow), which must be frontmost, with
// a simulated ⌘C, and restores the pasteboard as it was (pbCopy). Whether
// the app may receive ⌘C at all is for the caller to decide. It needs the
// Accessibility permission and returns ErrNotTrusted without it, and
// ErrCannotSave (nothing posted) when the pasteboard cannot be saved
// exactly, e.g. it is larger than 16 MB. Calls are serialized.
func CopySelection(target uintptr, o CopyOptions) (CopyResult, error) {
	q, err := quartz()
	if err != nil {
		return CopyResult{}, err
	}
	if !q.axIsProcessTrusted() {
		return CopyResult{}, ErrNotTrusted
	}
	if err := ns.Load(); err != nil {
		return CopyResult{}, fmt.Errorf("clip: %w", err)
	}
	copyMu.Lock()
	defer copyMu.Unlock()
	return pbCopy(macPB{q}, suppression, int(target), o)
}

var copyMu sync.Mutex

// macPB is pbAPI on the general pasteboard.
type macPB struct{ q *quartzFuncs }

func (macPB) frontmost() int {
	var p int32
	ns.Pool(func() { p = pid(frontmost()) })
	return int(p)
}

func (macPB) changeCount() int { return changeCount() }

func (macPB) save(maxBytes int, maxTime time.Duration) ([][]pbData, error) {
	var (
		items [][]pbData
		err   error
	)
	start := time.Now()
	ns.Pool(func() {
		all := ns.Send(pasteboard(), "pasteboardItems")
		total := 0
		for i := range ns.SendInt(all, "count") {
			item := ns.Send(all, "objectAtIndex:", i)
			var saved []pbData
			for _, typ := range ns.Strings(ns.Send(item, "types")) {
				d := ns.Send(item, "dataForType:", ns.String(typ))
				if d == 0 {
					continue // promised data that the owner does not deliver
				}
				if total += ns.SendInt(d, "length"); total > maxBytes {
					err = fmt.Errorf("%w: larger than %d MB", ErrCannotSave, maxBytes>>20)
					return
				}
				saved = append(saved, pbData{typ, ns.Bytes(d)})
				if maxTime > 0 && time.Since(start) > maxTime {
					err = fmt.Errorf("%w: rendering took longer than %v", ErrCannotSave, maxTime)
					return
				}
			}
			items = append(items, saved)
		}
	})
	return items, err
}

func (macPB) restore(items [][]pbData) error {
	var err error
	ns.Pool(func() {
		pb := pasteboard()
		ns.Send(pb, "clearContents")
		var objs []ns.ID
		for _, it := range items {
			if len(it) == 0 {
				continue
			}
			item := ns.Send(ns.Send(ns.Send(ns.Class("NSPasteboardItem"), "alloc"), "init"), "autorelease")
			for _, d := range it {
				ns.SendBool(item, "setData:forType:", ns.Data(d.data), ns.String(d.typ))
			}
			objs = append(objs, item)
		}
		if len(objs) > 0 && !ns.SendBool(pb, "writeObjects:", ns.Array(objs...)) {
			err = fmt.Errorf("clip: the pasteboard refused the saved content")
		}
		lastWriteCount.Store(int64(ns.SendInt(pb, "changeCount")))
	})
	return err
}

func (macPB) text() (string, []string) {
	var (
		s     string
		types []string
	)
	ns.Pool(func() {
		pb := pasteboard()
		types = ns.Strings(ns.Send(pb, "types"))
		s = ns.GoString(ns.Send(pb, "stringForType:", ns.String(typeText)))
	})
	return s, types
}

func (m macPB) postCopy() error {
	const (
		keyC        = 8       // kVK_ANSI_C
		flagCommand = 1 << 20 // kCGEventFlagMaskCommand
		hidTap      = 0       // kCGHIDEventTap
	)
	for _, down := range []bool{true, false} {
		ev := m.q.createKeyboardEvent(0, keyC, down)
		if ev == 0 {
			return fmt.Errorf("clip: CGEventCreateKeyboardEvent failed")
		}
		// Only ⌘: modifiers still held from the hotkey (⌥, ⇧) must not
		// turn the copy into another shortcut.
		m.q.setFlags(ev, flagCommand)
		m.q.post(hidTap, ev)
		m.q.release(ev)
	}
	return nil
}

func (macPB) now() time.Time        { return time.Now() }
func (macPB) sleep(d time.Duration) { time.Sleep(d) }
