// Package pipe is a minimal Windows named-pipe transport for the launcher's
// local AI tool server (docs/ai.md §4.4). Listen creates a byte-mode, duplex
// pipe whose DACL grants access only to the current user and which rejects
// remote clients; Dial connects to it. Connections are full-duplex net.Conns
// on overlapped handles: a read and a write may be in flight at once, Close
// cancels them, and deadlines apply to the reads and writes that start after
// they are set.
package pipe

import "errors"

var (
	// ErrNotFound is returned (wrapped) by Dial when no server listens on the pipe.
	ErrNotFound = errors.New("pipe: no server is listening")
	// ErrInUse is returned (wrapped) by Listen when another server already owns the pipe name.
	ErrInUse = errors.New("pipe: another server owns the pipe")
)

// addr is the net.Addr of a pipe endpoint: the pipe name.
type addr string

func (addr) Network() string  { return "pipe" }
func (a addr) String() string { return string(a) }
