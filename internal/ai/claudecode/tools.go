package claudecode

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
)

// toolServerName is the name of golauncher's MCP server in the CLI's MCP
// configuration; the CLI names its tools mcp__<server>__<tool>.
const toolServerName = "golauncher"

// launcherToolsPattern is the --allowedTools entry that pre-approves the
// tools of golauncher's MCP server (and nothing else). The launcher asks the
// user itself before any tool changes something (docs/ai.md §5).
var launcherToolsPattern = "mcp__" + toolServerName + "__*"

// webTools are Claude Code's own tools for searching and fetching web pages.
var webTools = []string{"WebSearch", "WebFetch"}

// toolFlags returns the flags that choose the model's tools: no local tool
// of Claude Code's, the web tools when web is set, and golauncher's MCP
// server when mcpConfig (its configuration file) is set. Only these tools
// are pre-approved; with --permission-prompts none everything else is
// denied.
func toolFlags(web bool, mcpConfig string) []string {
	var builtin, allowed []string
	if mcpConfig != "" {
		allowed = append(allowed, launcherToolsPattern)
	}
	if web {
		builtin = webTools
		allowed = append(allowed, webTools...)
	}
	a := []string{"--tools", strings.Join(builtin, ",")}
	if mcpConfig != "" {
		a = append(a, "--mcp-config", mcpConfig)
	}
	if len(allowed) > 0 {
		a = append(a, "--allowedTools", strings.Join(allowed, ","))
	}
	return a
}

// mcpServer is a server entry of the CLI's MCP configuration.
type mcpServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

// toolConfig returns the CLI's MCP configuration for a session: only
// golauncher's server, with the session ID in ts.SessionEnv.
func toolConfig(ts *ToolServer, sessionID string) ([]byte, error) {
	env := maps.Clone(ts.Env)
	if ts.SessionEnv != "" {
		if env == nil {
			env = map[string]string{}
		}
		env[ts.SessionEnv] = sessionID
	}
	args := ts.Args
	if args == nil {
		args = []string{}
	}
	cfg := map[string]map[string]mcpServer{
		"mcpServers": {toolServerName: {Type: "stdio", Command: ts.Command, Args: args, Env: env}},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// toolConfigName is the file name of a session's MCP configuration.
func toolConfigName(sessionID string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, sessionID)
	return "mcp-" + safe + ".json"
}

// launcherTools reports whether the conversation's model gets golauncher's
// tools.
func (c *conversation) launcherTools() bool {
	return c.oo.LauncherTools && c.b.o.Tools != nil
}

// writeToolConfig writes the conversation's MCP configuration into the
// CLI's working directory dir (again for every process, so it is current)
// and returns its path; "" when the model gets no launcher tools. The file
// is removed when the conversation closes.
func (c *conversation) writeToolConfig(dir string) (string, error) {
	if !c.launcherTools() {
		return "", nil
	}
	b, err := toolConfig(c.b.o.Tools, c.oo.SessionID)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, toolConfigName(c.oo.SessionID))
	// Under the lock, so that Close either sees the file or no file is
	// written after it.
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return "", errors.New("claudecode: conversation closed")
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", err
	}
	c.toolConfig = path
	return path, nil
}

// removeToolConfig removes the MCP configuration file written for the
// conversation, if any. It is called by Close, after which no file is
// written any more.
func (c *conversation) removeToolConfig() {
	c.mu.Lock()
	path := c.toolConfig
	c.toolConfig = ""
	c.mu.Unlock()
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		c.b.log.Warn("removing the MCP configuration failed", "error", err)
	}
}
