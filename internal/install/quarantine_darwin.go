package install

import (
	"errors"
	"io/fs"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// QuarantineAttr is the extended attribute with which macOS marks
// downloaded files for Gatekeeper.
const QuarantineAttr = "com.apple.quarantine"

// ClearQuarantine removes the quarantine attribute from root and
// everything under it (links themselves, not their targets). golauncher
// calls it only on files it wrote itself (an update it downloaded and
// verified), never on files the user brought.
func ClearQuarantine(root string) error {
	return filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := unix.Lremovexattr(path, QuarantineAttr); err != nil && !errors.Is(err, unix.ENOATTR) {
			return &fs.PathError{Op: "removexattr", Path: path, Err: err}
		}
		return nil
	})
}
