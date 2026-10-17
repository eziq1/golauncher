package network

import (
	"cmp"
	"context"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	winnet "github.com/eziq1/golauncher/internal/winapi/network"
	"github.com/eziq1/golauncher/pkg/plugin"
)

const (
	// publicTTL is how long a public address is kept.
	publicTTL = 5 * time.Minute
	// publicErrTTL is how long a failed lookup is kept before typing "ip"
	// tries again.
	publicErrTTL = 30 * time.Second
	// publicTimeout bounds the lookup of one address family.
	publicTimeout = 15 * time.Second
)

// publicCache holds the public addresses. They are looked up only when the
// user types the ip prefix or opens the view, never periodically.
type publicCache struct {
	mu      sync.Mutex
	v4, v6  netip.Addr
	err     error     // the IPv4 lookup failed
	at      time.Time // when the last lookup finished
	loading bool      // a lookup is running
	v6Done  bool      // the IPv6 lookup of the running lookup finished
}

type publicState struct {
	v4, v6  netip.Addr
	err     error
	at      time.Time
	loading bool
	v6Done  bool
}

func (c *publicCache) snapshot() publicState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return publicState{c.v4, c.v6, c.err, c.at, c.loading, c.v6Done || !c.loading}
}

// reset forgets the addresses so that the next lookup runs at once.
func (c *publicCache) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loading {
		c.at = time.Time{}
	}
}

// refreshPublic starts a lookup of the public addresses unless one runs or
// the cached result is fresh (or force). With Options.Sync it runs inline.
func (p *Plugin) refreshPublic(force bool) {
	c := &p.public
	now := p.h.Now()
	c.mu.Lock()
	ttl := publicTTL
	if c.err != nil {
		ttl = publicErrTTL
	}
	if c.loading || (!force && !c.at.IsZero() && now.Sub(c.at) < ttl) {
		c.mu.Unlock()
		return
	}
	c.loading, c.v6Done = true, false
	c.mu.Unlock()
	fetch := func() {
		ctx, cancel := context.WithTimeout(p.bg, publicTimeout)
		v4, err := p.b.PublicIP(ctx, false)
		cancel()
		c.mu.Lock()
		c.v4, c.err = v4, err
		c.mu.Unlock()
		if err != nil {
			p.h.Logger().Info("network: public IPv4 lookup failed", "err", err)
		}
		if !p.opts.Sync {
			p.h.Invalidate("")
		}
		ctx, cancel = context.WithTimeout(p.bg, publicTimeout)
		v6, err := p.b.PublicIP(ctx, true)
		cancel()
		if err != nil {
			v6 = netip.Addr{}
		}
		c.mu.Lock()
		c.v6, c.at, c.loading, c.v6Done = v6, p.h.Now(), false, true
		c.mu.Unlock()
		if !p.opts.Sync {
			p.h.Invalidate("")
		}
	}
	if p.opts.Sync {
		fetch()
		return
	}
	p.goBackground(fetch)
}

// publicItem is the item of the public addresses.
func (p *Plugin) publicItem(root bool) plugin.Item {
	l := p.h.I18n()
	s := p.public.snapshot()
	it := plugin.Item{ID: "public", Title: l.T("public.title"), Icon: plugin.Icon{Name: "globe"}, Accessory: l.T("public.accessory")}
	switch {
	case s.v4.IsValid() || s.v6.IsValid():
		main := cmp.Or(s.v4, s.v6)
		it.Title = l.T("public.titleValue", "address", main.String())
		switch {
		case s.v4.IsValid() && s.v6.IsValid():
			it.Subtitle = l.T("public.ipv6", "address", s.v6.String())
		case !s.v6Done:
			it.Subtitle = l.T("public.loadingIPv6")
		case s.v4.IsValid():
			it.Subtitle = l.T("public.noIPv6")
		}
		it.Actions = append(it.Actions, copyAction("copy", l.T("action.copyAddress"), main.String(), ""))
		if s.v4.IsValid() && s.v6.IsValid() {
			it.Actions = append(it.Actions, copyAction("copy-ipv6", l.T("action.copyIPv6"), s.v6.String(), "Ctrl+Enter"))
		}
		md := []plugin.Metadata{}
		if s.v4.IsValid() {
			md = append(md, plugin.Metadata{Label: l.T("meta.ipv4"), Value: s.v4.String()})
		}
		if s.v6.IsValid() {
			md = append(md, plugin.Metadata{Label: l.T("meta.ipv6"), Value: s.v6.String()})
		}
		if !s.at.IsZero() {
			md = append(md, plugin.Metadata{Label: l.T("meta.checked"), Value: l.RelativeTime(s.at, p.h.Now())})
		}
		md = append(md, plugin.Metadata{Label: l.T("meta.source"), Value: l.T("public.source")})
		it.Detail = &plugin.Detail{Metadata: md}
	case s.loading:
		it.Subtitle = l.T("public.loading")
		it.Icon.Tint = plugin.TintMuted
	case s.err != nil:
		it.Subtitle = l.T("public.failed")
		it.Icon = plugin.Icon{Name: "alert", Tint: plugin.TintDanger}
	default:
		it.Subtitle = l.T("public.loading")
	}
	it.Actions = append(it.Actions, callback("refresh", l.T("action.refreshPublic"), cbRefreshPublic, nil, "Ctrl+R"))
	if root {
		it.Actions = append(it.Actions, push("open", l.T("action.openIPView"), toolIP, nil, ""))
	}
	return it
}

