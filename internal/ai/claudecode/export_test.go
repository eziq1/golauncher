package claudecode

import (
	"path/filepath"
	"slices"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// commandFor is the command line and the variables added to the environment
// of a process of a conversation opened with oo.
func commandFor(b *Backend, oo backend.OpenOptions, path string, persistent, resume bool) (argv, extraEnv []string) {
	c := &conversation{b: b, oo: oo}
	mcpConfig := ""
	if c.launcherTools() {
		mcpConfig = filepath.Join(b.o.Dir, toolConfigName(oo.SessionID))
	}
	base := b.env()
	for _, kv := range c.env() {
		if !slices.Contains(base, kv) {
			extraEnv = append(extraEnv, kv)
		}
	}
	return c.argv(path, persistent, resume, mcpConfig), extraEnv
}
