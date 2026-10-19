package agentrt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// Discovery (R84, docs/ai.md §22): every installation of the CLI, how it
// was found, its version, which one the automatic choice takes and why, and
// the program the user chose instead. The rules are the ones Check uses
// (findAll, chooseBest); discovery only also lists what they skip.

// probeTimeout caps one version (or sign-in) command during discovery; the
// installations are probed at the same time.
const probeTimeout = 10 * time.Second

// Found is an installation an adapter's search found (Info.Discover).
type Found struct {
	Path   string
	Source backend.Source
	// Launcher: a launcher script the runtime does not run (it is listed,
	// not probed or chosen).
	Launcher bool
}

var _ backend.Discoverer = (*Runtime)(nil)

// SetProgram implements backend.Discoverer: the CLI at path is run from now
// on ("" for the automatic choice), through Info.ResolvePath. The next check
// (and the next process) uses it; the cached discovery is dropped.
func (r *Runtime) SetProgram(path string) {
	path = r.resolvePath(path)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.o.Path == path {
		return
	}
	r.o.Path = path
	r.cli = nil
	r.disc = nil
}

// resolvePath maps a configured program to the one to run.
func (r *Runtime) resolvePath(path string) string {
	if path == "" || r.info.ResolvePath == nil {
		return path
	}
	return r.info.ResolvePath(path)
}

// configured is the program chosen in the settings ("" automatic).
func (r *Runtime) configured() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.o.Path
}

// Program implements backend.Discoverer: the CLI the last check chose, or
// the one it refused when none could be used.
func (r *Runtime) Program() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cli == nil {
		return r.tried
	}
	return r.cli.path
}

// Discover implements backend.Discoverer.
func (r *Runtime) Discover(ctx context.Context, refresh bool) backend.Discovery {
	r.mu.Lock()
	if !refresh && r.disc != nil {
		d := cloneDiscovery(*r.disc)
		r.mu.Unlock()
		return d
	}
	configured := r.o.Path
	if refresh {
		r.stale = true // Resolved checks again: the CLI may have been updated
	}
	r.mu.Unlock()
	d := r.discover(ctx, configured)
	r.mu.Lock()
	if r.o.Path == configured {
		r.disc = &d
	}
	r.mu.Unlock()
	return cloneDiscovery(d)
}

func cloneDiscovery(d backend.Discovery) backend.Discovery {
	d.Installs = slices.Clone(d.Installs)
	return d
}

// Probe implements backend.Discoverer.
func (r *Runtime) Probe(ctx context.Context, path string) backend.Install {
	path = r.resolvePath(strings.Trim(strings.TrimSpace(path), `"`))
	in := backend.Install{Path: path, Source: backend.SourceCustom}
	switch fi, err := os.Stat(path); {
	case path == "" || err != nil:
		in.VersionError = backend.ProbeNotFound
		return in
	case fi.IsDir() || runtime.GOOS != "windows" && fi.Mode()&0o111 == 0:
		in.VersionError = backend.ProbeNotProgram
		return in
	}
	r.probe(ctx, &in)
	return in
}

// probe runs the version command of in.Path and records what it printed.
func (r *Runtime) probe(ctx context.Context, in *backend.Install) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cli, err := r.checkOne(ctx, in.Path)
	if err != nil {
		if e, ok := errors.AsType[*backend.Error](err); ok && e.Kind == backend.ErrVersion {
			in.Version, in.TooOld = e.Version, true
			return
		}
		in.VersionError = errorMessage(err)
		return
	}
	in.Version, in.Untested = cli.version, cli.untested
}

// errorMessage is the text of err without its kind.
func errorMessage(err error) string {
	if e, ok := errors.AsType[*backend.Error](err); ok && e.Message != "" {
		return e.Message
	}
	return err.Error()
}

