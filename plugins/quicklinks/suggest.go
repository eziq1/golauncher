package quicklinks

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

const (
	// maxSuggestions is how many suggestions are listed.
	maxSuggestions = 5
	// cacheSize is how many answered queries are kept.
	cacheSize = 32
	// maxSuggestBody is the most read from a suggestion response.
	maxSuggestBody = 64 << 10
)

// suggester fetches search suggestions in the background (docs/quicklinks.md
// §7): debounced, one request at a time (a newer query cancels the older
// one), with a timeout, cached; when an answer arrives the root search is
// pulled again.
type suggester struct {
	p *Plugin

	mu      sync.Mutex
	cache   map[string][]string
	order   []string // cache keys, oldest first
	pending string   // the key being fetched
	cancel  context.CancelFunc
	closed  bool
	wg      sync.WaitGroup
}

func newSuggester(p *Plugin) *suggester {
	return &suggester{p: p, cache: map[string][]string{}}
}

func suggestKey(e Engine, query string) string { return e.ID + "\x00" + e.Suggest + "\x00" + query }

// get returns the cached suggestions for query, or asks for them and
// returns false.
func (s *suggester) get(e Engine, query string) ([]string, bool) {
	key := suggestKey(e, query)
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.cache[key]; ok {
		return r, true
	}
	if s.closed || s.pending == key {
		return nil, false
	}
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.pending, s.cancel = key, cancel
	// The debounce timer starts now, not when the goroutine runs.
	wait := s.p.opts.After(s.p.opts.Debounce)
	s.wg.Add(1)
	s.p.h.Go(func() {
		defer s.wg.Done()
		defer cancel()
		s.fetch(ctx, wait, key, e, query)
	})
	return nil, false
}

// fetch waits for the debounce delay, then asks the engine.
func (s *suggester) fetch(ctx context.Context, wait <-chan time.Time, key string, e Engine, query string) {
	o := s.p.opts
	select {
	case <-wait:
	case <-ctx.Done():
		return
	}
	if ctx.Err() != nil { // cancelled while the timer fired
		return
	}
	tctx, cancel := context.WithTimeout(ctx, o.SuggestTimeout)
	res, err := fetchSuggestions(tctx, o.Client, expand(e.Suggest, true, values{query: query}))
	cancel()
	s.mu.Lock()
	if s.pending == key {
		s.pending, s.cancel = "", nil
	}
	if err != nil || ctx.Err() != nil || s.closed {
		s.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			s.p.h.Logger().Info("quicklinks: suggestions", "engine", e.ID, "err", err)
		}
		return
	}
	res = slices.DeleteFunc(res, func(r string) bool { return strings.EqualFold(strings.TrimSpace(r), query) })
	if len(res) > maxSuggestions {
		res = res[:maxSuggestions]
	}
	if _, ok := s.cache[key]; !ok {
		s.order = append(s.order, key)
	}
	s.cache[key] = res
	if len(s.order) > cacheSize {
		delete(s.cache, s.order[0])
		s.order = s.order[1:]
	}
	s.mu.Unlock()
	s.p.h.Invalidate("")
}

// stop cancels the request being made (the window was hidden, or the text
// no longer asks for suggestions).
func (s *suggester) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.pending, s.cancel = "", nil
	}
}

// close stops for good and waits for the request to end.
func (s *suggester) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.stop()
	s.wg.Wait()
}

// reopen undoes close: the plugin is initialized again after Close.
func (s *suggester) reopen() {
	s.mu.Lock()
	s.closed = false
	s.mu.Unlock()
}

// fetchSuggestions reads an OpenSearch suggestions response:
// ["query", ["suggestion", …], …]. A body that is not UTF-8 is decoded
// from GBK (Baidu).
func fetchSuggestions(ctx context.Context, c *http.Client, u string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, application/x-suggestions+json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSuggestBody))
	if err != nil {
		return nil, err
	}
	return parseSuggestions(body)
}

// parseSuggestions parses an OpenSearch suggestions body.
func parseSuggestions(body []byte) ([]string, error) {
	if !utf8.Valid(body) {
		dec, err := simplifiedchinese.GBK.NewDecoder().Bytes(body)
		if err != nil {
			return nil, err
		}
		body = dec
	}
	body = bytes.TrimSpace(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")))
	var top []jsontext.Value
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, err
	}
	if len(top) < 2 {
		return nil, fmt.Errorf("not an OpenSearch suggestions response")
	}
	var list []string
	if err := json.Unmarshal(top[1], &list); err != nil {
		return nil, err
	}
	var out []string
	for _, s := range list {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out, nil
}

// offlineTransport fails every request: the client of the fake
// environment, which must never go online.
type offlineTransport struct{}

func (offlineTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("quicklinks: offline")
}

// defaultAfter waits with a timer.
func defaultAfter(d time.Duration) <-chan time.Time { return time.After(d) }
