package everything

import "runtime"

// spotlightSystem is set where the system backend searches with Spotlight
// instead of Everything (macOS). New words the plugin for it; the plugin of
// the fake environment (NewWithBackend) keeps Everything's wording on every
// platform.
var spotlightSystem = runtime.GOOS == "darwin"

// spotlightKeys are the messages worded differently for Spotlight: their
// variants have the key with ".spotlight" appended.
var spotlightKeys = map[string]bool{
	"plugin.name":                            true,
	"plugin.description":                     true,
	"command.search.subtitle":                true,
	"command.search.keyword.everything":      true,
	"pref.hideSystemFiles.description":       true,
	"pref.extraLowPriorityPaths.description": true,
	"unavailable.notInstalled":               true,
	"view.hint":                              true,
	"view.noResults":                         true,
	"context.title":                          true,
	"context.keywords":                       true,
	"action.reveal":                          true,
}

// msg is the message key k, or its Spotlight variant for Spotlight.
func (p *Plugin) msg(k string) string {
	if p.spotlight && spotlightKeys[k] {
		return k + ".spotlight"
	}
	return k
}
