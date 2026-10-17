package homebrew

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Package kinds.
const (
	KindFormula = "formula"
	KindCask    = "cask"
)

// Package is a formula or cask of the local Homebrew API index.
type Package struct {
	Kind string `json:"kind"`
	// Name is the formula name or the cask token ("wget", "firefox").
	Name string `json:"name"`
	// Tap is "homebrew/core" or "homebrew/cask" for the official taps.
	Tap string `json:"tap,omitempty"`
	// Names are the display names of a cask ("Mozilla Firefox").
	Names       []string `json:"names,omitempty"`
	Desc        string   `json:"desc,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	License     string   `json:"license,omitempty"`
	Version     string   `json:"version,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
	Executables []string `json:"executables,omitempty"`
	AutoUpdates bool     `json:"autoUpdates,omitzero"`
	Deprecated  bool     `json:"deprecated,omitzero"`
	Disabled    bool     `json:"disabled,omitzero"`
}

// Title is how the package is shown: a cask's first display name, else
// its name.
func (p *Package) Title() string {
	if p.Kind == KindCask && len(p.Names) > 0 && p.Names[0] != "" {
		return p.Names[0]
	}
	return p.Name
}

// key identifies a package in maps: "formula/wget", "cask/firefox".
func key(kind, name string) string { return kind + "/" + strings.ToLower(name) }

// Index formats (Index.Format).
const (
	// FormatInternal is the single packages.<tag>.jws.json of newer
	// Homebrew versions (api/internal).
	FormatInternal = "internal"
	// FormatPublic is the formula.jws.json and cask.jws.json of the public
	// JSON API.
	FormatPublic = "public"
	// FormatNames is the plain lists of names (formula_names.txt,
	// cask_names.txt): names only, nothing else is known.
	FormatNames = "names"
)

// errNoIndex reports that Homebrew's cache has no API index (Homebrew was
// never updated, or it is set to install from its taps).
var errNoIndex = errors.New("no Homebrew API index")

// Index is the searchable list of formulae and casks.
type Index struct {
	Format   string
	Stamp    string
	Packages []*Package
	byKey    map[string]*Package
	entries  []entry
	formulae int
	casks    int
}

// entry is a package prepared for searching (lower-case fields).
type entry struct {
	pkg   *Package
	name  string
	alias []string // aliases, cask display names
	bins  []string
	words []string
	desc  string
}

// newIndex builds an Index of pkgs (sorted by kind and name).
func newIndex(format string, pkgs []*Package) *Index {
	slices.SortFunc(pkgs, func(a, b *Package) int {
		return cmp.Or(strings.Compare(a.Kind, b.Kind), strings.Compare(a.Name, b.Name))
	})
	ix := &Index{Format: format, Packages: pkgs, byKey: make(map[string]*Package, len(pkgs)), entries: make([]entry, len(pkgs))}
	for i, p := range pkgs {
		ix.byKey[key(p.Kind, p.Name)] = p
		if p.Kind == KindCask {
			ix.casks++
		} else {
			ix.formulae++
		}
		e := entry{pkg: p, name: strings.ToLower(p.Name), desc: strings.ToLower(p.Desc)}
		for _, a := range p.Aliases {
			e.alias = append(e.alias, strings.ToLower(a))
		}
		for _, n := range p.Names {
			n = strings.ToLower(n)
			e.alias = append(e.alias, n)
			e.words = append(e.words, strings.Fields(n)...)
		}
		for _, b := range p.Executables {
			e.bins = append(e.bins, strings.ToLower(b))
		}
		e.words = append(e.words, strings.FieldsFunc(e.desc, func(r rune) bool {
			return r == ' ' || r == ',' || r == '(' || r == ')' || r == '/' || r == '-'
		})...)
		ix.entries[i] = e
	}
	return ix
}

// Package returns the package of the kind called name.
func (ix *Index) Package(kind, name string) (*Package, bool) {
	if ix == nil {
		return nil, false
	}
	p, ok := ix.byKey[key(kind, name)]
	return p, ok
}

