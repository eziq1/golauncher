//go:build darwin

package network

// The macOS backend. Interfaces and addresses come from the net package,
// their names ("Wi-Fi", "Thunderbolt Bridge") and kinds from
// SystemConfiguration, gateways from `netstat -rn`, DNS servers from
// `scutil --dns`. The Wi-Fi connection comes from CoreWLAN: since macOS 14
// it gives the network name (SSID) only to apps the user allowed to use
// Location Services, so without that the SSID is empty and SSIDHidden is
// set. Saved networks come from networksetup; their passwords from the
// System keychain (security, after macOS asks for an administrator's
// password). Ping uses an ICMP datagram socket, which macOS allows without
// root; listening ports come from `netstat -anv`, which shows the PIDs of
// every user's sockets.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"golang.org/x/sys/unix"

	"github.com/eziq1/golauncher/internal/darwin/ns"
	"github.com/eziq1/golauncher/internal/winapi/process"
)

// commandTimeout bounds each system tool run here.
const commandTimeout = 2 * time.Second

// output runs a system tool and returns its standard output. Tests replace
// it with captured output.
var output = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

// --- Interfaces ---------------------------------------------------------------

// scInterface is a network interface known to SystemConfiguration.
type scInterface struct {
	name string // localized display name: "Wi-Fi", "Thunderbolt Bridge"
	kind string // "IEEE80211", "Ethernet", "Bridge", …
}

// scFuncs are the SystemConfiguration functions used here.
type scFuncs struct {
	copyAll        func() ns.ID
	bsd, disp, typ func(ns.ID) ns.ID
}

var loadSC = sync.OnceValues(func() (*scFuncs, error) {
	if err := ns.Load(); err != nil {
		return nil, err
	}
	h, err := purego.Dlopen("/System/Library/Frameworks/SystemConfiguration.framework/SystemConfiguration", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	f := &scFuncs{}
	purego.RegisterLibFunc(&f.copyAll, h, "SCNetworkInterfaceCopyAll")
	purego.RegisterLibFunc(&f.bsd, h, "SCNetworkInterfaceGetBSDName")
	purego.RegisterLibFunc(&f.disp, h, "SCNetworkInterfaceGetLocalizedDisplayName")
	purego.RegisterLibFunc(&f.typ, h, "SCNetworkInterfaceGetInterfaceType")
	return f, nil
})

// scInterfaces returns the hardware interfaces by BSD name ("en0").
func scInterfaces() map[string]scInterface {
	out := map[string]scInterface{}
	f, err := loadSC()
	if err != nil {
		return out
	}
	ns.Pool(func() {
		all := f.copyAll() // CFArrayRef, toll-free bridged to NSArray
		if all == 0 {
			return
		}
		defer ns.Send(all, "release")
		for i := range ns.SendInt(all, "count") {
			x := ns.Send(all, "objectAtIndex:", i)
			if name := ns.GoString(f.bsd(x)); name != "" {
				out[name] = scInterface{name: ns.GoString(f.disp(x)), kind: ns.GoString(f.typ(x))}
			}
		}
	})
	return out
}

// Adapters returns the network interfaces that are up and either hardware
// or have an address beyond link-local ones (a VPN tunnel with an IPv4
// address shows, the many utun interfaces with only fe80:: do not).
func Adapters() ([]Adapter, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("network: %w", err)
	}
	ctx := context.Background()
	var routes []byte
	var dns []byte
	var wg sync.WaitGroup
	wg.Go(func() { routes, _ = output(ctx, "/usr/sbin/netstat", "-rn") })
	wg.Go(func() { dns, _ = output(ctx, "/usr/sbin/scutil", "--dns") })
	hw := scInterfaces()
	wg.Wait()
	gws := parseRoutes(routes)
	dcs := parseScutilDNS(dns)
	var out []Adapter
	for _, ifc := range ifs {
		a := Adapter{Name: ifc.Name, Description: ifc.Name, GUID: ifc.Name, Up: ifc.Flags&net.FlagUp != 0 && ifc.Flags&net.FlagRunning != 0}
		if len(ifc.HardwareAddr) > 0 {
			a.MAC = ifc.HardwareAddr.String()
		}
		sc, isHW := hw[ifc.Name]
		if sc.name != "" {
			a.Name = sc.name
		}
		switch {
		case ifc.Flags&net.FlagLoopback != 0:
			a.IfType = IfTypeLoopback
		case sc.kind == "IEEE80211":
			a.IfType = IfTypeWireless
		case ifc.Flags&net.FlagPointToPoint != 0 || isTunnelName(ifc.Name):
			a.IfType = IfTypeTunnel
		default:
			a.IfType = IfTypeEthernet
		}
		addrs, _ := ifc.Addrs()
		global := false
		for _, ad := range addrs {
			ipn, ok := ad.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(ipn.IP)
			if !ok {
				continue
			}
			ones, _ := ipn.Mask.Size()
			ip = ip.Unmap()
			if !ip.IsLinkLocalUnicast() {
				global = true
			}
			if ip.Is4() {
				a.IPv4 = append(a.IPv4, netip.PrefixFrom(ip, ones))
			} else {
				a.IPv6 = append(a.IPv6, netip.PrefixFrom(ip.WithZone(""), ones))
			}
		}
		if !isHW && !global && a.IfType != IfTypeLoopback {
			continue
		}
		a.Gateways = gws[ifc.Name]
		if d, ok := dcs[ifc.Name]; ok {
			a.DNS, a.DNSSuffix = d.Servers, d.Search
		}
		out = append(out, a)
	}
	return out, nil
}

