package process

import (
	"errors"
	"math"
	"slices"
	"testing"
	"time"
)

// All names and paths here are invented (Contoso, Fabrikam, Northwind).

const sec = int64(1e7) // one second in 100 ns

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestRatesFirstSnapshotUnknown(t *testing.T) {
	cur := &Snapshot{Time: time.Unix(100, 0), CPUs: 4, Procs: []Proc{{ID: ID{10, 1}, CPU: sec}}}
	for id, u := range Rates(nil, cur, CPUTotal) {
		if u.CPU != -1 || u.IO != -1 {
			t.Errorf("%v: %+v", id, u)
		}
	}
}

func TestRates(t *testing.T) {
	t0 := time.Unix(1000, 0)
	prev := &Snapshot{Time: t0, Stamp: 500 * sec, CPUs: 4, Procs: []Proc{
		{ID: ID{10, 100}, CPU: 0, IO: 1000},            // steady
		{ID: ID{11, 100}, CPU: 5 * sec, IO: 5000},      // counters go back
		{ID: ID{12, 100}, CPU: 0},                      // restarted: old instance
		{ID: ID{13, 100}, CPU: 0},                      // very busy
		{ID: ID{14, 100}, CPU: 2 * sec, IO: 100 << 20}, // exits
	}}
	cur := &Snapshot{Time: t0.Add(time.Second), Stamp: 501 * sec, CPUs: 4, Procs: []Proc{
		{ID: ID{10, 100}, CPU: sec, IO: 3000},
		{ID: ID{11, 100}, CPU: 4 * sec, IO: 4000},
		{ID: ID{12, 500*sec + 10}, CPU: sec / 2, IO: 800}, // new instance, started after prev
		{ID: ID{13, 100}, CPU: 8 * sec},
		{ID: ID{20, 400 * sec}, CPU: sec}, // not in prev, but started before it: unknown
	}}
	total := Rates(prev, cur, CPUTotal)
	per := Rates(prev, cur, CPUPerCore)

	if u := total[ID{10, 100}]; !near(u.CPU, 25) || !near(u.IO, 2000) {
		t.Errorf("total steady = %+v", u)
	}
	if u := per[ID{10, 100}]; !near(u.CPU, 100) || !near(u.IO, 2000) {
		t.Errorf("per-core steady = %+v", u)
	}
	if u := total[ID{11, 100}]; u.CPU != 0 || u.IO != 0 {
		t.Errorf("backwards = %+v", u)
	}
	if u := per[ID{12, 500*sec + 10}]; !near(u.CPU, 50) || !near(u.IO, 800) {
		t.Errorf("restarted = %+v", u)
	}
	// 8 s of CPU in 1 s on 4 processors: clamped to the limit.
	if u := total[ID{13, 100}]; !near(u.CPU, 100) {
		t.Errorf("total busy = %+v", u)
	}
	if u := per[ID{13, 100}]; !near(u.CPU, 400) {
		t.Errorf("per-core busy = %+v", u)
	}
	if u := total[ID{20, 400 * sec}]; u.CPU != -1 || u.IO != -1 {
		t.Errorf("unknown = %+v", u)
	}
	if _, ok := total[ID{14, 100}]; ok || len(total) != len(cur.Procs) {
		t.Errorf("rates of %d processes: %v", len(total), total)
	}

	// More processors halve the total share.
	cur.CPUs = 8
	if u := Rates(prev, cur, CPUTotal)[ID{10, 100}]; !near(u.CPU, 12.5) {
		t.Errorf("8 CPUs = %+v", u)
	}
	// No time elapsed: unknown.
	same := *cur
	same.Time = prev.Time
	if u := Rates(prev, &same, CPUTotal)[ID{10, 100}]; u.CPU != -1 {
		t.Errorf("no time = %+v", u)
	}
	// Without the previous stamp a new process is unknown.
	prev.Stamp = 0
	if u := Rates(prev, cur, CPUTotal)[ID{12, 500*sec + 10}]; u.CPU != -1 {
		t.Errorf("no stamp = %+v", u)
	}
}

