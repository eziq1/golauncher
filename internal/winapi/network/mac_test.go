package network

import (
	"encoding/binary"
	"errors"
	"maps"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

// The captures below are from macOS 15 with addresses and names replaced
// by documentation ones.

const netstatTCP = `Active Internet connections (including servers)
Proto Recv-Q Send-Q  Local Address          Foreign Address        (state)          rxbytes      txbytes  rhiwat  shiwat    pid   epid state  options           gencnt    flags   flags1 usecnt rtncnt fltrs
tcp4       0      0  192.0.2.10.55073       198.51.100.7.443       ESTABLISHED       278156      2014267  131072  131072  17472      0 00102 00000008 000000000001a62d 00000081 04000900      2      0 000000
tcp4       0      0  127.0.0.1.8384         *.*                    LISTEN                 0            0  131072  131072   2098      0 00100 00000006 0000000000002f1b 00000000 00002900      1      0 000000
tcp46      0      0  *.22000                *.*                    LISTEN                 0            0  131072  131072   2098      0 00100 00000206 0000000000002ecb 00000000 00002800      1      0 000000
tcp6       0      0  ::1.42050              *.*                    LISTEN                 0            0  131072  131072    992      0 00100 00000006 0000000000002128 00000001 00000800      1      0 000000
tcp4       0      0  *.5000                 *.*                    LISTEN                 0            0  131072  131072    611      0 00100 00000006 0000000000000a1c 00000000 00000900      1      0 000000
tcp6       0      0  fe80::1%lo0.631        *.*                    LISTEN                 0            0  131072  131072    401      0 00100 00000006 0000000000000a1d 00000000 00000900      1      0 000000
`

const netstatUDP = `Active Internet connections (including servers)
Proto Recv-Q Send-Q  Local Address          Foreign Address        (state)          rxbytes      txbytes  rhiwat  shiwat    pid   epid state  options           gencnt    flags   flags1 usecnt rtncnt fltrs
udp4       0      0  192.0.2.10.52425       203.0.113.3.443                           14782         5950 1048576   29040   1738      0 00102 00000000 000000000001e236 00000000 04200800      1      0 000002
udp46      0      0  *.5353                 *.*                                           0            0  786896    9216   1738      0 00100 00000204 00000000000088ac 00000000 00000800      1      0 000000
udp4       0      0  *.137                  *.*                                           0            0  786896    9216    305      0 00100 00000204 00000000000088ab 00000000 00000800      1      0 000002
`

// An older layout, with the process name before the PID.
const netstatNamed = `Active Internet connections (including servers)
Proto Recv-Q Send-Q  Local Address          Foreign Address        (state)      rhiwat  shiwat process:pid  state  options
tcp4       0      0  *.7000                 *.*                    LISTEN       131072  131072  ControlCe:611  00100 00000006
udp4       0      0  *.68                   *.*                                 786896    9216  configd:89     00100 00000204
`

func TestParseNetstatSockets(t *testing.T) {
	got := parseNetstatSockets([]byte(netstatTCP))
	want := []Socket{
		{"tcp", netip.MustParseAddrPort("127.0.0.1:8384"), 2098},
		{"tcp", netip.MustParseAddrPort("[::]:22000"), 2098},
		{"tcp", netip.MustParseAddrPort("[::1]:42050"), 992},
		{"tcp", netip.MustParseAddrPort("0.0.0.0:5000"), 611},
		{"tcp", netip.AddrPortFrom(netip.MustParseAddr("fe80::1%lo0"), 631), 401},
	}
	if !slices.Equal(got, want) {
		t.Errorf("tcp:\n got %v\nwant %v", got, want)
	}
	got = parseNetstatSockets([]byte(netstatUDP))
	want = []Socket{
		{"udp", netip.MustParseAddrPort("192.0.2.10:52425"), 1738},
		{"udp", netip.MustParseAddrPort("[::]:5353"), 1738},
		{"udp", netip.MustParseAddrPort("0.0.0.0:137"), 305},
	}
	if !slices.Equal(got, want) {
		t.Errorf("udp:\n got %v\nwant %v", got, want)
	}
	got = parseNetstatSockets([]byte(netstatNamed))
	want = []Socket{
		{"tcp", netip.MustParseAddrPort("0.0.0.0:7000"), 611},
		{"udp", netip.MustParseAddrPort("0.0.0.0:68"), 89},
	}
	if !slices.Equal(got, want) {
		t.Errorf("named:\n got %v\nwant %v", got, want)
	}
	if got := parseNetstatSockets([]byte("tcp4 0 0 *.80 *.* LISTEN 1 2 3\n")); len(got) != 0 {
		t.Errorf("no header: %v", got)
	}
}

func TestParseNetstatAddr(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1.8384": "127.0.0.1:8384",
		"*.22000":        "[::]:22000",
		"::1.42050":      "[::1]:42050",
	} {
		ap, ok := parseNetstatAddr(in, "46")
		if !ok || ap.String() != want {
			t.Errorf("%s = %v %v", in, ap, ok)
		}
	}
	if ap, _ := parseNetstatAddr("*.80", "4"); ap.String() != "0.0.0.0:80" {
		t.Errorf("IPv4 wildcard = %v", ap)
	}
	for _, bad := range []string{"", "nodot", "1.2.3.4.x", "host.80", "1.2.3.4.70000"} {
		if _, ok := parseNetstatAddr(bad, "4"); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

const netstatRoutes = `Routing tables

Internet:
Destination        Gateway            Flags               Netif Expire
default            192.0.2.1          UGScg                 en0
default            link#18            UCSIg               utun4
127                127.0.0.1          UCS                   lo0
192.0.2/24         link#6             UCS                   en0      !

Internet6:
Destination                             Gateway                                 Flags               Netif Expire
default                                 fe80::1%en0                             UGcg                  en0
default                                 fe80::%utun0                            UGcIg               utun0
default                                 2001:db8:a1e0::                         UGcIg               utun4
::1                                     ::1                                     UHL                   lo0
`

func TestParseRoutes(t *testing.T) {
	got := parseRoutes([]byte(netstatRoutes))
	want := map[string][]netip.Addr{
		"en0":   {netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("fe80::1")},
		"utun0": {netip.MustParseAddr("fe80::")},
		"utun4": {netip.MustParseAddr("2001:db8:a1e0::")},
	}
	if !maps.EqualFunc(got, want, slices.Equal) {
		t.Errorf("got %v", got)
	}
}

const scutilDNS = `DNS configuration

resolver #1
  search domain[0] : tailnet.example
  search domain[1] : lan
  nameserver[0] : 100.100.100.100
  if_index : 18 (utun4)
  flags    : Supplemental, Request A records, Request AAAA records
  reach    : 0x00000003 (Reachable,Transient Connection)
  order    : 101200

resolver #2
  nameserver[0] : 192.0.2.53
  nameserver[1] : 2001:db8::53
  if_index : 6 (en0)
  flags    : Request A records
  order    : 200000

resolver #3
  domain   : local
  options  : mdns
  timeout  : 5

DNS configuration (for scoped queries)

resolver #1
  search domain[0] : lan
  nameserver[0] : 192.0.2.53
  nameserver[1] : 2001:db8::53
  if_index : 6 (en0)
  flags    : Scoped, Request A records

resolver #2
  search domain[0] : tailnet.example
  nameserver[0] : 100.100.100.100
  if_index : 18 (utun4)
  flags    : Scoped, Request A records, Request AAAA records
`

func TestParseScutilDNS(t *testing.T) {
	got := parseScutilDNS([]byte(scutilDNS))
	en0, utun := got["en0"], got["utun4"]
	if !slices.Equal(en0.Servers, []netip.Addr{netip.MustParseAddr("192.0.2.53"), netip.MustParseAddr("2001:db8::53")}) || en0.Search != "lan" {
		t.Errorf("en0 = %+v", en0)
	}
	if !slices.Equal(utun.Servers, []netip.Addr{netip.MustParseAddr("100.100.100.100")}) || utun.Search != "tailnet.example" {
		t.Errorf("utun4 = %+v", utun)
	}
	if len(got) != 2 {
		t.Errorf("got %v", got)
	}
	// Without a scoped section, the resolvers that name an interface.
	general, _, _ := strings.Cut(scutilDNS, "DNS configuration (for scoped")
	got = parseScutilDNS([]byte(general))
	if !slices.Equal(got["en0"].Servers, en0.Servers) || got["utun4"].Search != "tailnet.example" {
		t.Errorf("general only: %v", got)
	}
}

func TestParsePreferredNetworks(t *testing.T) {
	out := "Preferred networks on en0:\n\tHome Net\n\tCafé Wi-Fi\n\t  spaced  \n\n"
	if got := parsePreferredNetworks([]byte(out)); !slices.Equal(got, []string{"Home Net", "Café Wi-Fi", "  spaced  "}) {
		t.Errorf("got %q", got)
	}
	if got := parsePreferredNetworks([]byte("en5 is not a Wi-Fi interface.\n")); len(got) != 0 {
		t.Errorf("error output: %q", got)
	}
}

func TestParseSummarySSID(t *testing.T) {
	summary := "<dictionary> {\n  BSSID : <redacted>\n  InterfaceType : WiFi\n  SSID : Home Net\n  Security : WPA2_PSK\n}\n"
	if got := parseSummarySSID([]byte(summary)); got != "Home Net" {
		t.Errorf("got %q", got)
	}
	redacted := "<dictionary> {\n  SSID : <redacted>\n}\n"
	if got := parseSummarySSID([]byte(redacted)); got != "" {
		t.Errorf("redacted: %q", got)
	}
	if got := parseSummarySSID(nil); got != "" {
		t.Errorf("empty: %q", got)
	}
}

func TestSignalQuality(t *testing.T) {
	for rssi, want := range map[int]int{-30: 100, -50: 100, -60: 80, -75: 50, -100: 0, -110: 0} {
		if got := signalQuality(rssi); got != want {
			t.Errorf("%d dBm = %d%%, want %d", rssi, got, want)
		}
	}
}

func TestBuildEcho(t *testing.T) {
	b := buildEcho(false, 0x1234, 7, echoData)
	if b[0] != icmpEcho || b[1] != 0 || binary.BigEndian.Uint16(b[4:]) != 0x1234 || binary.BigEndian.Uint16(b[6:]) != 7 || string(b[8:]) != string(echoData) {
		t.Errorf("packet = %x", b)
	}
	if checksum(b) != 0 {
		t.Errorf("checksum does not verify: %x", checksum(b))
	}
	b6 := buildEcho(true, 1, 2, []byte("x"))
	if b6[0] != icmp6Echo || b6[2] != 0 || b6[3] != 0 {
		t.Errorf("v6 packet = %x", b6)
	}
	// RFC 1071's example.
	if got := checksum([]byte{0x00, 0x01, 0xf2, 0x03, 0xf4, 0xf5, 0xf6, 0xf7}); got != ^uint16(0xddf2) {
		t.Errorf("checksum = %x", got)
	}
	if got := checksum([]byte{0xff}); got != ^uint16(0xff00) {
		t.Errorf("odd length = %x", got)
	}
}

// ipv4 wraps an ICMP message in an IPv4 header with ttl.
func ipv4(ttl byte, msg []byte) []byte {
	h := make([]byte, 20)
	h[0] = 0x45
	h[8] = ttl
	h[9] = 1
	return append(h, msg...)
}

func TestParseEcho(t *testing.T) {
	req := buildEcho(false, 9, 3, echoData)
	reply := append([]byte(nil), req...)
	reply[0] = icmpEchoReply
	msg, ttl := stripIPv4(ipv4(57, reply))
	if ttl != 57 {
		t.Errorf("ttl = %d", ttl)
	}
	if st, ok := parseEcho(msg, false, 9, 3); !ok || st != StatusSuccess {
		t.Errorf("reply: %d %v", st, ok)
	}
	if _, ok := parseEcho(msg, false, 9, 4); ok {
		t.Error("another sequence matched")
	}
	if _, ok := parseEcho(msg, false, 8, 3); ok {
		t.Error("another identifier matched")
	}
	// Errors quote the request's IP header and first 8 bytes.
	quote := ipv4(1, req[:8])
	for code, want := range map[byte]uint32{0: StatusNetUnreachable, 1: StatusHostUnreachable, 3: StatusPortUnreachable, 13: StatusHostUnreachable} {
		e := append([]byte{icmpUnreachable, code, 0, 0, 0, 0, 0, 0}, quote...)
		if st, ok := parseEcho(e, false, 9, 3); !ok || st != want {
			t.Errorf("unreachable %d: %d %v", code, st, ok)
		}
	}
	te := append([]byte{icmpTimeExceeded, 0, 0, 0, 0, 0, 0, 0}, quote...)
	if st, ok := parseEcho(te, false, 9, 3); !ok || st != StatusTTLExpired {
		t.Errorf("time exceeded: %d %v", st, ok)
	}
	if _, ok := parseEcho(te, false, 1, 1); ok {
		t.Error("someone else's time exceeded matched")
	}
	if _, ok := parseEcho([]byte{icmpTimeExceeded, 0, 0, 0}, false, 9, 3); ok {
		t.Error("short message matched")
	}
	if _, ok := parseEcho(req, false, 9, 3); ok {
		t.Error("our own request matched")
	}
	// No IPv4 header (Linux-style sockets) is fine too.
	if m, ttl := stripIPv4(reply); len(m) != len(reply) || ttl != 0 {
		t.Error("stripped a header that is not there")
	}

	req6 := buildEcho(true, 5, 6, echoData)
	reply6 := append([]byte(nil), req6...)
	reply6[0] = icmp6EchoReply
	if st, ok := parseEcho(reply6, true, 5, 6); !ok || st != StatusSuccess {
		t.Errorf("v6 reply: %d %v", st, ok)
	}
	quote6 := append(make([]byte, 40), req6[:8]...)
	un6 := append([]byte{icmp6Unreachable, 3, 0, 0, 0, 0, 0, 0}, quote6...)
	if st, ok := parseEcho(un6, true, 5, 6); !ok || st != StatusHostUnreachable {
		t.Errorf("v6 unreachable: %d %v", st, ok)
	}
	te6 := append([]byte{icmp6TimeExceeded, 0, 0, 0, 0, 0, 0, 0}, quote6...)
	if st, ok := parseEcho(te6, true, 5, 6); !ok || st != StatusTTLExpired {
		t.Errorf("v6 time exceeded: %d %v", st, ok)
	}
}

func TestKeychain(t *testing.T) {
	argv := keychainArgv("Home Net")
	if !slices.Equal(argv, []string{"/usr/bin/security", "find-generic-password", "-D", "AirPort network password", "-a", "Home Net", "-w"}) {
		t.Errorf("argv = %q", argv)
	}
	if keychainError(0, "") != nil {
		t.Error("success is an error")
	}
	if err := keychainError(44, "security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.\n"); !errors.Is(err, ErrNoPassword) {
		t.Errorf("not found: %v", err)
	}
	for _, msg := range []string{"security: SecKeychainItemCopyAttributesAndData: User canceled the operation.", "User interaction is not allowed.", "(-128)"} {
		if err := keychainError(51, msg); !errors.Is(err, ErrNeedsAdmin) {
			t.Errorf("%q: %v", msg, err)
		}
	}
	err := keychainError(1, "something else")
	if err == nil || errors.Is(err, ErrNeedsAdmin) || errors.Is(err, ErrNoPassword) || err.Error() != "network: security: something else" {
		t.Errorf("other: %v", err)
	}
	if err := keychainError(9, ""); err == nil || err.Error() != "network: security exited with code 9" {
		t.Errorf("no message: %v", err)
	}
}
