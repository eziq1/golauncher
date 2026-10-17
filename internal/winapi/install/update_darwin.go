package install

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/eziq1/golauncher/internal/install"
	"github.com/eziq1/golauncher/internal/update"
)

// HelperSystem is update.HelperSystem on macOS.
type HelperSystem struct{}

var _ update.HelperSystem = HelperSystem{}

// WaitExit implements update.HelperSystem. golauncher is not the helper's
// child, so it is polled.
func (HelperSystem) WaitExit(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			return nil // gone (or never ours)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process %d still runs after %s", pid, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Start implements update.HelperSystem. golauncher.app is opened through
// Launch Services (open -n -W, which ends when the app quits), so that the
// new version runs as the app and keeps its permissions; env reaches it
// through open --env.
func (HelperSystem) Start(exe string, args []string, env []string) (update.Process, error) {
	var cmd *exec.Cmd
	app, inBundle := install.BundleOf(exe)
	if inBundle {
		cmd = exec.Command("/usr/bin/open", openArgs(app, args, env, true)...)
	} else {
		cmd = exec.Command(exe, args...)
		cmd.Dir = filepath.Dir(exe)
		if len(env) > 0 {
			cmd.Env = append(os.Environ(), env...)
		}
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("update: starting %s: %w", exe, err)
	}
	p := &process{cmd: cmd, bundle: inBundle, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

type process struct {
	cmd    *exec.Cmd
	bundle bool
	done   chan struct{}
}

func (p *process) Exited() <-chan struct{} { return p.done }

// Kill ends the program. For an app opened through open, that is every
// golauncher of this user other than this process (the helper has
// another name), and then open.
func (p *process) Kill() error {
	if p.bundle {
		for _, pid := range others([]string{install.BundleExeName}) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	return p.cmd.Process.Kill()
}

// SystemProxy returns the proxy function for updates: the environment's
// proxy, else the one set in System Settings › Network (scutil --proxy;
// automatic proxy configuration is not supported and means none).
func SystemProxy() func(*http.Request) (*url.URL, error) {
	out, err := exec.Command("/usr/sbin/scutil", "--proxy").Output()
	if err != nil {
		return http.ProxyFromEnvironment
	}
	return update.SystemProxy(update.ParseScutilProxy(string(out)))
}

// LoginItem turns starting at login on and off: with the system's login
// items (SMAppService, through MyGo), and when that fails, with a launch
// agent in ~/Library/LaunchAgents that opens the app.
type LoginItem struct {
	// Set and Enabled are the system's login item (mygo.App.SetOpenAtLogin
	// and OpenAtLogin).
	Set     func(bool) error
	Enabled func() bool
	// Agent is the launch agent's file; App the bundle it opens.
	Agent, App string
}

// NewLoginItem returns the login item of the running app bundle.
func NewLoginItem(set func(bool) error, enabled func() bool) *LoginItem {
	li := &LoginItem{Set: set, Enabled: enabled}
	if home, err := os.UserHomeDir(); err == nil {
		li.Agent = filepath.Join(home, "Library", "LaunchAgents", install.LaunchAgentLabel+".plist")
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		li.App, _ = install.BundleOf(exe)
	}
	return li
}

// Turn turns starting at login on or off. When the system refuses (or
// waits for the user's approval in System Settings › General › Login
// Items), the launch agent starts golauncher instead; a second start is
// harmless, it hands over to the first.
func (li *LoginItem) Turn(on bool) error {
	if !on {
		return errors.Join(li.Set(false), li.removeAgent())
	}
	err := li.Set(true)
	if err == nil {
		return li.removeAgent()
	}
	if li.Agent == "" || li.App == "" {
		return err // not an app bundle: nothing can start it
	}
	if werr := li.writeAgent(); werr != nil {
		return errors.Join(err, werr)
	}
	return nil
}

// On reports whether golauncher starts at login.
func (li *LoginItem) On() bool {
	if li.Enabled() {
		return true
	}
	_, err := os.Stat(li.Agent)
	return li.Agent != "" && err == nil
}

// writeAgent writes the launch agent; it takes effect at the next login
// (it is not loaded now, which would start a second golauncher).
func (li *LoginItem) writeAgent() error {
	if err := os.MkdirAll(filepath.Dir(li.Agent), 0o755); err != nil {
		return err
	}
	return os.WriteFile(li.Agent, install.LaunchAgentPlist(li.App, []string{"--background"}), 0o644)
}

func (li *LoginItem) removeAgent() error {
	if li.Agent == "" {
		return nil
	}
	if err := os.Remove(li.Agent); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// SetLoginItem turns starting at login on or off with set, falling back to
// a launch agent (LoginItem).
func SetLoginItem(on bool, set func(bool) error, enabled func() bool) error {
	return NewLoginItem(set, enabled).Turn(on)
}

// LoginItemOn reports whether golauncher starts at login, through the
// system's login items or the launch agent.
func LoginItemOn(set func(bool) error, enabled func() bool) bool {
	return NewLoginItem(set, enabled).On()
}
