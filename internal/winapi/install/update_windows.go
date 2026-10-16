package install

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/eziq1/golauncher/internal/update"
)

// HelperSystem is update.HelperSystem on Windows.
type HelperSystem struct{}

var _ update.HelperSystem = HelperSystem{}

// WaitExit implements update.HelperSystem.
func (HelperSystem) WaitExit(pid int, timeout time.Duration) error {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return nil // gone already (or never ours)
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, uint32(min(timeout.Milliseconds(), 1<<31-1)))
	switch {
	case err != nil:
		return err
	case ev == uint32(windows.WAIT_TIMEOUT):
		return fmt.Errorf("process %d still runs after %s", pid, timeout)
	}
	return nil
}

// Start implements update.HelperSystem.
func (HelperSystem) Start(exe string, args []string, env []string) (update.Process, error) {
	cmd := exec.Command(exe, args...)
	cmd.Dir = filepath.Dir(exe)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("update: starting %s: %w", exe, err)
	}
	p := &process{cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

type process struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func (p *process) Exited() <-chan struct{} { return p.done }
func (p *process) Kill() error             { return p.cmd.Process.Kill() }

// StartDetached starts exe with args without waiting for it, hidden from
// the process that starts it (the update helper outlives golauncher).
func StartDetached(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Dir = filepath.Dir(exe)
	const detachedProcess = 0x00000008
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | windows.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("update: starting %s: %w", exe, err)
	}
	return cmd.Process.Release()
}

// winHTTPProxyConfig is WINHTTP_CURRENT_USER_IE_PROXY_CONFIG.
type winHTTPProxyConfig struct {
	autoDetect    int32
	autoConfigURL *uint16
	proxy         *uint16
	proxyBypass   *uint16
}

var (
	winhttp                            = windows.NewLazySystemDLL("winhttp.dll")
	procGetIEProxyConfigForCurrentUser = winhttp.NewProc("WinHttpGetIEProxyConfigForCurrentUser")
	procGlobalFree                     = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalFree")
)

// SystemProxy returns the proxy function for updates: the environment's
// proxy, else the proxy set in Windows' settings ("Use a proxy server";
// automatic configuration scripts are not supported and mean no proxy).
func SystemProxy() func(*http.Request) (*url.URL, error) {
	proxy, bypass, err := ieProxy()
	if err != nil {
		return http.ProxyFromEnvironment
	}
	return update.SystemProxy(proxy, bypass)
}

// ieProxy reads the current user's proxy setting.
func ieProxy() (proxy, bypass string, err error) {
	if err := procGetIEProxyConfigForCurrentUser.Find(); err != nil {
		return "", "", err
	}
	var c winHTTPProxyConfig
	r, _, e := procGetIEProxyConfigForCurrentUser.Call(uintptr(unsafe.Pointer(&c)))
	if r == 0 {
		return "", "", errors.Join(errors.New("WinHttpGetIEProxyConfigForCurrentUser failed"), e)
	}
	take := func(p *uint16) string {
		if p == nil {
			return ""
		}
		s := windows.UTF16PtrToString(p)
		_, _, _ = procGlobalFree.Call(uintptr(unsafe.Pointer(p)))
		return s
	}
	_ = take(c.autoConfigURL)
	return take(c.proxy), take(c.proxyBypass), nil
}
