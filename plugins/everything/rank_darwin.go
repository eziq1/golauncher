package everything

// The path tiers of macOS (docs/everything.md §2.2), added to the common
// ones by defaultEnv. A user folder (Desktop, Documents, iCloud Drive …)
// still ranks as the user's even inside ~/Library.
var (
	// platformSystemDirs hold the system, and everything installed for all
	// users outside /Applications.
	platformSystemDirs = []string{"/System", "/Library", "/private", "/usr", "/bin", "/sbin", "/opt", "/cores"}
	// platformSystemSegments mark caches, sandbox containers, the Trash and
	// the inside of bundles (Contents\Resources, …; a segment cannot match
	// the end of a name such as ".app").
	platformSystemSegments = []string{`Library\Caches`, `Library\Containers`, `Library\Group Containers`,
		`.Trash`, `Library\Developer`, `Contents\MacOS`, `Contents\Resources`, `Contents\Frameworks`,
		`Contents\PlugIns`, `Contents\Library`, `Contents\Info.plist`}
	// platformProgramSegments mark applications and their data.
	platformProgramSegments = []string{`Applications`, `Library`}
)
