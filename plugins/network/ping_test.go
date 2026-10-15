package network

import (
	"context"
	"errors"
	"net/netip"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	winnet "github.com/eziq1/golauncher/internal/winapi/network"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// scripted answers pings from a script; other calls go to the fixture.
type scripted struct {
	*FixtureBackend
	mu      sync.Mutex
	replies []winnet.EchoReply
	errs    []error
	sent    int
}

func (s *scripted) Ping(_ context.Context, addr netip.Addr, _ time.Duration) (winnet.EchoReply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.sent
	s.sent++
	var err error
	if i < len(s.errs) {
		err = s.errs[i]
	}
	if i < len(s.replies) {
		return s.replies[i], err
	}
	return winnet.EchoReply{From: addr, RTT: 5 * time.Millisecond, TTL: 56}, err
}

func (s *scripted) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sent
}

// ticks replaces time.After: the ping loop waits until the test calls tick.
type ticks struct{ ch chan time.Time }

func newTicks() *ticks { return &ticks{ch: make(chan time.Time)} }

func (k *ticks) after(time.Duration) <-chan time.Time { return k.ch }

func (k *ticks) tick(t *testing.T) {
	t.Helper()
	select {
	case k.ch <- time.Time{}:
	case <-time.After(5 * time.Second):
		t.Fatal("the ping loop does not wait")
	}
}

// lastRender returns the latest view pushed for instance.
func lastRender(h *hostfake.Host, instance string) (plugin.View, int) {
	var v plugin.View
	n := 0
	for _, r := range h.Renders() {
		if r.Instance == instance {
			v, n = r.View, n+1
		}
	}
	return v, n
}

func openPing(t *testing.T, p *Plugin, instance string, pl hostPayload) plugin.View {
	t.Helper()
	v, err := p.View(t.Context(), plugin.ViewRequest{ID: toolPing, Instance: instance, Payload: plugin.MustPayload(pl)})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func waitSummary(t *testing.T, h *hostfake.Host, instance, title string) plugin.View {
	t.Helper()
	var v plugin.View
	eventually(t, "the summary reads "+title, func() bool {
		v, _ = lastRender(h, instance)
		return len(v.Sections) > 0 && v.Sections[0].Items[0].Title == title
	})
	return v
}

// TestPingStats pings three times: a reply, a timeout and an unreachable
// host, each pushed to the view, then the statistics.
func TestPingStats(t *testing.T) {
	b := &scripted{FixtureBackend: fixture(t), replies: []winnet.EchoReply{
		{From: netip.MustParseAddr("93.184.215.14"), RTT: 10 * time.Millisecond, TTL: 56},
		{Status: winnet.StatusTimedOut},
		{From: netip.MustParseAddr("192.0.2.254"), Status: winnet.StatusHostUnreachable},
	}}
	k := newTicks()
	p, h := newPlugin(t, Options{Backend: b, After: k.after})
	h.Prefs.Set(prefPingCount, plugin.MustPayload(3))

	items := root(t, p, "ping example.com")
	if len(items) != 1 || items[0].Subtitle != "Send 3 echo requests and show each reply" {
		t.Fatalf("root = %+v", items)
	}
	openPing(t, p, "v1", hostPayload{Host: "example.com"})
	v := waitSummary(t, h, "v1", "Pinging example.com [93.184.215.14]…")
	k.tick(t)
	k.tick(t)
	v = waitSummary(t, h, "v1", "Pinged example.com [93.184.215.14]")
	sum := v.Sections[0].Items[0]
	if sum.Subtitle != "Sent 3 · Received 1 · Lost 67% · Min 10 ms · Avg 10 ms · Max 10 ms" || sum.Accessory != "3/3" {
		t.Errorf("summary = %+v", sum)
	}
	if got := titles(v.Sections[1].Items); !slices.Equal(got, []string{"Destination host unreachable", "Request timed out", "Reply from 93.184.215.14: 10 ms"}) {
		t.Errorf("replies = %v", got)
	}
	if v.Sections[1].Items[2].Subtitle != "Time 10 ms · TTL 56" {
		t.Errorf("reply detail = %q", v.Sections[1].Items[2].Subtitle)
	}
	if b.count() != 3 {
		t.Errorf("%d pings", b.count())
	}
	// A pull returns the same state and starts nothing.
	if v2 := openPing(t, p, "v1", hostPayload{Host: "example.com"}); v2.Sections[0].Items[0].Title != sum.Title {
		t.Errorf("pull = %+v", v2.Sections[0].Items[0])
	}
	copyText := action(t, sum, "copy").Do.Target
	if !strings.Contains(copyText, "#2 Request timed out") || !strings.Contains(copyText, "Lost 67%") {
		t.Errorf("copy = %q", copyText)
	}
	// Ping again.
	if _, err := p.HandleAction(t.Context(), action(t, sum, "again").Do); err != nil {
		t.Fatal(err)
	}
	k.tick(t)
	k.tick(t)
	v = waitSummary(t, h, "v1", "Pinged example.com [93.184.215.14]")
	if v.Sections[0].Items[0].Subtitle != "Sent 3 · Received 3 · Lost 0% · Min 5 ms · Avg 5 ms · Max 5 ms" {
		t.Errorf("again = %q", v.Sections[0].Items[0].Subtitle)
	}
}

// TestPingContinuousStops pings until stopped; closing the view and hiding
// the launcher stop it too.
func TestPingContinuousStops(t *testing.T) {
	b := &scripted{FixtureBackend: fixture(t)}
	k := newTicks()
	p, h := newPlugin(t, Options{Backend: b, After: k.after})
	h.Prefs.Set(prefPingCount, plugin.MustPayload(2))
	openPing(t, p, "v1", hostPayload{Host: "93.184.215.14", Continuous: true})
	for range 4 {
		k.tick(t)
	}
	v := waitSummary(t, h, "v1", "Pinging 93.184.215.14…")
	eventually(t, "five pings", func() bool { return b.count() == 5 })
	if v.Sections[0].Items[0].Accessory != "Continuous" {
		t.Errorf("accessory = %q", v.Sections[0].Items[0].Accessory)
	}
	stop := action(t, v.Sections[0].Items[0], "stop")
	if _, err := p.HandleAction(t.Context(), stop.Do); err != nil {
		t.Fatal(err)
	}
	pv := openPing(t, p, "v1", hostPayload{Host: "93.184.215.14", Continuous: true})
	if s := pv.Sections[0].Items[0]; s.Title != "Pinged 93.184.215.14" || !strings.HasPrefix(s.Subtitle, "Sent 5 · Received 5") {
		t.Errorf("stopped = %+v", s)
	}
	select {
	case k.ch <- time.Time{}:
		t.Error("the loop still waits after Stop")
	case <-time.After(50 * time.Millisecond):
	}

	// Closing the view ends its loop.
	openPing(t, p, "v2", hostPayload{Host: "93.184.215.14", Continuous: true})
	eventually(t, "v2 pinged", func() bool { _, n := lastRender(h, "v2"); return n > 0 })
	p.ViewClosed("v2")
	sent := b.count()
	select {
	case k.ch <- time.Time{}:
		t.Error("the loop still waits after the view closed")
	case <-time.After(50 * time.Millisecond):
	}
	if b.count() != sent {
		t.Error("pinged after the view closed")
	}

	// Hiding the launcher ends every loop.
	openPing(t, p, "v3", hostPayload{Host: "93.184.215.14", Continuous: true})
	eventually(t, "v3 pinged", func() bool { _, n := lastRender(h, "v3"); return n > 0 })
	h.Bus.Publish(plugin.EventWindowHidden)
	eventually(t, "the sessions are gone", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.pings) == 0
	})
}

