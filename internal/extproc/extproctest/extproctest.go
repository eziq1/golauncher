// Package extproctest builds the example external plugins for tests.
package extproctest

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// RepoRoot returns the repository root (from this source file's location).
func RepoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// BuildExample builds examples/plugins/<name> into dst/<name>: the
// executable <name>.exe plus a copy of its plugin.json and locales. It
// returns the plugin directory.
func BuildExample(dst, name string) (string, error) {
	root := RepoRoot()
	src := filepath.Join(root, "examples", "plugins", name)
	out := filepath.Join(dst, name)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	cmd := exec.Command("go", "build", "-o", filepath.Join(out, name+".exe"), "./examples/plugins/"+name)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building %s: %v\n%s", name, err, b)
	}
	if runtime.GOOS == "darwin" {
		warmUp(filepath.Join(out, name+".exe"))
	}
	if err := copyFile(filepath.Join(src, "plugin.json"), filepath.Join(out, "plugin.json")); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	locales := filepath.Join(src, "locales")
	if _, err := os.Stat(locales); err == nil {
		if err := os.CopyFS(filepath.Join(out, "locales"), os.DirFS(locales)); err != nil && !os.IsExist(err) {
			return "", err
		}
	}
	return out, nil
}

// warmUp runs a freshly built program once, with no input, so that it ends
// at once. macOS checks a new executable the first time it runs, which can
// take seconds on a busy machine: longer than a plugin may take to start.
func warmUp(exe string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, exe).Run() // stdin is empty: plugins exit
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, b, fs.FileMode(0o644))
}
