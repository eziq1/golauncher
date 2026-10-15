package network

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi                = windows.NewLazySystemDLL("iphlpapi.dll")
	procIcmpCreateFile      = iphlpapi.NewProc("IcmpCreateFile")
	procIcmp6CreateFile     = iphlpapi.NewProc("Icmp6CreateFile")
	procIcmpCloseHandle     = iphlpapi.NewProc("IcmpCloseHandle")
	procIcmpSendEcho2       = iphlpapi.NewProc("IcmpSendEcho2")
	procIcmp6SendEcho2      = iphlpapi.NewProc("Icmp6SendEcho2")
	procGetExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
	procGetExtendedUdpTable = iphlpapi.NewProc("GetExtendedUdpTable")
	wlanapi                 = windows.NewLazySystemDLL("wlanapi.dll")
	procWlanOpenHandle      = wlanapi.NewProc("WlanOpenHandle")
	procWlanCloseHandle     = wlanapi.NewProc("WlanCloseHandle")
	procWlanEnumInterfaces  = wlanapi.NewProc("WlanEnumInterfaces")
	procWlanQueryInterface  = wlanapi.NewProc("WlanQueryInterface")
	procWlanGetProfileList  = wlanapi.NewProc("WlanGetProfileList")
	procWlanGetProfile      = wlanapi.NewProc("WlanGetProfile")
	procWlanFreeMemory      = wlanapi.NewProc("WlanFreeMemory")

	// wlanUnavailable are the errors of WlanOpenHandle on systems without
	// a running WLAN service.
	wlanUnavailable = []error{windows.ERROR_SERVICE_NOT_ACTIVE, windows.ERROR_SERVICE_DOES_NOT_EXIST, windows.ERROR_NOT_SUPPORTED}
)

// native converts an address returned by Windows to a pointer without
// tripping vet's unsafeptr check; the memory is not managed by Go.
func native(addr uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&addr)) }

// --- Adapters ---------------------------------------------------------------

// Adapters returns the network adapters with their addresses, gateways and
// DNS servers, in the system's order.
func Adapters() ([]Adapter, error) {
	const flags = windows.GAA_FLAG_INCLUDE_GATEWAYS | windows.GAA_FLAG_SKIP_ANYCAST | windows.GAA_FLAG_SKIP_MULTICAST
	size := uint32(16 << 10)
	for range 4 {
		buf := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, flags, 0, first, &size)
		if errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			continue
		}
		if errors.Is(err, windows.ERROR_NO_DATA) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("network: GetAdaptersAddresses: %w", err)
		}
		return convertAdapters(first), nil
	}
	return nil, errors.New("network: GetAdaptersAddresses: the adapter list keeps growing")
}

// convertAdapters converts the linked list returned by GetAdaptersAddresses.
func convertAdapters(first *windows.IpAdapterAddresses) []Adapter {
	var out []Adapter
	for a := first; a != nil; a = a.Next {
		ad := Adapter{
			Name:        windows.UTF16PtrToString(a.FriendlyName),
			Description: windows.UTF16PtrToString(a.Description),
			GUID:        windows.BytePtrToString(a.AdapterName),
			IfType:      a.IfType,
			Up:          a.OperStatus == windows.IfOperStatusUp,
			DNSSuffix:   windows.UTF16PtrToString(a.DnsSuffix),
			SpeedBps:    a.ReceiveLinkSpeed,
		}
		if ad.SpeedBps == ^uint64(0) {
			ad.SpeedBps = 0
		}
		if n := min(int(a.PhysicalAddressLength), len(a.PhysicalAddress)); n > 0 {
			ad.MAC = FormatMAC(a.PhysicalAddress[:n])
		}
		for u := a.FirstUnicastAddress; u != nil; u = u.Next {
			addr, ok := sockaddrAddr(u.Address)
			if !ok {
				continue
			}
			pfx := netip.PrefixFrom(addr, int(u.OnLinkPrefixLength))
			if addr.Is4() {
				ad.IPv4 = append(ad.IPv4, pfx)
			} else {
				ad.IPv6 = append(ad.IPv6, pfx)
			}
		}
		for g := a.FirstGatewayAddress; g != nil; g = g.Next {
			if addr, ok := sockaddrAddr(g.Address); ok {
				ad.Gateways = append(ad.Gateways, addr)
			}
		}
		for d := a.FirstDnsServerAddress; d != nil; d = d.Next {
			if addr, ok := sockaddrAddr(d.Address); ok {
				ad.DNS = append(ad.DNS, addr)
			}
		}
		out = append(out, ad)
	}
	return out
}

