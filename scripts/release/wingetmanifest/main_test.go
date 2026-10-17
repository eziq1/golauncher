package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The v0.8.0 release, as committed in packaging/winget.
var v080 = Release{
	Version: "0.8.0",
	Date:    "2026-10-17",
	Installers: []Installer{
		{Arch: "x64", GOARCH: "amd64", URL: DownloadURL("0.8.0", ZipName("0.8.0", "amd64")), SHA256: "c36f1031004f039df2f5bfc832a195d78bffcf63c8428eecec0c9e626be7257d"},
		{Arch: "arm64", GOARCH: "arm64", URL: DownloadURL("0.8.0", ZipName("0.8.0", "arm64")), SHA256: "00e5f83ea7ecac87d443e1e86160fec9c292b6bcd06cdd3962f9f3c93b8f819c"},
	},
}

// TestCommittedManifests keeps the committed example in step with the
// generator: rerun it (-download) after changing the text.
func TestCommittedManifests(t *testing.T) {
	dir := ManifestDir(filepath.Join("..", "..", "..", "packaging", "winget", "manifests"), "0.8.0")
	files := Manifests(v080)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(files) {
		t.Errorf("%d files committed, %d generated", len(entries), len(files))
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Error(err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s differs from the generator's:\n%s", name, want)
		}
	}
}

func TestManifestContent(t *testing.T) {
	files := Manifests(v080)
	inst := files["eziq1.golauncher.installer.yaml"]
	for _, want := range []string{
		"InstallerType: zip\nNestedInstallerType: portable\n",
		"- RelativeFilePath: golauncher.exe\n  PortableCommandAlias: golauncher\n",
		"- RelativeFilePath: glctl.exe\n  PortableCommandAlias: glctl\n",
		"- Architecture: x64\n  InstallerUrl: https://github.com/eziq1/golauncher/releases/download/v0.8.0/golauncher_0.8.0_windows_amd64.zip\n  InstallerSha256: C36F1031",
		"- Architecture: arm64\n",
		"ReleaseDate: 2026-10-17\n",
		"MinimumOSVersion: 10.0.17763.0\n",
		"ManifestType: installer\nManifestVersion: " + ManifestVersion + "\n",
	} {
		if !strings.Contains(inst, want) {
			t.Errorf("installer manifest lacks %q", want)
		}
	}
	en := files["eziq1.golauncher.locale.en-US.yaml"]
	for _, want := range []string{"PackageLocale: en-US\n", "Publisher: eziq1\n", "License: MIT\n", "ManifestType: defaultLocale\n", "Moniker: golauncher\n"} {
		if !strings.Contains(en, want) {
			t.Errorf("en-US manifest lacks %q", want)
		}
	}
	zh := files["eziq1.golauncher.locale.zh-CN.yaml"]
	if !strings.Contains(zh, "ManifestType: locale\n") || strings.Contains(zh, "Moniker") {
		t.Errorf("zh-CN manifest:\n%s", zh)
	}
	if v := files["eziq1.golauncher.yaml"]; !strings.Contains(v, "DefaultLocale: en-US\n") {
		t.Errorf("version manifest:\n%s", v)
	}
}

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"golauncher":          "golauncher",
		"https://example.com": "https://example.com",
		"0.8.0":               "0.8.0",
		"1.10":                "'1.10'",
		"a: b":                "'a: b'",
		"it's":                "it's",
		"'x'":                 "'''x'''",
		"yes":                 "'yes'",
		"-x":                  "'-x'",
		"":                    "''",
		"类 Raycast 的启动器，用 Go 编写。": "类 Raycast 的启动器，用 Go 编写。",
	} {
		if got := quote(in); got != want {
			t.Errorf("quote(%q) = %s, want %s", in, got, want)
		}
	}
}

