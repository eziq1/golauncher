package network

// The platform-independent part of the macOS backend (network_darwin.go):
// parsing what netstat, scutil, networksetup, ipconfig and security print,
// and building and reading ICMP echo packets. Kept here so that it is
// tested with captured output on any platform.

import (
	"encoding/binary"
	"net/netip"
	"strconv"
	"strings"
)

// parseNetstatSockets reads `netstat -anv -p tcp` (or udp): the TCP
// listeners, or every UDP socket, with their PIDs. The PID column is found
// from the header, counted from the end of the line (UDP lines have no
// state column, so counting from the start would not do); a header
// "process:pid" (some macOS versions) gives "name:pid" values.
func parseNetstatSockets(out []byte) []Socket {
	var socks []Socket
	fromEnd, named := -1, false
	for line := range strings.Lines(string(out)) {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if f[0] == "Proto" {
			for i, h := range f {
				if h == "pid" || h == "process:pid" {
					fromEnd, named = len(f)-1-i, h == "process:pid"
				}
			}
			continue
		}
		proto := strings.ToLower(f[0])
		if fromEnd < 0 || len(f) < 5+fromEnd || !(strings.HasPrefix(proto, "tcp") || strings.HasPrefix(proto, "udp")) {
			continue
		}
		tcp := strings.HasPrefix(proto, "tcp")
		if tcp && (len(f) < 6 || f[5] != "LISTEN") {
			continue
		}
		v := f[len(f)-1-fromEnd]
		if named {
			if i := strings.LastIndexByte(v, ':'); i >= 0 {
				v = v[i+1:]
			}
		}
		pid, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			continue
		}
		local, ok := parseNetstatAddr(f[3], strings.TrimLeft(proto, "tcpud"))
		if !ok {
			continue
		}
		s := Socket{Proto: "udp", Local: local, PID: uint32(pid)}
		if tcp {
			s.Proto = "tcp"
		}
		socks = append(socks, s)
	}
	return socks
}

// parseNetstatAddr reads a netstat address, whose port follows the last
// dot: "127.0.0.1.8384", "::1.42050", "fe80::1%lo0.5353", "*.22000". fam
// is what follows the protocol ("4", "6", "46"): a "*" address is 0.0.0.0
// for "4" and :: otherwise (a dual-stack socket).
func parseNetstatAddr(s, fam string) (netip.AddrPort, bool) {
	i := strings.LastIndexByte(s, '.')
	if i < 0 {
		return netip.AddrPort{}, false
	}
	port, err := strconv.ParseUint(s[i+1:], 10, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}
	host := s[:i]
	var a netip.Addr
	if host == "*" {
		a = netip.IPv6Unspecified()
		if fam == "4" {
			a = netip.IPv4Unspecified()
		}
	} else if a, err = netip.ParseAddr(host); err != nil {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(a.Unmap(), uint16(port)), true
}

// parseRoutes reads `netstat -rn`: the default gateways by interface name.
// Gateways that are not addresses (link#18, a MAC address) are skipped, as
// is the zone of link-local ones.
func parseRoutes(out []byte) map[string][]netip.Addr {
	gw := map[string][]netip.Addr{}
	for line := range strings.Lines(string(out)) {
		f := strings.Fields(line)
		if len(f) < 4 || f[0] != "default" {
			continue
		}
		a, err := netip.ParseAddr(f[1])
		if err != nil {
			continue
		}
		a = a.WithZone("")
		ifname := f[3]
		if !contains(gw[ifname], a) {
			gw[ifname] = append(gw[ifname], a)
		}
	}
	return gw
}

func contains(as []netip.Addr, a netip.Addr) bool {
	for _, x := range as {
		if x == a {
			return true
		}
	}
	return false
}

// dnsConfig is the DNS configuration of an interface.
type dnsConfig struct {
	Servers []netip.Addr
	Search  string // the first search domain
}

// parseScutilDNS reads `scutil --dns`: the servers and search domain of
// each interface, from the scoped resolvers ("for scoped queries"), else
// from the resolvers that name an interface and no domain.
func parseScutilDNS(out []byte) map[string]dnsConfig {
	type resolver struct {
		domain  string
		ifname  string
		servers []netip.Addr
		search  string
	}
	var general, scoped []resolver
	var cur *resolver
	inScoped := false
	flush := func() {
		if cur == nil {
			return
		}
		if inScoped {
			scoped = append(scoped, *cur)
		} else {
			general = append(general, *cur)
		}
		cur = nil
	}
	for line := range strings.Lines(string(out)) {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "DNS configuration"):
			flush()
			inScoped = strings.Contains(t, "scoped")
			continue
		case strings.HasPrefix(t, "resolver #"):
			flush()
			cur = &resolver{}
			continue
		case cur == nil:
			continue
		}
		key, val, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch {
		case strings.HasPrefix(key, "nameserver["):
			if a, err := netip.ParseAddr(val); err == nil && !contains(cur.servers, a) {
				cur.servers = append(cur.servers, a)
			}
		case strings.HasPrefix(key, "search domain[") && cur.search == "":
			cur.search = val
		case key == "domain":
			cur.domain = val
		case key == "if_index":
			// "6 (en0)"
			if i, j := strings.IndexByte(val, '('), strings.IndexByte(val, ')'); i >= 0 && j > i {
				cur.ifname = val[i+1 : j]
			}
		}
	}
	flush()
	out2 := map[string]dnsConfig{}
	add := func(rs []resolver, needNoDomain bool) {
		for _, r := range rs {
			if r.ifname == "" || len(r.servers) == 0 || needNoDomain && r.domain != "" {
				continue
			}
			c := out2[r.ifname]
			for _, s := range r.servers {
				if !contains(c.Servers, s) {
					c.Servers = append(c.Servers, s)
				}
			}
			if c.Search == "" {
				c.Search = r.search
			}
			out2[r.ifname] = c
		}
	}
	if len(scoped) > 0 {
		add(scoped, false)
	} else {
		add(general, true)
	}
	return out2
}

