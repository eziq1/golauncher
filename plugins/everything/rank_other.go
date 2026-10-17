//go:build !darwin

package everything

// The platform's path tiers: Windows' come from the environment
// (SystemRoot, ProgramData) and the common lists.
var platformSystemDirs, platformSystemSegments, platformProgramSegments []string
