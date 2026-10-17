// Command brewcask writes the Homebrew cask of a golauncher release
// (docs/homebrew-packaging.md): Casks/golauncher.rb of the tap eziq1/tap
// (the repository github.com/eziq1/homebrew-tap), which installs
// golauncher.app from the release's macOS zip for Apple silicon or Intel
// and links glctl, the CLI inside the bundle.
//
//	go run ./scripts/release/brewcask -version 0.8.0 -dist dist
//	go run ./scripts/release/brewcask -version 0.8.0 -download
//
// -dist reads golauncher_<version>_darwin_{arm64,amd64}.zip and
// checksums.txt from a folder (the release workflow's dist); -download
// fetches them from the GitHub release. Either way each zip is hashed,
// checked against checksums.txt and checked to hold golauncher.app with
// both programs, of this version; the cask's minimum macOS is the bundle's
// LSMinimumSystemVersion. The cask goes to <out>/golauncher.rb (out
// defaults to packaging/homebrew/Casks).
package main

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/eziq1/golauncher/internal/install"
)

// Repo is golauncher's repository.
const Repo = "eziq1/golauncher"

// Arch is one architecture's zip.
type Arch struct {
	Brew   string // Homebrew's on_<brew> block: arm, intel
	GOARCH string // the zip's: arm64, amd64
	SHA256 string // lowercase hex
}

// Release is what the cask describes.
type Release struct {
	Version string
	// MacOS is the oldest macOS, as Homebrew names it (ventura).
	MacOS  string
	Arches []Arch
}

// arches are the macOS zips of a release, in cask order.
var arches = []struct{ brew, goarch string }{{"arm", "arm64"}, {"intel", "amd64"}}

var versionRE = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func main() {
	version := flag.String("version", "", "the release's version, without v (0.8.0)")
	dist := flag.String("dist", "", "a folder with the macOS zips and checksums.txt")
	download := flag.Bool("download", false, "download the zips and checksums.txt from the GitHub release")
	out := flag.String("out", filepath.Join("packaging", "homebrew", "Casks"), "the folder the cask is written to (a tap's Casks)")
	flag.Parse()
	if *version == "" || (*dist == "") == !*download || flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: brewcask -version V (-dist DIR | -download) [-out DIR]")
		os.Exit(2)
	}
	path, err := run(context.Background(), *version, *dist, *download, *out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "brewcask:", err)
		os.Exit(1)
	}
	fmt.Println(path)
}

