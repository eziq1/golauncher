// Package codex is the AI backend that chats through the user's installed
// Codex CLI, signed in with a ChatGPT account (docs/ai.md §20). It is the
// Codex adapter of the agent runtime (internal/ai/agentrt): the runtime runs
// the processes, this package knows Codex's command line and the JSONL events
// of `codex exec --json`.
//
// Codex has no persistent mode that golauncher uses: every turn runs one
// `codex exec` process that reads the message from stdin, and later turns
// continue the session with `codex exec resume <thread id>`. Codex chooses
// the thread ID; the runtime keeps it per chat (agentrt.SessionMap).
//
// # Isolation
//
// The process runs isolated from the user's own Codex setup and from this
// computer:
//
//   - --ignore-user-config: $CODEX_HOME/config.toml is not read (its MCP
//     servers, notify program, hooks, plugins, profiles, model and project
//     trust); only the sign-in in CODEX_HOME is used. --ignore-rules skips
//     the execpolicy .rules files.
//   - --sandbox read-only with approval_policy="never": nothing may write,
//     and nothing asks.
//   - every feature that gives the model a way to run programs, reach this
//     computer or load the user's extensions is turned off with
//     -c features.<name>=false (an unknown name is ignored there, unlike
//     --disable, so the list works across versions): above all the shell
//     (shell_tool, unified_exec), so no command ever runs; also apps,
//     plugins, hooks, skills, memories, sub-agents, browser and computer use,
//     image tools. The code-mode host (codex-code-mode-host.exe, which runs
//     the JavaScript through which current models call their tools) is
//     off too, unless the model gets golauncher's tools: it is then the
//     way it calls them (verified with 0.162).
//   - the instructions Codex would add about skills, apps, sandbox
//     escalation and collaboration modes are left out.
//   - the only MCP server is golauncher's own (Launch.Tools), passed with
//     -c mcp_servers.golauncher.*; its tools are pre-approved
//     (default_tools_approval_mode="approve"): golauncher's tool layer asks
//     the user itself before anything changes. Web search is "live" or
//     "disabled".
//
// What is not isolated: Codex still reads $CODEX_HOME/AGENTS.md (the user's
// global instructions; no setting turns that off) and writes its session
// files to $CODEX_HOME/sessions, so golauncher's chats appear in the user's
// Codex history.
//
// # The native binary
//
// golauncher must not depend on Node.js, so it runs codex.exe itself, never
// the npm launcher (codex.cmd, codex.ps1 or the `codex` script, which start
// node with codex.js). Candidates finds codex.exe on PATH, the Codex desktop
// app's copies under %LOCALAPPDATA%\OpenAI\Codex\bin, and the native binary
// that an npm installation carries next to its launcher; the runtime uses the
// one with the highest version. On macOS the binary is `codex`: on PATH, in
// Homebrew's folders, in the Codex app, or inside an npm installation (the
// `codex` on PATH is then a link to the launcher script; see
// unixDiscover).
package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// ID is the backend's ID in the AI plugin's "Backend" preference.
const ID = "codex"

// The CLI versions this adapter was verified with. MinVersion is the oldest
// with --ignore-user-config and --ignore-rules (0.130.0); TestedMajor the
// major version of the verified ones (0.130 and 0.162).
const (
	MinVersion  = "0.130.0"
	TestedMajor = 0
)

// envDrop are variables that a parent Codex sets for the programs it starts
// (its sandbox, its thread, its network proxy and exec server) or that the
// npm launcher sets; the launcher has them when it was started from inside
// Codex, and the CLI must not see them. CODEX_HOME stays: the sign-in lives
// there.
var envDrop = []string{
	"CODEX_SANDBOX",
	"CODEX_SANDBOX_NETWORK_DISABLED",
	"CODEX_THREAD_ID",
	"CODEX_SESSION_ID",
	"CODEX_PERMISSION_PROFILE",
	"CODEX_STARTING_DIFF",
	"CODEX_EXEC_SERVER_URL",
	"CODEX_INTERNAL_ORIGINATOR_OVERRIDE",
	"CODEX_NETWORK_PROXY_ACTIVE",
	"CODEX_NETWORK_ALLOW_LOCAL_BINDING",
	"CODEX_NETWORK_POLICY_VIOLATION",
	"CODEX_WINDOWS_SANDBOX_PROXY_PORTS",
	"CODEX_MANAGED_BY_NPM",
	"CODEX_MANAGED_BY_BUN",
	"CODEX_MANAGED_BY_PNPM",
	"CODEX_MANAGED_BY_VITE_PLUS",
	"CODEX_MANAGED_PACKAGE_ROOT",
}