// sockaddrAddr returns the address of a SOCKET_ADDRESS, with the zone of a
// link-local IPv6 address dropped.
func sockaddrAddr(sa windows.SocketAddress) (netip.Addr, bool) {
	if sa.Sockaddr == nil {
		return netip.Addr{}, false
	}
	switch sa.Sockaddr.Addr.Family {
	case windows.AF_INET:
		p := (*syscall.RawSockaddrInet4)(unsafe.Pointer(sa.Sockaddr))
		return netip.AddrFrom4(p.Addr), true
	case windows.AF_INET6:
		p := (*syscall.RawSockaddrInet6)(unsafe.Pointer(sa.Sockaddr))
		return netip.AddrFrom16(p.Addr), true
	}
	return netip.Addr{}, false
}

// --- Wi-Fi -------------------------------------------------------------------

const (
	wlanIntfOpcodeCurrentConnection = 7
	wlanInterfaceStateConnected     = 1
	wlanProfileGetPlaintextKey      = 4
	wlanInterfaceInfoSize           = 532 // WLAN_INTERFACE_INFO
	wlanProfileInfoSize             = 516 // WLAN_PROFILE_INFO
)

// dot11SSID is DOT11_SSID.
type dot11SSID struct {
	Len  uint32
	SSID [32]byte
}

// wlanAssociationAttributes is WLAN_ASSOCIATION_ATTRIBUTES.
type wlanAssociationAttributes struct {
	SSID          dot11SSID
	BSSType       uint32
	BSSID         [6]byte
	PhyType       uint32
	PhyIndex      uint32
	SignalQuality uint32
	RxRate        uint32
	TxRate        uint32
}

// wlanConnectionAttributes is WLAN_CONNECTION_ATTRIBUTES.
type wlanConnectionAttributes struct {
	State       uint32
	Mode        uint32
	ProfileName [256]uint16
	Association wlanAssociationAttributes
	Security    [4]uint32 // WLAN_SECURITY_ATTRIBUTES
}

// wlanCall calls a WLAN function, which returns its error code.
func wlanCall(p *windows.LazyProc, args ...uintptr) error {
	if err := p.Find(); err != nil {
		return ErrNoWiFi
	}
	r, _, _ := p.Call(args...)
	if r != 0 {
		return windows.Errno(r)
	}
	return nil
}

// withWLAN opens a WLAN client handle for fn.
func withWLAN(fn func(h windows.Handle) error) error {
	var version uint32
	var h windows.Handle
	if err := wlanCall(procWlanOpenHandle, 2, 0, uintptr(unsafe.Pointer(&version)), uintptr(unsafe.Pointer(&h))); err != nil {
		for _, e := range wlanUnavailable {
			if errors.Is(err, e) {
				return ErrNoWiFi
			}
		}
		if errors.Is(err, ErrNoWiFi) {
			return err
		}
		return fmt.Errorf("network: WlanOpenHandle: %w", err)
	}
	defer procWlanCloseHandle.Call(uintptr(h), 0)
	return fn(h)
}

// wlanInterfaces returns the GUIDs of the WLAN interfaces.
func wlanInterfaces(h windows.Handle) ([]windows.GUID, error) {
	var list uintptr
	if err := wlanCall(procWlanEnumInterfaces, uintptr(h), 0, uintptr(unsafe.Pointer(&list))); err != nil {
		return nil, fmt.Errorf("network: WlanEnumInterfaces: %w", err)
	}
	defer procWlanFreeMemory.Call(list)
	n := *(*uint32)(native(list))
	var out []windows.GUID
	for i := range uintptr(n) {
		out = append(out, *(*windows.GUID)(native(list + 8 + i*wlanInterfaceInfoSize)))
	}
	return out, nil
}

