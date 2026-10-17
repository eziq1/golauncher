package agentrt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// The discovery (R84): every installation, its version, the automatic
// choice and why, the configured program, its fallback, and the custom path
// check. The programs are copies of the test binary, whose version is in
// their names (fake-<x.y.z>).

// fakeName is the file name of a fake CLI of version v.
func fakeName(v string) string {
	if runtime.GOOS == "windows" {
		return "fake-" + v + ".exe"
	}
	return "fake-" + v
}

// newDiscoveryRuntime returns a per-turn runtime of a on the fake CLIs.
func newDiscoveryRuntime(t *testing.T, a testAdapter, path string) *Runtime {
	t.Helper()
	a.perTurnOnly = true
	dir := t.TempDir()
	r := New(a, Options{Path: path, Dir: filepath.Join(dir, "work"),
		Env: []string{"AGENTRT_FAKE=1", "AGENTRT_FAKE_LOG=" + filepath.Join(dir, "log.txt")}})
	t.Cleanup(func() { r.Close() })
	return r
}

func installPaths(d backend.Discovery) []string {
	out := make([]string, len(d.Installs))
	for i, in := range d.Installs {
		out[i] = filepath.Base(in.Path)
	}
	return out
}

// TestDiscoverCandidates: the adapter's search, probed; the launcher is
// listed but not probed; the highest acceptable version is chosen, as
// Check does; the result is cached until a refresh.
func TestDiscoverCandidates(t *testing.T) {
	dir := t.TempDir()
	old, mid, high := copyExe(t, dir, fakeName("0.9.0")), copyExe(t, dir, fakeName("1.2.0")), copyExe(t, dir, fakeName("1.5.0"))
	launcher := filepath.Join(dir, "fake.cmd")
	if err := os.WriteFile(launcher, []byte("@echo off\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var searches atomic.Int32
	found := func() []Found {
		searches.Add(1)
		var out []Found
		for _, f := range []Found{{Path: mid, Source: backend.SourcePath}, {Path: old, Source: backend.SourceInstallDir},
			{Path: high, Source: backend.SourceApp}, {Path: launcher, Source: backend.SourcePath, Launcher: true}} {
			if isFile(f.Path) {
				out = append(out, f)
			}
		}
		return out
	}
	cands := func() ([]string, error) {
		var out []string
		for _, f := range found() {
			if !f.Launcher {
				out = append(out, f.Path)
			}
		}
		return out, nil
	}
	r := newDiscoveryRuntime(t, testAdapter{candidates: cands, discover: found}, "")
	d := r.Discover(t.Context(), false)
	if got := installPaths(d); !slices.Equal(got, []string{fakeName("1.2.0"), fakeName("0.9.0"), fakeName("1.5.0"), "fake.cmd"}) {
		t.Fatalf("installs %q", got)
	}
	in := d.Installs
	if in[0].Version != "1.2.0" || !in[0].Usable() || in[1].Version != "0.9.0" || !in[1].TooOld || in[1].Usable() ||
		in[2].Version != "1.5.0" || in[2].Source != backend.SourceApp || !in[3].Launcher || in[3].Version != "" || in[3].VersionError != "" {
		t.Fatalf("installs %+v", in)
	}
	if d.Auto != 2 || d.InUse != 2 || d.AutoReason != backend.ReasonHighest || d.Status != backend.StatusAvailable || d.MinVersion != "1.0.0" {
		t.Fatalf("discovery %+v", d)
	}
	// Check chooses the same one.
	if st, err := r.Check(t.Context()); err != nil || st.Version != "1.5.0" || r.Program() != high {
		t.Fatalf("Check = %+v, %v, %s", st, err, r.Program())
	}

	// Cached until a refresh.
	n := searches.Load()
	if err := os.Remove(high); err != nil {
		t.Fatal(err)
	}
	if d := r.Discover(t.Context(), false); len(d.Installs) != 4 || searches.Load() != n {
		t.Fatalf("not cached: %d installs, %d searches", len(d.Installs), searches.Load()-n)
	}
	d = r.Discover(t.Context(), true)
	if got := installPaths(d); len(got) != 3 || d.Auto != 0 || d.AutoReason != backend.ReasonOnly {
		t.Fatalf("after a refresh: %q %+v", got, d)
	}
}

// TestDiscoverProgram: a chosen program is used instead of the automatic
// choice; one that disappeared falls back to it, which Discover and the
// chat (once per conversation) report.
func TestDiscoverProgram(t *testing.T) {
	dir := t.TempDir()
	mid, high := copyExe(t, dir, fakeName("1.2.0")), copyExe(t, dir, fakeName("1.5.0"))
	other := copyExe(t, t.TempDir(), fakeName("1.3.0"))
	cands := func() ([]string, error) { return []string{mid, high}, nil }
	r := newDiscoveryRuntime(t, testAdapter{candidates: cands}, "")

	r.SetProgram(mid)
	d := r.Discover(t.Context(), false)
	if d.Configured != mid || d.InUse != 0 || d.Auto != 1 || d.ConfiguredMissing || d.Status != backend.StatusAvailable {
		t.Fatalf("configured found: %+v", d)
	}
	if st, err := r.Check(t.Context()); err != nil || st.Version != "1.2.0" {
		t.Fatalf("Check = %+v, %v", st, err)
	}

	// A program that the search does not find is listed as custom.
	r.SetProgram(other)
	d = r.Discover(t.Context(), false)
	if len(d.Installs) != 3 || d.InUse != 2 || d.Installs[2].Source != backend.SourceCustom || d.Installs[2].Version != "1.3.0" || d.Installs[2].Shadowed {
		t.Fatalf("custom: %+v", d)
	}
	if st, err := r.Check(t.Context()); err != nil || st.Version != "1.3.0" {
		t.Fatalf("Check = %+v, %v", st, err)
	}

	// It disappears: the automatic choice answers, and the chat says so once.
	if err := os.Remove(other); err != nil {
		t.Fatal(err)
	}
	r.SetProgram("")
	r.SetProgram(other) // as the plugin does when the preferences are read again
	d = r.Discover(t.Context(), false)
	if !d.ConfiguredMissing || d.InUse != d.Auto || d.Auto != 1 || d.Status != backend.StatusAvailable {
		t.Fatalf("missing: %+v", d)
	}
	if st, err := r.Check(t.Context()); err != nil || st.Version != "1.5.0" {
		t.Fatalf("Check = %+v, %v", st, err)
	}
	c, err := r.Open(t.Context(), backend.OpenOptions{SessionID: "chat-1"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	notices := func() int {
		n := 0
		for _, ev := range events(t, mustSend(t, c, "hi")) {
			if ev.Kind == backend.EventNotice && ev.Text == backend.NoticeProgramMissing {
				n++
			}
		}
		return n
	}
	if n := notices(); n != 1 {
		t.Fatalf("%d notices in the first turn", n)
	}
	if n := notices(); n != 0 {
		t.Fatalf("%d notices in the second turn", n)
	}

	// Nothing else is found either: not installed, naming the program.
	r2 := newDiscoveryRuntime(t, testAdapter{candidates: func() ([]string, error) { return nil, nil }}, other)
	_, err = r2.Check(t.Context())
	if e, ok := errors.AsType[*backend.Error](err); !ok || e.Kind != backend.ErrNotInstalled || e.Message != other {
		t.Fatalf("Check = %v", err)
	}
	if d := r2.Discover(t.Context(), false); d.Status != backend.StatusNotInstalled || !d.ConfiguredMissing || d.InUse != -1 {
		t.Fatalf("discovery %+v", d)
	}
}

// TestDiscoverCommands: without an adapter search, every match of the
// commands on PATH and the install paths are listed; the automatic choice is
// the first (as find), the others are shadowed.
func TestDiscoverCommands(t *testing.T) {
	a, b, c := t.TempDir(), t.TempDir(), t.TempDir()
	name := fakeName("1.2.0")
	copyExe(t, a, name)
	copyExe(t, b, name)
	inst := copyExe(t, c, name)
	t.Setenv("PATH", a+string(os.PathListSeparator)+b)
	t.Setenv("PATHEXT", ".COM;.EXE")
	cmd := "fake-1.2.0"
	r := newDiscoveryRuntime(t, testAdapter{commands: []string{cmd}, installs: []string{inst}}, "")
	d := r.Discover(t.Context(), false)
	if len(d.Installs) != 3 {
		t.Fatalf("installs %+v", d.Installs)
	}
	for i, want := range []string{a, b, c} {
		if in := d.Installs[i]; !samePath(filepath.Dir(in.Path), want) || in.Version != "1.2.0" {
			t.Fatalf("install %d: %+v", i, in)
		}
	}
	in := d.Installs
	if in[0].Source != backend.SourcePath || in[1].Source != backend.SourcePath || in[2].Source != backend.SourceInstallDir ||
		in[0].Shadowed || !in[1].Shadowed || !in[2].Shadowed {
		t.Fatalf("installs %+v", in)
	}
	if d.Auto != 0 || d.AutoReason != backend.ReasonFirst || d.InUse != 0 {
		t.Fatalf("discovery %+v", d)
	}
	if _, err := r.Check(t.Context()); err != nil || !samePath(r.Program(), in[0].Path) {
		t.Fatalf("Check: %v, %s", err, r.Program())
	}
}

// TestCommandsFound: the PATH matches in exec.LookPath's order (each
// directory with each extension), without duplicates, then the install
// paths; on every system, with Windows and Unix rules.
func TestCommandsFound(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"a/claude.cmd", "a/claude.exe", "b/claude.exe", "c/claude"} {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	j := func(rel string) string { return filepath.Join(root, rel) }
	info := Info{Commands: []string{"claude"}, InstallPaths: []string{j("b/claude.exe"), j("missing/claude.exe")}}
	got := commandsFound(info, []string{j("a"), "", `"` + j("b") + `"`, j("c")}, pathExts("windows", ".EXE;.CMD"), "windows")
	var rels []string
	for _, f := range got {
		r, _ := filepath.Rel(root, f.Path)
		rels = append(rels, filepath.ToSlash(r)+":"+string(f.Source))
	}
	if want := []string{"a/claude.exe:path", "a/claude.cmd:path", "b/claude.exe:path"}; !slices.Equal(rels, want) {
		t.Fatalf("windows: %q, want %q", rels, want)
	}
	if got := pathExts("windows", ""); !slices.Equal(got, []string{".com", ".exe", ".bat", ".cmd"}) {
		t.Fatalf("default PATHEXT: %q", got)
	}
	if runtime.GOOS != "windows" {
		got = commandsFound(info, []string{j("a"), j("c")}, pathExts("darwin", ""), "darwin")
		if len(got) != 2 || filepath.Base(filepath.Dir(got[0].Path)) != "c" || got[1].Source != backend.SourceInstallDir {
			t.Fatalf("unix: %+v", got)
		}
	}
}

// TestProbe: a typed program must exist, be a program and print an
// acceptable version.
func TestProbe(t *testing.T) {
	dir := t.TempDir()
	old, ok := copyExe(t, dir, fakeName("0.9.0")), copyExe(t, dir, fakeName("1.2.0"))
	r := newDiscoveryRuntime(t, testAdapter{candidates: func() ([]string, error) { return nil, nil }}, "")
	for _, tt := range []struct {
		path string
		want func(backend.Install) bool
	}{
		{filepath.Join(dir, "missing.exe"), func(in backend.Install) bool { return in.VersionError == backend.ProbeNotFound }},
		{"", func(in backend.Install) bool { return in.VersionError == backend.ProbeNotFound }},
		{dir, func(in backend.Install) bool { return in.VersionError == backend.ProbeNotProgram }},
		{old, func(in backend.Install) bool { return in.TooOld && in.Version == "0.9.0" && !in.Usable() }},
		{`"` + ok + `"`, func(in backend.Install) bool {
			return in.Usable() && in.Version == "1.2.0" && in.Path == ok && in.Source == backend.SourceCustom
		}},
	} {
		if in := r.Probe(t.Context(), tt.path); !tt.want(in) {
			t.Errorf("Probe(%s) = %+v", filepath.Base(tt.path), in)
		}
	}
}

// TestDiscoverStatus: not installed (with the adapter's reason), too old,
// not signed in.
func TestDiscoverStatus(t *testing.T) {
	dir := t.TempDir()
	old, ok := copyExe(t, dir, fakeName("0.9.0")), copyExe(t, dir, fakeName("1.2.0"))
	r := newDiscoveryRuntime(t, testAdapter{candidates: func() ([]string, error) { return nil, errors.New("only a launcher script was found") }}, "")
	if d := r.Discover(context.Background(), false); d.Status != backend.StatusNotInstalled || d.Message != "only a launcher script was found" || d.InUse != -1 {
		t.Fatalf("not installed: %+v", d)
	}
	r = newDiscoveryRuntime(t, testAdapter{candidates: func() ([]string, error) { return []string{old}, nil }}, "")
	if d := r.Discover(context.Background(), false); d.Status != backend.StatusTooOld || d.Auto != -1 {
		t.Fatalf("too old: %+v", d)
	}
	r = newDiscoveryRuntime(t, testAdapter{candidates: func() ([]string, error) { return []string{ok}, nil }, login: true}, "")
	if d := r.Discover(context.Background(), false); d.Status != backend.StatusAvailable {
		t.Fatalf("signed in: %+v", d)
	}
	t.Setenv("AGENTRT_FAKE_LOGIN", "fail")
	if d := r.Discover(context.Background(), true); d.Status != backend.StatusNotSignedIn || d.InUse != 0 {
		t.Fatalf("not signed in: %+v", d)
	}
}
