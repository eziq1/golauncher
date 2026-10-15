package everything

import (
	"context"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/eziq1/golauncher/internal/winapi/everything"
)

const (
	// poolMax is how many results a query asks for: re-ranking needs more
	// than the page shown, and a large Max costs Everything nothing extra
	// (it scans the whole index for every query anyway).
	poolMax = limitMax
	// rootPool is the pool of the root search, which shows rootMax files.
	rootPool = 200
	// shortRunes: queries shorter than this match so much that excluding
	// paths makes Everything several times slower (100-270 ms instead of
	// 25-35 ms); they get a single plain query, ranked locally.
	shortRunes = 3
)

// predictor remembers whether the previous query was narrow (fewer matches
// outside the system tier than the page size). Typing on narrows a query
// further, so the next query is likely narrow too and one plain query then
// returns all its matches; otherwise query A comes first. Either way the
// results are the same; only the number of round trips differs.
type predictor struct {
	mu     sync.Mutex
	last   string
	narrow bool
}

func (p *predictor) narrowFor(text string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.narrow && p.last != "" && strings.HasPrefix(text, p.last)
}

func (p *predictor) set(text string, narrow bool) {
	p.mu.Lock()
	p.last, p.narrow = text, narrow
	p.mu.Unlock()
}

// searchOptions are the preferences that shape a search.
type searchOptions struct {
	max        int  // results shown
	matchPath  bool // matchPath preference
	hideSystem bool // hideSystemFiles preference
	rules      *rules
}

// options reads the preferences.
func (p *Plugin) options(n int) searchOptions {
	prefs := p.h.Preferences()
	return searchOptions{
		max:        n,
		matchPath:  prefs.Bool(prefMatchPath),
		hideSystem: prefs.Bool(prefHideSystem),
		rules:      p.base.withExtra(prefs.Strings(prefExtraLowPriority)),
	}
}

func (p *Plugin) query(ctx context.Context, text string, n int, o searchOptions) ([]everything.Result, error) {
	res, err := p.b.Search(ctx, everything.Query{Text: text, Max: n, MatchPath: o.matchPath})
	if len(res) > n {
		res = res[:n]
	}
	return res, err
}

// fetch runs the queries of docs/everything.md §2.2 and returns the ranked
// results, best first, at most o.max:
//
//   - query A: the text without the system tier (` !"c:\windows\" …`);
//   - query B, only when A found fewer than o.max and system files are not
//     hidden: the text within the system tier, to fill up the page.
//
// A short query, or one predicted narrow, first runs the plain text: when
// that returns fewer than poolMax results it has every match and A and B are
// not needed.
func (p *Plugin) fetch(ctx context.Context, text string, o searchOptions) ([]ranked, error) {
	short := utf8.RuneCountInString(text) < shortRunes
	var plain []everything.Result
	if short || p.pred.narrowFor(text) {
		res, err := p.query(ctx, text, poolMax, o)
		if err != nil {
			return nil, err
		}
		if short || len(res) < poolMax {
			p.pred.set(text, len(res) < poolMax)
			return p.rank(o, res, text), nil
		}
		plain = res // too many: keep them to fill up after A
	}
	a, err := p.query(ctx, searchText(text, o.rules.exclusions()), poolMax, o)
	if err != nil {
		return nil, err
	}
	narrow := len(a) < o.max
	p.pred.set(text, narrow)
	res := merge(a, plain)
	if narrow && plain == nil && !o.hideSystem {
		b, err := p.query(ctx, searchText(text, o.rules.inclusion()), o.max-len(a), o)
		if err != nil {
			return nil, err
		}
		res = merge(res, b)
	}
	return p.rank(o, res, text), nil
}

func (p *Plugin) rank(o searchOptions, res []everything.Result, text string) []ranked {
	out := rank(o.rules, res, text, p.h.Now(), o.hideSystem)
	return out[:min(len(out), o.max)]
}

// merge appends the results of b not in a (by path, ignoring case).
func merge(a, b []everything.Result) []everything.Result {
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]bool, len(a))
	for _, r := range a {
		seen[strings.ToLower(r.Path)] = true
	}
	out := a[:len(a):len(a)]
	for _, r := range b {
		if k := strings.ToLower(r.Path); !seen[k] {
			seen[k] = true
			out = append(out, r)
		}
	}
	return out
}

// fetchRoot returns the files for the root search: query A only, keeping
// the user and normal tiers.
func (p *Plugin) fetchRoot(ctx context.Context, text string, o searchOptions) ([]ranked, error) {
	res, err := p.query(ctx, searchText(text, o.rules.exclusions()), rootPool, o)
	if err != nil {
		return nil, err
	}
	o.max = rootPool
	var out []ranked
	for _, r := range p.rank(o, res, text) {
		if r.tier >= tierNormal && len(out) < rootMax {
			out = append(out, r)
		}
	}
	return out, nil
}
