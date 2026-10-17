package process

import (
	"context"
	"encoding/binary"
	"errors"
	"slices"
	"testing"
	"time"
)

// Captured from `ps -axo pid=,rss=,time=` on macOS 15 (trimmed).
const psFixture = `    1  14336  12:01.80
   88  52240 345:12.03
  402   2112   0:00.05
  999    104 1-02:03:04
 junk line
  1234  x  0:01.00
`

func TestParsePS(t *testing.T) {
	rows := parsePS([]byte(psFixture))
	want := []psRow{
		{PID: 1, RSS: 14336 * 1024, CPU: int64((12*time.Minute + 1800*time.Millisecond) / 100)},
		{PID: 88, RSS: 52240 * 1024, CPU: int64((345*time.Minute + 12030*time.Millisecond) / 100)},
		{PID: 402, RSS: 2112 * 1024, CPU: int64(50 * time.Millisecond / 100)},
		{PID: 999, RSS: 104 * 1024, CPU: int64((26*time.Hour + 3*time.Minute + 4*time.Second) / 100)},
	}
	if !slices.Equal(rows, want) {
		t.Errorf("got %+v\nwant %+v", rows, want)
	}
}

func TestParseCPUTime(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"0:00.00":    0,
		"0:01.23":    1230 * time.Millisecond,
		"61:00.50":   61*time.Minute + 500*time.Millisecond,
		"1:02:03":    time.Hour + 2*time.Minute + 3*time.Second,
		"2-00:00:01": 48*time.Hour + time.Second,
	} {
		if got, err := parseCPUTime(in); err != nil || got != want {
			t.Errorf("%q = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "12", "a:01", "1:-2", "1:1e3", "x-1:00", "1:2:3:4"} {
		if _, err := parseCPUTime(in); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
}

func TestCPUSmoother(t *testing.T) {
	var s cpuSmoother
	id := ID{PID: 7, Created: 1}
	t0 := time.Unix(1000, 0)
	if _, ok := s.value(id, t0); ok {
		t.Fatal("unknown process has a value")
	}
	s.update(id, 100*sec, t0)
	if v, _ := s.value(id, t0.Add(time.Second)); v != 100*sec {
		t.Errorf("one reading: %d, want it unchanged", v)
	}
	// 3 s of CPU in 3 s: 100% of one processor from now on.
	s.update(id, 103*sec, t0.Add(3*time.Second))
	prev := int64(0)
	for i := range 7 {
		v, _ := s.value(id, t0.Add(3*time.Second+time.Duration(i)*500*time.Millisecond))
		if want := 103*sec + int64(i)*sec/2; v != want {
			t.Errorf("step %d: %d, want %d", i, v, want)
		}
		if v < prev {
			t.Errorf("step %d: went back", i)
		}
		prev = v
	}
	// It went idle: the counter stays where the estimate had it (never back).
	s.update(id, 103*sec, t0.Add(6*time.Second))
	v1, _ := s.value(id, t0.Add(6*time.Second))
	v2, _ := s.value(id, t0.Add(8*time.Second))
	if v1 != 106*sec || v2 != v1 {
		t.Errorf("idle: %d, %d; want %d twice", v1, v2, 106*sec)
	}
	s.keep(map[ID]bool{})
	if _, ok := s.value(id, t0); ok {
		t.Error("keep did not forget")
	}
}

// procArgs builds a KERN_PROCARGS2 buffer.
func procArgs(exe string, args []string, env ...string) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(len(args)))
	b = append(b, exe...)
	b = append(b, 0, 0, 0, 0) // NUL and padding
	for _, a := range append(args, env...) {
		b = append(b, a...)
		b = append(b, 0)
	}
	return b
}

func TestParseProcArgs(t *testing.T) {
	b := procArgs("/Applications/Contoso.app/Contents/MacOS/Contoso", []string{"Contoso", "--open", "My File.txt", ""}, "SECRET=env")
	exe, args, err := parseProcArgs(b)
	if err != nil || exe != "/Applications/Contoso.app/Contents/MacOS/Contoso" || !slices.Equal(args, []string{"Contoso", "--open", "My File.txt", ""}) {
		t.Fatalf("got %q %q %v", exe, args, err)
	}
	if got := joinArgs(args); got != `Contoso --open "My File.txt" ""` {
		t.Errorf("joined: %s", got)
	}
	if got := joinArgs([]string{`say "hi"`}); got != `"say \"hi\""` {
		t.Errorf("quotes: %s", got)
	}
	for _, bad := range [][]byte{nil, {1, 0}, {1, 0, 0, 0, 'x'}, {0xff, 0xff, 0xff, 0xff, 'x', 0}} {
		if _, _, err := parseProcArgs(bad); err == nil {
			t.Errorf("%v: no error", bad)
		}
	}
	// The environment after the arguments is never returned.
	if _, args, _ := parseProcArgs(procArgs("/bin/sleep", []string{"sleep"}, "TOKEN=x")); !slices.Equal(args, []string{"sleep"}) {
		t.Errorf("env leaked: %q", args)
	}
}

