package shell

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// loginShellTimeout bounds asking the login shell for its PATH: a slow or
// interactive profile must not hold up the start.
const loginShellTimeout = 2 * time.Second

// fixPath gives golauncher the PATH of the user's login shell when it was
// started with launchd's (see loginpath.go). It runs before anything looks
// up a program.
func fixPath(log *slog.Logger) {
	home, _ := os.UserHomeDir()
	cur := os.Getenv("PATH")
	if !needsPath(cur, home) {
		return
	}
	login, err := loginShellPath()
	if err != nil {
		log.Warn("login shell PATH", "err", err)
	}
	path := mergePath(cur, login, home)
	os.Setenv("PATH", path)
	log.Info("PATH from the login shell", "path", path)
}

// loginShellPath runs $SHELL (or zsh) as a login shell and returns its PATH.
func loginShellPath() (string, error) {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/zsh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), loginShellTimeout)
	defer cancel()
	// The markers keep what a profile prints apart from the PATH.
	out, err := exec.CommandContext(ctx, sh, "-l", "-c", `printf '\n__GLPATH__%s__GLPATH__\n' "$PATH"`).Output()
	if err != nil {
		return "", err
	}
	_, rest, ok := strings.Cut(string(out), "__GLPATH__")
	path, _, ok2 := strings.Cut(rest, "__GLPATH__")
	if !ok || !ok2 {
		return "", nil
	}
	return path, nil
}