// isTunnelName reports whether an interface name is a tunnel's.
func isTunnelName(name string) bool {
	for _, p := range []string{"utun", "ipsec", "ppp", "gif", "stf", "tun", "tap"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// --- Wi-Fi --------------------------------------------------------------------

var loadCoreWLAN = sync.OnceValue(func() error {
	if err := ns.Load(); err != nil {
		return err
	}
	_, err := purego.Dlopen("/System/Library/Frameworks/CoreWLAN.framework/CoreWLAN", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	return err
})

// wifiDevices returns the BSD names of the Wi-Fi interfaces.
func wifiDevices() []string {
	var out []string
	for name, sc := range scInterfaces() {
		if sc.kind == "IEEE80211" {
			out = append(out, name)
		}
	}
	return out
}

var (
	ssidMu    sync.Mutex
	ssidCache = map[string]ssidEntry{}
)

type ssidEntry struct {
	ssid string
	at   time.Time
}

// summarySSID is the SSID that `ipconfig getsummary` shows (macOS 14 and
// earlier without Location permission; later versions redact it), kept 30
// seconds.
func summarySSID(dev string) string {
	ssidMu.Lock()
	e, ok := ssidCache[dev]
	ssidMu.Unlock()
	if ok && time.Since(e.at) < 30*time.Second {
		return e.ssid
	}
	out, _ := output(context.Background(), "/usr/sbin/ipconfig", "getsummary", dev)
	s := parseSummarySSID(out)
	ssidMu.Lock()
	ssidCache[dev] = ssidEntry{s, time.Now()}
	ssidMu.Unlock()
	return s
}

// WiFiConnections returns the current connection of each Wi-Fi interface
// (CoreWLAN). Without Location permission the SSID is "" and SSIDHidden
// set; the signal and rates are known either way.
func WiFiConnections() ([]WiFiConnection, error) {
	if err := loadCoreWLAN(); err != nil {
		return nil, ErrNoWiFi
	}
	var out []WiFiConnection
	found := false
	ns.Pool(func() {
		client := ns.Send(ns.Class("CWWiFiClient"), "sharedWiFiClient")
		list := ns.Send(client, "interfaces")
		for i := range ns.SendInt(list, "count") {
			ifc := ns.Send(list, "objectAtIndex:", i)
			found = true
			if !ns.SendBool(ifc, "powerOn") {
				continue
			}
			rssi := ns.SendInt(ifc, "rssiValue")
			if rssi == 0 {
				continue // not associated
			}
			c := WiFiConnection{InterfaceGUID: ns.GoString(ns.Send(ifc, "interfaceName")), Signal: signalQuality(rssi)}
			c.SSID = ns.GoString(ns.Send(ifc, "ssid"))
			c.BSSID = ns.GoString(ns.Send(ifc, "bssid"))
			if rate := objc.Send[float64](ifc, ns.Sel("transmitRate")); rate > 0 {
				c.TxKbps = uint32(rate * 1000)
			}
			out = append(out, c)
		}
	})
	if !found {
		return nil, ErrNoWiFi
	}
	for i := range out {
		if out[i].SSID == "" {
			out[i].SSID = summarySSID(out[i].InterfaceGUID)
		}
		out[i].SSIDHidden = out[i].SSID == ""
		out[i].Profile = out[i].SSID
	}
	return out, nil
}

// WiFiProfiles returns the preferred (saved) networks of each Wi-Fi
// interface.
func WiFiProfiles() ([]WiFiProfile, error) {
	devs := wifiDevices()
	if len(devs) == 0 {
		return nil, ErrNoWiFi
	}
	var out []WiFiProfile
	var errs []error
	for _, dev := range devs {
		b, err := output(context.Background(), "/usr/sbin/networksetup", "-listpreferredwirelessnetworks", dev)
		if err != nil {
			errs = append(errs, fmt.Errorf("network: networksetup: %w", err))
			continue
		}
		for _, n := range parsePreferredNetworks(b) {
			out = append(out, WiFiProfile{InterfaceGUID: dev, Name: n})
		}
	}
	if len(out) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// ReadWiFiKey reads the password of the saved network profile from the
// System keychain. macOS asks for an administrator's name and password;
// declining gives ErrNeedsAdmin, a network without a stored password
// ErrNoPassword.
func ReadWiFiKey(ifaceGUID, profile string) (WiFiKey, error) {
	if profile == "" {
		return WiFiKey{}, ErrNotFound
	}
	argv := keychainArgv(profile)
	cmd := exec.Command(argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return WiFiKey{}, fmt.Errorf("network: security: %w", err)
		}
		code = ee.ExitCode()
	}
	if err := keychainError(code, stderr.String()); err != nil {
		return WiFiKey{}, err
	}
	key := strings.TrimRight(stdout.String(), "\r\n")
	if key == "" {
		return WiFiKey{}, ErrNoPassword
	}
	return WiFiKey{SSID: profile, Key: key, Source: "keychain"}, nil
}

// --- Ping ---------------------------------------------------------------------

var pingSeq atomic.Uint32

// Ping sends one ICMP echo request to addr from an ICMP datagram socket
// (no root needed) and waits up to timeout for the reply. The returned
// error is for failures to send; an unanswered request is a reply with
// StatusTimedOut.
func Ping(ctx context.Context, addr netip.Addr, timeout time.Duration) (EchoReply, error) {
	if err := ctx.Err(); err != nil {
		return EchoReply{}, err
	}
	addr = addr.Unmap()
	v6 := addr.Is6()
	fam, proto := unix.AF_INET, unix.IPPROTO_ICMP
	if v6 {
		fam, proto = unix.AF_INET6, unix.IPPROTO_ICMPV6
	}
	fd, err := unix.Socket(fam, unix.SOCK_DGRAM, proto)
	if err != nil {
		return EchoReply{}, fmt.Errorf("network: ICMP socket: %w", err)
	}
	f := os.NewFile(uintptr(fd), "icmp")
	c, err := net.FilePacketConn(f)
	f.Close()
	if err != nil {
		return EchoReply{}, fmt.Errorf("network: ICMP socket: %w", err)
	}
	defer c.Close()
	id := uint16(rand.Uint32())
	seq := uint16(pingSeq.Add(1))
	pkt := buildEcho(v6, id, seq, echoData)
	dst := &net.UDPAddr{IP: addr.AsSlice(), Zone: addr.Zone()}
	start := time.Now()
	deadline := start.Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := c.SetDeadline(deadline); err != nil {
		return EchoReply{}, err
	}
	stop := context.AfterFunc(ctx, func() { c.SetDeadline(time.Now()) })
	defer stop()
	if _, err := c.WriteTo(pkt, dst); err != nil {
		if ctx.Err() != nil {
			return EchoReply{}, ctx.Err()
		}
		switch {
		case errors.Is(err, unix.ENETUNREACH):
			return EchoReply{Status: StatusNetUnreachable}, nil
		case errors.Is(err, unix.EHOSTUNREACH):
			return EchoReply{Status: StatusHostUnreachable}, nil
		}
		return EchoReply{}, fmt.Errorf("network: sending the echo request: %w", err)
	}
	buf := make([]byte, 1500)
	for {
		n, from, err := c.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return EchoReply{}, ctx.Err()
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return EchoReply{Status: StatusTimedOut}, nil
			}
			return EchoReply{}, fmt.Errorf("network: reading the reply: %w", err)
		}
		rtt := time.Since(start)
		msg, ttl := buf[:n], 0
		if !v6 {
			msg, ttl = stripIPv4(msg)
		}
		status, ok := parseEcho(msg, v6, id, seq)
		if !ok {
			continue // another socket's reply, or not about this request
		}
		r := EchoReply{Status: status, RTT: rtt, TTL: ttl}
		if ua, ok := from.(*net.UDPAddr); ok {
			if a, ok := netip.AddrFromSlice(ua.IP); ok {
				r.From = a.Unmap()
			}
		}
		if status != StatusSuccess {
			r.TTL = 0
		}
		return r, nil
	}
}

// --- Ports and processes ------------------------------------------------------

// Sockets returns the TCP listeners and the bound UDP sockets (IPv4 and
// IPv6) with their owning processes, from netstat: unlike lsof it shows the
// sockets of every user without root.
func Sockets() ([]Socket, error) {
	var out []Socket
	for _, p := range []string{"tcp", "udp"} {
		b, err := output(context.Background(), "/usr/sbin/netstat", "-anv", "-p", p)
		if err != nil {
			return nil, fmt.Errorf("network: netstat: %w", err)
		}
		out = append(out, parseNetstatSockets(b)...)
	}
	return out, nil
}

// ProcessInfo returns the name, path and creation time of process pid,
// through internal/winapi/process. Fields that cannot be read are left
// empty.
func ProcessInfo(pid uint32) Process {
	p := Process{PID: pid}
	id, err := process.Identify(pid)
	if err != nil {
		return p
	}
	p.Created = id.Created
	if path := process.ExecutablePath(id); path != "" {
		p.Path = path
		p.Name = filepath.Base(p.Path)
	}
	if p.Name == "" {
		var s process.Sampler
		if snap, err := s.Snapshot(); err == nil {
			if q, ok := snap.Find(id); ok {
				p.Name = q.Name
			}
		}
	}
	return p
}

// Kill ends process pid created at created (see Process.Created) with
// process.Kill; with created 0 the process running as pid now is ended. It
// returns ErrNeedsAdmin for another user's process, ErrNotFound when the
// process exited or its PID now belongs to another process, and
// ErrProtected for launchd, the window server, the login window and
// golauncher itself.
func Kill(pid uint32, created int64) error {
	id := process.ID{PID: pid, Created: created}
	if created == 0 {
		var err error
		if id, err = process.Identify(pid); err != nil {
			return killError(err)
		}
	}
	return killError(process.Kill(id))
}

// killError maps the errors of the process package to this package's.
func killError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, process.ErrNeedsAdmin):
		return ErrNeedsAdmin
	case errors.Is(err, process.ErrGone):
		return ErrNotFound
	case errors.Is(err, process.ErrProtected):
		return ErrProtected
	}
	return fmt.Errorf("network: %w", err)
}
