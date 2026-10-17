// Package clock is the built-in world clock plugin (docs/world-clock.md).
//
// Typing a prefix such as "time" in the root search lists the local time and
// the favorite or default cities; "time bj", "time 北京" or "time utc+8"
// searches about 32,000 cities (GeoNames, plus a curated overlay) by name,
// pinyin, alias, country, IANA time zone, UTC offset or time zone
// abbreviation. The times tick: while such a search
// is showing, the plugin calls Host.Invalidate once a second, and the World
// Clock view pushes a new snapshot with Host.Render once a second.
package clock

import (
	"cmp"
	"context"
	"embed"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	_ "time/tzdata" // users' machines have no Go installation and no zoneinfo
	"unicode/utf8"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/pkg/plugin/bg"
)

// ID is the plugin's manifest ID.
const ID = "builtin.clock"

const (
	cmdWorld = "world-clock"

	prefPrefixes    = "prefixes"
	prefCities      = "cities"
	prefHour24      = "hour24"
	prefShowSeconds = "showSeconds"

	kvFavorites = "favorites"

	callbackCopy     = "copy"
	callbackFavorite = "favorite"

	localID = "local"

	// maxResults bounds the cities listed for a search.
	maxResults = 30
)

var (
	defaultPrefixes = []string{"time", "tz"}
	defaultCities   = []string{"Beijing", "Tokyo", "Singapore", "Mumbai", "Dubai", "Moscow", "London", "Paris",
		"Berlin", "New York", "San Francisco", "Sydney"}
	// localKeys find the local clock in a search.
	localKeys = func() []matchKey {
		var keys []matchKey
		for _, s := range []string{"local", "local time", "here", "本地", "本地时间", "当地", "bendi"} {
			keys = append(keys, newMatchKey(s, 1))
		}
		return keys
	}()
)

//go:embed locales/*.json
var locales embed.FS

// Options change how the plugin waits between ticks; tests replace them.
type Options struct {
	// After returns a channel that receives once d has passed (default
	// time.After). The refresh loops wait on it until the next whole second.
	After func(d time.Duration) <-chan time.Time
	// IdleAfter is the timer of the idle release of the city index
	// (default time.After).
	IdleAfter func(d time.Duration) <-chan time.Time
	// Sync loads the cities on the goroutine of the first search, view or
	// action that needs them instead of in the background, so a search never
	// asks the host to pull it again (glctl's fake environment). The index
	// then also stays cached for the process when the plugin closes.
	Sync bool
}

// Plugin is the world clock plugin.
type Plugin struct {
	opts  Options
	cache *indexCache
	// index returns the city index, building it if needed (cache.get).
	index func() (*index, error)
	// loaded is set once the cities are indexed; missed when a prefixed root
	// search came in before that (the host may have dropped it).
	loaded, missed atomic.Bool

	h     plugin.Host
	unsub func()
	wg    sync.WaitGroup
	loops bg.SecondLoops // the refresh loops

	mu     sync.Mutex
	closed bool
	root   *rootLoop            // refresh of the root search; nil when stopped
	views  map[string]*viewLoop // pushes to open World Clock views, by instance
	// Use of the index (load.go).
	held    bool            // p counts as a user of cache
	loading bool            // a background load runs
	gen     int             // incremented when p releases the index
	hidden  bool            // the launcher is hidden
	idle    chan struct{}   // closes to stop the idle timer; nil when none runs
	open    map[string]bool // World Clock view instances not closed
}

var (
	_ plugin.Plugin         = (*Plugin)(nil)
	_ plugin.RootProvider   = (*Plugin)(nil)
	_ plugin.ViewProvider   = (*Plugin)(nil)
	_ plugin.ActionHandler  = (*Plugin)(nil)
	_ plugin.LocaleProvider = (*Plugin)(nil)
	_ plugin.ViewCloser     = (*Plugin)(nil)
)

// New returns the world clock plugin.
func New() *Plugin { return NewWithOptions(Options{}) }

