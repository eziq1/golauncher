package shell

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/winapi/clip"
	winselection "github.com/eziq1/golauncher/internal/winapi/selection"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// selCapture is a capture of the text selected in another app: a
// winselection.Pending, a fake in tests.
type selCapture interface {
	NeedsCopy() bool
	Copy()
	Read(ctx context.Context) (plugin.Selection, bool)
	Err() error
	CopyErr() error
}

// selectionReader is the one path by which golauncher reads the text
// selected in another app (docs/selected-text.md): the launcher's show and
// the Action Ring both capture through it, with the user's selection
// settings (context awareness, the excluded apps, the copy fallback and
// its apps, the length limit); the policies themselves are winselection's.
// The only difference between its callers is allowCopy: the Action Ring
// opening, the middle button still held, never simulates a copy.
type selectionReader struct {
	config func() config.Config
	// begin is phase 1 (winselection.Begin).
	begin func(fg uintptr, o winselection.Options) selCapture
	// async runs fn off the main thread; post runs fn on it, later.
	async, post func(fn func())
	log         *slog.Logger
	// notRestored tells the user that the clipboard was not restored
	// after a copy (main thread).
	notRestored func()
}

// newSelectionReader returns the reader of the system's selection.
func newSelectionReader(cfg func() config.Config, post func(func()), log *slog.Logger, notRestored func()) *selectionReader {
	return &selectionReader{
		config:      cfg,
		begin:       func(fg uintptr, o winselection.Options) selCapture { return winselection.Begin(fg, o) },
		async:       func(fn func()) { go fn() },
		post:        post,
		log:         log,
		notRestored: notRestored,
	}
}

// options returns the capture options of the selection settings sc; the
// copy fallback only with allowCopy.
func (r *selectionReader) options(sc config.SelectionConfig, allowCopy bool) winselection.Options {
	o := winselection.Options{MaxChars: sc.EffectiveMaxChars(), ExcludeApps: sc.ExcludeApps, Log: r.log}
	if allowCopy {
		o.Clipboard, o.ClipboardApps = sc.Clipboard, sc.ClipboardApps
	}
	return o
}

// Capture starts capturing the text selected in window fg: phase 1 now,
// on the main thread, before anything takes the focus; when the settings
// (and allowCopy) choose the clipboard method, the copy then runs off the
// main thread. done runs on the main thread with the capture, ready to
// Read: at once, or after the copy (Capture then reports true). It gets
// nil when context awareness is off or there is no window.
func (r *selectionReader) Capture(fg uintptr, allowCopy bool, done func(selCapture)) (copying bool) {
	cfg := r.config()
	if !cfg.ContextAware || fg == 0 {
		done(nil)
		return false
	}
	sel := r.begin(fg, r.options(cfg.Selection, allowCopy))
	finish := func() {
		if err := sel.CopyErr(); errors.Is(err, clip.ErrNotRestored) {
			r.log.Warn("selection: clipboard not restored", "err", err)
			if r.notRestored != nil {
				r.notRestored()
			}
		}
		done(sel)
	}
	if !sel.NeedsCopy() {
		finish()
		return false
	}
	r.async(func() {
		sel.Copy()
		r.post(finish)
	})
	return true
}

// Read captures (Capture) and reads the text selected in window fg within
// budget, and runs done on the main thread with it, nil for none.
func (r *selectionReader) Read(ctx context.Context, fg uintptr, allowCopy bool, budget time.Duration, done func(*plugin.Selection)) {
	r.Capture(fg, allowCopy, func(sel selCapture) {
		if sel == nil || sel.Err() != nil {
			done(nil)
			return
		}
		r.async(func() {
			ctx, cancel := context.WithTimeout(ctx, budget)
			defer cancel()
			v, ok := sel.Read(ctx)
			r.post(func() {
				if !ok {
					done(nil)
					return
				}
				done(&v)
			})
		})
	})
}
