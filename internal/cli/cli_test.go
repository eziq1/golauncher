package cli

import (
	"bytes"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

const fixtures = "../../testdata/fixtures"

// clockNow is the fixed time of the world clock goldens.
const clockNow = "2026-10-15T10:00:00Z"

// glctl runs the CLI and returns its stdout and exit code.
func glctl(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Main(t.Context(), args, &out, &errOut)
	if code != 0 && errOut.Len() > 0 {
		t.Logf("stderr: %s", errOut.String())
	}
	return out.String(), code
}

var (
	elapsedRe = regexp.MustCompile(`"elapsedMs": \d+`)
	// swatchRe matches color swatches written to the temporary cache.
	swatchRe = regexp.MustCompile(`"image": "[^"]*swatches[\\/]+([0-9a-f]{6}\.png)"`)
)

// golden compares got with testdata/golden/name.json.
func golden(t *testing.T, name, got string) {
	t.Helper()
	got = elapsedRe.ReplaceAllString(got, `"elapsedMs": 0`)
	got = swatchRe.ReplaceAllString(got, `"image": "<cache>/swatches/$1"`)
	path := filepath.Join("testdata", "golden", name+".json")
	if *update {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/cli -update)", err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("%s differs from golden file %s; rerun with -update if the change is intended.\ngot:\n%s", name, path, got)
	}
}

func TestGolden(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"query_empty", []string{"query"}},
		{"query_vsc", []string{"query", "vsc", "--fixtures", fixtures}},
		{"query_pinyin", []string{"query", "wx", "--fixtures", fixtures}},
		{"query_calc", []string{"query", "12*3+4"}},
		{"query_hex", []string{"query", "255 in hex"}},
		{"run_clipboard_history", []string{"run", "builtin.clipboard/history", "--fixtures", fixtures}},
		{"run_clipboard_search", []string{"run", "builtin.clipboard/history", "--text", "世界", "--fixtures", fixtures}},
		{"plugin_list", []string{"plugin", "list"}},
		{"run_file_search", []string{"run", "builtin.everything/search", "--text", "工作", "--fixtures", fixtures}},
		{"run_file_search_report", []string{"run", "builtin.everything/search", "--text", "report q3", "--fixtures", fixtures}},
		{"query_scripts", []string{"query", "greet", "--fixtures", fixtures}},
		{"run_scoop_search", []string{"run", "builtin.scoop/search", "--text", "git", "--fixtures", fixtures}},
		{"run_scoop_search_chinese", []string{"run", "builtin.scoop/search", "--text", "微信", "--fixtures", fixtures}},
		{"run_scoop_installed", []string{"run", "builtin.scoop/installed", "--fixtures", fixtures}},
		{"run_scoop_updates", []string{"run", "builtin.scoop/updates", "--fixtures", fixtures}},
		// WinGet (docs/winget.md).
		{"run_winget_search", []string{"run", "builtin.winget/search", "--text", "textpad", "--fixtures", fixtures}},
		{"run_winget_search_chinese", []string{"run", "builtin.winget/search", "--text", "聊天", "--fixtures", fixtures}},
		{"run_winget_installed", []string{"run", "builtin.winget/installed", "--fixtures", fixtures}},
		{"run_winget_updates", []string{"run", "builtin.winget/updates", "--fixtures", fixtures}},
		{"run_winget_sources", []string{"run", "builtin.winget/sources", "--fixtures", fixtures}},
		{"query_winget", []string{"query", "winget gitty", "--fixtures", fixtures}},
		{"zh_run_winget_updates", []string{"run", "builtin.winget/updates", "--locale", "zh-CN", "--fixtures", fixtures}},
		{"query_clock", []string{"query", "time", "--now", clockNow}},
		{"query_clock_bj", []string{"query", "time bj", "--now", clockNow}},
		{"query_clock_zh", []string{"query", "time 北京", "--now", clockNow}},
		// Timers and reminders (docs/timers.md).
		{"query_timer", []string{"query", "timer 25m", "--now", clockNow}},
		{"query_timer_label", []string{"query", "timer 1h30m 喝水", "--now", clockNow}},
		{"query_timer_empty", []string{"query", "timer", "--now", clockNow}},
		{"query_remind", []string{"query", "remind 15:30 开会", "--now", clockNow}},
		{"query_remind_rolled", []string{"query", "remind 9am standup", "--now", clockNow}},
		{"run_timers", []string{"run", "builtin.timer/timers", "--text", "15:30 call", "--now", clockNow}},
		// Quicklinks and web search (docs/quicklinks.md).
		{"query_web_search", []string{"query", "g golang generics"}},
		{"query_web_search_baidu", []string{"query", "bd 北京 天气"}},
		{"query_web_keyword", []string{"query", "yt"}},
		{"query_web_url", []string{"query", "github.com/foo"}},
		{"query_web_fallback", []string{"query", "zzqx qwv"}},
		{"run_web_engines", []string{"run", "builtin.quicklinks/engines"}},
		{"zh_query_web_fallback", []string{"query", "zzqx qwv", "--locale", "zh-CN"}},
		{"zh_query_web_search", []string{"query", "zhihu 量子计算", "--locale", "zh-CN"}},
		// Unit conversion (docs/unit-conversion.md).
		{"query_unit", []string{"query", "10 km in mi"}},
		{"query_unit_bare", []string{"query", "30 ℃"}},
		{"query_unit_expr", []string{"query", "5 GB / 100 Mbps"}},
		{"query_unit_zh", []string{"query", "3斤换成kg"}},
		{"query_currency", []string{"query", "100 usd in cny", "--fixtures", fixtures}},
		{"query_currency_bare", []string{"query", "100 美元", "--fixtures", fixtures}},
		{"query_currency_no_rates", []string{"query", "100 usd"}},
		{"query_timestamp", []string{"query", "1700000000 to date", "--now", clockNow}},
		{"query_now_unix", []string{"query", "now in unix", "--now", clockNow}},
		{"query_color", []string{"query", "#ff8800 to rgb"}},
		{"query_size", []string{"query", "US 9 shoe in EU"}},
		{"query_not_unit", []string{"query", "mi", "--fixtures", fixtures}},
		{"query_system_lock", []string{"query", "lock"}},
		{"query_system_shutdown_zh", []string{"query", "关机"}},
		{"query_system_bluetooth_zh", []string{"query", "蓝牙"}},
		{"query_network_ip", []string{"query", "ip", "--fixtures", fixtures}},
		{"query_network_port", []string{"query", "port 8080", "--fixtures", fixtures}},
		{"query_network_invalid", []string{"query", "ping exa..mple"}},
		// The Simplified Chinese UI (docs/i18n-zh-cn.md).
		{"zh_query_empty", []string{"query", "--locale", "zh-CN"}},
		{"zh_query_calc", []string{"query", "12*3+4", "--locale", "zh-CN"}},
		{"zh_query_english", []string{"query", "clipboard", "--locale", "zh-CN"}}, // English words still find commands
		{"zh_query_pinyin", []string{"query", "jtb", "--locale", "zh-CN"}},
		{"zh_run_clipboard_history", []string{"run", "builtin.clipboard/history", "--locale", "zh-CN", "--fixtures", fixtures}},
		{"zh_query_clock", []string{"query", "time 北京", "--locale", "zh-CN", "--now", clockNow}},
		{"zh_query_timer", []string{"query", "计时 半小时", "--locale", "zh-CN", "--now", clockNow}},
		{"zh_query_remind", []string{"query", "提醒 明天 9点 交周报", "--locale", "zh-CN", "--now", clockNow}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, code := glctl(t, tt.args...)
			if code != 0 {
				t.Fatalf("exit code %d", code)
			}
			golden(t, tt.name, out)
		})
	}
}