// NewWithOptions returns the plugin with o.
func NewWithOptions(o Options) *Plugin {
	if o.After == nil {
		o.After = time.After
	}
	if o.IdleAfter == nil {
		o.IdleAfter = time.After
	}
	p := &Plugin{opts: o, cache: sharedCache, index: sharedCache.get, views: map[string]*viewLoop{}, open: map[string]bool{}}
	p.loops.After, p.loops.WG = o.After, &p.wg
	return p
}

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:            ID,
		Name:          "%plugin.name%",
		Description:   "%plugin.description%",
		Version:       "1.0.0",
		Icon:          plugin.Icon{Name: "clock"},
		DefaultLocale: "en",
		Permissions:   []plugin.Permission{plugin.PermClipboard, plugin.PermStorage},
		Commands: []plugin.Command{{
			Name:     cmdWorld,
			Title:    "%command.world.title%",
			Subtitle: "%command.world.subtitle%",
			Mode:     plugin.ModeView,
			Icon:     plugin.Icon{Name: "clock"},
			Keywords: []string{"%command.world.keywords%", "时间", "时区", "世界时间"},
		}},
		Preferences: []plugin.Preference{
			{Name: prefPrefixes, Type: plugin.PrefStringList, Title: "%pref.prefixes.title%",
				Description: "%pref.prefixes.description%", Default: plugin.MustPayload(defaultPrefixes)},
			{Name: prefCities, Type: plugin.PrefStringList, Title: "%pref.cities.title%",
				Description: "%pref.cities.description%", Default: plugin.MustPayload(defaultCities)},
			{Name: prefHour24, Type: plugin.PrefBool, Title: "%pref.hour24.title%",
				Description: "%pref.hour24.description%", Default: plugin.MustPayload(true)},
			{Name: prefShowSeconds, Type: plugin.PrefBool, Title: "%pref.showSeconds.title%",
				Description: "%pref.showSeconds.description%", Default: plugin.MustPayload(true)},
		},
	}
}

// Locales implements plugin.LocaleProvider.
func (p *Plugin) Locales() fs.FS {
	sub, err := fs.Sub(locales, "locales")
	if err != nil {
		panic(err)
	}
	return sub
}

// Init implements plugin.Plugin. The city index is not built here but on
// first use (load.go).
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.mu.Lock()
	if p.closed { // Init after Close starts again (docs/plugin-guide.md "Lifecycle")
		p.closed, p.hidden = false, false
		p.gen++
		p.loaded.Store(false)
		clear(p.open)
	}
	p.mu.Unlock()
	p.h = h
	p.loops.Host = h
	hidden := h.Events().Subscribe(plugin.EventWindowHidden, p.windowHidden)
	shown := h.Events().Subscribe(plugin.EventWindowShown, p.windowShown)
	p.unsub = func() { hidden(); shown() }
	return nil
}

// Close implements plugin.Plugin. It stops all refresh loops and releases
// the city index.
func (p *Plugin) Close() error {
	p.mu.Lock()
	p.closed = true
	p.disarmIdleLocked()
	p.mu.Unlock()
	p.stopAll()
	if p.unsub != nil {
		p.unsub()
	}
	// Release only after a background load has ended: a load still to start
	// would otherwise build the index again after it was dropped.
	p.wg.Wait()
	p.mu.Lock()
	p.releaseOnCloseLocked()
	p.mu.Unlock()
	return nil
}

// RootItems implements plugin.RootProvider. Only queries starting with one
// of the prefixes get results; they are scored here because the host cannot
// match text that includes the prefix.
func (p *Plugin) RootItems(ctx context.Context, q plugin.Query) ([]plugin.Item, error) {
	rest, ok := p.cutPrefix(q.Text)
	if !ok {
		p.stopRoot()
		return nil, nil
	}
	p.use()
	if !p.loaded.Load() && !p.opts.Sync {
		p.missed.Store(true)
	}
	p.rootPulled()
	idx, err := p.index()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c := p.newContext(idx)
	group := c.f.l.T("group")
	var items []plugin.Item
	if rest == "" {
		items = append(items, c.localItem(true))
		for _, ct := range c.listed() {
			items = append(items, c.cityItem(ct, true))
		}
		// The local clock first; frecency (at most 0.3 of the rank) must not
		// move a city above it.
		for i := range items {
			items[i].Score = 0.55 - float64(0.001*float64(i))
		}
		items[0].Score = 1
	} else {
		items = c.search(rest, true)
		for i := range items {
			items[i].Score = 1 - float64(0.005*float64(i))
		}
	}
	for i := range items {
		items[i].Group = group
	}
	return items, nil
}

