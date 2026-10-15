package terminal

import (
	"bytes"
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Profile is a Windows Terminal profile as shown in the root search.
type Profile struct {
	GUID        string // lower case with braces, "" when the profile has none
	Name        string
	Commandline string // "" for most dynamic profiles (WSL, Azure, …)
	Icon        string // an image file that exists, or ""
	Source      string // the dynamic profile generator or fragment, e.g. "Windows.Terminal.Wsl", "Git"
	Default     bool   // the defaultProfile
}

// Terminal is what was read from Windows Terminal's settings and fragments.
type Terminal struct {
	Settings string // the settings.json read, "" when none was found
	Profiles []Profile
	// Problems are fragments that could not be read; their profiles are
	// left out.
	Problems []error
}

// settingsLocations returns where Windows Terminal keeps settings.json, in
// the order they are tried: the Store release, the Preview, the unpackaged
// build (scoop, winget portable, …) (docs/terminal-wsl.md §1).
func settingsLocations(localAppData string) []string {
	if localAppData == "" {
		return nil
	}
	return []string{
		filepath.Join(localAppData, "Packages", "Microsoft.WindowsTerminal_8wekyb3d8bbwe", "LocalState", "settings.json"),
		filepath.Join(localAppData, "Packages", "Microsoft.WindowsTerminalPreview_8wekyb3d8bbwe", "LocalState", "settings.json"),
		filepath.Join(localAppData, "Microsoft", "Windows Terminal", "settings.json"),
	}
}

// fragmentRoots returns the folders holding JSON fragment extensions: one
// folder per app with *.json files in it.
func fragmentRoots(localAppData, programData string) []string {
	var out []string
	for _, base := range []string{localAppData, programData} {
		if base != "" {
			out = append(out, filepath.Join(base, "Microsoft", "Windows Terminal", "Fragments"))
		}
	}
	return out
}

// findSettings returns the first settings.json that exists, or "".
func findSettings(localAppData string) string {
	for _, p := range settingsLocations(localAppData) {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// fragmentFiles lists the fragment files under roots, sorted per root.
func fragmentFiles(roots []string) []string {
	var out []string
	for _, root := range roots {
		matches, _ := filepath.Glob(filepath.Join(root, "*", "*.json"))
		slices.Sort(matches)
		out = append(out, matches...)
	}
	return out
}

// signature identifies the state of the files the profiles are read from,
// so that a change (an edit, a new fragment) is noticed cheaply.
func signature(settings string, fragments []string) string {
	var b strings.Builder
	for _, p := range append([]string{settings}, fragments...) {
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "%s|%d|%d\n", p, st.ModTime().UnixNano(), st.Size())
		}
	}
	return b.String()
}

// profileJSON is a profile object of settings.json or of a fragment. Pointer
// fields tell an absent field from an empty one, which matters when fields
// are layered (profiles.defaults, fragments).
type profileJSON struct {
	GUID        *string `json:"guid"`
	Name        *string `json:"name"`
	Commandline *string `json:"commandline"`
	Icon        *string `json:"icon"`
	Hidden      *bool   `json:"hidden"`
	Source      *string `json:"source"`
	Updates     *string `json:"updates"` // fragments: changes the profile with this GUID
}

// fill sets the fields absent in p from q.
func (p *profileJSON) fill(q profileJSON) {
	p.GUID = cmp.Or(p.GUID, q.GUID)
	p.Name = cmp.Or(p.Name, q.Name)
	p.Commandline = cmp.Or(p.Commandline, q.Commandline)
	p.Icon = cmp.Or(p.Icon, q.Icon)
	p.Hidden = cmp.Or(p.Hidden, q.Hidden)
	p.Source = cmp.Or(p.Source, q.Source)
}

// builtinCommandlines are the command lines of the profiles Windows Terminal
// creates itself, which settings.json lists without one.
var builtinCommandlines = map[string]string{
	"{61c54bbd-c2c6-5271-96e7-009a87ff44bf}": `%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe`, // Windows PowerShell
	"{0caa0dad-35be-5f56-a8ff-afceeeaa6101}": `%SystemRoot%\System32\cmd.exe`,                               // Command Prompt
}

// layered is a profile with the folder its icon path is relative to.
type layered struct {
	profileJSON
	iconBase string
}

// settingsJSON is the part of settings.json the plugin reads.
type settingsJSON struct {
	DefaultProfile string `json:"defaultProfile"`
	// Profiles is {"defaults": {…}, "list": […]} or, in old files, the list.
	Profiles jsontext.Value `json:"profiles"`
}

type profilesObject struct {
	Defaults profileJSON   `json:"defaults"`
	List     []profileJSON `json:"list"`
}

// fragmentJSON is a fragment file.
type fragmentJSON struct {
	Profiles []profileJSON `json:"profiles"`
}

// lenient are the options for Windows Terminal's hand-edited JSON: a
// duplicated name keeps the last value, as Windows Terminal does.
var lenient = json.JoinOptions(jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))

