package gemini

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"path/filepath"
	"runtime"
	"strings"
)

// The variables that move Gemini CLI's system settings and system defaults
// files.
const (
	envSystemSettings = "GEMINI_CLI_SYSTEM_SETTINGS_PATH"
	envSystemDefaults = "GEMINI_CLI_SYSTEM_DEFAULTS_PATH"
)

// defaultSystemSettings is where Gemini CLI reads its system settings on
// this system; its system defaults are system-defaults.json beside it.
var defaultSystemSettings = systemSettingsPath(runtime.GOOS)

// systemSettingsPath is Gemini CLI's system settings path on goos
// (settings.ts getSystemSettingsPath; verified with the bundle of 0.64).
func systemSettingsPath(goos string) string {
	switch goos {
	case "windows":
		return `C:\ProgramData\gemini-cli\settings.json`
	case "darwin":
		return "/Library/Application Support/GeminiCli/settings.json"
	}
	return "/etc/gemini-cli/settings.json"
}

// localTools are Gemini CLI's built-in tools that touch this computer (or
// keep state on it), and its sub-agents; webTools its web tools (0.34
// names).
var (
	localTools = []string{
		"run_shell_command", "read_file", "read_many_files", "write_file", "replace", "glob", "grep_search",
		"list_directory", "save_memory", "ask_user", "activate_skill", "write_todos", "enter_plan_mode",
		"exit_plan_mode", "get_internal_docs", "tracker_create_task", "tracker_update_task", "tracker_get_task",
		"tracker_list_tasks", "tracker_add_dependency", "tracker_visualize",
		"codebase_investigator", "cli_help", "generalist",
	}
	webTools = []string{"google_web_search", "web_fetch"}
)

// noContextFile is the context (memory) file name Gemini CLI looks for
// instead of GEMINI.md: one that does not exist, so neither the user's
// ~/.gemini/GEMINI.md nor a project's is read.
const noContextFile = "golauncher-no-context-file.md"

// ownSettings are the system settings golauncher sets (keys verified with
// the settings schema of Gemini CLI 0.34). Arrays replace those of other
// settings files, except tools.exclude (a union).
func ownSettings(web bool) map[string]any {
	core := []string{}
	exclude := append([]string{}, localTools...)
	if web {
		core = append(core, webTools...)
	} else {
		exclude = append(exclude, webTools...)
	}
	off := map[string]any{"enabled": false}
	return map[string]any{
		"tools": map[string]any{
			// tools.core lists the only built-in tools registered; [] is none.
			"core":    core,
			"exclude": exclude,
			// No tools discovered or called through commands.
			"discoveryCommand": "",
			"callCommand":      "",
		},
		"mcp": map[string]any{
			"allowed":       []string{"golauncher"},
			"serverCommand": "",
		},
		"agents":        map[string]any{"overrides": map[string]any{"codebase_investigator": off, "cli_help": off, "generalist": off}},
		"experimental":  map[string]any{"enableAgents": false, "jitContext": false, "plan": false, "taskTracker": false},
		"context":       map[string]any{"fileName": noContextFile, "loadMemoryFromIncludeDirectories": false},
		"hooksConfig":   off,
		"skills":        off,
		"ide":           off,
		"useWriteTodos": false,
		"security":      map[string]any{"disableYoloMode": true},
		"general": map[string]any{
			"defaultApprovalMode":          "default",
			"enableAutoUpdate":             false,
			"enableAutoUpdateNotification": false,
			"checkpointing":                off,
		},
	}
}

// settings is the system settings file golauncher writes.
type settings struct {
	content      []byte
	hash         string // of content, for the file's name
	defaultsPath string // the system defaults file Gemini CLI would read
}

// systemSettings returns golauncher's system settings merged over the
// system settings file Gemini CLI would read otherwise (an administrator's;
// golauncher's keys win), and where its system defaults are.
func systemSettings(web bool, h host) settings {
	orig := h.getenv(envSystemSettings)
	if orig == "" {
		orig = defaultSystemSettings
	}
	defaults := h.getenv(envSystemDefaults)
	if defaults == "" {
		defaults = filepath.Join(filepath.Dir(orig), "system-defaults.json")
	}
	merged := map[string]any{}
	if b, err := h.readFile(orig); err == nil {
		// Gemini CLI ignores a file it cannot parse; so does golauncher.
		var admin map[string]any
		if json.Unmarshal(stripComments(b), &admin) == nil && admin != nil {
			merged = admin
		}
	}
	merge(merged, ownSettings(web))
	b, err := json.Marshal(merged, json.Deterministic(true), jsontext.WithIndentPrefix(""), jsontext.WithIndent("  "))
	if err != nil {
		panic(err) // JSON values only
	}
	b = append(b, '\n')
	sum := sha256.Sum256(b)
	return settings{content: b, hash: hex.EncodeToString(sum[:6]), defaultsPath: defaults}
}

// merge merges src into dst: objects key by key, anything else replaced.
func merge(dst, src map[string]any) {
	for k, v := range src {
		if sv, ok := v.(map[string]any); ok {
			if dv, ok := dst[k].(map[string]any); ok {
				merge(dv, sv)
				continue
			}
			c := map[string]any{}
			merge(c, sv)
			dst[k] = c
			continue
		}
		dst[k] = v
	}
}

// stripComments removes // and /* */ comments outside strings (Gemini CLI
// reads its settings files with strip-json-comments).
func stripComments(b []byte) []byte {
	var out strings.Builder
	s := string(b)
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '"':
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			end := min(j+1, len(s))
			out.WriteString(s[i:end])
			i = end - 1
		case strings.HasPrefix(s[i:], "//"):
			for i < len(s) && s[i] != '\n' {
				i++
			}
			if i < len(s) {
				out.WriteByte('\n')
			}
		case strings.HasPrefix(s[i:], "/*"):
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				i = len(s)
			} else {
				i += 2 + end + 1
			}
			out.WriteByte(' ')
		default:
			out.WriteByte(s[i])
		}
	}
	return []byte(out.String())
}