func run(ctx context.Context, version, dist string, download bool, out string) (string, error) {
	version = strings.TrimPrefix(version, "v")
	if !versionRE.MatchString(version) {
		// livecheck and brew upgrade follow the latest stable release.
		return "", fmt.Errorf("version %q is not MAJOR.MINOR.PATCH (pre-releases are not published to Homebrew)", version)
	}
	if download {
		tmp, err := os.MkdirTemp("", "brewcask-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(tmp)
		if err := fetchRelease(ctx, http.DefaultClient, "https://api.github.com", version, tmp); err != nil {
			return "", err
		}
		dist = tmp
	}
	rel, err := readDist(dist, version)
	if err != nil {
		return "", err
	}
	return Write(out, rel)
}

// ZipName is the name of a macOS release zip.
func ZipName(version, goarch string) string {
	return fmt.Sprintf("golauncher_%s_darwin_%s.zip", version, goarch)
}

// DownloadURL is where a release asset is downloaded from.
func DownloadURL(version, name string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", Repo, version, name)
}

// readDist hashes the zips in dir and checks them against checksums.txt
// and their bundles.
func readDist(dir, version string) (Release, error) {
	sums, err := readChecksums(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		return Release{}, err
	}
	rel := Release{Version: version}
	for _, a := range arches {
		name := ZipName(version, a.goarch)
		path := filepath.Join(dir, name)
		sum, err := hashFile(path)
		if err != nil {
			return Release{}, err
		}
		want, ok := sums[name]
		switch {
		case !ok:
			return Release{}, fmt.Errorf("checksums.txt has no line for %s", name)
		case want != sum:
			return Release{}, fmt.Errorf("%s: SHA-256 %s, checksums.txt says %s", name, sum, want)
		}
		macos, err := checkZip(path, version)
		if err != nil {
			return Release{}, err
		}
		if rel.MacOS != "" && rel.MacOS != macos {
			return Release{}, fmt.Errorf("%s needs macOS %s, the other zip %s", name, macos, rel.MacOS)
		}
		rel.MacOS = macos
		rel.Arches = append(rel.Arches, Arch{Brew: a.brew, GOARCH: a.goarch, SHA256: sum})
	}
	return rel, nil
}

// readChecksums reads "<sha256>  <name>" lines.
func readChecksums(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sums := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 {
			sums[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
		}
	}
	return sums, sc.Err()
}

func hashFile(path string) (string, error) {
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

// The paths in a zip the cask relies on: the app at the root, its
// programs (binary links glctl) and its Info.plist.
var (
	zipApp   = install.BundleName + "/"
	zipPlist = install.BundleName + "/Contents/Info.plist"
	zipExes  = []string{
		install.BundleName + "/Contents/MacOS/" + install.BundleExeName,
		install.BundleName + "/Contents/MacOS/" + install.BundleCtlName,
	}
)

// checkZip checks that golauncher.app is at the zip's root with both
// programs and is version, and returns its minimum macOS by Homebrew's
// name.
func checkZip(path, version string) (macos string, err error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	name := filepath.Base(path)
	found := map[string]bool{}
	var plist []byte
	for _, f := range zr.File {
		found[f.Name] = true
		if strings.HasPrefix(f.Name, zipApp) && !f.FileInfo().IsDir() {
			found[zipApp] = true
		}
		if f.Name == zipPlist {
			r, err := f.Open()
			if err != nil {
				return "", err
			}
			plist, err = io.ReadAll(r)
			r.Close()
			if err != nil {
				return "", err
			}
		}
	}
	for _, p := range append([]string{zipApp, zipPlist}, zipExes...) {
		if !found[p] {
			return "", fmt.Errorf("%s: no %s", name, p)
		}
	}
	info, err := install.PlistStrings(plist)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	if v := install.PlistVersion(info); v != version {
		return "", fmt.Errorf("%s: golauncher.app is version %q, not %s", name, v, version)
	}
	if id := info["CFBundleIdentifier"]; id != install.BundleID {
		return "", fmt.Errorf("%s: golauncher.app is %q, not %s", name, id, install.BundleID)
	}
	minOS := info["LSMinimumSystemVersion"]
	macos, ok := MacOSName(minOS)
	if !ok {
		return "", fmt.Errorf("%s: LSMinimumSystemVersion %q has no Homebrew name", name, minOS)
	}
	return macos, nil
}

// macOSNames are Homebrew's names of macOS versions (MacOSVersion::SYMBOLS).
var macOSNames = map[string]string{
	"11": "big_sur", "12": "monterey", "13": "ventura", "14": "sonoma", "15": "sequoia", "26": "tahoe",
}

// MacOSName returns Homebrew's name of a macOS version ("13.0": ventura).
// Homebrew's depends_on macos compares major versions, so the minor one
// must be 0.
func MacOSName(v string) (string, bool) {
	major, minor, _ := strings.Cut(v, ".")
	if minor != "" && strings.Trim(minor, "0.") != "" {
		return "", false
	}
	name, ok := macOSNames[major]
	return name, ok
}

// fetchRelease downloads the macOS zips and checksums.txt of a release
// into dir. A pre-release is refused.
func fetchRelease(ctx context.Context, c *http.Client, api, version, dir string) error {
	var rel struct {
		Draft      bool `json:"draft"`
		Prerelease bool `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := get(ctx, c, fmt.Sprintf("%s/repos/%s/releases/tags/v%s", api, Repo, version), func(r io.Reader) error {
		return json.NewDecoder(r).Decode(&rel)
	}); err != nil {
		return err
	}
	if rel.Draft || rel.Prerelease {
		return fmt.Errorf("release v%s is a draft or a pre-release", version)
	}
	want := map[string]bool{"checksums.txt": true}
	for _, a := range arches {
		want[ZipName(version, a.goarch)] = true
	}
	for _, a := range rel.Assets {
		if !want[a.Name] {
			continue
		}
		delete(want, a.Name)
		err := get(ctx, c, a.URL, func(r io.Reader) error {
			f, err := os.Create(filepath.Join(dir, a.Name))
			if err != nil {
				return err
			}
			_, err = io.Copy(f, r)
			return errors.Join(err, f.Close())
		})
		if err != nil {
			return err
		}
	}
	for name := range want {
		return fmt.Errorf("release v%s has no asset %s", version, name)
	}
	return nil
}

func get(ctx context.Context, c *http.Client, url string, read func(io.Reader) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "golauncher-brewcask")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return read(resp.Body)
}

// Write writes the cask of rel into dir and returns its path.
func Write(dir string, rel Release) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, CaskFile)
	return path, os.WriteFile(path, []byte(Cask(rel)), 0o644)
}
