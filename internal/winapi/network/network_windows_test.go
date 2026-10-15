package network

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"slices"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// realNetwork reports whether tests that read real Wi-Fi state may run.
func realNetwork(t *testing.T) {
	t.Helper()
	if os.Getenv("GOLAUNCHER_NETWORK_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_NETWORK_TESTS=1 to run")
	}
}

func TestAdaptersHaveLoopback(t *testing.T) {
	ads, err := Adapters()
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(ads, Adapter.Loopback)
	if i < 0 {
		t.Fatalf("no loopback adapter among %d", len(ads))
	}
	if !slices.Contains(ads[i].IPv4, netip.MustParsePrefix("127.0.0.1/8")) {
		t.Errorf("loopback IPv4 = %v", ads[i].IPv4)
	}
}

// TestConvertAdapters converts a hand-built GetAdaptersAddresses list.
func TestConvertAdapters(t *testing.T) {
	sa4 := func(a [4]byte) windows.SocketAddress {
		raw := &syscall.RawSockaddrAny{}
		p := (*windows.RawSockaddrInet4)(unsafe.Pointer(raw))
		p.Family, p.Addr = windows.AF_INET, a
		return windows.SocketAddress{Sockaddr: raw, SockaddrLength: int32(unsafe.Sizeof(*p))}
	}
	sa6 := func(s string) windows.SocketAddress {
		raw := &syscall.RawSockaddrAny{}
		p := (*windows.RawSockaddrInet6)(unsafe.Pointer(raw))
		p.Family, p.Addr = windows.AF_INET6, netip.MustParseAddr(s).As16()
		return windows.SocketAddress{Sockaddr: raw, SockaddrLength: int32(unsafe.Sizeof(*p))}
	}
	name, _ := windows.UTF16PtrFromString("Wi-Fi")
	desc, _ := windows.UTF16PtrFromString("Test Wireless")
	guid := []byte("{0F0E0D0C-0B0A-0908-0706-050403020100}\x00")
	u6 := &windows.IpAdapterUnicastAddress{Address: sa6("fe80::1"), OnLinkPrefixLength: 64}
	u4 := &windows.IpAdapterUnicastAddress{Address: sa4([4]byte{192, 168, 1, 23}), OnLinkPrefixLength: 24, Next: u6}
	a := &windows.IpAdapterAddresses{
		FriendlyName: name, Description: desc, AdapterName: &guid[0],
		IfType: IfTypeWireless, OperStatus: windows.IfOperStatusUp,
		PhysicalAddress: [8]byte{0xaa, 0xbb, 0xcc, 0x01, 0x02, 0x03}, PhysicalAddressLength: 6,
		FirstUnicastAddress:   u4,
		FirstGatewayAddress:   &windows.IpAdapterGatewayAddress{Address: sa4([4]byte{192, 168, 1, 1})},
		FirstDnsServerAddress: &windows.IpAdapterDnsServerAdapter{Address: sa4([4]byte{1, 1, 1, 1})},
		ReceiveLinkSpeed:      ^uint64(0),
	}
	got := convertAdapters(a)
	if len(got) != 1 {
		t.Fatalf("%d adapters", len(got))
	}
	g := got[0]
	if g.Name != "Wi-Fi" || g.Description != "Test Wireless" || g.GUID != string(guid[:len(guid)-1]) || !g.Up || !g.Wireless() ||
		g.MAC != "AA-BB-CC-01-02-03" || g.SpeedBps != 0 {
		t.Errorf("adapter = %+v", g)
	}
	if !slices.Equal(g.IPv4, []netip.Prefix{netip.MustParsePrefix("192.168.1.23/24")}) ||
		!slices.Equal(g.IPv6, []netip.Prefix{netip.MustParsePrefix("fe80::1/64")}) ||
		!slices.Equal(g.Gateways, []netip.Addr{netip.MustParseAddr("192.168.1.1")}) ||
		!slices.Equal(g.DNS, []netip.Addr{netip.MustParseAddr("1.1.1.1")}) {
		t.Errorf("addresses = %v %v %v %v", g.IPv4, g.IPv6, g.Gateways, g.DNS)
	}
}

