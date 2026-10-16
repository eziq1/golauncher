package quicklinks

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// suggestServer answers OpenSearch suggestions for every engine: the
// client's transport sends all requests to it, so nothing goes online.
type suggestServer struct {
	srv   *httptest.Server
	mu    sync.Mutex
	reqs  []*url.URL
	delay time.Duration
	body  func(q string) []byte
}

func newSuggestServer(t *testing.T) *suggestServer {
	s := &suggestServer{}
	s.body = func(q string) []byte {
		return fmt.Appendf(nil, `[%q, [%q, %q, %q]]`, q, q, q+" tutorial", q+" examples")
	}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.reqs = append(s.reqs, r.URL)
		delay, body := s.delay, s.body
		s.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		q := r.URL.Query()
		text := q.Get("q") + q.Get("query") + q.Get("wd") + q.Get("search")
		w.Write(body(text))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// client sends every request to the test server, keeping the path and query.
func (s *suggestServer) client() *http.Client {
	target, _ := url.Parse(s.srv.URL)
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host, r.Host = target.Scheme, target.Host, target.Host
		return http.DefaultTransport.RoundTrip(r)
	})}
}

func (s *suggestServer) requests() []*url.URL {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reqs)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func titles(items []plugin.Item, prefix string) []string {
	var out []string
	for _, it := range items {
		if strings.HasPrefix(it.ID, prefix) {
			out = append(out, it.Title)
		}
	}
	return out
}

func TestSuggestionsOffByDefault(t *testing.T) {
	s := newSuggestServer(t)
	e := newEnv(t, Options{Client: s.client(), Debounce: time.Millisecond})
	e.root("g golang")
	e.p.sug.wg.Wait()
	time.Sleep(20 * time.Millisecond)
	if n := len(s.requests()); n != 0 {
		t.Fatalf("%d requests with suggestions off", n)
	}
}

func TestSuggestions(t *testing.T) {
	s := newSuggestServer(t)
	e := newEnv(t, Options{Client: s.client(), Debounce: time.Millisecond})
	e.h.Prefs.Set(prefSuggest, plugin.MustPayload(true))
	// Text without a keyword, an engine without suggestions, a fallback:
	// nothing is sent.
	e.root("golang generics")
	e.root("gh golang")
	e.fallback("golang generics")
	e.p.sug.wg.Wait()
	if n := len(s.requests()); n != 0 {
		t.Fatalf("%d requests without a keyword", n)
	}
	if got := titles(e.root("g golang"), "suggest:"); len(got) != 0 {
		t.Fatalf("suggestions before the answer: %v", got)
	}
	e.p.sug.wg.Wait()
	reqs := s.requests()
	if len(reqs) != 1 || reqs[0].Path != "/complete/search" || reqs[0].Query().Get("q") != "golang" {
		t.Fatalf("requests %v", reqs)
	}
	if !slices.Contains(e.h.Invalidated(), "") {
		t.Error("no Invalidate after the answer")
	}
	items := e.root("g golang")
	// The query itself is left out; the search item comes first.
	if got := titles(items, "suggest:"); !slices.Equal(got, []string{"golang tutorial", "golang examples"}) {
		t.Fatalf("suggestions %v", got)
	}
	it := find(t, items, "suggest:golang tutorial")
	if it.Score >= 1 || !it.Transient || it.Actions[0].Do.Target != "https://www.google.com/search?q=golang%20tutorial" || it.Subtitle != "Search Google" {
		t.Errorf("suggestion %+v", it)
	}
	e.p.sug.wg.Wait()
	if n := len(s.requests()); n != 1 {
		t.Errorf("cached answer asked again: %d requests", n)
	}
	// Chinese, with a space, through Baidu (GBK answer).
	s.mu.Lock()
	s.body = func(q string) []byte {
		b, _ := simplifiedchinese.GBK.NewEncoder().Bytes(fmt.Appendf(nil, `[%q, [%q]]`, q, q+"预报"))
		return b
	}
	s.mu.Unlock()
	e.root("bd 北京 天气")
	e.p.sug.wg.Wait()
	reqs = s.requests()
	if last := reqs[len(reqs)-1]; last.Query().Get("wd") != "北京 天气" || !strings.Contains(last.RawQuery, "wd=%E5%8C%97%E4%BA%AC%20%E5%A4%A9%E6%B0%94") {
		t.Errorf("baidu request %v", last)
	}
	if got := titles(e.root("bd 北京 天气"), "suggest:"); !slices.Equal(got, []string{"北京 天气预报"}) {
		t.Errorf("baidu suggestions %v", got)
	}
}