// Counts returns the number of formulae and casks.
func (ix *Index) Counts() (formulae, casks int) {
	if ix == nil {
		return 0, 0
	}
	return ix.formulae, ix.casks
}

// ---- reading the index ----

// indexFiles are the files an index is read from.
type indexFiles struct {
	format string
	paths  []string // missing files are ""
	stamp  string
}

// fileStamp is the modification time and size of path, or "" when it does
// not exist.
func fileStamp(path string) (string, time.Time) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return "", time.Time{}
	}
	return strconv.FormatInt(st.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(st.Size(), 10), st.ModTime()
}

// findIndex picks the newest index in Homebrew's cache directory: the
// internal packages file of the newest bottle tag, or the public API files,
// or the lists of names.
func findIndex(cache string) (indexFiles, error) {
	api := filepath.Join(cache, "api")
	var internal string
	var internalAt time.Time
	matches, _ := filepath.Glob(filepath.Join(api, "internal", "packages.*.jws.json"))
	for _, m := range matches {
		if s, at := fileStamp(m); s != "" && (internal == "" || at.After(internalAt)) {
			internal, internalAt = m, at
		}
	}
	formula, cask := filepath.Join(api, "formula.jws.json"), filepath.Join(api, "cask.jws.json")
	fs1, fAt := fileStamp(formula)
	cs1, cAt := fileStamp(cask)
	publicAt := fAt
	if cAt.After(publicAt) {
		publicAt = cAt
	}
	switch {
	case internal != "" && (fs1 == "" && cs1 == "" || !internalAt.Before(publicAt)):
		s, _ := fileStamp(internal)
		return indexFiles{format: FormatInternal, paths: []string{internal}, stamp: FormatInternal + ":" + internal + ":" + s}, nil
	case fs1 != "" || cs1 != "":
		if fs1 == "" {
			formula = ""
		}
		if cs1 == "" {
			cask = ""
		}
		return indexFiles{format: FormatPublic, paths: []string{formula, cask}, stamp: FormatPublic + ":" + fs1 + ":" + cs1}, nil
	}
	fn, cn := filepath.Join(api, "formula_names.txt"), filepath.Join(api, "cask_names.txt")
	fs2, _ := fileStamp(fn)
	cs2, _ := fileStamp(cn)
	if fs2 == "" && cs2 == "" {
		return indexFiles{}, errNoIndex
	}
	if fs2 == "" {
		fn = ""
	}
	if cs2 == "" {
		cn = ""
	}
	return indexFiles{format: FormatNames, paths: []string{fn, cn}, stamp: FormatNames + ":" + fs2 + ":" + cs2}, nil
}

// readIndex reads the files of f. now decides whether a deprecation with a
// date has taken effect.
func readIndex(f indexFiles, now time.Time) (*Index, error) {
	read := func(path string) ([]byte, error) {
		if path == "" {
			return nil, nil
		}
		return os.ReadFile(path)
	}
	var pkgs []*Package
	switch f.format {
	case FormatInternal:
		b, err := read(f.paths[0])
		if err != nil {
			return nil, err
		}
		if pkgs, err = ParseInternal(b, now); err != nil {
			return nil, fmt.Errorf("%s: %w", f.paths[0], err)
		}
	case FormatPublic:
		fb, err := read(f.paths[0])
		if err != nil {
			return nil, err
		}
		cb, err := read(f.paths[1])
		if err != nil {
			return nil, err
		}
		if pkgs, err = ParsePublic(fb, cb); err != nil {
			return nil, err
		}
	case FormatNames:
		fb, err := read(f.paths[0])
		if err != nil {
			return nil, err
		}
		cb, err := read(f.paths[1])
		if err != nil {
			return nil, err
		}
		pkgs = ParseNames(fb, cb)
	default:
		return nil, errNoIndex
	}
	ix := newIndex(f.format, pkgs)
	ix.Stamp = f.stamp
	return ix, nil
}

