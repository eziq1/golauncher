package quicklinks

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Storage keys and format version (docs/quicklinks.md §9).
const (
	keyLinks   = "quicklinks"
	keyEngines = "engines"
	version    = 1
)

// Quicklink is a named link defined by the user.
type Quicklink struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Link     string    `json:"link"`
	Keyword  string    `json:"keyword,omitempty"`
	OpenWith string    `json:"openWith,omitempty"`
	Created  time.Time `json:"created,omitzero"`
	Updated  time.Time `json:"updated,omitzero"`
}

// linksData is the stored list of quicklinks.
type linksData struct {
	Version int         `json:"version"`
	Next    int         `json:"next"`
	Items   []Quicklink `json:"items"`
}

// override is the user's change to a built-in engine.
type override struct {
	Disabled bool     `json:"disabled,omitzero"`
	Keywords []string `json:"keywords,omitempty"`
}

// enginesData is the stored engine settings.
type enginesData struct {
	Version int                 `json:"version"`
	Next    int                 `json:"next"`
	Builtin map[string]override `json:"builtin,omitempty"`
	Custom  []Engine            `json:"custom,omitempty"`
}

// load reads both keys. A missing key is empty data; a corrupt one is
// logged and treated as empty (it is overwritten only by the next change).
func (p *Plugin) load() {
	kv := p.h.Storage()
	read := func(key string, v any) {
		b, ok, err := kv.Get(key)
		if err == nil && ok {
			err = json.Unmarshal(b, v)
		}
		if err != nil {
			p.h.Logger().Warn("quicklinks: cannot read stored data", "key", key, "err", err)
		}
	}
	var ld linksData
	read(keyLinks, &ld)
	var ed enginesData
	read(keyEngines, &ed)
	p.links, p.engines = ld, ed
}

// saveLinks writes the quicklinks. p.mu must be held.
func (p *Plugin) saveLinks() error {
	p.links.Version = version
	b, err := json.Marshal(p.links)
	if err != nil {
		return err
	}
	return p.h.Storage().Set(keyLinks, b)
}

// saveEngines writes the engine settings. p.mu must be held.
func (p *Plugin) saveEngines() error {
	p.engines.Version = version
	b, err := json.Marshal(p.engines)
	if err != nil {
		return err
	}
	return p.h.Storage().Set(keyEngines, b)
}

// linkList returns a copy of the quicklinks.
func (p *Plugin) linkList() []Quicklink {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.links.Items)
}

// findLink returns the quicklink with id.
func (p *Plugin) findLink(id string) (Quicklink, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := slices.IndexFunc(p.links.Items, func(q Quicklink) bool { return q.ID == id })
	if i < 0 {
		return Quicklink{}, false
	}
	return p.links.Items[i], true
}

// errNotFound is returned for an ID that no longer exists.
var errNotFound = errors.New("quicklinks: not found")

// putLink adds q (empty ID) or replaces the quicklink with its ID.
func (p *Plugin) putLink(q Quicklink) (Quicklink, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.h.Now().Round(0)
	q.Updated = now
	if q.ID == "" {
		p.links.Next++
		q.ID = strconv.Itoa(p.links.Next)
		q.Created = now
		p.links.Items = append(p.links.Items, q)
	} else {
		i := slices.IndexFunc(p.links.Items, func(x Quicklink) bool { return x.ID == q.ID })
		if i < 0 {
			return Quicklink{}, errNotFound
		}
		q.Created = p.links.Items[i].Created
		p.links.Items[i] = q
	}
	return q, p.saveLinks()
}

// deleteLink removes the quicklink with id.
func (p *Plugin) deleteLink(id string) (Quicklink, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := slices.IndexFunc(p.links.Items, func(x Quicklink) bool { return x.ID == id })
	if i < 0 {
		return Quicklink{}, errNotFound
	}
	q := p.links.Items[i]
	p.links.Items = slices.Delete(p.links.Items, i, i+1)
	return q, p.saveLinks()
}

// engineList returns all engines, built-in ones first, with the user's
// changes, their names and URLs for the current locale.
func (p *Plugin) engineList() []Engine {
	l := p.h.I18n()
	zh := isChinese(l.Locale())
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Engine, 0, len(builtins)+len(p.engines.Custom))
	for _, b := range builtins {
		e := Engine{ID: b.id, Name: b.localName(l), Keywords: slices.Clone(b.keywords), URL: b.url, Suggest: b.suggest}
		if zh && b.urlZh != "" {
			e.URL = b.urlZh
		}
		if o, ok := p.engines.Builtin[b.id]; ok {
			e.Disabled = o.Disabled
			if o.Keywords != nil {
				e.Keywords = slices.Clone(o.Keywords)
			}
		}
		out = append(out, e)
	}
	for _, c := range p.engines.Custom {
		c.Custom = true
		c.Keywords = slices.Clone(c.Keywords)
		out = append(out, c)
	}
	return out
}

