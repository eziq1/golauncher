package claudecode

import (
	"fmt"
	"strings"

	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// Version compatibility (docs/ai.md §22). Every flag of the command line is
// one of three kinds:
//
//   - essential: the CLI cannot be driven without it (-p, --output-format
//     stream-json, --verbose, --session-id, --resume); without one the CLI
//     is refused as too old;
//   - isolation: it keeps the CLI away from the user's own setup and from
//     the computer; without it an older equivalent is used, or the feature
//     that needs it is turned off, or the CLI is refused, never run with
//     less isolation;
//   - optional: without it the experience degrades (no streaming, per-turn
//     mode, the system prompt in the first message, the default model).
//
// Which flags the CLI has comes from `claude --help` (agentrt's probe);
// when that fails, from the versions in introducedIn; otherwise every flag
// is assumed (the tested set).

// The flags, by their long name.
const (
	flagPrint             = "--print"
	flagOutputFormat      = "--output-format"
	flagVerbose           = "--verbose"
	flagSessionID         = "--session-id"
	flagResume            = "--resume"
	flagInputFormat       = "--input-format"
	flagPartial           = "--include-partial-messages"
	flagSettingSources    = "--setting-sources"
	flagSafeMode          = "--safe-mode"
	flagStrictMCP         = "--strict-mcp-config"
	flagMCPConfig         = "--mcp-config"
	flagNoSlashCommands   = "--disable-slash-commands"
	flagTools             = "--tools"
	flagAllowedTools      = "--allowedTools"
	flagPermissionPrompts = "--permission-prompts"
	flagPermissionMode    = "--permission-mode"
	flagAppendSystem      = "--append-system-prompt"
	flagModel             = "--model"
)

// markDontAsk is the marker parseHelp adds when --permission-mode offers
// "dontAsk" (deny whatever is not pre-approved).
const markDontAsk = "--permission-mode=dontAsk"

// essentialFlags are the flags without which the CLI is refused.
var essentialFlags = []string{flagPrint, flagOutputFormat, flagVerbose, flagSessionID, flagResume}

// introducedIn are the versions that added the flags newer than MinVersion,
// from Claude Code's CHANGELOG; used when the probe fails. The other flags
// predate MinVersion (2.1.0).
var introducedIn = map[string]string{
	flagSafeMode:          "2.1.169",
	flagPermissionPrompts: "2.1.259",
}

// parseHelp is Info.ParseHelp: the long options of `claude --help`, plus
// markDontAsk. A help text without --print is not Claude Code's (nil).
func parseHelp(out string) agentrt.Flags {
	f := agentrt.ParseHelpFlags(out)
	if !f.Has(flagPrint) {
		return nil
	}
	if strings.Contains(out, `"dontAsk"`) {
		f[markDontAsk] = true
	}
	return f
}

// support is what a CLI of a version, with probed flags (nil: unknown),
// can do.
type support struct {
	version string
	flags   agentrt.Flags
}

// has reports whether the CLI has flag: as probed; else by the version
// that introduced it; else assumed (the tested set). Markers are only
// known from a probe.
func (s support) has(flag string) bool {
	if s.flags != nil {
		return s.flags.Has(flag)
	}
	if strings.Contains(flag, "=") {
		return false
	}
	if v, ok := introducedIn[flag]; ok && s.version != "" {
		return agentrt.CompareVersions(s.version, v) >= 0
	}
	return true
}

// plan is how a CLI is run, decided by decide.
type plan struct {
	refused error // the CLI cannot be used

	persistent bool // --input-format stream-json
	partial    bool // --include-partial-messages
	safeMode   bool // --safe-mode (when there is no MCP server)
	strictMCP  bool
	slash      bool // --disable-slash-commands
	launcher   bool // golauncher's MCP server can be passed
	web        bool // the web tools can be pre-approved
	prompts    bool // --permission-prompts none
	dontAsk    bool // --permission-mode dontAsk instead
	appendSP   bool // --append-system-prompt (else in the first message)
	model      bool

	reduced []string // backend.Feature* codes
	dropped []string // for the log
}

// decide returns the plan for a CLI.
func decide(s support) plan {
	var p plan
	var missing []string
	for _, f := range essentialFlags {
		if !s.has(f) {
			missing = append(missing, f)
		}
	}
	// Isolation flags without a safe equivalent: --setting-sources "" (the
	// user's settings, hooks and plugins) and --tools "" (a deny list cannot
	// be complete, and read-only tools run without asking).
	for _, f := range []string{flagSettingSources, flagTools} {
		if !s.has(f) {
			missing = append(missing, f)
		}
	}
	p.safeMode = s.has(flagSafeMode)
	p.strictMCP = s.has(flagStrictMCP)
	if !p.strictMCP && !p.safeMode {
		// Nothing else keeps the user's own MCP servers out.
		missing = append(missing, flagStrictMCP)
	}
	if len(missing) > 0 {
		v := s.version
		if v == "" {
			v = "this version"
		}
		p.refused = &backend.Error{Kind: backend.ErrVersion, Version: s.version,
			Message: fmt.Sprintf("Claude Code %s lacks %s, which golauncher needs.", v, strings.Join(missing, ", "))}
		return p
	}

	p.persistent = s.has(flagInputFormat)
	if !p.persistent {
		p.dropped = append(p.dropped, flagInputFormat+" (one process per turn)")
	}
	p.partial = s.has(flagPartial)
	if !p.partial {
		p.reduced = append(p.reduced, backend.FeatureStreaming)
		p.dropped = append(p.dropped, flagPartial+" (answers arrive whole)")
	}
	if !p.safeMode {
		p.dropped = append(p.dropped, flagSafeMode+" → CLAUDE_CODE_DISABLE_AUTO_MEMORY=1 (as with launcher tools)")
	}
	p.slash = s.has(flagNoSlashCommands)
	if !p.slash {
		// Messages that start with "/" get a space (promptText), and the
		// Skill tool is not among --tools.
		p.dropped = append(p.dropped, flagNoSlashCommands+" (no Skill tool; \"/\" messages get a space)")
	}
	p.web = s.has(flagAllowedTools)
	p.launcher = p.web && p.strictMCP && s.has(flagMCPConfig)
	if !p.web {
		// Nothing can be pre-approved: the web tools are not offered.
		p.reduced = append(p.reduced, backend.FeatureWebSearch)
		p.dropped = append(p.dropped, flagAllowedTools+" (no web tools)")
	}
	if !p.launcher {
		p.reduced = append(p.reduced, backend.FeatureLauncherTools)
		p.dropped = append(p.dropped, "golauncher's MCP server (needs "+flagMCPConfig+", "+flagStrictMCP+", "+flagAllowedTools+")")
	}
	p.prompts = s.has(flagPermissionPrompts)
	if !p.prompts {
		// In -p mode without a permission prompt tool, a tool that is not
		// pre-approved is denied anyway; the model only has pre-approved
		// tools (--tools, --allowedTools). dontAsk says so explicitly.
		p.dontAsk = s.has(markDontAsk)
		if p.dontAsk {
			p.dropped = append(p.dropped, flagPermissionPrompts+" → "+flagPermissionMode+" dontAsk")
		} else {
			p.dropped = append(p.dropped, flagPermissionPrompts+" (-p denies what is not pre-approved)")
		}
	}
	p.appendSP = s.has(flagAppendSystem)
	if !p.appendSP {
		p.dropped = append(p.dropped, flagAppendSystem+" (in the first message)")
	}
	p.model = s.has(flagModel)
	if !p.model {
		p.reduced = append(p.reduced, backend.FeatureModel)
		p.dropped = append(p.dropped, flagModel+" (the CLI's default model)")
	}
	return p
}

// fit is Info.Fit.
func fit(version string, flags agentrt.Flags) (agentrt.Fit, error) {
	p := decide(support{version: version, flags: flags})
	if p.refused != nil {
		return agentrt.Fit{}, p.refused
	}
	return agentrt.Fit{PerTurn: !p.persistent, Reduced: p.reduced, Dropped: p.dropped}, nil
}

// unknownOption returns the flag of the CLI's "error: unknown option
// '--x'" (commander), "" when text is not that.
func unknownOption(text string) string {
	const marker = "unknown option '"
	i := strings.Index(text, marker)
	if i < 0 {
		return ""
	}
	rest := text[i+len(marker):]
	j := strings.IndexByte(rest, '\'')
	if j <= 0 {
		return ""
	}
	return rest[:j]
}
