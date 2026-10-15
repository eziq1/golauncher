// Package system wraps the Windows calls of the builtin.system plugin: lock,
// sleep, shut down, the Recycle Bin, the app theme, media keys and the
// display (docs/system-commands.md).
//
// The functions that only read state (QueryRecycleBin, DarkMode,
// HibernateAvailable, OSBuild, EditionID) are safe to call anywhere. All
// others change the state of the machine and must never run in tests; the
// plugin calls them through an interface that tests replace with a fake.
//
// On other platforms the functions return errors.ErrUnsupported.
package system

import "strings"

// RecycleBin is the content of the Recycle Bins of all drives.
type RecycleBin struct {
	Items int64 // number of files and folders
	Size  int64 // bytes
}

// ExitKind is how ExitWindows ends the session.
type ExitKind int

const (
	Logoff ExitKind = iota
	Reboot
	PowerOff
)

// MediaKey is a volume key sent with PressMediaKey.
type MediaKey int

const (
	VolumeMute MediaKey = iota
	VolumeDown
	VolumeUp
)

// Win11Build is the first build number of Windows 11.
const Win11Build = 22000

// HomeEdition reports whether edition (the EditionID of the registry, e.g.
// "Core", "CoreSingleLanguage", "Professional") is a Home edition, which has
// no Group Policy editor.
func HomeEdition(edition string) bool {
	return strings.HasPrefix(edition, "Core")
}