// cutPrefix returns the text after a search prefix ("time bj" -> "bj").
// A prefix must be followed by a space or the end of the text, except
// prefixes ending in a non-ASCII letter ("时间北京").
func (p *Plugin) cutPrefix(text string) (string, bool) {
	text = strings.TrimSpace(text)
	prefixes := p.h.Preferences().Strings(prefPrefixes)
	best, found := "", false
	bestLen := -1
	for _, pfx := range prefixes {
		pfx = strings.TrimSpace(pfx)
		if pfx == "" || len(pfx) > len(text) || !strings.EqualFold(text[:len(pfx)], pfx) || len(pfx) <= bestLen {
			continue
		}
		rest := text[len(pfx):]
		last, _ := utf8.DecodeLastRuneInString(pfx)
		if rest != "" && rest[0] != ' ' && last < utf8.RuneSelf {
			continue
		}
		best, found, bestLen = strings.TrimSpace(rest), true, len(pfx)
	}
	return best, found
}

// listContext is what building one list needs: the time, formatter, index
// and favorites.
type listContext struct {
	p    *Plugin
	idx  *index
	f    formatter
	now  time.Time
	favs []string
}

func (p *Plugin) newContext(idx *index) *listContext {
	prefs := p.h.Preferences()
	return &listContext{
		p:    p,
		idx:  idx,
		f:    formatter{l: p.h.I18n(), hour24: prefs.Bool(prefHour24), seconds: prefs.Bool(prefShowSeconds)},
		now:  p.h.Now(),
		favs: p.favorites(),
	}
}

// listed returns the favorite cities followed by the cities of the cities
// preference, without duplicates.
func (c *listContext) listed() []*city {
	var out []*city
	seen := map[string]bool{}
	add := func(names []string) {
		for _, n := range names {
			if ct, ok := c.idx.lookup(n); ok && !seen[ct.ID] {
				seen[ct.ID] = true
				out = append(out, ct)
			}
		}
	}
	add(c.favs)
	add(c.p.h.Preferences().Strings(prefCities))
	return out
}

// favoriteCities returns the favorites, in the order they were added.
func (c *listContext) favoriteCities() []*city {
	var out []*city
	for _, id := range c.favs {
		if ct, ok := c.idx.lookup(id); ok {
			out = append(out, ct)
		}
	}
	return out
}

// search returns the items of the cities matching q, best first; the local
// clock is included when q names it.
func (c *listContext) search(q string, root bool) []plugin.Item {
	nq := normalize(q)
	if nq == "" {
		return nil
	}
	qc := strings.ReplaceAll(nq, " ", "")
	// Cities that are favorites or listed come first among equal matches.
	rank := map[int]int{}
	for i, ct := range c.listed() {
		rank[ct.ord] = i - c.idx.len() - 1
	}
	hits := c.idx.find(q, c.now, rank, maxResults)
	var items []plugin.Item
	localScore := 0.0
	for i := range localKeys {
		localScore = max(localScore, scoreKey(nq, qc, &localKeys[i]))
	}
	for _, h := range hits {
		if localScore > 0 && localScore >= h.score {
			items = append(items, c.localItem(root))
			localScore = 0
		}
		items = append(items, c.cityItem(c.idx.at(h.ord), root))
		if len(items) == maxResults {
			break
		}
	}
	if localScore > 0 && len(items) < maxResults {
		items = append(items, c.localItem(root))
	}
	return items
}

// localItem is the item of the local clock.
func (c *listContext) localItem(root bool) plugin.Item {
	f, t := c.f, c.now
	name := f.l.T("local.name")
	zone := t.Location().String()
	if zone == "Local" || zone == "" {
		zone = zoneAbbr(t)
	}
	subtitle := f.l.T("item.subtitleLocal", "date", f.date(t), "offset", f.offset(t), "zone", zone)
	if zone == "" || zone == f.offset(t) {
		subtitle = f.l.T("item.subtitleLocalNoZone", "date", f.date(t), "offset", f.offset(t))
	}
	it := plugin.Item{
		ID:        localID,
		Title:     f.l.T("item.title", "city", name, "time", f.clock(t, f.seconds)),
		Subtitle:  subtitle,
		Icon:      plugin.Icon{Name: "clock"},
		Accessory: f.accessory(t, t, false),
		Detail:    f.detail(name, "", "", zone, t),
	}
	it.Actions = c.actions(localID, false, false, root)
	return it
}

