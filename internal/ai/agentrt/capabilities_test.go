package agentrt

import (
	"slices"
	"testing"
)

func TestParseHelpFlags(t *testing.T) {
	for _, tt := range []struct {
		name string
		help string
		want []string
	}{
		{"commander", `Usage: claude [options]

Options:
  --allowedTools, --allowed-tools <tools...>
      Comma or space-separated list of tool names to allow
  -p, --print                           Print response and exit. Not
                                        --described-flag here
  -r, --resume [value]                  Resume a conversation
  --bare                                Minimal mode. Explicitly provide
                                        --system-prompt[-file], --add-dir
`, []string{"--allowed-tools", "--allowedTools", "--bare", "--print", "--resume"}},
		{"clap", `Usage: codex exec [OPTIONS] [PROMPT]

Options:
  -c, --config <key=value>
          Override a configuration value
      --oss
          Use an open source model
  -m, --model <MODEL>
          Model the agent should use
`, []string{"--config", "--model", "--oss"}},
		{"yargs", `Options:
  -m, --model                  Model                              [string]
      --acp                    Starts the agent in ACP mode      [boolean]
`, []string{"--acp", "--model"}},
		{"none", "no options here\n", nil},
	} {
		got := ParseHelpFlags(tt.help)
		if tt.want == nil {
			if got != nil {
				t.Errorf("%s: %v, want nil", tt.name, got.List())
			}
			continue
		}
		if !slices.Equal(got.List(), tt.want) {
			t.Errorf("%s: %v, want %v", tt.name, got.List(), tt.want)
		}
	}
	if f := FlagsOf("--a", "--b"); !f.Has("--a") || f.Has("--c") || Flags(nil).Has("--a") {
		t.Error("Flags.Has")
	}
}
