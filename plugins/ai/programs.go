package ai

import (
	"context"
	"strings"
	"sync"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// The programs of the backends (R84, docs/ai.md §22): the settings page
// (Settings › Plugins › AI › Backends) and `glctl ai backends` list each
// backend's installations through the plugin (backend.Reporter); the user's
// choice is the preference "program.<backend ID>", a path or "" for the
// automatic choice. It is not declared in the manifest (the settings page
// has its own section for it), so older versions ignore it.

var _ backend.Reporter = (*Plugin)(nil)

// discoverer returns the backend of o when it can list its installations.
func discoverer(o BackendOption) (backend.Discoverer, bool) {
	d, ok := o.Backend.(backend.Discoverer)
	return d, ok
}

// applyPrograms hands the programs chosen in the preferences to the
// backends; a backend whose program changed is checked again.
func (p *Plugin) applyPrograms() {
	if p.h == nil {
		return
	}
	prefs := p.h.Preferences()
	for _, o := range p.be.opts {
		d, ok := discoverer(o)
		if !ok {
			continue
		}
		path := strings.TrimSpace(prefs.String(backend.ProgramPref(o.ID)))
		p.mu.Lock()
		old, seen := p.programs[o.ID]
		p.programs[o.ID] = path
		if seen && old != path {
			delete(p.checks, o.ID)
		}
		p.mu.Unlock()
		d.SetProgram(path)
	}
}

// BackendReports implements backend.Reporter. The backends are probed at
// the same time.
func (p *Plugin) BackendReports(ctx context.Context, refresh bool) []backend.Report {
	p.applyPrograms()
	var out []backend.Report
	var ds []backend.Discoverer
	for _, o := range p.be.opts {
		if ml, ok := o.Backend.(backend.ModelLister); ok && refresh && o.DynamicModels {
			ml.Redetect() // "Re-detect" lists the models again too (R109)
		}
	}
	for _, o := range p.be.opts {
		if d, ok := discoverer(o); ok {
			out = append(out, backend.Report{ID: o.ID, Name: o.Backend.Name()})
			ds = append(ds, d)
		}
	}
	var wg sync.WaitGroup
	for i, d := range ds {
		run := func() { out[i].Discovery = d.Discover(ctx, refresh) }
		if p.h == nil { // not initialized (the plugin is off)
			run()
			continue
		}
		wg.Add(1)
		p.h.Go(func() {
			defer wg.Done()
			run()
		})
	}
	wg.Wait()
	return out
}

// ProbeProgram implements backend.Reporter.
func (p *Plugin) ProbeProgram(ctx context.Context, id, path string) backend.Install {
	o, ok := p.be.option(id)
	d, can := discoverer(o)
	if !ok || !can {
		return backend.Install{Path: path, Source: backend.SourceCustom, VersionError: "unknown backend " + id}
	}
	return d.Probe(ctx, path)
}

// programOf is the program a chat's backend runs ("" when unknown).
func (p *Plugin) programOf(sess Session) string {
	_, b := p.backendOf(sess)
	if d, ok := b.(backend.Discoverer); ok {
		return d.Program()
	}
	return ""
}

// wrongInstallation reports whether an error of kind with the backend's
// message msg may come from running the wrong one of several installations:
// the version is too old, or the service no longer supports this client.
func wrongInstallation(kind, msg string) bool {
	if kind == string(backend.ErrVersion) {
		return true
	}
	if kind != string(backend.ErrFailed) {
		return false
	}
	msg = strings.ToLower(msg)
	for _, s := range []string{"no longer supported", "unsupported client", "client is not supported", "requires a newer version", "please update"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
