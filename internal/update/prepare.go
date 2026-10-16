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
}

// Exe is the new golauncher.exe.
func (s Staged) Exe() string { return filepath.Join(s.Dir, install.ExeName) }

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
// extracts golauncher.exe and glctl.exe into o.Dir/files. Nothing is
// installed.
func (c *Client) Prepare(ctx context.Context, r Release, o PrepareOptions) (PrepareResult, error) {
	version, err := r.Version()
	if err != nil {
		return PrepareResult{}, err
	}
	assets, err := SelectAssets(r, o.Arch)
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
	names, err := Extract(archive, files)
	if !o.KeepArchive {
		_ = os.Remove(archive)
	} else {
		res.Download = archive
	}
	if err != nil {
		return PrepareResult{}, err
	}
	res.Files = names
	res.Staged = Staged{Version: version, Dir: files, Page: r.Page()}
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

func extractFile(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
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