func TestSystemCPU(t *testing.T) {
	prev := &Snapshot{SysIdle: 1000, SysBusy: 1000}
	if got := SystemCPU(nil, prev); got != -1 {
		t.Errorf("first = %v", got)
	}
	if got := SystemCPU(prev, &Snapshot{SysIdle: 1300, SysBusy: 1100}); !near(got, 25) {
		t.Errorf("25%% = %v", got)
	}
	if got := SystemCPU(prev, &Snapshot{SysIdle: 900, SysBusy: 1100}); got != -1 {
		t.Errorf("backwards = %v", got)
	}
	if got := SystemCPU(prev, prev); got != -1 {
		t.Errorf("no time = %v", got)
	}
}

func TestMemoryUsed(t *testing.T) {
	if got := (&Snapshot{MemTotal: 400, MemAvail: 100}).MemoryUsed(); !near(got, 75) {
		t.Errorf("used = %v", got)
	}
	if got := (&Snapshot{}).MemoryUsed(); got != 0 {
		t.Errorf("unknown = %v", got)
	}
	if got := (&Snapshot{MemTotal: 100, MemAvail: 200}).MemoryUsed(); got != 0 {
		t.Errorf("bad = %v", got)
	}
}

// groupFixture is a small process tree:
//
//	50 explorer
//	└ 100 fabrikam (main)
//	  ├ 101 fabrikam
//	  │ └ 102 fabrikam
//	  └ 103 node
//	200 fabrikam whose parent 300 exited; 300 is now a newer fabrikam
//	400, 401 Contoso Notes (packaged), 402 its helper
func groupFixture() ([]Proc, map[ID]string, map[ID]string) {
	const fab = `C:\Program Files\Fabrikam\fabrikam.exe`
	procs := []Proc{
		{ID: ID{50, 1}, Name: "explorer.exe"},
		{ID: ID{100, 10}, Parent: 50, Name: "fabrikam.exe"},
		{ID: ID{101, 20}, Parent: 100, Name: "fabrikam.exe"},
		{ID: ID{102, 30}, Parent: 101, Name: "fabrikam.exe"},
		{ID: ID{103, 25}, Parent: 100, Name: "node.exe"},
		{ID: ID{200, 40}, Parent: 300, Name: "fabrikam.exe"},
		{ID: ID{300, 50}, Name: "fabrikam.exe"},
		{ID: ID{400, 5}, Parent: 50, Name: "notes.exe"},
		{ID: ID{401, 3}, Parent: 50, Name: "notes.exe"},
		{ID: ID{402, 6}, Parent: 400, Name: "notes-helper.exe"},
	}
	paths := map[ID]string{
		{50, 1}:   `C:\Windows\explorer.exe`,
		{100, 10}: fab, {101, 20}: fab, {102, 30}: fab,
		{103, 25}: `C:\Program Files\nodejs\node.exe`,
		{200, 40}: fab, {300, 50}: fab,
		{400, 5}: `C:\Program Files\WindowsApps\Contoso.Notes\notes.exe`,
		{401, 3}: `C:\Program Files\WindowsApps\Contoso.Notes\notes.exe`,
		{402, 6}: `C:\Program Files\WindowsApps\Contoso.Notes\notes-helper.exe`,
	}
	pkgs := map[ID]string{
		{400, 5}: "Contoso.Notes_1.0.0.0_x64__abc", {401, 3}: "Contoso.Notes_1.0.0.0_x64__abc", {402, 6}: "Contoso.Notes_1.0.0.0_x64__abc",
	}
	return procs, paths, pkgs
}

func TestGroupRoots(t *testing.T) {
	procs, paths, pkgs := groupFixture()
	roots := GroupRoots(procs, func(id ID) string { return paths[id] }, func(id ID) string { return pkgs[id] })
	want := map[ID]ID{
		{50, 1}:   {50, 1},
		{100, 10}: {100, 10}, // its parent has another path
		{101, 20}: {100, 10},
		{102, 30}: {100, 10},
		{103, 25}: {103, 25}, // another path: its own group
		{200, 40}: {200, 40}, // PID 300 is newer than 200: not its parent
		{300, 50}: {300, 50},
		{400, 5}:  {401, 3}, // packaged: under the oldest of the package
		{401, 3}:  {401, 3},
		{402, 6}:  {401, 3},
	}
	for id, w := range want {
		if roots[id] != w {
			t.Errorf("root of %v = %v, want %v", id, roots[id], w)
		}
	}
	// A process whose path is unknown is a group of its own.
	delete(paths, ID{101, 20})
	roots = GroupRoots(procs, func(id ID) string { return paths[id] }, func(ID) string { return "" })
	if roots[ID{101, 20}] != (ID{101, 20}) || roots[ID{102, 30}] != (ID{102, 30}) {
		t.Errorf("unknown path: %v %v", roots[ID{101, 20}], roots[ID{102, 30}])
	}
}

