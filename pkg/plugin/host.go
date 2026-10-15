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
