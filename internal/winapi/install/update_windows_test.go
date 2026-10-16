package install

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// cmdExe is the system's command interpreter, a harmless program to start.
func cmdExe(t *testing.T) string {
	t.Helper()
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "cmd.exe")
}

func TestHelperSystemStartAndWait(t *testing.T) {
	integration(t)
	var hs HelperSystem
	marker := filepath.Join(t.TempDir(), "m")
	// The environment reaches the program.
	p, err := hs.Start(cmdExe(t), []string{"/d", "/c", "type nul > %GL_TEST_MARKER%"}, []string{"GL_TEST_MARKER=" + marker})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Exited():
	case <-time.After(10 * time.Second):
		p.Kill()
		t.Fatal("cmd did not exit")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the environment did not reach the program: %v", err)
	}
	// Kill ends a program; WaitExit sees it end.
	p, err = hs.Start(cmdExe(t), []string{"/d", "/c", "ping -n 30 127.0.0.1 >nul"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pid := p.(*process).cmd.Process.Pid
	if err := hs.WaitExit(pid, 200*time.Millisecond); err == nil {
		t.Fatal("WaitExit returned while the program runs")
	}
	p.Kill()
	if err := hs.WaitExit(pid, 10*time.Second); err != nil {
		t.Fatalf("WaitExit after Kill: %v", err)
	}
	// No such process: nothing to wait for.
	if err := hs.WaitExit(0x7ffffff0, time.Second); err != nil {
		t.Fatalf("WaitExit of no process: %v", err)
	}
}

func TestSystemProxyIsUsable(t *testing.T) {
	proxy := SystemProxy()
	u, _ := url.Parse("http://127.0.0.1:1/")
	if got, err := proxy(&http.Request{URL: u}); err != nil || got != nil {
		t.Fatalf("loopback goes through a proxy: %v, %v", got, err)
	}
}