// jwsPayload returns the payload of a JSON Web Signature file as Homebrew
// stores its API files: {"payload": "<JSON text>", "signature": ...}. The
// signature is not checked (brew did when it downloaded the file).
func jwsPayload(b []byte) ([]byte, error) {
	var jws struct {
		Payload *string `json:"payload"`
	}
	if err := json.Unmarshal(b, &jws); err != nil {
		return nil, err
	}
	if jws.Payload == nil {
		return nil, errors.New("no payload")
	}
	return []byte(*jws.Payload), nil
}

// ParseInternal parses an api/internal/packages.<tag>.jws.json file.
func ParseInternal(b []byte, now time.Time) ([]*Package, error) {
	payload, err := jwsPayload(b)
	if err != nil {
		return nil, err
	}
	type formula struct {
		Desc          string         `json:"desc"`
		Homepage      string         `json:"homepage"`
		License       jsontext.Value `json:"license"`
		StableVersion string         `json:"stable_version"`
		Revision      int            `json:"revision"`
		Executables   []string       `json:"executables"`
		Aliases       []string       `json:"aliases"`
		DeprecateArgs jsontext.Value `json:"deprecate_args"`
		DisableArgs   jsontext.Value `json:"disable_args"`
	}
	type cask struct {
		Desc          string         `json:"desc"`
		Homepage      string         `json:"homepage"`
		Version       string         `json:"version"`
		Names         []string       `json:"names"`
		TapString     string         `json:"tap_string"`
		AutoUpdates   bool           `json:"auto_updates"`
		DeprecateArgs jsontext.Value `json:"deprecate_args"`
		DisableArgs   jsontext.Value `json:"disable_args"`
	}
	var data struct {
		Formulae       map[string]formula `json:"formulae"`
		Casks          map[string]cask    `json:"casks"`
		FormulaAliases map[string]string  `json:"formula_aliases"`
	}
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, err
	}
	if data.Formulae == nil && data.Casks == nil {
		return nil, errors.New("no formulae and no casks")
	}
	aliases := map[string][]string{}
	for a, name := range data.FormulaAliases {
		aliases[name] = append(aliases[name], a)
	}
	pkgs := make([]*Package, 0, len(data.Formulae)+len(data.Casks))
	for name, f := range data.Formulae {
		al := f.Aliases
		for _, a := range aliases[name] {
			if !slices.Contains(al, a) {
				al = append(al, a)
			}
		}
		slices.Sort(al)
		pkgs = append(pkgs, &Package{
			Kind: KindFormula, Name: name, Tap: "homebrew/core",
			Desc: f.Desc, Homepage: f.Homepage, License: licenseText(f.License),
			Version: pkgVersion(f.StableVersion, f.Revision), Aliases: al, Executables: f.Executables,
			Deprecated: argsInEffect(f.DeprecateArgs, now), Disabled: argsInEffect(f.DisableArgs, now),
		})
	}
	for token, c := range data.Casks {
		pkgs = append(pkgs, &Package{
			Kind: KindCask, Name: token, Tap: cmp.Or(c.TapString, "homebrew/cask"), Names: c.Names,
			Desc: c.Desc, Homepage: c.Homepage, Version: c.Version, AutoUpdates: c.AutoUpdates,
			Deprecated: argsInEffect(c.DeprecateArgs, now), Disabled: argsInEffect(c.DisableArgs, now),
		})
	}
	return pkgs, nil
}

