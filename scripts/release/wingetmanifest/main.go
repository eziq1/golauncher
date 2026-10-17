// Command wingetmanifest writes the winget manifests of a golauncher release
// (docs/winget-packaging.md): the multi-file manifest of the package
// eziq1.golauncher for microsoft/winget-pkgs, a zip holding a portable
// golauncher.exe and glctl.exe for x64 and arm64.
//
//	go run ./scripts/release/wingetmanifest -version 0.8.0 -dist dist -date 2026-10-17
//	go run ./scripts/release/wingetmanifest -version 0.8.0 -download
//
// -dist reads golauncher_<version>_windows_{amd64,arm64}.zip and
// checksums.txt from a folder (the release workflow's dist); -download
// fetches them, and the release date, from the GitHub release. Either way
// each zip is hashed, checked against checksums.txt and checked to hold the
// two programs at its root. The manifests go to
// <out>/e/eziq1/golauncher/<version>/ (out defaults to
// packaging/winget/manifests), the layout of winget-pkgs.
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
	"time"
)

// The package.
const (
	PackageID       = "eziq1.golauncher"
	Repo            = "eziq1/golauncher"
	ManifestVersion = "1.12.0"
	// MinimumOSVersion is Windows 10 1809, the oldest Windows that winget
	// itself (App Installer) runs on; golauncher needs Windows 10 or 11.
	MinimumOSVersion = "10.0.17763.0"
)

// Installer is one architecture's zip.
type Installer struct {
	Arch   string // winget's: x64, arm64
	GOARCH string // the zip's: amd64, arm64
	URL    string
	SHA256 string // lowercase hex
}

// Release is what the manifests describe.
type Release struct {
	Version    string
	Date       string // YYYY-MM-DD
	Installers []Installer
}

// arches are the Windows zips of a release, in manifest order.
var arches = []struct{ winget, goarch string }{{"x64", "amd64"}, {"arm64", "arm64"}}

var versionRE = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func main() {
	version := flag.String("version", "", "the release's version, without v (0.8.0)")
	dist := flag.String("dist", "", "a folder with the Windows zips and checksums.txt")
	download := flag.Bool("download", false, "download the zips and checksums.txt from the GitHub release")
	date := flag.String("date", "", "the release date, YYYY-MM-DD (default: the GitHub release's, or today with -dist)")
	out := flag.String("out", filepath.Join("packaging", "winget", "manifests"), "the manifests root folder")
	flag.Parse()
	if *version == "" || (*dist == "") == !*download || flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: wingetmanifest -version V (-dist DIR | -download) [-date YYYY-MM-DD] [-out DIR]")
		os.Exit(2)
	}
	dir, err := run(context.Background(), *version, *dist, *download, *date, *out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wingetmanifest:", err)
		os.Exit(1)
	}
	fmt.Println(dir)
}

func run(ctx context.Context, version, dist string, download bool, date, out string) (string, error) {
	version = strings.TrimPrefix(version, "v")
	if !versionRE.MatchString(version) {
		// winget orders 0.9.0-rc.1 after 0.9.0: only stable releases.
		return "", fmt.Errorf("version %q is not MAJOR.MINOR.PATCH (pre-releases are not published to winget)", version)
	}
	if download {
		tmp, err := os.MkdirTemp("", "wingetmanifest-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(tmp)
		published, err := fetchRelease(ctx, http.DefaultClient, "https://api.github.com", version, tmp)
		if err != nil {
			return "", err
		}
		if date == "" {
			date = published.UTC().Format(time.DateOnly)
		}
		dist = tmp
	}
	if date == "" {
		date = time.Now().UTC().Format(time.DateOnly)
	}
	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return "", fmt.Errorf("date %q is not YYYY-MM-DD", date)
	}
	rel, err := readDist(dist, version)
	if err != nil {
		return "", err
	}
	rel.Date = date
	return Write(out, rel)
}

// ZipName is the name of a Windows release zip.
func ZipName(version, goarch string) string {
	return fmt.Sprintf("golauncher_%s_windows_%s.zip", version, goarch)
}

// DownloadURL is where a release asset is downloaded from.
func DownloadURL(version, name string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", Repo, version, name)
}

// readDist hashes the zips in dir and checks them against checksums.txt.
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
		if err := checkZip(path); err != nil {
			return Release{}, err
		}
		rel.Installers = append(rel.Installers, Installer{Arch: a.winget, GOARCH: a.goarch, URL: DownloadURL(version, name), SHA256: sum})
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

// checkZip checks that the programs the manifest names are at the zip's
// root.
func checkZip(path string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close()
	need := map[string]bool{"golauncher.exe": false, "glctl.exe": false}
	for _, f := range zr.File {
		if _, ok := need[f.Name]; ok {
			need[f.Name] = true
		}
	}
	for name, found := range need {
		if !found {
			return fmt.Errorf("%s: no %s at the root", filepath.Base(path), name)
		}
	}
	return nil
}

// fetchRelease downloads the Windows zips and checksums.txt of a release
// into dir and returns when it was published.
func fetchRelease(ctx context.Context, c *http.Client, api, version, dir string) (time.Time, error) {
	var rel struct {
		PublishedAt time.Time `json:"published_at"`
		Assets      []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := get(ctx, c, fmt.Sprintf("%s/repos/%s/releases/tags/v%s", api, Repo, version), func(r io.Reader) error {
		return json.NewDecoder(r).Decode(&rel)
	}); err != nil {
		return time.Time{}, err
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
			return time.Time{}, err
		}
	}
	for name := range want {
		return time.Time{}, fmt.Errorf("release v%s has no asset %s", version, name)
	}
	return rel.PublishedAt, nil
}

func get(ctx context.Context, c *http.Client, url string, read func(io.Reader) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "golauncher-wingetmanifest")
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

// Write writes the manifests of rel under root and returns their folder.
func Write(root string, rel Release) (string, error) {
	dir := ManifestDir(root, rel.Version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for name, data := range Manifests(rel) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// ManifestDir is the folder of a version's manifests, as in winget-pkgs:
// manifests/<first letter>/<publisher>/<name>/<version>.
func ManifestDir(root, version string) string {
	return filepath.Join(root, "e", "eziq1", "golauncher", version)
}