// parsePreferredNetworks reads `networksetup -listpreferredwirelessnetworks
// <device>`: a heading, then a tab-indented network name per line.
func parsePreferredNetworks(out []byte) []string {
	var names []string
	for line := range strings.Lines(string(out)) {
		if !strings.HasPrefix(line, "\t") {
			continue
		}
		if n := strings.TrimRight(strings.TrimPrefix(line, "\t"), "\r\n"); n != "" {
			names = append(names, n)
		}
	}
	return names
}

// parseSummarySSID reads the SSID from `ipconfig getsummary <device>`; ""
// when it is not there or macOS redacted it (no Location permission).
func parseSummarySSID(out []byte) string {
	for line := range strings.Lines(string(out)) {
		k, v, ok := strings.Cut(strings.TrimSpace(line), " : ")
		if ok && k == "SSID" {
			v = strings.TrimSpace(v)
			if v == "<redacted>" {
				return ""
			}
			return v
		}
	}
	return ""
}

// signalQuality turns a received signal strength (dBm) into the quality in
// percent that Windows reports: -100 dBm or less is 0%, -50 dBm or more
// 100%, linear in between.
func signalQuality(rssi int) int {
	return min(max(2*(rssi+100), 0), 100)
}

// --- ICMP -------------------------------------------------------------------

// echoData is the payload of an echo request, as Windows ping sends it.
var echoData = []byte("abcdefghijklmnopqrstuvwabcdefghi")

// ICMP types.
const (
	icmpEchoReply      = 0
	icmpUnreachable    = 3
	icmpEcho           = 8
	icmpTimeExceeded   = 11
	icmp6Unreachable   = 1
	icmp6TimeExceeded  = 3
	icmp6Echo          = 128
	icmp6EchoReply     = 129
	icmpHeaderLen      = 8
	ipv4MinHeaderLen   = 20
	ipv6HeaderLen      = 40
	icmpChecksumOffset = 2
)

// buildEcho returns an echo request. The IPv4 checksum is computed here;
// the kernel fills in the ICMPv6 one.
func buildEcho(v6 bool, id, seq uint16, data []byte) []byte {
	b := make([]byte, icmpHeaderLen+len(data))
	b[0] = icmpEcho
	if v6 {
		b[0] = icmp6Echo
	}
	binary.BigEndian.PutUint16(b[4:], id)
	binary.BigEndian.PutUint16(b[6:], seq)
	copy(b[8:], data)
	if !v6 {
		binary.BigEndian.PutUint16(b[icmpChecksumOffset:], checksum(b))
	}
	return b
}

