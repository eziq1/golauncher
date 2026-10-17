package update

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/eziq1/golauncher/internal/install"
)

// maxExtracted bounds the size of a file taken out of the archive.
const maxExtracted = 200 << 20

// Staged is a downloaded, verified and extracted update, ready to install.
type Staged struct {
	Version string `json:"version"`
	// Dir holds golauncher.exe and, if the archive had it, glctl.exe.
	Dir string `json:"dir"`
	// Page is the release's web page ("What's new").
	Page string `json:"page,omitempty"`
	// Bundle is set on macOS: Dir holds golauncher.app.
	Bundle bool `json:"bundle,omitempty"`
}

// Exe is the new golauncher.exe, or on macOS the executable of the new
// golauncher.app.
func (s Staged) Exe() string { return programIn(s.Dir, s.Bundle) }

// programIn returns golauncher's executable in dir: golauncher.exe, or
// with bundle that of golauncher.app.
func programIn(dir string, bundle bool) string {
	if bundle {
		return install.BundleExe(install.BundleIn(dir))
	}
	return filepath.Join(dir, install.ExeName)
}

// PrepareOptions configure Prepare.
type PrepareOptions struct {
	// Arch is the GOARCH whose archive is downloaded (default runtime.GOARCH).
	Arch string
	// Dir is the folder the update is downloaded into; Prepare creates it.
	Dir string
	// PublicKey, when set, requires checksums.txt to be signed with it
	// (default: none; the service passes update.PublicKey).
	PublicKey string
	// Progress reports the archive's download.
	Progress Progress
	// KeepArchive leaves the archive next to the extracted files.
	KeepArchive bool
	// Bundle downloads the macOS archive and extracts its golauncher.app.
	Bundle bool
}

// PrepareResult describes what Prepare verified.
type PrepareResult struct {
	Staged   Staged
	Archive  string // the archive's name
	Size     int64
	SHA256   string // hex
	Signed   bool   // the signature of checksums.txt was verified
	Files    []string
	Download string // the archive on disk when KeepArchive
}

// Prepare downloads release's archive for o.Arch into o.Dir, verifies it
// against checksums.txt (and its signature when o.PublicKey is set) and
// extracts golauncher.exe and glctl.exe (on macOS golauncher.app) into
// o.Dir/files. Nothing is installed.
func (c *Client) Prepare(ctx context.Context, r Release, o PrepareOptions) (PrepareResult, error) {
	version, err := r.Version()
	if err != nil {
		return PrepareResult{}, err
	}
	assets, err := SelectAssetsFor(r, o.Arch, o.Bundle)
	if err != nil {
		return PrepareResult{}, err
	}
	sumsData, err := c.fetch(ctx, assets.Checksums.URL, maxChecksums)
	if err != nil {
		return PrepareResult{}, err
	}
	res := PrepareResult{Archive: assets.Archive.Name, Size: assets.Archive.Size}
	if o.PublicKey != "" {
		if assets.Signature == nil {
			return PrepareResult{}, fmt.Errorf("%w: the release has no %s", ErrSignature, SignatureName)
		}
		sig, err := c.fetch(ctx, assets.Signature.URL, maxChecksums)
		if err != nil {
			return PrepareResult{}, err
		}
		if err := VerifySignature(o.PublicKey, sumsData, sig); err != nil {
			return PrepareResult{}, err
		}
		res.Signed = true
	}
	sums, err := ParseChecksums(sumsData)
	if err != nil {
		return PrepareResult{}, err
	}
	want, ok := sums[assets.Archive.Name]
	if !ok {
		return PrepareResult{}, fmt.Errorf("%w: %s does not list %s", ErrChecksum, ChecksumsName, assets.Archive.Name)
	}
	res.SHA256 = fmt.Sprintf("%x", want)

	if err := os.MkdirAll(o.Dir, 0o755); err != nil {
		return PrepareResult{}, err
	}
	archive := filepath.Join(o.Dir, assets.Archive.Name)
	if err := c.download(ctx, assets.Archive.URL, archive, assets.Archive.Size, o.Progress); err != nil {
		return PrepareResult{}, err
	}
	if err := VerifyFile(archive, want); err != nil {
		_ = os.Remove(archive)
		return PrepareResult{}, err
	}
	files := filepath.Join(o.Dir, "files")
	extract := Extract
	if o.Bundle {
		extract = ExtractBundle
	}
	names, err := extract(archive, files)
	if !o.KeepArchive {
		_ = os.Remove(archive)
	} else {
		res.Download = archive
	}
	if err != nil {
		return PrepareResult{}, err
	}
	res.Files = names
	res.Staged = Staged{Version: version, Dir: files, Page: r.Page(), Bundle: o.Bundle}
	return res, nil
}

