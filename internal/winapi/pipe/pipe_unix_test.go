//go:build !windows

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
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// shortDir is a socket directory for a test, short enough for sun_path
// (t.TempDir() on macOS is too long for some names).
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "glpipe")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("XDG_RUNTIME_DIR", dir)
	return dir
}

func testName() string { return `\\.\pipe\golauncher-test-` + rand.Text() }

func listen(t *testing.T) (net.Listener, string) {
	t.Helper()
	shortDir(t)
	name := testName()
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

func TestEchoConcurrent(t *testing.T) {
	l, name := listen(t)
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
	var clients sync.WaitGroup
	for i := range 8 {
		clients.Go(func() {
			c := dial(t, name)
			c.SetDeadline(time.Now().Add(5 * time.Second))
			r := bufio.NewReader(c)
			for j := range 20 {
				msg := fmt.Sprintf("client %d message %d %s\n", i, j, strings.Repeat("x", 1000*j))
				if _, err := io.WriteString(c, msg); err != nil {
					t.Error(err)
					return
				}
				if got, err := r.ReadString('\n'); err != nil || got != msg {
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
	if got := l.Addr().String(); got != name {
		t.Errorf("Addr = %q", got)
	}
}

func TestDialNotFound(t *testing.T) {
	shortDir(t)
	if _, err := Dial(t.Context(), testName()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Dial = %v, want ErrNotFound", err)
	}
}

func TestPeerCloseEOF(t *testing.T) {
	l, name := listen(t)
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := l.Accept()
		accepted <- c
	}()
	client := dial(t, name)
	server := <-accepted
	if server == nil {
		t.FailNow()
	}
	server.Write([]byte("bye"))
	server.Close()
	if b, err := io.ReadAll(client); err != nil || string(b) != "bye" {
		t.Fatalf("client ReadAll = %q, %v", b, err)
	}
	if _, err := server.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Read on closed conn = %v, want net.ErrClosed", err)
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
}

// TestInUseAndStale: a second server is refused while the first listens;
// the socket file of a server that is gone is replaced.
func TestInUseAndStale(t *testing.T) {
	l, name := listen(t)
	if l2, err := Listen(name); !errors.Is(err, ErrInUse) {
		if l2 != nil {
			l2.Close()
		}
		t.Fatalf("second Listen = %v, want ErrInUse", err)
	}
	path, _ := socketPath(name)
	l.(*listener).SetUnlinkOnClose(false)
	l.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("socket file: %v", err)
	}
	l3, err := Listen(name)
	if err != nil {
		t.Fatalf("Listen over a stale socket: %v", err)
	}
	l3.Close()
}

func TestPermissions(t *testing.T) {
	dir := shortDir(t)
	os.Chmod(dir, 0o755)
	l, err := Listen(testName())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	fi, err := os.Stat(filepath.Join(dir, "golauncher"))
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("socket directory: %v, %v", fi.Mode(), err)
	}
}

func TestSocketFile(t *testing.T) {
	if got := socketFile("/run/u", `\\.\pipe\golauncher-1-agent`); got != "/run/u/golauncher-1-agent.sock" {
		t.Errorf("socketFile = %q", got)
	}
	if got := socketFile("/run/u", `a/b\c`); got != "/run/u/a_b_c.sock" {
		t.Errorf("socketFile = %q", got)
	}
	long := socketFile("/run/u", strings.Repeat("x", 200))
	if len(long) > maxSocketPath || !strings.HasPrefix(long, "/run/u/") || !strings.HasSuffix(long, ".sock") {
		t.Errorf("long name = %q", long)
	}
	if long == socketFile("/run/u", strings.Repeat("x", 199)) {
		t.Error("hashes collide")
	}
	if got, _ := socketPath("/abs/path.sock"); got != "/abs/path.sock" {
		t.Errorf("absolute = %q", got)
	}
}

func TestAgentName(t *testing.T) {
	dir := shortDir(t)
	name, err := AgentName()
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := UserSID()
	if name != filepath.Join(dir, "golauncher", "golauncher-"+uid+"-agent.sock") {
		t.Errorf("AgentName = %q", name)
	}
}
