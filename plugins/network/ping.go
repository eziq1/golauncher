package network

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"

	winnet "github.com/eziq1/golauncher/internal/winapi/network"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// The ping view. Each open view instance has a session whose loop runs on
// Host.Go: it resolves the host, sends one echo request a second and pushes
// the view with Host.Render after each reply. The loop ends after the
// configured count (or, when continuous, never), when the view closes, the
// launcher hides or the plugin closes.

const (
	// maxShownReplies bounds the replies listed (the statistics count all).
	maxShownReplies = 100
	maxPingCount    = 1000
)

// pingControl is the payload of the stop and restart callbacks.
type pingControl struct {
	Instance   string `json:"instance"`
	Continuous bool   `json:"continuous,omitzero"`
}

// pingResult is one echo request.
type pingResult struct {
	seq   int
	reply winnet.EchoReply
	err   error
}

// pingStats are the statistics of a run.
type pingStats struct {
	sent, received int
	min, max, sum  time.Duration
}

func (st *pingStats) add(r pingResult) {
	st.sent++
	if r.err != nil || r.reply.Status != winnet.StatusSuccess {
		return
	}
	if st.received == 0 || r.reply.RTT < st.min {
		st.min = r.reply.RTT
	}
	st.max = max(st.max, r.reply.RTT)
	st.sum += r.reply.RTT
	st.received++
}

// lossPercent is the share of requests without a reply, rounded.
func (st pingStats) lossPercent() int {
	if st.sent == 0 {
		return 0
	}
	return ((st.sent-st.received)*100 + st.sent/2) / st.sent
}

func (st pingStats) avg() time.Duration {
	if st.received == 0 {
		return 0
	}
	return st.sum / time.Duration(st.received)
}

// pingSession is the state of one open ping view.
type pingSession struct {
	instance string
	host     string

	mu         sync.Mutex
	gen        int           // the current run; loops of older runs exit
	stop       chan struct{} // closed to stop the current run
	running    bool
	continuous bool
	count      int
	addr       netip.Addr
	resolveErr error
	results    []pingResult // the last maxShownReplies
	stats      pingStats
}

// pingCount is the number of requests of a run that is not continuous.
func (p *Plugin) pingCount() int {
	return min(max(p.h.Preferences().Int(prefPingCount), 1), maxPingCount)
}

// pingRoot is the root search for the ping prefix.
func (p *Plugin) pingRoot(rest string) []plugin.Item {
	l := p.h.I18n()
	if rest == "" {
		return []plugin.Item{p.hint("ping-hint", l.T("hint.ping.title"), l.T("hint.ping.subtitle"))}
	}
	host, prob := parseHost(rest, false)
	if prob != nil {
		return []plugin.Item{p.problemItem("ping-invalid", prob)}
	}
	return []plugin.Item{{
		ID:       "ping:" + host,
		Title:    l.T("ping.item", "host", host),
		Subtitle: l.N("ping.itemSubtitle", p.pingCount()),
		Icon:     plugin.Icon{Name: "activity"},
		Actions: []plugin.Action{
			push("ping", l.T("action.ping"), toolPing, hostPayload{Host: host}, ""),
			push("ping-continuous", l.T("action.pingContinuous"), toolPing, hostPayload{Host: host, Continuous: true}, "Ctrl+Enter"),
			callback("tracert", l.T("action.tracert"), cbTracert, hostPayload{Host: host}, "Ctrl+T"),
		},
	}}
}

// pingView is the ping view; opening it starts the session of its
// instance.
func (p *Plugin) pingView(req plugin.ViewRequest) (plugin.View, error) {
	l := p.h.I18n()
	var pl hostPayload
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &pl); err != nil {
			return plugin.View{}, err
		}
	}
	if h, ok := req.Arguments["host"]; ok && pl.Host == "" {
		pl.Host = h
	}
	host, prob := parseHost(pl.Host, false)
	if prob != nil {
		return plugin.View{Kind: plugin.ViewList, Title: l.T("view.ping.titleEmpty"),
			Sections: []plugin.Section{{Items: []plugin.Item{p.problemItem("ping-invalid", prob)}}}}, nil
	}
	s := p.pingSession(req.Instance, host, pl.Continuous)
	return p.pingSnapshot(s), nil
}

// pingSession returns the session of instance, creating and starting it.
// Without an instance (or after Close) the session is not started.
func (p *Plugin) pingSession(instance, host string, continuous bool) *pingSession {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.pings[instance]; ok && instance != "" {
		return s
	}
	s := &pingSession{instance: instance, host: host, continuous: continuous, count: p.pingCount()}
	if instance == "" || p.closed {
		return s
	}
	p.pings[instance] = s
	p.startPing(s)
	return s
}

