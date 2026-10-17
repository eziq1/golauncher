//go:build !darwin

package clip

import "sync"

// CopySelection copies the text selected in hwnd, which must be the
// foreground window, with a simulated Ctrl+C, and restores the clipboard as
// it was. Whether the window may receive Ctrl+C at all (terminals, excluded
// or elevated programs, password fields) is for the caller to decide.
//
// Before any key is sent, every clipboard format is saved; when that is not
// possible exactly, ErrCannotSave is returned and nothing is sent. Changes
// to the clipboard during the copy are hidden from Watch in this process,
// and the restored content carries the formats that ask clipboard managers,
// Windows clipboard history and cloud clipboard not to record it again.
// Other clipboard managers may still record the window's own write.
//
// It returns ErrNoSelection when no text was copied, and the text together
// with an error wrapping ErrNotRestored when the clipboard could not be put
// back. Calls are serialized.
func CopySelection(hwnd uintptr, o CopyOptions) (CopyResult, error) {
	a, err := systemCopyAPI()
	if err != nil {
		return CopyResult{}, err
	}
	copyMu.Lock()
	defer copyMu.Unlock()
	return copier{api: a, sup: suppression}.copy(hwnd, o)
}

var copyMu sync.Mutex