func TestBundles(t *testing.T) {
	helper := "/Applications/Fabrikam Browser.app/Contents/Frameworks/Fabrikam Framework.framework/Helpers/Fabrikam Helper (GPU).app/Contents/MacOS/Fabrikam Helper (GPU)"
	if got := outermostBundle(helper); got != "/Applications/Fabrikam Browser.app" {
		t.Errorf("outermost = %q", got)
	}
	if got := innermostBundle(helper); got != "/Applications/Fabrikam Browser.app/Contents/Frameworks/Fabrikam Framework.framework/Helpers/Fabrikam Helper (GPU).app" {
		t.Errorf("innermost = %q", got)
	}
	for _, p := range []string{"/usr/local/bin/node", "", "relative/x"} {
		if outermostBundle(p) != "" || innermostBundle(p) != "" {
			t.Errorf("%q is in a bundle", p)
		}
	}
}

func TestMacCritical(t *testing.T) {
	for _, c := range []struct {
		pid       uint32
		name, exe string
		want      bool
	}{
		{1, "launchd", "/sbin/launchd", true},
		{150, "WindowServer", "/System/Library/PrivateFrameworks/SkyLight.framework/Versions/A/Resources/WindowServer", true},
		{160, "loginwindow", "/System/Library/CoreServices/loginwindow.app/Contents/MacOS/loginwindow", true},
		{170, "loginwindow", "", true},
		{180, "loginwindow", "/Users/someone/Downloads/loginwindow", false}, // a look-alike
		{190, "Finder", "/System/Library/CoreServices/Finder.app/Contents/MacOS/Finder", false},
	} {
		if got := macCritical(c.pid, c.name, c.exe); got != c.want {
			t.Errorf("%d %s %s = %v", c.pid, c.name, c.exe, got)
		}
		// Classify, as the plugin and Kill use it, protects them.
		class, _ := Classify(Facts{ID: ID{PID: c.pid}, Name: c.name, Path: c.exe, Critical: macCritical(c.pid, c.name, c.exe)})
		if class.Endable() == c.want {
			t.Errorf("%d %s: class %v", c.pid, c.name, class)
		}
	}
}

func TestElevateArgv(t *testing.T) {
	argv := elevateArgv("Allow?", "/Applications/My App's.app/Contents/MacOS/golauncher",
		HelperRequest{ID: ID{PID: 1234, Created: 5678}, Tree: true}.Args())
	if argv[0] != "/usr/bin/osascript" || argv[1] != "-e" || argv[2] != elevateScript || argv[3] != "Allow?" {
		t.Fatalf("argv = %q", argv)
	}
	want := `'/Applications/My App'\''s.app/Contents/MacOS/golauncher' 'kill' '--pid' '1234' '--created' '5678' '--tree'; echo $?`
	if argv[4] != want {
		t.Errorf("command = %s\nwant      %s", argv[4], want)
	}
	if len(argv) != 5 {
		t.Errorf("extra arguments: %q", argv)
	}
	if got := elevateArgv("", "/x", nil)[3]; got == "" {
		t.Error("no default prompt")
	}
	if shellQuote("") != "''" || shellQuote("a b") != "'a b'" {
		t.Error("shellQuote")
	}
}

func TestElevateResult(t *testing.T) {
	if code, err := elevateResult("3\n", "", nil); code != ExitGone || err != nil {
		t.Errorf("exit code: %d %v", code, err)
	}
	if code, err := elevateResult("some output\n0\n", "", nil); code != 0 || err != nil {
		t.Errorf("last line: %d %v", code, err)
	}
	if _, err := elevateResult("", "0:47: execution error: User canceled. (-128)\n", errors.New("exit status 1")); !errors.Is(err, ErrCancelled) {
		t.Errorf("cancel: %v", err)
	}
	if _, err := elevateResult("", "boom", errors.New("exit status 1")); err == nil || errors.Is(err, ErrCancelled) {
		t.Errorf("failure: %v", err)
	}
	if _, err := elevateResult("", "", nil); err == nil {
		t.Error("no output: no error")
	}
	if _, err := elevateResult("x\n", "", nil); err == nil {
		t.Error("not a number: no error")
	}
}

func TestWithPrompt(t *testing.T) {
	if promptOf(context.Background()) != "" {
		t.Error("prompt without WithPrompt")
	}
	if got := promptOf(WithPrompt(context.Background(), "End Contoso?")); got != "End Contoso?" {
		t.Errorf("prompt = %q", got)
	}
}

func TestRatesNoCPU(t *testing.T) {
	t0 := time.Unix(1000, 0)
	prev := &Snapshot{Time: t0, Stamp: 500 * sec, CPUs: 1, Procs: []Proc{
		{ID: ID{1, 100}, NoCPU: true},   // not read yet
		{ID: ID{2, 100}, CPU: 10 * sec}, // read
	}}
	cur := &Snapshot{Time: t0.Add(time.Second), Stamp: 501 * sec, CPUs: 1, Procs: []Proc{
		{ID: ID{1, 100}, CPU: 3600 * sec},               // read now: a jump, not a use
		{ID: ID{2, 100}, NoCPU: true},                   // no longer readable
		{ID: ID{3, 500*sec + 1}, CPU: sec, NoCPU: true}, // new, not read
	}}
	for id, u := range Rates(prev, cur, CPUPerCore) {
		if u.CPU != -1 || u.IO != -1 {
			t.Errorf("%v: %+v", id, u)
		}
	}
}

func TestUnixFiletime(t *testing.T) {
	// 2026-10-16 00:00:00 UTC in microseconds, and as a FILETIME.
	usec := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC).UnixMicro()
	ft := unixFiletime(usec)
	if back := time.Unix(0, (ft-116444736000000000)*100).UTC(); !back.Equal(time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("round trip: %v", back)
	}
}