// startPing starts a run of s.
func (p *Plugin) startPing(s *pingSession) {
	s.mu.Lock()
	s.gen++
	gen, stop := s.gen, make(chan struct{})
	s.stop, s.running = stop, true
	s.results, s.stats = nil, pingStats{}
	s.count = p.pingCount()
	s.mu.Unlock()
	p.goBackground(func() { p.pingLoop(s, gen, stop) })
}

// pingLoop is a run of s.
func (p *Plugin) pingLoop(s *pingSession, gen int, stop <-chan struct{}) {
	ctx, cancel := context.WithCancel(p.bg)
	defer cancel()
	stopped := func() bool {
		select {
		case <-stop:
			return true
		default:
			return false
		}
	}
	s.mu.Lock()
	addr := s.addr
	s.mu.Unlock()
	if !addr.IsValid() {
		rctx, rcancel := context.WithTimeout(ctx, lookupTimeout)
		a, err := p.b.Resolve(rctx, s.host)
		rcancel()
		s.mu.Lock()
		if s.gen != gen {
			s.mu.Unlock()
			return
		}
		s.addr, s.resolveErr = a, err
		if err != nil {
			s.running = false
		}
		s.mu.Unlock()
		p.renderPing(s)
		if err != nil {
			return
		}
		addr = a
	}
	for seq := 1; ; seq++ {
		if stopped() {
			return
		}
		sent := time.Now()
		r, err := p.b.Ping(ctx, addr, pingTimeout)
		if stopped() {
			return
		}
		s.mu.Lock()
		if s.gen != gen {
			s.mu.Unlock()
			return
		}
		res := pingResult{seq: seq, reply: r, err: err}
		s.stats.add(res)
		s.results = append(s.results, res)
		if len(s.results) > maxShownReplies {
			s.results = s.results[len(s.results)-maxShownReplies:]
		}
		done := !s.continuous && seq >= s.count
		if done {
			s.running = false
		}
		s.mu.Unlock()
		p.renderPing(s)
		if done {
			return
		}
		select {
		case <-stop:
			return
		case <-p.opts.After(max(pingInterval-time.Since(sent), 0)):
		}
	}
}

// renderPing pushes the view of s if it is still open.
func (p *Plugin) renderPing(s *pingSession) {
	p.mu.Lock()
	open := p.pings[s.instance] == s
	p.mu.Unlock()
	if open {
		p.h.Render(s.instance, p.pingSnapshot(s))
	}
}

// stopRun stops the current run of s.
func (s *pingSession) stopRun() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop != nil {
		close(s.stop)
		s.stop = nil
	}
	s.running = false
	s.gen++
}

// stopPing stops the run of the view instance.
func (p *Plugin) stopPing(instance string) {
	p.mu.Lock()
	s := p.pings[instance]
	p.mu.Unlock()
	if s != nil {
		s.stopRun()
	}
}

// restartPing starts a new run of the view instance.
func (p *Plugin) restartPing(instance string, continuous bool) {
	p.mu.Lock()
	s := p.pings[instance]
	closed := p.closed
	p.mu.Unlock()
	if s == nil || closed {
		return
	}
	s.stopRun()
	s.mu.Lock()
	s.continuous = continuous
	if s.resolveErr != nil {
		s.addr, s.resolveErr = netip.Addr{}, nil
	}
	s.mu.Unlock()
	p.startPing(s)
}

// ViewClosed implements plugin.ViewCloser: it stops the ping of instance.
func (p *Plugin) ViewClosed(instance string) {
	p.mu.Lock()
	s := p.pings[instance]
	delete(p.pings, instance)
	p.mu.Unlock()
	if s != nil {
		s.stopRun()
	}
}

// stopPings stops every ping: the launcher was hidden or the plugin closes.
func (p *Plugin) stopPings() {
	p.mu.Lock()
	sessions := p.pings
	p.pings = map[string]*pingSession{}
	p.mu.Unlock()
	for _, s := range sessions {
		s.stopRun()
	}
}