// TestSessionTimer starts a timer from the root search; "timer" then lists
// it with the time left (the clock is fixed, so it does not tick).
func TestSessionTimer(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s, "--now", clockNow)
	glctl(t, "session", "type", "-s", s, "timer 25m tea")
	out, _ := glctl(t, "session", "key", "-s", s, "Enter")
	golden(t, "session_timer_start", out)
	out, _ = glctl(t, "session", "type", "-s", s, "timer")
	golden(t, "session_timer_list", out)
}

// TestSessionWebSearch opens a search typed after a keyword (the fake
// system only records it), and it is not recorded as recently used.
func TestSessionWebSearch(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s)
	glctl(t, "session", "type", "-s", s, "g golang generics")
	out, _ := glctl(t, "session", "key", "-s", s, "Enter")
	golden(t, "session_web_search", out)
	out, _ = glctl(t, "session", "type", "-s", s, "")
	if strings.Contains(out, "golang generics") {
		t.Errorf("the search was recorded:\n%s", out)
	}
}

// TestSessionQuicklink creates a quicklink with {query} from its form,
// finds it by its keyword and opens it with the text typed after it.
func TestSessionQuicklink(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s)
	out := steps(t, s,
		[]string{"type", "create quicklink"},
		[]string{"key", "Enter"},
		[]string{"field", "name", "Open Issue"},
		[]string{"field", "link", "https://jira.example.com/browse/{query}"},
		[]string{"field", "keyword", "jira"},
		[]string{"key", "Ctrl+Enter"},
	)
	golden(t, "session_quicklink_create", out)
	out = steps(t, s, []string{"type", "jira ABC-123"}, []string{"key", "Enter"})
	golden(t, "session_quicklink_open", out)
	// By name, with the inline argument.
	out = steps(t, s, []string{"type", "open issue"}, []string{"arg", "query", "XYZ-9"}, []string{"key", "Enter"})
	if !strings.Contains(out, `"target": "https://jira.example.com/browse/XYZ-9"`) {
		t.Errorf("argument not used:\n%s", out)
	}
}

