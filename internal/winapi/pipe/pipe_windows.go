package pipe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const bufSize = 64 << 10

// UserSID returns the string SID of the user running this process, read from
// the process token (e.g. "S-1-5-21-...").
func UserSID() (string, error) {
	tok := windows.GetCurrentProcessToken() // pseudo handle, never closed
	tu, err := tok.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("pipe: token user: %w", err)
	}
	return tu.User.Sid.String(), nil
}

// AgentName returns the agent pipe name `\\.\pipe\golauncher-<user SID>-agent`.
func AgentName() (string, error) {
	sid, err := UserSID()
	if err != nil {
		return "", err
	}
	return `\\.\pipe\golauncher-` + sid + `-agent`, nil
}

// listener owns one unconnected pipe instance at a time; Accept waits for a
// client on it, then creates the next instance before returning the conn.
type listener struct {
	name    *uint16
	addr    addr
	sa      *windows.SecurityAttributes
	closeEv windows.Handle // manual-reset, signalled by Close

	acceptMu sync.Mutex     // serializes Accept and final cleanup in Close
	h        windows.Handle // pending instance, 0 if none (guarded by acceptMu)
	connEv   windows.Handle
	ov       *windows.Overlapped // heap-allocated: the kernel writes it asynchronously
	closed   bool

	closeOnce sync.Once
}

// Listen creates the server end of the named pipe name. The first instance is
// created with FILE_FLAG_FIRST_PIPE_INSTANCE, so Listen fails with ErrInUse
// when another server (of any user) already owns the name.
func Listen(name string) (net.Listener, error) {
	sid, err := UserSID()
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;;GA;;;" + sid + ")")
	if err != nil {
		return nil, fmt.Errorf("pipe: security descriptor: %w", err)
	}
	n16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("pipe: name %q: %w", name, err)
	}
	l := &listener{
		name: n16,
		addr: addr(name),
		sa:   &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd},
		ov:   new(windows.Overlapped),
	}
	if l.h, err = l.newInstance(true); err != nil {
		return nil, err
	}
	if l.closeEv, err = windows.CreateEvent(nil, 1, 0, nil); err == nil {
		l.connEv, err = windows.CreateEvent(nil, 1, 0, nil)
	}
	if err != nil {
		l.release()
		return nil, fmt.Errorf("pipe: create event: %w", err)
	}
	l.ov.HEvent = l.connEv
	return l, nil
}

func (l *listener) newInstance(first bool) (windows.Handle, error) {
	flags := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
	if first {
		flags |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	mode := uint32(windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS)
	h, err := windows.CreateNamedPipe(l.name, flags, mode, windows.PIPE_UNLIMITED_INSTANCES, bufSize, bufSize, 0, l.sa)
	if err != nil {
		if first && errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return 0, fmt.Errorf("%w: %s", ErrInUse, l.addr)
		}
		return 0, fmt.Errorf("pipe: create %s: %w", l.addr, err)
	}
	return h, nil
}

func (l *listener) Addr() net.Addr { return l.addr }

func (l *listener) Accept() (net.Conn, error) {
	l.acceptMu.Lock()
	defer l.acceptMu.Unlock()
	for {
		if l.closed {
			return nil, l.opErr(net.ErrClosed)
		}
		if l.h == 0 {
			h, err := l.newInstance(false)
			if err != nil {
				return nil, err
			}
			l.h = h
		}
		err := l.connect()
		if errors.Is(err, net.ErrClosed) {
			return nil, l.opErr(err)
		}
		h := l.h
		l.h = 0
		if errors.Is(err, windows.ERROR_NO_DATA) { // client came and went
			windows.CloseHandle(h)
			continue
		}
		if err != nil {
			windows.CloseHandle(h)
			return nil, l.opErr(err)
		}
		// Keep an instance listening while the caller serves this client;
		// if this fails, the next Accept retries.
		if next, err := l.newInstance(false); err == nil {
			l.h = next
		}
		return newConn(h, l.addr), nil
	}
}

