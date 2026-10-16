package network

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	winnet "github.com/eziq1/golauncher/internal/winapi/network"
	"github.com/eziq1/golauncher/pkg/plugin"
)

const fixturePath = "../../testdata/fixtures/network.json"

// newPlugin starts the plugin with o on a fake host.
func newPlugin(t *testing.T, o Options) (*Plugin, *hostfake.Host) {
	t.Helper()
	p := NewWithOptions(o)
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p, h
}

// fixture is the backend of the shared fixture file.
func fixture(t *testing.T) *FixtureBackend {
	t.Helper()
	b := LoadFixture(fixturePath)
	if b.Err != nil {
		t.Fatal(b.Err)
	}
	return b
}

func root(t *testing.T, p *Plugin, text string) []plugin.Item {
	t.Helper()
	items, err := p.RootItems(t.Context(), plugin.Query{Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func titles(items []plugin.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Title)
	}
	return out
}

func action(t *testing.T, it plugin.Item, id string) plugin.Action {
	t.Helper()
	for _, a := range it.Actions {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("%s has no action %s: %+v", it.ID, id, it.Actions)
	return plugin.Action{}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// offline is a backend that fails the test on any request leaving the
// machine.
type offline struct {
	*FixtureBackend
	t *testing.T
}

func (o offline) PublicIP(context.Context, bool) (netip.Addr, error) {
	o.t.Error("public IP requested")
	return netip.Addr{}, nil
}
func (o offline) Resolve(context.Context, string) (netip.Addr, error) {
	o.t.Error("resolve requested")
	return netip.Addr{}, nil
}
func (o offline) Ping(context.Context, netip.Addr, time.Duration) (winnet.EchoReply, error) {
	o.t.Error("ping sent")
	return winnet.EchoReply{}, nil
}
func (o offline) LookupDNS(context.Context, string, string, string) ([]Record, error) {
	o.t.Error("DNS lookup sent")
	return nil, nil
}
func (o offline) Dial(context.Context, string, time.Duration) (time.Duration, string, error) {
	o.t.Error("connection opened")
	return 0, "", nil
}

func TestNoPrefixNoResults(t *testing.T) {
	p, _ := newPlugin(t, Options{Backend: offline{fixture(t), t}})
	for _, q := range []string{"", "pin", "ipconfig", "dnsx example.com", "portal", "wifix", "chrome"} {
		if items := root(t, p, q); len(items) != 0 {
			t.Errorf("%q: %v", q, titles(items))
		}
	}
}

// TestInvalidInputSendsNothing gives hints for malformed input without any
// request (docs/network-tools.md §1).
func TestInvalidInputSendsNothing(t *testing.T) {
	p, h := newPlugin(t, Options{Backend: offline{fixture(t), t}})
	h.Prefs.Set(prefPublicIP, plugin.MustPayload(false))
	cases := map[string]string{
		"ping":                    "Type a host to ping",
		"ping exa..mple":          "“exa..mple” is not a valid host name or IP address",
		"ping -t example.com":     "“-t example.com” is not a valid host name or IP address",
		"ping 999.1.1.1":          "“999.1.1.1” is not a valid host name or IP address",
		"ping bad_host.com":       "“bad_host.com” is not a valid host name or IP address",
		"dns":                     "Type a domain to look up",
		"dns example.com @":       "“@” is not a valid DNS server",
		"dns example.com @a..b":   "“a..b” is not a valid DNS server",
		"dns a.com b.com":         "Type a domain and an optional @server, not “a.com b.com”",
		"port 70000":              "Port 70000 is out of range (1–65535)",
		"port 0":                  "Port 0 is out of range (1–65535)",
		"port abc":                "“abc” is not a port number",
		"port example.com:99999":  "Port 99999 is out of range (1–65535)",
		"port exa mple com":       "Type a port (8080) or host:port (example.com:443), not “exa mple com”",
		"tracert":                 "Type a host to trace the route to",
		"tracert -h 3 x.com":      "“-h 3 x.com” is not a valid host name or IP address",
		"port host..x:80":         "“host..x” is not a valid host name or IP address",
		"ping " + longLabel + ".": "“" + longLabel + ".” is not a valid host name or IP address",
	}
	for q, want := range cases {
		items := root(t, p, q)
		if len(items) == 0 || items[0].Title != want {
			t.Errorf("%q: %v, want %q", q, titles(items), want)
			continue
		}
		for _, a := range items[0].Actions {
			if a.Do.Kind == plugin.ActionPush || a.Do.Kind == plugin.ActionCallback {
				t.Errorf("%q: the hint has action %s", q, a.ID)
			}
		}
	}
}

var longLabel = strings.Repeat("a", 64) + ".com"

func TestParsers(t *testing.T) {
	for _, s := range []string{"example.com", "localhost", "a-b.example.co.uk", "127.0.0.1", "::1", "[2001:db8::1]", "Example.COM."} {
		if _, prob := parseHost(s, false); prob != nil {
			t.Errorf("parseHost(%q) failed", s)
		}
	}
	if h, _ := parseHost("Example.COM", false); h != "example.com" {
		t.Errorf("not lower-cased: %s", h)
	}
	if _, prob := parseHost("_sip._tcp.example.com", true); prob != nil {
		t.Error("DNS names may have underscores")
	}
	for in, want := range map[string]portTarget{
		"8080":              {Port: 8080},
		"example.com:443":   {Host: "example.com", Port: 443},
		"example.com 443":   {Host: "example.com", Port: 443},
		"[2001:db8::1]:22":  {Host: "2001:db8::1", Port: 22},
		"2001:db8::1 22":    {Host: "2001:db8::1", Port: 22},
		"":                  {},
		"192.168.1.1:65535": {Host: "192.168.1.1", Port: 65535},
	} {
		got, prob := parsePortTarget(in)
		if prob != nil || got != want {
			t.Errorf("parsePortTarget(%q) = %+v, %v", in, got, prob)
		}
	}
	for in, want := range map[string]dnsQuery{
		"example.com":                  {Name: "example.com"},
		"example.com @1.1.1.1":         {Name: "example.com", Server: "1.1.1.1:53"},
		"@8.8.8.8 example.com":         {Name: "example.com", Server: "8.8.8.8:53"},
		"example.com @[2606:4700::1]":  {Name: "example.com", Server: "[2606:4700::1]:53"},
		"example.com @127.0.0.1:5353":  {Name: "example.com", Server: "127.0.0.1:5353"},
		"example.com @dns.google":      {Name: "example.com", Server: "dns.google:53"},
		"8.8.8.8":                      {Name: "8.8.8.8", Reverse: true},
		"_dmarc.example.com @1.0.0.1 ": {Name: "_dmarc.example.com", Server: "1.0.0.1:53"},
	} {
		got, prob := parseDNSQuery(in)
		if prob != nil || got != want {
			t.Errorf("parseDNSQuery(%q) = %+v, %v", in, got, prob)
		}
	}
	if q, _ := parseDNSQuery("example.com @[2606:4700::1]"); q.String() != "example.com @[2606:4700::1]" {
		t.Errorf("String = %q", q.String())
	}
}

func TestCustomPrefixes(t *testing.T) {
	p, h := newPlugin(t, Options{Backend: offline{fixture(t), t}})
	h.Prefs.Set(prefPrefix+toolPing, plugin.MustPayload("p"))
	h.Prefs.Set(prefPrefix+toolDNS, plugin.MustPayload(""))
	if got := titles(root(t, p, "p example.com")); !slices.Equal(got, []string{"Ping example.com"}) {
		t.Errorf("custom prefix: %v", got)
	}
	if got := root(t, p, "ping example.com"); len(got) != 0 {
		t.Errorf("old prefix still works: %v", titles(got))
	}
	if got := root(t, p, "dns example.com"); len(got) != 0 {
		t.Errorf("disabled prefix works: %v", titles(got))
	}
}

// TestIPRootFromFixture lists the public address and the connected
// adapters (not the loopback or a down one) with Wi-Fi details.
func TestIPRootFromFixture(t *testing.T) {
	p, h := newPlugin(t, Options{Backend: fixture(t), Sync: true})
	items := root(t, p, "ip")
	want := []string{"Public IP  203.0.113.7", "Wi-Fi  192.168.1.23", "Ethernet 2  172.20.0.1"}
	if got := titles(items); !slices.Equal(got, want) {
		t.Fatalf("titles = %v", got)
	}
	if items[0].Subtitle != "IPv6 2001:db8::7" || action(t, items[0], "copy-ipv6").Do.Target != "2001:db8::7" {
		t.Errorf("public = %+v", items[0])
	}
	wifi := items[1]
	if wifi.Subtitle != "HomeNet · 2001:db8:1::23 · Contoso Wireless Adapter" || wifi.Accessory != "82%" || wifi.Icon.Name != "wifi" {
		t.Errorf("wifi = %+v", wifi)
	}
	if a := action(t, wifi, "copy"); a.Do.Kind != plugin.ActionCopyText || a.Do.Target != "192.168.1.23" {
		t.Errorf("copy = %+v", a)
	}
	md := map[string]string{}
	for _, m := range wifi.Detail.Metadata {
		md[m.Label] = m.Value
	}
	for label, value := range map[string]string{
		"IPv4": "192.168.1.23/24", "Gateway": "192.168.1.1, fe80::1", "DNS servers": "192.168.1.1, 2001:db8:1::1",
		"MAC address": "AA-BB-CC-00-11-22", "Signal": "82%", "Link speed": "866.7 Mbit/s", "Wi-Fi rate": "↓ 866.7 Mbit/s · ↑ 720.6 Mbit/s",
	} {
		if md[label] != value {
			t.Errorf("%s = %q, want %q", label, md[label], value)
		}
	}
	if got := titles(root(t, p, "ip hyper-v")); !slices.Equal(got, []string{"Ethernet 2  172.20.0.1"}) {
		t.Errorf("filtered: %v", got)
	}
	h.Prefs.Set(prefPublicIP, plugin.MustPayload(false))
	if got := titles(root(t, p, "ip")); slices.Contains(got, "Public IP  203.0.113.7") {
		t.Errorf("public IP shown although turned off: %v", got)
	}
	v, err := p.View(t.Context(), plugin.ViewRequest{ID: toolIP})
	if err != nil || len(v.Sections) != 1 || len(v.Sections[0].Items) != 2 {
		t.Errorf("view = %+v, %v", v, err)
	}
}

// ipServer is a fake public IP service: /json answers like ipify, /plain
// like ifconfig.me, /fail with an error. It counts the requests.
func ipServer(t *testing.T, addr string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/json":
			fmt.Fprintf(w, `{"ip":%q}`, addr)
		case "/plain":
			fmt.Fprintln(w, addr)
		case "/garbage":
			fmt.Fprintln(w, "<html>not an address</html>")
		default:
			http.Error(w, "down", http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestPublicIPFallback(t *testing.T) {
	srv, _ := ipServer(t, "203.0.113.9")
	s := &System{Transport: srv.Client().Transport,
		IPv4URLs: []string{srv.URL + "/fail", srv.URL + "/garbage", srv.URL + "/plain"},
		IPv6URLs: []string{srv.URL + "/json"}}
	a, err := s.PublicIP(t.Context(), false)
	if err != nil || a != netip.MustParseAddr("203.0.113.9") {
		t.Errorf("v4 = %v, %v", a, err)
	}
	// The IPv6 service answered with an IPv4 address: not accepted.
	if a, err := s.PublicIP(t.Context(), true); err == nil {
		t.Errorf("v6 = %v", a)
	}
	s.IPv4URLs = []string{srv.URL + "/fail"}
	if _, err := s.PublicIP(t.Context(), false); err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("err = %v", err)
	}
}

// TestPublicIPCached looks the address up in the background when "ip" is
// typed, invalidates the list when it arrives and asks again only after
// five minutes.
func TestPublicIPCached(t *testing.T) {
	srv, hits := ipServer(t, "203.0.113.9")
	b := &systemNoAdapters{System{Transport: srv.Client().Transport,
		IPv4URLs: []string{srv.URL + "/json"}, IPv6URLs: []string{srv.URL + "/fail"}}}
	p, h := newPlugin(t, Options{Backend: b})
	now := hostfake.Fixed
	h.Clock = func() time.Time { return now }

	if got := root(t, p, "ping x.com"); len(got) != 1 || hits.Load() != 0 {
		t.Fatalf("another prefix looked the address up: %d", hits.Load())
	}
	items := root(t, p, "ip")
	if items[0].Title != "Public IP" || items[0].Subtitle != "Looking up…" {
		t.Errorf("first = %+v", items[0])
	}
	eventually(t, "the lookup finished", func() bool { return p.public.snapshot().v6Done && len(h.Invalidated()) >= 2 })
	items = root(t, p, "ip")
	if items[0].Title != "Public IP  203.0.113.9" || items[0].Subtitle != "No public IPv6 address" {
		t.Errorf("after = %+v", items[0])
	}
	n := hits.Load() // one IPv4 and one IPv6 request
	root(t, p, "ip")
	now = now.Add(4 * time.Minute)
	root(t, p, "ip")
	if hits.Load() != n {
		t.Errorf("asked again within 5 minutes: %d -> %d", n, hits.Load())
	}
	now = now.Add(2 * time.Minute)
	root(t, p, "ip")
	eventually(t, "asked again after 5 minutes", func() bool { return hits.Load() > n })
}

// systemNoAdapters is System without real adapters or Wi-Fi.
type systemNoAdapters struct{ System }

func (systemNoAdapters) Adapters(context.Context) ([]winnet.Adapter, error) { return nil, nil }
func (systemNoAdapters) WiFiConnections(context.Context) ([]winnet.WiFiConnection, error) {
	return nil, winnet.ErrNoWiFi
}

func TestPublicIPFailureAndRetry(t *testing.T) {
	srv, hits := ipServer(t, "203.0.113.9")
	b := &systemNoAdapters{System{Transport: srv.Client().Transport,
		IPv4URLs: []string{srv.URL + "/fail"}, IPv6URLs: []string{srv.URL + "/fail"}}}
	p, _ := newPlugin(t, Options{Backend: b, Sync: true})
	it := root(t, p, "ip")[0]
	if it.Subtitle != "Couldn’t reach api.ipify.org or ifconfig.me" || it.Icon.Tint != plugin.TintDanger {
		t.Errorf("failed = %+v", it)
	}
	n := hits.Load()
	b.IPv4URLs = []string{srv.URL + "/json"}
	r, err := p.HandleAction(t.Context(), action(t, it, "refresh").Do)
	if err != nil || !r.Refresh || hits.Load() == n {
		t.Fatalf("retry: %+v %v", r, err)
	}
	if it := root(t, p, "ip")[0]; it.Title != "Public IP  203.0.113.9" {
		t.Errorf("after retry = %+v", it)
	}
}

// TestPublicIPReal asks the real services (GOLAUNCHER_NETWORK_TESTS=1).
// The address is not logged.
func TestPublicIPReal(t *testing.T) {
	if os.Getenv("GOLAUNCHER_NETWORK_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_NETWORK_TESTS=1 to ask api.ipify.org and ifconfig.me")
	}
	for _, u := range append(slices.Clone(publicIPv4URLs), publicIPv6URLs...) {
		s := &System{IPv4URLs: []string{u}}
		a, err := s.PublicIP(t.Context(), false)
		t.Logf("%s over IPv4: valid %v, err %v", u, a.Is4(), err)
		if err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
}

func TestWiFi(t *testing.T) {
	p, h := newPlugin(t, Options{Backend: fixture(t)})
	items := root(t, p, "wifi")
	if got := titles(items); !slices.Equal(got, []string{"HomeNet", "CoffeeShop", "Office"}) {
		t.Fatalf("titles = %v", got)
	}
	if items[0].Accessory != "Connected" || items[0].Subtitle != "Connected · signal 82%" {
		t.Errorf("connected = %+v", items[0])
	}
	for _, it := range items {
		b, _ := json.Marshal(it)
		if strings.Contains(string(b), "fixture-password") {
			t.Fatalf("the password is in the item %s", it.ID)
		}
	}
	r, err := p.HandleAction(t.Context(), action(t, items[0], "copy-password").Do)
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := h.Clip.Read(); c.Text != "fixture-password" || !c.Exclude {
		t.Error("the password was not copied")
	}
	if !r.Hide || r.Toast == nil || r.Toast.Title != "Copied the password of HomeNet" || strings.Contains(r.Toast.Title+r.Toast.Message, "fixture-password") {
		t.Errorf("result = %+v", r.Toast)
	}
	h.Clip.Set(plugin.ClipboardContent{})
	r, _ = p.HandleAction(t.Context(), action(t, items[1], "copy-password").Do)
	if r.Toast.Style != plugin.ToastInfo || r.Toast.Title != "CoffeeShop is an open network without a password" {
		t.Errorf("open = %+v", r.Toast)
	}
	r, _ = p.HandleAction(t.Context(), action(t, items[2], "copy-password").Do)
	if r.Toast.Style != plugin.ToastFailure || r.Toast.Title != "Needs administrator rights" {
		t.Errorf("admin = %+v", r.Toast)
	}
	if c, _ := h.Clip.Read(); c.Text != "" {
		t.Error("something was copied")
	}
	if got := titles(root(t, p, "wifi offi")); !slices.Equal(got, []string{"Office"}) {
		t.Errorf("filter: %v", got)
	}

	none, _ := newPlugin(t, Options{Backend: &FixtureBackend{}})
	if got := titles(root(t, none, "wifi")); !slices.Equal(got, []string{"No Wi-Fi adapter"}) {
		t.Errorf("no wifi: %v", got)
	}
}

// protectedBackend refuses to end any process, as for a critical system
// process.
type protectedBackend struct{ *FixtureBackend }

func (protectedBackend) Kill(uint32, int64) error { return winnet.ErrProtected }

// TestKillChecksCreationTime ends a process by PID and creation time: a
// payload whose creation time no longer matches ends nothing.
func TestKillChecksCreationTime(t *testing.T) {
	b := &FixtureBackend{F: Fixture{
		Sockets:   []fixSocket{{Proto: "tcp", Local: netip.MustParseAddrPort("127.0.0.1:3000"), PID: 7070}},
		Processes: []fixProcess{{PID: 7070, Name: "fabrikam.exe", Path: `C:\Program Files\Fabrikam\fabrikam.exe`, Created: 133700000000000000}},
	}}
	p, _ := newPlugin(t, Options{Backend: b})
	items := root(t, p, "port 3000")
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	kill := action(t, items[0], "kill")
	var pl killPayload
	if err := json.Unmarshal(kill.Do.Payload, &pl); err != nil || pl.Created != 133700000000000000 {
		t.Fatalf("payload = %s (%v)", kill.Do.Payload, err)
	}
	// The PID now belongs to a process created later: nothing is ended.
	stale := callback("kill", "", cbKill, killPayload{PID: 7070, Name: "fabrikam.exe", Created: 133600000000000000}, "")
	r, err := p.HandleAction(t.Context(), stale.Do)
	if err != nil || r.Toast == nil || r.Toast.Style != plugin.ToastFailure || r.Toast.Title != "fabrikam.exe is no longer running" || len(b.Killed()) != 0 {
		t.Errorf("stale: %+v %v %v", r.Toast, err, b.Killed())
	}
	r, err = p.HandleAction(t.Context(), kill.Do)
	if err != nil || r.Toast.Title != "Ended fabrikam.exe (PID 7070)" || !slices.Equal(b.Killed(), []uint32{7070}) {
		t.Errorf("kill: %+v %v %v", r.Toast, err, b.Killed())
	}

	pp, _ := newPlugin(t, Options{Backend: protectedBackend{b}})
	r, _ = pp.HandleAction(t.Context(), kill.Do)
	if r.Toast == nil || r.Toast.Style != plugin.ToastFailure || r.Toast.Title != "fabrikam.exe is a protected system process and cannot be ended." {
		t.Errorf("protected: %+v", r.Toast)
	}
}

func TestLocalPortFromFixture(t *testing.T) {
	b := fixture(t)
	p, _ := newPlugin(t, Options{Backend: b})
	items := root(t, p, "port 8080")
	if len(items) != 1 || items[0].Title != "node.exe (PID 4242)" || items[0].Subtitle != "TCP 0.0.0.0:8080, TCP [::]:8080" {
		t.Fatalf("items = %+v", items)
	}
	kill := action(t, items[0], "kill")
	if kill.Style != plugin.StyleDestructive {
		t.Error("ending a process does not ask for confirmation")
	}
	r, err := p.HandleAction(t.Context(), kill.Do)
	if err != nil || r.Toast.Title != "Ended node.exe (PID 4242)" || !slices.Equal(b.Killed(), []uint32{4242}) {
		t.Errorf("kill: %+v %v %v", r.Toast, err, b.Killed())
	}
	// The PID now belongs to another process: nothing is ended.
	r, _ = p.HandleAction(t.Context(), callback("kill", "", cbKill, killPayload{PID: 5151, Name: "node.exe"}, "").Do)
	if r.Toast.Style != plugin.ToastFailure || len(b.Killed()) != 1 {
		t.Errorf("changed: %+v %v", r.Toast, b.Killed())
	}
	if got := titles(root(t, p, "port 9")); !slices.Equal(got, []string{"Nothing listens on port 9"}) {
		t.Errorf("free: %v", got)
	}
	// The system process gets no kill action.
	if it := root(t, p, "port 445")[0]; slices.ContainsFunc(it.Actions, func(a plugin.Action) bool { return a.ID == "kill" }) {
		t.Error("System can be ended")
	}
	v, err := p.View(t.Context(), plugin.ViewRequest{ID: toolPort, Payload: plugin.MustPayload(portTarget{})})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(v.Sections[0].Items); !slices.Equal(got, []string{"445  System (PID 4)", "5353  svchost.exe (PID 1880)",
		"5432  postgres.exe (PID 5151)", "8080  node.exe (PID 4242)"}) {
		t.Errorf("all ports: %v", got)
	}
	v, _ = p.View(t.Context(), plugin.ViewRequest{ID: toolPort, Arguments: map[string]string{"target": "5432"}})
	if v.Title != "Port 5432" || v.Sections[0].Items[0].Title != "postgres.exe (PID 5151)" {
		t.Errorf("argument: %+v", v)
	}
}

// TestLocalPortReal opens a port and finds this process listening on it.
func TestLocalPortReal(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	p, _ := newPlugin(t, Options{})
	items := root(t, p, fmt.Sprintf("port %d", port))
	exe := filepath.Base(os.Args[0])
	want := fmt.Sprintf("%s (PID %d)", exe, os.Getpid())
	if len(items) != 1 || !strings.EqualFold(items[0].Title, want) {
		t.Errorf("items = %v, want %s", titles(items), want)
	}
}

// TestRemotePortCheck checks a local listener and a closed port.
func TestRemotePortCheck(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	closedAddr := closed.Addr().String()
	closed.Close()

	p, _ := newPlugin(t, Options{})
	items := root(t, p, "port "+ln.Addr().String())
	if len(items) != 1 || items[0].Title != "Check whether "+ln.Addr().String()+" can be reached" {
		t.Fatalf("root = %v", titles(items))
	}
	push := action(t, items[0], "check").Do
	v, err := p.View(t.Context(), plugin.ViewRequest{ID: push.Target, Payload: push.Payload})
	if err != nil {
		t.Fatal(err)
	}
	it := v.Sections[0].Items[0]
	if it.Title != ln.Addr().String()+" is reachable" || !strings.HasPrefix(it.Subtitle, "Connected in ") || it.Icon.Tint != plugin.TintSuccess {
		t.Errorf("open = %+v", it)
	}
	v, _ = p.View(t.Context(), plugin.ViewRequest{ID: toolPort, Arguments: map[string]string{"target": closedAddr}})
	if it := v.Sections[0].Items[0]; it.Title != closedAddr+" can’t be reached" || it.Icon.Tint != plugin.TintDanger {
		t.Errorf("closed = %+v", it)
	}
}

func TestTasks(t *testing.T) {
	p, h := newPlugin(t, Options{Backend: offline{fixture(t), t}})
	items := root(t, p, "tracert example.com")
	r, err := p.HandleAction(t.Context(), action(t, items[0], "tracert").Do)
	if err != nil || r.Do == nil || r.Do.Kind != plugin.ActionShowTask || r.Do.Target == "" {
		t.Fatalf("tracert: %+v %v", r, err)
	}
	r, err = p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdFlushDNS})
	if err != nil {
		t.Fatal(err)
	}
	if r.Toast == nil || r.Toast.Title != "Flush DNS Cache: requires administrator rights" ||
		r.Toast.Message != "Confirm the Windows administrator prompt (UAC)." {
		t.Errorf("flush toast %+v", r.Toast)
	}
	if _, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdRenewIP}); err != nil {
		t.Fatal(err)
	}
	r, _ = p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: toolTracert,
		Values: map[string]jsontext.Value{"host": plugin.MustPayload("-d evil")}})
	if r.Toast == nil || r.Toast.Style != plugin.ToastFailure {
		t.Errorf("an invalid host ran: %+v", r)
	}
	h.WaitTasks()
	var got []string
	for _, c := range h.Runner.Commands() {
		line := strings.Join(c.Argv, " ")
		if c.Elevate {
			line += " elevate"
		}
		got = append(got, line)
	}
	slices.Sort(got) // tasks in different queues run in parallel
	// The ipconfig tasks ask for administrator rights (R68); tracert not.
	want := []string{"cmd.exe /d /c ipconfig /release && ipconfig /renew elevate", "ipconfig /flushdns elevate", "tracert example.com"}
	if !slices.Equal(got, want) {
		t.Errorf("commands = %q", got)
	}
	m := p.Manifest()
	i := slices.IndexFunc(m.Commands, func(c plugin.Command) bool { return c.Name == cmdRenewIP })
	if i < 0 || !m.Commands[i].Destructive {
		t.Error("renew-ip is not destructive")
	}
	for _, c := range m.Commands {
		if admin := c.Name == cmdFlushDNS || c.Name == cmdRenewIP; admin != (c.Subtitle == "%command.subtitleAdmin%") {
			t.Errorf("%s: subtitle %q", c.Name, c.Subtitle)
		}
	}
}

// TestTasksWhenAdmin: when golauncher runs as administrator, the ipconfig
// tasks run as usual (no UAC prompt, with their output).
func TestTasksWhenAdmin(t *testing.T) {
	f := fixture(t)
	f.F.Elevated = true
	p, h := newPlugin(t, Options{Backend: offline{f, t}})
	r, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdFlushDNS})
	if err != nil {
		t.Fatal(err)
	}
	if r.Toast == nil || r.Toast.Title != "Flush DNS Cache: running in the background" || r.Toast.Message != "" {
		t.Errorf("toast %+v", r.Toast)
	}
	if _, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdRenewIP}); err != nil {
		t.Fatal(err)
	}
	h.WaitTasks()
	cmds := h.Runner.Commands()
	if len(cmds) != 2 || cmds[0].Elevate || cmds[1].Elevate {
		t.Errorf("commands %+v", cmds)
	}
}
