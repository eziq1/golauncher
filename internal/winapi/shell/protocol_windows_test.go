//go:build windows

package shell

import "testing"

func TestProtocolRegistered(t *testing.T) {
	// Windows registers the http scheme for the default browser; no app
	// registers this one.
	if !ProtocolRegistered("http") {
		t.Skip("no handler for http links on this machine")
	}
	if ProtocolRegistered("golauncher-no-such-scheme") || ProtocolRegistered("") {
		t.Error("an unknown scheme counts as registered")
	}
}
