package network

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	winnet "github.com/eziq1/golauncher/internal/winapi/network"
	"github.com/eziq1/golauncher/internal/winapi/process"
)

// Backend is everything the plugin reads from the system and the network.
// System returns the real one; FixtureBackend one for fake environments
// and tests.
type Backend interface {
	Adapters(ctx context.Context) ([]winnet.Adapter, error)
	WiFiConnections(ctx context.Context) ([]winnet.WiFiConnection, error)
	WiFiProfiles(ctx context.Context) ([]winnet.WiFiProfile, error)
	// WiFiKey reads the key of a saved profile (see winnet.ReadWiFiKey).
	WiFiKey(ctx context.Context, ifaceGUID, profile string) (winnet.WiFiKey, error)
	// PublicIP asks a web service for the public address of the IPv4 (or
	// IPv6) connection.
	PublicIP(ctx context.Context, v6 bool) (netip.Addr, error)
	// Resolve returns an address of host, IPv4 preferred.
	Resolve(ctx context.Context, host string) (netip.Addr, error)
	// Ping sends one ICMP echo request.
	Ping(ctx context.Context, addr netip.Addr, timeout time.Duration) (winnet.EchoReply, error)
	// LookupDNS looks up the records of type typ ("A", "AAAA", "CNAME",
	// "MX", "TXT", "NS", "PTR") of name, at server ("host:port") or with
	// the system resolver when server is "".
	LookupDNS(ctx context.Context, name, typ, server string) ([]Record, error)
	// Dial opens a TCP connection to address ("host:port") and closes it,
	// returning the time it took and the address connected to.
	Dial(ctx context.Context, address string, timeout time.Duration) (time.Duration, string, error)
	// Sockets returns the TCP listeners and bound UDP sockets.
	Sockets(ctx context.Context) ([]winnet.Socket, error)
	// Process describes process pid, with its creation time when known.
	Process(pid uint32) winnet.Process
	// Kill ends process pid. A non-zero created (Process.Created) guards
	// against a PID reused by another process: winnet.ErrNotFound.
	Kill(pid uint32, created int64) error
	// Elevated reports whether golauncher runs as administrator; when it
	// does not, the DNS flush and the IP renewal ask for elevation
	// (ProcessSpec.Elevate).
	Elevated() bool
}

// Record is a DNS record.
type Record struct {
	Type  string `json:"type"`
	Value string `json:"value"`
	// Pref is the preference of an MX record.
	Pref uint16 `json:"pref,omitzero"`
}

// errNotFound reports a DNS name or record that does not exist.
var errNotFound = errors.New("not found")

// Public IP services (verified 2026-10-15: free, no API key, see
// docs/network-tools.md). The IPv4 lookups are made over IPv4 and the IPv6
// lookups over IPv6, so a dual-stack endpoint answers with the right
// family.
var (
	publicIPv4URLs = []string{"https://api.ipify.org?format=json", "https://ifconfig.me/ip"}
	publicIPv6URLs = []string{"https://api64.ipify.org?format=json", "https://ifconfig.me/ip"}
)

// System is the backend of the real system.
type System struct {
	// IPv4URLs and IPv6URLs override the public IP services (tests).
	IPv4URLs, IPv6URLs []string
	// Transport overrides the HTTP transport (tests); its dialer is not
	// forced to one address family.
	Transport http.RoundTripper
}

var _ Backend = (*System)(nil)

// Elevated implements Backend.
func (s *System) Elevated() bool { return process.IsElevated() }

func (s *System) Adapters(context.Context) ([]winnet.Adapter, error) { return winnet.Adapters() }

func (s *System) WiFiConnections(context.Context) ([]winnet.WiFiConnection, error) {
	return winnet.WiFiConnections()
}

func (s *System) WiFiProfiles(context.Context) ([]winnet.WiFiProfile, error) {
	return winnet.WiFiProfiles()
}

func (s *System) WiFiKey(_ context.Context, iface, profile string) (winnet.WiFiKey, error) {
	return winnet.ReadWiFiKey(iface, profile)
}

func (s *System) Sockets(context.Context) ([]winnet.Socket, error) { return winnet.Sockets() }

func (s *System) Process(pid uint32) winnet.Process { return winnet.ProcessInfo(pid) }

func (s *System) Kill(pid uint32, created int64) error { return winnet.Kill(pid, created) }

func (s *System) Ping(ctx context.Context, addr netip.Addr, timeout time.Duration) (winnet.EchoReply, error) {
	return winnet.Ping(ctx, addr, timeout)
}