func TestSessionPasteFromClipboardHistory(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	if _, code := glctl(t, "session", "new", "-s", s, "--fixtures", fixtures); code != 0 {
		t.Fatal("session new failed")
	}
	steps := [][]string{
		{"type", "clipboard history"},
		{"key", "Enter"},
		{"type", "git"},
		{"key", "Enter"},
	}
	var out string
	for _, st := range steps {
		var code int
		out, code = glctl(t, append([]string{"session", st[0], "-s", s}, st[1:]...)...)
		if code != 0 {
			t.Fatalf("step %v failed", st)
		}
	}
	golden(t, "session_paste", out)
}

// TestSessionNetworkDNS opens the DNS view from the root search; the fake
// resolver answers from testdata/fixtures/network.json.
func TestSessionNetworkDNS(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s, "--fixtures", fixtures)
	glctl(t, "session", "type", "-s", s, "dns example.com")
	out, _ := glctl(t, "session", "key", "-s", s, "Enter")
	golden(t, "session_network_dns", out)
}

func TestSessionConfirmClear(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s, "--fixtures", fixtures)
	glctl(t, "session", "type", "-s", s, "clear clipboard")
	out, _ := glctl(t, "session", "key", "-s", s, "Enter")
	golden(t, "session_confirm_ask", out)
	out, _ = glctl(t, "session", "key", "-s", s, "Enter")
	golden(t, "session_confirm_done", out)
}

// TestSessionEmptyRecycleBin asks before emptying the Recycle Bin (of the
// fake machine) and then reports it.
func TestSessionEmptyRecycleBin(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s)
	glctl(t, "session", "type", "-s", s, "empty recycle bin")
	out, _ := glctl(t, "session", "key", "-s", s, "Enter")
	golden(t, "session_system_empty_bin_ask", out)
	out, _ = glctl(t, "session", "key", "-s", s, "Enter")
	golden(t, "session_system_empty_bin_done", out)
}

// TestSessionShutDownWarnsAboutTasks: while a background task runs, the
// confirmation of Shut Down says that it will be interrupted.
func TestSessionShutDownWarnsAboutTasks(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s, "--fixtures", fixtures)
	glctl(t, "session", "type", "-s", s, "shut down")
	out, _ := glctl(t, "session", "key", "-s", s, "Enter")
	if !strings.Contains(out, `"title": "Shut Down?"`) || !strings.Contains(out, "Open apps will be closed.") {
		t.Errorf("no confirmation without tasks:\n%s", out)
	}
	glctl(t, "session", "key", "-s", s, "Escape")
	glctl(t, "session", "type", "-s", s, "update scoop buckets")
	glctl(t, "session", "key", "-s", s, "Enter") // held until wait-tasks
	glctl(t, "session", "type", "-s", s, "shut down")
	out, _ = glctl(t, "session", "key", "-s", s, "Enter")
	if !strings.Contains(out, `"message": "1 background task will be interrupted."`) {
		t.Errorf("no warning about the running task:\n%s", out)
	}
}

// TestSessionScoopInstall installs a package from the search view: the
// command runs as a background task (the fake runner records it), the
// action returns at once with a toast, and the task view shows the output.
func TestSessionScoopInstall(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s, "--fixtures", fixtures)
	glctl(t, "session", "type", "-s", s, "search scoop packages")
	glctl(t, "session", "key", "-s", s, "Enter")
	glctl(t, "session", "type", "-s", s, "lazygit")
	out, _ := glctl(t, "session", "key", "-s", s, "Enter")
	golden(t, "session_scoop_install", out)
	if !strings.Contains(out, "Install lazygit: running in the background") {
		t.Errorf("no toast:\n%s", out)
	}
	if out, code := glctl(t, "session", "wait-tasks", "-s", s); code != 0 || strings.Contains(out, `"tasks"`) {
		t.Errorf("wait-tasks (%d): the indicator should be gone:\n%s", code, out)
	}
	out, _ = glctl(t, "session", "key", "-s", s, "Ctrl+J")
	for _, want := range []string{`"title": "Background Tasks"`, `"title": "Install lazygit"`, `"accessory": "✓ 0s"`,
		"[not run] scoop install main/lazygit", `"value": "scoop install main/lazygit"`} {
		if !strings.Contains(out, want) {
			t.Errorf("the task view lacks %s:\n%s", want, out)
		}
	}
	out, code := glctl(t, "tasks", "list", "-s", s, "--format", "text")
	if code != 0 || !strings.Contains(out, "succeeded Install lazygit — scoop install main/lazygit") {
		t.Errorf("tasks list (%d):\n%s", code, out)
	}
}

