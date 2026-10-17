package shell

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMergePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a Unix PATH; macOS and Unix only")
	}
	sep := string(filepath.ListSeparator)
	join := func(d ...string) string { return strings.Join(d, sep) }
	launchd := join("/usr/bin", "/bin", "/usr/sbin", "/sbin")
	if !needsPath(launchd, "/Users/a") || needsPath(join("/opt/homebrew/bin", "/usr/bin"), "/Users/a") ||
		needsPath(join("/usr/bin", "/Users/a/.local/bin"), "/Users/a") {
		t.Error("needsPath")
	}
	if got := mergePath(launchd, join("/opt/homebrew/bin", "/usr/bin", "", "/bin"), "/Users/a"); got != join("/opt/homebrew/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin") {
		t.Errorf("merged = %q", got)
	}
	// Without the login shell's PATH: the well-known folders.
	if got := mergePath("/usr/bin", " ", "/Users/a"); got != join("/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin", "/Users/a/.local/bin", "/usr/bin") {
		t.Errorf("fallback = %q", got)
	}
}