// checksum is the Internet checksum of b.
func checksum(b []byte) uint16 {
	var s uint32
	for i := 0; i+1 < len(b); i += 2 {
		s += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		s += uint32(b[len(b)-1]) << 8
	}
	for s>>16 != 0 {
		s = s&0xffff + s>>16
	}
	return ^uint16(s)
}

// stripIPv4 removes the IPv4 header macOS puts before what an ICMP
// datagram socket receives, and returns its TTL (0 when there is none).
func stripIPv4(b []byte) ([]byte, int) {
	if len(b) < ipv4MinHeaderLen || b[0]>>4 != 4 {
		return b, 0
	}
	hl := int(b[0]&0x0f) * 4
	if hl < ipv4MinHeaderLen || hl > len(b) {
		return b, 0
	}
	return b[hl:], int(b[8])
}

// parseEcho reads a received ICMP message (without the IP header) and
// reports whether it answers the request id/seq: an echo reply, or an
// error quoting the request. The status is one of the Status* codes.
func parseEcho(b []byte, v6 bool, id, seq uint16) (status uint32, ok bool) {
	if len(b) < icmpHeaderLen {
		return 0, false
	}
	typ, code := b[0], b[1]
	ours := func(m []byte) bool {
		return len(m) >= icmpHeaderLen && binary.BigEndian.Uint16(m[4:]) == id && binary.BigEndian.Uint16(m[6:]) == seq
	}
	// quoted is the ICMP header of the request an error message quotes.
	quoted := func() []byte {
		inner := b[icmpHeaderLen:]
		if v6 {
			if len(inner) < ipv6HeaderLen {
				return nil
			}
			return inner[ipv6HeaderLen:]
		}
		m, _ := stripIPv4(inner)
		if len(m) == len(inner) {
			return nil
		}
		return m
	}
	if !v6 {
		switch typ {
		case icmpEchoReply:
			return StatusSuccess, ours(b)
		case icmpUnreachable:
			q := quoted()
			if q == nil || q[0] != icmpEcho || !ours(q) {
				return 0, false
			}
			switch code {
			case 0:
				return StatusNetUnreachable, true
			case 2:
				return StatusProtUnreachable, true
			case 3:
				return StatusPortUnreachable, true
			}
			return StatusHostUnreachable, true
		case icmpTimeExceeded:
			q := quoted()
			return StatusTTLExpired, q != nil && q[0] == icmpEcho && ours(q)
		}
		return 0, false
	}
	switch typ {
	case icmp6EchoReply:
		return StatusSuccess, ours(b)
	case icmp6Unreachable:
		q := quoted()
		if q == nil || q[0] != icmp6Echo || !ours(q) {
			return 0, false
		}
		if code == 0 {
			return StatusNetUnreachable, true
		}
		return StatusHostUnreachable, true
	case icmp6TimeExceeded:
		q := quoted()
		return StatusTTLExpired, q != nil && q[0] == icmp6Echo && ours(q)
	}
	return 0, false
}

// --- Wi-Fi passwords --------------------------------------------------------

// keychainArgv is the command line that reads the password of the saved
// Wi-Fi network ssid from the System keychain. macOS asks for an
// administrator's name and password first.
func keychainArgv(ssid string) []string {
	return []string{"/usr/bin/security", "find-generic-password", "-D", "AirPort network password", "-a", ssid, "-w"}
}

// keychainError maps the outcome of keychainArgv (exit code, standard
// error) to this package's errors: no item is ErrNoPassword (an open
// network, or one whose password macOS keeps elsewhere); a declined or
// failed authorization is ErrNeedsAdmin.
func keychainError(code int, stderr string) error {
	low := strings.ToLower(stderr)
	switch {
	case code == 0:
		return nil
	case code == 44 || strings.Contains(low, "could not be found"):
		return ErrNoPassword
	case code == 128 || code == 51 || strings.Contains(low, "-128") || strings.Contains(low, "canceled") ||
		strings.Contains(low, "cancelled") || strings.Contains(low, "not allowed") || strings.Contains(low, "authorization"):
		return ErrNeedsAdmin
	}
	return &keychainFailure{code: code, msg: strings.TrimSpace(stderr)}
}

type keychainFailure struct {
	code int
	msg  string
}

func (e *keychainFailure) Error() string {
	if e.msg == "" {
		return "network: security exited with code " + strconv.Itoa(e.code)
	}
	return "network: security: " + e.msg
}
