package pipe

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func testName(t *testing.T) string {
	t.Helper()
	return `\\.\pipe\golauncher-test-` + rand.Text()
}

func listen(t *testing.T) (net.Listener, string) {
	t.Helper()
	name := testName(t)
	l, err := Listen(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l, name
}

func dial(t *testing.T, name string) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// echoServer accepts until the listener closes, echoing lines back.
func echoServer(l net.Listener) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				defer c.Close()
				io.Copy(c, c)
			})
		}
	})
	return &wg
}

func TestEchoConcurrent(t *testing.T) {
	l, name := listen(t)
	wg := echoServer(l)
	var clients sync.WaitGroup
	for i := range 8 {
		clients.Go(func() {
			c := dial(t, name)
			if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Errorf("SetDeadline: %v", err) // fails if the handle is not on the poller
				return
			}
			r := bufio.NewReader(c)
			for j := range 20 {
				msg := fmt.Sprintf("client %d message %d %s\n", i, j, strings.Repeat("x", 1000*j))
				if _, err := io.WriteString(c, msg); err != nil {
					t.Error(err)
					return
				}
				got, err := r.ReadString('\n')
				if err != nil || got != msg {
					t.Errorf("echo = %.40q, %v; want %.40q", got, err, msg)
					return
				}
			}
			c.Close()
		})
	}
	clients.Wait()
	l.Close()
	wg.Wait()
	if _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Accept after Close = %v, want net.ErrClosed", err)
	}
	if got := l.Addr().Network(); got != "pipe" {
		t.Errorf("Network = %q", got)
	}
}

func TestCloseUnblocksAccept(t *testing.T) {
	l, _ := listen(t)
	done := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Errorf("Accept = %v, want net.ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not unblock Accept")
	}
	if err := l.Close(); err == nil {
		t.Error("second Close returned nil")
	}
}

func TestDialNotFound(t *testing.T) {
	start := time.Now()
	_, err := Dial(t.Context(), testName(t))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Dial = %v, want ErrNotFound", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("Dial took %v", d)
	}
}

func TestDialBusyTimeout(t *testing.T) {
	_, name := listen(t)
	_ = dial(t, name) // occupies the only instance; nobody calls Accept again
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := Dial(ctx, name)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Dial = %v, want DeadlineExceeded", err)
	}
}

func TestPeerCloseEOF(t *testing.T) {
	l, name := listen(t)
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			t.Error(err)
		}
		accepted <- c
	}()
	client := dial(t, name)
	server := <-accepted
	if server == nil {
		t.FailNow()
	}

	// Server writes then closes: the client reads the data, then io.EOF.
	if _, err := server.Write([]byte("bye")); err != nil {
		t.Fatal(err)
	}
	server.Close()
	b, err := io.ReadAll(client)
	if err != nil || string(b) != "bye" {
		t.Fatalf("client ReadAll = %q, %v", b, err)
	}
	if _, err := client.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("client Read after close = %v, want io.EOF", err)
	}
	if _, err := server.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Read on closed conn = %v, want net.ErrClosed", err)
	}

	// Client closes: the server side of a second connection reads io.EOF.
	go func() {
		c, _ := l.Accept()
		accepted <- c
	}()
	client2 := dial(t, name)
	server2 := <-accepted
	if server2 == nil {
		t.FailNow()
	}
	defer server2.Close()
	client2.Close()
	if _, err := server2.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("server Read after client close = %v, want io.EOF", err)
	}
}

func TestReadDeadline(t *testing.T) {
	l, name := listen(t)
	go l.Accept()
	c := dial(t, name)
	c.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, err := c.Read(make([]byte, 1))
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Read = %v, want deadline exceeded", err)
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Errorf("error %v is not a net.Error timeout", err)
	}
}

func TestSecondListenInUse(t *testing.T) {
	_, name := listen(t)
	if l2, err := Listen(name); !errors.Is(err, ErrInUse) {
		if l2 != nil {
			l2.Close()
		}
		t.Fatalf("second Listen = %v, want ErrInUse", err)
	}
}

func TestACL(t *testing.T) {
	sid, err := UserSID()
	if err != nil {
		t.Fatal(err)
	}
	_, name := listen(t)
	c := dial(t, name)
	h := c.(*conn).h
	sd, err := windows.GetSecurityInfo(h, windows.SE_KERNEL_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}

	ctl, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if ctl&windows.SE_DACL_PROTECTED == 0 {
		t.Error("DACL is not protected")
	}
	if owner, _, err := sd.Owner(); err != nil || owner.String() != sid {
		t.Errorf("owner = %v, %v; want %s", owner, err, sid)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		t.Fatalf("DACL = %v, %v", acl, err)
	}
	if acl.AceCount != 1 {
		t.Fatalf("DACL has %d ACEs, want 1", acl.AceCount)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(acl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		t.Errorf("ACE type = %d, want ACCESS_ALLOWED", ace.Header.AceType)
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if aceSID.String() != sid {
		t.Errorf("ACE SID = %s, want %s", aceSID, sid)
	}
	for _, w := range []windows.WELL_KNOWN_SID_TYPE{windows.WinWorldSid, windows.WinAnonymousSid, windows.WinNetworkSid} {
		if aceSID.IsWellKnown(w) {
			t.Errorf("ACE grants well-known SID %d", w)
		}
	}
}

func TestAgentName(t *testing.T) {
	name, err := AgentName()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(name, `\\.\pipe\golauncher-S-1-`) || !strings.HasSuffix(name, "-agent") {
		t.Errorf("AgentName = %q", name)
	}
}

func handleCount(t *testing.T) uint32 {
	t.Helper()
	var n uint32
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")
	if r, _, err := proc.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&n))); r == 0 {
		t.Fatal(err)
	}
	return n
}

func TestNoHandleLeak(t *testing.T) {
	cycle := func() {
		l, err := Listen(testName(t))
		if err != nil {
			t.Fatal(err)
		}
		wg := echoServer(l)
		c, err := Dial(t.Context(), l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte("x"))
		c.Read(make([]byte, 1))
		c.Close()
		l.Close()
		wg.Wait()
	}
	cycle() // warm up lazy runtime handles
	before := handleCount(t)
	for range 50 {
		cycle()
	}
	if after := handleCount(t); after > before+5 {
		t.Errorf("handle count grew from %d to %d", before, after)
	}
}