// TestSessionScoopUninstallConfirms asks before uninstalling.
func TestSessionScoopUninstallConfirms(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s, "--fixtures", fixtures)
	glctl(t, "session", "type", "-s", s, "installed scoop apps")
	glctl(t, "session", "key", "-s", s, "Enter")
	glctl(t, "session", "type", "-s", s, "7zip")
	out, _ := glctl(t, "session", "key", "-s", s, "Ctrl+X")
	if !strings.Contains(out, `"confirm"`) {
		t.Fatalf("no confirmation:\n%s", out)
	}
	glctl(t, "session", "key", "-s", s, "Enter")
	out, _ = glctl(t, "tasks", "list", "-s", s, "--format", "text")
	if !strings.Contains(out, "scoop uninstall 7zip") {
		t.Errorf("the uninstall command did not run:\n%s", out)
	}
}

// TestTasksFixture configures the fake output of background tasks with
// tasks.json, shows a failed task with glctl tasks show and the red dot.
func TestTasksFixture(t *testing.T) {
	dir := t.TempDir()
	copyFixtures(t, dir)
	os.WriteFile(filepath.Join(dir, "tasks.json"), []byte(`[{"match": "scoop update", "output": "Updating Scoop...\nERROR network down", "exitCode": 1}]`), 0o644)
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s, "--fixtures", dir)
	glctl(t, "session", "type", "-s", s, "update scoop buckets")
	glctl(t, "session", "key", "-s", s, "Enter")
	out, _ := glctl(t, "session", "wait-tasks", "-s", s)
	for _, want := range []string{`"failed": 1`, `"label": "A background task failed"`, `"title": "Update Buckets failed"`, `"message": "ERROR network down"`} {
		if !strings.Contains(out, want) {
			t.Errorf("after the failure, no %s:\n%s", want, out)
		}
	}
	out, _ = glctl(t, "tasks", "list", "-s", s)
	id := regexp.MustCompile(`"id": "(t[0-9-]+)"`).FindStringSubmatch(out)
	if id == nil {
		t.Fatalf("no task:\n%s", out)
	}
	out, code := glctl(t, "tasks", "show", id[1], "-s", s)
	if code != 0 || !strings.Contains(out, `"output": "Updating Scoop...\nERROR network down"`) || !strings.Contains(out, `"exitCode": 1`) {
		t.Errorf("tasks show (%d):\n%s", code, out)
	}
	if _, code := glctl(t, "tasks", "show", "t-nope", "-s", s); code == 0 {
		t.Error("an unknown task was shown")
	}
}

// copyFixtures copies the fixtures directory to dir.
func copyFixtures(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(fixtures, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(fixtures, path)
		target := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"bogus"}, {"run"}, {"session", "type"}, {"run", "noslash"}, {"tasks"}, {"tasks", "show"}} {
		if _, code := glctl(t, args...); code != 2 {
			t.Errorf("%v: exit code %d, want 2", args, code)
		}
	}
	if out, code := glctl(t, "help"); code != 0 || !strings.Contains(out, "glctl drives") {
		t.Errorf("help: %d %q", code, out)
	}
}

func TestConfigPaths(t *testing.T) {
	tree := map[string]any{}
	setPath(tree, strings.Split("plugins.builtin.apps.commands.reload.alias", "."), "rr")
	setPath(tree, []string{"theme"}, "dark")
	if v, ok := lookup(tree, "plugins.builtin.apps.commands.reload.alias"); !ok || v != "rr" {
		t.Fatalf("lookup = %v %v; tree = %v", v, ok, tree)
	}
	if v, _ := lookup(tree, "theme"); v != "dark" {
		t.Fatalf("theme = %v", v)
	}
}

func TestI18nCheck(t *testing.T) {
	out, code := glctl(t, "i18n", "check", "--src", "../..")
	if code != 0 {
		t.Fatalf("i18n check failed:\n%s", out)
	}
}