// cityItem is the item of a city's clock.
func (c *listContext) cityItem(ct *city, root bool) plugin.Item {
	f := c.f
	t := c.now.In(ct.loc)
	_, cityOff := t.Zone()
	_, localOff := c.now.Zone()
	fav := slices.Contains(c.favs, ct.ID)
	country, region := c.placeNames(ct)
	subtitle := f.l.T("item.subtitle", "date", f.date(t), "offset", f.offset(t),
		"difference", f.difference(cityOff-localOff))
	if place := c.place(ct); place != "" {
		subtitle = f.l.T("item.subtitlePlace", "place", place, "date", f.date(t), "offset", f.offset(t),
			"difference", f.difference(cityOff-localOff))
	}
	it := plugin.Item{
		ID:        "city:" + ct.ID,
		Title:     f.l.T("item.title", "city", ct.Name, "time", f.clock(t, f.seconds)),
		Subtitle:  subtitle,
		Icon:      plugin.Icon{Name: "clock"},
		Accessory: f.accessory(t, c.now, fav),
		Detail:    f.detail(ct.Name, country, region, ct.TZ, t),
	}
	it.Actions = c.actions(ct.ID, true, fav, root)
	return it
}

// placeNames are the country and region of ct in the UI language: the
// Chinese names in a Chinese UI where there are any.
func (c *listContext) placeNames(ct *city) (country, region string) {
	country, region = ct.Country, ct.region
	if normalize(region) == normalize(ct.Name) {
		region = "" // Beijing, Beijing; Moscow, Moscow
	}
	if strings.HasPrefix(c.f.l.Locale(), "zh") {
		country = cmp.Or(ct.CountryZh, country)
		if region != "" {
			region = cmp.Or(ct.regionZh, region)
		}
	}
	return country, region
}

// place is where ct is, for the subtitle: the country, plus the region
// when another city has the same name ("Illinois, United States"); "" for
// clocks that are not places (UTC, cities made up from a zone).
func (c *listContext) place(ct *city) string {
	country, region := c.placeNames(ct)
	if country == "" || ct.TZ == "UTC" {
		return ""
	}
	if ct.ambiguous && region != "" {
		return c.f.l.T("place.region", "region", region, "country", country)
	}
	return country
}

// copyPayload is the payload of the copy callback.
type copyPayload struct {
	City string `json:"city"`
	ISO  bool   `json:"iso,omitzero"`
}

// favoritePayload is the payload of the favorite callback.
type favoritePayload struct {
	City string `json:"city"`
}

// actions are the actions of the clock of city id ("local" for the local
// clock). Copying runs in the plugin so that it copies the time at the
// moment it runs, also from a stale entry in the recently used list.
func (c *listContext) actions(id string, isCity, fav, root bool) []plugin.Action {
	l := c.f.l
	acts := []plugin.Action{
		{ID: "copy", Title: l.T("action.copyTime"),
			Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackCopy, Payload: plugin.MustPayload(copyPayload{City: id})}},
		{ID: "copy-iso", Title: l.T("action.copyISO"), Shortcut: "Ctrl+Enter",
			Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackCopy, Payload: plugin.MustPayload(copyPayload{City: id, ISO: true})}},
	}
	if isCity {
		title := l.T("action.favorite")
		if fav {
			title = l.T("action.unfavorite")
		}
		acts = append(acts, plugin.Action{ID: "favorite", Title: title, Shortcut: "Ctrl+Shift+P",
			Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackFavorite, Payload: plugin.MustPayload(favoritePayload{City: id})}})
	}
	if root {
		acts = append(acts, plugin.Action{ID: "open", Title: l.T("action.openView"),
			Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: cmdWorld}})
	}
	return acts
}

// View implements plugin.ViewProvider: the World Clock view. Each open
// instance gets a new snapshot pushed every second.
func (p *Plugin) View(ctx context.Context, req plugin.ViewRequest) (plugin.View, error) {
	if req.ID != cmdWorld {
		return plugin.View{}, fmt.Errorf("clock: unknown view %q", req.ID)
	}
	p.use()
	idx, err := p.index()
	if err != nil {
		return plugin.View{}, err
	}
	if req.Instance == "" {
		return p.worldView(idx, req.Query.Text), nil
	}
	vl := p.viewPulled(req.Instance)
	if vl == nil { // closed
		return p.worldView(idx, req.Query.Text), nil
	}
	// The loop renders with the query under the same lock, so no push made
	// after this pull uses the old query.
	vl.mu.Lock()
	defer vl.mu.Unlock()
	vl.query = req.Query.Text
	return p.worldView(idx, vl.query), ctx.Err()
}