// writeZip writes a zip with the named (empty) files and returns its
// SHA-256.
func writeZip(t *testing.T, path string, names ...string) string {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprint(w, n)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// fakeDist writes a release's Windows zips and checksums.txt; edit changes
// the checksums lines.
func fakeDist(t *testing.T, version string, zipFiles []string, edit func(lines []string) []string) string {
	t.Helper()
	dir := t.TempDir()
	var lines []string
	for _, a := range arches {
		name := ZipName(version, a.goarch)
		lines = append(lines, writeZip(t, filepath.Join(dir, name), zipFiles...)+"  "+name)
	}
	if edit != nil {
		lines = edit(lines)
	}
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

var programs = []string{"golauncher.exe", "glctl.exe", "LICENSE"}

func TestReadDist(t *testing.T) {
	dir := fakeDist(t, "1.2.3", programs, nil)
	rel, err := readDist(dir, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(rel.Installers) != 2 || rel.Installers[0].Arch != "x64" || rel.Installers[1].Arch != "arm64" {
		t.Fatalf("installers = %+v", rel.Installers)
	}
	sum, _ := hashFile(filepath.Join(dir, ZipName("1.2.3", "amd64")))
	if rel.Installers[0].SHA256 != sum || !strings.HasSuffix(rel.Installers[0].URL, "/v1.2.3/golauncher_1.2.3_windows_amd64.zip") {
		t.Fatalf("x64 = %+v", rel.Installers[0])
	}
}

func TestReadDistErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		files []string
		edit  func([]string) []string
		want  string
	}{
		"bad checksum": {programs, func(l []string) []string {
			l[1] = strings.Repeat("0", 64) + l[1][64:]
			return l
		}, "checksums.txt says"},
		"no checksum": {programs, func(l []string) []string { return l[:1] }, "no line for golauncher_1.2.3_windows_arm64.zip"},
		"no glctl":    {[]string{"golauncher.exe"}, nil, "no glctl.exe at the root"},
		"nested":      {[]string{"golauncher/golauncher.exe", "golauncher/glctl.exe"}, nil, "at the root"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := readDist(fakeDist(t, "1.2.3", tc.files, tc.edit), "1.2.3")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestRunWrites(t *testing.T) {
	dist := fakeDist(t, "1.2.3", programs, nil)
	out := t.TempDir()
	dir, err := run(context.Background(), "v1.2.3", dist, false, "2026-01-02", out)
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(out, "e", "eziq1", "golauncher", "1.2.3") {
		t.Fatalf("dir = %s", dir)
	}
	data, err := os.ReadFile(filepath.Join(dir, "eziq1.golauncher.installer.yaml"))
	if err != nil || !strings.Contains(string(data), "ReleaseDate: 2026-01-02\n") {
		t.Fatalf("installer manifest: %v\n%s", err, data)
	}
}

func TestRunRejects(t *testing.T) {
	dist := fakeDist(t, "1.2.3", programs, nil)
	if _, err := run(context.Background(), "1.2.3-rc.1", dist, false, "", t.TempDir()); err == nil {
		t.Fatal("a pre-release was accepted")
	}
	if _, err := run(context.Background(), "1.2.3", dist, false, "02/01/2026", t.TempDir()); err == nil {
		t.Fatal("a bad date was accepted")
	}
}

func TestFetchRelease(t *testing.T) {
	src := fakeDist(t, "1.2.3", programs, nil)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/eziq1/golauncher/releases/tags/v1.2.3" {
			var assets []string
			for _, n := range []string{"checksums.txt", ZipName("1.2.3", "amd64"), ZipName("1.2.3", "arm64"), "golauncher_1.2.3_darwin_arm64.zip"} {
				assets = append(assets, fmt.Sprintf(`{"name":%q,"browser_download_url":%q}`, n, srv.URL+"/dl/"+n))
			}
			fmt.Fprintf(w, `{"published_at":"2026-03-04T23:30:00Z","assets":[%s]}`, strings.Join(assets, ","))
			return
		}
		if name, ok := strings.CutPrefix(r.URL.Path, "/dl/"); ok && !strings.Contains(name, "darwin") {
			http.ServeFile(w, r, filepath.Join(src, name))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	dir := t.TempDir()
	published, err := fetchRelease(context.Background(), srv.Client(), srv.URL, "1.2.3", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !published.Equal(time.Date(2026, 3, 4, 23, 30, 0, 0, time.UTC)) {
		t.Fatalf("published = %v", published)
	}
	if _, err := readDist(dir, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if _, err := fetchRelease(context.Background(), srv.Client(), srv.URL, "9.9.9", t.TempDir()); err == nil {
		t.Fatal("a missing release was fetched")
	}
}
