package agentrt

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// CLIs under Node.js version managers (R111): how they are listed, when the
// automatic choice takes them (PATH first, then the manager's default
// version, then the highest version), and that they run with their folder
// on PATH, where their node is.

// setInstallDirs makes the runtime search dirs after PATH for the rest of
// the test.
func setInstallDirs(t *testing.T, dirs ...BinDir) {
	t.Helper()
	old := installDirs
	installDirs = func() []BinDir { return dirs }
	t.Cleanup(func() { installDirs = old })
}

// versionCommands are the command names of the fake CLIs of versions vs
// (their version is in their name).
func versionCommands(vs ...string) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = "fake-" + v
	}
	return out
}

func mkdir(t *testing.T, elem ...string) string {
	t.Helper()
	d := filepath.Join(elem...)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// TestDiscoverVersionManagers: the commands are also looked for in the
// version managers' folders, listed with their manager; the one on PATH
// keeps priority, then nvm's default version, then the highest version.
func TestDiscoverVersionManagers(t *testing.T) {
	root := t.TempDir()
	onPath := mkdir(t, root, "bin")
	def := mkdir(t, root, ".nvm", "versions", "node", "v20.20.0", "bin")
	high := mkdir(t, root, ".nvm", "versions", "node", "v22.1.0", "bin")
	volta := mkdir(t, root, ".volta", "bin")
	copyExe(t, onPath, fakeName("1.1.0"))
	copyExe(t, def, fakeName("1.3.0"))
	copyExe(t, high, fakeName("1.5.0"))
	copyExe(t, volta, fakeName("1.4.0"))
	setInstallDirs(t, BinDir{Dir: def, Manager: "nvm", Default: true}, BinDir{Dir: high, Manager: "nvm"}, BinDir{Dir: volta, Manager: "volta"})
	t.Setenv("PATHEXT", ".EXE")
	cmds := versionCommands("1.1.0", "1.3.0", "1.4.0", "1.5.0")

	// On PATH: it is used, the managers' ones are shadowed.
	t.Setenv("PATH", onPath)
	r := newDiscoveryRuntime(t, testAdapter{commands: cmds}, "")
	d := r.Discover(t.Context(), false)
	var got []string
	for _, in := range d.Installs {
		got = append(got, filepath.Base(in.Path)+":"+string(in.Source)+":"+in.Manager)
	}
	want := []string{fakeName("1.1.0") + ":path:", fakeName("1.3.0") + ":versionManager:nvm",
		fakeName("1.5.0") + ":versionManager:nvm", fakeName("1.4.0") + ":versionManager:volta"}
	if !slices.Equal(got, want) {
		t.Fatalf("installs %q, want %q", got, want)
	}
	if d.Auto != 0 || d.AutoReason != backend.ReasonFirst || !d.Installs[1].Shadowed || !d.Installs[2].Shadowed || !d.Installs[3].Shadowed {
		t.Fatalf("PATH first: %+v", d)
	}
	if st, err := r.Check(t.Context()); err != nil || st.Version != "1.1.0" {
		t.Fatalf("Check = %+v, %v", st, err)
	}

	// Not on PATH: nvm's default version, although others are newer.
	t.Setenv("PATH", mkdir(t, root, "empty"))
	r = newDiscoveryRuntime(t, testAdapter{commands: cmds}, "")
	d = r.Discover(t.Context(), false)
	if len(d.Installs) != 3 || d.Auto != 0 || d.AutoReason != backend.ReasonManagerDefault || d.Installs[0].Manager != "nvm" ||
		!d.Installs[1].Shadowed || !d.Installs[2].Shadowed || d.Status != backend.StatusAvailable {
		t.Fatalf("nvm default: %+v", d)
	}
	if st, err := r.Check(t.Context()); err != nil || st.Version != "1.3.0" {
		t.Fatalf("Check = %+v, %v", st, err)
	}

	// The default version's CLI is too old: the highest of the others.
	if err := os.Remove(filepath.Join(def, fakeName("1.3.0"))); err != nil {
		t.Fatal(err)
	}
	copyExe(t, def, fakeName("0.9.0"))
	r = newDiscoveryRuntime(t, testAdapter{commands: versionCommands("0.9.0", "1.4.0", "1.5.0")}, "")
	d = r.Discover(t.Context(), false)
	if d.Auto < 0 || d.Installs[d.Auto].Version != "1.5.0" || d.AutoReason != backend.ReasonHighest || !d.Installs[0].TooOld {
		t.Fatalf("highest: %+v", d)
	}
	if st, err := r.Check(t.Context()); err != nil || st.Version != "1.5.0" {
		t.Fatalf("Check = %+v, %v", st, err)
	}
}

// TestDiscoverCandidatesVersionManagers: an adapter's candidates under a
// version manager are taken only when none of the others can be used.
func TestDiscoverCandidatesVersionManagers(t *testing.T) {
	dir := t.TempDir()
	old := copyExe(t, mkdir(t, dir, "brew"), fakeName("0.9.0"))
	mid := copyExe(t, mkdir(t, dir, "app"), fakeName("1.2.0"))
	vm := copyExe(t, mkdir(t, dir, "nvm"), fakeName("1.5.0"))
	found := []Found{{Path: old, Source: backend.SourceInstallDir}, {Path: mid, Source: backend.SourceApp},
		{Path: vm, Source: backend.SourceNpmVendor, Manager: "nvm"}}
	adapter := func(fs []Found) testAdapter {
		return testAdapter{
			discover: func() []Found { return fs },
			candidates: func() ([]string, error) {
				var out []string
				for _, f := range fs {
					out = append(out, f.Path)
				}
				return out, nil
			},
		}
	}
	r := newDiscoveryRuntime(t, adapter(found), "")
	d := r.Discover(t.Context(), false)
	if d.Auto != 1 || !d.Installs[2].Shadowed || d.Installs[2].Manager != "nvm" {
		t.Fatalf("usable outside the manager: %+v", d)
	}
	if st, err := r.Check(t.Context()); err != nil || st.Version != "1.2.0" {
		t.Fatalf("Check = %+v, %v", st, err)
	}
	r = newDiscoveryRuntime(t, adapter([]Found{found[0], found[2]}), "")
	d = r.Discover(t.Context(), false)
	if d.Auto != 1 || d.AutoReason != backend.ReasonOnly {
		t.Fatalf("only the manager's: %+v", d)
	}
	if st, err := r.Check(t.Context()); err != nil || st.Version != "1.5.0" {
		t.Fatalf("Check = %+v, %v", st, err)
	}
}

// TestNodeNextToCLI: a CLI that needs node (like npm's "#!/usr/bin/env
// node" launcher), found in a version manager's folder that is not on PATH,
// runs with that folder first on PATH: its checks, its conversations, and
// the same program chosen by its path (R84).
func TestNodeNextToCLI(t *testing.T) {
	root := t.TempDir()
	bin := mkdir(t, root, ".nvm", "versions", "node", "v20.20.0", "bin")
	cli := copyExe(t, bin, fakeName("1.2.0"))
	node := "node"
	if runtime.GOOS == "windows" {
		node = "node.exe"
	}
	if err := os.WriteFile(filepath.Join(bin, node), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	setInstallDirs(t, BinDir{Dir: bin, Manager: "nvm", Default: true})
	t.Setenv("PATH", mkdir(t, root, "empty"))
	t.Setenv("PATHEXT", ".EXE")
	newRuntime := func(path string) *Runtime {
		a := testAdapter{commands: versionCommands("1.2.0"), perTurnOnly: true}
		r := New(a, Options{Path: path, Dir: filepath.Join(root, "work"),
			Env: []string{"AGENTRT_FAKE=1", "AGENTRT_FAKE_NEEDNODE=1", "AGENTRT_FAKE_LOG=" + filepath.Join(root, "log.txt")}})
		t.Cleanup(func() { r.Close() })
		return r
	}

	r := newRuntime("")
	if d := r.Discover(t.Context(), false); d.Status != backend.StatusAvailable || d.Auto != 0 || d.Installs[0].Version != "1.2.0" {
		t.Fatalf("discovery %+v", d)
	}
	if st, err := r.Check(t.Context()); err != nil || st.Version != "1.2.0" {
		t.Fatalf("Check = %+v, %v", st, err)
	}
	c, err := r.Open(t.Context(), backend.OpenOptions{SessionID: "chat-1"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if text, final, _ := send(t, c, "hi", ""); text != "echo: hi" || final.Kind != backend.EventDone {
		t.Fatalf("turn: %q %+v", text, final)
	}

	// Chosen by its path.
	r = newRuntime(cli)
	if in := r.Probe(t.Context(), cli); !in.Usable() || in.Version != "1.2.0" {
		t.Fatalf("Probe = %+v", in)
	}
	if st, err := r.Check(t.Context()); err != nil || st.Version != "1.2.0" {
		t.Fatalf("Check = %+v, %v", st, err)
	}

	// Without the folder on PATH it would fail: the fake does need node.
	if err := os.Remove(filepath.Join(bin, node)); err != nil {
		t.Fatal(err)
	}
	if in := newRuntime("").Probe(t.Context(), cli); in.Usable() || !strings.Contains(in.VersionError, "node") {
		t.Fatalf("without node: %+v", in)
	}
}