// worldView is the World Clock view for search text q.
func (p *Plugin) worldView(idx *index, q string) plugin.View {
	c := p.newContext(idx)
	l := c.f.l
	v := plugin.View{
		Kind:        plugin.ViewList,
		Title:       l.T("view.title"),
		Placeholder: l.T("view.placeholder"),
		EmptyText:   l.T("view.empty"),
		ShowDetail:  true,
	}
	if strings.TrimSpace(q) != "" {
		v.Sections = []plugin.Section{{Items: c.search(q, false)}}
		return v
	}
	v.Sections = append(v.Sections, plugin.Section{Title: l.T("section.local"), Items: []plugin.Item{c.localItem(false)}})
	favs := c.favoriteCities()
	if len(favs) > 0 {
		s := plugin.Section{Title: l.T("section.favorites")}
		for _, ct := range favs {
			s.Items = append(s.Items, c.cityItem(ct, false))
		}
		v.Sections = append(v.Sections, s)
	}
	s := plugin.Section{Title: l.T("section.cities")}
	for _, ct := range c.listed() {
		if !slices.Contains(c.favs, ct.ID) {
			s.Items = append(s.Items, c.cityItem(ct, false))
		}
	}
	if len(s.Items) > 0 {
		v.Sections = append(v.Sections, s)
	}
	return v
}

// HandleAction implements plugin.ActionHandler: copying a time and toggling
// a favorite.
func (p *Plugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	p.use()
	idx, err := p.index()
	if err != nil {
		return plugin.Result{}, err
	}
	l := p.h.I18n()
	switch a.Target {
	case callbackCopy:
		var pl copyPayload
		if err := json.Unmarshal(a.Payload, &pl); err != nil {
			return plugin.Result{}, err
		}
		text, err := p.copyText(idx, pl)
		if err != nil {
			return plugin.Result{}, err
		}
		if err := p.h.Clipboard().WriteText(text); err != nil {
			return plugin.Result{}, err
		}
		return plugin.Result{Hide: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("toast.copied"), Message: text}}, nil
	case callbackFavorite:
		var pl favoritePayload
		if err := json.Unmarshal(a.Payload, &pl); err != nil {
			return plugin.Result{}, err
		}
		ct, ok := idx.lookup(pl.City)
		if !ok {
			return plugin.Result{}, errors.New(l.T("error.unknownCity", "city", pl.City))
		}
		added, err := p.toggleFavorite(ct.ID)
		if err != nil {
			return plugin.Result{}, err
		}
		msg := l.T("toast.unfavorited", "city", ct.Name)
		if added {
			msg = l.T("toast.favorited", "city", ct.Name)
		}
		return plugin.Result{Refresh: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: msg}}, nil
	default:
		return plugin.Result{}, fmt.Errorf("clock: unknown action %q", a.Target)
	}
}

// copyText is the text the copy actions put on the clipboard, at the time
// they run: "14:32 Beijing (UTC+8)" or "2026-10-15T14:32:05+08:00".
func (p *Plugin) copyText(idx *index, pl copyPayload) (string, error) {
	f := formatter{l: p.h.I18n(), hour24: p.h.Preferences().Bool(prefHour24)}
	t := p.h.Now()
	name := f.l.T("local.name")
	if pl.City != localID {
		ct, ok := idx.lookup(pl.City)
		if !ok {
			return "", errors.New(f.l.T("error.unknownCity", "city", pl.City))
		}
		t, name = t.In(ct.loc), ct.Name
	}
	if pl.ISO {
		return t.Format(time.RFC3339), nil
	}
	return f.l.T("copy.time", "time", f.clock(t, false), "city", name, "offset", f.offset(t)), nil
}

// favorites returns the IDs of the favorite cities.
func (p *Plugin) favorites() []string {
	b, ok, err := p.h.Storage().Get(kvFavorites)
	if err != nil || !ok {
		return nil
	}
	var ids []string
	if err := json.Unmarshal(b, &ids); err != nil {
		p.h.Logger().Warn("clock: bad favorites", "err", err)
		return nil
	}
	return ids
}

// toggleFavorite adds or removes city id and reports whether it was added.
func (p *Plugin) toggleFavorite(id string) (added bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := p.favorites()
	if i := slices.Index(ids, id); i >= 0 {
		ids = slices.Delete(ids, i, i+1)
	} else {
		ids, added = append(ids, id), true
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return false, err
	}
	return added, p.h.Storage().Set(kvFavorites, b)
}
