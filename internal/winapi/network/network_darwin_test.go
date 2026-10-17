//go:build darwin

package network

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"
)

// Real pings go only to the loopback addresses; ports and processes are the
// test's own. Wi-Fi is read only with GOLAUNCHER_NETWORK_TESTS=1.

func TestPingLoopback(t *testing.T) {
	for _, a := range []string{"127.0.0.1", "::1"} {
		r, err := Ping(t.Context(), netip.MustParseAddr(a), 2*time.Second)
		if err != nil {
			t.Fatalf("%s: %v", a, err)
		}
		if r.Status != StatusSuccess || r.From != netip.MustParseAddr(a) || r.RTT <= 0 || r.RTT > 2*time.Second {
			t.Errorf("%s: %+v", a, r)
		}
		if a == "127.0.0.1" && r.TTL == 0 {
			t.Errorf("no TTL: %+v", r)
		}
	}
}

func TestPingCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Ping(ctx, netip.MustParseAddr("127.0.0.1"), time.Second); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

func TestSocketsFindsOwnListener(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	u, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	socks, err := Sockets()
	if err != nil {
		t.Fatal(err)
	}
	pid := uint32(os.Getpid())
	tcp := Socket{"tcp", netip.MustParseAddrPort(l.Addr().String()), pid}
	udp := Socket{"udp", netip.MustParseAddrPort(u.LocalAddr().String()), pid}
	if !slices.Contains(socks, tcp) || !slices.Contains(socks, udp) {
		t.Errorf("%v and %v not in %d sockets", tcp, udp, len(socks))
	}
	p := ProcessInfo(pid)
	if p.Name == "" || p.Path == "" || p.Created == 0 {
		t.Errorf("self = %+v", p)
	}
}

func TestAdaptersHaveLoopback(t *testing.T) {
	ads, err := Adapters()
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(ads, Adapter.Loopback)
	if i < 0 {
		t.Fatalf("no loopback in %+v", ads)
	}
	if lo := ads[i]; !lo.Up || !slices.Contains(lo.IPv4, netip.MustParsePrefix("127.0.0.1/8")) {
		t.Errorf("loopback = %+v", lo)
	}
}

func TestKillChild(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer cmd.Process.Kill()
	pid := uint32(cmd.Process.Pid)
	p := ProcessInfo(pid)
	if p.Name != "sleep" || p.Created == 0 {
		t.Fatalf("child = %+v", p)
	}
	if err := Kill(pid, p.Created+1); !errors.Is(err, ErrNotFound) {
		t.Errorf("stale: %v", err)
	}
	if err := Kill(pid, p.Created); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the child is still running")
	}
	if err := Kill(uint32(os.Getpid()), 0); !errors.Is(err, ErrProtected) {
		t.Errorf("self: %v", err)
	}
}

func TestWiFi(t *testing.T) {
	if os.Getenv("GOLAUNCHER_NETWORK_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_NETWORK_TESTS=1 to read the Wi-Fi state")
	}
	conns, err := WiFiConnections()
	if errors.Is(err, ErrNoWiFi) {
		t.Skip("no Wi-Fi")
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range conns {
		// Names are not logged.
		t.Logf("%s: signal %d%%, rate %d kbit/s, SSID known %v (hidden %v)", c.InterfaceGUID, c.Signal, c.TxKbps, c.SSID != "", c.SSIDHidden)
		if c.Signal <= 0 || c.SSIDHidden != (c.SSID == "") {
			t.Errorf("%+v", c)
		}
	}
	profiles, err := WiFiProfiles()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d saved networks", len(profiles))
}
