package homebrew

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"maps"
	"slices"
	"strings"
)

// Installed is an installed formula or cask, from
// `brew info --json=v2 --installed`.
type Installed struct {
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	FullName string   `json:"fullName,omitempty"` // "user/tap/name" for other taps
	Tap      string   `json:"tap,omitempty"`
	Names    []string `json:"names,omitempty"` // display names of a cask
	Desc     string   `json:"desc,omitempty"`
	Homepage string   `json:"homepage,omitempty"`
	// Version is the installed version (the newest, if several are).
	Version string `json:"version"`
	// Latest is the version the tap has now.
	Latest      string `json:"latest,omitempty"`
	Outdated    bool   `json:"outdated,omitzero"`
	Pinned      bool   `json:"pinned,omitzero"`
	AutoUpdates bool   `json:"autoUpdates,omitzero"`
	// OnRequest is false for a formula installed only as a dependency.
	OnRequest bool `json:"onRequest,omitzero"`
}

// Title is how the package is shown.
func (in *Installed) Title() string {
	if in.Kind == KindCask && len(in.Names) > 0 && in.Names[0] != "" {
		return in.Names[0]
	}
	return in.Name
}

// Ref is the name brew commands take: the full name for packages of other
// taps.
func (in *Installed) Ref() string { return cmp.Or(in.FullName, in.Name) }

// infoFormula is a formula in `brew info --json=v2` output.
type infoFormula struct {
	Name     string         `json:"name"`
	FullName string         `json:"full_name"`
	Tap      string         `json:"tap"`
	Aliases  []string       `json:"aliases"`
	Desc     string         `json:"desc"`
	Homepage string         `json:"homepage"`
	License  jsontext.Value `json:"license"`
	Versions struct {
		Stable string `json:"stable"`
		Head   string `json:"head"`
	} `json:"versions"`
	Revision          int      `json:"revision"`
	Dependencies      []string `json:"dependencies"`
	BuildDependencies []string `json:"build_dependencies"`
	ConflictsWith     []string `json:"conflicts_with"`
	Caveats           string   `json:"caveats"`
	KegOnly           bool     `json:"keg_only"`
	Installed         []struct {
		Version          string `json:"version"`
		InstalledOnReq   bool   `json:"installed_on_request"`
		InstalledAsDep   bool   `json:"installed_as_dependency"`
		PouredFromBottle bool   `json:"poured_from_bottle"`
		Time             int64  `json:"time"`
	} `json:"installed"`
	LinkedKeg         string `json:"linked_keg"`
	Pinned            bool   `json:"pinned"`
	Outdated          bool   `json:"outdated"`
	Deprecated        bool   `json:"deprecated"`
	DeprecationReason string `json:"deprecation_reason"`
	Disabled          bool   `json:"disabled"`
	DisableReason     string `json:"disable_reason"`
}

// infoCask is a cask in `brew info --json=v2` output.
type infoCask struct {
	Token             string           `json:"token"`
	FullToken         string           `json:"full_token"`
	Tap               string           `json:"tap"`
	Name              []string         `json:"name"`
	Desc              string           `json:"desc"`
	Homepage          string           `json:"homepage"`
	URL               string           `json:"url"`
	Version           string           `json:"version"`
	Installed         string           `json:"installed"`
	Outdated          bool             `json:"outdated"`
	AutoUpdates       bool             `json:"auto_updates"`
	Caveats           string           `json:"caveats"`
	Artifacts         []jsontext.Value `json:"artifacts"`
	ConflictsWith     jsontext.Value   `json:"conflicts_with"`
	DependsOn         jsontext.Value   `json:"depends_on"`
	Deprecated        bool             `json:"deprecated"`
	DeprecationReason string           `json:"deprecation_reason"`
	Disabled          bool             `json:"disabled"`
	DisableReason     string           `json:"disable_reason"`
}

type infoOutput struct {
	Formulae []infoFormula `json:"formulae"`
	Casks    []infoCask    `json:"casks"`
}

