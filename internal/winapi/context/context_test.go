package wincontext

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// fakeFS is a file system of absolute Windows paths: true for folders.
type fakeFS map[string]bool

func (f fakeFS) stat(p string) (bool, bool) {
	for k, dir := range f {
		if strings.EqualFold(k, p) {
			return dir, true
		}
	}
	return false, false
}

func TestPathFromTitle(t *testing.T) {
	fs := fakeFS{
		`C:\`:                              true,
		`C:\Users\ann`:                     true,
		`C:\Users\ann\notes.txt`:           false,
		`C:\Program Files\My App`:          true,
		`D:\项目\季度 报告`:                      true,
		`D:\项目\季度 报告\总结.docx`:              false,
		`\\nas\share\photos`:               true,
		`C:\Windows\System32\cmd.exe`:      false,
		`C:\work\src`:                      true,
		`C:\work\src\main.go`:              false,
		`C:\Users\ann\Downloads`:           true,
		`C:\tmp\a - b`:                     true,
		`C:\tmp`:                           true,
		`C:\Users\ann\with'quote`:          true,
		`C:\Users\ann\Documents\draft.md`:  false,
		`C:\Users\ann\Documents`:           true,
		`C:\data\file [1].csv`:             false,
		`C:\data`:                          true,
		`E:\only\dir`:                      true,
		`C:\Users\ann\project`:             true,
		`C:\Users\ann\project\README.md`:   false,
		`C:\Users\ann\project\docs`:        true,
		`C:\Users\ann\project\docs\x.html`: false,
	}
	tests := []struct {
		title string
		want  string
		dir   bool
	}{
		{`C:\Users\ann`, `C:\Users\ann`, true},
		{`*C:\Users\ann\notes.txt - Notepad++`, `C:\Users\ann\notes.txt`, false},
		{`C:\Program Files\My App - Total Commander`, `C:\Program Files\My App`, true},
		{`"D:\项目\季度 报告\总结.docx" - Word`, `D:\项目\季度 报告\总结.docx`, false},
		{`总结.docx - D:\项目\季度 报告 - WPS`, `D:\项目\季度 报告`, true},
		{`\\nas\share\photos - OneCommander`, `\\nas\share\photos`, true},
		{`Administrator: C:\Windows\System32\cmd.exe`, `C:\Windows\System32\cmd.exe`, false},
		{`main.go - C:\work\src - Visual Studio Code`, `C:\work\src`, true},
		{`~\Downloads`, `C:\Users\ann\Downloads`, true},
		{`PS C:/Users/ann/Downloads>`, `C:\Users\ann\Downloads`, true},
		{`C:\tmp\a - b`, `C:\tmp\a - b`, true},
		{`C:\Users\ann\Documents\draft.md • Modified`, `C:\Users\ann\Documents\draft.md`, false},
		{`C:\data\file [1].csv`, `C:\data\file [1].csv`, false},
		{`C:\data\missing.csv`, ``, false}, // only existing paths count
		{`C:\\Users\\ann`, `C:\Users\ann`, true},
		{`C:\`, `C:\`, true},
		{`「E:\only\dir」 を開く`, `E:\only\dir`, true},
		{`Inbox - Outlook`, ``, false},
		{`X:\nowhere\at all`, ``, false},
		{``, ``, false},
	}
	for _, tt := range tests {
		got, dir, ok := PathFromTitle(tt.title, `C:\Users\ann`, fs.stat)
		if got != tt.want || ok != (tt.want != "") || (ok && dir != tt.dir) {
			t.Errorf("PathFromTitle(%q) = %q, %v, %v; want %q, %v", tt.title, got, dir, ok, tt.want, tt.dir)
		}
	}
}

func TestPathFromTitleBoundsLookups(t *testing.T) {
	n := 0
	stat := func(string) (bool, bool) { n++; return false, false }
	PathFromTitle(strings.Repeat(`C:\a b `, 100), "", stat)
	if n > maxStats {
		t.Fatalf("%d lookups, want at most %d", n, maxStats)
	}
}

func TestParentDir(t *testing.T) {
	for in, want := range map[string]string{
		`C:\a\b.txt`:        `C:\a`,
		`C:\b.txt`:          `C:\`,
		`\\nas\share\x.txt`: `\\nas\share`,
		`D:\项目\总结.docx`:     `D:\项目`,
		`C:\a\b\`:           `C:\a`,
	} {
		if got := parentDir(in); got != want {
			t.Errorf("parentDir(%q) = %q, want %q", in, got, want)
		}
	}
}

// withPlatform replaces the platform hooks for a test.
func withPlatform(t *testing.T, explorer func(context.Context, uintptr) (string, []string, bool), desktop func(context.Context) (string, []string, bool), fs fakeFS) {
	oldE, oldD, oldS, oldH := explorerView, desktopView, statPath, homeDir
	t.Cleanup(func() { explorerView, desktopView, statPath, homeDir = oldE, oldD, oldS, oldH })
	explorerView, desktopView = nil, nil
	if explorer != nil {
		explorerView = func(ctx context.Context, hwnd uintptr, name string) (folderView, bool) {
			if name != "" {
				t.Errorf("a name looked up without a pointer: %q", name)
			}
			f, s, ok := explorer(ctx, hwnd)
			return folderView{Folder: f, Selected: s}, ok
		}
	}
	if desktop != nil {
		desktopView = func(ctx context.Context, name string) (folderView, bool) {
			if name != "" {
				t.Errorf("a name looked up without a pointer: %q", name)
			}
			f, s, ok := desktop(ctx)
			return folderView{Folder: f, Selected: s}, ok
		}
	}
	statPath = fs.stat
	homeDir = func() string { return `C:\Users\ann` }
}

func TestClassDispatch(t *testing.T) {
	var explorerCalls, desktopCalls int
	withPlatform(t,
		func(_ context.Context, hwnd uintptr) (string, []string, bool) {
			explorerCalls++
			if hwnd == 2 {
				return "", nil, true // a virtual folder such as This PC
			}
			return `C:\Users\ann\Downloads`, []string{`C:\Users\ann\Downloads\a.zip`}, true
		},
		func(context.Context) (string, []string, bool) {
			desktopCalls++
			return `C:\Users\ann\Desktop`, nil, true
		},
		fakeFS{`C:\work\src`: true, `C:\work\src\main.go`: false},
	)
	tests := []struct {
		w      Window
		want   string // detector name
		folder string
		sel    []string
	}{
		{Window{HWND: 1, Class: ClassExplorer, Title: "Downloads - File Explorer"}, plugin.ContextExplorer, `C:\Users\ann\Downloads`, []string{`C:\Users\ann\Downloads\a.zip`}},
		{Window{HWND: 2, Class: ClassExplorer, Title: "This PC - File Explorer"}, plugin.ContextExplorer, "", nil},
		{Window{HWND: 3, Class: ClassProgman}, plugin.ContextDesktop, `C:\Users\ann\Desktop`, nil},
		{Window{HWND: 4, Class: ClassWorkerW, Desktop: true}, plugin.ContextDesktop, `C:\Users\ann\Desktop`, nil},
		{Window{HWND: 5, Class: ClassWorkerW}, "", "", nil},
		{Window{HWND: 6, Class: "Chrome_WidgetWin_1", Title: `main.go - C:\work\src - Visual Studio Code`}, plugin.ContextPathInTitle, `C:\work\src`, nil},
		{Window{HWND: 7, Class: "Notepad", Title: `C:\work\src\main.go - Notepad`}, plugin.ContextPathInTitle, `C:\work\src`, nil},
		{Window{HWND: 8, Class: "Notepad", Title: "Untitled - Notepad"}, "", "", nil},
	}
	for _, tt := range tests {
		c := plugin.Context{Title: tt.w.Title}
		got := run(t.Context(), Detectors(), tt.w, &c)
		if got != tt.want || c.Kind != tt.want || c.Folder != tt.folder || !slices.Equal(c.Selected, tt.sel) {
			t.Errorf("%+v: detector %q, context %+v; want %q in %q with %q", tt.w, got, c, tt.want, tt.folder, tt.sel)
		}
	}
	if explorerCalls != 2 || desktopCalls != 2 {
		t.Errorf("explorer called %d times, desktop %d; want 2 each (only for their classes)", explorerCalls, desktopCalls)
	}
}

func TestTitleDetectorKeepsFileAndSkipsOwnExe(t *testing.T) {
	withPlatform(t, nil, nil, fakeFS{`C:\work\src\main.go`: false, `C:\Windows\System32\cmd.exe`: false})
	c := plugin.Context{App: plugin.AppInfo{Path: `C:\Windows\System32\cmd.exe`}}
	if run(t.Context(), Detectors(), Window{Title: `C:\Windows\System32\cmd.exe`}, &c) != "" {
		t.Errorf("a console's own executable in its title is no context: %+v", c)
	}
	c = plugin.Context{}
	run(t.Context(), Detectors(), Window{Title: `C:\work\src\main.go - Notepad`}, &c)
	if c.Extra["file"] != `C:\work\src\main.go` {
		t.Errorf("Extra = %v, want the file", c.Extra)
	}
}

type nameDetector string

func (d nameDetector) Name() string { return string(d) }
func (d nameDetector) Detect(_ context.Context, w Window, c *plugin.Context) bool {
	if w.Class != "Custom" {
		return false
	}
	c.Kind = string(d)
	return true
}

func TestRegisterFirst(t *testing.T) {
	old := Detectors()
	t.Cleanup(func() { mu.Lock(); detectors = old; mu.Unlock() })
	withPlatform(t, nil, nil, fakeFS{})
	RegisterFirst(nameDetector("custom"))
	Register(nameDetector("late"))
	c := plugin.Context{}
	if got := run(t.Context(), Detectors(), Window{Class: "Custom"}, &c); got != "custom" {
		t.Fatalf("detector %q, want the one registered first", got)
	}
}
