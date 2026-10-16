package cli

import (
	"strings"
	"testing"
)

// mcpScript is an AI client's session with glctl mcp: it finds VS Code and
// opens it, looks for a WinGet package and tries to install it.
const mcpScript = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude-code","version":"2.1.0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_plugins","arguments":{"plugin":"builtin.winget"}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search","arguments":{"query":"vsc","limit":3}}}
{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"run_action","arguments":{"handle":"i1"}}}
{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"run_command","arguments":{"plugin":"builtin.winget","command":"search","query":"tailspin"}}}
{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"get_detail","arguments":{"handle":"i5"}}}
{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"run_action","arguments":{"handle":"i5"}}}
{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"run_action","arguments":{"handle":"i42"}}}
{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"delete_everything","arguments":{}}}
{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"list_plugins","arguments":{"plugin":"builtin.clipboard"}}}
`

func glctlMCP(t *testing.T, input string, args ...string) string {
	t.Helper()
	old := stdin
	stdin = strings.NewReader(input)
	defer func() { stdin = old }()
	out, code := glctl(t, append([]string{"mcp", "--fixtures", fixtures}, args...)...)
	if code != 0 {
		t.Fatalf("glctl mcp: exit %d", code)
	}
	return out
}

func TestMCPGolden(t *testing.T) {
	out := glctlMCP(t, mcpScript)
	golden(t, "mcp_deny", out)
	for _, want := range []string{`\"status\":\"declined\"`, `\"status\":\"done\"`, `"code":-32602`, `unknown handle`, `\"status\":\"refused\"`} {
		if !strings.Contains(out, want) {
			t.Errorf("no %s in:\n%s", want, out)
		}
	}
	out = glctlMCP(t, mcpScript, "--confirm", "allow")
	golden(t, "mcp_allow", out)
	if !strings.Contains(out, `Install Tailspin Toolbox: running in the background`) {
		t.Errorf("not installed:\n%s", out)
	}
}

func TestMCPUsage(t *testing.T) {
	if _, code := glctl(t, "mcp", "--confirm", "maybe"); code != 2 {
		t.Errorf("exit %d", code)
	}
}
