package codex

import (
	"testing"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// setCandidateEnv makes Candidates look at e for the rest of the test.
func setCandidateEnv(t *testing.T, e findEnv) {
	t.Helper()
	old := candidateEnv
	candidateEnv = func() findEnv { return e }
	t.Cleanup(func() { candidateEnv = old })
}

// commandFor is the command line of a per-turn process of a conversation
// opened with oo, for a CLI of the given version.
func commandFor(b *Backend, oo backend.OpenOptions, path string, resume bool) []string {
	cmd, err := b.CommandAt(oo, path, "0.162.0", false, resume)
	if err != nil {
		panic(err)
	}
	return cmd.Argv
}
