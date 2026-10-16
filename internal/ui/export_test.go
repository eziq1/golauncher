package ui

// SetDesktop points the Claude Desktop helper at a test file and program.
func SetDesktop(configPath, exe string) func() {
	oldPath, oldExe := desktopConfigPath, executable
	desktopConfigPath = func() (string, error) { return configPath, nil }
	executable = func() (string, error) { return exe, nil }
	return func() { desktopConfigPath, executable = oldPath, oldExe }
}
