package ai

import (
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// An older CLI (docs/ai.md §23): the backend reports the features it runs
// without (backend.Status.Reduced); the chat's footer says so. The version
// in use is on Settings › Plugins › AI › Backends (R84).

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
