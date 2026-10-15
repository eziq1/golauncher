package everything

import "golang.org/x/sys/windows"

// userFolderIDs are the known folders holding the user's own files.
var userFolderIDs = []*windows.KNOWNFOLDERID{
	windows.FOLDERID_Desktop,
	windows.FOLDERID_Documents,
	windows.FOLDERID_Downloads,
	windows.FOLDERID_Pictures,
	windows.FOLDERID_Videos,
	windows.FOLDERID_Music,
	windows.FOLDERID_SkyDrive, // OneDrive
}

// UserFolders returns the current paths of the user's Desktop, Documents,
// Downloads, Pictures, Videos, Music and OneDrive folders, following
// redirection (e.g. Documents moved to D:\ or into OneDrive). Folders that do
// not exist or cannot be resolved are left out.
func UserFolders() []string {
	var out []string
	for _, id := range userFolderIDs {
		if p, err := windows.KnownFolderPath(id, windows.KF_FLAG_DONT_VERIFY); err == nil && p != "" {
			out = append(out, p)
		}
	}
	return out
}
