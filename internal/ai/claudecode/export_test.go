package claudecode

import (
	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// The runtime's helpers under the names the tests used before the agent
// runtime was split off (R71).
var (
	parseVersion    = agentrt.ParseVersion
	compareVersions = agentrt.CompareVersions
	toolConfigName  = agentrt.MCPConfigName
)

// liveCount is the number of conversations with a persistent process.
func (b *Backend) liveCount() int { return b.LiveCount() }

// env is the CLI's environment.
func (b *Backend) env() []string { return b.Env() }

// commandFor is the command line and the variables added to the environment
// of a process of a conversation opened with oo.
func commandFor(b *Backend, oo backend.OpenOptions, path string, persistent, resume bool) (argv, extraEnv []string) {
	cmd, err := b.Command(oo, path, persistent, resume)
	if err != nil {
		panic(err)
	}
	return cmd.Argv, cmd.Env
}

// commandWith is commandFor for a CLI of version with the probed flags (nil:
// unknown).
func commandWith(b *Backend, oo backend.OpenOptions, path, version string, flags agentrt.Flags, persistent, resume bool) (argv, extraEnv []string, err error) {
	cmd, err := b.CommandFor(oo, path, version, flags, persistent, resume)
	return cmd.Argv, cmd.Env, err
}

// commandAtts is commandWith for a per-turn process whose message has the
// attachments atts.
func commandAtts(b *Backend, oo backend.OpenOptions, path, version string, flags agentrt.Flags, resume bool, atts []backend.Attachment) (argv, extraEnv []string, err error) {
	cmd, err := b.CommandWith(oo, path, version, flags, false, resume, atts)
	return cmd.Argv, cmd.Env, err
}
