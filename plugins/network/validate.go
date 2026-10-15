package network

import (
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Input is checked before anything is sent (docs/network-tools.md §1): a
// malformed host, port or server gives a hint item and no request.

// problem is an input error; msg translates it.
type problem struct {
	msg func(l plugin.Localizer) string
}

func bad(msg func(l plugin.Localizer) string) *problem { return &problem{msg} }

// parseHost checks a host name or an IP address ("[::1]" is accepted). For
// DNS names underscores are allowed ("_sip._tcp.example.com").
func parseHost(s string, dnsName bool) (string, *problem) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", bad(func(l plugin.Localizer) string { return l.T("invalid.emptyHost") })
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		s = s[1 : len(s)-1]
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a.String(), nil
	}
	if !validHostname(s, dnsName) {
		return "", bad(func(l plugin.Localizer) string { return l.T("invalid.host", "host", s) })
	}
	return strings.ToLower(s), nil
}

// validHostname reports whether s is a syntactically valid host name: at
// most 253 characters, labels of 1-63 letters, digits and hyphens that do
// not start or end with a hyphen, not all numeric (a mistyped IPv4
// address).
func validHostname(s string, underscore bool) bool {
	s = strings.TrimSuffix(s, ".")
	if s == "" || len(s) > 253 {
		return false
	}
	numeric := true
	for label := range strings.SplitSeq(s, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range []byte(label) {
			switch {
			case c >= '0' && c <= '9':
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-':
				numeric = false
			case c == '_' && underscore:
				numeric = false
			default:
				return false
			}
		}
	}
	return !numeric
}

// parsePort checks a port number (1-65535).
func parsePort(s string) (uint16, *problem) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, bad(func(l plugin.Localizer) string { return l.T("invalid.portNumber", "port", s) })
	}
	if n < 1 || n > 65535 {
		return 0, bad(func(l plugin.Localizer) string { return l.T("invalid.portRange", "port", s) })
	}
	return uint16(n), nil
}

// portTarget is what follows the port prefix.
type portTarget struct {
	// Host is "" for a local port.
	Host string `json:"host,omitempty"`
	Port uint16 `json:"port,omitzero"`
}

// address is "host:port" for a remote target.
func (t portTarget) address() string {
	return net.JoinHostPort(t.Host, strconv.Itoa(int(t.Port)))
}

// parsePortTarget parses "8080" (a local port), "host:port", "[v6]:port"
// or "host port". An empty text is the list of all listening ports.
func parsePortTarget(s string) (portTarget, *problem) {
	s = strings.TrimSpace(s)
	if s == "" {
		return portTarget{}, nil
	}
	if f := strings.Fields(s); len(f) == 2 {
		s = net.JoinHostPort(strings.Trim(f[0], "[]"), f[1])
	} else if len(f) > 2 {
		return portTarget{}, bad(func(l plugin.Localizer) string { return l.T("invalid.portTarget", "text", s) })
	}
	if !strings.ContainsAny(s, ":") || isAllDigits(s) {
		p, err := parsePort(s)
		return portTarget{Port: p}, err
	}
	h, ps, err := net.SplitHostPort(s)
	if err != nil {
		return portTarget{}, bad(func(l plugin.Localizer) string { return l.T("invalid.portTarget", "text", s) })
	}
	host, prob := parseHost(h, false)
	if prob != nil {
		return portTarget{}, prob
	}
	p, prob := parsePort(ps)
	if prob != nil {
		return portTarget{}, prob
	}
	return portTarget{Host: host, Port: p}, nil
}

func isAllDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// dnsQuery is a parsed "name [@server]".
type dnsQuery struct {
	Name string `json:"name"`
	// Server is "host:port", "" for the system resolver.
	Server string `json:"server,omitempty"`
	// Reverse is set when Name is an IP address (a PTR lookup).
	Reverse bool `json:"reverse,omitzero"`
}

// String is the query as typed: "example.com @1.1.1.1".
func (q dnsQuery) String() string {
	if q.Server == "" {
		return q.Name
	}
	return q.Name + " @" + q.serverName()
}

// serverName is the server without the default port.
func (q dnsQuery) serverName() string {
	h, p, err := net.SplitHostPort(q.Server)
	if err != nil || p != "53" {
		return q.Server
	}
	if strings.Contains(h, ":") {
		return "[" + h + "]"
	}
	return h
}

// parseDNSQuery parses "name [@server]"; the server may be an IP address
// or host name with an optional port ("1.1.1.1", "[2606:4700::1111]:53",
// "dns.google").
func parseDNSQuery(s string) (dnsQuery, *problem) {
	var q dnsQuery
	var server string
	for f := range strings.FieldsSeq(s) {
		if rest, ok := strings.CutPrefix(f, "@"); ok {
			if server != "" {
				return q, bad(func(l plugin.Localizer) string { return l.T("invalid.dnsQuery", "text", s) })
			}
			server = rest
			if server == "" {
				return q, bad(func(l plugin.Localizer) string { return l.T("invalid.dnsServer", "server", f) })
			}
			continue
		}
		if q.Name != "" {
			return q, bad(func(l plugin.Localizer) string { return l.T("invalid.dnsQuery", "text", s) })
		}
		q.Name = f
	}
	if q.Name == "" {
		return q, bad(func(l plugin.Localizer) string { return l.T("invalid.emptyHost") })
	}
	name, prob := parseHost(q.Name, true)
	if prob != nil {
		return q, prob
	}
	q.Name = name
	if _, err := netip.ParseAddr(name); err == nil {
		q.Reverse = true
	}
	if server != "" {
		h, p := server, "53"
		if sh, sp, err := net.SplitHostPort(server); err == nil {
			h, p = sh, sp
		}
		host, prob := parseHost(h, false)
		if prob != nil {
			return q, bad(func(l plugin.Localizer) string { return l.T("invalid.dnsServer", "server", server) })
		}
		if _, prob := parsePort(p); prob != nil {
			return q, bad(func(l plugin.Localizer) string { return l.T("invalid.dnsServer", "server", server) })
		}
		q.Server = net.JoinHostPort(host, p)
	}
	return q, nil
}