// WiFiConnections returns the current connections of the Wi-Fi interfaces.
// It returns ErrNoWiFi when the system has no WLAN service.
func WiFiConnections() ([]WiFiConnection, error) {
	var out []WiFiConnection
	err := withWLAN(func(h windows.Handle) error {
		ifaces, err := wlanInterfaces(h)
		if err != nil {
			return err
		}
		for _, g := range ifaces {
			var size uint32
			var data uintptr
			var valueType uint32
			if err := wlanCall(procWlanQueryInterface, uintptr(h), uintptr(unsafe.Pointer(&g)), wlanIntfOpcodeCurrentConnection, 0,
				uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&valueType))); err != nil {
				continue // not connected (ERROR_INVALID_STATE)
			}
			if uintptr(size) >= unsafe.Sizeof(wlanConnectionAttributes{}) {
				out = append(out, convertConnection(g, (*wlanConnectionAttributes)(native(data))))
			}
			procWlanFreeMemory.Call(data)
		}
		return nil
	})
	return out, err
}

func convertConnection(g windows.GUID, c *wlanConnectionAttributes) WiFiConnection {
	as := &c.Association
	n := min(int(as.SSID.Len), len(as.SSID.SSID))
	return WiFiConnection{
		InterfaceGUID: g.String(),
		Profile:       windows.UTF16ToString(c.ProfileName[:]),
		SSID:          strings.ToValidUTF8(string(as.SSID.SSID[:n]), "?"),
		BSSID:         FormatMAC(as.BSSID[:]),
		Signal:        int(min(as.SignalQuality, 100)),
		RxKbps:        as.RxRate,
		TxKbps:        as.TxRate,
	}
}

// WiFiProfiles returns the saved Wi-Fi profiles of every interface, in
// each interface's preference order.
func WiFiProfiles() ([]WiFiProfile, error) {
	var out []WiFiProfile
	err := withWLAN(func(h windows.Handle) error {
		ifaces, err := wlanInterfaces(h)
		if err != nil {
			return err
		}
		for _, g := range ifaces {
			var list uintptr
			if err := wlanCall(procWlanGetProfileList, uintptr(h), uintptr(unsafe.Pointer(&g)), 0, uintptr(unsafe.Pointer(&list))); err != nil {
				return fmt.Errorf("network: WlanGetProfileList: %w", err)
			}
			n := *(*uint32)(native(list))
			for i := range uintptr(n) {
				name := (*[256]uint16)(native(list + 8 + i*wlanProfileInfoSize))
				out = append(out, WiFiProfile{InterfaceGUID: g.String(), Name: windows.UTF16ToString(name[:])})
			}
			procWlanFreeMemory.Call(list)
		}
		return nil
	})
	return out, err
}

// ReadWiFiKey reads the key of a saved profile: first from WlanGetProfile with
// WLAN_PROFILE_GET_PLAINTEXT_KEY, then from `netsh wlan export profile
// key=clear`. It returns ErrNoPassword for open and enterprise networks
// and ErrNeedsAdmin when neither gives the plain text key.
func ReadWiFiKey(ifaceGUID, profile string) (WiFiKey, error) {
	g, err := windows.GUIDFromString(ifaceGUID)
	if err != nil {
		return WiFiKey{}, err
	}
	var k WiFiKey
	err = withWLAN(func(h windows.Handle) error {
		name, err := windows.UTF16PtrFromString(profile)
		if err != nil {
			return err
		}
		var xmlPtr uintptr
		flags := uint32(wlanProfileGetPlaintextKey)
		var access uint32
		if err := wlanCall(procWlanGetProfile, uintptr(h), uintptr(unsafe.Pointer(&g)), uintptr(unsafe.Pointer(name)), 0,
			uintptr(unsafe.Pointer(&xmlPtr)), uintptr(unsafe.Pointer(&flags)), uintptr(unsafe.Pointer(&access))); err != nil {
			if errors.Is(err, windows.ERROR_NOT_FOUND) {
				return ErrNotFound
			}
			if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				return ErrNeedsAdmin
			}
			return fmt.Errorf("network: WlanGetProfile: %w", err)
		}
		doc := windows.UTF16PtrToString((*uint16)(native(xmlPtr)))
		procWlanFreeMemory.Call(xmlPtr)
		_, k, err = ParseProfileXML([]byte(doc))
		return err
	})
	if err != nil && !errors.Is(err, ErrNeedsAdmin) {
		return WiFiKey{}, err
	}
	if err == nil {
		if k.Plaintext() {
			k.Source = "wlanapi"
			return k, nil
		}
		if k.Open() || k.Key == "" {
			return k, ErrNoPassword
		}
	}
	if nk, nerr := netshKey(profile); nerr == nil && nk.Plaintext() {
		nk.Source = "netsh"
		return nk, nil
	}
	k.Key = ""
	return k, ErrNeedsAdmin
}

