package clipboard

import "slices"

// windowsIgnoreApps is the default of the ignoreApps preference on Windows:
// the executables of common password managers.
var windowsIgnoreApps = []string{"KeePassXC.exe", "1Password.exe", "Bitwarden.exe", "KeePass.exe"}

// macIgnoreApps is the default on macOS, where the clipboard's source app
// is named by the executable in its .app bundle. Most of them also mark
// their copies concealed, which is never recorded either.
var macIgnoreApps = []string{"KeePassXC", "1Password", "1Password 7", "Bitwarden"}

// passwordManagers are the programs whose clips are always sensitive, as
// Windows and macOS name them.
var passwordManagers = slices.Concat(
	[]string{"KeePassXC.exe", "KeePass.exe", "1Password.exe", "Bitwarden.exe", "LastPass.exe", "Dashlane.exe", "Enpass.exe", "RoboForm.exe"},
	[]string{"KeePassXC", "1Password", "1Password 7", "Bitwarden", "LastPass", "Dashlane", "Enpass", "Strongbox", "MacPass"},
)

// defaultIgnoreApps is the default of the ignoreApps preference.
func (p *Plugin) defaultIgnoreApps() []string {
	if p.mac {
		return macIgnoreApps
	}
	return windowsIgnoreApps
}