// unmarshalJSONC decodes JSON with comments and trailing commas into v.
func unmarshalJSONC(data []byte, v any) error {
	return json.Unmarshal(StandardJSON(data), v, lenient)
}

// StandardJSON turns JSONC (JSON with // and /* */ comments and trailing
// commas, as Windows Terminal and VS Code write it) into standard JSON. A
// UTF-8 byte order mark is removed. Strings are left untouched; comments are
// replaced by spaces, so error offsets stay the same.
func StandardJSON(data []byte) []byte {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	out := slices.Clone(data)
	// Pass 1: blank out comments.
	inString := false
	for i := 0; i < len(out); i++ {
		c := out[i]
		switch {
		case inString:
			switch c {
			case '\\':
				i++
			case '"':
				inString = false
			}
		case c == '"':
			inString = true
		case c == '/' && i+1 < len(out) && out[i+1] == '/':
			for i < len(out) && out[i] != '\n' {
				out[i] = ' '
				i++
			}
		case c == '/' && i+1 < len(out) && out[i+1] == '*':
			end := bytes.Index(out[i+2:], []byte("*/"))
			stop := len(out)
			if end >= 0 {
				stop = i + 2 + end + 2
			}
			for ; i < stop; i++ {
				if out[i] != '\n' && out[i] != '\r' {
					out[i] = ' '
				}
			}
			i--
		}
	}
	// Pass 2: blank out commas followed only by space before } or ].
	inString = false
	for i := 0; i < len(out); i++ {
		c := out[i]
		switch {
		case inString:
			switch c {
			case '\\':
				i++
			case '"':
				inString = false
			}
		case c == '"':
			inString = true
		case c == ',':
			j := i + 1
			for j < len(out) && isSpace(out[j]) {
				j++
			}
			if j < len(out) && (out[j] == '}' || out[j] == ']') {
				out[i] = ' '
			}
		}
	}
	return out
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// loadTerminal reads the profiles of settings (may be "") and of the
// fragments: hidden profiles are left out, fields missing in a profile come
// from profiles.defaults, then from the fragment the profile came from;
// getenv expands %VAR% in icon paths. The
// error reports a settings.json that cannot be read or parsed (e.g. while it
// is being saved); broken fragments are only listed in Problems.
func loadTerminal(settings string, fragments []string, getenv func(string) string) (Terminal, error) {
	t := Terminal{Settings: settings}
	var (
		list     []layered
		defaults profileJSON
		defGUID  string
	)
	if settings != "" {
		data, err := os.ReadFile(settings)
		if err == nil {
			var s settingsJSON
			if err = unmarshalJSONC(data, &s); err == nil {
				defGUID = normGUID(s.DefaultProfile)
				var raw []profileJSON
				raw, defaults, err = decodeProfiles(s.Profiles)
				for _, p := range raw {
					list = append(list, layered{profileJSON: p, iconBase: filepath.Dir(settings)})
				}
			}
		}
		if err != nil {
			return t, fmt.Errorf("%s: %w", settings, err)
		}
	}

	// Fragments: new profiles, and "updates" of existing ones.
	type fragProfile struct {
		layered
		used bool
	}
	var frags []*fragProfile
	updates := map[string][]layered{}
	for _, f := range fragments {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Problems = append(t.Problems, err)
			continue
		}
		var fj fragmentJSON
		if err := unmarshalJSONC(data, &fj); err != nil {
			t.Problems = append(t.Problems, fmt.Errorf("%s: %w", f, err))
			continue
		}
		app := filepath.Base(filepath.Dir(f))
		for _, p := range fj.Profiles {
			l := layered{profileJSON: p, iconBase: filepath.Dir(f)}
			if p.Updates != nil {
				g := normGUID(*p.Updates)
				updates[g] = append(updates[g], l)
				continue
			}
			if p.Source == nil {
				l.Source = &app
			}
			frags = append(frags, &fragProfile{layered: l})
		}
	}

	// A profile Windows Terminal already copied from a fragment into
	// settings.json (a stub with name and source) takes the fragment's
	// command line and icon.
	for i := range list {
		p := &list[i]
		for _, u := range updates[normGUID(deref(p.GUID))] {
			fillLayered(p, u)
		}
		if p.Source == nil {
			continue
		}
		for _, fp := range frags {
			if !fp.used && strings.EqualFold(deref(fp.Source), *p.Source) && deref(fp.Name) == deref(p.Name) {
				fp.used = true
				fillLayered(p, fp.layered)
			}
		}
	}
	// Fragments Windows Terminal has not seen yet.
	for _, fp := range frags {
		if !fp.used {
			list = append(list, fp.layered)
		}
	}

	for i := range list {
		p := &list[i]
		fillLayered(p, layered{profileJSON: defaults, iconBase: p.iconBase})
		if deref(p.Hidden) || strings.TrimSpace(deref(p.Name)) == "" {
			continue
		}
		g := normGUID(deref(p.GUID))
		t.Profiles = append(t.Profiles, Profile{
			GUID:        g,
			Name:        *p.Name,
			Commandline: cmp.Or(strings.TrimSpace(deref(p.Commandline)), builtinCommandlines[g]),
			Icon:        resolveIcon(deref(p.Icon), p.iconBase, getenv),
			Source:      deref(p.Source),
			Default:     g != "" && g == defGUID,
		})
	}
	return t, nil
}

