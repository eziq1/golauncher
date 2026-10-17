package clip

import (
	"errors"
	"fmt"
	"time"
)

// Copying another app's selection on macOS (CopySelection in
// copy_darwin.go): the pasteboard's items are saved, ⌘C is posted, the
// pasteboard's changeCount shows when the app copied, the text is read and
// the saved items are put back. The flow is written against pbAPI so that
// it is tested on every platform (pbcopy_test.go).

// pbAPI is what the macOS copy flow needs from the system.
type pbAPI interface {
	// frontmost returns the process identifier of the frontmost app.
	frontmost() int
	changeCount() int
	// save copies every item of the pasteboard, each a list of types with
	// their data; it fails with ErrCannotSave beyond maxBytes or maxTime.
	save(maxBytes int, maxTime time.Duration) ([][]pbData, error)
	// restore replaces the pasteboard content with items (empty: clear it).
	restore(items [][]pbData) error
	// text returns the plain text on the pasteboard and its types.
	text() (string, []string)
	// postCopy posts ⌘C (with no other modifier) to the frontmost app.
	postCopy() error
	now() time.Time
	sleep(d time.Duration)
}

// pbPoll is how often the flow reads the changeCount while it waits.
const pbPoll = 5 * time.Millisecond

// pbCopy copies the selection of app pid, which must be frontmost, with ⌘C
// and restores the pasteboard. Changes during the copy are hidden from
// Watch by sup. Text on a pasteboard marked concealed, transient or
// auto-generated (a password manager's field) is not returned.
func pbCopy(a pbAPI, sup *suppressor, pid int, o CopyOptions) (CopyResult, error) {
	var res CopyResult
	if o.Timeout <= 0 {
		o.Timeout = defaultCopyTimeout
	}
	if pid <= 0 || a.frontmost() != pid {
		return res, ErrNotForeground
	}

	// 1. Save.
	t := a.now()
	c0 := a.changeCount()
	saved, err := a.save(maxSaveBytes, maxSaveTime)
	res.Save = a.now().Sub(t)
	if err != nil {
		if !errors.Is(err, ErrCannotSave) {
			err = fmt.Errorf("%w: %w", ErrCannotSave, err)
		}
		return res, err
	}
	if a.changeCount() != c0 {
		return res, fmt.Errorf("%w: the clipboard changed while it was saved", ErrCannotSave)
	}

	// 2. Hide the changes from Watch until the restore has settled.
	defer sup.begin(uint32(c0))()

	// 3. ⌘C.
	sent := a.now()
	if err := a.postCopy(); err != nil {
		return res, err
	}

	// 4. Wait for the copy, then for the pasteboard to settle.
	deadline := sent.Add(o.Timeout)
	for a.changeCount() == c0 {
		if !a.now().Before(deadline) {
			res.Wait = a.now().Sub(sent)
			return res, ErrNoSelection
		}
		a.sleep(pbPoll)
	}
	seen, stable := a.changeCount(), a.now()
	for limit := a.now().Add(maxSettle); a.now().Before(limit) && a.now().Sub(stable) < settleDelay; {
		a.sleep(pbPoll)
		if n := a.changeCount(); n != seen {
			seen, stable = n, a.now()
		}
	}
	res.Wait = a.now().Sub(sent)

	// 5. Read the text.
	text, types := a.text()
	if excludedTypes(types) {
		text = ""
	}

	// 6. Restore, unless someone else wrote the pasteboard meanwhile.
	t = a.now()
	var rerr error
	if a.changeCount() == seen {
		if rerr = a.restore(saved); rerr == nil {
			res.Restored = true
		}
	}
	res.Restore = a.now().Sub(t)

	res.Text, res.Truncated = truncateRunes(text, o.MaxChars)
	var errs []error
	if blank(res.Text) {
		res.Text, res.Truncated = "", false
		errs = append(errs, ErrNoSelection)
	}
	if rerr != nil {
		errs = append(errs, fmt.Errorf("%w: %w", ErrNotRestored, rerr))
	}
	return res, errors.Join(errs...)
}