// ParsePublic parses the formula.jws.json and cask.jws.json of the public
// API (either may be nil).
func ParsePublic(formulae, casks []byte) ([]*Package, error) {
	type formula struct {
		Name     string         `json:"name"`
		Tap      string         `json:"tap"`
		Aliases  []string       `json:"aliases"`
		Desc     string         `json:"desc"`
		License  jsontext.Value `json:"license"`
		Homepage string         `json:"homepage"`
		Versions struct {
			Stable string `json:"stable"`
		} `json:"versions"`
		Revision   int  `json:"revision"`
		Deprecated bool `json:"deprecated"`
		Disabled   bool `json:"disabled"`
	}
	type cask struct {
		Token       string   `json:"token"`
		Tap         string   `json:"tap"`
		Name        []string `json:"name"`
		Desc        string   `json:"desc"`
		Homepage    string   `json:"homepage"`
		Version     string   `json:"version"`
		AutoUpdates bool     `json:"auto_updates"`
		Deprecated  bool     `json:"deprecated"`
		Disabled    bool     `json:"disabled"`
	}
	var pkgs []*Package
	if formulae != nil {
		payload, err := jwsPayload(formulae)
		if err != nil {
			return nil, fmt.Errorf("formula.jws.json: %w", err)
		}
		var fs []formula
		if err := json.Unmarshal(payload, &fs); err != nil {
			return nil, fmt.Errorf("formula.jws.json: %w", err)
		}
		for _, f := range fs {
			if f.Name == "" {
				continue
			}
			pkgs = append(pkgs, &Package{
				Kind: KindFormula, Name: f.Name, Tap: cmp.Or(f.Tap, "homebrew/core"), Desc: f.Desc, Homepage: f.Homepage,
				License: licenseText(f.License), Version: pkgVersion(f.Versions.Stable, f.Revision), Aliases: f.Aliases,
				Deprecated: f.Deprecated, Disabled: f.Disabled,
			})
		}
	}
	if casks != nil {
		payload, err := jwsPayload(casks)
		if err != nil {
			return nil, fmt.Errorf("cask.jws.json: %w", err)
		}
		var cs []cask
		if err := json.Unmarshal(payload, &cs); err != nil {
			return nil, fmt.Errorf("cask.jws.json: %w", err)
		}
		for _, c := range cs {
			if c.Token == "" {
				continue
			}
			pkgs = append(pkgs, &Package{
				Kind: KindCask, Name: c.Token, Tap: cmp.Or(c.Tap, "homebrew/cask"), Names: c.Name, Desc: c.Desc,
				Homepage: c.Homepage, Version: c.Version, AutoUpdates: c.AutoUpdates, Deprecated: c.Deprecated, Disabled: c.Disabled,
			})
		}
	}
	return pkgs, nil
}

// ParseNames parses formula_names.txt and cask_names.txt (one name per
// line; either may be nil).
func ParseNames(formulae, casks []byte) []*Package {
	var pkgs []*Package
	add := func(kind string, b []byte) {
		for line := range strings.Lines(string(b)) {
			if name := strings.TrimSpace(line); name != "" {
				pkgs = append(pkgs, &Package{Kind: kind, Name: name})
			}
		}
	}
	add(KindFormula, formulae)
	add(KindCask, casks)
	return pkgs
}

// pkgVersion is Homebrew's version with the revision: "1.25.0_2".
func pkgVersion(version string, revision int) string {
	if version == "" || revision <= 0 {
		return version
	}
	return version + "_" + strconv.Itoa(revision)
}

// licenseText renders a formula license: a SPDX string, or {"all_of":
// [...]}/{"any_of": [...]} with nested values; null is "".
func licenseText(v jsontext.Value) string {
	if len(v) == 0 {
		return ""
	}
	var x any
	if err := json.Unmarshal(v, &x); err != nil {
		return ""
	}
	var render func(any) string
	render = func(x any) string {
		switch x := x.(type) {
		case string:
			return strings.TrimPrefix(x, ":")
		case []any:
			var parts []string
			for _, e := range x {
				if s := render(e); s != "" {
					parts = append(parts, s)
				}
			}
			return strings.Join(parts, ", ")
		case map[string]any:
			var parts []string
			for _, k := range slices.Sorted(maps.Keys(x)) {
				name := strings.TrimPrefix(k, ":")
				arr, isList := x[k].([]any)
				switch {
				case (name == "all_of" || name == "any_of") && isList:
					sep := " and "
					if name == "any_of" {
						sep = " or "
					}
					var sub []string
					for _, e := range arr {
						if s := render(e); s != "" {
							sub = append(sub, s)
						}
					}
					parts = append(parts, strings.Join(sub, sep))
				default:
					// {"GPL-2.0-only": {"with": "Linux-syscall-note"}}
					parts = append(parts, name)
				}
			}
			return strings.Join(parts, ", ")
		}
		return ""
	}
	return render(x)
}