// parseInfo parses `brew info --json=v2` output. Text that brew printed
// before the JSON (a warning on stdout) is skipped.
func parseInfo(out []byte) (*infoOutput, error) {
	if i := strings.Index(string(out), "{"); i > 0 {
		out = out[i:]
	}
	var info infoOutput
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// ParseInstalled parses the output of `brew info --json=v2 --installed`.
func ParseInstalled(out []byte) ([]Installed, error) {
	info, err := parseInfo(out)
	if err != nil {
		return nil, err
	}
	items := make([]Installed, 0, len(info.Formulae)+len(info.Casks))
	for _, f := range info.Formulae {
		if len(f.Installed) == 0 {
			continue
		}
		in := Installed{Kind: KindFormula, Name: f.Name, Tap: f.Tap, Desc: f.Desc, Homepage: f.Homepage,
			Latest: pkgVersion(f.Versions.Stable, f.Revision), Outdated: f.Outdated, Pinned: f.Pinned}
		if f.FullName != f.Name {
			in.FullName = f.FullName
		}
		// The linked keg is the one in use; else the newest.
		in.Version = f.Installed[len(f.Installed)-1].Version
		for _, k := range f.Installed {
			if k.Version == f.LinkedKeg {
				in.Version = k.Version
			}
			in.OnRequest = in.OnRequest || k.InstalledOnReq
		}
		items = append(items, in)
	}
	for _, c := range info.Casks {
		if c.Installed == "" {
			continue
		}
		in := Installed{Kind: KindCask, Name: c.Token, Tap: c.Tap, Names: c.Name, Desc: c.Desc, Homepage: c.Homepage,
			Version: c.Installed, Latest: c.Version, Outdated: c.Outdated, AutoUpdates: c.AutoUpdates, OnRequest: true}
		if c.FullToken != c.Token {
			in.FullName = c.FullToken
		}
		items = append(items, in)
	}
	return items, nil
}

// Details is what `brew info --json=v2` says about one package.
type Details struct {
	Kind       string
	Name       string
	FullName   string
	Tap        string
	Names      []string
	Desc       string
	Homepage   string
	License    string
	Version    string // the tap's version
	Installed  []string
	Outdated   bool
	Pinned     bool
	KegOnly    bool
	AutoUpd    bool
	Aliases    []string
	Deps       []string
	BuildDeps  []string
	Conflicts  []string
	Apps       []string // a cask's .app artifacts
	Requires   string   // a cask's minimum macOS, e.g. ">= :monterey"
	Caveats    string
	Deprecated string // the reason, or "yes"; "" when not deprecated
	Disabled   string
	URL        string // a cask's download
}

// reasonText is the shown reason of a deprecation: brew's reason symbols
// read as words.
func reasonText(flag bool, reason string) string {
	if !flag {
		return ""
	}
	if reason == "" {
		return "yes"
	}
	return strings.ReplaceAll(strings.TrimPrefix(reason, ":"), "_", " ")
}

// ParseDetails parses `brew info --json=v2 --formula|--cask <name>`.
func ParseDetails(out []byte) (*Details, error) {
	info, err := parseInfo(out)
	if err != nil {
		return nil, err
	}
	switch {
	case len(info.Formulae) > 0:
		f := info.Formulae[0]
		d := &Details{Kind: KindFormula, Name: f.Name, FullName: f.FullName, Tap: f.Tap, Desc: f.Desc, Homepage: f.Homepage,
			License: licenseText(f.License), Version: pkgVersion(f.Versions.Stable, f.Revision),
			Outdated: f.Outdated, Pinned: f.Pinned, KegOnly: f.KegOnly, Aliases: f.Aliases,
			Deps: f.Dependencies, BuildDeps: f.BuildDependencies, Conflicts: f.ConflictsWith,
			Caveats:    strings.TrimSpace(f.Caveats),
			Deprecated: reasonText(f.Deprecated, f.DeprecationReason), Disabled: reasonText(f.Disabled, f.DisableReason)}
		for _, k := range f.Installed {
			d.Installed = append(d.Installed, k.Version)
		}
		return d, nil
	case len(info.Casks) > 0:
		c := info.Casks[0]
		d := &Details{Kind: KindCask, Name: c.Token, FullName: c.FullToken, Tap: c.Tap, Names: c.Name, Desc: c.Desc,
			Homepage: c.Homepage, Version: c.Version, Outdated: c.Outdated, AutoUpd: c.AutoUpdates,
			Caveats: strings.TrimSpace(c.Caveats), URL: c.URL, Apps: caskApps(c.Artifacts),
			Conflicts: caskConflicts(c.ConflictsWith), Requires: caskMacOS(c.DependsOn),
			Deprecated: reasonText(c.Deprecated, c.DeprecationReason), Disabled: reasonText(c.Disabled, c.DisableReason)}
		if c.Installed != "" {
			d.Installed = []string{c.Installed}
		}
		return d, nil
	}
	return nil, errors.New("brew info returned no package")
}

// caskApps returns the .app names among a cask's artifacts
// ([{"app": ["Firefox.app"], "target": ...}, ...]).
func caskApps(artifacts []jsontext.Value) []string {
	var apps []string
	for _, a := range artifacts {
		var m map[string]jsontext.Value
		if json.Unmarshal(a, &m) != nil {
			continue
		}
		var list []any
		if json.Unmarshal(m["app"], &list) != nil {
			continue
		}
		for _, e := range list {
			if s, ok := e.(string); ok && !slices.Contains(apps, s) {
				apps = append(apps, s)
			}
		}
	}
	return apps
}

// caskConflicts lists the casks and formulae a cask conflicts with
// ({"cask": ["firefox@beta"], "formula": [...]}).
func caskConflicts(v jsontext.Value) []string {
	var m map[string][]string
	if len(v) == 0 || json.Unmarshal(v, &m) != nil {
		return nil
	}
	return append(m["cask"], m["formula"]...)
}

// caskMacOS is a cask's macOS requirement ({"macos": {">=": ["12"]}}).
func caskMacOS(v jsontext.Value) string {
	var m struct {
		MacOS map[string][]string `json:"macos"`
	}
	if len(v) == 0 || json.Unmarshal(v, &m) != nil {
		return ""
	}
	for _, op := range slices.Sorted(maps.Keys(m.MacOS)) {
		if len(m.MacOS[op]) > 0 {
			return op + " " + strings.Join(m.MacOS[op], ", ")
		}
	}
	return ""
}
