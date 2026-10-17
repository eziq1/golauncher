package extproc

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/pkg/plugin/protocol"
)

// Report is the result of Validate.
type Report struct {
	Dir      string   `json:"dir"`
	ID       string   `json:"id,omitempty"`
	OK       bool     `json:"ok"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Validate checks the plugin in dir: the manifest schema, the protocol
// version, that run.command can be found, and that every "%key%" in the
// manifest exists in locales/en.json. It does not start the plugin.
func Validate(dir string) (r Report) {
	r = Report{Dir: dir, Errors: []string{}, Warnings: []string{}}
	errorf := func(format string, args ...any) { r.Errors = append(r.Errors, fmt.Sprintf(format, args...)) }
	warnf := func(format string, args ...any) { r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...)) }
	defer func() { r.OK = len(r.Errors) == 0 }()

	data, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		errorf("%v", err)
		return r
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		errorf("%s: %v", ManifestFile, err)
		return r
	}
	if err := json.Unmarshal(data, new(Manifest), json.RejectUnknownMembers(true)); err != nil {
		warnf("%s: %v", ManifestFile, err)
	}
	r.ID = m.ID

	switch {
	case m.ID == "":
		errorf("id is missing")
	case !idRe.MatchString(m.ID):
		errorf("id %q may only contain lower-case letters, digits, '.', '_' and '-'", m.ID)
	case strings.HasPrefix(m.ID, "builtin."):
		errorf("id %q: the builtin. prefix is reserved", m.ID)
	}
	if m.Name == "" {
		errorf("name is missing")
	}
	if m.Version == "" {
		errorf("version is missing")
	}
	switch m.Protocol {
	case 0:
		errorf("protocol is missing (this host speaks protocol %d)", protocol.Version)
	case protocol.Version:
	default:
		errorf("protocol %d is not supported (this host speaks protocol %d)", m.Protocol, protocol.Version)
	}
	for _, p := range m.Platforms {
		if !slices.Contains(plugin.Platforms, p) {
			errorf("platforms: unknown platform %q (valid: %s)", p, strings.Join(plugin.Platforms, ", "))
		}
	}
	if m.CheckPlatforms() == nil && !m.SupportsPlatform(runtime.GOOS) {
		warnf("platforms %q leave out this system (%s): the plugin is not loaded here", m.Platforms, runtime.GOOS)
	}
	if m.Run.Command == "" {
		errorf("run.command is missing")
	} else if _, err := resolveCommand(dir, m.Run.Command); err != nil {
		errorf("run.command %q cannot be found in the plugin directory or on PATH", m.Run.Command)
	}
	for _, p := range m.Permissions {
		if !slices.Contains([]plugin.Permission{plugin.PermClipboard, plugin.PermStorage, plugin.PermNetwork, plugin.PermExec}, p) {
			errorf("unknown permission %q", p)
		}
	}
	names := map[string]bool{}
	for i, c := range m.Commands {
		switch {
		case c.Name == "":
			errorf("commands[%d]: name is missing", i)
		case names[c.Name]:
			errorf("commands[%d]: duplicate name %q", i, c.Name)
		}
		names[c.Name] = true
		if c.Title == "" {
			errorf("commands[%d]: title is missing", i)
		}
		if c.Mode != plugin.ModeView && c.Mode != plugin.ModeNoView {
			errorf("commands[%d]: mode must be %q or %q", i, plugin.ModeView, plugin.ModeNoView)
		}
		if len(c.Arguments) > 3 {
			errorf("commands[%d]: at most 3 arguments", i)
		}
	}
	prefTypes := []plugin.PrefType{plugin.PrefText, plugin.PrefPassword, plugin.PrefNumber, plugin.PrefBool,
		plugin.PrefSelect, plugin.PrefStringList, plugin.PrefPath, plugin.PrefHotkey, plugin.PrefButton}
	for i, p := range m.Preferences {
		if p.Name == "" {
			errorf("preferences[%d]: name is missing", i)
		}
		if !slices.Contains(prefTypes, p.Type) {
			errorf("preferences[%d]: unknown type %q", i, p.Type)
		}
	}

	checkLocales(dir, data, errorf, warnf)
	return r
}

func checkLocales(dir string, manifest []byte, errorf, warnf func(string, ...any)) {
	keys, err := manifestKeys(manifest)
	if err != nil {
		return
	}
	ldir := filepath.Join(dir, "locales")
	catalogs := map[string]map[string]jsontext.Value{}
	entries, _ := os.ReadDir(ldir)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(ldir, e.Name()))
		if err == nil {
			err = checkCatalog(data)
		}
		var cat map[string]jsontext.Value
		if err == nil {
			err = json.Unmarshal(data, &cat)
		}
		if err != nil {
			errorf("locales/%s: %v", e.Name(), err)
			continue
		}
		catalogs[strings.TrimSuffix(e.Name(), ".json")] = cat
	}
	src, ok := catalogs[i18n.Source]
	if !ok {
		if len(keys) > 0 {
			errorf("the manifest uses %%key%% references but locales/%s.json is missing", i18n.Source)
		}
		return
	}
	for _, k := range keys {
		if _, ok := src[k]; !ok {
			errorf("%%%s%% is not defined in locales/%s.json", k, i18n.Source)
		}
	}
	for _, loc := range slices.Sorted(maps.Keys(catalogs)) {
		if loc == i18n.Source {
			continue
		}
		for _, k := range slices.Sorted(maps.Keys(catalogs[loc])) {
			if _, ok := src[k]; !ok {
				warnf("locales/%s.json: %q is not in %s.json", loc, k, i18n.Source)
			}
		}
		for _, k := range keys {
			if _, ok := catalogs[loc][k]; !ok {
				warnf("locales/%s.json: %%%s%% is missing (falls back to %s)", loc, k, i18n.Source)
			}
		}
	}
}
