//go:build !windows

package pipe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// On Unix a "pipe" is a Unix domain socket in a directory only the user can
// enter (mode 0700), and the listener also checks the peer's user ID where
// the system reports it (peerUID). A name that is not an absolute path,
// such as a Windows pipe name `\\.\pipe\golauncher-…`, is mapped to a socket
// file of that base name in the directory (socketPath).

// maxSocketPath bounds the length of a socket path: sun_path holds 104
// bytes on macOS and 108 on Linux, including the terminating NUL.
const maxSocketPath = 100

// UserSID returns the user ID of this process (the Unix counterpart of a
// Windows SID).
func UserSID() (string, error) { return strconv.Itoa(os.Getuid()), nil }

// AgentName returns the agent socket, golauncher-<uid>-agent.sock in the
// socket directory.
func AgentName() (string, error) {
	uid, _ := UserSID()
	return socketPath("golauncher-" + uid + "-agent")
}

// socketDir returns the user's socket directory: $XDG_RUNTIME_DIR/golauncher
// where it is set (Linux), else golauncher/run in the user cache directory
// (~/Library/Caches on macOS), which does not depend on $TMPDIR, so that a
// launcher started from Finder and a bridge started by an AI app agree.
func socketDir() (string, error) {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, "golauncher"), nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("pipe: socket directory: %w", err)
	}
	return filepath.Join(cache, "golauncher", "run"), nil
}

// socketPath maps name to its socket file. A long base name is replaced by
// a hash of it so that the path fits in sun_path.
func socketPath(name string) (string, error) {
	if filepath.IsAbs(name) {
		return name, nil
	}
	dir, err := socketDir()
	if err != nil {
		return "", err
	}
	return socketFile(dir, name), nil
}

// socketFile is the socket of name in dir (see socketPath).
func socketFile(dir, name string) string {
	base := strings.TrimPrefix(name, `\\.\pipe\`)
	base = strings.NewReplacer(`\`, "_", "/", "_").Replace(base)
	p := filepath.Join(dir, base+".sock")
	if len(p) > maxSocketPath || base == "" {
		sum := sha256.Sum256([]byte(name))
		p = filepath.Join(dir, hex.EncodeToString(sum[:8])+".sock")
	}
	return p
}

// privateDir creates dir (and its parents) and makes sure only the user
// can use it.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("pipe: %w", err)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("pipe: %w", err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("pipe: %s is not a directory of this user", dir)
	}
	if fi.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("pipe: %w", err)
		}
	}
	return nil
}

// listener is a Unix socket listener that only accepts the user's own
// connections and reports the name it was created with.
type listener struct {
	*net.UnixListener
	addr addr
}

// Listen creates the socket of name. It fails with ErrInUse when a server
// already listens on it; a socket file left behind by a server that ended
// is replaced.
func Listen(name string) (net.Listener, error) {
	path, err := socketPath(name)
	if err != nil {
		return nil, err
	}
	if err := privateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if c, err := net.Dial("unix", path); err == nil {
		c.Close()
		return nil, fmt.Errorf("%w: %s", ErrInUse, name)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("pipe: removing a stale socket: %w", err)
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("pipe: listen %s: %w", name, err)
	}
	_ = os.Chmod(path, 0o600)
	return &listener{UnixListener: ln, addr: addr(name)}, nil
}

func (l *listener) Addr() net.Addr { return l.addr }

// Accept waits for a connection of this user; others are closed at once.
func (l *listener) Accept() (net.Conn, error) {
	for {
		c, err := l.AcceptUnix()
		if err != nil {
			return nil, err
		}
		if uid, ok := peerUID(c); ok && uid != os.Getuid() {
			c.Close()
			continue
		}
		return c, nil
	}
}

// Dial connects to the socket of name until ctx ends. It returns an error
// wrapping ErrNotFound when no server listens on name.
func Dial(ctx context.Context, name string) (net.Conn, error) {
	path, err := socketPath(name)
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err == nil {
		return c, nil
	}
	if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
		err = ErrNotFound
	} else if ctx.Err() != nil {
		err = ctx.Err()
	}
	return nil, &net.OpError{Op: "dial", Net: "pipe", Addr: addr(name), Err: err}
}
