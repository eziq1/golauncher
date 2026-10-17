package agentrt

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
)

// ToolServerName is the name of golauncher's MCP server in a CLI's MCP
// configuration (Claude Code names its tools mcp__<server>__<tool>).
const ToolServerName = "golauncher"

// ToolServer is how a CLI starts golauncher's MCP server (stdio,
// docs/ai.md §4.3).
type ToolServer struct {
	// Command and Args start the server, e.g. golauncher.exe mcp.
	Command string
	Args    []string
	// Env are variables set for the server (e.g. the pipe's token).
	Env map[string]string
	// SessionEnv, when set, is the name of a variable that receives the
	// conversation's session ID, e.g. "GOLAUNCHER_AGENT_SESSION", so the
	// server can tie its tool calls to the conversation.
	SessionEnv string
}

// server is ts as the conversation with the chat ID sessionID starts it.
func (ts *ToolServer) server(sessionID string) MCPServer {
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
	return MCPServer{Name: ToolServerName, Command: ts.Command, Args: args, Env: env}
}

// MCPConfigName is the file name of a session's MCP configuration.
func MCPConfigName(sessionID string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, sessionID)
	return "mcp-" + safe + ".json"
}

// writeFiles writes an adapter's files (Command.Files) into dir, skipping
// those whose content did not change.
func writeFiles(dir string, files map[string][]byte) error {
	for name, b := range files {
		if name == "" || filepath.Base(name) != name {
			return errors.New("agentrt: invalid file name " + name)
		}
		path := filepath.Join(dir, name)
		if old, err := os.ReadFile(path); err == nil && string(old) == string(b) {
			continue
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// launcherTools reports whether the conversation's model gets golauncher's
// tools.
func (c *conversation) launcherTools() bool {
	return c.oo.LauncherTools && c.r.o.Tools != nil
}

// toolServer is the conversation's tool server, nil without launcher tools.
func (c *conversation) toolServer() *MCPServer {
	if !c.launcherTools() {
		return nil
	}
	s := c.r.o.Tools.server(c.oo.SessionID)
	return &s
}

// writeToolConfig writes the conversation's MCP configuration (when the
// adapter wants a file) into the CLI's working directory dir, again for every
// process so it is current, and returns its path; "" when the model gets no
// launcher tools or the adapter passes the server otherwise. The file is
// removed when the conversation closes.
func (c *conversation) writeToolConfig(dir string) (string, error) {
	s := c.toolServer()
	if s == nil {
		return "", nil
	}
	b, err := c.r.a.MCPConfig(*s)
	if err != nil || b == nil {
		return "", err
	}
	path := filepath.Join(dir, MCPConfigName(c.oo.SessionID))
	// Under the lock, so that Close either sees the file or no file is
	// written after it.
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return "", c.r.errorf("conversation closed")
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
		c.r.log.Warn("removing the MCP configuration failed", "error", err)
	}
}
