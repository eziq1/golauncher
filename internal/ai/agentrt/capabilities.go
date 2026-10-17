package agentrt

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// The capability probe (docs/ai.md §22): an agent CLI's command line grows
// with its versions, and a flag an older version does not know makes it
// exit at once ("error: unknown option '--x'"). Check therefore asks the
// CLI which flags it has (Info.HelpArgs, e.g. --help) and lets the adapter
// decide how to run it (Info.Fit): which flags to leave out or replace,
// which features to turn off, or that it is too old.

// Flags are the long options (and adapter-defined markers) a CLI reported,
// e.g. "--print". A nil Flags is unknown: the probe failed or there was none.
type Flags map[string]bool

// Has reports whether f has flag.
func (f Flags) Has(flag string) bool { return f[flag] }

// List returns the flags, sorted.
func (f Flags) List() []string {
	out := make([]string, 0, len(f))
	for k := range f {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// FlagsOf returns the flags of list.
func FlagsOf(list ...string) Flags {
	f := Flags{}
	for _, s := range list {
		f[s] = true
	}
	return f
}

// ParseHelpFlags returns the long options listed in a CLI's help text, nil
// when there are none. It reads the option lines of commander (Claude Code,
// "  -p, --print", "  --allowedTools, --allowed-tools <tools...>"), yargs
// and clap ("      --oss"): lines indented by at most 8 spaces that start
// with "-". Description lines, indented further, are not read, so a flag
// that is only mentioned in another flag's description does not count.
func ParseHelpFlags(out string) Flags {
	f := Flags{}
	for line := range strings.Lines(out) {
		line = strings.TrimRight(line, "\r\n")
		trimmed := strings.TrimLeft(line, " \t")
		indent := len(line) - len(trimmed)
		if indent == 0 || indent > 8 || !strings.HasPrefix(trimmed, "-") {
			continue
		}
		// The option part ends where the description starts (two spaces).
		if i := strings.Index(trimmed, "  "); i >= 0 {
			trimmed = trimmed[:i]
		}
		for tok := range strings.FieldsFuncSeq(trimmed, func(r rune) bool { return r == ' ' || r == ',' || r == '=' || r == '|' }) {
			if !strings.HasPrefix(tok, "--") || len(tok) < 3 {
				continue
			}
			if i := strings.IndexAny(tok, "<[("); i >= 0 {
				tok = tok[:i]
			}
			if isFlagName(tok[2:]) {
				f[tok] = true
			}
		}
	}
	if len(f) == 0 {
		return nil
	}
	return f
}

// isFlagName reports whether s is a flag's name without its dashes.
func isFlagName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// Fit is how the runtime uses a CLI, as the adapter decided from its version
// and flags (Info.Fit).
type Fit struct {
	// PerTurn: the CLI has no usable persistent mode; every turn runs a
	// process of its own.
	PerTurn bool
	// Reduced are the features turned off or degraded for this CLI
	// (backend.Feature* codes), shown to the user.
	Reduced []string
	// Dropped describes what the command line leaves out or replaces (for
	// the log), e.g. "--permission-prompts → --permission-mode dontAsk".
	Dropped []string
}

// helpTimeout caps the capability probe; a CLI that takes longer is run
// with what its version is known to support.
const helpTimeout = 10 * time.Second

// helpOutputMax is how much of the help text is read (Claude Code's is
// about 22 KB).
const helpOutputMax = 256 << 10

// capsFile is the probe cache in the working directory.
const capsFile = "capabilities.json"

// capsKey identifies a CLI binary: a different file, version, size or
// modification time is probed again.
type capsKey struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime"` // Unix nanoseconds
}

// capsEntry is a cached probe.
type capsEntry struct {
	capsKey
	Flags []string `json:"flags"`
}

// maxCapsEntries caps the entries kept on disk (old installations drop out).
const maxCapsEntries = 8

// capsCache is the in-memory probe cache of a runtime (in front of the file).
type capsCache struct {
	mu sync.Mutex
	m  map[capsKey]Flags
}

// probeFlags returns the flags of the CLI at path (version ver), from the cache
// or by running Info.HelpArgs; nil when they cannot be told. fresh reports
// that the CLI was run (the caller logs what that changed, once per probe).
func (r *Runtime) probeFlags(ctx context.Context, path, ver string) (flags Flags, fresh bool) {
	if len(r.info.HelpArgs) == 0 {
		return nil, false
	}
	key := capsKey{Path: path, Version: ver}
	if fi, err := os.Stat(path); err == nil {
		key.Size, key.ModTime = fi.Size(), fi.ModTime().UnixNano()
	}
	r.caps.mu.Lock()
	if r.caps.m == nil {
		r.caps.m = map[capsKey]Flags{}
	}
	if f, ok := r.caps.m[key]; ok {
		r.caps.mu.Unlock()
		return f, false
	}
	r.caps.mu.Unlock()
	if f, ok := r.loadCaps(key); ok {
		r.caps.mu.Lock()
		r.caps.m[key] = f
		r.caps.mu.Unlock()
		return f, false
	}
	ctx, cancel := context.WithTimeout(ctx, helpTimeout)
	defer cancel()
	start := time.Now()
	out, code, err := r.runMax(ctx, path, r.info.HelpArgs, helpOutputMax)
	parse := r.info.ParseHelp
	if parse == nil {
		parse = ParseHelpFlags
	}
	if err != nil || code != 0 {
		// Not cached: the next Check tries again.
		r.log.Warn("the agent CLI's help could not be read; using what its version is known to support",
			"version", ver, "error", errString(err), "code", code, "duration", time.Since(start))
		return nil, true
	}
	flags = parse(out)
	if flags == nil {
		r.log.Warn("the agent CLI's help lists no options; using what its version is known to support", "version", ver, "len", len(out))
	}
	r.caps.mu.Lock()
	r.caps.m[key] = flags
	r.caps.mu.Unlock()
	if flags != nil {
		r.saveCaps(key, flags)
	}
	r.log.Debug("agent CLI probed", "version", ver, "flags", len(flags), "duration", time.Since(start))
	return flags, true
}

// loadCaps returns the cached flags of key from the working directory.
func (r *Runtime) loadCaps(key capsKey) (Flags, bool) {
	for _, e := range r.readCaps() {
		if e.capsKey == key && len(e.Flags) > 0 {
			return FlagsOf(e.Flags...), true
		}
	}
	return nil, false
}

// readCaps reads the cache file (nothing when it is missing or broken).
func (r *Runtime) readCaps() []capsEntry {
	dir, err := r.dir()
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(dir, capsFile))
	if err != nil {
		return nil
	}
	var entries []capsEntry
	if json.Unmarshal(b, &entries) != nil {
		return nil
	}
	return entries
}

// saveCaps records the flags of key in the working directory (best effort:
// a failure only costs another probe).
func (r *Runtime) saveCaps(key capsKey, flags Flags) {
	r.capsFileMu.Lock()
	defer r.capsFileMu.Unlock()
	entries := slices.DeleteFunc(r.readCaps(), func(e capsEntry) bool { return e.Path == key.Path || e.capsKey == key })
	entries = append(entries, capsEntry{capsKey: key, Flags: flags.List()})
	if n := len(entries) - maxCapsEntries; n > 0 {
		entries = entries[n:]
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return
	}
	dir, err := r.dir()
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, capsFile+".*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(append(b, '\n'))
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), filepath.Join(dir, capsFile)) != nil {
		os.Remove(tmp.Name())
		r.log.Debug("saving the capability cache failed")
	}
}

// fit runs Info.Fit for a CLI (the full feature set without it).
func (r *Runtime) fit(ver string, flags Flags) (Fit, error) {
	if r.info.Fit == nil {
		return Fit{}, nil
	}
	fit, err := r.info.Fit(ver, flags)
	if e, ok := errors.AsType[*backend.Error](err); ok && e.Version == "" {
		e.Version = ver
	}
	return fit, err
}