// disabledFeatures are the Codex features turned off for every process
// (-c features.<name>=false), sorted. Names a version does not know are
// ignored by it.
var disabledFeatures = []string{
	"apps",                         // ChatGPT apps (connectors) and their MCP server
	"browser_use",                  // browser control
	"browser_use_external",         //
	"code_mode",                    // the experimental code mode (code_mode_host: see Command)
	"computer_use",                 // desktop control
	"enable_mcp_apps",              //
	"goals",                        // persistent goals
	"guardian_approval",            // a model that reviews approval requests
	"hooks",                        // the user's hooks (hooks.json)
	"image_generation",             //
	"in_app_browser",               //
	"js_repl",                      // a JavaScript REPL
	"memories",                     // the user's memories
	"multi_agent",                  // sub-agents
	"multi_agent_v2",               //
	"plugins",                      // the user's plugins and their MCP servers
	"realtime_conversation",        //
	"recommended_plugins",          //
	"remote_plugin",                //
	"request_permissions_tool",     // asking for more permissions
	"shell_snapshot",               //
	"shell_tool",                   // the shell: no command ever runs
	"skill_mcp_dependency_install", // installing MCP servers for skills
	"skill_search",                 // the user's skills
	"sleep_tool",                   //
	"tool_suggest",                 //
	"unified_exec",                 // the other way to run commands
	"view_image",                   // reading image files of this computer
	"workspace_dependencies",       //
}

// isolationConfig are the other settings (-c key=value) of every process.
var isolationConfig = []string{
	`approval_policy="never"`,
	"skills.include_instructions=false",
	"include_permissions_instructions=false",
	"include_apps_instructions=false",
	"include_collaboration_mode_instructions=false",
	"project_doc_max_bytes=0",
	"check_for_update_on_startup=false",
}

// Options configure a Backend (see agentrt.Options). Options.Path, when set,
// must be the native CLI (codex.exe on Windows); the npm launcher of an
// installation with a native binary is replaced by that binary.
type Options = agentrt.Options

// ToolServer is how the CLI starts golauncher's MCP server (stdio).
type ToolServer = agentrt.ToolServer

// Backend is the Codex backend: the agent runtime with the Codex adapter. It
// is safe for concurrent use.
type Backend struct {
	*agentrt.Runtime
}

// New returns a backend configured by o.
func New(o Options) *Backend {
	return &Backend{Runtime: agentrt.New(adapter{}, o)}
}

// adapter is Codex's agentrt.Adapter.
type adapter struct{}

var _ agentrt.Adapter = adapter{}

// Info implements agentrt.Adapter.
func (adapter) Info() agentrt.Info {
	return agentrt.Info{
		ID:               "codex",
		Name:             "Codex (ChatGPT)",
		Commands:         []string{"codex"}, // for messages only: Candidates finds the CLI
		VersionArgs:      []string{"--version"},
		ParseVersion:     parseVersion,
		MinVersion:       MinVersion,
		TestedMajor:      TestedMajor,
		EnvDrop:          envDrop,
		WorkDir:          "codex",
		PerTurnOnly:      true,
		AssignsSessionID: true,
		Candidates:       func() ([]string, error) { return candidates(candidateEnv()) },
		Discover:         func() []agentrt.Found { return discover(candidateEnv()) },
		// A configured npm launcher runs the native binary next to it.
		ResolvePath: func(path string) string { return nativePath(path, candidateEnv()) },
		LoginArgs:   []string{"login", "status"},
		Attachments: backend.AllAttachments(),
	}
}

// versionRE matches a x.y.z version, which may go on with a pre-release
// suffix ("0.162.0-alpha.17.2").
var versionRE = regexp.MustCompile(`(?:^|[^0-9.])v?(\d+)\.(\d+)\.(\d+)`)

// parseVersion returns the first x.y.z in out, e.g. "0.162.0" from
// "codex-cli 0.162.0-alpha.17.2".
func parseVersion(out string) string {
	m := versionRE.FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	return m[1] + "." + m[2] + "." + m[3]
}