func (s *System) PublicIP(ctx context.Context, v6 bool) (netip.Addr, error) {
	urls, network := publicIPv4URLs, "tcp4"
	if v6 {
		urls, network = publicIPv6URLs, "tcp6"
	}
	if s.IPv4URLs != nil && !v6 {
		urls = s.IPv4URLs
	}
	if s.IPv6URLs != nil && v6 {
		urls = s.IPv6URLs
	}
	tr := s.Transport
	if tr == nil {
		d := &net.Dialer{Timeout: 5 * time.Second}
		tr = &http.Transport{
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				return d.DialContext(ctx, network, addr)
			},
			TLSHandshakeTimeout: 5 * time.Second,
		}
		defer tr.(*http.Transport).CloseIdleConnections()
	}
	client := &http.Client{Transport: tr, Timeout: 6 * time.Second}
	var errs []error
	for _, u := range urls {
		a, err := fetchIP(ctx, client, u)
		if err == nil && a.Is6() == v6 {
			return a, nil
		}
		if err == nil {
			err = fmt.Errorf("%s: got %s", u, a)
		}
		errs = append(errs, err)
		if ctx.Err() != nil {
			break
		}
	}
	return netip.Addr{}, errors.Join(errs...)
}

// fetchIP reads an address from url, which answers either {"ip": "…"} or
// the plain address.
func fetchIP(ctx context.Context, client *http.Client, url string) (netip.Addr, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return netip.Addr{}, err
	}
	req.Header.Set("User-Agent", "golauncher")
	req.Header.Set("Accept", "application/json, text/plain")
	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("%s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return netip.Addr{}, err
	}
	text := strings.TrimSpace(string(body))
	if strings.HasPrefix(text, "{") {
		var v struct {
			IP string `json:"ip"`
		}
		if err := json.Unmarshal([]byte(text), &v); err != nil {
			return netip.Addr{}, fmt.Errorf("%s: %w", url, err)
		}
		text = v.IP
	}
	a, err := netip.ParseAddr(text)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%s: no address in the answer", url)
	}
	return a.Unmap(), nil
}

func (s *System) Resolve(ctx context.Context, host string) (netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return a, nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return netip.Addr{}, err
	}
	return preferIPv4(addrs)
}

// preferIPv4 returns the first IPv4 address, else the first address.
func preferIPv4(addrs []netip.Addr) (netip.Addr, error) {
	if len(addrs) == 0 {
		return netip.Addr{}, errNotFound
	}
	if i := slices.IndexFunc(addrs, func(a netip.Addr) bool { return a.Unmap().Is4() }); i >= 0 {
		return addrs[i].Unmap(), nil
	}
	return addrs[0], nil
}

// resolver returns the resolver for server ("" for the system's).
func resolver(server string) *net.Resolver {
	if server == "" {
		return net.DefaultResolver
	}
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, server)
	}}
}

func (s *System) LookupDNS(ctx context.Context, name, typ, server string) ([]Record, error) {
	r := resolver(server)
	var out []Record
	add := func(v string) { out = append(out, Record{Type: typ, Value: v}) }
	var err error
	switch typ {
	case "A", "AAAA":
		network := "ip4"
		if typ == "AAAA" {
			network = "ip6"
		}
		var addrs []netip.Addr
		addrs, err = r.LookupNetIP(ctx, network, name)
		for _, a := range addrs {
			add(a.Unmap().String())
		}
	case "CNAME":
		var c string
		c, err = r.LookupCNAME(ctx, name)
		if err == nil && !strings.EqualFold(strings.TrimSuffix(c, "."), strings.TrimSuffix(name, ".")) {
			add(c)
		}
	case "MX":
		var mx []*net.MX
		mx, err = r.LookupMX(ctx, name)
		for _, m := range mx {
			out = append(out, Record{Type: typ, Value: m.Host, Pref: m.Pref})
		}
	case "TXT":
		var txt []string
		txt, err = r.LookupTXT(ctx, name)
		for _, t := range txt {
			add(t)
		}
	case "NS":
		var ns []*net.NS
		ns, err = r.LookupNS(ctx, name)
		for _, n := range ns {
			add(n.Host)
		}
	case "PTR":
		var names []string
		names, err = r.LookupAddr(ctx, name)
		for _, n := range names {
			add(n)
		}
	default:
		return nil, fmt.Errorf("unknown record type %s", typ)
	}
	if dnsErr, ok := errors.AsType[*net.DNSError](err); ok && dnsErr.IsNotFound {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *System) Dial(ctx context.Context, address string, timeout time.Duration) (time.Duration, string, error) {
	d := net.Dialer{Timeout: timeout}
	start := time.Now()
	c, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return 0, "", err
	}
	elapsed := time.Since(start)
	remote := c.RemoteAddr().String()
	c.Close()
	return elapsed, remote, nil
}
