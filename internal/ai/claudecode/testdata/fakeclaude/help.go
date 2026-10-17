package main

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// defaultHelp is the --help of the fake without a script's: commander's
// layout, with every flag the backend uses (the tested set).
const defaultHelp = `Usage: claude [options] [command] [prompt]

Options:
  --allowedTools, --allowed-tools <tools...>
      Comma or space-separated list of tool names to allow
  --append-system-prompt <prompt>       Append a system prompt to the default
                                        system prompt
  --disable-slash-commands              Disable all skills
  -h, --help                            Display help for command
  --include-partial-messages            Include partial message chunks as they
                                        arrive
  --input-format <format>               Input format (only works with --print)
  --mcp-config <configs...>             Load MCP servers from JSON files or
                                        strings (space-separated)
  --model <model>                       Model for the current session
  --output-format <format>              Output format (only works with --print)
  --permission-mode <mode>              Permission mode to use for the session
                                        (choices: "acceptEdits", "default",
                                        "dontAsk", "plan")
  --permission-prompts <target>         Who answers permission prompts
  -p, --print                           Print response and exit
  -r, --resume [value]                  Resume a conversation by session ID
  --safe-mode                           Start with all customizations disabled
  --session-id <uuid>                   Use a specific session ID
  --setting-sources <sources>           Comma-separated list of setting sources
  --strict-mcp-config                   Only use MCP servers from --mcp-config
  --tools <tools...>                    Specify the list of available tools
  --verbose                             Override verbose mode setting
  -v, --version                         Output the version number
`

// helpText is the fake's --help: the script's helpFile, else defaultHelp.
func helpText() string {
	if sc.HelpFile != "" {
		b, err := os.ReadFile(sc.HelpFile)
		if err != nil {
			fail("fake: " + err.Error())
		}
		return string(b)
	}
	return defaultHelp
}

// knownFlags are the long options of helpText, read like commander would
// list them: lines indented by two spaces.
func knownFlags() map[string]bool {
	known := map[string]bool{}
	for line := range strings.Lines(helpText()) {
		if !strings.HasPrefix(line, "  -") {
			continue
		}
		spec := strings.TrimSpace(line)
		if i := strings.Index(spec, "  "); i >= 0 {
			spec = spec[:i]
		}
		for _, tok := range strings.FieldsFunc(spec, func(r rune) bool { return r == ' ' || r == ',' }) {
			if strings.HasPrefix(tok, "--") {
				known[tok] = true
			}
		}
	}
	return known
}

// rejectUnknown exits like commander when args have a long option that
// helpText does not list.
func rejectUnknown(args []string) {
	known := knownFlags()
	for _, a := range args {
		name, _, _ := strings.Cut(a, "=")
		if strings.HasPrefix(name, "--") && (!known[name] || slices.Contains(sc.Rejects, name)) {
			fmt.Fprintf(os.Stderr, "error: unknown option '%s'\n", name)
			os.Exit(1)
		}
	}
}