func TestParentOf(t *testing.T) {
	procs, _, _ := groupFixture()
	if p, ok := ParentOf(procs, procs[2]); !ok || p.ID != (ID{100, 10}) {
		t.Errorf("parent of 101 = %v %v", p.ID, ok)
	}
	if _, ok := ParentOf(procs, Proc{ID: ID{200, 40}, Parent: 300}); ok {
		t.Error("a newer process is taken for the parent")
	}
}

func TestTree(t *testing.T) {
	procs, _, _ := groupFixture()
	// 104 claims 100 as parent but is older than it: the PID was reused.
	procs = append(procs, Proc{ID: ID{104, 5}, Parent: 100})
	got := Tree(procs, ID{100, 10})
	want := []ID{{100, 10}, {101, 20}, {103, 25}, {102, 30}}
	if !slices.Equal(got, want) {
		t.Errorf("tree = %v, want %v", got, want)
	}
	if got := Tree(procs, ID{100, 11}); got != nil {
		t.Errorf("another instance = %v", got)
	}
	if got := Tree(procs, ID{999, 1}); got != nil {
		t.Errorf("missing = %v", got)
	}
	if got := Tree(procs, ID{102, 30}); !slices.Equal(got, []ID{{102, 30}}) {
		t.Errorf("leaf = %v", got)
	}
}

func TestSnapshotFind(t *testing.T) {
	s := &Snapshot{Procs: []Proc{{ID: ID{7, 1}, Name: "contoso.exe"}}}
	if p, ok := s.Find(ID{7, 1}); !ok || p.Name != "contoso.exe" {
		t.Errorf("find = %+v %v", p, ok)
	}
	if _, ok := s.Find(ID{7, 2}); ok {
		t.Error("found another instance")
	}
	if got := (ID{7, 1}).String(); got != "7:1" {
		t.Errorf("String = %q", got)
	}
}

func TestClassify(t *testing.T) {
	const root = `C:\Windows`
	f := func(pid uint32, name, path string) Facts {
		return Facts{ID: ID{pid, 1}, Name: name, Path: path, Self: 9000, SystemRoot: root}
	}
	cases := []struct {
		name  string
		facts Facts
		class Class
		note  string
	}{
		{"real lsass", f(700, "lsass.exe", `C:\Windows\System32\lsass.exe`), ClassCritical, ""},
		{"real csrss, other case", f(701, "CSRSS.EXE", `c:\windows\system32\csrss.exe`), ClassCritical, ""},
		{"lsass elsewhere", f(702, "lsass.exe", `C:\Users\Contoso\Downloads\lsass.exe`), ClassNormal, ""},
		{"lsass, path unknown", f(703, "lsass.exe", ""), ClassCritical, ""},
		{"idle", f(0, "System Idle Process", ""), ClassKernel, ""},
		{"system", f(4, "System", ""), ClassKernel, ""},
		{"registry", f(120, "Registry", ""), ClassKernel, ""},
		{"memory compression", f(121, "Memory Compression", ""), ClassKernel, ""},
		{"self", f(9000, "golauncher.exe", `C:\Program Files\golauncher\golauncher.exe`), ClassSelf, ""},
		{"critical flag", Facts{ID: ID{800, 1}, Name: "northwind.exe", Path: `C:\Program Files\Northwind\northwind.exe`, Critical: true, SystemRoot: root}, ClassCritical, ""},
		{"shell explorer", Facts{ID: ID{801, 1}, Name: "explorer.exe", Path: `C:\Windows\explorer.exe`, Shell: true, SystemRoot: root}, ClassShell, ""},
		{"other explorer", f(802, "explorer.exe", `C:\Windows\explorer.exe`), ClassNormal, ""},
		{"svchost", f(803, "svchost.exe", `C:\Windows\System32\svchost.exe`), ClassComponent, "svchost"},
		{"svchost elsewhere", f(804, "svchost.exe", `C:\Users\Contoso\svchost.exe`), ClassNormal, ""},
		{"dwm", f(805, "dwm.exe", `C:\Windows\System32\dwm.exe`), ClassComponent, "dwm"},
		{"normal", f(806, "fabrikam.exe", `C:\Program Files\Fabrikam\fabrikam.exe`), ClassNormal, ""},
	}
	for _, c := range cases {
		class, note := Classify(c.facts)
		if class != c.class || note != c.note {
			t.Errorf("%s: %v %q, want %v %q", c.name, class, note, c.class, c.note)
		}
	}
	for c, endable := range map[Class]bool{ClassNormal: true, ClassComponent: true, ClassKernel: false, ClassCritical: false, ClassSelf: false, ClassShell: false} {
		if c.Endable() != endable {
			t.Errorf("%v.Endable() = %v", c, !endable)
		}
	}
}

func TestRedactCommandLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{`fabrikam.exe --password=hunter2 -v`, `fabrikam.exe --password=••• -v`},
		{`fabrikam.exe --password hunter2 -v`, `fabrikam.exe --password ••• -v`},
		{`contoso.exe --api-key:abc123`, `contoso.exe --api-key:•••`},
		{`contoso.exe /password:abc`, `contoso.exe /password:•••`},
		{`northwind.exe -u admin -p s3cret`, `northwind.exe -u admin -p •••`},
		{`northwind.exe "--token=a b" next`, `northwind.exe "--token=•••" next`},
		{`northwind.exe --db-password=x`, `northwind.exe --db-password=•••`},
		{`git.exe clone https://user:pass@example.com/contoso/repo`, `git.exe clone https://•••@example.com/contoso/repo`},
		{`fabrikam.exe --secret "two words" --quiet`, `fabrikam.exe --secret ••• --quiet`},
		{`fabrikam.exe -p -v`, `fabrikam.exe -p -v`},
		{`fabrikam.exe --password`, `fabrikam.exe --password`},
		// Kept as typed.
		{`fabrikam.exe -v --keyboard us C:\Contoso\file.txt /home/fabrikam/notes.txt`, `fabrikam.exe -v --keyboard us C:\Contoso\file.txt /home/fabrikam/notes.txt`},
		{`"C:\Program Files\Fabrikam\fabrikam.exe" --type=renderer`, `"C:\Program Files\Fabrikam\fabrikam.exe" --type=renderer`},
		{``, ``},
	}
	for _, c := range cases {
		if got := RedactCommandLine(c.in); got != c.want {
			t.Errorf("RedactCommandLine(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestParseHelperArgs(t *testing.T) {
	r, err := ParseHelperArgs([]string{"kill", "--pid", "1234", "--created", "133700000000000000"})
	if err != nil || r != (HelperRequest{ID: ID{1234, 133700000000000000}}) {
		t.Errorf("valid = %+v %v", r, err)
	}
	r, err = ParseHelperArgs([]string{"kill", "--tree", "--created", "5", "--pid", "5"})
	if err != nil || r != (HelperRequest{ID: ID{5, 5}, Tree: true}) {
		t.Errorf("tree = %+v %v", r, err)
	}
	// Args round-trips.
	want := HelperRequest{ID: ID{4321, 99}, Tree: true}
	if r, err := ParseHelperArgs(want.Args()); err != nil || r != want {
		t.Errorf("round trip = %+v %v", r, err)
	}
	bad := [][]string{
		nil,
		{},
		{"mcp"},
		{"kill"},
		{"kill", "--pid", "1234"},
		{"kill", "--created", "1"},
		{"kill", "--pid", "0", "--created", "1"},
		{"kill", "--pid", "4", "--created", "1"},
		{"kill", "--pid", "12a", "--created", "1"},
		{"kill", "--pid", "+5", "--created", "1"},
		{"kill", "--pid", "-1", "--created", "1"},
		{"kill", "--pid", " 5", "--created", "1"},
		{"kill", "--pid", "0x10", "--created", "1"},
		{"kill", "--pid", "", "--created", "1"},
		{"kill", "--pid", "1234", "--created", "0"},
		{"kill", "--pid", "1234", "--created", "-5"},
		{"kill", "--pid", "1234", "--pid", "1235", "--created", "1"},
		{"kill", "--pid", "1234", "--created", "1", "--tree", "--tree"},
		{"kill", "--pid", "1234", "--created", "1", "--force"},
		{"kill", "--pid", "1234", "--created", "1", "extra"},
		{"kill", "--pid", "4294967296", "--created", "1"},
		{"kill", "--pid", "1234", "--created", "9223372036854775808"},
		{"kill", "--pid", "1234", "--created", "99999999999999999999"},
		{"kill", "--pid", "1234", "--created", "123456789012345678901"},
		{"kill", "--pid", "1234", "--created"},
		{"KILL", "--pid", "1234", "--created", "1"},
	}
	for _, args := range bad {
		if r, err := ParseHelperArgs(args); err == nil {
			t.Errorf("%q accepted: %+v", args, r)
		}
	}
}

func TestExitCodes(t *testing.T) {
	for _, err := range []error{nil, ErrGone, ErrProtected, ErrNeedsAdmin} {
		code := ExitCode(err)
		if back := ExitError(code); back != err && !errors.Is(back, err) {
			t.Errorf("%v -> %d -> %v", err, code, back)
		}
	}
	if code := ExitCode(errors.New("boom")); code != ExitFailed {
		t.Errorf("other error = %d", code)
	}
	if code := ExitCode(errors.Join(ErrGone)); code != ExitGone {
		t.Errorf("wrapped = %d", code)
	}
	for _, code := range []int{ExitFailed, ExitUsage, ExitPartial, 42} {
		err := ExitError(code)
		if err == nil || errors.Is(err, ErrGone) || errors.Is(err, ErrProtected) || errors.Is(err, ErrNeedsAdmin) {
			t.Errorf("code %d = %v", code, err)
		}
	}
}

func TestKillReportAdd(t *testing.T) {
	var r KillReport
	r.Add(ID{1, 1}, nil)
	r.Add(ID{2, 1}, ErrGone)
	r.Add(ID{3, 1}, ErrNeedsAdmin)
	r.Add(ID{4, 1}, ErrProtected)
	r.Add(ID{5, 1}, errors.New("boom"))
	if len(r.Ended) != 1 || len(r.Gone) != 1 || len(r.NeedsAdmin) != 1 || len(r.Protected) != 1 || len(r.Failed) != 1 {
		t.Errorf("report = %+v", r)
	}
}

func TestHelperMain(t *testing.T) {
	valid := []string{"kill", "--pid", "1234", "--created", "77"}
	tree := append(slices.Clone(valid), "--tree")
	var killed []ID
	kill := func(err error) func(ID) error {
		return func(id ID) error {
			killed = append(killed, id)
			return err
		}
	}
	report := func(r KillReport) func(ID) KillReport {
		return func(id ID) KillReport {
			killed = append(killed, id)
			return r
		}
	}
	noTree := report(KillReport{})

	if code := helperMain([]string{"kill", "--pid", "1234"}, kill(nil), noTree); code != ExitUsage || len(killed) != 0 {
		t.Errorf("bad args: %d %v", code, killed)
	}
	if code := helperMain(valid, kill(nil), noTree); code != ExitOK || !slices.Equal(killed, []ID{{1234, 77}}) {
		t.Errorf("ok: %d %v", code, killed)
	}
	for err, want := range map[error]int{ErrProtected: ExitProtected, ErrNeedsAdmin: ExitNeedsAdmin, ErrGone: ExitGone, errors.New("boom"): ExitFailed} {
		if code := helperMain(valid, kill(err), noTree); code != want {
			t.Errorf("%v: %d, want %d", err, code, want)
		}
	}
	a, b, c := ID{1234, 77}, ID{1300, 80}, ID{1301, 81}
	for _, tc := range []struct {
		name string
		r    KillReport
		want int
	}{
		{"all", KillReport{Ended: []ID{a, b}}, ExitOK},
		{"partial", KillReport{Ended: []ID{a, b}, NeedsAdmin: []ID{c}}, ExitPartial},
		{"partial protected", KillReport{Ended: []ID{a}, Protected: []ID{c}}, ExitPartial},
		{"refused", KillReport{Protected: []ID{a}}, ExitProtected},
		{"needs admin", KillReport{NeedsAdmin: []ID{a}}, ExitNeedsAdmin},
		{"gone", KillReport{Gone: []ID{a}}, ExitGone},
		{"failed", KillReport{Failed: []ID{a}}, ExitFailed},
		{"some gone", KillReport{Ended: []ID{a}, Gone: []ID{b}}, ExitOK},
	} {
		killed = nil
		code := helperMain(tree, kill(errors.New("not a tree")), report(tc.r))
		if code != tc.want || !slices.Equal(killed, []ID{a}) {
			t.Errorf("%s: %d %v, want %d", tc.name, code, killed, tc.want)
		}
	}
}
