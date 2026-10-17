// Package gemini is the AI backend that chats through the user's installed
// Gemini CLI (docs/ai.md §21): the agent runtime (internal/ai/agentrt) with
// the ACP adapter (internal/ai/acp), configured for `gemini --acp`. The user's
// Google sign-in (~/.gemini) is used as it is; golauncher never asks Gemini
// CLI to authenticate.
//
// Gemini CLI is a Node.js program; golauncher starts gemini.exe when one is on
// PATH, else the npm shim gemini.cmd (never the PowerShell or sh launchers).
//
// # Isolation
//
// Gemini CLI's own tools run locally, so they are turned off rather than
// asked for: golauncher writes a system settings file
// (GEMINI_CLI_SYSTEM_SETTINGS_PATH; system settings override the user's and
// the workspace's) that registers no built-in tool but the web tools (when
// the conversation allows web search), denies the local ones again, and turns
// off hooks, skills, sub-agents, tool discovery commands, the GEMINI.md
// memory, checkpointing, IDE integration and YOLO mode. Only golauncher's MCP
// server is allowed (mcp.allowed, --allowed-mcp-server-names, which also lets
// its tools run without asking: golauncher's own tool layer confirms what
// changes something), no extension is loaded (-e none), the approval mode is
// "default" and sandboxes are off (GEMINI_SANDBOX=false). An administrator's
// system settings file is kept: golauncher's settings are merged over it, and
// its system-defaults.json is still read.
package gemini

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/eziq1/golauncher/internal/ai/acp"
	"github.com/eziq1/golauncher/internal/ai/agentrt"
)

// ID is the backend's ID in the AI plugin's "Backend" preference.
const ID = "gemini"

// The CLI versions ACP mode (--acp) was verified with. Versions below
// MinVersion are refused (--acp replaced --experimental-acp in 0.34).
const (
	MinVersion  = "0.34.0"
	TestedMajor = 0
)

// Options configure a Backend (see agentrt.Options).
type Options = agentrt.Options

// ToolServer is how the CLI starts golauncher's MCP server (stdio).
type ToolServer = agentrt.ToolServer

// Backend is the Gemini CLI backend. It is safe for concurrent use.
type Backend struct {
	*agentrt.Runtime
}

// New returns a backend configured by o.
func New(o Options) *Backend {
	return &Backend{Runtime: agentrt.New(acp.NewAdapter(config(hostEnv())), o)}
}

// envDrop are variables removed from the CLI's environment: those Gemini CLI
// sets for the programs it starts (the launcher has them when it was started
// from inside Gemini CLI), the system settings paths golauncher sets itself,
// and those that would replace Gemini CLI's system prompt, write it or the
// activity to files, or connect it to an IDE. The user's own choices (API
// keys, GEMINI_MODEL, GEMINI_CLI_HOME, auth types) are kept.
var envDrop = []string{
	"GEMINI_CLI", "GEMINI_CLI_NO_RELAUNCH", "SANDBOX",
	"GEMINI_CLI_SYSTEM_SETTINGS_PATH", "GEMINI_CLI_SYSTEM_DEFAULTS_PATH",
	"GEMINI_SYSTEM_MD", "GEMINI_WRITE_SYSTEM_MD", "GEMINI_CLI_ACTIVITY_LOG_TARGET", "GEMINI_DEBUG_LOG_FILE",
	"GEMINI_CLI_IDE_SERVER_PORT", "GEMINI_CLI_IDE_WORKSPACE_PATH", "GEMINI_CLI_IDE_AUTH_TOKEN", "GEMINI_CLI_IDE_PID",
	"GEMINI_CLI_IDE_SERVER_STDIO_COMMAND", "GEMINI_CLI_IDE_SERVER_STDIO_ARGS",
	"GEMINI_CLI_INTEGRATION_TEST", "GEMINI_PTY_INFO",
}

