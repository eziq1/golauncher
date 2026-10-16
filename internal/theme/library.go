package theme

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

//go:embed builtin/*.json
var builtinFS embed.FS

// BuiltinOrder is the order of the built-in themes in the gallery.
var BuiltinOrder = []string{
	"golauncher-light", "golauncher-dark",
	"raycast-light", "raycast-dark",
	"github-light", "github-dark",
	"nord", "dracula",
	"catppuccin-latte", "catppuccin-mocha",
	"tokyo-night",
	"solarized-light", "solarized-dark",
	"gruvbox-dark", "one-dark",
	"paper",
	"high-contrast-light", "high-contrast-dark",
}

// builtinFiles are the parsed built-in theme files, by ID.
var builtinFiles = func() map[string]*File {
	m := map[string]*File{}
	for _, id := range BuiltinOrder {
		data, err := builtinFS.ReadFile("builtin/" + id + ".json")
		if err != nil {
			panic(err)
		}
		f, _, err := ParseFile(data)
		if err != nil {
			panic(fmt.Sprintf("theme: built-in %s: %v", id, err))
		}
		m[id] = f
	}
	return m
}()

// BuiltinSource returns the file of a built-in theme, as embedded.
func BuiltinSource(id string) ([]byte, bool) {
	if _, ok := builtinFiles[id]; !ok {
		return nil, false
	}
	data, err := builtinFS.ReadFile("builtin/" + id + ".json")
	return data, err == nil
}

// builtins are the built-in themes.
var builtins = func() []*Theme {
	var out []*Theme
	for _, id := range BuiltinOrder {
		t, err := build(id, func(id string) (*File, bool) { f, ok := builtinFiles[id]; return f, ok })
		if err != nil {
			panic(fmt.Sprintf("theme: built-in %s: %v", id, err))
		}
		t.Builtin = true
		out = append(out, t)
	}
	return out
}()

// Builtin returns the built-in themes, in gallery order. They must not be
// changed.
func Builtin() []*Theme { return slices.Clone(builtins) }

// build applies the extends chain of theme id, whose files lookup finds.
func build(id string, lookup func(string) (*File, bool)) (*Theme, error) {
	var chain []*File
	seen := map[string]bool{}
	for cur := id; ; {
		if seen[cur] {
			return nil, fmt.Errorf("extends: a cycle through %q", cur)
		}
		seen[cur] = true
		f, ok := lookup(cur)
		if !ok {
			if cur == id {
				return nil, fmt.Errorf("no theme %q", id)
			}
			return nil, fmt.Errorf("extends: no theme %q", cur)
		}
		chain = append(chain, f)
		if f.Extends == "" {
			break
		}
		cur = f.Extends
	}
	t := newRoot()
	n := len(chain)
	for i := n - 1; i > 0; i-- {
		t.apply(chain[i], n-1-i) // the bases report their own warnings
	}
	t.Warnings = t.apply(chain[0], n-1)
	if missing := t.missingColors(); len(missing) > 0 {
		return nil, fmt.Errorf("missing colors %s (or extends another theme)", strings.Join(missing, ", "))
	}
	t.ID, t.Extends = id, chain[0].Extends
	return t, nil
}

// Problem is what is wrong with a theme file.
type Problem struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	// Error stops the file from being used: the theme keeps the version
	// loaded last, if any (Stale), or is missing.
	Error    string   `json:"error,omitempty"`
	Stale    bool     `json:"stale,omitzero"`
	Warnings []string `json:"warnings,omitempty"`
}

// Library holds the built-in themes and the user's theme files, which it
// reloads when they change. It is safe for concurrent use.
type Library struct {
	dir string // "" has no user themes

	mu        sync.RWMutex
	user      map[string]*Theme // the version of each file loaded last that worked
	good      map[string]*File  // their files, for themes that extend them
	problems  []Problem
	sig       string // the directory as last read
	gen       uint64
	listeners []func()
}

// NewLibrary returns the themes: the built-in ones and, when dir is not "",
// the *.json files in it. Call Reload to read them.
func NewLibrary(dir string) *Library {
	return &Library{dir: dir, user: map[string]*Theme{}, good: map[string]*File{}}
}

// Dir returns the folder of user themes, "" for none.
func (l *Library) Dir() string { return l.dir }

// Generation changes every time the themes change.
func (l *Library) Generation() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.gen
}

// OnChange registers fn to run after the themes changed.
func (l *Library) OnChange(fn func()) {
	l.mu.Lock()
	l.listeners = append(l.listeners, fn)
	l.mu.Unlock()
}

// Themes returns the built-in themes, then the user's by ID.
func (l *Library) Themes() []*Theme {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := slices.Clone(builtins)
	for _, id := range slices.Sorted(maps.Keys(l.user)) {
		out = append(out, l.user[id])
	}
	return out
}

