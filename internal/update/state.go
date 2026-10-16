package update

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Files in the update folder (%LOCALAPPDATA%\golauncher\update).
const (
	stateFile    = "state.json"
	ResultFile   = "result.json"
	planFile     = "plan.json"
	downloadsDir = "downloads"
	helperDir    = "helper"
	previousDir  = "previous"
	// HelperName is the update helper's file name: not golauncher.exe, so
	// that stopping golauncher does not stop it.
	HelperName = "golauncher-update.exe"
)

// maxAttempts is how often installing a version may fail before it is no
// longer tried automatically.
const maxAttempts = 3

// State is what the updater remembers (state.json), apart from the user's
// settings, which are in config.json.
type State struct {
	// LastCheck is when the last check succeeded; LastAttempt when the last
	// one ran; LastError why it failed.
	LastCheck   time.Time `json:"lastCheck,omitzero"`
	LastAttempt time.Time `json:"lastAttempt,omitzero"`
	LastError   string    `json:"lastError,omitempty"`
	// LimitedUntil is when GitHub's rate limit ends.
	LimitedUntil time.Time `json:"limitedUntil,omitzero"`
	// ETag and Latest are the last answer of the API.
	ETag   string   `json:"etag,omitempty"`
	Latest *Release `json:"latest,omitempty"`
	// Skipped is the version the user chose to skip; Bad the versions that
	// were rolled back; Attempts the failed installs by version.
	Skipped  string         `json:"skipped,omitempty"`
	Bad      []string       `json:"bad,omitempty"`
	Attempts map[string]int `json:"attempts,omitempty"`
	// Staged is the update downloaded and ready to install.
	Staged *Staged `json:"staged,omitempty"`
	// FailedDownload is the version whose download last failed, at
	// FailedAt; it is tried again RetryAfter later.
	FailedDownload string    `json:"failedDownload,omitempty"`
	FailedAt       time.Time `json:"failedAt,omitzero"`
	// Notified is the version last announced in "notify" mode.
	Notified string `json:"notified,omitempty"`
	// Reported is the ID of the last helper result shown to the user.
	Reported string `json:"reported,omitempty"`
}

// blocked reports whether version must not be installed automatically.
func (s State) blocked(version string) bool {
	return slices.Contains(s.Bad, version) || s.Attempts[version] >= maxAttempts
}

// loadState reads path; a missing or damaged file is a new state.
func loadState(path string) State {
	var s State
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &s) != nil {
			s = State{}
		}
	}
	return s
}

// writeJSON writes v to path atomically.
func writeJSON(path string, v any) error {
	data, err := json.Marshal(v, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Outcome is how an installation by the helper ended.
type Outcome string

const (
	OutcomeApplying   Outcome = "applying"   // the helper is at work
	OutcomeUpdated    Outcome = "updated"    // the new version runs
	OutcomeRolledBack Outcome = "rolledBack" // it failed to start; the old one is back
	OutcomeFailed     Outcome = "failed"     // it could not be installed; nothing changed
)

// Result is what the helper reports (result.json).
type Result struct {
	ID      string    `json:"id"`
	From    string    `json:"from"`
	To      string    `json:"to"`
	Outcome Outcome   `json:"outcome"`
	Error   string    `json:"error,omitempty"`
	Page    string    `json:"page,omitempty"`
	Time    time.Time `json:"time"`
}

// ReadResult reads the helper's result in dir.
func ReadResult(dir string) (Result, bool) {
	data, err := os.ReadFile(filepath.Join(dir, ResultFile))
	if err != nil {
		return Result{}, false
	}
	var r Result
	if json.Unmarshal(data, &r) != nil || r.ID == "" {
		return Result{}, false
	}
	return r, true
}

// removeAll removes path; a missing one is fine.
func removeAll(path string) error {
	if err := os.RemoveAll(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
