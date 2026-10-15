package network

import (
	"context"
	"encoding/json/v2"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

const (
	// dnsCacheTTL is how long the records of a lookup are shown again
	// without asking the server (when the view is pulled again).
	dnsCacheTTL   = time.Minute
	dnsCacheLimit = 32
)

var (
	forwardTypes = []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS"}
	reverseTypes = []string{"PTR"}
)

// dnsGroup is the outcome of the lookup of one record type.
type dnsGroup struct {
	typ  string
	recs []Record
	err  error
}

type dnsEntry struct {
	at     time.Time
	groups []dnsGroup
}

// dnsCache keeps recent lookups by query.
type dnsCache struct {
	mu sync.Mutex
	m  map[string]dnsEntry
}

func (c *dnsCache) get(key string, now time.Time) ([]dnsGroup, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || now.Sub(e.at) > dnsCacheTTL {
		return nil, false
	}
	return e.groups, true
}

func (c *dnsCache) put(key string, now time.Time, groups []dnsGroup) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= dnsCacheLimit {
		c.m = map[string]dnsEntry{}
	}
	c.m[key] = dnsEntry{now, groups}
}

// lookupAll looks up every record type of q in parallel.
func (p *Plugin) lookupAll(ctx context.Context, q dnsQuery) []dnsGroup {
	if g, ok := p.dns.get(q.String(), p.h.Now()); ok {
		return g
	}
	types := forwardTypes
	if q.Reverse {
		types = reverseTypes
	}
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	groups := make([]dnsGroup, len(types))
	var wg sync.WaitGroup
	for i, typ := range types {
		wg.Add(1)
		p.h.Go(func() {
			defer wg.Done()
			recs, err := p.b.LookupDNS(ctx, q.Name, typ, q.Server)
			groups[i] = dnsGroup{typ: typ, recs: recs, err: err}
		})
	}
	wg.Wait()
	// Failures (timeouts, a cancelled pull) are not kept: the next pull
	// asks again.
	for _, g := range groups {
		if g.err != nil && !errors.Is(g.err, errNotFound) {
			return groups
		}
	}
	p.dns.put(q.String(), p.h.Now(), groups)
	return groups
}

// dnsRoot is the root search for the dns prefix.
func (p *Plugin) dnsRoot(rest string) []plugin.Item {
	l := p.h.I18n()
	if rest == "" {
		return []plugin.Item{p.hint("dns-hint", l.T("hint.dns.title"), l.T("hint.dns.subtitle"))}
	}
	q, prob := parseDNSQuery(rest)
	if prob != nil {
		return []plugin.Item{p.problemItem("dns-invalid", prob)}
	}
	title := l.T("dns.item", "name", q.Name)
	if q.Reverse {
		title = l.T("dns.itemReverse", "address", q.Name)
	}
	sub := l.T("dns.viaSystem")
	if q.Server != "" {
		sub = l.T("dns.viaServer", "server", q.serverName())
	}
	return []plugin.Item{{
		ID: "dns:" + q.String(), Title: title, Subtitle: sub, Icon: plugin.Icon{Name: "globe"},
		Actions: []plugin.Action{push("lookup", l.T("action.lookup"), toolDNS, q, "")},
	}}
}

// dnsView looks up the records of the query in the payload (or in the
// command's argument) and lists them by type.
func (p *Plugin) dnsView(ctx context.Context, req plugin.ViewRequest) (plugin.View, error) {
	l := p.h.I18n()
	v := plugin.View{Kind: plugin.ViewList, Title: l.T("view.dns.titleEmpty"), Placeholder: l.T("view.dns.placeholder"), FilterLocally: true}
	var q dnsQuery
	var prob *problem
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &q); err != nil {
			return plugin.View{}, err
		}
		q, prob = parseDNSQuery(q.String())
	} else {
		q, prob = parseDNSQuery(req.Arguments["query"])
	}
	if prob != nil {
		v.Sections = []plugin.Section{{Items: []plugin.Item{p.problemItem("dns-invalid", prob)}}}
		return v, nil
	}
	v.Title = l.T("view.dns.title", "query", q.String())
	groups := p.lookupAll(ctx, q)
	if err := ctx.Err(); err != nil {
		return plugin.View{}, err
	}
	var all []string
	for _, g := range groups {
		for _, r := range g.recs {
			all = append(all, r.Type+"\t"+r.Value)
		}
	}
	copyAll := copyAction("copy-all", l.T("action.copyAllRecords"), strings.Join(all, "\n"), "Ctrl+Shift+C")
	found := false
	for _, g := range groups {
		s := plugin.Section{Title: g.typ}
		switch {
		case g.err != nil && !errors.Is(g.err, errNotFound):
			s.Items = append(s.Items, plugin.Item{ID: g.typ + ":error", Title: l.T("dns.failed", "type", g.typ),
				Subtitle: g.err.Error(), Icon: plugin.Icon{Name: "alert", Tint: plugin.TintDanger}})
		default:
			for _, r := range g.recs {
				it := plugin.Item{ID: r.Type + ":" + r.Value, Title: r.Value, Accessory: r.Type, Icon: plugin.Icon{Name: "globe"},
					Actions: []plugin.Action{copyAction("copy", l.T("action.copyValue"), r.Value, ""), copyAll}}
				if r.Type == "MX" {
					it.Subtitle = l.T("dns.mxPref", "pref", r.Pref)
				}
				if r.Type == "A" || r.Type == "AAAA" {
					it.Actions = append(it.Actions, push("ping", l.T("action.ping"), toolPing, hostPayload{Host: r.Value}, "Ctrl+P"))
				}
				s.Items = append(s.Items, it)
				found = true
			}
		}
		if len(s.Items) > 0 {
			v.Sections = append(v.Sections, s)
		}
	}
	if !found && len(v.Sections) == 0 {
		v.Sections = []plugin.Section{{Items: []plugin.Item{{ID: "none", Title: l.T("dns.noRecords", "name", q.Name),
			Subtitle: l.T("dns.noRecordsSubtitle"), Icon: plugin.Icon{Name: "info", Tint: plugin.TintMuted}}}}}
	}
	return v, nil
}