// connect waits for a client on l.h or for Close.
func (l *listener) connect() error {
	windows.ResetEvent(l.connEv)
	switch err := windows.ConnectNamedPipe(l.h, l.ov); {
	case err == nil, errors.Is(err, windows.ERROR_PIPE_CONNECTED):
		return nil
	case !errors.Is(err, windows.ERROR_IO_PENDING):
		return err
	}
	ev, err := windows.WaitForMultipleObjects([]windows.Handle{l.connEv, l.closeEv}, false, windows.INFINITE)
	var n uint32
	if err != nil || ev != windows.WAIT_OBJECT_0 {
		windows.CancelIoEx(l.h, l.ov)
		windows.GetOverlappedResult(l.h, l.ov, &n, true) // wait until the kernel releases l.ov
		return net.ErrClosed
	}
	return windows.GetOverlappedResult(l.h, l.ov, &n, false)
}

func (l *listener) opErr(err error) error {
	return &net.OpError{Op: "accept", Net: "pipe", Addr: l.addr, Err: err}
}

// Close stops Accept; connections already accepted stay open.
func (l *listener) Close() error {
	err := net.ErrClosed
	l.closeOnce.Do(func() {
		windows.SetEvent(l.closeEv)
		l.acceptMu.Lock() // wait for a pending Accept to unwind
		defer l.acceptMu.Unlock()
		l.closed = true
		l.release()
		err = nil
	})
	if err != nil {
		return l.opErr(err)
	}
	return nil
}

func (l *listener) release() {
	for _, h := range []*windows.Handle{&l.h, &l.connEv, &l.closeEv} {
		if *h != 0 {
			windows.CloseHandle(*h)
			*h = 0
		}
	}
}

// Dial connects to the pipe name, retrying while all instances are busy
// until ctx ends. It returns an error wrapping ErrNotFound when no server
// listens on name.
func Dial(ctx context.Context, name string) (net.Conn, error) {
	n16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("pipe: name %q: %w", name, err)
	}
	delay := time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return nil, dialErr(name, err)
		}
		// SECURITY_IDENTIFICATION: the server may identify but not impersonate us.
		h, err := windows.CreateFile(n16, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
		switch {
		case err == nil:
			return newConn(h, addr(name)), nil
		case errors.Is(err, windows.ERROR_FILE_NOT_FOUND):
			return nil, dialErr(name, ErrNotFound)
		case !errors.Is(err, windows.ERROR_PIPE_BUSY):
			return nil, dialErr(name, err)
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, dialErr(name, ctx.Err())
		case <-t.C:
		}
		delay = min(2*delay, 50*time.Millisecond)
	}
}

func dialErr(name string, err error) error {
	return &net.OpError{Op: "dial", Net: "pipe", Addr: addr(name), Err: err}
}

// conn is one end of a connected pipe. Reads and writes use overlapped I/O
// with their own events, so a read and a write can be in flight at once
// (full duplex) and Close cancels both. (*os.File would put the handle on the
// runtime poller, but it tracks a file offset that concurrent reads and
// writes race on.) A blocked read or write waits on an OS thread.
type conn struct {
	h       windows.Handle
	addr    addr
	closeEv windows.Handle // manual-reset, signalled by Close

	rmu, wmu  sync.Mutex // one read and one write at a time
	rov, wov  *windows.Overlapped
	rdl, wdl  atomic.Int64 // deadlines in Unix nanoseconds, 0 for none
	closed    atomic.Bool
	closeOnce sync.Once
}

func newConn(h windows.Handle, a addr) *conn {
	c := &conn{h: h, addr: a, rov: new(windows.Overlapped), wov: new(windows.Overlapped)}
	var err error
	if c.closeEv, err = windows.CreateEvent(nil, 1, 0, nil); err == nil {
		if c.rov.HEvent, err = windows.CreateEvent(nil, 1, 0, nil); err == nil {
			c.wov.HEvent, err = windows.CreateEvent(nil, 1, 0, nil)
		}
	}
	if err != nil {
		c.closed.Store(true) // every call fails; Close releases what exists
	}
	return c
}

// errDeadline is returned when a deadline passed.
var errDeadline = os.ErrDeadlineExceeded

