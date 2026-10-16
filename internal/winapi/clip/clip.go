// Package clip reads and writes the Windows clipboard, watches it for
// changes and pastes into other windows. It needs no GUI framework and may be
// used from any goroutine. On other platforms every function returns
// errors.ErrUnsupported.
//
// Parts of the Win32 clipboard handling (retrying OpenClipboard, the
// registered "PNG" format, DIB decoding) are adapted from MyGo
// (github.com/egoist/mygo, MIT License).
package clip

import (
	"errors"
	"sync"
	"time"
)

// ErrBusy is returned when another program keeps the clipboard open.
var ErrBusy = errors.New("clip: clipboard is busy")

// ErrNotForeground is returned by PasteTo when the target window could not
// be brought to the foreground, and by CopySelection when the window is not
// (or no longer) the foreground window.
var ErrNotForeground = errors.New("clip: target window did not become the foreground window")

// ReadOptions selects what ReadWith fetches.
type ReadOptions struct {
	// Images requests an image when the clipboard holds neither text nor
	// files. Without it no image format is requested at all, so programs
	// that render images on demand are not asked to.
	Images bool
}

// debounceDelay is how long Watch waits for the clipboard to settle; some
// programs update it several times in a row for a single copy.
const debounceDelay = 50 * time.Millisecond

// debouncer calls fn once triggers stop arriving for delay.
type debouncer struct {
	delay time.Duration
	fn    func()
	// hold, when set and true at the time fn is due, postpones fn by delay.
	hold func() bool

	mu      sync.Mutex
	t       *time.Timer
	stopped bool
}

func (d *debouncer) trigger() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	if d.t == nil {
		d.t = time.AfterFunc(d.delay, d.fire)
		return
	}
	d.t.Reset(d.delay)
}

func (d *debouncer) fire() {
	held := d.hold != nil && d.hold()
	d.mu.Lock()
	stopped := d.stopped
	if held && !stopped {
		d.t.Reset(d.delay)
	}
	d.mu.Unlock()
	if !stopped && !held {
		d.fn()
	}
}

// stop cancels a pending call and ignores later triggers.
func (d *debouncer) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	if d.t != nil {
		d.t.Stop()
	}
}
