// Package install implements internal/install.System on Windows: the
// uninstall entry in the registry, .lnk shortcuts through IShellLinkW,
// version resources, waiting for running instances, starting programs and
// deleting the program files after the uninstaller exits.
//
// On macOS it handles golauncher.app instead: an install record and the
// bundles in /Applications and ~/Applications, the version in Info.plist,
// processes through sysctl, starting through Launch Services (open), the
// login item's launch agent fallback and the proxy of System Settings
// (docs/self-install.md §9). Elsewhere every operation fails with
// errors.ErrUnsupported and the shell skips self-installation.
package install
