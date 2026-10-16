package network

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	winnet "github.com/eziq1/golauncher/internal/winapi/network"
)

// Fixture is the content of a fixture file (testdata/fixtures/network.json):
// the state a fake environment reports. Nothing in it is read from the
// system or the network.
type Fixture struct {
	Adapters []struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		GUID        string         `json:"guid"`
		IfType      uint32         `json:"ifType"`
		Up          bool           `json:"up"`
		MAC         string         `json:"mac"`
		IPv4        []netip.Prefix `json:"ipv4"`
		IPv6        []netip.Prefix `json:"ipv6"`
		Gateways    []netip.Addr   `json:"gateways"`
		DNS         []netip.Addr   `json:"dns"`
		DNSSuffix   string         `json:"dnsSuffix"`
		SpeedBps    uint64         `json:"speedBps"`
	} `json:"adapters"`
	WiFi []struct {
		InterfaceGUID string `json:"interfaceGuid"`
		Profile       string `json:"profile"`
		SSID          string `json:"ssid"`
		Signal        int    `json:"signal"`
		RxKbps        uint32 `json:"rxKbps"`
		TxKbps        uint32 `json:"txKbps"`
	} `json:"wifi"`
	Profiles []struct {
		InterfaceGUID string `json:"interfaceGuid"`
		Name          string `json:"name"`
		Auth          string `json:"auth"`
		// Key is the fake key; "" with Auth "open" is an open network, ""
		// otherwise a key that needs administrator rights.
		Key string `json:"key"`
	} `json:"profiles"`
	PublicIPv4 string `json:"publicIPv4"`
	PublicIPv6 string `json:"publicIPv6"`
	// Hosts maps host names to addresses (for ping).
	Hosts map[string]string `json:"hosts"`
	// PingMs maps addresses to the round-trip time of their replies; other
	// addresses time out.
	PingMs map[string]int `json:"pingMs"`
	// DNS maps names to their records.
	DNS map[string][]Record `json:"dns"`
	// Open maps "host:port" to the connection time in ms; other addresses
	// refuse connections.
	Open      map[string]int `json:"open"`
	Sockets   []fixSocket    `json:"sockets"`
	Processes []fixProcess   `json:"processes"`
}

type fixSocket struct {
	Proto string         `json:"proto"`
	Local netip.AddrPort `json:"local"`
	PID   uint32         `json:"pid"`
}

type fixProcess struct {
	PID  uint32 `json:"pid"`
	Name string `json:"name"`
	Path string `json:"path"`
	// Created is the creation time (FILETIME); 0 when unknown.
	Created int64 `json:"created"`
}

// FixtureBackend is a backend answering from a Fixture.
type FixtureBackend struct {
	F   Fixture
	Err error // returned by every call when set (a broken fixture file)

	mu     sync.Mutex
	killed []uint32
}

var _ Backend = (*FixtureBackend)(nil)

// LoadFixture reads a fixture file; path "" gives an empty fixture.
func LoadFixture(path string) *FixtureBackend {
	b := &FixtureBackend{}
	if path == "" {
		return b
	}
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &b.F)
	}
	if err != nil {
		b.Err = fmt.Errorf("network fixture %s: %w", path, err)
	}
	return b
}

// Killed returns the PIDs passed to Kill.
func (b *FixtureBackend) Killed() []uint32 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]uint32(nil), b.killed...)
}

func (b *FixtureBackend) Adapters(context.Context) ([]winnet.Adapter, error) {
	if b.Err != nil {
		return nil, b.Err
	}
	var out []winnet.Adapter
	for _, a := range b.F.Adapters {
		out = append(out, winnet.Adapter{Name: a.Name, Description: a.Description, GUID: a.GUID, IfType: a.IfType, Up: a.Up,
			MAC: a.MAC, IPv4: a.IPv4, IPv6: a.IPv6, Gateways: a.Gateways, DNS: a.DNS, DNSSuffix: a.DNSSuffix, SpeedBps: a.SpeedBps})
	}
	return out, nil
}

func (b *FixtureBackend) WiFiConnections(context.Context) ([]winnet.WiFiConnection, error) {
	if b.Err != nil {
		return nil, b.Err
	}
	if len(b.F.WiFi) == 0 && len(b.F.Profiles) == 0 {
		return nil, winnet.ErrNoWiFi
	}
	var out []winnet.WiFiConnection
	for _, w := range b.F.WiFi {
		out = append(out, winnet.WiFiConnection{InterfaceGUID: w.InterfaceGUID, Profile: w.Profile, SSID: w.SSID,
			Signal: w.Signal, RxKbps: w.RxKbps, TxKbps: w.TxKbps})
	}
	return out, nil
}