// Get returns the theme id.
func (l *Library) Get(id string) (*Theme, bool) {
	for _, t := range builtins {
		if t.ID == id {
			return t, true
		}
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	t, ok := l.user[id]
	return t, ok
}

// Problems returns what is wrong with the user's theme files.
func (l *Library) Problems() []Problem {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return slices.Clone(l.problems)
}

// fileExt is the extension of theme files.
const fileExt = ".json"

// IDOf returns the ID of the theme file at path: its name without .json.
func IDOf(path string) string { return strings.TrimSuffix(filepath.Base(path), fileExt) }

// Reload reads the theme files again. A file that cannot be used keeps the
// version loaded last, and is reported by Problems.
func (l *Library) Reload() error {
	if l.dir == "" {
		return nil
	}
	entries, err := os.ReadDir(l.dir)
	if errors.Is(err, fs.ErrNotExist) {
		entries, err = nil, nil
	}
	if err != nil {
		return err
	}
	sig := signature(entries)
	files := map[string]*File{}
	paths := map[string]string{}
	parseErr := map[string]error{}
	warnings := map[string][]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), fileExt) {
			continue
		}
		id := IDOf(e.Name())
		path := filepath.Join(l.dir, e.Name())
		paths[id] = path
		if _, ok := builtinFiles[id]; ok {
			parseErr[id] = fmt.Errorf("%q is the ID of a built-in theme; rename the file", id)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			parseErr[id] = err
			continue
		}
		f, w, err := ParseFile(data)
		if err != nil {
			parseErr[id] = err
			continue
		}
		files[id], warnings[id] = f, w
	}

	l.mu.Lock()
	// Themes extend the files as they are now, or the last that worked.
	lookup := func(id string) (*File, bool) {
		if f, ok := builtinFiles[id]; ok {
			return f, true
		}
		if f, ok := files[id]; ok {
			return f, true
		}
		f, ok := l.good[id]
		if _, exists := paths[id]; !exists {
			return nil, false // removed
		}
		return f, ok
	}
	user := map[string]*Theme{}
	good := map[string]*File{}
	var problems []Problem
	for _, id := range slices.Sorted(maps.Keys(paths)) {
		p := Problem{ID: id, Path: paths[id], Warnings: warnings[id]}
		err := parseErr[id]
		var t *Theme
		if err == nil {
			t, err = build(id, lookup)
		}
		if err == nil {
			t.Path = paths[id]
			p.Warnings = append(p.Warnings, t.Warnings...)
			for _, c := range CheckContrast(t) {
				if !c.OK {
					p.Warnings = append(p.Warnings, fmt.Sprintf("low contrast: %s is %.2f:1, needs %.1f:1", c.Pair, c.Ratio, c.Min))
				}
			}
			t.Warnings = p.Warnings
			user[id], good[id] = t, files[id]
		} else {
			p.Error = err.Error()
			if old, ok := l.user[id]; ok {
				user[id], good[id], p.Stale = old, l.good[id], true
			}
		}
		if p.Error != "" || len(p.Warnings) > 0 {
			problems = append(problems, p)
		}
	}
	changed := sig != l.sig
	l.user, l.good, l.problems, l.sig = user, good, problems, sig
	var listeners []func()
	if changed {
		l.gen++
		listeners = slices.Clone(l.listeners)
	}
	l.mu.Unlock()
	for _, fn := range listeners {
		fn()
	}
	return nil
}

// signature describes the theme files of a directory listing: their names,
// sizes and modification times.
func signature(entries []fs.DirEntry) string {
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), fileExt) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "%s\x00%d\x00%d\n", e.Name(), info.Size(), info.ModTime().UnixNano())
	}
	return b.String()
}

// PollInterval is how often Watch looks at the theme folder.
const PollInterval = 500 * time.Millisecond

// Watch reloads the themes whenever the files in the folder change, until
// ctx ends. It looks every interval: the folder holds a handful of small
// files, so a look is a directory listing.
func (l *Library) Watch(ctx context.Context, interval time.Duration) {
	if l.dir == "" {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		entries, err := os.ReadDir(l.dir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		l.mu.RLock()
		same := signature(entries) == l.sig
		l.mu.RUnlock()
		if !same {
			l.Reload()
		}
	}
}

// Report is the result of checking a theme file (glctl theme validate).
type Report struct {
	File string `json:"file"`
	ID   string `json:"id"`
	// OK is false when the theme cannot be used; Warnings and low
	// Contrast do not stop it.
	OK       bool             `json:"ok"`
	Errors   []string         `json:"errors"`
	Warnings []string         `json:"warnings"`
	Contrast []ContrastResult `json:"contrast"`
}

// Validate checks the theme file at path, holding data, as if it were in
// the theme folder: it may extend a built-in theme or one of l's.
func (l *Library) Validate(path string, data []byte) Report {
	r := Report{File: path, ID: IDOf(path), Errors: []string{}, Warnings: []string{}, Contrast: []ContrastResult{}}
	if _, ok := builtinFiles[r.ID]; ok {
		r.Errors = append(r.Errors, fmt.Sprintf("%q is the ID of a built-in theme; rename the file", r.ID))
		return r
	}
	f, warnings, err := ParseFile(data)
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
		return r
	}
	r.Warnings = append(r.Warnings, warnings...)
	l.mu.RLock()
	t, err := build(r.ID, func(id string) (*File, bool) {
		if id == r.ID {
			return f, true
		}
		if bf, ok := builtinFiles[id]; ok {
			return bf, true
		}
		uf, ok := l.good[id]
		return uf, ok
	})
	l.mu.RUnlock()
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
		return r
	}
	r.OK = true
	r.Warnings = append(r.Warnings, t.Warnings...)
	r.Contrast = CheckContrast(t)
	return r
}