func TestPingResolveFailure(t *testing.T) {
	k := newTicks()
	p, h := newPlugin(t, Options{Backend: &scripted{FixtureBackend: fixture(t)}, After: k.after})
	openPing(t, p, "v1", hostPayload{Host: "nowhere.example"})
	v := waitSummary(t, h, "v1", "Couldn’t find nowhere.example")
	if v.Sections[0].Items[0].Icon.Tint != plugin.TintDanger || len(v.Sections) != 1 {
		t.Errorf("view = %+v", v)
	}
	// An invalid host in the arguments gives a hint and starts nothing.
	v, err := p.View(t.Context(), plugin.ViewRequest{ID: toolPing, Instance: "v2", Arguments: map[string]string{"host": "a b"}})
	if err != nil || v.Sections[0].Items[0].Title != "“a b” is not a valid host name or IP address" {
		t.Errorf("invalid = %+v, %v", v, err)
	}
}

func TestPingSendError(t *testing.T) {
	b := &scripted{FixtureBackend: fixture(t), errs: []error{errors.New("boom")}}
	p, h := newPlugin(t, Options{Backend: b, After: newTicks().after})
	h.Prefs.Set(prefPingCount, plugin.MustPayload(1))
	openPing(t, p, "v1", hostPayload{Host: "127.0.0.1"})
	v := waitSummary(t, h, "v1", "Pinged 127.0.0.1")
	if r := v.Sections[1].Items[0]; r.Title != "Couldn’t send the request" || r.Subtitle != "boom" {
		t.Errorf("reply = %+v", r)
	}
	if v.Sections[0].Items[0].Icon.Tint != plugin.TintDanger {
		t.Error("all lost is not red")
	}
}

// TestPingLoopbackReal pings 127.0.0.1 with IcmpSendEcho2.
func TestPingLoopbackReal(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}
	p, h := newPlugin(t, Options{After: newTicks().after})
	h.Prefs.Set(prefPingCount, plugin.MustPayload(1))
	openPing(t, p, "v1", hostPayload{Host: "127.0.0.1"})
	v := waitSummary(t, h, "v1", "Pinged 127.0.0.1")
	if !strings.HasPrefix(v.Sections[0].Items[0].Subtitle, "Sent 1 · Received 1 · Lost 0%") {
		t.Errorf("summary = %+v", v.Sections[0].Items[0])
	}
	if r := v.Sections[1].Items[0]; !strings.HasPrefix(r.Title, "Reply from 127.0.0.1") || !strings.Contains(r.Subtitle, "TTL 128") {
		t.Errorf("reply = %+v", r)
	}
}

func TestPingStatsMath(t *testing.T) {
	var st pingStats
	for _, ms := range []int{10, 30, -1, 20} {
		r := pingResult{reply: winnet.EchoReply{RTT: time.Duration(ms) * time.Millisecond}}
		if ms < 0 {
			r.reply.Status = winnet.StatusTimedOut
		}
		st.add(r)
	}
	if st.sent != 4 || st.received != 3 || st.min != 10*time.Millisecond || st.max != 30*time.Millisecond ||
		st.avg() != 20*time.Millisecond || st.lossPercent() != 25 {
		t.Errorf("stats = %+v", st)
	}
	if (pingStats{}).lossPercent() != 0 || (pingStats{}).avg() != 0 {
		t.Error("empty stats")
	}
}