// netshKey exports the profile with key=clear to a private temporary
// directory, reads it and removes it at once.
func netshKey(profile string) (WiFiKey, error) {
	if strings.ContainsAny(profile, "\"\r\n") {
		return WiFiKey{}, errors.New("network: the profile name cannot be passed to netsh")
	}
	dir, err := os.MkdirTemp("", "gl-wlan-")
	if err != nil {
		return WiFiKey{}, err
	}
	defer os.RemoveAll(dir)
	netsh := filepath.Join(os.Getenv("SystemRoot"), "System32", "netsh.exe")
	cmd := exec.Command(netsh)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
		CmdLine:       fmt.Sprintf(`%s wlan export profile name="%s" key=clear folder="%s"`, syscall.EscapeArg(netsh), profile, dir),
	}
	if err := cmd.Run(); err != nil {
		return WiFiKey{}, fmt.Errorf("network: netsh: %w", err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.xml"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		name, k, err := ParseProfileXML(data)
		clear(data)
		if err == nil && name == profile {
			return k, nil
		}
	}
	return WiFiKey{}, ErrNotFound
}

// --- ICMP --------------------------------------------------------------------

// pingData is the payload of an echo request, as Windows ping sends it.
var pingData = []byte("abcdefghijklmnopqrstuvwabcdefghi")

// Ping sends one ICMP echo request to addr and waits up to timeout for the
// reply. The returned error is for failures to send; an unanswered request
// is a reply with StatusTimedOut.
func Ping(ctx context.Context, addr netip.Addr, timeout time.Duration) (EchoReply, error) {
	if err := ctx.Err(); err != nil {
		return EchoReply{}, err
	}
	addr = addr.Unmap()
	ms := uintptr(max(timeout.Milliseconds(), 1))
	reply := make([]byte, 256+len(pingData))
	if addr.Is4() {
		h, _, e := procIcmpCreateFile.Call()
		if windows.Handle(h) == windows.InvalidHandle {
			return EchoReply{}, fmt.Errorf("network: IcmpCreateFile: %w", e)
		}
		defer procIcmpCloseHandle.Call(h)
		dst := addr.As4()
		n, _, e := procIcmpSendEcho2.Call(h, 0, 0, 0, uintptr(binary.LittleEndian.Uint32(dst[:])),
			uintptr(unsafe.Pointer(&pingData[0])), uintptr(len(pingData)), 0,
			uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)), ms)
		if n == 0 {
			return failedEcho(e)
		}
		return parseEcho4(reply), nil
	}
	h, _, e := procIcmp6CreateFile.Call()
	if windows.Handle(h) == windows.InvalidHandle {
		return EchoReply{}, fmt.Errorf("network: Icmp6CreateFile: %w", e)
	}
	defer procIcmpCloseHandle.Call(h)
	src := windows.RawSockaddrInet6{Family: windows.AF_INET6}
	dst := windows.RawSockaddrInet6{Family: windows.AF_INET6, Addr: addr.As16()}
	n, _, e := procIcmp6SendEcho2.Call(h, 0, 0, 0, uintptr(unsafe.Pointer(&src)), uintptr(unsafe.Pointer(&dst)),
		uintptr(unsafe.Pointer(&pingData[0])), uintptr(len(pingData)), 0,
		uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)), ms)
	if n == 0 {
		return failedEcho(e)
	}
	return parseEcho6(reply), nil
}

// failedEcho turns the error of a send without replies into a reply with
// its status when it is an IP_STATUS code.
func failedEcho(e error) (EchoReply, error) {
	if errno, ok := errors.AsType[windows.Errno](e); ok && errno >= 11000 && errno < 11100 {
		return EchoReply{Status: Normalize(uint32(errno))}, nil
	}
	return EchoReply{}, fmt.Errorf("network: IcmpSendEcho2: %w", e)
}