// fakeAfter hands out debounce timers that fire only when released.
type fakeAfter struct {
	mu    sync.Mutex
	chans []chan time.Time
}

func (f *fakeAfter) after(time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := make(chan time.Time, 1)
	f.chans = append(f.chans, c)
	return c
}

func (f *fakeAfter) fireAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.chans {
		c <- time.Time{}
	}
	f.chans = nil
}

func TestSuggestionsDebounceAndCancel(t *testing.T) {
	s := newSuggestServer(t)
	fa := &fakeAfter{}
	e := newEnv(t, Options{Client: s.client(), After: fa.after})
	e.h.Prefs.Set(prefSuggest, plugin.MustPayload(true))
	// Typing quickly: each keystroke cancels the previous wait; only the
	// last text is asked for.
	for _, q := range []string{"g g", "g go", "g gol", "g gola"} {
		e.root(q)
	}
	fa.fireAll()
	e.p.sug.wg.Wait()
	reqs := s.requests()
	if len(reqs) != 1 || reqs[0].Query().Get("q") != "gola" {
		t.Fatalf("requests %v", reqs)
	}
	// Text without a keyword cancels the pending request.
	e.root("g golan")
	e.root("golang")
	fa.fireAll()
	e.p.sug.wg.Wait()
	if n := len(s.requests()); n != 1 {
		t.Errorf("%d requests after leaving the keyword", n)
	}
	// Hiding the window cancels too.
	e.root("g golang x")
	e.h.Bus.Publish(plugin.EventWindowHidden)
	e.p.sug.wg.Wait() // returns once the wait was cancelled
	fa.fireAll()
	if n := len(s.requests()); n != 1 {
		t.Errorf("%d requests after hiding", n)
	}
}

func TestSuggestionsTimeout(t *testing.T) {
	s := newSuggestServer(t)
	// Wide margins: on a loaded machine the request may take a while to
	// reach the server, and the timeout must still end well before the
	// server's answer.
	s.delay = 5 * time.Second
	e := newEnv(t, Options{Client: s.client(), Debounce: time.Millisecond, SuggestTimeout: 200 * time.Millisecond})
	e.h.Prefs.Set(prefSuggest, plugin.MustPayload(true))
	start := time.Now()
	e.root("g slow")
	e.p.sug.wg.Wait()
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("timeout took %v", d)
	}
	if len(s.requests()) != 1 || len(titles(e.root("g slow"), "suggest:")) != 0 || slices.Contains(e.h.Invalidated(), "") {
		t.Error("a timed-out request gave suggestions")
	}
	// It is asked again later (failures are not cached).
	e.p.sug.wg.Wait()
	if len(s.requests()) != 2 {
		t.Errorf("requests %d", len(s.requests()))
	}
}

func TestParseSuggestions(t *testing.T) {
	for body, want := range map[string][]string{
		`["q", ["a", "b", "a", " "]]`:                       {"a", "b"},
		"\xef\xbb\xbf[\"q\", [\"x\"], [\"desc\"], [\"u\"]]": {"x"},
	} {
		got, err := parseSuggestions([]byte(body))
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("parseSuggestions(%q) = %v, %v", body, got, err)
		}
	}
	for _, bad := range []string{``, `{}`, `["q"]`, `["q", "x"]`} {
		if _, err := parseSuggestions([]byte(bad)); err == nil {
			t.Errorf("parseSuggestions(%q) succeeded", bad)
		}
	}
}

// TestRealSuggestions asks Google for real; it runs only with
// GOLAUNCHER_NET_TESTS=1.
func TestRealSuggestions(t *testing.T) {
	if os.Getenv("GOLAUNCHER_NET_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_NET_TESTS=1 to go online")
	}
	res, err := fetchSuggestions(t.Context(), &http.Client{Timeout: 5 * time.Second}, expand(suggestGoogle, true, values{query: "golang"}))
	if err != nil || len(res) == 0 {
		t.Fatalf("%v %v", res, err)
	}
}