// fillLayered fills absent fields of p from q, taking q's icon folder when
// the icon comes from q.
func fillLayered(p *layered, q layered) {
	if p.Icon == nil && q.Icon != nil {
		p.iconBase = q.iconBase
	}
	p.fill(q.profileJSON)
}

// decodeProfiles reads "profiles" in either of its forms.
func decodeProfiles(v jsontext.Value) ([]profileJSON, profileJSON, error) {
	v = bytes.TrimSpace(v)
	switch {
	case len(v) == 0 || string(v) == "null":
		return nil, profileJSON{}, nil
	case v[0] == '[':
		var list []profileJSON
		err := json.Unmarshal(v, &list, lenient)
		return list, profileJSON{}, err
	default:
		var o profilesObject
		err := json.Unmarshal(v, &o, lenient)
		return o.List, o.Defaults, err
	}
}

func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// normGUID returns g in lower case with braces, "" when g is empty.
func normGUID(g string) string {
	g = strings.ToLower(strings.TrimSpace(g))
	if g == "" {
		return ""
	}
	if !strings.HasPrefix(g, "{") {
		g = "{" + g + "}"
	}
	return g
}

// iconExts are the image types an icon file may have.
var iconExts = []string{".png", ".jpg", ".jpeg", ".gif", ".bmp", ".ico", ".svg"}

// resolveIcon returns the image file icon names, or "" when it is not one
// that exists: emoji, ms-appx:// resources, URLs and missing files are left
// to the built-in icon. %VAR% is expanded and relative paths are resolved
// against base.
func resolveIcon(icon, base string, getenv func(string) string) string {
	icon = strings.TrimSpace(icon)
	if icon == "" || strings.Contains(icon, "://") {
		return ""
	}
	icon = expandEnv(icon, getenv)
	if !slices.Contains(iconExts, strings.ToLower(filepath.Ext(icon))) {
		return ""
	}
	if !filepath.IsAbs(icon) {
		if base == "" {
			return ""
		}
		icon = filepath.Join(base, icon)
	}
	if st, err := os.Stat(icon); err != nil || !st.Mode().IsRegular() {
		return ""
	}
	return icon
}

// expandEnv replaces %NAME% with the value of the environment variable;
// unknown variables are kept.
func expandEnv(s string, getenv func(string) string) string {
	if getenv == nil || !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for {
		before, rest, ok := strings.Cut(s, "%")
		if !ok {
			break
		}
		name, after, ok := strings.Cut(rest, "%")
		if !ok {
			break
		}
		b.WriteString(before)
		if v := getenv(name); name != "" && v != "" {
			b.WriteString(v)
		} else {
			b.WriteString("%" + name + "%")
		}
		s = after
	}
	b.WriteString(s)
	return b.String()
}