// parseEcho4 reads an ICMP_ECHO_REPLY. A 64-bit process receives the
// native layout, whose IP_OPTION_INFORMATION starts after the Data pointer.
func parseEcho4(b []byte) EchoReply {
	r := EchoReply{
		From:   netip.AddrFrom4([4]byte(b[0:4])),
		Status: binary.LittleEndian.Uint32(b[4:]),
		RTT:    time.Duration(binary.LittleEndian.Uint32(b[8:])) * time.Millisecond,
	}
	r.TTL = int(b[16+unsafe.Sizeof(uintptr(0))])
	return r
}

// parseEcho6 reads an ICMPV6_ECHO_REPLY, whose IPV6_ADDRESS_EX is packed:
// port (2), flow info (4), address (16), scope (4), then status and RTT.
func parseEcho6(b []byte) EchoReply {
	return EchoReply{
		From:   netip.AddrFrom16([16]byte(b[6:22])),
		Status: Normalize(binary.LittleEndian.Uint32(b[26:])),
		RTT:    time.Duration(binary.LittleEndian.Uint32(b[30:])) * time.Millisecond,
	}
}

// --- Ports and processes ------------------------------------------------------

const (
	tcpTableOwnerPIDListener = 3
	udpTableOwnerPID         = 1
)

// Sockets returns the TCP listeners and the bound UDP sockets (IPv4 and
// IPv6) with their owning processes.
func Sockets() ([]Socket, error) {
	var out []Socket
	for _, t := range []struct {
		proc  *windows.LazyProc
		af    uint32
		parse func([]byte) []Socket
		class uintptr
	}{
		{procGetExtendedTcpTable, windows.AF_INET, ParseTCP4Table, tcpTableOwnerPIDListener},
		{procGetExtendedTcpTable, windows.AF_INET6, ParseTCP6Table, tcpTableOwnerPIDListener},
		{procGetExtendedUdpTable, windows.AF_INET, ParseUDP4Table, udpTableOwnerPID},
		{procGetExtendedUdpTable, windows.AF_INET6, ParseUDP6Table, udpTableOwnerPID},
	} {
		b, err := table(t.proc, t.af, t.class)
		if err != nil {
			return nil, err
		}
		out = append(out, t.parse(b)...)
	}
	return out, nil
}

// table reads an owner-PID table, growing the buffer as needed.
func table(p *windows.LazyProc, af uint32, class uintptr) ([]byte, error) {
	size := uint32(16 << 10)
	for range 5 {
		buf := make([]byte, size)
		r, _, _ := p.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 1, uintptr(af), class, 0)
		switch windows.Errno(r) {
		case 0:
			return buf[:size], nil
		case windows.ERROR_INSUFFICIENT_BUFFER:
			size += 4 << 10
			continue
		default:
			return nil, fmt.Errorf("network: %s: %w", p.Name, windows.Errno(r))
		}
	}
	return nil, fmt.Errorf("network: %s: the table keeps growing", p.Name)
}

// ProcessInfo returns the name and path of process pid. Fields that cannot
// be read are left empty.
func ProcessInfo(pid uint32) Process {
	p := Process{PID: pid}
	switch pid {
	case 0:
		p.Name = "System Idle Process"
		return p
	case 4:
		p.Name = "System"
		return p
	}
	if h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid); err == nil {
		buf := make([]uint16, windows.MAX_LONG_PATH)
		n := uint32(len(buf))
		if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
			p.Path = windows.UTF16ToString(buf[:n])
			p.Name = filepath.Base(p.Path)
		}
		windows.CloseHandle(h)
	}
	if p.Name == "" {
		p.Name = snapshotName(pid)
	}
	return p
}

// snapshotName finds the executable name of pid in a process snapshot,
// which works also for processes that cannot be opened.
func snapshotName(pid uint32) string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err := windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if e.ProcessID == pid {
			return windows.UTF16ToString(e.ExeFile[:])
		}
	}
	return ""
}

// Kill ends process pid. It returns ErrNeedsAdmin when access is denied and
// ErrNotFound when there is no such process.
func Kill(pid uint32) error {
	if pid == 0 || pid == 4 || pid == uint32(os.Getpid()) {
		return ErrNeedsAdmin
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return ErrNeedsAdmin
		}
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return ErrNotFound
		}
		return fmt.Errorf("network: OpenProcess: %w", err)
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return ErrNeedsAdmin
		}
		return fmt.Errorf("network: TerminateProcess: %w", err)
	}
	return nil
}
