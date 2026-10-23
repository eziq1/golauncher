package clip

import (
	"errors"
	"slices"
	"testing"
	"time"
)

// fakePB is a pasteboard and an app: postCopy makes the app copy text
// (after delay; none when text is ""), with extra types.
type fakePB struct {
	front    int
	count    int
	items    [][]pbData
	saveErr  error
	t        time.Time
	text_    string
	types    []string
	copyText string
	extra    []string
	delay    time.Duration
	copyAt   time.Time
	posted   int
	restored [][]pbData
	stolen   bool   // another app writes after the copy
	custom   []byte // the copy's web custom data
}

func (f *fakePB) frontmost() int { return f.front }
func (f *fakePB) changeCount() int {
	if !f.copyAt.IsZero() && !f.t.Before(f.copyAt) {
		f.copyAt = time.Time{}
		f.count++
		f.text_, f.types = f.copyText, append([]string{typeText}, f.extra...)
		if f.stolen {
			f.count++
		}
	}
	return f.count
}
func (f *fakePB) save(int, time.Duration) ([][]pbData, error) { return f.items, f.saveErr }
func (f *fakePB) restore(items [][]pbData) error {
	f.restored = items
	f.count++
	return nil
}
func (f *fakePB) text() (string, []string) { return f.text_, f.types }

func (f *fakePB) data(typ string) []byte {
	if typ == webCustomType {
		return f.custom
	}
	return nil
}
func (f *fakePB) postCopy() error {
	f.posted++
	if f.copyText != "" {
		f.copyAt = f.t.Add(f.delay)
	}
	return nil
}
func (f *fakePB) now() time.Time        { return f.t }
func (f *fakePB) sleep(d time.Duration) { f.t = f.t.Add(d) }

func newPB() *fakePB {
	return &fakePB{front: 42, count: 7, t: time.Unix(1e9, 0), items: [][]pbData{{{typeText, []byte("old")}}}}
}

func TestPBCopy(t *testing.T) {
	f := newPB()
	f.copyText, f.delay = "selected text", 30*time.Millisecond
	res, err := pbCopy(f, &suppressor{}, 42, CopyOptions{MaxChars: 8})
	if err != nil || res.Text != "selected" || !res.Truncated || !res.Restored || f.posted != 1 {
		t.Fatalf("res %+v, err %v", res, err)
	}
	if !slices.EqualFunc(f.restored, f.items, func(a, b []pbData) bool {
		return slices.EqualFunc(a, b, func(x, y pbData) bool { return x.typ == y.typ })
	}) {
		t.Errorf("restored %v", f.restored)
	}
	if res.Wait < 30*time.Millisecond || res.Wait > 30*time.Millisecond+maxSettle {
		t.Errorf("wait %v", res.Wait)
	}
}

func TestPBCopyNothing(t *testing.T) {
	f := newPB()
	res, err := pbCopy(f, &suppressor{}, 42, CopyOptions{})
	if !errors.Is(err, ErrNoSelection) || res.Restored || f.restored != nil {
		t.Fatalf("res %+v, err %v", res, err)
	}
}

func TestPBCopyRefusals(t *testing.T) {
	f := newPB()
	f.front = 1
	if _, err := pbCopy(f, &suppressor{}, 42, CopyOptions{}); !errors.Is(err, ErrNotForeground) || f.posted != 0 {
		t.Errorf("not frontmost: %v", err)
	}
	f = newPB()
	f.saveErr = errTooLarge
	if _, err := pbCopy(f, &suppressor{}, 42, CopyOptions{}); !errors.Is(err, ErrCannotSave) || f.posted != 0 {
		t.Errorf("cannot save: %v", err)
	}
}

// A password manager's copy: restored, but its text is not the selection.
func TestPBCopyConcealed(t *testing.T) {
	f := newPB()
	f.copyText, f.extra = "hunter2", []string{typeConcealed}
	res, err := pbCopy(f, &suppressor{}, 42, CopyOptions{})
	if !errors.Is(err, ErrNoSelection) || res.Text != "" || !res.Restored {
		t.Fatalf("res %+v, err %v", res, err)
	}
}

// Another app wrote the pasteboard after the copy: it is left alone.
func TestPBCopyStolen(t *testing.T) {
	f := newPB()
	f.copyText, f.stolen = "x", true
	res, err := pbCopy(f, &suppressor{}, 42, CopyOptions{})
	if err != nil || res.Text != "x" {
		t.Fatalf("res %+v, err %v", res, err)
	}
	// The second change arrived within the settle time, so it counts as
	// the copy's and the saved items are restored over it; one arriving
	// later would be kept (res.Restored false).
	if !res.Restored {
		t.Error("not restored")
	}
}

// TestPBCopyLineMark (R132): on macOS too, with LineMark, VS Code's mark of
// a copy made with nothing selected is reported; without it, ignored.
func TestPBCopyLineMark(t *testing.T) {
	for _, tc := range []struct {
		lineMark, empty, want bool
	}{{true, true, true}, {true, false, false}, {false, true, false}} {
		f := newPB()
		f.copyText, f.delay = "func main() {}\n", 10*time.Millisecond
		f.extra, f.custom = []string{webCustomType}, vscodeData(tc.empty)
		res, err := pbCopy(f, &suppressor{}, 42, CopyOptions{LineMark: tc.lineMark})
		if err != nil || res.FromEmptySelection != tc.want {
			t.Errorf("%+v: res %+v, err %v", tc, res, err)
		}
	}
}