// adapterInfo is an adapter with its Wi-Fi connection.
type adapterInfo struct {
	a    winnet.Adapter
	wifi *winnet.WiFiConnection
}

// adapters returns the adapters worth showing: up, not loopback, with an
// address; those with a gateway first.
func (p *Plugin) adapters(ctx context.Context) ([]adapterInfo, error) {
	ads, err := p.b.Adapters(ctx)
	if err != nil {
		return nil, err
	}
	conns, _ := p.b.WiFiConnections(ctx) // no Wi-Fi is fine
	var out []adapterInfo
	for _, a := range ads {
		if !a.Up || a.Loopback() || len(a.IPv4)+len(a.IPv6) == 0 {
			continue
		}
		ai := adapterInfo{a: a}
		for i := range conns {
			if strings.EqualFold(conns[i].InterfaceGUID, a.GUID) {
				ai.wifi = &conns[i]
			}
		}
		out = append(out, ai)
	}
	noGateway := func(ai adapterInfo) int {
		if len(ai.a.Gateways) > 0 {
			return 0
		}
		return 1
	}
	slices.SortStableFunc(out, func(x, y adapterInfo) int { return cmp.Compare(noGateway(x), noGateway(y)) })
	return out, nil
}

// primaryAddress is the address shown for an adapter: the first IPv4
// address that is not link-local, else the first global IPv6 address,
// else the first address.
func primaryAddress(a winnet.Adapter) netip.Addr {
	for _, pf := range a.IPv4 {
		if !pf.Addr().IsLinkLocalUnicast() {
			return pf.Addr()
		}
	}
	for _, pf := range a.IPv6 {
		if pf.Addr().IsGlobalUnicast() && !pf.Addr().IsLinkLocalUnicast() {
			return pf.Addr()
		}
	}
	if len(a.IPv4) > 0 {
		return a.IPv4[0].Addr()
	}
	if len(a.IPv6) > 0 {
		return a.IPv6[0].Addr()
	}
	return netip.Addr{}
}

// adapterItem is the item of an adapter.
func (p *Plugin) adapterItem(ai adapterInfo, root bool) plugin.Item {
	l := p.h.I18n()
	a := ai.a
	main := primaryAddress(a)
	it := plugin.Item{
		ID:    "adapter:" + a.GUID,
		Title: l.T("adapter.title", "name", a.Name, "address", main.String()),
		Icon:  plugin.Icon{Name: "network"},
	}
	var sub []string
	if ai.wifi != nil {
		it.Icon.Name = "wifi"
		sub = append(sub, p.ssid(ai.wifi))
		it.Accessory = l.T("adapter.signal", "signal", ai.wifi.Signal)
	}
	if v6 := firstIPv6(a, main); v6.IsValid() {
		sub = append(sub, v6.String())
	}
	sub = append(sub, a.Description)
	it.Subtitle = strings.Join(sub, " · ")

	md := p.adapterMetadata(ai)
	it.Detail = &plugin.Detail{Metadata: md}
	it.Actions = []plugin.Action{copyAction("copy", l.T("action.copyAddress"), main.String(), "")}
	if v6 := firstIPv6(a, main); v6.IsValid() {
		it.Actions = append(it.Actions, copyAction("copy-ipv6", l.T("action.copyIPv6"), v6.String(), "Ctrl+Enter"))
	}
	if a.MAC != "" {
		it.Actions = append(it.Actions, copyAction("copy-mac", l.T("action.copyMAC"), a.MAC, "Ctrl+M"))
	}
	var details []string
	for _, m := range md {
		details = append(details, m.Label+": "+m.Value)
	}
	it.Actions = append(it.Actions, copyAction("copy-details", l.T("action.copyDetails"), a.Name+"\n"+strings.Join(details, "\n"), "Ctrl+Shift+C"))
	if root {
		it.Actions = append(it.Actions, push("open", l.T("action.openIPView"), toolIP, nil, ""))
	}
	return it
}

// firstIPv6 is the first IPv6 address other than main, preferring global
// ones.
func firstIPv6(a winnet.Adapter, main netip.Addr) netip.Addr {
	var link netip.Addr
	for _, pf := range a.IPv6 {
		switch {
		case pf.Addr() == main:
		case pf.Addr().IsLinkLocalUnicast():
			if !link.IsValid() {
				link = pf.Addr()
			}
		default:
			return pf.Addr()
		}
	}
	return link
}