// engineByID returns the engine with id.
func (p *Plugin) engineByID(id string) (Engine, bool) {
	for _, e := range p.engineList() {
		if e.ID == id {
			return e, true
		}
	}
	return Engine{}, false
}

// updateBuiltin changes the override of a built-in engine.
func (p *Plugin) updateBuiltin(id string, fn func(*override)) error {
	b, ok := builtinByID(id)
	if !ok {
		return errNotFound
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.engines.Builtin == nil {
		p.engines.Builtin = map[string]override{}
	}
	o := p.engines.Builtin[id]
	fn(&o)
	if o.Keywords != nil && slices.Equal(o.Keywords, b.keywords) {
		o.Keywords = nil
	}
	if !o.Disabled && o.Keywords == nil {
		delete(p.engines.Builtin, id)
	} else {
		p.engines.Builtin[id] = o
	}
	return p.saveEngines()
}

// putCustom adds e (empty ID) or replaces the custom engine with its ID.
func (p *Plugin) putCustom(e Engine) (Engine, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e.Custom = false
	if e.ID == "" {
		p.engines.Next++
		e.ID = "custom" + strconv.Itoa(p.engines.Next)
		p.engines.Custom = append(p.engines.Custom, e)
	} else {
		i := slices.IndexFunc(p.engines.Custom, func(x Engine) bool { return x.ID == e.ID })
		if i < 0 {
			return Engine{}, errNotFound
		}
		e.Disabled = p.engines.Custom[i].Disabled
		p.engines.Custom[i] = e
	}
	return e, p.saveEngines()
}

// setCustomDisabled turns a custom engine on or off.
func (p *Plugin) setCustomDisabled(id string, disabled bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := slices.IndexFunc(p.engines.Custom, func(x Engine) bool { return x.ID == id })
	if i < 0 {
		return errNotFound
	}
	p.engines.Custom[i].Disabled = disabled
	return p.saveEngines()
}

// deleteCustom removes a custom engine.
func (p *Plugin) deleteCustom(id string) (Engine, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := slices.IndexFunc(p.engines.Custom, func(x Engine) bool { return x.ID == id })
	if i < 0 {
		return Engine{}, errNotFound
	}
	e := p.engines.Custom[i]
	p.engines.Custom = slices.Delete(p.engines.Custom, i, i+1)
	return e, p.saveEngines()
}

// keywordOwner returns the name of the quicklink or enabled engine using
// keyword kw, other than the quicklink skipLink and the engine skipEngine.
func (p *Plugin) keywordOwner(kw, skipLink, skipEngine string) (string, bool) {
	kw = normKeyword(kw)
	if kw == "" {
		return "", false
	}
	for _, q := range p.linkList() {
		if q.ID != skipLink && normKeyword(q.Keyword) == kw {
			return q.Name, true
		}
	}
	for _, e := range p.engineList() {
		if e.ID == skipEngine || e.Disabled {
			continue
		}
		if slices.ContainsFunc(e.Keywords, func(k string) bool { return normKeyword(k) == kw }) {
			return e.Name, true
		}
	}
	return "", false
}

// exportFile is the import/export format (docs/quicklinks.md §5.5).
type exportFile struct {
	Golauncher     string           `json:"golauncher"`
	Version        int              `json:"version"`
	Quicklinks     []exportLink     `json:"quicklinks"`
	SearchEngines  []exportEngine   `json:"searchEngines"`
	BuiltinEngines []exportOverride `json:"builtinEngines,omitempty"`
}

type exportLink struct {
	Name     string `json:"name"`
	Link     string `json:"link"`
	Keyword  string `json:"keyword,omitempty"`
	OpenWith string `json:"openWith,omitempty"`
}

type exportEngine struct {
	Name     string   `json:"name"`
	Keywords []string `json:"keywords"`
	URL      string   `json:"url"`
	Suggest  string   `json:"suggest,omitempty"`
	Disabled bool     `json:"disabled,omitzero"`
}

type exportOverride struct {
	ID       string   `json:"id"`
	Enabled  *bool    `json:"enabled,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
}

// exportJSON returns all quicklinks, custom engines and changes of
// built-in engines as indented JSON.
func (p *Plugin) exportJSON() string {
	p.mu.Lock()
	f := exportFile{Golauncher: "quicklinks", Version: version, Quicklinks: []exportLink{}, SearchEngines: []exportEngine{}}
	for _, q := range p.links.Items {
		f.Quicklinks = append(f.Quicklinks, exportLink{Name: q.Name, Link: q.Link, Keyword: q.Keyword, OpenWith: q.OpenWith})
	}
	for _, e := range p.engines.Custom {
		f.SearchEngines = append(f.SearchEngines, exportEngine{Name: e.Name, Keywords: e.Keywords, URL: e.URL, Suggest: e.Suggest, Disabled: e.Disabled})
	}
	for _, b := range builtins {
		o, ok := p.engines.Builtin[b.id]
		if !ok {
			continue
		}
		x := exportOverride{ID: b.id, Keywords: o.Keywords}
		if o.Disabled {
			x.Enabled = new(false)
		}
		f.BuiltinEngines = append(f.BuiltinEngines, x)
	}
	p.mu.Unlock()
	b, _ := json.Marshal(f, jsontext.WithIndent("  "))
	return string(b)
}

// importReport counts what an import did.
type importReport struct {
	Links, Engines, Builtins, Skipped, DroppedKeywords int
}

// errFormat is returned for JSON that is not an export file.
var errFormat = errors.New("quicklinks: not a quicklinks file")

// importJSON merges an export file (or a Raycast quicklinks export, a JSON
// array) into the data: identical quicklinks are skipped, keywords already
// in use are dropped, a custom engine with the same name and URL is
// updated. Nothing changes when the JSON is invalid.
func (p *Plugin) importJSON(data []byte) (importReport, error) {
	var f exportFile
	data = []byte(strings.TrimSpace(strings.TrimPrefix(string(data), "\xef\xbb\xbf")))
	if len(data) > 0 && data[0] == '[' {
		if err := json.Unmarshal(data, &f.Quicklinks); err != nil {
			return importReport{}, errFormat
		}
	} else if err := json.Unmarshal(data, &f); err != nil || (f.Quicklinks == nil && f.SearchEngines == nil && f.BuiltinEngines == nil) {
		return importReport{}, errFormat
	}
	var r importReport
	taken := func(kw string) bool {
		_, used := p.keywordOwner(kw, "", "")
		return used
	}
	// Built-in changes first, so that keywords given to them count.
	for _, o := range f.BuiltinEngines {
		if _, ok := builtinByID(o.ID); !ok {
			r.Skipped++
			continue
		}
		var kws []string
		for _, k := range o.Keywords {
			if k = normKeyword(k); k != "" && !strings.ContainsFunc(k, isSpace) {
				kws = append(kws, k)
			}
		}
		err := p.updateBuiltin(o.ID, func(ov *override) {
			if o.Enabled != nil {
				ov.Disabled = !*o.Enabled
			}
			if len(kws) > 0 {
				ov.Keywords = kws
			}
		})
		if err != nil {
			return r, err
		}
		r.Builtins++
	}
	for _, x := range f.SearchEngines {
		x.Name, x.URL = strings.TrimSpace(x.Name), strings.TrimSpace(x.URL)
		if x.Name == "" || x.URL == "" {
			r.Skipped++
			continue
		}
		var existing string
		for _, e := range p.engineList() {
			if e.Custom && e.Name == x.Name && e.URL == x.URL {
				existing = e.ID
			}
		}
		var kws []string
		for _, k := range parseKeywords(strings.Join(x.Keywords, " ")) {
			if owner, used := p.keywordOwner(k, "", existing); used && owner != "" {
				r.DroppedKeywords++
				continue
			}
			kws = append(kws, k)
		}
		e := Engine{ID: existing, Name: x.Name, Keywords: kws, URL: x.URL, Suggest: strings.TrimSpace(x.Suggest)}
		saved, err := p.putCustom(e)
		if err != nil {
			return r, err
		}
		if existing == "" && x.Disabled {
			if err := p.setCustomDisabled(saved.ID, true); err != nil {
				return r, err
			}
		}
		r.Engines++
	}
	for _, x := range f.Quicklinks {
		x.Name, x.Link = strings.TrimSpace(x.Name), strings.TrimSpace(x.Link)
		if x.Name == "" || x.Link == "" {
			r.Skipped++
			continue
		}
		if slices.ContainsFunc(p.linkList(), func(q Quicklink) bool { return q.Name == x.Name && q.Link == x.Link }) {
			r.Skipped++
			continue
		}
		kw := normKeyword(x.Keyword)
		if kw != "" && (strings.ContainsFunc(kw, isSpace) || taken(kw)) {
			kw = ""
			r.DroppedKeywords++
		}
		if _, err := p.putLink(Quicklink{Name: x.Name, Link: x.Link, Keyword: kw, OpenWith: strings.TrimSpace(x.OpenWith)}); err != nil {
			return r, err
		}
		r.Links++
	}
	return r, nil
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '　' }
