package scoop

import (
	"bufio"
	"cmp"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// Bucket is an added bucket and its manifests.
type Bucket struct {
	Name string `json:"name"`
	// Dir is the bucket's directory (buckets\<name>).
	Dir string `json:"dir"`
	// Stamp changes when the bucket is updated; it keys the cache.
	Stamp string `json:"stamp"`
	// Updated is when the bucket was last fetched (or changed).
	Updated  time.Time `json:"updated,omitzero"`
	Source   string    `json:"source,omitempty"` // git remote URL
	Packages []Package `json:"packages"`
}

// App is an installed app.
type App struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Bucket  string `json:"bucket,omitempty"`
	Arch    string `json:"arch,omitempty"`
	URL     string `json:"url,omitempty"` // installed from a manifest URL
	Held    bool   `json:"held,omitzero"`
	// Global apps are installed for all users; the plugin does not change them.
	Global bool `json:"global,omitzero"`
	// Dir is the app's current directory (apps\<name>\current).
	Dir string `json:"dir"`
	// Info comes from the manifest the app was installed with.
	Info Package `json:"info"`
}

// Index is everything the plugin read from the Scoop directories. It is not
// changed after it is built.
type Index struct {
	Root    string
	Global  string
	Buckets []*Bucket
	Apps    []App

	entries []entry             // search entries, in bucket rank order
	byName  map[string][]*entry // lower-case name → entries, in bucket rank order
	apps    map[string]*App     // lower-case name → app (the user's before global)
}

// entry is a package prepared for searching.
type entry struct {
	pkg   *Package
	rank  int // of the bucket
	name  string
	desc  string
	bins  []string
	words []string // of name and description, for word-prefix matches
}

// knownOrder ranks well-known buckets first (main before extras before
// versions…); others follow by name.
var knownOrder = []string{"main", "extras", "versions", "java", "nerd-fonts", "games", "nirsoft", "sysinternals", "php", "nonportable"}

func bucketRank(name string) int {
	if i := slices.Index(knownOrder, strings.ToLower(name)); i >= 0 {
		return i
	}
	return len(knownOrder)
}

// newIndex prepares an index of buckets and apps for searching.
func newIndex(root, global string, buckets []*Bucket, apps []App) *Index {
	slices.SortFunc(buckets, func(a, b *Bucket) int {
		return cmp.Or(cmp.Compare(bucketRank(a.Name), bucketRank(b.Name)), strings.Compare(a.Name, b.Name))
	})
	ix := &Index{Root: root, Global: global, Buckets: buckets, Apps: apps,
		byName: map[string][]*entry{}, apps: map[string]*App{}}
	n := 0
	for _, b := range buckets {
		n += len(b.Packages)
	}
	ix.entries = make([]entry, 0, n)
	for _, b := range buckets {
		rank := bucketRank(b.Name)
		for i := range b.Packages {
			p := &b.Packages[i]
			e := entry{pkg: p, rank: rank, name: strings.ToLower(p.Name), desc: strings.ToLower(p.Description)}
			for _, bin := range p.Bin {
				e.bins = append(e.bins, strings.ToLower(bin))
			}
			e.words = strings.FieldsFunc(e.name+" "+e.desc, isSep)
			ix.entries = append(ix.entries, e)
		}
	}
	for i := range ix.entries {
		e := &ix.entries[i]
		ix.byName[e.name] = append(ix.byName[e.name], e)
	}
	for i := range ix.Apps {
		a := &ix.Apps[i]
		key := strings.ToLower(a.Name)
		if old, ok := ix.apps[key]; !ok || (old.Global && !a.Global) {
			ix.apps[key] = a
		}
	}
	return ix
}

// isSep separates words of names and descriptions.
func isSep(r rune) bool {
	switch r {
	case ' ', '-', '_', '.', ',', '(', ')', '/', '\t', '\n', '"', '\'', ':', ';', '!', '?', '[', ']':
		return true
	}
	return false
}

// PackageCount is the number of manifests in all buckets.
func (ix *Index) PackageCount() int { return len(ix.entries) }