// adapterMetadata is the detail of an adapter.
func (p *Plugin) adapterMetadata(ai adapterInfo) []plugin.Metadata {
	l := p.h.I18n()
	a := ai.a
	var md []plugin.Metadata
	add := func(label, value string) {
		if value != "" {
			md = append(md, plugin.Metadata{Label: label, Value: value})
		}
	}
	add(l.T("meta.ipv4"), joinPrefixes(a.IPv4))
	add(l.T("meta.ipv6"), joinPrefixes(a.IPv6))
	add(l.T("meta.mac"), a.MAC)
	add(l.T("meta.gateway"), joinAddrs(a.Gateways))
	add(l.T("meta.dns"), joinAddrs(a.DNS))
	add(l.T("meta.dnsSuffix"), a.DNSSuffix)
	if w := ai.wifi; w != nil {
		if w.SSID == "" && w.SSIDHidden {
			add(l.T("meta.ssid"), l.T("wifi.ssidHiddenHint"))
		} else {
			add(l.T("meta.ssid"), w.SSID)
		}
		add(l.T("meta.signal"), l.T("adapter.signal", "signal", w.Signal))
		if w.RxKbps > 0 || w.TxKbps > 0 {
			add(l.T("meta.wifiRate"), l.T("adapter.rates", "rx", p.speed(uint64(w.RxKbps)*1000), "tx", p.speed(uint64(w.TxKbps)*1000)))
		}
	}
	if a.SpeedBps > 0 {
		add(l.T("meta.speed"), p.speed(a.SpeedBps))
	}
	add(l.T("meta.description"), a.Description)
	return md
}

// ssid is the name of the network w is connected to, or a note that macOS
// keeps it from apps without Location Services.
func (p *Plugin) ssid(w *winnet.WiFiConnection) string {
	if w.SSID == "" && w.SSIDHidden {
		return p.h.I18n().T("wifi.ssidHidden")
	}
	return w.SSID
}

// speed formats a link speed: "1 Gbit/s", "866.7 Mbit/s".
func (p *Plugin) speed(bps uint64) string {
	l := p.h.I18n()
	switch {
	case bps >= 1_000_000_000:
		return l.T("unit.gbps", "n", l.Number(round1(float64(bps)/1e9)))
	case bps >= 1_000_000:
		return l.T("unit.mbps", "n", l.Number(round1(float64(bps)/1e6)))
	}
	return l.T("unit.kbps", "n", l.Number(round1(float64(bps)/1e3)))
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

func joinPrefixes(ps []netip.Prefix) string {
	s := make([]string, len(ps))
	for i, pf := range ps {
		s[i] = pf.String()
	}
	return strings.Join(s, ", ")
}

func joinAddrs(as []netip.Addr) string {
	s := make([]string, len(as))
	for i, a := range as {
		s[i] = a.String()
	}
	return strings.Join(s, ", ")
}

// ipRoot is the root search for the ip prefix; rest filters the items.
func (p *Plugin) ipRoot(ctx context.Context, rest string) []plugin.Item {
	l := p.h.I18n()
	var items []plugin.Item
	if p.h.Preferences().Bool(prefPublicIP) {
		p.refreshPublic(false)
		it := p.publicItem(true)
		if matches(rest, it.Title, it.Subtitle, l.T("public.keywords")) {
			items = append(items, it)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, rootTimeout)
	defer cancel()
	ads, err := p.adapters(ctx)
	if err != nil {
		p.h.Logger().Warn("network: reading adapters", "err", err)
		return append(items, plugin.Item{ID: "adapters-error", Title: l.T("adapters.failed"), Subtitle: err.Error(),
			Icon: plugin.Icon{Name: "alert", Tint: plugin.TintDanger}})
	}
	for _, ai := range ads {
		it := p.adapterItem(ai, true)
		if matches(rest, it.Title, it.Subtitle, ai.a.MAC, joinPrefixes(ai.a.IPv4), joinPrefixes(ai.a.IPv6)) {
			items = append(items, it)
		}
	}
	return items
}

// ipView is the view of the adapters and the public addresses.
func (p *Plugin) ipView(ctx context.Context) plugin.View {
	l := p.h.I18n()
	v := plugin.View{Kind: plugin.ViewList, Title: l.T("view.ip.title"), Placeholder: l.T("view.ip.placeholder"),
		EmptyText: l.T("view.ip.empty"), FilterLocally: true, ShowDetail: true}
	if p.h.Preferences().Bool(prefPublicIP) {
		p.refreshPublic(false)
		v.Sections = append(v.Sections, plugin.Section{Title: l.T("section.public"), Items: []plugin.Item{p.publicItem(false)}})
	}
	ads, err := p.adapters(ctx)
	s := plugin.Section{Title: l.T("section.adapters")}
	if err != nil {
		s.Items = append(s.Items, plugin.Item{ID: "adapters-error", Title: l.T("adapters.failed"), Subtitle: err.Error(),
			Icon: plugin.Icon{Name: "alert", Tint: plugin.TintDanger}})
	}
	for _, ai := range ads {
		s.Items = append(s.Items, p.adapterItem(ai, false))
	}
	v.Sections = append(v.Sections, s)
	return v
}
