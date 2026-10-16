// Package network is the built-in network tools plugin (docs/network-tools.md).
//
// Each tool is a command and can also be used from the root search with a
// prefix: "ip" (local adapters, Wi-Fi and the public address), "ping
// <host>" (a live view pushed with Host.Render), "dns <name> [@server]",
// "port <port>" (which process listens) or "port <host>:<port>" (can it be
// reached), "wifi" (saved networks, copy a password) and "tracert <host>"
// (a background task). Text without a prefix gives no results. Malformed
// input gives a hint and sends nothing.
package network

import (
	"context"
	"embed"
	"encoding/json/v2"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// ID is the plugin's manifest ID.
const ID = "builtin.network"

// Tools, also the names of their commands and views.
const (
	toolIP      = "ip"
	toolPing    = "ping"
	toolDNS     = "dns"
	toolPort    = "port"
	toolWiFi    = "wifi"
	toolTracert = "tracert"

	cmdFlushDNS = "flush-dns"
	cmdRenewIP  = "renew-ip"
)

// tools are the prefixed tools, in the order their prefixes are checked.
var tools = []string{toolIP, toolPing, toolDNS, toolPort, toolWiFi, toolTracert}

// Preferences.
const (
	prefPrefix    = "prefix." // + tool
	prefPingCount = "pingCount"
	prefPublicIP  = "publicIP"
)

// Callbacks.
const (
	cbRefreshPublic = "refresh-public"
	cbPingStop      = "ping-stop"
	cbPingRestart   = "ping-restart"
	cbKill          = "kill"
	cbWiFiCopy      = "wifi-copy"
	cbTracert       = "tracert"
)

const (
	// pingTimeout is how long a ping waits for its reply.
	pingTimeout = 2 * time.Second
	// pingInterval is the time between two pings.
	pingInterval = time.Second
	// dialTimeout bounds a remote port check.
	dialTimeout = 3 * time.Second
	// lookupTimeout bounds a DNS lookup.
	lookupTimeout = 5 * time.Second
	// rootTimeout bounds system calls made for the root search, which the
	// host gives 150ms.
	rootTimeout = 120 * time.Millisecond
	// taskQueue serializes ipconfig runs.
	taskQueue = "network.ipconfig"
)

//go:embed locales/*.json
var locales embed.FS

// Options configure the plugin; tests and fake environments replace them.
type Options struct {
	// Backend reads the system and the network (default: System).
	Backend Backend
	// After returns a channel that receives once d has passed (default
	// time.After). The ping loop waits on it between pings.
	After func(d time.Duration) <-chan time.Time
	// Sync fetches the public IP address within the root search instead
	// of in the background (fake environments, for deterministic output).
	Sync bool
}

// Plugin is the network tools plugin.
type Plugin struct {
	opts Options
	b    Backend

	h     plugin.Host
	unsub func()
	wg    sync.WaitGroup
	// bg is cancelled by Close; background lookups run under it.
	bg     context.Context
	stopBG context.CancelFunc

	public publicCache
	dns    dnsCache

	mu     sync.Mutex
	closed bool
	pings  map[string]*pingSession // by view instance
}

var (
	_ plugin.Plugin         = (*Plugin)(nil)
	_ plugin.RootProvider   = (*Plugin)(nil)
	_ plugin.ViewProvider   = (*Plugin)(nil)
	_ plugin.ActionHandler  = (*Plugin)(nil)
	_ plugin.LocaleProvider = (*Plugin)(nil)
	_ plugin.ViewCloser     = (*Plugin)(nil)
)

// New returns the plugin for the real system.
func New() *Plugin { return NewWithOptions(Options{}) }

// NewFake returns the plugin for a fake environment: everything is read
// from the fixture file (testdata/fixtures/network.json; "" for an empty
// fixture), nothing from the system or the network.
func NewFake(fixture string) *Plugin {
	return NewWithOptions(Options{Backend: LoadFixture(fixture), Sync: true})
}

// NewWithOptions returns the plugin with o.
func NewWithOptions(o Options) *Plugin {
	if o.Backend == nil {
		o.Backend = &System{}
	}
	if o.After == nil {
		o.After = time.After
	}
	bg, stop := context.WithCancel(context.Background())
	return &Plugin{opts: o, b: o.Backend, pings: map[string]*pingSession{}, bg: bg, stopBG: stop}
}

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	prefixPref := func(tool, title string) plugin.Preference {
		return plugin.Preference{Name: prefPrefix + tool, Type: plugin.PrefText, Title: title,
			Default: plugin.MustPayload(tool), Group: "%pref.group.prefixes%"}
	}
	return plugin.Manifest{
		ID:            ID,
		Name:          "%plugin.name%",
		Description:   "%plugin.description%",
		Version:       "1.0.0",
		Icon:          plugin.Icon{Name: "network"},
		DefaultLocale: "en",
		Permissions:   []plugin.Permission{plugin.PermNetwork, plugin.PermExec, plugin.PermClipboard},
		Commands: []plugin.Command{
			{Name: toolIP, Title: "%command.ip.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView,
				Icon: plugin.Icon{Name: "network"}, Keywords: []string{"%command.ip.keywords%", "网络", "公网", "地址"}},
			{Name: toolPing, Title: "%command.ping.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView,
				Icon: plugin.Icon{Name: "activity"}, Keywords: []string{"%command.ping.keywords%"},
				Arguments: []plugin.Argument{{Name: "host", Type: plugin.ArgText, Placeholder: "%arg.host%", Required: true}}},
			{Name: toolDNS, Title: "%command.dns.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView,
				Icon: plugin.Icon{Name: "globe"}, Keywords: []string{"%command.dns.keywords%", "域名", "解析"},
				Arguments: []plugin.Argument{{Name: "query", Type: plugin.ArgText, Placeholder: "%arg.dnsQuery%", Required: true}}},
			{Name: toolPort, Title: "%command.port.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView,
				Icon: plugin.Icon{Name: "hash"}, Keywords: []string{"%command.port.keywords%", "端口", "占用"},
				Arguments: []plugin.Argument{{Name: "target", Type: plugin.ArgText, Placeholder: "%arg.portTarget%"}}},
			{Name: toolWiFi, Title: "%command.wifi.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView,
				Icon: plugin.Icon{Name: "wifi"}, Keywords: []string{"%command.wifi.keywords%", "无线", "密码"}},
			{Name: toolTracert, Title: "%command.tracert.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeNoView,
				Icon: plugin.Icon{Name: "network"}, Keywords: []string{"%command.tracert.keywords%", "路由"},
				Arguments: []plugin.Argument{{Name: "host", Type: plugin.ArgText, Placeholder: "%arg.host%", Required: true}}},
			{Name: cmdFlushDNS, Title: "%command.flushDNS.title%", Subtitle: "%command.subtitleAdmin%", Mode: plugin.ModeNoView,
				Icon: plugin.Icon{Name: "globe"}, Keywords: []string{"%command.flushDNS.keywords%", "刷新", "缓存"}},
			{Name: cmdRenewIP, Title: "%command.renewIP.title%", Subtitle: "%command.subtitleAdmin%", Mode: plugin.ModeNoView,
				Icon: plugin.Icon{Name: "network"}, Keywords: []string{"%command.renewIP.keywords%", "续租"}, Destructive: true},
		},
		Preferences: []plugin.Preference{
			{Name: prefPublicIP, Type: plugin.PrefBool, Title: "%pref.publicIP.title%", Description: "%pref.publicIP.description%",
				Default: plugin.MustPayload(true)},
			{Name: prefPingCount, Type: plugin.PrefNumber, Title: "%pref.pingCount.title%", Description: "%pref.pingCount.description%",
				Default: plugin.MustPayload(4)},
			prefixPref(toolIP, "%pref.prefix.ip.title%"),
			prefixPref(toolPing, "%pref.prefix.ping.title%"),
			prefixPref(toolDNS, "%pref.prefix.dns.title%"),
			prefixPref(toolPort, "%pref.prefix.port.title%"),
			prefixPref(toolWiFi, "%pref.prefix.wifi.title%"),
			prefixPref(toolTracert, "%pref.prefix.tracert.title%"),
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

// Init implements plugin.Plugin.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.h = h
	p.unsub = h.Events().Subscribe(plugin.EventWindowHidden, p.stopPings)
	return nil
}

// Close implements plugin.Plugin. It stops the ping loops and waits for
// background work.
func (p *Plugin) Close() error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.stopPings()
	p.stopBG()
	if p.unsub != nil {
		p.unsub()
	}
	p.wg.Wait()
	return nil
}

// goBackground runs fn with Host.Go, counted for Close.
func (p *Plugin) goBackground(fn func()) {
	p.wg.Add(1)
	p.h.Go(func() {
		defer p.wg.Done()
		fn()
	})
}

// cutPrefix returns the tool whose prefix starts text, and the rest. A
// prefix must be followed by a space or the end of the text (except one
// ending in a non-ASCII letter); the longest matching prefix wins.
func (p *Plugin) cutPrefix(text string) (tool, rest string, ok bool) {
	text = strings.TrimSpace(text)
	best := -1
	for _, t := range tools {
		pfx := strings.TrimSpace(p.h.Preferences().String(prefPrefix + t))
		if pfx == "" || len(pfx) > len(text) || !strings.EqualFold(text[:len(pfx)], pfx) || len(pfx) <= best {
			continue
		}
		r := text[len(pfx):]
		last, _ := utf8.DecodeLastRuneInString(pfx)
		if r != "" && r[0] != ' ' && last < utf8.RuneSelf {
			continue
		}
		tool, rest, ok, best = t, strings.TrimSpace(r), true, len(pfx)
	}
	return tool, rest, ok
}

// RootItems implements plugin.RootProvider. Only prefixed queries get
// results; they are scored here because the host cannot match text that
// includes the prefix.
func (p *Plugin) RootItems(ctx context.Context, q plugin.Query) ([]plugin.Item, error) {
	tool, rest, ok := p.cutPrefix(q.Text)
	if !ok {
		return nil, nil
	}
	var items []plugin.Item
	switch tool {
	case toolIP:
		items = p.ipRoot(ctx, rest)
	case toolPing:
		items = p.pingRoot(rest)
	case toolDNS:
		items = p.dnsRoot(rest)
	case toolPort:
		items = p.portRoot(ctx, rest)
	case toolWiFi:
		items = p.wifiRoot(ctx, rest)
	case toolTracert:
		items = p.tracertRoot(rest)
	}
	group := p.h.I18n().T("group")
	for i := range items {
		items[i].Group = group
		items[i].Score = 1 - 0.001*float64(i)
	}
	return items, nil
}

// View implements plugin.ViewProvider.
func (p *Plugin) View(ctx context.Context, req plugin.ViewRequest) (plugin.View, error) {
	switch req.ID {
	case toolIP:
		return p.ipView(ctx), nil
	case toolPing:
		return p.pingView(req)
	case toolDNS:
		return p.dnsView(ctx, req)
	case toolPort:
		return p.portView(ctx, req)
	case toolWiFi:
		return p.wifiView(ctx), nil
	}
	return plugin.View{}, fmt.Errorf("network: unknown view %q", req.ID)
}

// HandleAction implements plugin.ActionHandler.
func (p *Plugin) HandleAction(ctx context.Context, a plugin.ActionDo) (plugin.Result, error) {
	if a.Kind == plugin.ActionRunCommand {
		switch a.Target {
		case toolTracert:
			var host string
			if v, ok := a.Values["host"]; ok {
				if err := json.Unmarshal(v, &host); err != nil {
					return plugin.Result{}, err
				}
			}
			return p.runTracert(host), nil
		case cmdFlushDNS:
			return p.runFlushDNS(), nil
		case cmdRenewIP:
			return p.runRenewIP(), nil
		}
		return plugin.Result{}, fmt.Errorf("network: unknown command %q", a.Target)
	}
	switch a.Target {
	case cbRecheck:
		return plugin.Result{Refresh: true}, nil
	case cbRefreshPublic:
		p.public.reset()
		p.refreshPublic(true)
		return plugin.Result{Refresh: true}, nil
	case cbPingStop, cbPingRestart:
		var pl pingControl
		if err := json.Unmarshal(a.Payload, &pl); err != nil {
			return plugin.Result{}, err
		}
		if a.Target == cbPingStop {
			p.stopPing(pl.Instance)
		} else {
			p.restartPing(pl.Instance, pl.Continuous)
		}
		return plugin.Result{Refresh: true}, nil
	case cbKill:
		var pl killPayload
		if err := json.Unmarshal(a.Payload, &pl); err != nil {
			return plugin.Result{}, err
		}
		return p.kill(pl), nil
	case cbWiFiCopy:
		var pl wifiPayload
		if err := json.Unmarshal(a.Payload, &pl); err != nil {
			return plugin.Result{}, err
		}
		return p.copyWiFiPassword(ctx, pl)
	case cbTracert:
		var pl hostPayload
		if err := json.Unmarshal(a.Payload, &pl); err != nil {
			return plugin.Result{}, err
		}
		return p.runTracert(pl.Host), nil
	}
	return plugin.Result{}, fmt.Errorf("network: unknown action %q", a.Target)
}

// hostPayload is the payload of actions on a host.
type hostPayload struct {
	Host string `json:"host"`
	// Continuous pings until the view is closed.
	Continuous bool `json:"continuous,omitzero"`
}

// --- shared item helpers ---------------------------------------------------

// hint is an item explaining what to type.
func (p *Plugin) hint(id, title, subtitle string) plugin.Item {
	return plugin.Item{ID: id, Title: title, Subtitle: subtitle, Icon: plugin.Icon{Name: "info", Tint: plugin.TintMuted}}
}

// problemItem is an item describing invalid input.
func (p *Plugin) problemItem(id string, prob *problem) plugin.Item {
	l := p.h.I18n()
	return plugin.Item{ID: id, Title: prob.msg(l), Subtitle: l.T("invalid.subtitle"),
		Icon: plugin.Icon{Name: "alert", Tint: plugin.TintDanger}}
}

// copyAction copies text.
func copyAction(id, title, text, shortcut string) plugin.Action {
	return plugin.Action{ID: id, Title: title, Shortcut: shortcut, Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: text}}
}

