package files

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// reservedNames are device names Windows does not allow as file names.
var reservedNames = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true}

func init() {
	for i := 1; i <= 9; i++ {
		reservedNames[fmt.Sprintf("COM%d", i)] = true
		reservedNames[fmt.Sprintf("LPT%d", i)] = true
	}
}

// validName reports whether name can be the name of a new file or folder:
// no path separators or characters Windows forbids, no trailing dot or
// space, no device name.
func validName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 255 {
		return false
	}
	if strings.ContainsAny(name, `<>:"/\|?*`) || strings.ContainsFunc(name, func(r rune) bool { return r < 32 }) {
		return false
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return false
	}
	stem, _, _ := strings.Cut(name, ".")
	return !reservedNames[strings.ToUpper(strings.TrimSpace(stem))]
}

// maxUnique bounds the numbered names tried.
const maxUnique = 10000

// numbered is the n-th alternative of name: "New folder (2)",
// "notes (2).txt".
func numbered(name string, n int, folder bool) string {
	ext := ""
	if !folder {
		ext = filepath.Ext(name)
	}
	return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), n, ext)
}

// create makes a new folder or an empty file called name in dir. When the
// name is taken it adds a number, as Explorer does: "New folder (2)".
func create(dir, name string, folder bool) (string, error) {
	if dir == "" {
		return "", errors.New("no folder")
	}
	for n := 1; n <= maxUnique; n++ {
		cand := name
		if n > 1 {
			cand = numbered(name, n, folder)
		}
		path := filepath.Join(dir, cand)
		var err error
		if folder {
			err = os.Mkdir(path, 0o755)
		} else {
			var f *os.File
			if f, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644); err == nil {
				err = f.Close()
			}
		}
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return path, err
	}
	return "", fmt.Errorf("%s: too many files with this name", name)
}

// zipPaths compresses the files and folders paths into a new ZIP file in
// dir, named after the first of them ("report.zip", "report (2).zip", …),
// and returns its path. Folders are stored with their contents.
func zipPaths(dir string, paths []string) (out string, err error) {
	if dir == "" {
		dir = filepath.Dir(paths[0])
	}
	base := filepath.Base(paths[0])
	if st, err := os.Stat(paths[0]); err == nil && !st.IsDir() {
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	var f *os.File
	for n := 1; n <= maxUnique && f == nil; n++ {
		name := base + ".zip"
		if n > 1 {
			name = numbered(name, n, false)
		}
		out = filepath.Join(dir, name)
		f, err = os.OpenFile(out, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil && !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
	if f == nil {
		return "", fmt.Errorf("%s: too many files with this name", base)
	}
	archive := out
	defer func() {
		if err != nil {
			os.Remove(archive) // no half-written archive
		}
	}()
	zw := zip.NewWriter(f)
	for _, p := range paths {
		if err = addToZip(zw, p, archive); err != nil {
			zw.Close()
			f.Close()
			return "", err
		}
	}
	if err = zw.Close(); err != nil {
		f.Close()
		return "", err
	}
	return out, f.Close()
}

// addToZip adds path (a file, or a folder with its contents) under its base
// name, skipping the archive itself.
func addToZip(zw *zip.Writer, path, archive string) error {
	root := filepath.Dir(path)
	return filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.EqualFold(p, archive) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		if d.IsDir() {
			h.Name += "/"
			_, err = zw.CreateHeader(h)
			return err
		}
		if !info.Mode().IsRegular() {
			return nil // links and devices are left out
		}
		h.Method = zip.Deflate
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(w, src)
		return err
	})
}

// sha256File returns the SHA-256 checksum of a file in lower-case hex.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