// discover finds, probes and chooses as Check does, with configured as the
// chosen program.
func (r *Runtime) discover(ctx context.Context, configured string) backend.Discovery {
	d := backend.Discovery{MinVersion: r.info.MinVersion, Auto: -1, InUse: -1, Configured: configured}
	for _, f := range r.found() {
		d.Installs = append(d.Installs, backend.Install{Path: f.Path, Source: f.Source, Launcher: f.Launcher})
	}
	auto, autoErr := r.autoPaths()
	custom := -1
	if configured != "" {
		if isFile(configured) {
			custom = slices.IndexFunc(d.Installs, func(in backend.Install) bool { return samePath(in.Path, configured) })
			if custom < 0 {
				d.Installs = append(d.Installs, backend.Install{Path: configured, Source: backend.SourceCustom})
				custom = len(d.Installs) - 1
			}
		} else {
			d.ConfiguredMissing = true
		}
	}

	var wg sync.WaitGroup
	for i := range d.Installs {
		if d.Installs[i].Launcher {
			continue
		}
		wg.Go(func() { r.probe(ctx, &d.Installs[i]) })
	}
	wg.Wait()

	considered := func(p string) bool {
		return slices.ContainsFunc(auto, func(a string) bool { return samePath(a, p) })
	}
	usable, shadowed := 0, false
	for i := range d.Installs {
		in := &d.Installs[i]
		if !in.Usable() || in.Source == backend.SourceCustom {
			continue
		}
		if !considered(in.Path) {
			in.Shadowed, shadowed = true, true
			continue
		}
		usable++
		if d.Auto < 0 || CompareVersions(in.Version, d.Installs[d.Auto].Version) > 0 {
			d.Auto = i
		}
	}
	switch {
	case usable > 1:
		d.AutoReason = backend.ReasonHighest
	case usable == 1 && shadowed:
		d.AutoReason = backend.ReasonFirst
	case usable == 1:
		d.AutoReason = backend.ReasonOnly
	}

	d.InUse = d.Auto
	if custom >= 0 {
		d.InUse = custom
	}
	in, ok := d.InUseInstall()
	switch {
	case ok && in.Usable():
		d.Status = backend.StatusAvailable
		if len(r.info.LoginArgs) > 0 {
			pctx, cancel := context.WithTimeout(ctx, probeTimeout)
			_, code, err := r.run(pctx, in.Path, r.info.LoginArgs)
			cancel()
			switch {
			case err != nil:
				d.Status, d.Message = backend.StatusFailed, err.Error()
			case code != 0:
				d.Status = backend.StatusNotSignedIn
			}
		}
	case ok && in.TooOld:
		d.Status = backend.StatusTooOld
	case ok:
		d.Status, d.Message = backend.StatusFailed, in.VersionError
	default:
		d.Status = backend.StatusNotInstalled
		for _, in := range d.Installs {
			if in.Launcher || in.Source == backend.SourceCustom || !considered(in.Path) {
				continue
			}
			if in.TooOld {
				d.Status = backend.StatusTooOld
				break
			}
			if in.VersionError != "" {
				d.Status, d.Message = backend.StatusFailed, in.VersionError
			}
		}
		if d.Status == backend.StatusNotInstalled && autoErr != nil {
			d.Message = errorMessage(autoErr)
		}
	}
	return d
}

// found lists every installation: the adapter's search (Info.Discover), or
// its candidates, or the matches of Info.Commands on PATH and the
// Info.InstallPaths that exist.
func (r *Runtime) found() []Found {
	info := r.a.Info()
	if info.Discover != nil {
		return info.Discover()
	}
	if info.Candidates != nil {
		paths, _ := info.Candidates()
		out := make([]Found, len(paths))
		for i, p := range paths {
			out[i] = Found{Path: p, Source: backend.SourcePath}
		}
		return out
	}
	return commandsFound(info, filepath.SplitList(os.Getenv("PATH")), pathExts(runtime.GOOS, os.Getenv("PATHEXT")), runtime.GOOS)
}

// pathExts are the extensions exec.LookPath tries on Windows (PATHEXT);
// elsewhere none.
func pathExts(goos, pathext string) []string {
	if goos != "windows" {
		return []string{""}
	}
	var exts []string
	for e := range strings.SplitSeq(strings.ToLower(pathext), ";") {
		if e = strings.TrimSpace(e); e != "" {
			if e[0] != '.' {
				e = "." + e
			}
			exts = append(exts, e)
		}
	}
	if len(exts) == 0 {
		exts = []string{".com", ".exe", ".bat", ".cmd"}
	}
	return exts
}

// commandsFound lists the programs named info.Commands in the directories
// dirs (each tried with exts, in the order exec.LookPath tries them, so the
// first is the one find chooses), then the info.InstallPaths that exist.
func commandsFound(info Info, dirs, exts []string, goos string) []Found {
	var out []Found
	add := func(p string, src backend.Source) {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if !slices.ContainsFunc(out, func(f Found) bool { return samePath(f.Path, p) }) {
			out = append(out, Found{Path: p, Source: src})
		}
	}
	for _, name := range info.Commands {
		for _, d := range dirs {
			d = strings.Trim(strings.TrimSpace(d), `"`)
			if d == "" {
				continue
			}
			for _, ext := range exts {
				p := filepath.Join(d, name+ext)
				if isFile(p) && (goos == "windows" || isExecutable(p)) {
					add(p, backend.SourcePath)
				}
			}
		}
	}
	for _, p := range info.InstallPaths {
		if isFile(p) {
			add(p, backend.SourceInstallDir)
		}
	}
	return out
}

// sourceOf returns how the CLI at path was found (SourceCustom when it was
// configured and not found otherwise).
func (r *Runtime) sourceOf(path string) backend.Source {
	for _, f := range r.found() {
		if samePath(f.Path, path) {
			return f.Source
		}
	}
	return backend.SourceCustom
}

// samePath reports whether a and b name the same file (ignoring case on
// Windows).
func samePath(a, b string) bool {
	if abs, err := filepath.Abs(a); err == nil {
		a = abs
	}
	if abs, err := filepath.Abs(b); err == nil {
		b = abs
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// isFile reports whether p is an existing file.
func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// isExecutable reports whether p is a file that may be run.
func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}
