package backend

import "context"

// Installations of a backend's program (R84, docs/ai.md §22): which copies of
// an agent CLI are on this computer, which one is used and why, so that the
// settings page can show them and the user can choose another.

// Source is how an installation was found.
type Source string

const (
	// SourcePath: in a directory of PATH.
	SourcePath Source = "path"
	// SourceInstallDir: in a usual install folder searched after PATH
	// (%APPDATA%\npm, ~/.local/bin, Homebrew's bin folders, nvm's, …).
	SourceInstallDir Source = "installDir"
	// SourceApp: the copy a desktop app carries (the Codex app's CLI).
	SourceApp Source = "app"
	// SourceNpmVendor: the native binary inside an npm package, next to its
	// launcher script.
	SourceNpmVendor Source = "npmVendor"
	// SourceCustom: the program the user chose by its path.
	SourceCustom Source = "custom"
)

// Install is one installation of a backend's program.
type Install struct {
	// Path is the program (shown to the user as it is; never logged).
	Path   string `json:"path"`
	Source Source `json:"source"`
	// Launcher marks a launcher script golauncher does not run (Codex's npm
	// launcher, which needs Node.js; gemini.ps1). It is not probed.
	Launcher bool `json:"launcher,omitzero"`
	// Version is what the version command printed ("" when it failed:
	// VersionError says why).
	Version      string `json:"version,omitempty"`
	VersionError string `json:"versionError,omitempty"`
	// TooOld: the version is below the backend's minimum.
	TooOld bool `json:"tooOld,omitzero"`
	// Untested: another major version than the one the protocol was verified
	// with (it works, more carefully).
	Untested bool `json:"untested,omitzero"`
	// Shadowed: usable, but the automatic choice only uses the first program
	// found (Claude Code: the first on PATH), and this one comes later.
	Shadowed bool `json:"shadowed,omitzero"`
}

// Usable reports whether golauncher can run the installation: not a
// launcher script, its version known and not too old.
func (i Install) Usable() bool {
	return !i.Launcher && i.Version != "" && i.VersionError == "" && !i.TooOld
}

// Status of a backend, as the settings page shows it.
const (
	StatusAvailable    = "available"
	StatusNotInstalled = "notInstalled"
	StatusNotSignedIn  = "notSignedIn"
	StatusTooOld       = "tooOld"
	StatusFailed       = "failed"
)

// Why the automatic choice took its installation.
const (
	// ReasonOnly: it is the only usable one.
	ReasonOnly = "only"
	// ReasonHighest: of several usable ones, it has the highest version.
	ReasonHighest = "highestVersion"
	// ReasonFirst: it was found first (on PATH, else in the usual install
	// folders); later ones are Shadowed.
	ReasonFirst = "firstFound"
)

// Discovery is what a backend found of its program.
type Discovery struct {
	// MinVersion is the oldest version the backend accepts.
	MinVersion string `json:"minVersion,omitempty"`
	// Installs are every installation found, in the order they were found
	// (a custom program that was not found otherwise comes last).
	Installs []Install `json:"installs"`
	// Auto is the index of the installation the automatic choice uses (-1:
	// none), AutoReason why (Reason… constants).
	Auto       int    `json:"auto"`
	AutoReason string `json:"autoReason,omitempty"`
	// Configured is the program chosen in the settings ("" automatic).
	// ConfiguredMissing: it no longer exists, so the automatic choice is
	// used instead (the settings and the chat say so).
	Configured        string `json:"configured,omitempty"`
	ConfiguredMissing bool   `json:"configuredMissing,omitzero"`
	// InUse is the index of the installation the backend runs (-1: none).
	InUse int `json:"inUse"`
	// Status is one of the Status… constants; Message explains a status
	// other than available when the backend knows more (e.g. that only a
	// launcher script was found).
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// InUseInstall returns the installation in use.
func (d Discovery) InUseInstall() (Install, bool) {
	if d.InUse < 0 || d.InUse >= len(d.Installs) {
		return Install{}, false
	}
	return d.Installs[d.InUse], true
}

// Discoverer is a backend that can list the installations of its program
// and run the one the user chose. Implementations must be safe for
// concurrent use.
type Discoverer interface {
	// Discover lists the installations. The result is cached; refresh looks
	// and probes again. Probing runs each program's version command
	// (read-only, with a short timeout), so call it in the background.
	Discover(ctx context.Context, refresh bool) Discovery
	// SetProgram sets the program to run ("" for the automatic choice). A
	// change takes effect for the processes started afterwards.
	SetProgram(path string)
	// Probe checks a program the user typed: it must exist, be a program and
	// print an acceptable version. The Install's VersionError (or TooOld)
	// tells what is wrong.
	Probe(ctx context.Context, path string) Install
	// Program is the program the last check chose ("" when not checked yet
	// or none was found).
	Program() string
}

// The VersionError of a probed program that cannot be run at all (the
// settings page words them).
const (
	ProbeNotFound   = "not found"
	ProbeNotProgram = "not a program"
)

// Report is one backend's installations, as the AI plugin reports them.
type Report struct {
	// ID is the backend's ID in the AI plugin's preferences, e.g. "codex";
	// Name its display name.
	ID   string `json:"id"`
	Name string `json:"name"`
	Discovery
}

// Reporter is implemented by the AI plugin: the settings page and glctl list
// the backends' installations through it.
type Reporter interface {
	// BackendReports returns the reports of the backends that can list
	// their installations, in the order of the "Backend" preference.
	BackendReports(ctx context.Context, refresh bool) []Report
	// ProbeProgram checks a program typed for the backend with id.
	ProbeProgram(ctx context.Context, id, path string) Install
}

// ProgramPref is the name of the AI plugin's preference with the program of
// the backend with id: a path, or "" for the automatic choice. The settings
// page writes it; it is not declared in the manifest, so older versions
// ignore it.
func ProgramPref(id string) string { return "program." + id }

// NoticeProgramMissing (EventNotice): the program chosen in the settings no
// longer exists; the automatic choice answers instead.
const NoticeProgramMissing = "program-missing"
