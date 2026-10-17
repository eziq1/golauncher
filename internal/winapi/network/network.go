// Package network reads the network state of Windows for the network tools
// plugin (docs/network-tools.md): the adapters and their addresses
// (GetAdaptersAddresses), the Wi-Fi connection and saved Wi-Fi profiles
// (wlanapi), ICMP echo (IcmpSendEcho2, no administrator rights needed), the
// owners of local ports (GetExtendedTcpTable / GetExtendedUdpTable) and
// processes.
//
// The parsing of what the system returns is kept apart from the system calls
// so that it can be tested with fixtures on any platform. On other platforms
// the system calls return errors.ErrUnsupported.
package network

import (
	"encoding/binary"
	"encoding/xml"
	"errors"
	"net/netip"
	"strings"
	"time"
)

var (
	// ErrNoWiFi reports that the system has no WLAN service or interface.
	ErrNoWiFi = errors.New("network: no Wi-Fi")
	// ErrNeedsAdmin reports that reading a Wi-Fi key or ending a process
	// needs administrator rights.
	ErrNeedsAdmin = errors.New("network: needs administrator rights")
	// ErrNoPassword reports a Wi-Fi profile without a stored key (an open
	// or an enterprise network).
	ErrNoPassword = errors.New("network: the profile has no password")
	// ErrNotFound reports a profile or process that does not exist.
	ErrNotFound = errors.New("network: not found")
	// ErrProtected reports a process that must not or cannot be ended: the
	// kernel, a critical system process, golauncher itself, or a protected
	// process that refuses even an administrator (process.ErrProtected).
	ErrProtected = errors.New("network: protected process")
)

// Adapter is a network adapter with its configuration.
type Adapter struct {
	Name        string // friendly name, e.g. "Wi-Fi"
	Description string // e.g. "Contoso Wireless Adapter"
	GUID        string // "{...}", the adapter name; matches WiFiConnection.InterfaceGUID
	IfType      uint32
	Up          bool
	MAC         string // "AA-BB-CC-DD-EE-FF", "" when none
	IPv4        []netip.Prefix
	IPv6        []netip.Prefix
	Gateways    []netip.Addr
	DNS         []netip.Addr
	DNSSuffix   string
	// SpeedBps is the receive link speed in bits per second (0: unknown).
	SpeedBps uint64
}

// Interface types (IANA ifType) used by the plugin.
const (
	IfTypeEthernet = 6
	IfTypeLoopback = 24
	IfTypeWireless = 71
	IfTypeTunnel   = 131
)

// Wireless reports whether a is a Wi-Fi adapter.
func (a Adapter) Wireless() bool { return a.IfType == IfTypeWireless }

// Loopback reports whether a is the loopback pseudo-interface.
func (a Adapter) Loopback() bool { return a.IfType == IfTypeLoopback }

// WiFiConnection is the current connection of a Wi-Fi interface.
type WiFiConnection struct {
	InterfaceGUID string
	Profile       string
	SSID          string
	BSSID         string
	// Signal is the signal quality in percent (0-100).
	Signal int
	// RxKbps and TxKbps are the link rates in kbit/s.
	RxKbps, TxKbps uint32
	// SSIDHidden reports that the system did not tell the network's name:
	// macOS gives it only to apps allowed to use Location Services.
	SSIDHidden bool
}

// WiFiProfile is a saved Wi-Fi network.
type WiFiProfile struct {
	InterfaceGUID string
	Name          string
}

// WiFiKey is what a profile says about its security.
type WiFiKey struct {
	SSID           string
	Authentication string // e.g. "WPA2PSK", "open"
	Encryption     string // e.g. "AES", "none"
	// Key is the key material; it is plain text unless Protected.
	Key       string
	Protected bool
	// Source tells how the plain text key was read: "wlanapi" or "netsh".
	Source string
}

// Open reports whether the network needs no key.
func (k WiFiKey) Open() bool {
	return strings.EqualFold(k.Authentication, "open") && (k.Encryption == "" || strings.EqualFold(k.Encryption, "none"))
}

// Plaintext reports whether Key holds the plain text key.
func (k WiFiKey) Plaintext() bool { return k.Key != "" && !k.Protected }

// profileXML is the part of a WLAN profile (WLANProfile schema) read here.
type profileXML struct {
	Name string `xml:"name"`
	SSID []struct {
		Name string `xml:"name"`
		Hex  string `xml:"hex"`
	} `xml:"SSIDConfig>SSID"`
	Auth       string `xml:"MSM>security>authEncryption>authentication"`
	Encryption string `xml:"MSM>security>authEncryption>encryption"`
	Protected  bool   `xml:"MSM>security>sharedKey>protected"`
	Key        string `xml:"MSM>security>sharedKey>keyMaterial"`
}

// ParseProfileXML reads the SSID and the key of a WLAN profile XML document.
// The returned name is the profile's name.
func ParseProfileXML(data []byte) (name string, k WiFiKey, err error) {
	var p profileXML
	if err := xml.Unmarshal(data, &p); err != nil {
		return "", WiFiKey{}, err
	}
	k = WiFiKey{Authentication: p.Auth, Encryption: p.Encryption, Key: p.Key, Protected: p.Protected}
	if len(p.SSID) > 0 {
		k.SSID = p.SSID[0].Name
	}
	return p.Name, k, nil
}

