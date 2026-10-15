package winget

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	_ "modernc.org/sqlite" // the "sqlite" driver
)

// Package is a package of a source's index.
type Package struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Moniker  string   `json:"moniker,omitempty"`
	Version  string   `json:"version"`
	Source   string   `json:"source"`
	Tags     []string `json:"tags,omitempty"`
	Commands []string `json:"commands,omitempty"`
}

// Catalog is the index of one pre-indexed source.
type Catalog struct {
	Source   string
	Stamp    string // changes when the source's index package is updated
	Packages []*Package
}

// errSchema reports an index whose format is not supported.
var errSchema = errors.New("winget: unsupported index schema")

// readOnlyDSN is the DSN that opens the SQLite file at path read-only.
// immutable tells SQLite that nobody changes the file (true for the files of
// an installed MSIX package), so it takes no locks.
func readOnlyDSN(path string, immutable bool) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	dsn := u.String() + "?mode=ro&_pragma=busy_timeout(2000)"
	if immutable {
		dsn += "&immutable=1"
	}
	return dsn
}

// OpenFile opens the SQLite file at path read-only.
func OpenFile(path string, immutable bool) (*sql.DB, error) {
	return sql.Open("sqlite", readOnlyDSN(path, immutable))
}

// ReadCatalog reads the packages, tags and commands of a winget source
// index (schema 2.x, the format of the pre-indexed "winget" source).
func ReadCatalog(ctx context.Context, db *sql.DB, source string) (*Catalog, error) {
	var major string
	if err := db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE name = 'majorVersion'`).Scan(&major); err != nil {
		return nil, fmt.Errorf("winget: reading the index version: %w", err)
	}
	if major != "2" {
		return nil, fmt.Errorf("%w %s", errSchema, major)
	}
	rows, err := db.QueryContext(ctx, `SELECT rowid, id, name, IFNULL(moniker, ''), latest_version FROM packages`)
	if err != nil {
		return nil, err
	}
	byRow := map[int64]*Package{}
	c := &Catalog{Source: source}
	for rows.Next() {
		var row int64
		p := &Package{Source: source}
		if err := rows.Scan(&row, &p.ID, &p.Name, &p.Moniker, &p.Version); err != nil {
			rows.Close()
			return nil, err
		}
		byRow[row] = p
		c.Packages = append(c.Packages, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, m := range []struct {
		query string
		add   func(p *Package, v string)
	}{
		{`SELECT m.package, t.tag FROM tags2_map m JOIN tags2 t ON t.rowid = m.tag`,
			func(p *Package, v string) { p.Tags = append(p.Tags, v) }},
		{`SELECT m.package, c.command FROM commands2_map m JOIN commands2 c ON c.rowid = m.command`,
			func(p *Package, v string) { p.Commands = append(p.Commands, v) }},
	} {
		rows, err := db.QueryContext(ctx, m.query)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var row int64
			var v string
			if err := rows.Scan(&row, &v); err != nil {
				rows.Close()
				return nil, err
			}
			if p := byRow[row]; p != nil {
				m.add(p, v)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	for _, p := range c.Packages {
		slices.Sort(p.Tags)
		slices.Sort(p.Commands)
	}
	return c, nil
}

// entry is a package prepared for searching.
type entry struct {
	pkg      *Package
	id, name string // lower case
	moniker  string
	parts    []string // the dot-separated parts of the ID
	words    []string // the words of the name
	commands []string
	tags     []string
}

// Index is what the plugin searches: the catalogs of all pre-indexed
// sources.
type Index struct {
	Catalogs []*Catalog
	entries  []entry
	byID     map[string]*Package // lower-case "source/id"
}

func newIndex(catalogs []*Catalog) *Index {
	ix := &Index{Catalogs: catalogs, byID: map[string]*Package{}}
	for _, c := range catalogs {
		for _, p := range c.Packages {
			ix.byID[strings.ToLower(p.Source+"/"+p.ID)] = p
			e := entry{
				pkg:     p,
				id:      strings.ToLower(p.ID),
				name:    strings.ToLower(p.Name),
				moniker: strings.ToLower(p.Moniker),
			}
			e.parts = strings.Split(e.id, ".")
			e.words = strings.FieldsFunc(e.name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
			for _, c := range p.Commands {
				e.commands = append(e.commands, strings.ToLower(c))
			}
			for _, t := range p.Tags {
				e.tags = append(e.tags, strings.ToLower(t))
			}
			ix.entries = append(ix.entries, e)
		}
	}
	return ix
}

// PackageCount returns the number of packages of all catalogs.
func (ix *Index) PackageCount() int {
	if ix == nil {
		return 0
	}
	return len(ix.entries)
}

// Package returns the package id of source.
func (ix *Index) Package(source, id string) (*Package, bool) {
	if ix == nil {
		return nil, false
	}
	p, ok := ix.byID[strings.ToLower(source+"/"+id)]
	return p, ok
}

// termScore scores how well the lower-case search term t matches e; 0
// means no match. Exact names, IDs and monikers beat prefixes, which beat
// commands, contained text and tags.
func termScore(e *entry, t string) int {
	switch {
	case e.id == t || e.name == t:
		return 1000
	case e.moniker == t:
		return 950
	case strings.HasPrefix(e.name, t):
		return 800 - min(len(e.name)-len(t), 100)
	case strings.HasPrefix(e.id, t):
		return 780 - min(len(e.id)-len(t), 100)
	}
	best := 0
	for _, c := range e.commands {
		switch {
		case c == t:
			best = max(best, 750)
		case strings.HasPrefix(c, t):
			best = max(best, 550)
		}
	}
	if strings.HasPrefix(e.moniker, t) {
		best = max(best, 700)
	}
	if i := strings.Index(e.name, t); i >= 0 {
		best = max(best, 600-min(i, 50)-min(len(e.name)-len(t), 100))
	}
	for _, p := range e.parts {
		if strings.HasPrefix(p, t) {
			best = max(best, 500)
		}
	}
	if best > 0 {
		return best
	}
	for _, w := range e.words {
		if strings.HasPrefix(w, t) {
			return 400
		}
	}
	if strings.Contains(e.id, t) {
		return 350
	}
	for _, tag := range e.tags {
		switch {
		case tag == t:
			return 300
		case strings.HasPrefix(tag, t):
			best = max(best, 200)
		}
	}
	if best > 0 {
		return best
	}
	if len(t) >= 2 && subsequence(e.name, t) {
		return 100
	}
	return 0
}

// subsequence reports whether the runes of t appear in s in order.
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
// first; ties go to the shorter name.
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
		if i%2048 == 0 && ctx.Err() != nil {
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
		if total > 0 && len(terms) > 1 && strings.Contains(e.name, phrase) {
			total += 1500 // the words in order: "visual studio code"
		}
		if total > 0 {
			hits = append(hits, hit{e, total})
		}
	}
	slices.SortFunc(hits, func(a, b hit) int {
		return cmp.Or(
			cmp.Compare(b.score, a.score),
			cmp.Compare(len(a.e.name), len(b.e.name)),
			strings.Compare(a.e.name, b.e.name),
			strings.Compare(a.e.id, b.e.id),
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
