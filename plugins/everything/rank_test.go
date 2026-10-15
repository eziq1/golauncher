package everything

import (
	"context"
	"encoding/json/jsontext"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/winapi/everything"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// testEnv has Documents redirected to D:\Docs and Downloads given in the
// \\?\ form.
var testEnv = rulesEnv{
	Home:        `C:\Users\demo`,
	SystemRoot:  `C:\WINDOWS`,
	ProgramData: `C:\ProgramData`,
	UserFolders: []string{`C:\Users\demo\Desktop`, `D:\Docs`, `C:\Users\demo\OneDrive`, `\\?\C:\Users\demo\Downloads\`},
}

func TestTier(t *testing.T) {
	r := newRules(testEnv).withExtra([]string{`D:\VMs`, "vendor", ` "build/out" `, `E:`, `a|b<c>`, "", `\\nas\backup\`})
	type want struct {
		tier  tier
		known bool
	}
	sys, prog, normal, user, known := want{tierSystem, false}, want{tierProgram, false}, want{tierNormal, false}, want{tierUser, false}, want{tierUser, true}
	for _, c := range []struct {
		path string
		want want
	}{
		// System and caches.
		{`C:\Windows\notepad.exe`, sys},
		{`c:\windows\SYSTEM32`, sys},
		{`C:\Windows`, sys},
		{`\\?\C:\Windows\x`, sys},
		{`C:/Windows/x`, sys},
		{`C:\ProgramData\Microsoft\x`, sys},
		{`D:\$RECYCLE.BIN\x`, sys},
		{`D:\System Volume Information\x`, sys},
		{`D:\proj\node_modules\left-pad\index.js`, sys},
		{`D:\proj\NODE_MODULES`, sys},
		{`C:\Users\demo\Documents\proj\.git\HEAD`, sys},
		{`C:\Users\demo\Sources\x\__pycache__\a.pyc`, sys},
		{`C:\Users\demo\.cache\x`, sys},
		{`C:\Users\demo\AppData\Local\Google\Chrome\User Data\Default\Cache\x`, sys},
		{`C:\Users\demo\AppData\Roaming\Code\Service Worker\CacheStorage\x`, sys},
		{`C:\Python312\Lib\site-packages\x`, sys},
		{`C:\Users\demo\go\pkg\mod\golang.org\x`, sys},
		{`C:\Users\demo\.nuget\packages\x`, sys},
		{`C:\Users\demo\AppData\Local\Temp\x`, sys},
		{`C:\Users\other\AppData\Local\Temp\x`, sys},
		{`E:\Containers\Layers\a\Files\Windows\WinSxS\x`, sys},
		{`D:\WinSxS\x`, sys},
		{`C:\Users\demo\OneDrive\proj\node_modules\x`, sys}, // a cache inside a known folder
		{`\\server\share\node_modules\x`, sys},
		// Programs and app data.
		{`C:\Program Files\App\a.exe`, prog},
		{`D:\Program Files (x86)\App`, prog},
		{`C:\Users\demo\AppData\Roaming\x`, prog},
		{`C:\Users\demo\AppData\Local\Programs\x`, prog},
		{`C:\Users\demo\scoop\apps\git\current\git.exe`, prog},
		{`C:\Users\demo\scoop\persist\vscode\data\x`, prog},
		{`C:\Users\demo\.vscode\extensions\x`, prog},
		// The user's own files.
		{`C:\Users\demo\Desktop\a.txt`, known},
		{`c:\users\DEMO\desktop\a.txt`, known},
		{`\\?\C:\Users\demo\Desktop\a.txt`, known},
		{`D:\Docs\a.txt`, known}, // redirected Documents
		{`d:\docs`, known},
		{`C:\Users\demo\Downloads\setup.msi`, known},
		{`C:\Users\demo\OneDrive\Reports\r.docx`, known},
		{`D:\Docs2\a.txt`, user}, // another drive, not the known folder
		{`D:\go\pkg\modules\x`, user},
		{`D:\mycache\x`, user},
		{`C:\Users\demo\Sources\x.go`, user},
		{`C:\Users\demo`, user},
		// Anything else.
		{`C:\Users\other\Documents\x`, normal},
		{`C:\Tools\x.exe`, normal},
		{`C:\WindowsApps\x`, normal},
		{`C:\`, normal},
		{`\\?\UNC\server\share\x`, normal},
		// extraLowPriorityPaths.
		{`D:\VMs\disk.vhdx`, sys},
		{`d:\vms`, sys},
		{`D:\VMs2\x`, user},
		{`C:\Users\demo\Sources\x\vendor\y`, sys},
		{`C:\x\build\out\y`, sys},
		{`C:\x\build\y`, normal},
		{`E:\anything`, sys},
		{`C:\x\abc\y`, sys},
		{`\\NAS\Backup\x`, sys},
	} {
		tr, kn := r.tier(c.path)
		if got := (want{tr, kn}); got != c.want {
			t.Errorf("tier(%q) = %v, want %v", c.path, got, c.want)
		}
	}

	if r := newRules(rulesEnv{}); r.sysDrive != "c:" || len(r.sysPrefixes) != 0 || len(r.userDirs) != 0 {
		t.Errorf("empty env: %+v", r)
	}
	if r := newRules(rulesEnv{SystemRoot: `D:\Win`}); r.sysDrive != "d:" {
		t.Errorf("system drive = %q", r.sysDrive)
	}
	if base := newRules(testEnv); base.withExtra(nil) != base {
		t.Error("withExtra(nil) copied the rules")
	}
}

func TestQueryText(t *testing.T) {
	r := newRules(rulesEnv{SystemRoot: `C:\Windows`}).withExtra([]string{`a"b|<c>`, `D:\VMs`})
	ex := r.exclusions()
	if !strings.HasPrefix(ex, ` !"c:\windows\" !"d:\vms\" !"\$recycle.bin\" !"\system volume information\" `) ||
		!strings.HasSuffix(ex, ` !"\ebwebview\" !"\abc\"`) {
		t.Errorf("exclusions = %s", ex)
	}
	if n := strings.Count(ex, ` !"`); n != len(systemSegments)+3 {
		t.Errorf("%d exclusions", n)
	}
	in := r.inclusion()
	if !strings.HasPrefix(in, ` <"c:\windows\"|"d:\vms\"|"\$recycle.bin\"|`) || !strings.HasSuffix(in, `|"\abc\">`) || strings.Count(in, `"`)%2 != 0 {
		t.Errorf("inclusion = %s", in)
	}
	for _, c := range []struct{ text, suffix, want string }{
		{"report", "", "report"},
		{"report", ` !"x"`, `report !"x"`},
		{`"my report`, ` !"x"`, `"my report" !"x"`},
		{`"my report"`, ` !"x"`, `"my report" !"x"`},
		{`jpg|png`, ` !"x"`, `<jpg|png> !"x"`},
		{`a "b|c`, ` !"x"`, `<a "b|c"> !"x"`},
	} {
		if got := searchText(c.text, c.suffix); got != c.want {
			t.Errorf("searchText(%q, %q) = %s, want %s", c.text, c.suffix, got, c.want)
		}
	}
}

func TestNameQuality(t *testing.T) {
	for _, c := range []struct {
		name, text string
		want       float64
	}{
		{"report.pdf", "report", exactQuality},
		{"Report", "REPORT", exactQuality},
		{"annual report.pdf", "annual report", exactQuality},
		{"reporting.dll", "report", prefixQuality},
		{"annual report.pdf", "report", wordQuality},
		{"q3_report.xlsx", "report", wordQuality},
		{"2026-report.xlsx", "report", wordQuality},
		{"bugreport.txt", "report", containQuality},
		{"项目计划书.docx", "计划", containQuality},
		{"项目计划书.docx", "项目计划书", exactQuality},
		{"index.js", "report", 0},
		{"Quarterly Report Q3.xlsx", "quarterly report", (prefixQuality + wordQuality) / 2},
		{"x.txt", `C:\foo\ *.txt`, 0},
	} {
		if got := nameQuality(c.name, plainTerms(c.text)); got != c.want {
			t.Errorf("nameQuality(%q, %q) = %v, want %v", c.name, c.text, got, c.want)
		}
	}
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		mod  time.Time
		want float64
	}{
		{time.Time{}, 0},
		{now, recentWeight},
		{now.Add(time.Hour), recentWeight},
		{now.Add(-15 * 24 * time.Hour), recentWeight / 2},
		{now.Add(-31 * 24 * time.Hour), 0},
	} {
		if got := recency(c.mod, now); got != c.want {
			t.Errorf("recency(%v) = %v, want %v", c.mod, got, c.want)
		}
	}
}

func TestRankOrder(t *testing.T) {
	r := newRules(testEnv)
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	res := []everything.Result{
		{Name: "report.pdf", Path: `C:\Windows\WinSxS\report.pdf`, Modified: now},           // system, exact, recent
		{Name: "reporting.dll", Path: `C:\Program Files\App\reporting.dll`},                 // program
		{Name: "bugreport.txt", Path: `D:\Docs\bugreport.txt`},                              // user, weak match
		{Name: "report", Path: `C:\Tools\report`, IsFolder: true},                           // normal folder
		{Name: "report", Path: `C:\Tools\x\report`},                                         // normal file
		{Name: "my report.txt", Path: `C:\Users\demo\Sources\my report.txt`, Modified: now}, // user, not known
	}
	var got []string
	for _, x := range rank(r, res, "report", now, false) {
		got = append(got, x.Path)
	}
	want := []string{
		`C:\Users\demo\Sources\my report.txt`, // 6 + .45 + .5
		`D:\Docs\bugreport.txt`,               // 6 + .225 + .3
		`C:\Tools\x\report`,                   // 4 + .9
		`C:\Tools\report`,                     // 4 + .9 - .05
		`C:\Program Files\App\reporting.dll`,
		`C:\Windows\WinSxS\report.pdf`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("order:\n%s", strings.Join(got, "\n"))
	}
	if n := len(rank(r, res, "report", now, true)); n != 5 {
		t.Errorf("hideSystem kept %d", n)
	}
}

// counting counts the queries sent to a backend.
type counting struct {
	Backend
	mu sync.Mutex
	qs []everything.Query
}

func (c *counting) Search(ctx context.Context, q everything.Query) ([]everything.Result, error) {
	c.mu.Lock()
	c.qs = append(c.qs, q)
	c.mu.Unlock()
	return c.Backend.Search(ctx, q)
}

func (c *counting) take() []everything.Query {
	c.mu.Lock()
	defer c.mu.Unlock()
	qs := c.qs
	c.qs = nil
	return qs
}

func paths(its []plugin.Item) []string {
	var out []string
	for _, it := range its {
		out = append(out, it.ID)
	}
	return out
}

func TestFixtureRanking(t *testing.T) {
	b := &counting{Backend: FixtureBackend("testdata/ranking.json")}
	p, h := start(t, b)

	got := paths(items(view(t, p, "report")))
	want := []string{
		`C:\Users\demo\Sources\app\report.go`,
		`C:\Users\demo\OneDrive\Reports\annual report 2026.docx`,
		`C:\Tools\report.exe`,
		`C:\Program Files\App\reporting.dll`,
		`C:\Users\demo\AppData\Local\Temp\report.tmp`,
		`C:\Users\demo\Sources\app\node_modules\report`,
		`C:\Python312\Lib\site-packages\report`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("report:\n%s", strings.Join(got, "\n"))
	}
	// A found 4 (< maxResults), so B filled up with the system tier.
	if qs := b.take(); len(qs) != 2 || !strings.Contains(qs[0].Text, `!"\node_modules\"`) || !strings.Contains(qs[1].Text, `|"\node_modules\"|`) ||
		qs[0].Max != poolMax || qs[1].Max != defaultMax-4 {
		t.Errorf("queries = %+v", qs)
	}

	// Typing on: predicted narrow, one plain query has every match.
	got = paths(items(view(t, p, "report.")))
	if qs := b.take(); len(qs) != 1 || qs[0].Text != "report." {
		t.Errorf("queries = %+v", qs)
	}
	if len(got) != 3 || got[0] != `C:\Users\demo\Sources\app\report.go` || got[2] != `C:\Users\demo\AppData\Local\Temp\report.tmp` {
		t.Errorf("report.:\n%s", strings.Join(got, "\n"))
	}

	got = paths(items(view(t, p, "notepad")))
	want = []string{
		`C:\Users\demo\Desktop\Notepad tips.md`,
		`D:\Docs\my notepad notes.txt`,
		`C:\Program Files\Notepad++\notepad++.exe`,
		`C:\Windows\System32\notepad.exe`,
		`C:\ProgramData\Microsoft\Windows\Containers\Layers\8cfc\Files\Windows\notepad.exe`,
		`C:\Windows\WinSxS\amd64_microsoft-windows-notepad`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("notepad:\n%s", strings.Join(got, "\n"))
	}
	b.take()

	// hideSystemFiles: no query B and no system files.
	h.Prefs.Set(prefHideSystem, jsontext.Value("true"))
	got = paths(items(view(t, p, "notepad")))
	if !slices.Equal(got, want[:3]) {
		t.Errorf("hidden:\n%s", strings.Join(got, "\n"))
	}
	if qs := b.take(); len(qs) != 1 {
		t.Errorf("queries = %+v", qs)
	}
	// Also when one plain query is used (short text).
	if got := paths(items(view(t, p, "no"))); len(got) != 3 || got[0] != want[0] {
		t.Errorf("short hidden:\n%s", strings.Join(got, "\n"))
	}
	h.Prefs.Set(prefHideSystem, jsontext.Value("false"))

	// extraLowPriorityPaths moves D:\Docs below Program Files.
	h.Prefs.Set(prefExtraLowPriority, jsontext.Value(`["D:\\Docs"]`))
	got = paths(items(view(t, p, "notepad")))
	if i := slices.Index(got, `D:\Docs\my notepad notes.txt`); i != 4 { // system tier, after notepad++
		t.Errorf("extra low priority:\n%s", strings.Join(got, "\n"))
	}
	h.Prefs.Set(prefExtraLowPriority, jsontext.Value(`[]`))

	// Root search: user and normal tiers only.
	h.Prefs.Set(prefShowInRoot, jsontext.Value("true"))
	its, err := p.RootItems(t.Context(), plugin.Query{Text: "report"})
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(its); !slices.Equal(got, []string{
		`C:\Users\demo\Sources\app\report.go`,
		`C:\Users\demo\OneDrive\Reports\annual report 2026.docx`,
		`C:\Tools\report.exe`,
	}) {
		t.Errorf("root:\n%s", strings.Join(got, "\n"))
	}
}

func TestFixtureSyntax(t *testing.T) {
	b := FixtureBackend("testdata/ranking.json")
	for _, c := range []struct {
		text string
		want int
	}{
		{"notepad", 6},
		{`notepad !"c:\windows\"`, 4},
		{`notepad !"c:\windows\" !"\containers\layers\"`, 3},
		{`notepad <"c:\windows\"|"\containers\layers\">`, 3},
		{`<notepad|report> !"\node_modules\" !"c:\windows\" !"c:\programdata\"`, 9},
		{`"my notepad"`, 1},
		{`!notepad`, 8},
	} {
		res, err := b.Search(t.Context(), everything.Query{Text: c.text})
		if err != nil || len(res) != c.want {
			t.Errorf("%s: %d results, want %d (%v)", c.text, len(res), c.want, err)
		}
	}
}

func TestMerge(t *testing.T) {
	a := []everything.Result{{Path: `C:\a`}, {Path: `C:\b`}}
	b := []everything.Result{{Path: `c:\A`}, {Path: `C:\c`}, {Path: `C:\c`}}
	got := merge(a, b)
	if len(got) != 3 || got[2].Path != `C:\c` || len(a) != 2 {
		t.Errorf("merge = %+v", got)
	}
	if got := merge(a, nil); len(got) != 2 {
		t.Errorf("merge nil = %+v", got)
	}
}

func BenchmarkRank(b *testing.B) {
	r := newRules(testEnv)
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	res := make([]everything.Result, poolMax)
	for i := range res {
		res[i] = everything.Result{Name: "quarterly report.pdf", Path: `C:\Users\demo\Documents\Projects\2026\Finance\quarterly report.pdf`, Modified: now}
	}
	for b.Loop() {
		rank(r, res, "report", now, false)
	}
}