// argsInEffect reports whether deprecate_args/disable_args of the internal
// index apply at now: present and not null, without a date or with a date
// that has passed.
func argsInEffect(v jsontext.Value, now time.Time) bool {
	if len(v) == 0 || string(v) == "null" {
		return false
	}
	var args map[string]any
	if err := json.Unmarshal(v, &args); err != nil {
		return true
	}
	d, ok := args[":date"].(string)
	if !ok {
		d, ok = args["date"].(string)
	}
	if !ok {
		return true
	}
	t, err := time.Parse(time.DateOnly, d)
	return err != nil || !now.Before(t)
}

// ---- searching ----

// termScore scores how well one lower-case search term matches e; 0 means
// no match. Name matches beat alias and command matches, which beat
// description matches.
func termScore(e *entry, t string) int {
	if e.name == t {
		return 1000
	}
	best := 0
	if strings.HasPrefix(e.name, t) {
		best = 800 - min(len(e.name)-len(t), 100)
	}
	for _, a := range e.alias {
		switch {
		case a == t:
			best = max(best, 900)
		case strings.HasPrefix(a, t):
			best = max(best, 650)
		case strings.Contains(a, t):
			best = max(best, 450)
		}
	}
	for _, b := range e.bins {
		switch {
		case b == t:
			best = max(best, 750)
		case strings.HasPrefix(b, t):
			best = max(best, 550)
		}
	}
	if i := strings.Index(e.name, t); i >= 0 {
		best = max(best, 600-min(i, 50)-min(len(e.name)-len(t), 100))
	}
	if best > 0 {
		return best
	}
	for _, w := range e.words {
		if strings.HasPrefix(w, t) {
			return 300
		}
	}
	if strings.Contains(e.desc, t) {
		return 200
	}
	if len(t) >= 2 && subsequence(e.name, t) {
		return 100
	}
	return 0
}

// phraseScore is the bonus of a query of several words that is a cask's
// display name or an alias as a whole ("vs code", "google chrome").
func phraseScore(e *entry, phrase string) int {
	best := 0
	for _, a := range e.alias {
		switch {
		case a == phrase:
			return 2000
		case strings.HasPrefix(a, phrase):
			best = 1000
		}
	}
	return best
}

// subsequence reports whether the letters of t appear in s in order.
func subsequence(s, t string) bool {
	for _, r := range t {
		i := strings.IndexRune(s, r)
		if i < 0 {
			return false
		}
		s = s[i+len(string(r)):]
	}
	return true
}

// Search returns up to limit packages matching every word of query, best
// first. Disabled packages and ties go last, then shorter names.
func (ix *Index) Search(ctx context.Context, query string, limit int) []*Package {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 || ix == nil {
		return nil
	}
	phrase := strings.Join(terms, " ")
	type hit struct {
		e     *entry
		score int
	}
	var hits []hit
	for i := range ix.entries {
		if i%1024 == 0 && ctx.Err() != nil {
			return nil
		}
		e := &ix.entries[i]
		total := 0
		for k, t := range terms {
			s := termScore(e, t)
			if s == 0 {
				total = 0
				break
			}
			if k == 0 {
				s *= 2 // the first word is usually the name
			}
			total += s
		}
		if total > 0 {
			if len(terms) > 1 {
				total += phraseScore(e, phrase)
			}
			if e.pkg.Disabled {
				total /= 2
			}
			hits = append(hits, hit{e, total})
		}
	}
	slices.SortFunc(hits, func(a, b hit) int {
		return cmp.Or(
			cmp.Compare(b.score, a.score),
			cmp.Compare(len(a.e.name), len(b.e.name)),
			strings.Compare(a.e.name, b.e.name),
			strings.Compare(a.e.pkg.Kind, b.e.pkg.Kind),
		)
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]*Package, len(hits))
	for i, h := range hits {
		out[i] = h.e.pkg
	}
	return out
}

// isNotExist reports a missing file or index.
func isNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) || errors.Is(err, errNoIndex) }
