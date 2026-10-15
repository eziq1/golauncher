package calc

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
)

const (
	erAPIBody = `{"result":"success","provider":"https://www.exchangerate-api.com","time_last_update_unix":1792022551,` +
		`"base_code":"USD","rates":{"USD":1,"CNY":6.706494,"EUR":0.894425,"JPY":158.305497,"GBP":0.756165,"HKD":7.848465}}`
	frankfurterBody = `{"amount":1.0,"base":"USD","date":"2026-10-14","rates":{"CNY":6.6927,"EUR":0.89326,"JPY":158.45,"GBP":0.75377,"HKD":7.8481}}`
)

// rateServer is a fake of both rate providers.
type rateServer struct {
	*httptest.Server
	erHits, frHits atomic.Int32
	mu             sync.Mutex
	erStatus       int // 0: 200
	frStatus       int
	erBody, frBody string
	userAgent      string
	block          chan struct{} // if set, requests wait for it or cancellation
}

func newRateServer(t *testing.T) *rateServer {
	s := &rateServer{erBody: erAPIBody, frBody: frankfurterBody}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.userAgent = r.UserAgent()
		status, body, block := s.erStatus, s.erBody, s.block
		if strings.HasPrefix(r.URL.Path, "/fr") {
			s.frHits.Add(1)
			status, body = s.frStatus, s.frBody
		} else {
			s.erHits.Add(1)
		}
		s.mu.Unlock()
		if block != nil {
			select {
			case <-block:
			case <-r.Context().Done():
				return
			}
		}
		if status != 0 {
			http.Error(w, "down", status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *rateServer) set(fn func(s *rateServer)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

func (s *rateServer) options() Options {
	return Options{Client: s.Client(), ERAPIURL: s.URL + "/er/v6/latest/USD", FrankfurterURL: s.URL + "/fr/v1/latest?base=USD",
		UserAgent: "golauncher/test"}
}

// waitRefresh waits until no refresh is running.
func waitRefresh(t *testing.T, p *Plugin) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		p.ratesMu.Lock()
		busy := p.refreshing
		p.ratesMu.Unlock()
		if !busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("refresh did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRatesFirstRunOffline(t *testing.T) {
	s := newRateServer(t)
	s.set(func(s *rateServer) { s.erStatus, s.frStatus = 500, 503 })
	p, h := newPluginWith(t, s.options())
	items := root(t, p, "100 usd in cny")
	if len(items) != 1 || items[0].ID != "no-rates" || !strings.Contains(items[0].Subtitle, "Downloading") {
		t.Fatalf("first run = %+v", items)
	}
	waitRefresh(t, p)
	if s.erHits.Load() != 1 || s.frHits.Load() != 1 {
		t.Errorf("hits er %d fr %d", s.erHits.Load(), s.frHits.Load())
	}
	items = root(t, p, "100 usd")
	if len(items) != 1 || !strings.Contains(items[0].Subtitle, "Could not download") {
		t.Errorf("after failure = %+v", items)
	}
	if got := h.Invalidated(); len(got) != 1 {
		t.Errorf("invalidated %q", got)
	}
	// No new attempt within retryAfter, one after it.
	waitRefresh(t, p)
	if s.erHits.Load() != 1 {
		t.Errorf("retried at once: %d hits", s.erHits.Load())
	}
	s.set(func(s *rateServer) { s.erStatus = 0 })
	now := hostfake.Fixed.Add(retryAfter + time.Second)
	h.Clock = func() time.Time { return now }
	root(t, p, "100 usd")
	waitRefresh(t, p)
	if got := titles(root(t, p, "100 usd in cny")); len(got) != 1 || got[0] != "670.65 CNY" {
		t.Errorf("after retry = %q", got)
	}
	// Only currency queries go online.
	hits := s.erHits.Load()
	root(t, p, "10 km in mi")
	if s.erHits.Load() != hits {
		t.Error("a unit conversion fetched rates")
	}
}

func TestRatesRefreshAndCache(t *testing.T) {
	s := newRateServer(t)
	o := s.options()
	o.Location = time.UTC
	dir := t.TempDir()
	p := NewWithOptions(o)
	h, err := hostfake.New(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	root(t, p, "1 usd")
	waitRefresh(t, p)
	if s.userAgent != "golauncher/test" {
		t.Errorf("User-Agent %q", s.userAgent)
	}
	it := root(t, p, "100 usd in cny")[0]
	if it.Title != "670.65 CNY" || it.Subtitle != "100 USD = 670.65 CNY · rates from 2026-10-15" {
		t.Errorf("item = %q / %q", it.Title, it.Subtitle)
	}
	if !strings.Contains(it.Detail.Markdown, "[Rates by Exchange Rate API](https://www.exchangerate-api.com)") {
		t.Errorf("attribution = %q", it.Detail.Markdown)
	}
	if len(h.Invalidated()) != 1 {
		t.Errorf("invalidated %q", h.Invalidated())
	}
	p.Close()
	data, err := os.ReadFile(filepath.Join(dir, "cache", ratesFile))
	if err != nil {
		t.Fatal(err)
	}
	var cached Rates
	if err := json.Unmarshal(data, &cached); err != nil || cached.Provider != providerERAPI || !cached.Fetched.Equal(hostfake.Fixed) {
		t.Errorf("cache = %+v, %v", cached, err)
	}
	// A new instance uses the fresh cache without going online.
	p2 := NewWithOptions(o)
	h2, _ := hostfake.New(p2, dir)
	if err := p2.Init(t.Context(), h2); err != nil {
		t.Fatal(err)
	}
	defer p2.Close()
	if got := titles(root(t, p2, "100 usd in eur")); len(got) != 1 || got[0] != "89.44 EUR" {
		t.Errorf("from cache = %q", got)
	}
	waitRefresh(t, p2)
	if s.erHits.Load() != 1 {
		t.Errorf("fresh cache refetched: %d hits", s.erHits.Load())
	}
	// Stale (older than 6 h): shown at once, refreshed in the background.
	s.set(func(s *rateServer) { s.erBody = strings.Replace(erAPIBody, `"CNY":6.706494`, `"CNY":7`, 1) })
	later := hostfake.Fixed.Add(rateMaxAge + time.Minute)
	h2.Clock = func() time.Time { return later }
	if got := titles(root(t, p2, "100 usd in cny")); got[0] != "670.65 CNY" {
		t.Errorf("stale = %q", got)
	}
	waitRefresh(t, p2)
	if got := titles(root(t, p2, "100 usd in cny")); got[0] != "700.00 CNY" {
		t.Errorf("refreshed = %q", got)
	}
}

func TestRatesFailureKeepsStaleCache(t *testing.T) {
	s := newRateServer(t)
	s.set(func(s *rateServer) { s.erStatus, s.frStatus = 500, 500 })
	p, h := newPluginWith(t, s.options())
	stale := &Rates{Provider: providerERAPI, Updated: hostfake.Fixed.Add(-48 * time.Hour), Fetched: hostfake.Fixed.Add(-47 * time.Hour),
		Rates: map[string]float64{"USD": 1, "CNY": 7}}
	data, _ := json.Marshal(stale)
	if err := os.WriteFile(filepath.Join(h.CacheDir(), ratesFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
	root(t, p, "1 usd in cny")
	waitRefresh(t, p)
	it := root(t, p, "1 usd in cny")[0]
	if it.Title != "7.00 CNY" || !strings.Contains(it.Subtitle, "rates from 2026-10-12") {
		t.Errorf("stale after failure = %q / %q", it.Title, it.Subtitle)
	}
	if !p.refreshFailed() {
		t.Error("failure not recorded")
	}
}

func TestRatesFallbackProvider(t *testing.T) {
	s := newRateServer(t)
	s.set(func(s *rateServer) { s.erStatus = 500 })
	p, _ := newPluginWith(t, s.options())
	root(t, p, "1 usd")
	waitRefresh(t, p)
	it := root(t, p, "100 usd in cny")[0]
	if it.Title != "669.27 CNY" || !strings.Contains(it.Subtitle, "rates from 2026-10-14") || !strings.Contains(it.Detail.Markdown, "Frankfurter") {
		t.Errorf("fallback = %q / %q / %q", it.Title, it.Subtitle, it.Detail.Markdown)
	}
	// Frankfurter has no rate for this currency.
	if items := root(t, p, "1 usd in krw"); len(items) != 0 {
		t.Errorf("krw = %q", titles(items))
	}
}

func TestRatesPreferredProvider(t *testing.T) {
	s := newRateServer(t)
	p, h := newPluginWith(t, s.options())
	setPref(h, prefProvider, `"frankfurter"`)
	root(t, p, "1 usd")
	waitRefresh(t, p)
	if s.frHits.Load() != 1 || s.erHits.Load() != 0 {
		t.Errorf("hits er %d fr %d", s.erHits.Load(), s.frHits.Load())
	}
	// Off: no currencies, nothing fetched.
	s2 := newRateServer(t)
	p2, h2 := newPluginWith(t, s2.options())
	setPref(h2, prefProvider, `"off"`)
	if items := root(t, p2, "1 usd"); len(items) != 0 {
		t.Errorf("off = %q", titles(items))
	}
	p2.maybeRefresh(providerOff)
	if s2.erHits.Load()+s2.frHits.Load() != 0 {
		t.Error("fetched with currencies off")
	}
}

func TestRatesBadResponses(t *testing.T) {
	for name, set := range map[string]func(s *rateServer){
		"not json": func(s *rateServer) { s.erBody, s.frBody = "<html>", "<html>" },
		"bad json": func(s *rateServer) { s.erBody, s.frBody = `{"result":`, `{"base":` },
		"error result": func(s *rateServer) {
			s.erBody, s.frBody = `{"result":"error","error-type":"x"}`, `{"base":"EUR","date":"x","rates":{}}`
		},
		"bad date": func(s *rateServer) { s.erStatus, s.frBody = 404, `{"base":"USD","date":"x","rates":{"EUR":1}}` },
	} {
		t.Run(name, func(t *testing.T) {
			s := newRateServer(t)
			s.set(set)
			p, _ := newPluginWith(t, s.options())
			root(t, p, "1 usd")
			waitRefresh(t, p)
			if items := root(t, p, "1 usd"); len(items) != 1 || items[0].ID != "no-rates" {
				t.Errorf("items = %+v", items)
			}
		})
	}
}

func TestRatesBadCacheIgnored(t *testing.T) {
	s := newRateServer(t)
	s.set(func(s *rateServer) { s.erStatus, s.frStatus = 500, 500 })
	p, h := newPluginWith(t, s.options())
	if err := os.WriteFile(filepath.Join(h.CacheDir(), ratesFile), []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	if items := root(t, p, "1 usd"); len(items) != 1 || items[0].ID != "no-rates" {
		t.Errorf("items = %+v", items)
	}
}

// TestCloseCancelsRefresh: Close does not wait for a hanging download.
func TestCloseCancelsRefresh(t *testing.T) {
	s := newRateServer(t)
	s.set(func(s *rateServer) { s.block = make(chan struct{}) })
	p := NewWithOptions(s.options())
	h, _ := hostfake.New(p, t.TempDir())
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	root(t, p, "1 usd")
	done := make(chan struct{})
	h.Go(func() {
		p.Close()
		close(done)
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hangs on a download")
	}
	p.maybeRefresh(providerERAPI) // closed: no new refresh
	if p.refreshing {
		t.Error("refresh after Close")
	}
}

func TestFixtureRates(t *testing.T) {
	r, err := FixtureRates(filepath.Join("..", "..", "testdata", "fixtures", "rates.json"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Provider != providerERAPI || r.Rates["CNY"] != 6.706494 || !r.Fetched.Equal(r.Updated) {
		t.Errorf("rates = %+v", r)
	}
	if _, err := FixtureRates("missing.json"); err == nil {
		t.Error("missing fixture")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte(`{"result":"error"}`), 0o644)
	if _, err := FixtureRates(bad); err == nil {
		t.Error("bad fixture")
	}
	// Fixed rates are never refreshed; offline plugins never go online.
	p, _ := newPluginWith(t, Options{Rates: r})
	if got := titles(root(t, p, "1 usd in cny")); got[0] != "6.71 CNY" {
		t.Errorf("fixture rates: %q", got)
	}
	p2, _ := newPluginWith(t, Options{Offline: true})
	if items := root(t, p2, "1 usd"); len(items) != 1 || !strings.Contains(items[0].Subtitle, "Could not download") {
		t.Errorf("offline = %+v", items)
	}
}

// TestRealProviders downloads from the real providers; it runs only with
// GOLAUNCHER_NETWORK_TESTS=1.
func TestRealProviders(t *testing.T) {
	if os.Getenv("GOLAUNCHER_NETWORK_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_NETWORK_TESTS=1 to download real exchange rates")
	}
	p, _ := newPluginWith(t, Options{UserAgent: "golauncher/test"})
	for _, provider := range []string{providerERAPI, providerFrankfurter} {
		url, parse := DefaultERAPIURL, parseERAPI
		if provider == providerFrankfurter {
			url, parse = DefaultFrankfurterURL, parseFrankfurter
		}
		data, err := p.get(t.Context(), url)
		if err != nil {
			t.Fatalf("%s: %v", provider, err)
		}
		r, err := parse(data)
		if err != nil {
			t.Fatalf("%s: %v", provider, err)
		}
		if len(r.Rates) < 25 || r.Rates["EUR"] <= 0 || r.Rates["CNY"] <= 0 || time.Since(r.Updated) > 7*24*time.Hour {
			t.Errorf("%s: %d rates, EUR %v, updated %v", provider, len(r.Rates), r.Rates["EUR"], r.Updated)
		}
		t.Logf("%s: %d rates, updated %v, 1 USD = %v CNY", provider, len(r.Rates), r.Updated, r.Rates["CNY"])
	}
}
