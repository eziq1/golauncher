//go:build !windows && !darwin

package network

import (
	"context"
	"errors"
	"net/netip"
	"time"
)

// Adapters is only supported on Windows.
func Adapters() ([]Adapter, error) { return nil, errors.ErrUnsupported }

// WiFiConnections is only supported on Windows.
func WiFiConnections() ([]WiFiConnection, error) { return nil, ErrNoWiFi }

// WiFiProfiles is only supported on Windows.
func WiFiProfiles() ([]WiFiProfile, error) { return nil, ErrNoWiFi }

// ReadWiFiKey is only supported on Windows.
func ReadWiFiKey(ifaceGUID, profile string) (WiFiKey, error) { return WiFiKey{}, ErrNoWiFi }

// Ping is only supported on Windows.
func Ping(ctx context.Context, addr netip.Addr, timeout time.Duration) (EchoReply, error) {
	return EchoReply{}, errors.ErrUnsupported
}

// Sockets is only supported on Windows.
func Sockets() ([]Socket, error) { return nil, errors.ErrUnsupported }

// ProcessInfo is only supported on Windows; it returns only the PID.
func ProcessInfo(pid uint32) Process { return Process{PID: pid} }

// Kill is only supported on Windows.
func Kill(pid uint32, created int64) error { return errors.ErrUnsupported }