// host is what the adapter reads of this computer (replaced by tests).
type host struct {
	getenv   func(string) string
	exists   func(path string) bool
	readFile func(path string) ([]byte, error)
}

func hostEnv() host {
	return host{
		getenv: os.Getenv,
		exists: func(p string) bool {
			fi, err := os.Stat(p)
			return err == nil && !fi.IsDir()
		},
		readFile: os.ReadFile,
	}
}

// config is the ACP configuration of Gemini CLI.
func config(h host) acp.Config {
	return acp.Config{
		Info: agentrt.Info{
			ID:          ID,
			Name:        "Gemini CLI",
			Commands:    []string{"gemini"},
			VersionArgs: []string{"--version"},
			MinVersion:  MinVersion,
			TestedMajor: TestedMajor,
			EnvDrop:     envDrop,
			WorkDir:     "gemini",
			Candidates:  func() ([]string, error) { return candidates(h) },
		},
		Args:       func(l agentrt.Launch) (agentrt.Command, error) { return args(l, h) },
		Tool:       tool,
		ToolResult: toolResult,
	}
}

// candidates finds Gemini CLI: gemini.exe on PATH, then the npm shim
// gemini.cmd on PATH, then %APPDATA%\npm\gemini.cmd.
func candidates(h host) ([]string, error) {
	var exes, cmds, others []string
	for dir := range strings.SplitSeq(h.getenv("PATH"), string(os.PathListSeparator)) {
		dir = strings.Trim(strings.TrimSpace(dir), `"`)
		if dir == "" {
			continue
		}
		if p := filepath.Join(dir, "gemini.exe"); h.exists(p) {
			exes = append(exes, p)
		}
		if p := filepath.Join(dir, "gemini.cmd"); h.exists(p) {
			cmds = append(cmds, p)
		}
		for _, n := range []string{"gemini.ps1", "gemini"} {
			if p := filepath.Join(dir, n); h.exists(p) {
				others = append(others, p)
			}
		}
	}
	if appData := h.getenv("APPDATA"); appData != "" {
		if p := filepath.Join(appData, "npm", "gemini.cmd"); h.exists(p) {
			cmds = append(cmds, p)
		}
	}
	var out []string
	for _, p := range append(exes, cmds...) {
		if !slices.ContainsFunc(out, func(q string) bool { return strings.EqualFold(p, q) }) {
			out = append(out, p)
		}
	}
	if len(out) == 0 && len(others) > 0 {
		return nil, fmt.Errorf("only %s was found; golauncher starts gemini.exe or the npm shim gemini.cmd", others[0])
	}
	return out, nil
}

// validModel is a model name golauncher passes on the command line (which
// may go through cmd.exe for gemini.cmd).
var validModel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// The system settings file golauncher writes into the working directory
// (one per content, so conversations with different settings never race).
const settingsPrefix = "golauncher-system-settings-"

// args is the command line after the executable: ACP mode, the default
// approval mode, no extensions, only golauncher's MCP server, the model; and
// the environment with golauncher's system settings.
func args(l agentrt.Launch, h host) (agentrt.Command, error) {
	a := []string{"--acp", "--approval-mode", "default", "-e", "none", "--allowed-mcp-server-names", agentrt.ToolServerName}
	if l.Model != "" {
		if !validModel.MatchString(l.Model) {
			return agentrt.Command{}, errors.New("gemini: invalid model name " + l.Model)
		}
		a = append(a, "-m", l.Model)
	}
	s := systemSettings(l.WebSearch, h)
	name := settingsPrefix + s.hash + ".json"
	return agentrt.Command{
		Argv: a,
		Env: []string{
			"GEMINI_CLI_SYSTEM_SETTINGS_PATH=" + filepath.Join(l.Dir, name),
			"GEMINI_CLI_SYSTEM_DEFAULTS_PATH=" + s.defaultsPath,
			"GEMINI_SANDBOX=false",
		},
		Files: map[string][]byte{name: s.content},
	}, nil
}
