package update

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strings"

	"github.com/eziq1/golauncher/internal/install"
)

// PublicKey is the base64 ed25519 public key that checksums.txt must be
// signed with, set at build time:
//
//	-ldflags "-X github.com/eziq1/golauncher/internal/update.PublicKey=<base64>"
//
// When it is set, a release without a valid checksums.txt.sig is refused;
// when it is empty (the default), only the SHA-256 checksums are checked
// (docs/auto-update.md §4).
var PublicKey = ""

// ArchiveName is the release archive of version for arch (a GOARCH),
// as scripts/windows/build.ps1 names it.
func ArchiveName(version, arch string) string {
	return "golauncher_" + version + "_windows_" + arch + ".zip"
}

// Universal is the "architecture" of a macOS archive whose golauncher.app
// runs on both Apple silicon and Intel Macs (scripts/macos/build.sh --arch
// universal). Releases publish one archive per architecture instead; an
// update falls back to a universal one.
const Universal = "universal"

// BundleArchiveName is the macOS release archive of version for arch
// (Universal, amd64 or arm64), as scripts/macos/build.sh --app --zip names
// it; it holds golauncher.app.
func BundleArchiveName(version, arch string) string {
	return "golauncher_" + version + "_darwin_" + arch + ".zip"
}

// Assets are the files of a release that an update downloads.
type Assets struct {
	Archive   Asset
	Checksums Asset
	Signature *Asset // nil when the release has none
}

// SelectAssets finds the archive of the release for arch, its checksums and
// its signature. The archive and checksums.txt must both be there.
func SelectAssets(r Release, arch string) (Assets, error) {
	return SelectAssetsFor(r, arch, false)
}

// SelectAssetsFor is SelectAssets for the Windows archive, or with bundle
// for the macOS one: the archive of arch, else the universal one.
func SelectAssetsFor(r Release, arch string, bundle bool) (Assets, error) {
	if arch == "" {
		arch = runtime.GOARCH
	}
	v, err := r.Version()
	if err != nil {
		return Assets{}, err
	}
	names := []string{ArchiveName(v, arch)}
	if bundle {
		names = []string{BundleArchiveName(v, arch), BundleArchiveName(v, Universal)}
	}
	var a Assets
	var ok bool
	for _, name := range names {
		if a.Archive, ok = r.Asset(name); ok {
			break
		}
	}
	if !ok {
		return Assets{}, fmt.Errorf("%w (%s)", ErrNoAsset, names[0])
	}
	if a.Checksums, ok = r.Asset(ChecksumsName); !ok {
		return Assets{}, fmt.Errorf("%w (%s)", ErrNoAsset, ChecksumsName)
	}
	if sig, ok := r.Asset(SignatureName); ok {
		a.Signature = &sig
	}
	return a, nil
}

// ParseChecksums reads a sha256sum file: "<hex>  <name>" lines (a "*" before
// the name marks binary mode). It returns the digests by file name.
func ParseChecksums(data []byte) (map[string][]byte, error) {
	sums := map[string][]byte{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		hexSum, name, ok := strings.Cut(line, " ")
		name = strings.TrimPrefix(strings.TrimSpace(name), "*")
		sum, err := hex.DecodeString(hexSum)
		if !ok || name == "" || err != nil || len(sum) != sha256.Size {
			return nil, fmt.Errorf("update: %s line %d is not \"<sha256>  <file>\"", ChecksumsName, n)
		}
		sums[name] = sum
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return sums, nil
}

// VerifySignature checks that sig (base64, as in checksums.txt.sig) is the
// ed25519 signature of data by the base64 public key key.
func VerifySignature(key string, data, sig []byte) error {
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(key))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("update: the public key is not %d bytes of base64", ed25519.PublicKeySize)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return fmt.Errorf("%w: the signature is malformed", ErrSignature)
	}
	if !ed25519.Verify(pub, data, raw) {
		return ErrSignature
	}
	return nil
}

// Sign returns the base64 signature of data with the base64 private key
// seed (32 bytes) or full private key (64 bytes), for the release workflow.
func Sign(key string, data []byte) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(key))
	if err != nil {
		return "", errors.New("update: the signing key is not base64")
	}
	var priv ed25519.PrivateKey
	switch len(raw) {
	case ed25519.SeedSize:
		priv = ed25519.NewKeyFromSeed(raw)
	case ed25519.PrivateKeySize:
		priv = ed25519.PrivateKey(raw)
	default:
		return "", fmt.Errorf("update: the signing key is %d bytes, want %d", len(raw), ed25519.SeedSize)
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data)), nil
}

// PublicKeyOf returns the base64 public key of a base64 private key (seed).
func PublicKeyOf(key string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(key))
	if err != nil || len(raw) != ed25519.SeedSize && len(raw) != ed25519.PrivateKeySize {
		return "", errors.New("update: the signing key is not a base64 ed25519 seed")
	}
	priv := ed25519.NewKeyFromSeed(raw[:ed25519.SeedSize])
	return base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)), nil
}

// GenerateKey returns a new base64 seed and public key, from rand.
func GenerateKey(rand io.Reader) (seed, pub string, err error) {
	p, s, err := ed25519.GenerateKey(rand)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(s.Seed()), base64.StdEncoding.EncodeToString(p), nil
}

// VerifyFile checks that the file at path has the SHA-256 digest want.
func VerifyFile(path string, want []byte) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := h.Sum(nil); !bytes.Equal(got, want) {
		return fmt.Errorf("%w (got %x, want %x)", ErrChecksum, got, want)
	}
	return nil
}

// Updatable reports whether version is a release build that may update
// itself: MAJOR.MINOR.PATCH, perhaps with a pre-release (1.0.0-rc.1), but
// not "dev" or a build between tags (git describe's -N-gHASH, -dirty, a bare
// hash), which are development builds.
func Updatable(version string) bool {
	m := releaseRe.FindStringSubmatch(version)
	if m == nil {
		return false
	}
	pre := m[1]
	return pre != "dirty" && !hashRe.MatchString(pre)
}

var (
	// releaseRe matches a release version; its pre-release has no hyphen,
	// unlike git describe's "-3-gabc1234" suffix.
	releaseRe = regexp.MustCompile(`^v?\d+\.\d+\.\d+(?:-([0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*))?$`)
	// hashRe matches a bare commit hash ("0.0.0-<hash>" before the first tag).
	hashRe = regexp.MustCompile(`^g?[0-9a-f]{7,40}$`)
)

// Newer reports whether latest is a newer version than current.
func Newer(latest, current string) bool {
	order, ok := install.CompareVersions(latest, current)
	return ok && order > 0
}
