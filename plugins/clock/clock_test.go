package clock

import (
	"encoding/json/v2"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// fakeClock is an adjustable clock for the fake host.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fakeTicks replaces time.After: a tick is delivered only when the test
// calls tick, which also advances the clock to the time waited for.
type fakeTicks struct {
	ch    chan time.Time
	clock *fakeClock

	mu       sync.Mutex
	waits    []time.Duration
	consumed int // waits answered by tick
}

func (f *fakeTicks) after(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	f.waits = append(f.waits, d)
	f.mu.Unlock()
	return f.ch
}

func (f *fakeTicks) lastWait() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.waits) == 0 {
		return 0
	}
	return f.waits[len(f.waits)-1]
}

// tick lets one waiting refresh loop run, as if the time it waited for had
// passed.
func (f *fakeTicks) tick(t *testing.T) {
	t.Helper()
	eventually(t, "a loop waits", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.waits) > f.consumed
	})
	f.mu.Lock()
	f.consumed = len(f.waits)
	f.mu.Unlock()
	f.clock.advance(f.lastWait())
	select {
	case f.ch <- f.clock.now():
	case <-time.After(2 * time.Second):
		t.Fatal("no refresh loop is waiting for a tick")
	}
}

type env struct {
	p     *Plugin
	h     *hostfake.Host
	clock *fakeClock
	ticks *fakeTicks
}

// start is 2026-10-15 10:00:00.25 UTC, a quarter second past a whole second.
var start = time.Date(2026, 10, 15, 10, 0, 0, 250e6, time.UTC)

func newEnv(t *testing.T) *env {
	t.Helper()
	clock := &fakeClock{t: start}
	ticks := &fakeTicks{ch: make(chan time.Time), clock: clock}
	p := NewWithOptions(Options{After: ticks.after})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.Clock = clock.now
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	// Let the cities finish loading, so tests count only their own
	// invalidations (a search during loading asks for one more).
	eventually(t, "cities loaded", p.loaded.Load)
	return &env{p, h, clock, ticks}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second) // loading the cities is slow under -race
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func (e *env) root(t *testing.T, text string) []plugin.Item {
	t.Helper()
	items, err := e.p.RootItems(t.Context(), plugin.Query{Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func (e *env) setPref(name string, v any) {
	e.h.Prefs.Set(name, plugin.MustPayload(v))
}

func ids(items []plugin.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func TestManifest(t *testing.T) {
	m := New().Manifest()
	if m.ID != ID || m.Icon.Name != "clock" || len(m.Commands) != 1 || m.Commands[0].Mode != plugin.ModeView {
		t.Errorf("manifest = %+v", m)
	}
	var names []string
	for _, p := range m.Preferences {
		names = append(names, p.Name)
	}
	if want := []string{prefPrefixes, prefCities, prefHour24, prefShowSeconds}; !slices.Equal(names, want) {
		t.Errorf("preferences = %v, want %v", names, want)
	}
}

func TestCities(t *testing.T) {
	idx, err := loadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(idx.cities); n < 30000 {
		t.Errorf("%d cities, want 30,000 or more", n)
	}
	for _, name := range defaultCities {
		if c, ok := idx.lookup(name); !ok || !c.curated {
			t.Errorf("default city %q not found", name)
		}
	}
	curated := 0
	for _, c := range idx.cities {
		if c.Name == "" || c.Country == "" {
			t.Errorf("city %s lacks a name or country", c.ID)
		}
		if !c.curated {
			continue
		}
		curated++
		if c.ord != curated-1 {
			t.Errorf("curated city %s at %d, want the curated cities first", c.ID, c.ord)
		}
		if c.Zh == "" || c.CountryZh == "" {
			t.Errorf("curated city %s lacks a Chinese name", c.ID)
		}
		// Each curated city's geonameid is in the GeoNames data, which
		// gives it a country code.
		if c.GeonameID != 0 && c.cc == "" {
			t.Errorf("curated city %s: geonameid %d not found in the GeoNames data", c.ID, c.GeonameID)
		}
	}
	if curated < 150 {
		t.Errorf("%d curated cities, want about 150 or more", curated)
	}
}

func TestNoPrefixNoResults(t *testing.T) {
	e := newEnv(t)
	for _, q := range []string{"", "beijing", "timer", "times", "vsc", "tzz"} {
		if items := e.root(t, q); len(items) != 0 {
			t.Errorf("%q: got %v", q, ids(items))
		}
	}
}

func TestRootList(t *testing.T) {
	e := newEnv(t)
	items := e.root(t, "time")
	if len(items) != 1+len(defaultCities) {
		t.Fatalf("got %v", ids(items))
	}
	if items[0].ID != localID || items[0].Score != 1 || items[1].ID != "city:beijing" || items[12].ID != "city:sydney" {
		t.Errorf("order = %v", ids(items))
	}
	for i, it := range items {
		if it.Group != "World Clock" {
			t.Errorf("group = %q", it.Group)
		}
		if i > 0 && (it.Score >= items[i-1].Score || 1-it.Score <= 0.3/0.7) {
			t.Errorf("score of %s = %v: local first, then in order, frecency must not lift a city above local", it.ID, it.Score)
		}
	}
	if got := items[1].Title; got != "Beijing  18:00:00" {
		t.Errorf("title = %q", got)
	}
	if got := items[1].Subtitle; got != "China · Thursday, Oct 15 · UTC+8 · +8h" {
		t.Errorf("subtitle = %q", got)
	}
	if got := items[0].Subtitle; got != "Thursday, Oct 15 · UTC" {
		t.Errorf("local subtitle = %q", got)
	}
	// "tz" works too, upper case and with surrounding spaces.
	if got := e.root(t, "  TZ "); len(got) != len(items) {
		t.Errorf("tz: %v", ids(got))
	}
}

func TestFavoritesFirst(t *testing.T) {
	e := newEnv(t)
	for _, id := range []string{"kathmandu", "london"} {
		res, err := e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackFavorite,
			Payload: plugin.MustPayload(favoritePayload{City: id})})
		if err != nil || !res.Refresh || res.Toast == nil {
			t.Fatalf("favorite %s: %+v %v", id, res, err)
		}
	}
	items := e.root(t, "time")
	got := ids(items)
	if !slices.Equal(got[:4], []string{localID, "city:kathmandu", "city:london", "city:beijing"}) || slices.Index(got[4:], "city:london") >= 0 {
		t.Errorf("order = %v", got)
	}
	if !strings.HasPrefix(items[1].Accessory, symbolFavorite) || items[1].Actions[2].Title != "Remove from Favorites" {
		t.Errorf("favorite item = %+v", items[1])
	}
	// Toggle back.
	if _, err := e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackFavorite,
		Payload: plugin.MustPayload(favoritePayload{City: "kathmandu"})}); err != nil {
		t.Fatal(err)
	}
	if got := ids(e.root(t, "time")); got[1] != "city:london" || slices.Contains(got, "city:kathmandu") {
		t.Errorf("after removing: %v", got)
	}
}