// EchoReply is the outcome of one ICMP echo request.
type EchoReply struct {
	// From is the address that answered (the target, or a router reporting
	// an error).
	From   netip.Addr
	Status uint32
	RTT    time.Duration
	TTL    int // 0 when unknown
}

// ICMP status codes (IP_STATUS).
const (
	StatusSuccess          = 0
	StatusNetUnreachable   = 11002
	StatusHostUnreachable  = 11003
	StatusProtUnreachable  = 11004
	StatusPortUnreachable  = 11005
	StatusTimedOut         = 11010
	StatusTTLExpired       = 11013
	StatusGeneralFailure   = 11050
	statusIPv6Unreachable  = 11040 // IP_DEST_UNREACHABLE (IPv6)
	statusIPv6TimeExceeded = 11041
)

// Normalize maps the IPv6 variants of status codes to the IPv4 ones.
func Normalize(status uint32) uint32 {
	switch status {
	case statusIPv6Unreachable:
		return StatusHostUnreachable
	case statusIPv6TimeExceeded:
		return StatusTTLExpired
	}
	return status
}

// Socket is a local TCP listener or a bound UDP socket.
type Socket struct {
	Proto string // "tcp" or "udp"
	Local netip.AddrPort
	PID   uint32
}

// Row sizes of the owner-PID tables (MIB_*ROW_OWNER_PID).
const (
	tcp4RowSize = 24
	tcp6RowSize = 56
	udp4RowSize = 12
	udp6RowSize = 28
	tcpListen   = 2 // MIB_TCP_STATE_LISTEN
)

// port reads a port stored in network byte order in the low bytes of a DWORD.
func port(b []byte) uint16 { return binary.BigEndian.Uint16(b[:2]) }

// rows splits a MIB table (a DWORD count followed by rows) into rows.
func rows(b []byte, size int) [][]byte {
	if len(b) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(b))
	var out [][]byte
	for i := range n {
		off := 4 + i*size
		if off+size > len(b) {
			break
		}
		out = append(out, b[off:off+size])
	}
	return out
}

// ParseTCP4Table returns the listeners of a MIB_TCPTABLE_OWNER_PID.
func ParseTCP4Table(b []byte) []Socket {
	var out []Socket
	for _, r := range rows(b, tcp4RowSize) {
		if binary.LittleEndian.Uint32(r) != tcpListen {
			continue
		}
		addr := netip.AddrFrom4([4]byte(r[4:8]))
		out = append(out, Socket{Proto: "tcp", Local: netip.AddrPortFrom(addr, port(r[8:])), PID: binary.LittleEndian.Uint32(r[20:])})
	}
	return out
}

// ParseTCP6Table returns the listeners of a MIB_TCP6TABLE_OWNER_PID.
func ParseTCP6Table(b []byte) []Socket {
	var out []Socket
	for _, r := range rows(b, tcp6RowSize) {
		if binary.LittleEndian.Uint32(r[48:]) != tcpListen {
			continue
		}
		addr := netip.AddrFrom16([16]byte(r[0:16]))
		out = append(out, Socket{Proto: "tcp", Local: netip.AddrPortFrom(addr, port(r[20:])), PID: binary.LittleEndian.Uint32(r[52:])})
	}
	return out
}

// ParseUDP4Table returns the sockets of a MIB_UDPTABLE_OWNER_PID.
func ParseUDP4Table(b []byte) []Socket {
	var out []Socket
	for _, r := range rows(b, udp4RowSize) {
		addr := netip.AddrFrom4([4]byte(r[0:4]))
		out = append(out, Socket{Proto: "udp", Local: netip.AddrPortFrom(addr, port(r[4:])), PID: binary.LittleEndian.Uint32(r[8:])})
	}
	return out
}

// ParseUDP6Table returns the sockets of a MIB_UDP6TABLE_OWNER_PID.
func ParseUDP6Table(b []byte) []Socket {
	var out []Socket
	for _, r := range rows(b, udp6RowSize) {
		addr := netip.AddrFrom16([16]byte(r[0:16]))
		out = append(out, Socket{Proto: "udp", Local: netip.AddrPortFrom(addr, port(r[20:])), PID: binary.LittleEndian.Uint32(r[24:])})
	}
	return out
}

// Process describes a running process.
type Process struct {
	PID  uint32
	Name string // e.g. "node.exe"
	Path string // full path, "" when it cannot be read
	// Created is the creation time (FILETIME, 100 ns since 1601), which
	// identifies the process instance with its PID; 0 when unknown.
	Created int64
}

// FormatMAC formats a hardware address as Windows does: "AA-BB-CC-DD-EE-FF".
func FormatMAC(b []byte) string {
	const hex = "0123456789ABCDEF"
	var sb strings.Builder
	for i, c := range b {
		if i > 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(hex[c>>4])
		sb.WriteByte(hex[c&0xf])
	}
	return sb.String()
}