func (b *FixtureBackend) WiFiProfiles(context.Context) ([]winnet.WiFiProfile, error) {
	if b.Err != nil {
		return nil, b.Err
	}
	if len(b.F.WiFi) == 0 && len(b.F.Profiles) == 0 {
		return nil, winnet.ErrNoWiFi
	}
	var out []winnet.WiFiProfile
	for _, p := range b.F.Profiles {
		out = append(out, winnet.WiFiProfile{InterfaceGUID: p.InterfaceGUID, Name: p.Name})
	}
	return out, nil
}

func (b *FixtureBackend) WiFiKey(_ context.Context, iface, profile string) (winnet.WiFiKey, error) {
	if b.Err != nil {
		return winnet.WiFiKey{}, b.Err
	}
	for _, p := range b.F.Profiles {
		if p.Name != profile || !strings.EqualFold(p.InterfaceGUID, iface) {
			continue
		}
		k := winnet.WiFiKey{SSID: p.Name, Authentication: p.Auth, Key: p.Key, Source: "fixture"}
		switch {
		case k.Open():
			return k, winnet.ErrNoPassword
		case p.Key == "":
			return k, winnet.ErrNeedsAdmin
		}
		return k, nil
	}
	return winnet.WiFiKey{}, winnet.ErrNotFound
}

func (b *FixtureBackend) PublicIP(_ context.Context, v6 bool) (netip.Addr, error) {
	if b.Err != nil {
		return netip.Addr{}, b.Err
	}
	s := b.F.PublicIPv4
	if v6 {
		s = b.F.PublicIPv6
	}
	if s == "" {
		return netip.Addr{}, errors.New("no public address in the fixture")
	}
	return netip.ParseAddr(s)
}

func (b *FixtureBackend) Resolve(_ context.Context, host string) (netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return a, nil
	}
	if s, ok := b.F.Hosts[strings.ToLower(host)]; ok {
		return netip.ParseAddr(s)
	}
	return netip.Addr{}, errNotFound
}

func (b *FixtureBackend) Ping(_ context.Context, addr netip.Addr, _ time.Duration) (winnet.EchoReply, error) {
	if ms, ok := b.F.PingMs[addr.String()]; ok {
		return winnet.EchoReply{From: addr, RTT: time.Duration(ms) * time.Millisecond, TTL: 56}, nil
	}
	return winnet.EchoReply{Status: winnet.StatusTimedOut}, nil
}

func (b *FixtureBackend) LookupDNS(_ context.Context, name, typ, _ string) ([]Record, error) {
	recs, ok := b.F.DNS[strings.ToLower(strings.TrimSuffix(name, "."))]
	if !ok {
		return nil, errNotFound
	}
	var out []Record
	for _, r := range recs {
		if r.Type == typ {
			out = append(out, r)
		}
	}
	return out, nil
}

func (b *FixtureBackend) Dial(_ context.Context, address string, _ time.Duration) (time.Duration, string, error) {
	if ms, ok := b.F.Open[address]; ok {
		return time.Duration(ms) * time.Millisecond, address, nil
	}
	return 0, "", errors.New("connection refused")
}

func (b *FixtureBackend) Sockets(context.Context) ([]winnet.Socket, error) {
	if b.Err != nil {
		return nil, b.Err
	}
	var out []winnet.Socket
	for _, s := range b.F.Sockets {
		out = append(out, winnet.Socket{Proto: s.Proto, Local: s.Local, PID: s.PID})
	}
	return out, nil
}

func (b *FixtureBackend) Process(pid uint32) winnet.Process {
	for _, p := range b.F.Processes {
		if p.PID == pid {
			return winnet.Process{PID: pid, Name: p.Name, Path: p.Path, Created: p.Created}
		}
	}
	return winnet.Process{PID: pid}
}

// Kill records pid as ended. It returns winnet.ErrNotFound, and records
// nothing, when created and the fixture process's creation time are both
// known and differ (the PID now belongs to another process).
func (b *FixtureBackend) Kill(pid uint32, created int64) error {
	if created != 0 {
		for _, p := range b.F.Processes {
			if p.PID == pid && p.Created != 0 && p.Created != created {
				return winnet.ErrNotFound
			}
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.killed = append(b.killed, pid)
	return nil
}