// io runs one overlapped ReadFile or WriteFile and waits for it, for Close
// or for the deadline dl.
func (c *conn) io(op string, b []byte, ov *windows.Overlapped, dl *atomic.Int64) (int, error) {
	if c.closed.Load() {
		return 0, c.mapErr(op, net.ErrClosed)
	}
	timeout := uint32(windows.INFINITE)
	if d := dl.Load(); d != 0 {
		left := time.Until(time.Unix(0, d))
		if left <= 0 {
			return 0, c.mapErr(op, errDeadline)
		}
		timeout = uint32(min(left.Milliseconds()+1, int64(windows.INFINITE-1)))
	}
	windows.ResetEvent(ov.HEvent)
	var n uint32
	var err error
	if op == "read" {
		err = windows.ReadFile(c.h, b, &n, ov)
	} else {
		err = windows.WriteFile(c.h, b, &n, ov)
	}
	if err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
		return int(n), c.mapErr(op, err)
	}
	ev, werr := windows.WaitForMultipleObjects([]windows.Handle{ov.HEvent, c.closeEv}, false, timeout)
	if werr == nil && ev == windows.WAIT_OBJECT_0 {
		err = windows.GetOverlappedResult(c.h, ov, &n, false)
		return int(n), c.mapErr(op, err)
	}
	// Closed, timed out or failed: cancel, and wait until the kernel is done
	// with ov and b. What completed meanwhile is returned.
	windows.CancelIoEx(c.h, ov)
	err = windows.GetOverlappedResult(c.h, ov, &n, true)
	if err == nil {
		return int(n), nil
	}
	switch {
	case werr != nil:
		err = werr
	case ev == uint32(windows.WAIT_TIMEOUT):
		err = errDeadline
	default:
		err = net.ErrClosed
	}
	return int(n), c.mapErr(op, err)
}

func (c *conn) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	c.rmu.Lock()
	defer c.rmu.Unlock()
	return c.io("read", b, c.rov, &c.rdl)
}

func (c *conn) Write(b []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	done := 0
	for done < len(b) {
		n, err := c.io("write", b[done:], c.wov, &c.wdl)
		done += n
		if err != nil {
			return done, err
		}
	}
	return done, nil
}

// Close cancels the reads and writes in flight and closes the handle.
func (c *conn) Close() error {
	err := error(net.ErrClosed)
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		if c.closeEv != 0 {
			windows.SetEvent(c.closeEv)
		}
		// Wait for the operations in flight to unwind.
		c.rmu.Lock()
		c.wmu.Lock()
		defer c.rmu.Unlock()
		defer c.wmu.Unlock()
		for _, h := range []windows.Handle{c.h, c.rov.HEvent, c.wov.HEvent, c.closeEv} {
			if h != 0 {
				windows.CloseHandle(h)
			}
		}
		err = nil
	})
	if err != nil {
		return c.mapErr("close", err)
	}
	return nil
}

func (c *conn) LocalAddr() net.Addr  { return c.addr }
func (c *conn) RemoteAddr() net.Addr { return c.addr }

func (c *conn) SetDeadline(t time.Time) error {
	c.SetReadDeadline(t)
	return c.SetWriteDeadline(t)
}

// SetReadDeadline sets the deadline of the next reads; a read already
// waiting keeps its own.
func (c *conn) SetReadDeadline(t time.Time) error { c.rdl.Store(unixNano(t)); return nil }

// SetWriteDeadline sets the deadline of the next writes.
func (c *conn) SetWriteDeadline(t time.Time) error { c.wdl.Store(unixNano(t)); return nil }

func unixNano(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

// mapErr turns a closed peer into io.EOF and a closed conn into net.ErrClosed.
func (c *conn) mapErr(op string, err error) error {
	switch {
	case err == nil, err == io.EOF:
		return err
	case op == "read" && (errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED) || errors.Is(err, windows.ERROR_NO_DATA)):
		return io.EOF
	case errors.Is(err, os.ErrClosed):
		err = net.ErrClosed
	}
	return &net.OpError{Op: op, Net: "pipe", Addr: c.addr, Err: err}
}