// pingSnapshot is the view of s.
func (p *Plugin) pingSnapshot(s *pingSession) plugin.View {
	l := p.h.I18n()
	s.mu.Lock()
	defer s.mu.Unlock()
	target := s.host
	if s.addr.IsValid() && s.addr.String() != s.host {
		target = l.T("ping.hostAddress", "host", s.host, "address", s.addr.String())
	}
	st := s.stats
	summary := plugin.Item{ID: "summary", Icon: plugin.Icon{Name: "activity"}}
	switch {
	case s.resolveErr != nil:
		summary.Title = l.T("ping.resolveFailed", "host", s.host)
		summary.Subtitle = s.resolveErr.Error()
		summary.Icon = plugin.Icon{Name: "alert", Tint: plugin.TintDanger}
	case !s.addr.IsValid() && s.running:
		summary.Title = l.T("ping.resolving", "host", s.host)
	case s.running:
		summary.Title = l.T("ping.running", "target", target)
		summary.Icon.Tint = plugin.TintAccent
	default:
		summary.Title = l.T("ping.done", "target", target)
		switch {
		case st.sent > 0 && st.received == st.sent:
			summary.Icon.Tint = plugin.TintSuccess
		case st.sent > 0 && st.received == 0:
			summary.Icon.Tint = plugin.TintDanger
		}
	}
	if st.sent > 0 {
		summary.Subtitle = l.T("ping.stats", "sent", st.sent, "received", st.received, "loss", st.lossPercent())
		if st.received > 0 {
			summary.Subtitle += " · " + l.T("ping.rtt", "min", p.ms(st.min), "avg", p.ms(st.avg()), "max", p.ms(st.max))
		}
	}
	if s.continuous {
		summary.Accessory = l.T("ping.continuous")
	} else if s.resolveErr == nil {
		summary.Accessory = l.T("ping.progress", "sent", st.sent, "count", s.count)
	}

	var actions []plugin.Action
	if s.running {
		actions = append(actions, callback("stop", l.T("action.stop"), cbPingStop, pingControl{Instance: s.instance}, ""))
	} else {
		actions = append(actions,
			callback("again", l.T("action.pingAgain"), cbPingRestart, pingControl{Instance: s.instance}, ""),
			callback("continuous", l.T("action.pingContinuous"), cbPingRestart, pingControl{Instance: s.instance, Continuous: true}, "Ctrl+Enter"))
	}
	var lines []string
	lines = append(lines, summary.Title)
	if summary.Subtitle != "" {
		lines = append(lines, summary.Subtitle)
	}
	replies := plugin.Section{Title: l.T("section.replies")}
	for i := len(s.results) - 1; i >= 0; i-- {
		it := p.replyItem(s.results[i])
		replies.Items = append(replies.Items, it)
	}
	for _, r := range s.results {
		lines = append(lines, l.T("ping.copyLine", "seq", r.seq, "reply", p.replyText(r)))
	}
	actions = append(actions, copyAction("copy", l.T("action.copyResults"), strings.Join(lines, "\n"), "Ctrl+Shift+C"))
	summary.Actions = actions
	for i := range replies.Items {
		replies.Items[i].Actions = actions
	}
	v := plugin.View{Kind: plugin.ViewList, Title: l.T("view.ping.title", "host", s.host), FilterLocally: true,
		Sections: []plugin.Section{{Items: []plugin.Item{summary}}}}
	if len(replies.Items) > 0 {
		v.Sections = append(v.Sections, replies)
	}
	return v
}

// replyItem is the item of one echo request.
func (p *Plugin) replyItem(r pingResult) plugin.Item {
	l := p.h.I18n()
	it := plugin.Item{ID: fmt.Sprintf("reply:%d", r.seq), Title: p.replyText(r), Accessory: l.T("ping.seq", "seq", r.seq),
		Icon: plugin.Icon{Name: "alert", Tint: plugin.TintDanger}}
	switch {
	case r.err != nil:
		it.Subtitle = r.err.Error()
	case r.reply.Status == winnet.StatusSuccess:
		it.Icon = plugin.Icon{Name: "check", Tint: plugin.TintSuccess}
		it.Subtitle = p.ms(r.reply.RTT)
		if r.reply.TTL > 0 {
			it.Subtitle = l.T("ping.replyDetail", "time", p.ms(r.reply.RTT), "ttl", r.reply.TTL)
		}
	case r.reply.Status == winnet.StatusTimedOut:
		it.Icon.Tint = plugin.TintMuted
	}
	return it
}

// replyText describes the outcome of one echo request.
func (p *Plugin) replyText(r pingResult) string {
	l := p.h.I18n()
	if r.err != nil {
		return l.T("ping.error")
	}
	switch winnet.Normalize(r.reply.Status) {
	case winnet.StatusSuccess:
		return l.T("ping.reply", "address", r.reply.From.String(), "time", p.ms(r.reply.RTT))
	case winnet.StatusTimedOut:
		return l.T("ping.timedOut")
	case winnet.StatusHostUnreachable:
		return l.T("ping.hostUnreachable")
	case winnet.StatusNetUnreachable:
		return l.T("ping.netUnreachable")
	case winnet.StatusTTLExpired:
		return l.T("ping.ttlExpired")
	}
	return l.T("ping.failed", "status", r.reply.Status)
}
