package everything

import "testing"

func TestDarwinTiers(t *testing.T) {
	r := newRules(rulesEnv{
		Home:            "/Users/demo",
		UserFolders:     []string{"/Users/demo/Documents", "/Users/demo/Library/Mobile Documents/com~apple~CloudDocs"},
		SystemDirs:      platformSystemDirs,
		SystemSegments:  platformSystemSegments,
		ProgramSegments: platformProgramSegments,
	})
	for path, want := range map[string]tier{
		"/System/Library/CoreServices/Finder.app":                         tierSystem,
		"/Library/Preferences/x.plist":                                    tierSystem,
		"/usr/local/bin/brew":                                             tierSystem,
		"/Users/demo/Library/Caches/com.apple.Safari/x":                   tierSystem,
		"/Applications/Safari.app/Contents/Info.plist":                    tierSystem,
		"/Users/demo/project/node_modules/x/index.js":                     tierSystem,
		"/Applications/Safari.app":                                        tierProgram,
		"/Users/demo/Library/Application Support/Code/User/settings.json": tierProgram,
		"/Users/demo/.zshrc":                                              tierProgram,
		"/Users/demo/Documents/report.pdf":                                tierUser,
		"/Users/demo/Library/Mobile Documents/com~apple~CloudDocs/a.txt":  tierUser,
		"/Users/demo/src/main.go":                                         tierUser,
		"/Volumes/Backup/photo.jpg":                                       tierNormal,
	} {
		if got, _ := r.tier(path); got != want {
			t.Errorf("tier(%s) = %d, want %d", path, got, want)
		}
	}
}