// Command implements agentrt.Adapter (see the package documentation for the
// isolation flags).
//
// The images of the message (Launch.Attachments, R91) are passed as
// --image=<path>, one per image: `codex exec --image` takes several values
// (it would swallow the arguments after it) and resume's may split a value
// at ',', so they go right after "exec", or right after "resume" before the
// thread ID, in the equals form; a path with a ',' is refused.
func (adapter) Command(l agentrt.Launch) (agentrt.Command, error) {
	var images []string
	for _, img := range backend.Images(l.Attachments) {
		if strings.Contains(img.Path, ",") {
			return agentrt.Command{}, backend.AttachmentError("Codex", "an image whose path has a ',' cannot be sent")
		}
		images = append(images, "--image="+img.Path)
	}
	a := []string{l.Path, "exec"}
	if !l.Resume {
		a = append(a, images...)
	}
	a = append(a, "--json", "--color", "never", "--skip-git-repo-check",
		"--ignore-user-config", "--ignore-rules", "--sandbox", "read-only")
	cfg := func(kv string) { a = append(a, "-c", kv) }
	for _, kv := range isolationConfig {
		cfg(kv)
	}
	for _, f := range disabledFeatures {
		cfg("features." + f + "=false")
	}
	if l.Tools == nil {
		// The current models call every tool from JavaScript run by the
		// code-mode host (their tool_mode is "code_mode_only"); with the shell
		// off, the only tools it can reach are golauncher's. Without them it
		// is not needed at all.
		cfg("features.code_mode_host=false")
	}
	if l.WebSearch {
		cfg(`web_search="live"`)
	} else {
		cfg(`web_search="disabled"`)
	}
	if l.Model != "" {
		a = append(a, "-m", l.Model)
	}
	if l.SystemPrompt != "" {
		cfg("developer_instructions=" + tomlString(l.SystemPrompt))
	}
	if s := l.Tools; s != nil {
		key := "mcp_servers." + tomlKey(s.Name) + "."
		cfg(key + "command=" + tomlString(s.Command))
		args := make([]string, len(s.Args))
		for i, arg := range s.Args {
			args[i] = tomlString(arg)
		}
		cfg(key + "args=[" + strings.Join(args, ", ") + "]")
		names := make([]string, 0, len(s.Env))
		for k := range s.Env {
			names = append(names, k)
		}
		slices.Sort(names)
		for _, k := range names {
			cfg(key + "env." + tomlKey(k) + "=" + tomlString(s.Env[k]))
		}
		cfg(key + `default_tools_approval_mode="approve"`)
	}
	if l.Resume {
		if l.SessionID == "" {
			return agentrt.Command{}, errors.New("codex: no session to resume")
		}
		a = append(a, "resume")
		a = append(a, images...)
		a = append(a, l.SessionID)
	}
	a = append(a, "-")
	return agentrt.Command{Argv: a}, nil
}

// tomlString is s as a TOML basic string (JSON's escapes are TOML's).
func tomlString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// tomlKey is k as a key of a TOML dotted key: bare when it can be, else
// quoted.
func tomlKey(k string) string {
	bare := k != ""
	for _, r := range k {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			bare = false
			break
		}
	}
	if bare {
		return k
	}
	return tomlString(k)
}

// MCPConfig implements agentrt.Adapter: the tool server is passed on the
// command line, not in a file.
func (adapter) MCPConfig(agentrt.MCPServer) ([]byte, error) { return nil, nil }

// NewCodec implements agentrt.Adapter.
func (adapter) NewCodec(agentrt.Launch) agentrt.Codec { return codec{} }

// ExitOutcome implements agentrt.Adapter: text is the end of stderr, where
// Codex reports errors before a turn starts (e.g. an unknown thread to
// resume).
func (adapter) ExitOutcome(text string) agentrt.Outcome {
	if lostSession(text) {
		return agentrt.Outcome{Kind: agentrt.LostSession, Err: &backend.Error{Kind: backend.ErrFailed, Message: text}}
	}
	return agentrt.Outcome{Kind: agentrt.Failed, Err: &backend.Error{Kind: classify(text), Message: text}}
}

// codec is the protocol of one `codex exec` process.
type codec struct{}

// errNoPersistent: Codex runs one process per turn.
var errNoPersistent = errors.New("codex: no persistent mode")

// Message implements agentrt.Codec; it is never used (Info.PerTurnOnly).
func (codec) Message(string) ([]byte, error) { return nil, errNoPersistent }

// Prompt implements agentrt.Codec: the message is read from stdin ("-").
func (codec) Prompt(text string) []byte { return []byte(text) }

var _ agentrt.AttachmentCodec = codec{}

// MessageWith implements agentrt.AttachmentCodec; it is never used
// (Info.PerTurnOnly).
func (codec) MessageWith(string, []backend.Attachment) ([]byte, error) { return nil, errNoPersistent }

// PromptWith implements agentrt.AttachmentCodec: the text files go inline
// before the message; the images are on the command line (Command).
func (codec) PromptWith(text string, atts []backend.Attachment) ([]byte, error) {
	s, err := backend.InlineText(text, atts)
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}

// Interrupt implements agentrt.Codec: a stopped answer kills the process.
func (codec) Interrupt() []byte { return nil }

// NewDecoder implements agentrt.Codec.
func (codec) NewDecoder(io agentrt.TurnIO) agentrt.Decoder { return newDecoder(io.Emit) }