// push opens view of this plugin with payload.
func push(id, title, view string, payload any, shortcut string) plugin.Action {
	return plugin.Action{ID: id, Title: title, Shortcut: shortcut,
		Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: view, Payload: plugin.MustPayload(payload)}}
}

// callback runs a callback of this plugin with payload.
func callback(id, title, target string, payload any, shortcut string) plugin.Action {
	a := plugin.Action{ID: id, Title: title, Shortcut: shortcut, Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: target}}
	if payload != nil {
		a.Do.Payload = plugin.MustPayload(payload)
	}
	return a
}

// matches reports whether every word of q occurs in one of fields
// (case-insensitively).
func matches(q string, fields ...string) bool {
	all := strings.ToLower(strings.Join(fields, "\n"))
	for w := range strings.FieldsSeq(strings.ToLower(q)) {
		if !strings.Contains(all, w) {
			return false
		}
	}
	return true
}

// failure is a failure toast.
func failure(title, message string) plugin.Result {
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastFailure, Title: title, Message: message}}
}

// ms formats a duration in whole milliseconds ("<1 ms" below one).
func (p *Plugin) ms(d time.Duration) string {
	l := p.h.I18n()
	if d < time.Millisecond {
		return l.T("unit.lessThanMs")
	}
	return l.T("unit.ms", "ms", l.Number(float64(d.Milliseconds())))
}