func TestPingLoopback(t *testing.T) {
	r, err := Ping(t.Context(), netip.MustParseAddr("127.0.0.1"), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusSuccess || r.From != netip.MustParseAddr("127.0.0.1") || r.RTT > time.Second || r.TTL != 128 {
		t.Errorf("reply = %+v", r)
	}
}

func TestPingLoopbackIPv6(t *testing.T) {
	r, err := Ping(t.Context(), netip.MustParseAddr("::1"), 2*time.Second)
	if err != nil {
		t.Skipf("no IPv6: %v", err)
	}
	if r.Status != StatusSuccess || r.From != netip.MustParseAddr("::1") || r.RTT > time.Second {
		t.Errorf("reply = %+v", r)
	}
}

// TestPingUnreachable pings an address of TEST-NET-1 (RFC 5737), which is
// never routed: it times out or is unreachable, but is not an error.
func TestPingUnreachable(t *testing.T) {
	if os.Getenv("GOLAUNCHER_NETWORK_TESTS") != "1" {
		t.Skip("sends a packet towards the default gateway; set GOLAUNCHER_NETWORK_TESTS=1")
	}
	r, err := Ping(t.Context(), netip.MustParseAddr("192.0.2.1"), 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status == StatusSuccess {
		t.Errorf("reply = %+v", r)
	}
}

// TestSocketsFindOwnListener opens a local TCP and UDP port and finds this
// process as their owner.
func TestSocketsFindOwnListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	socks, err := Sockets()
	if err != nil {
		t.Fatal(err)
	}
	pid := uint32(os.Getpid())
	want := []Socket{
		{Proto: "tcp", Local: ln.Addr().(*net.TCPAddr).AddrPort(), PID: pid},
		{Proto: "udp", Local: pc.LocalAddr().(*net.UDPAddr).AddrPort(), PID: pid},
	}
	for _, w := range want {
		if !slices.Contains(socks, w) {
			t.Errorf("%+v not among the %d sockets", w, len(socks))
		}
	}
	p := ProcessInfo(pid)
	exe, _ := os.Executable()
	if p.Path == "" || !sameFile(p.Path, exe) {
		t.Errorf("process = %+v, want %s", p, exe)
	}
}

func sameFile(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

func TestProcessInfoSystem(t *testing.T) {
	if p := ProcessInfo(4); p.Name != "System" {
		t.Errorf("pid 4 = %+v", p)
	}
}

// TestKillChild ends a child process this test started, and nothing else.
func TestKillChild(t *testing.T) {
	cmd := exec.Command(os.Getenv("SystemRoot")+`\System32\PING.EXE`, "-n", "30", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if err := Kill(uint32(cmd.Process.Pid)); err != nil {
		cmd.Process.Kill()
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the child did not end")
	}
	if err := Kill(uint32(os.Getpid())); !errors.Is(err, ErrNeedsAdmin) {
		t.Errorf("killing itself: %v", err)
	}
}

func TestWiFiConnectionsNoError(t *testing.T) {
	realNetwork(t)
	conns, err := WiFiConnections()
	if errors.Is(err, ErrNoWiFi) {
		t.Skip("no Wi-Fi")
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range conns {
		// The SSID is not logged.
		t.Logf("connection: signal %d%%, ssid length %d", c.Signal, len(c.SSID))
		if c.Signal < 0 || c.Signal > 100 || c.InterfaceGUID == "" {
			t.Errorf("bad connection: signal %d, guid %q", c.Signal, c.InterfaceGUID)
		}
	}
}

// TestWiFiPlaintext reports whether the plain text key of a saved profile
// can be read with the current rights, and how. It never logs a key or a
// network name.
func TestWiFiPlaintext(t *testing.T) {
	realNetwork(t)
	profiles, err := WiFiProfiles()
	if errors.Is(err, ErrNoWiFi) {
		t.Skip("no Wi-Fi")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d profiles, elevated: %v", len(profiles), windows.GetCurrentProcessToken().IsElevated())
	tried := 0
	for _, p := range profiles {
		k, err := ReadWiFiKey(p.InterfaceGUID, p.Name)
		switch {
		case errors.Is(err, ErrNoPassword):
			continue
		case errors.Is(err, ErrNeedsAdmin):
			t.Logf("profile %d: plain text key: no (needs administrator)", tried)
		case err != nil:
			t.Errorf("profile %d: %v", tried, err)
		default:
			t.Logf("profile %d: plain text key: yes, via %s (length hidden)", tried, k.Source)
			nk, err := netshKey(p.Name)
			t.Logf("profile %d: netsh export fallback gives the same key: %v (err: %v)", tried, err == nil && nk.Key == k.Key, err)
		}
		tried++
		if tried == 3 {
			break
		}
	}
}