func TestCitiesPreference(t *testing.T) {
	e := newEnv(t)
	// Boise is a GeoNames city (ID 5586437); no city uses Etc/GMT+5.
	e.setPref(prefCities, []string{"东京", "nyc", "Europe/Lisbon", "Boise", "Etc/GMT+5", "Qwxzville"})
	got := ids(e.root(t, "time"))
	if want := []string{localID, "city:tokyo", "city:new-york", "city:lisbon", "city:5586437", "city:tz:Etc/GMT+5"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCustomPrefixes(t *testing.T) {
	e := newEnv(t)
	e.setPref(prefPrefixes, []string{"时间", "clock"})
	if got := e.root(t, "时间北京"); len(got) == 0 || got[0].ID != "city:beijing" {
		t.Errorf("时间北京: %v", ids(got))
	}
	if got := e.root(t, "clock tokyo"); len(got) == 0 || got[0].ID != "city:tokyo" {
		t.Errorf("clock tokyo: %v", ids(got))
	}
	if got := e.root(t, "time tokyo"); len(got) != 0 {
		t.Errorf("time is no longer a prefix: %v", ids(got))
	}
	if got := e.root(t, "clockwork"); len(got) != 0 {
		t.Errorf("clockwork: %v", ids(got))
	}
}

func TestMatching(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		query string
		first string
		also  []string // also listed
	}{
		{"bj", "beijing", nil},
		{"beijing", "beijing", nil},
		{"北京", "beijing", nil},
		{"bei", "beijing", []string{"beirut"}},
		{"Beijing ", "beijing", nil},
		{"nyc", "new-york", nil},
		{"new york", "new-york", nil},
		{"newyork", "new-york", nil},
		// York, England is a city of its own (an exact match beats the word
		// prefix in "New York City").
		{"york", "2633352", []string{"new-york"}},
		// GeoNames cities (IDs are GeoNames IDs).
		{"springfield", "4409896", []string{"4951788", "4250542"}}, // largest first: Missouri, Massachusetts, Illinois
		{"san jose", "san-jose", []string{"3621849", "1689510"}},   // curated California, Costa Rica, the Philippines
		{"san josé", "san-jose", []string{"3621849"}},
		{"kiruna", "605155", nil}, // about 20,000 people
		{"tromso", "3133895", nil},
		{"tromsø", "3133895", nil},
		{"宝鸡", "10942359", nil}, // Chinese name from GeoNames
		{"baoji", "10942359", nil},
		{"基律纳", "605155", nil},
		{"jilvna", "605155", nil},                              // pinyin of a transliteration
		{"nepal", "6941099", []string{"kathmandu", "1282898"}}, // Nepalgunj (a name prefix) before the country
		{"nepal ", "6941099", nil},
		{"尼泊尔", "kathmandu", []string{"1282898"}},
		{"sf", "san-francisco", nil},
		{"la", "los-angeles", nil},
		{"hk", "hong-kong", nil},
		{"sh", "shanghai", nil},
		{"上海", "shanghai", nil},
		{"shanghai", "shanghai", nil},
		{"sz", "shenzhen", []string{"suzhou"}},
		{"chongqing", "chongqing", nil},
		{"海参崴", "vladivostok", nil},
		{"sao paulo", "sao-paulo", nil},
		{"são", "sao-paulo", nil},
		{"xian", "xian", nil},
		{"hcmc", "ho-chi-minh-city", nil},
		{"Asia/Tokyo", "tokyo", []string{"osaka"}},
		{"asia/kolkata", "mumbai", []string{"new-delhi"}},
		{"japan", "tokyo", []string{"osaka"}},
		{"日本", "tokyo", []string{"osaka"}},
		{"riben", "tokyo", nil},
		{"utc+8", "beijing", []string{"singapore", "perth", "taipei"}},
		{"gmt+8", "beijing", []string{"singapore"}},
		{"+8", "beijing", nil},
		{"utc+5:30", "mumbai", []string{"new-delhi", "colombo"}},
		{"UTC+0530", "mumbai", nil},
		{"utc+5:45", "kathmandu", nil},
		{"utc-3", "halifax", []string{"sao-paulo", "buenos-aires"}},
		{"utc", "utc", []string{"accra"}},
		{"pst", "san-francisco", []string{"los-angeles", "seattle"}},
		{"pdt", "san-francisco", nil},
		{"cet", "paris", []string{"berlin"}},
		{"jst", "tokyo", nil},
		{"ist", "mumbai", []string{"new-delhi"}},
		{"cst", "beijing", []string{"chicago"}},
		{"est", "new-york", []string{"toronto"}},
		{"sydny", "sydney", nil},
		{"local", localID, nil},
		{"本地", localID, nil},
	}
	for _, tt := range tests {
		items := e.root(t, "time "+tt.query)
		got := ids(items)
		want := tt.first
		if want != localID {
			want = "city:" + want
		}
		if len(got) == 0 || got[0] != want {
			t.Errorf("time %s: got %v, want %s first", tt.query, got, want)
			continue
		}
		if len(got) > maxResults {
			t.Errorf("time %s: %d results, want at most %d", tt.query, len(got), maxResults)
		}
		for _, a := range tt.also {
			if !slices.Contains(got, "city:"+a) {
				t.Errorf("time %s: %s missing from %v", tt.query, a, got)
			}
		}
		for i := 1; i < len(items); i++ {
			if items[i].Score >= items[i-1].Score {
				t.Errorf("time %s: scores not decreasing: %v", tt.query, items)
			}
		}
	}
	for _, q := range []string{"zzzz", "utc+15", "utc+5:99", "xyz/abc"} {
		if got := e.root(t, "time "+q); len(got) != 0 {
			t.Errorf("time %s: got %v", q, ids(got))
		}
	}
	// Precise queries do not list unrelated cities. ("bj" also finds 宝鸡
	// and names starting with "Bj", "nyc" substrings such as Bortnychi.)
	for _, q := range []string{"北京"} {
		if got := e.root(t, "time "+q); len(got) != 1 {
			t.Errorf("time %s: got %v, want one city", q, ids(got))
		}
	}
	// Pinyin initials count only for Chinese cities: "bkl" is not 博克拉
	// (Pokhara, Nepal), while "bj" finds 宝鸡 (Baoji) after Beijing.
	if got := ids(e.root(t, "time bkl")); slices.Contains(got, "city:1282898") {
		t.Errorf("time bkl: got %v", got)
	}
	if got := ids(e.root(t, "time bj")); got[0] != "city:beijing" || !slices.Contains(got, "city:10942359") {
		t.Errorf("time bj: got %v", got)
	}
	// Same-name cities show their region and country.
	items := e.root(t, "time springfield")
	if got := items[0].Subtitle; got != "Missouri, United States · Thursday, Oct 15 · UTC-5 · -5h" {
		t.Errorf("springfield subtitle = %q", got)
	}
	if got := e.root(t, "time kiruna")[0].Subtitle; got != "Sweden · Thursday, Oct 15 · UTC+2 · +2h" {
		t.Errorf("kiruna subtitle = %q", got)
	}
}

// TestChineseUI: the subtitle and detail use Chinese country and region
// names in a Chinese UI.
func TestChineseUI(t *testing.T) {
	e := newEnv(t)
	e.h.I18N.SetLocale("zh-CN")
	it := e.root(t, "time springfield")[0]
	if got := it.Subtitle; !strings.HasPrefix(got, "美国 密苏里州 · ") {
		t.Errorf("subtitle = %q", got)
	}
	var country, region string
	for _, m := range it.Detail.Metadata {
		switch m.Label {
		case "国家/地区":
			country = m.Value
		case "省/州":
			region = m.Value
		}
	}
	if country != "美国" || region != "密苏里州" {
		t.Errorf("detail = %+v", it.Detail.Metadata)
	}
	if got := e.root(t, "time 北京")[0].Subtitle; !strings.HasPrefix(got, "中国 · ") {
		t.Errorf("北京 subtitle = %q", got)
	}
}

func TestParseOffset(t *testing.T) {
	tests := map[string]offsetQuery{
		"utc":       {0, true},
		"gmt":       {0, true},
		"utc+8":     {8 * 3600, true},
		"utc +8":    {8 * 3600, true},
		"utc-5:30":  {-(5*3600 + 30*60), true},
		"utc−3":     {-3 * 3600, true},
		"+0545":     {5*3600 + 45*60, true},
		"-10":       {-10 * 3600, true},
		"utc+14":    {14 * 3600, true},
		"utc+15":    {},
		"utc+5:7":   {},
		"utc8":      {},
		"8":         {},
		"utc+":      {},
		"beijing":   {},
		"utc+08:00": {8 * 3600, true},
	}
	for in, want := range tests {
		if got := parseOffset(in); got != want {
			t.Errorf("parseOffset(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func TestCopyActions(t *testing.T) {
	e := newEnv(t)
	items := e.root(t, "time kathmandu")
	run := func(a plugin.Action) string {
		t.Helper()
		res, err := e.p.HandleAction(t.Context(), a.Do)
		if err != nil || !res.Hide || res.Toast == nil {
			t.Fatalf("%s: %+v %v", a.ID, res, err)
		}
		c, _ := e.h.Clip.Read()
		return c.Text
	}
	acts := items[0].Actions
	if acts[0].ID != "copy" || acts[1].ID != "copy-iso" || acts[1].Shortcut != "Ctrl+Enter" || acts[2].Shortcut != "Ctrl+Shift+P" ||
		acts[3].Do.Kind != plugin.ActionPush || acts[3].Do.Target != cmdWorld {
		t.Fatalf("actions = %+v", acts)
	}
	// The copied time is the time the action runs, not when the list was made.
	e.clock.advance(time.Minute)
	if got := run(acts[0]); got != "15:46 Kathmandu (UTC+5:45)" {
		t.Errorf("copy = %q", got)
	}
	if got := run(acts[1]); got != "2026-10-15T15:46:00+05:45" {
		t.Errorf("copy ISO = %q", got)
	}
	local := e.root(t, "time")[0]
	if got := run(local.Actions[1]); got != "2026-10-15T10:01:00Z" {
		t.Errorf("local ISO = %q", got)
	}
	if len(local.Actions) != 3 { // no favorite action
		t.Errorf("local actions = %+v", local.Actions)
	}
	e.setPref(prefHour24, false)
	if got := run(acts[0]); got != "3:46 PM Kathmandu (UTC+5:45)" {
		t.Errorf("12-hour copy = %q", got)
	}
	var pl copyPayload
	if err := json.Unmarshal(acts[0].Do.Payload, &pl); err != nil || pl.City != "kathmandu" {
		t.Errorf("payload = %s", acts[0].Do.Payload)
	}
}

func BenchmarkRootItems(b *testing.B) {
	for _, q := range []string{"time", "time b", "time bj", "time 北京", "time springfield", "time japan", "time utc+8", "time cst", "time sydny"} {
		b.Run(q, func(b *testing.B) {
			p := NewWithOptions(Options{After: func(time.Duration) <-chan time.Time { return nil }})
			h, err := hostfake.New(p, b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			if err := p.Init(b.Context(), h); err != nil {
				b.Fatal(err)
			}
			defer p.Close()
			for b.Loop() {
				if _, err := p.RootItems(b.Context(), plugin.Query{Text: q}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestSearchDuringLoadingInvalidates: a prefixed search that came in while
// the cities were still loading (and may have been dropped by the host's
// time limit) is asked for again once they are loaded.
func TestSearchDuringLoadingInvalidates(t *testing.T) {
	release := make(chan struct{})
	p := New()
	slow := p.index
	p.index = func() (*index, error) { <-release; return slow() }
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	go p.RootItems(t.Context(), plugin.Query{Text: "time"})
	eventually(t, "the search noticed loading", p.missed.Load)
	close(release)
	eventually(t, "invalidated after loading", func() bool { return len(h.Invalidated()) > 0 })
	if got := h.Invalidated()[0]; got != "" {
		t.Errorf("invalidated %q, want the root", got)
	}
}
