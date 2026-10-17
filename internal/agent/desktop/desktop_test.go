package desktop

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// All tests work in t.TempDir(); none touches the real Claude Desktop config.

var testNow = time.Date(2026, 10, 15, 9, 8, 7, 0, time.UTC)

const testBackupSuffix = ".golauncher-backup-20261015-090807"

// testExe is a Windows path with backslashes and spaces, which must be
// escaped in JSON.
const testExe = `C:\Program Files\gol auncher\golauncher.exe`

func setup(t *testing.T, content string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Claude")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, configName)
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// entries returns the files in dir.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, de := range des {
		names = append(names, de.Name())
	}
	return names
}

// golauncherEntry decodes mcpServers.golauncher from a config file.
func golauncherEntry(t *testing.T, data string) Entry {
	t.Helper()
	var doc struct {
		MCPServers map[string]Entry `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(data), &doc); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, data)
	}
	e, ok := doc.MCPServers[ServerName]
	if !ok {
		t.Fatalf("no mcpServers.golauncher in\n%s", data)
	}
	return e
}

func TestConnectCreatesFile(t *testing.T) {
	path := setup(t, "")
	ch, err := Connect(path, testExe, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if !ch.Created || ch.Backup != "" || ch.Unchanged || ch.Previous != "" || ch.Path != path {
		t.Errorf("change = %+v", ch)
	}
	got := read(t, path)
	want := "{\n" +
		"  \"mcpServers\": {\n" +
		"    \"golauncher\": {\n" +
		"      \"command\": \"C:\\\\Program Files\\\\gol auncher\\\\golauncher.exe\",\n" +
		"      \"args\": [\n" +
		"        \"mcp\"\n" +
		"      ]\n" +
		"    }\n" +
		"  }\n" +
		"}\n"
	if got != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
	if e := golauncherEntry(t, got); e.Command != testExe || len(e.Args) != 1 || e.Args[0] != "mcp" {
		t.Errorf("entry = %+v", e)
	}
	if !strings.Contains(ch.New, `"command": "C:\\Program Files\\gol auncher\\golauncher.exe"`) {
		t.Errorf("New = %s", ch.New)
	}
	if n := entries(t, filepath.Dir(path)); len(n) != 1 {
		t.Errorf("folder holds %v, want just the config", n)
	}
}

const existing = `{
  "globalShortcut": "Ctrl+Space",
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "C:\\Users\\someone\\Desktop"],
      "env": {"A": "\u00e9", "N": 1.50}
    },
    "golauncher": {
      "command": "C:\\old\\golauncher.exe",
      "args": [
        "mcp"
      ]
    },
    "zeta": {"command": "z"}
  },
  "unknownLast": [1, 2.0, {"b": null, "a": true}]
}
`

func TestConnectUpdatesEntry(t *testing.T) {
	path := setup(t, existing)
	ch, err := Connect(path, testExe, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Created || ch.Unchanged {
		t.Errorf("change = %+v", ch)
	}
	if ch.Backup != path+testBackupSuffix {
		t.Errorf("backup = %q", ch.Backup)
	}
	if got := read(t, ch.Backup); got != existing {
		t.Errorf("backup differs from the old file:\n%s", got)
	}
	if !strings.Contains(ch.Previous, `C:\\old\\golauncher.exe`) {
		t.Errorf("Previous = %s", ch.Previous)
	}
	got := read(t, path)
	want := strings.Replace(existing, `"C:\\old\\golauncher.exe"`, `"C:\\Program Files\\gol auncher\\golauncher.exe"`, 1)
	if got != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
	if e := golauncherEntry(t, got); e.Command != testExe {
		t.Errorf("command = %q", e.Command)
	}
	// No temp files left behind.
	if n := entries(t, filepath.Dir(path)); len(n) != 2 {
		t.Errorf("folder holds %v", n)
	}
}

func TestConnectAddsEntry(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{
			name: "other servers",
			in:   "{\n  \"globalShortcut\": \"\",\n  \"mcpServers\": {\n    \"a\": {\"command\": \"x\"}\n  },\n  \"z\": 1\n}",
			want: "{\n  \"globalShortcut\": \"\",\n  \"mcpServers\": {\n    \"a\": {\"command\": \"x\"},\n" +
				"    \"golauncher\": {\n      \"command\": \"C:\\\\Program Files\\\\gol auncher\\\\golauncher.exe\",\n" +
				"      \"args\": [\n        \"mcp\"\n      ]\n    }\n  },\n  \"z\": 1\n}",
		},
		{
			name: "empty servers",
			in:   "{\n  \"mcpServers\": {   },\n  \"z\": 1\n}",
			want: "{\n  \"mcpServers\": {\n    \"golauncher\": {\n      \"command\": \"C:\\\\Program Files\\\\gol auncher\\\\golauncher.exe\",\n" +
				"      \"args\": [\n        \"mcp\"\n      ]\n    }\n  },\n  \"z\": 1\n}",
		},
		{
			name: "no servers",
			in:   "{\n  \"globalShortcut\": \"Alt+Space\"\n}\n",
			want: "{\n  \"globalShortcut\": \"Alt+Space\",\n  \"mcpServers\": {\n    \"golauncher\": {\n" +
				"      \"command\": \"C:\\\\Program Files\\\\gol auncher\\\\golauncher.exe\",\n" +
				"      \"args\": [\n        \"mcp\"\n      ]\n    }\n  }\n}\n",
		},
		{
			name: "empty root",
			in:   "{}",
			want: "{\n  \"mcpServers\": {\n    \"golauncher\": {\n      \"command\": \"C:\\\\Program Files\\\\gol auncher\\\\golauncher.exe\",\n" +
				"      \"args\": [\n        \"mcp\"\n      ]\n    }\n  }\n}",
		},
		{
			name: "single line",
			in:   `{"mcpServers":{"a":{"command":"x"}},"k":[1]}`,
			want: `{"mcpServers":{"a":{"command":"x"}, "golauncher": {"command":"C:\\Program Files\\gol auncher\\golauncher.exe","args":["mcp"]}},"k":[1]}`,
		},
		{
			name: "tabs and CRLF",
			in:   "{\r\n\t\"mcpServers\": {\r\n\t\t\"a\": {}\r\n\t}\r\n}\r\n",
			want: "{\r\n\t\"mcpServers\": {\r\n\t\t\"a\": {},\r\n\t\t\"golauncher\": {\r\n" +
				"\t\t\t\"command\": \"C:\\\\Program Files\\\\gol auncher\\\\golauncher.exe\",\r\n" +
				"\t\t\t\"args\": [\r\n\t\t\t\t\"mcp\"\r\n\t\t\t]\r\n\t\t}\r\n\t}\r\n}\r\n",
		},
		{
			name: "BOM",
			in:   "\xef\xbb\xbf{\"mcpServers\":{}}",
			want: "\xef\xbb\xbf{\"mcpServers\":{\n  \"golauncher\": {\n    \"command\": \"C:\\\\Program Files\\\\gol auncher\\\\golauncher.exe\",\n" +
				"    \"args\": [\n      \"mcp\"\n    ]\n  }\n}}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := setup(t, tt.in)
			ch, err := Connect(path, testExe, testNow)
			if err != nil {
				t.Fatal(err)
			}
			if ch.Previous != "" || ch.Created || ch.Unchanged || ch.Backup == "" {
				t.Errorf("change = %+v", ch)
			}
			if got := read(t, ch.Backup); got != tt.in {
				t.Errorf("backup = %q", got)
			}
			got := read(t, path)
			if got != tt.want {
				t.Errorf("file =\n%q\nwant\n%q", got, tt.want)
			}
			if !jsontext.Value(bytes.TrimPrefix([]byte(got), bom)).IsValid() {
				t.Errorf("result is not valid JSON")
			}
		})
	}
}

func TestConnectUnchanged(t *testing.T) {
	exeJSON, _ := jsontext.AppendQuote(nil, testExe)
	// Different formatting and member order from what Connect writes.
	content := `{"mcpServers": {"golauncher": {"args": ["mcp"], "command": ` + string(exeJSON) + `}}}`
	path := setup(t, content)
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	ch, err := Connect(path, testExe, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if !ch.Unchanged || ch.Backup != "" || ch.Created || ch.Previous == "" {
		t.Errorf("change = %+v", ch)
	}
	if got := read(t, path); got != content {
		t.Errorf("file changed:\n%s", got)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(old) {
		t.Errorf("mtime changed: %v, want %v", fi.ModTime(), old)
	}
	if n := entries(t, filepath.Dir(path)); len(n) != 1 {
		t.Errorf("folder holds %v", n)
	}
}

func TestConnectRejects(t *testing.T) {
	tests := map[string]string{
		"invalid":           `{"mcpServers": {"a": }`,
		"truncated":         `{"mcpServers": {`,
		"trailing":          `{} {}`,
		"empty":             ` `,
		"root array":        `[]`,
		"root string":       `"x"`,
		"servers array":     `{"mcpServers": []}`,
		"servers null":      `{"mcpServers": null}`,
		"servers string":    `{"mcpServers": "x"}`,
		"duplicate root":    `{"mcpServers": {}, "mcpServers": {}}`,
		"duplicate servers": `{"mcpServers": {"golauncher": {}, "golauncher": {}}}`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			path := setup(t, content)
			_, err := Connect(path, testExe, testNow)
			if err == nil {
				t.Fatal("no error")
			}
			if errors.Is(err, ErrNotInstalled) {
				t.Fatalf("err = %v", err)
			}
			if got := read(t, path); got != content {
				t.Errorf("file changed: %q", got)
			}
			if n := entries(t, filepath.Dir(path)); len(n) != 1 {
				t.Errorf("folder holds %v", n)
			}
			if _, _, err := Status(path, testExe); err == nil {
				t.Errorf("Status: no error")
			}
		})
	}
}

func TestConnectNotInstalled(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Claude")
	path := filepath.Join(dir, configName)
	if _, err := Connect(path, testExe, testNow); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("err = %v, want ErrNotInstalled", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("folder was created: %v", err)
	}
}

func TestConnectBackupNameTaken(t *testing.T) {
	path := setup(t, existing)
	if err := os.WriteFile(path+testBackupSuffix, []byte("older"), 0o644); err != nil {
		t.Fatal(err)
	}
	ch, err := Connect(path, testExe, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Backup != path+testBackupSuffix+"-1" {
		t.Errorf("backup = %q", ch.Backup)
	}
	if got := read(t, path+testBackupSuffix); got != "older" {
		t.Errorf("existing backup overwritten")
	}
}

func TestStatus(t *testing.T) {
	exeJSON, _ := jsontext.AppendQuote(nil, testExe)
	// The same file spelled differently: in other case on Windows, with a
	// "." element (which filepath.Clean removes) elsewhere.
	exe, other := testExe, strings.ToUpper(testExe)
	if runtime.GOOS != "windows" {
		exe, other = "/Applications/gol auncher.app/Contents/MacOS/golauncher", "/Applications/gol auncher.app/Contents/./MacOS/golauncher"
		exeJSON, _ = jsontext.AppendQuote(nil, exe)
	}
	otherJSON, _ := jsontext.AppendQuote(nil, other)
	tests := []struct {
		name               string
		content            string // "" = no file
		connected, sameExe bool
	}{
		{"no file", "", false, false},
		{"no servers", `{"globalShortcut": ""}`, false, false},
		{"other servers", `{"mcpServers": {"a": {"command": "x"}}}`, false, false},
		{"same", `{"mcpServers": {"golauncher": {"command": ` + string(exeJSON) + `, "args": ["mcp"]}}}`, true, true},
		{"same path, other spelling", `{"mcpServers": {"golauncher": {"command": ` + string(otherJSON) + `, "args": ["mcp"]}}}`, true, true},
		{"other copy", `{"mcpServers": {"golauncher": {"command": "D:\\gl\\golauncher.exe", "args": ["mcp"]}}}`, true, false},
		{"other args", `{"mcpServers": {"golauncher": {"command": ` + string(exeJSON) + `, "args": []}}}`, true, false},
		{"odd value", `{"mcpServers": {"golauncher": "x"}}`, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := setup(t, tt.content)
			connected, sameExe, err := Status(path, exe)
			if err != nil {
				t.Fatal(err)
			}
			if connected != tt.connected || sameExe != tt.sameExe {
				t.Errorf("Status = %v, %v; want %v, %v", connected, sameExe, tt.connected, tt.sameExe)
			}
		})
	}

	t.Run("no folder", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "Claude", configName)
		connected, sameExe, err := Status(path, testExe)
		if connected || sameExe || err != nil {
			t.Errorf("Status = %v, %v, %v", connected, sameExe, err)
		}
	})

	t.Run("after Connect", func(t *testing.T) {
		path := setup(t, existing)
		if c, s, _ := Status(path, testExe); !c || s {
			t.Errorf("before: %v, %v", c, s)
		}
		if _, err := Connect(path, testExe, testNow); err != nil {
			t.Fatal(err)
		}
		if c, s, _ := Status(path, testExe); !c || !s {
			t.Errorf("after: %v, %v", c, s)
		}
	})
}

func TestConfigPath(t *testing.T) {
	mk := func(t *testing.T, p string) {
		t.Helper()
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("roaming", func(t *testing.T) {
		appData, local := t.TempDir(), t.TempDir()
		mk(t, filepath.Join(appData, "Claude"))
		mk(t, filepath.Join(local, "Packages", "Claude_abc123", "LocalCache", "Roaming", "Claude"))
		if got, want := configPath(appData, local), filepath.Join(appData, "Claude", configName); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("msix", func(t *testing.T) {
		appData, local := t.TempDir(), t.TempDir()
		mk(t, filepath.Join(local, "Packages", "Claude_abc123", "LocalCache", "Roaming", "Claude"))
		mk(t, filepath.Join(local, "Packages", "ClaudeOther"))
		want := filepath.Join(local, "Packages", "Claude_abc123", "LocalCache", "Roaming", "Claude", configName)
		if got := configPath(appData, local); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("msix package without the folder", func(t *testing.T) {
		appData, local := t.TempDir(), t.TempDir()
		mk(t, filepath.Join(local, "Packages", "Claude_abc123", "LocalCache"))
		if got, want := configPath(appData, local), filepath.Join(appData, "Claude", configName); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("neither", func(t *testing.T) {
		appData, local := t.TempDir(), t.TempDir()
		if got, want := configPath(appData, local), filepath.Join(appData, "Claude", configName); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("ConfigPath", func(t *testing.T) {
		got, err := ConfigPath()
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Base(got) != configName {
			t.Errorf("got %q", filepath.Base(got))
		}
	})
}
