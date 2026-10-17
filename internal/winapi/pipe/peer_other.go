//go:build !windows && !darwin && !linux

package pipe

import "net"

// peerUID is not known here; the socket directory's mode still keeps other
// users out.
func peerUID(*net.UnixConn) (int, bool) { return 0, false }