// App returns the installed app called name.
func (ix *Index) App(name string) (*App, bool) {
	a, ok := ix.apps[strings.ToLower(name)]
	return a, ok
}

// Lookup returns the package name of bucket, or of the best-ranked bucket
// when bucket is "".
func (ix *Index) Lookup(bucket, name string) (*Package, bool) {
	for _, e := range ix.byName[strings.ToLower(name)] {
		if bucket == "" || strings.EqualFold(e.pkg.Bucket, bucket) {
			return e.pkg, true
		}
	}
	return nil, false
}

// Latest returns the bucket manifest an installed app updates from: the one
// of its bucket. Apps installed from a URL or a removed bucket have none.
func (ix *Index) Latest(a *App) (*Package, bool) {
	if a.Bucket == "" {
		return nil, false
	}
	return ix.Lookup(a.Bucket, a.Name)
}

// Outdated reports whether a has a newer version in its bucket and returns
// that version.
func (ix *Index) Outdated(a *App) (string, bool) {
	p, ok := ix.Latest(a)
	if !ok || !outdated(a.Version, p.Version) {
		return "", false
	}
	return p.Version, true
}

// ---- reading the Scoop directories ----

// bucketInfo returns the manifest directory and the cache stamp of the
// bucket in dir.
func bucketInfo(dir string) (manifests, stamp string, updated time.Time) {
	manifests = dir
	if st, err := os.Stat(filepath.Join(dir, "bucket")); err == nil && st.IsDir() {
		manifests = filepath.Join(dir, "bucket")
	}
	var fetched, changed time.Time
	if st, err := os.Stat(filepath.Join(dir, ".git", "FETCH_HEAD")); err == nil {
		fetched = st.ModTime()
	}
	if st, err := os.Stat(manifests); err == nil {
		changed = st.ModTime()
	}
	updated = fetched
	if changed.After(updated) {
		updated = changed
	}
	return manifests, fmt.Sprintf("%d/%d", fetched.UnixNano(), changed.UnixNano()), updated
}

// gitRemote returns the URL of the "origin" remote in the repository dir.
func gitRemote(dir string) string {
	f, err := os.Open(filepath.Join(dir, ".git", "config"))
	if err != nil {
		return ""
	}
	defer f.Close()
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			in = line == `[remote "origin"]`
			continue
		}
		if k, v, ok := strings.Cut(line, "="); in && ok && strings.TrimSpace(k) == "url" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// bucketDirs returns the bucket directories under root, by name.
func bucketDirs(root string) map[string]string {
	out := map[string]string{}
	entries, _ := os.ReadDir(filepath.Join(root, "buckets"))
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out[e.Name()] = filepath.Join(root, "buckets", e.Name())
		}
	}
	return out
}

// readBucket parses every manifest of the bucket called name in dir. The
// files are parsed by up to GOMAXPROCS workers started with goFn.
func readBucket(name, dir string, goFn func(func())) (*Bucket, error) {
	manifests, stamp, updated := bucketInfo(dir)
	b := &Bucket{Name: name, Dir: dir, Stamp: stamp, Updated: updated, Source: gitRemote(dir)}
	entries, err := os.ReadDir(manifests)
	if err != nil {
		return b, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".json") && !strings.HasPrefix(e.Name(), ".") {
			files = append(files, e.Name())
		}
	}
	pkgs := make([]Package, len(files))
	ok := make([]bool, len(files))
	parse := func(i int) {
		path := filepath.Join(manifests, files[i])
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		p, err := ParseManifest(strings.TrimSuffix(files[i], filepath.Ext(files[i])), data)
		if err != nil || p.Version == "" {
			return // not a manifest (e.g. a config file at the bucket root)
		}
		p.Bucket, p.File = name, path
		pkgs[i], ok[i] = p, true
	}
	workers := min(runtime.GOMAXPROCS(0), max(len(files)/64, 1))
	if goFn == nil || workers <= 1 {
		for i := range files {
			parse(i)
		}
	} else {
		var wg sync.WaitGroup
		for w := range workers {
			wg.Add(1)
			goFn(func() {
				defer wg.Done()
				for i := w; i < len(files); i += workers {
					parse(i)
				}
			})
		}
		wg.Wait()
	}
	b.Packages = make([]Package, 0, len(files))
	for i := range pkgs {
		if ok[i] {
			b.Packages = append(b.Packages, pkgs[i])
		}
	}
	return b, nil
}