// Extract takes golauncher.exe and glctl.exe from the root of the zip
// archive into dir (replacing what is there) and returns their names.
// golauncher.exe must be there; other files are ignored.
func Extract(archive, dir string) ([]string, error) {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return nil, fmt.Errorf("update: opening %s: %w", filepath.Base(archive), err)
	}
	defer zr.Close()
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var names []string
	for _, f := range zr.File {
		name := f.Name
		if name != install.ExeName && name != install.CtlName {
			continue // anything else, including paths with folders
		}
		if slices.Contains(names, name) {
			return nil, fmt.Errorf("update: the archive has %s twice", name)
		}
		if f.UncompressedSize64 > maxExtracted {
			return nil, fmt.Errorf("update: %s is too large", name)
		}
		if err := extractFile(f, filepath.Join(dir, name)); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if !slices.Contains(names, install.ExeName) {
		return nil, fmt.Errorf("update: the archive has no %s", install.ExeName)
	}
	return names, nil
}

func extractFile(f *zip.File, dst string) error { return extractFileMode(f, dst, 0o755) }

func extractFileMode(f *zip.File, dst string, perm os.FileMode) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(rc, maxExtracted+1))
	err = errors.Join(err, out.Sync(), out.Close())
	if err == nil && n > maxExtracted {
		err = fmt.Errorf("update: %s is too large", f.Name)
	}
	if err != nil {
		_ = os.Remove(dst)
	}
	return err
}

// maxBundle bounds the size of the extracted golauncher.app.
const maxBundle = 400 << 20

// ExtractBundle takes golauncher.app from the root of the zip archive (the
// macOS release archive) into dir (replacing what is there) and returns the
// names of its files. It must hold its executable and Info.plist. Paths
// that leave the bundle and links are refused, other files ignored; the
// permissions are kept, and the quarantine attribute is cleared, as these
// are files golauncher downloaded and verified itself.
func ExtractBundle(archive, dir string) ([]string, error) {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return nil, fmt.Errorf("update: opening %s: %w", filepath.Base(archive), err)
	}
	defer zr.Close()
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	prefix := install.BundleName + "/"
	var names []string
	var total uint64
	for _, f := range zr.File {
		name := f.Name
		if !strings.HasPrefix(name, prefix) {
			continue // LICENSE, README.md, __MACOSX/...
		}
		if strings.Contains(name, `\`) || !filepath.IsLocal(filepath.FromSlash(strings.TrimSuffix(name, "/"))) {
			return nil, fmt.Errorf("update: the archive has a bad path %q", name)
		}
		dst := filepath.Join(dir, filepath.FromSlash(name))
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return nil, err
			}
			continue
		case !mode.IsRegular():
			return nil, fmt.Errorf("update: the archive has %s, which is not a file", name)
		}
		if slices.Contains(names, name) {
			return nil, fmt.Errorf("update: the archive has %s twice", name)
		}
		total += f.UncompressedSize64
		if f.UncompressedSize64 > maxExtracted || total > maxBundle {
			return nil, fmt.Errorf("update: %s is too large", install.BundleName)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		perm := os.FileMode(0o644)
		if mode.Perm()&0o111 != 0 {
			perm = 0o755
		}
		if err := extractFileMode(f, dst, perm); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	app := install.BundleIn(dir)
	exe := install.BundleExe(app)
	for _, need := range []string{exe, filepath.Join(app, "Contents", "Info.plist")} {
		if fi, err := os.Stat(need); err != nil || !fi.Mode().IsRegular() {
			rel, _ := filepath.Rel(dir, need)
			return nil, fmt.Errorf("update: the archive has no %s", filepath.ToSlash(rel))
		}
	}
	if fi, err := os.Stat(exe); err != nil || fi.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("update: %s in the archive is not executable", install.BundleExeName)
	}
	if err := install.ClearQuarantine(app); err != nil {
		return nil, err
	}
	return names, nil
}
