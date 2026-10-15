package plugin

import (
	"encoding/json/jsontext"
	"log/slog"
	"time"
)

// Host is the plugin's only gateway to the system. A Host passed to Init is
// scoped to that plugin: Storage, Preferences, I18n and DataDir are private
// to it.
type Host interface {
	Logger() *slog.Logger
	// Now returns the current time; tests and glctl --now may fix it.
	Now() time.Time
	Storage() KV
	Preferences() Preferences
	Clipboard() Clipboard
	Events() Events
	I18n() Localizer
	// DataDir is a directory the plugin may write to. It exists.
	DataDir() string
	// CacheDir is a directory for data that can be rebuilt. It exists.
	CacheDir() string
	// Go runs fn on a new goroutine. A panic in fn is logged and contained
	// instead of crashing the launcher. Plugins must start all background
	// work through Go (a test enforces this for built-in plugins); to wait
	// for it, call wg.Add(1) before and defer wg.Done() inside fn.
	Go(fn func())
	// Invalidate tells the host that data the plugin shows changed. The host
	// pulls the plugin's root results and its open views with ID viewID (""
	// for all) again, coalescing bursts. Safe from any goroutine.
	Invalidate(viewID string)
	// Render pushes new content for the open view instance (see
	// ViewRequest.Instance). Pushes for closed or unknown instances are
	// dropped. Safe from any goroutine.
	Render(instance string, v View)

	// RunProcess queues a command line as a background task and returns its
	// ID at once. It needs PermExec; without it the task fails at once. The
	// host shows the task in Background Tasks, keeps its output and notifies
	// when it ends (TaskSpec.Notify). See docs/background-tasks.md.
	RunProcess(spec TaskSpec, cmd ProcessSpec) string
	// RunTask queues fn as a background task (work done in Go) and returns
	// its ID. fn must return when its context is cancelled (Stop or quit).
	// A panic in fn fails the task instead of crashing the launcher.
	RunTask(spec TaskSpec, fn TaskFunc) string
	// Toast shows a short message: in the launcher's footer when it is
	// visible, otherwise as a Windows notification. Safe from any goroutine.
	Toast(t Toast)
}

// KV is a small persistent key/value store.
type KV interface {
	Get(key string) ([]byte, bool, error)
	Set(key string, value []byte) error
	Delete(key string) error
}

// Preferences reads the values of the plugin's declared preferences.
// Missing values fall back to the declared default.
type Preferences interface {
	Raw(name string) (jsontext.Value, bool)
	String(name string) string
	Int(name string) int
	Bool(name string) bool
	Strings(name string) []string
}

// ClipboardContent is a snapshot of the clipboard.
type ClipboardContent struct {
	Text  string
	Image []byte // PNG
	Files []string
	// SourceApp is the executable name of the clipboard owner, if known.
	SourceApp string
	// Exclude is set when the content asked not to be recorded in clipboard
	// history (e.g. copied from a password manager).
	Exclude bool
}

// Clipboard reads and writes the system clipboard.
type Clipboard interface {
	Read() (ClipboardContent, error)
	WriteText(text string) error
	WriteImage(png []byte) error
	WriteFiles(paths []string) error
}

// Event names published by the host.
const (
	EventClipboardChanged = "clipboard-changed" // payload: nil
	EventWindowShown      = "window-shown"
	EventWindowHidden     = "window-hidden" // the launcher panel was hidden
	EventLocaleChanged    = "locale-changed"
	EventConfigChanged    = "config-changed"
)

// Events lets a plugin subscribe to host events. Handlers run on a
// background goroutine and must not block for long.
type Events interface {
	Subscribe(name string, fn func()) (unsubscribe func())
}

// Localizer translates messages from the plugin's catalogs and formats
// values for the current locale.
type Localizer interface {
	// Locale is the BCP 47 tag of the current locale, e.g. "en".
	Locale() string
	// T translates id. kv are alternating name/value template arguments,
	// referenced in messages as {{.name}}.
	T(id string, kv ...any) string
	// N translates a message with plural forms selected by count, which is
	// also available to the message as {{.Count}}.
	N(id string, count int, kv ...any) string
	// Number formats a number with the locale's digit grouping and decimal mark.
	Number(v float64) string
	// GroupDigits formats a plain decimal string such as "-1234567.25" with
	// the locale's separators. It is meant for exact (big) numbers.
	GroupDigits(decimal string) string
	// RelativeTime formats t relative to now, e.g. "5 minutes ago".
	RelativeTime(t, now time.Time) string
}