// installInfo is install.json (scoop-install.json in newer Scoop).
type installInfo struct {
	Bucket       string `json:"bucket"`
	Architecture string `json:"architecture"`
	URL          string `json:"url"`
	Hold         bool   `json:"hold"`
}

// readFirst returns the content of the first existing file of names in dir.
func readFirst(dir string, names ...string) ([]byte, string, error) {
	var err error
	for _, n := range names {
		var b []byte
		path := filepath.Join(dir, n)
		if b, err = os.ReadFile(path); err == nil {
			return b, path, nil
		}
	}
	return nil, "", err
}

// readApps returns the apps installed under scoopDir (a root or the global
// directory). Scoop itself is left out.
func readApps(scoopDir string, global bool) []App {
	if scoopDir == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(scoopDir, "apps"))
	if err != nil {
		return nil
	}
	var apps []App
	for _, e := range entries {
		if !e.IsDir() || strings.EqualFold(e.Name(), "scoop") {
			continue
		}
		cur := filepath.Join(scoopDir, "apps", e.Name(), "current")
		data, path, err := readFirst(cur, "manifest.json", "scoop-manifest.json")
		if err != nil {
			continue // not (completely) installed
		}
		info, err := ParseManifest(e.Name(), data)
		if err != nil {
			continue
		}
		info.File = path
		a := App{Name: e.Name(), Version: info.Version, Global: global, Dir: cur, Info: info}
		if data, _, err := readFirst(cur, "install.json", "scoop-install.json"); err == nil {
			var ii installInfo
			if json.Unmarshal(data, &ii, lenient) == nil {
				a.Bucket, a.Arch, a.URL, a.Held = ii.Bucket, ii.Architecture, ii.URL, ii.Hold
			}
		}
		a.Info.Bucket = a.Bucket
		apps = append(apps, a)
	}
	return apps
}

// ---- cache ----

const cacheVersion = 1

// cacheFile is the on-disk form of the bucket index.
type cacheFile struct {
	Version int       `json:"version"`
	Root    string    `json:"root"`
	Buckets []*Bucket `json:"buckets"`
}

func cachePath(dir string) string { return filepath.Join(dir, "index.json") }

// loadCache reads the cached buckets of root from dir.
func loadCache(dir, root string) ([]*Bucket, error) {
	data, err := os.ReadFile(cachePath(dir))
	if err != nil {
		return nil, err
	}
	var c cacheFile
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Version != cacheVersion || !strings.EqualFold(c.Root, root) {
		return nil, errors.New("scoop: stale cache")
	}
	return c.Buckets, nil
}

// saveCache writes the buckets of root to dir, atomically.
func saveCache(dir, root string, buckets []*Bucket) error {
	data, err := json.Marshal(cacheFile{Version: cacheVersion, Root: root, Buckets: buckets})
	if err != nil {
		return err
	}
	tmp := cachePath(dir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, cachePath(dir))
}

// refreshBuckets returns the buckets of root, reusing those of old whose
// stamp did not change and reading the others. changed reports whether
// anything differs from old.
func refreshBuckets(root string, old []*Bucket, goFn func(func())) (buckets []*Bucket, changed bool, errs []error) {
	dirs := bucketDirs(root)
	byName := map[string]*Bucket{}
	for _, b := range old {
		byName[b.Name] = b
	}
	for name, dir := range dirs {
		_, stamp, _ := bucketInfo(dir)
		if b, ok := byName[name]; ok && b.Stamp == stamp && strings.EqualFold(b.Dir, dir) {
			buckets = append(buckets, b)
			continue
		}
		b, err := readBucket(name, dir, goFn)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
		buckets = append(buckets, b)
		changed = true
	}
	if len(buckets) != len(old) {
		changed = true
	}
	return buckets, changed, errs
}
