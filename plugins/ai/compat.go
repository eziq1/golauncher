package ai

import (
	"context"
	"strings"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// An older CLI (docs/ai.md §22): the backend reports the features it runs
// without (backend.Status.Reduced); the chat's footer and the "Check"
// button in Settings › Plugins › AI say so and suggest the update.

// prefCheck is the button that checks the chosen backend.
const prefCheck = "checkBackend"

// checkTimeout caps the check run from the settings.
const checkTimeout = 30 * time.Second

// checkPreference is the "Check" button preference.
func checkPreference() plugin.Preference {
	return plugin.Preference{Name: prefCheck, Type: plugin.PrefButton, Title: "%pref.checkBackend.title%",
		Description: "%pref.checkBackend.description%", Action: &plugin.ActionDo{Kind: plugin.ActionCallback, Target: prefCheck}}
}

// reducedFeatures are the localized names of the features st reports as
// reduced, of those the preferences ask for, joined for a sentence ("" when
// none).
func (p *Plugin) reducedFeatures(st backend.Status, modelSet bool) string {
	l := p.h.I18n()
	prefs := p.h.Preferences()
	var names []string
	for _, f := range st.Reduced {
		switch f {
		case backend.FeatureWebSearch:
			if !prefs.Bool(prefWebSearch) {
				continue
			}
		case backend.FeatureLauncherTools:
			if !prefs.Bool(prefTools) {
				continue
			}
		case backend.FeatureModel:
			if !modelSet {
				continue
			}
		case backend.FeatureStreaming:
		default:
			continue
		}
		names = append(names, featureName(l, f))
	}
	return strings.Join(names, l.T("feature.separator"))
}

// checkBackend runs the chosen backend's check now and reports it as a
// toast: the version found, and what an older version runs without.
func (p *Plugin) checkBackend(ctx context.Context) plugin.Result {
	l := p.h.I18n()
	id, be := p.be.current()
	name := be.Name()
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	st, err := be.Check(ctx)
	if err != nil {
		msg := backendText(l, string(backend.KindOf(err)), id)
		if backend.KindOf(err) == backend.ErrFailed {
			msg = l.T("error.failed", "backend", name)
		}
		if t := strings.TrimSpace(errorText(err)); t != "" && t != string(backend.KindOf(err)) {
			msg += "\n" + t
		}
		return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastFailure, Title: l.T("check.failed", "backend", name), Message: msg}}
	}
	p.mu.Lock()
	p.checks[id] = checked{at: time.Now(), status: st}
	p.mu.Unlock()
	t := &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("check.found", "backend", name, "version", st.Version)}
	var lines []string
	if st.Untested {
		lines = append(lines, l.T("check.untested"))
	}
	model := ""
	if o, ok := p.be.option(id); ok {
		model = p.h.Preferences().String(modelPref(o))
	}
	if f := p.reducedFeatures(st, model != ""); f != "" {
		t.Style = plugin.ToastInfo
		lines = append(lines, l.T("check.reduced", "backend", name, "features", f))
	}
	t.Message = strings.Join(lines, "\n")
	return plugin.Result{Toast: t}
}

// featureName is the localized name of a backend.Feature* code.
func featureName(l plugin.Localizer, f string) string {
	switch f {
	case backend.FeatureWebSearch:
		return l.T("feature.web-search")
	case backend.FeatureLauncherTools:
		return l.T("feature.launcher-tools")
	case backend.FeatureStreaming:
		return l.T("feature.streaming")
	case backend.FeatureModel:
		return l.T("feature.model")
	}
	return f
}
