package pipe

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerUID returns the effective user ID of the process at the other end
// of c (getpeereid).
func peerUID(c *net.UnixConn) (uid int, ok bool) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, false
	}
	var cred *unix.Xucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil || cerr != nil {
		return 0, false
	}
	return int(cred.Uid), true
}
