package agentrt

import "path/filepath"

// UnixBinDirs are the folders where agent CLIs are usually installed on
// macOS (and Linux), searched after PATH, for the home folder home: Homebrew
// (Apple silicon, then Intel), ~/.local/bin, the usual custom npm prefix
// ~/.npm-global and nvm's Node.js versions, where npm links the launchers of
// the packages it installs globally. PATH is the login shell's (the shell
// sets it for a GUI launch, docs/macos-port.md §5.3); these cover a shell
// that could not be asked or that sets up nvm only for interactive use.
func UnixBinDirs(home string) []string {
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"), filepath.Join(home, ".npm-global", "bin"))
		nvm, _ := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin"))
		dirs = append(dirs, nvm...)
	}
	return dirs
}
