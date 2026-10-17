package pipe

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerUID returns the user ID of the process at the other end of c
// (SO_PEERCRED).
func peerUID(c *net.UnixConn) (uid int, ok bool) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, false
	}
	var cred *unix.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil || cerr != nil {
		return 0, false
	}
	return int(cred.Uid), true
}
