package backend

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// FakeInstalls is a Fake whose program has made-up installations (R84):
// tests and glctl's fake environment list them, choose one and type a
// custom path without any program on this computer. It is safe for
// concurrent use; set the fields before handing it out.
type FakeInstalls struct {
	*Fake
	// BackendName is the backend's name ("Claude Code" when empty).
	BackendName string
	// Found is what the automatic choice found: Installs, Auto, AutoReason,
	// MinVersion, Status, Message. Discover fills in the program chosen with
	// SetProgram: one of Installs, else it is missing.
	Found Discovery
	// ModelList and ModelErr are what Models returns (R109); EffortLevels and
	// EffortErr what Efforts returns.
	ModelList    ModelList
	ModelErr     error
	EffortLevels []string
	EffortErr    error

	mu        sync.Mutex
	program   string
	searches  int
	refresh   int
	listed    int
	redetects int
}

var (
	_ Discoverer   = (*FakeInstalls)(nil)
	_ ModelLister  = (*FakeInstalls)(nil)
	_ EffortLister = (*FakeInstalls)(nil)
)

// Models implements ModelLister.
func (f *FakeInstalls) Models(context.Context) (ModelList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listed++
	if f.ModelErr != nil {
		return ModelList{}, f.ModelErr
	}
	l := f.ModelList
	l.Models = slices.Clone(l.Models)
	return l, nil
}

// Redetect implements ModelLister.
func (f *FakeInstalls) Redetect() {
	f.mu.Lock()
	f.redetects++
	f.mu.Unlock()
}

// Listed reports how often Models was called, and Redetect.
func (f *FakeInstalls) Listed() (models, redetects int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listed, f.redetects
}

// Efforts implements EffortLister.
func (f *FakeInstalls) Efforts(context.Context) ([]string, error) {
	return slices.Clone(f.EffortLevels), f.EffortErr
}

// Name implements Backend.
func (f *FakeInstalls) Name() string {
	if f.BackendName != "" {
		return f.BackendName
	}
	return f.Fake.Name()
}

// Discover implements Discoverer.
func (f *FakeInstalls) Discover(_ context.Context, refresh bool) Discovery {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searches++
	if refresh {
		f.refresh++
	}
	return f.discovery()
}

// discovery is Found with the chosen program. f.mu must be held.
func (f *FakeInstalls) discovery() Discovery {
	d := f.Found
	d.Installs = slices.Clone(d.Installs)
	d.InUse = d.Auto
	if f.program != "" {
		d.Configured = f.program
		if i := f.index(f.program); i >= 0 {
			d.InUse = i
		} else {
			d.ConfiguredMissing = true
		}
	}
	if in, ok := d.InUseInstall(); ok && !in.Usable() {
		d.Status = StatusFailed
		if in.TooOld {
			d.Status = StatusTooOld
		}
	}
	return d
}

// index returns the index of the installation at path, -1 when none.
func (f *FakeInstalls) index(path string) int {
	return slices.IndexFunc(f.Found.Installs, func(in Install) bool { return strings.EqualFold(filepath.Clean(in.Path), filepath.Clean(path)) })
}

// Searches reports how often Discover was called, and how often with
// refresh.
func (f *FakeInstalls) Searches() (all, refreshed int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.searches, f.refresh
}

// SetProgram implements Discoverer.
func (f *FakeInstalls) SetProgram(path string) {
	f.mu.Lock()
	f.program = path
	f.mu.Unlock()
}

// Probe implements Discoverer: one of the installations is what it is, any
// other path is not found.
func (f *FakeInstalls) Probe(_ context.Context, path string) Install {
	f.mu.Lock()
	defer f.mu.Unlock()
	path = strings.Trim(strings.TrimSpace(path), `"`)
	if i := f.index(path); i >= 0 {
		in := f.Found.Installs[i]
		in.Shadowed = false
		return in
	}
	return Install{Path: path, Source: SourceCustom, VersionError: ProbeNotFound}
}

// Program implements Discoverer.
func (f *FakeInstalls) Program() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if in, ok := f.discovery().InUseInstall(); ok {
		return in.Path
	}
	return ""
}
