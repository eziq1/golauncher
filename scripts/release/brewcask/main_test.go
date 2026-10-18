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
	"regexp"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/install"
)

// The v0.8.0 release, as committed in packaging/homebrew.
var v080 = Release{
	Version: "0.8.0",
	MacOS:   "ventura",
	Arches: []Arch{
		{Brew: "arm", GOARCH: "arm64", SHA256: "d1414c62f13c7085806d589f8675c8f8639547ee7cfd47cb96908fe5cc8be0f0"},
		{Brew: "intel", GOARCH: "amd64", SHA256: "d8738ede7bdfdc6f206ed2c461a04b51adcd87da417983343c3b624de798da7a"},
	},
}

// TestCommittedCask keeps the committed example in step with the
// generator: rerun it (-download) after changing the text.
func TestCommittedCask(t *testing.T) {
	got, err := os.ReadFile(filepath.Join("..", "..", "..", "packaging", "homebrew", "Casks", CaskFile))
	if err != nil {
		t.Fatal(err)
	}
	if want := Cask(v080); string(got) != want {
		t.Errorf("the committed cask differs from the generator's:\n%s", want)
	}
}

func TestCaskContent(t *testing.T) {
	c := Cask(v080)
	for _, want := range []string{
		"cask \"golauncher\" do\n  version \"0.8.0\"\n\n",
		"  on_arm do\n    sha256 \"d1414c62f13c7085806d589f8675c8f8639547ee7cfd47cb96908fe5cc8be0f0\"\n\n" +
			"    url \"https://github.com/eziq1/golauncher/releases/download/v#{version}/golauncher_#{version}_darwin_arm64.zip\"\n  end\n",
		"  on_intel do\n    sha256 \"d8738ede7bdfdc6f206ed2c461a04b51adcd87da417983343c3b624de798da7a\"\n\n" +
			"    url \"https://github.com/eziq1/golauncher/releases/download/v#{version}/golauncher_#{version}_darwin_amd64.zip\"\n  end\n",
		"  name \"golauncher\"\n  desc \"" + Desc + "\"\n  homepage \"https://github.com/eziq1/golauncher\"\n",
		"  livecheck do\n    url :url\n    strategy :github_latest\n  end\n",
		"  depends_on macos: :ventura\n",
		"  app \"golauncher.app\"\n  binary \"#{appdir}/golauncher.app/Contents/MacOS/glctl\"\n",
		"    \"~/Library/Application Support/golauncher\",\n",
		"    \"~/Library/Caches/golauncher\",\n",
		"    \"~/Library/LaunchAgents/io.github.eziq1.golauncher.login.plist\",\n",
		"      brew upgrade --cask golauncher\n",
		"  EOS\nend\n",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("the cask lacks %q", want)
		}
	}
	if strings.Contains(c, "uninstall ") || strings.Contains(c, "quit:") {
		t.Error("the cask quits golauncher: an upgrade from its Homebrew commands would be cut short")
	}
	// The interpolated URLs are the release's assets.
	for _, a := range v080.Arches {
		url := strings.ReplaceAll(fmt.Sprintf("%s/releases/download/v#{version}/%s", homepage, ZipName("#{version}", a.GOARCH)), "#{version}", v080.Version)
		if url != DownloadURL(v080.Version, ZipName(v080.Version, a.GOARCH)) {
			t.Errorf("%s: %s", a.GOARCH, url)
		}
	}
	// Homebrew's rules for desc.
	if len(Desc) > 80 || strings.HasSuffix(Desc, ".") || regexp.MustCompile(`^(?i)(a|an|the) `).MatchString(Desc) ||
		strings.Contains(strings.ToLower(Desc), "golauncher") || strings.Contains(strings.ToLower(Desc), "macos") {
		t.Errorf("desc %q", Desc)
	}
	// Structure: balanced blocks, two-space indentation, no tabs, no
	// trailing spaces, no interpolation outside the url and binary lines.
	depth := 0
	inHeredoc := false
	for i, line := range strings.Split(strings.TrimSuffix(c, "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.ContainsAny(line, "\t\r") || strings.HasSuffix(line, " "):
			t.Errorf("line %d: %q", i+1, line)
		case inHeredoc:
			if trimmed == "EOS" {
				inHeredoc = false
			}
			continue
		case strings.HasSuffix(line, "<<~EOS"):
			inHeredoc = true
		}
		if strings.Contains(line, "#{") && !strings.HasPrefix(trimmed, "url ") && !strings.HasPrefix(trimmed, "binary ") {
			t.Errorf("line %d interpolates: %q", i+1, line)
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if trimmed == "end" || trimmed == "]" {
			depth--
		}
		if indent != 2*depth {
			t.Errorf("line %d: indent %d at depth %d: %q", i+1, indent, depth, line)
		}
		if strings.HasSuffix(trimmed, " do") || strings.HasSuffix(trimmed, "[") {
			depth++
		}
	}
	if depth != 0 || inHeredoc {
		t.Errorf("unbalanced: depth %d, heredoc %v", depth, inHeredoc)
	}
}

func TestRuby(t *testing.T) {
	for in, want := range map[string]string{
		"golauncher":     `"golauncher"`,
		`say "hi"`:       `"say \"hi\""`,
		`a\b`:            `"a\\b"`,
		"#{system('x')}": `"\#{system('x')}"`,
		"~/Library/x y":  `"~/Library/x y"`,
		"line\nbreak":    `"line\nbreak"`,
		"类 Raycast 的启动器": `"类 Raycast 的启动器"`,
	} {
		if got := ruby(in); got != want {
			t.Errorf("ruby(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestMacOSName(t *testing.T) {
	for v, want := range map[string]string{"13.0": "ventura", "13": "ventura", "14.0.0": "sonoma", "26.0": "tahoe", "11.0": "big_sur", "13.5": "", "10.15": "", "": ""} {
		got, ok := MacOSName(v)
		if got != want || ok != (want != "") {
			t.Errorf("MacOSName(%q) = %q, %v; want %q", v, got, ok, want)
		}
	}
	// The app's own minimum is one Homebrew knows.
	if got, _ := MacOSName(install.MinSystemVersion); got != v080.MacOS {
		t.Errorf("install.MinSystemVersion %s is %q, the committed cask says %s", install.MinSystemVersion, got, v080.MacOS)
	}
}

// bundleFiles are the files of a release zip, with an Info.plist of
// version.
func bundleFiles(version string) map[string][]byte {
	return map[string][]byte{
		"golauncher.app/":                          nil,
		"golauncher.app/Contents/Info.plist":       install.InfoPlist(install.BundleInfo{Version: version}),
		"golauncher.app/Contents/MacOS/golauncher": []byte("golauncher"),
		"golauncher.app/Contents/MacOS/glctl":      []byte("glctl"),
		"LICENSE":                                  []byte("MIT"),
	}
}

// writeZip writes a zip with files and returns its SHA-256.
func writeZip(t *testing.T, path string, files map[string][]byte) string {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
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

// fakeDist writes a release's macOS zips (and a Windows one, which the
// cask ignores) and checksums.txt; files changes a zip's files, edit the
// checksums lines.
func fakeDist(t *testing.T, version string, files func(goarch string, f map[string][]byte), edit func(lines []string) []string) string {
	t.Helper()
	dir := t.TempDir()
	var lines []string
	for _, goarch := range []string{"arm64", "amd64"} {
		f := bundleFiles(version)
		if files != nil {
			files(goarch, f)
		}
		name := ZipName(version, goarch)
		lines = append(lines, writeZip(t, filepath.Join(dir, name), f)+"  "+name)
	}
	win := fmt.Sprintf("golauncher_%s_windows_amd64.zip", version)
	lines = append(lines, writeZip(t, filepath.Join(dir, win), map[string][]byte{"golauncher.exe": nil})+"  "+win)
	if edit != nil {
		lines = edit(lines)
	}
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReadDist(t *testing.T) {
	dir := fakeDist(t, "1.2.3", nil, nil)
	rel, err := readDist(dir, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if rel.MacOS != "ventura" || len(rel.Arches) != 2 || rel.Arches[0].Brew != "arm" || rel.Arches[1].Brew != "intel" {
		t.Fatalf("release = %+v", rel)
	}
	sum, _ := hashFile(filepath.Join(dir, ZipName("1.2.3", "amd64")))
	if rel.Arches[1].SHA256 != sum || rel.Arches[1].GOARCH != "amd64" {
		t.Fatalf("intel = %+v", rel.Arches[1])
	}
}

func TestReadDistErrors(t *testing.T) {
	remove := func(name string) func(string, map[string][]byte) {
		return func(_ string, f map[string][]byte) { delete(f, name) }
	}
	plist := func(edit func(string) string) func(string, map[string][]byte) {
		return func(goarch string, f map[string][]byte) {
			if goarch == "amd64" {
				p := "golauncher.app/Contents/Info.plist"
				f[p] = []byte(edit(string(f[p])))
			}
		}
	}
	for name, tc := range map[string]struct {
		files func(string, map[string][]byte)
		edit  func([]string) []string
		want  string
	}{
		"bad checksum": {nil, func(l []string) []string {
			l[1] = strings.Repeat("0", 64) + l[1][64:]
			return l
		}, "checksums.txt says"},
		"no checksum": {nil, func(l []string) []string { return l[:1] }, "no line for golauncher_1.2.3_darwin_amd64.zip"},
		"no glctl":    {remove("golauncher.app/Contents/MacOS/glctl"), nil, "no golauncher.app/Contents/MacOS/glctl"},
		"no plist":    {remove("golauncher.app/Contents/Info.plist"), nil, "no golauncher.app/Contents/Info.plist"},
		"nested": {func(_ string, f map[string][]byte) {
			for k, v := range f {
				delete(f, k)
				f["golauncher/"+k] = v
			}
		}, nil, "no golauncher.app/"},
		"other version": {plist(func(s string) string { return strings.ReplaceAll(s, "1.2.3", "1.2.4") }), nil, `version "1.2.4", not 1.2.3`},
		"other bundle":  {plist(func(s string) string { return strings.ReplaceAll(s, install.BundleID, "com.example.x") }), nil, `"com.example.x"`},
		"unknown macOS": {plist(func(s string) string { return strings.ReplaceAll(s, "<string>13.0</string>", "<string>13.4</string>") }), nil, `LSMinimumSystemVersion "13.4"`},
		"macOS differs": {plist(func(s string) string { return strings.ReplaceAll(s, "<string>13.0</string>", "<string>14.0</string>") }), nil, "needs macOS sonoma"},
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
	dist := fakeDist(t, "1.2.3", nil, nil)
	out := filepath.Join(t.TempDir(), "tap", "Casks")
	path, err := run(context.Background(), "v1.2.3", dist, false, out)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(out, "golauncher.rb") {
		t.Fatalf("path = %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "  version \"1.2.3\"\n") {
		t.Fatalf("cask: %v\n%s", err, data)
	}
}

func TestRunRejectsPrerelease(t *testing.T) {
	dist := fakeDist(t, "1.2.3-rc.1", nil, nil)
	if _, err := run(context.Background(), "1.2.3-rc.1", dist, false, t.TempDir()); err == nil || !strings.Contains(err.Error(), "pre-releases") {
		t.Fatalf("a pre-release was accepted: %v", err)
	}
}

func TestFetchRelease(t *testing.T) {
	src := fakeDist(t, "1.2.3", nil, nil)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/eziq1/golauncher/releases/tags/v1.2.3", "/repos/eziq1/golauncher/releases/tags/v1.3.0":
			var assets []string
			for _, n := range []string{"checksums.txt", ZipName("1.2.3", "arm64"), ZipName("1.2.3", "amd64"), "golauncher_1.2.3_windows_amd64.zip"} {
				assets = append(assets, fmt.Sprintf(`{"name":%q,"browser_download_url":%q}`, n, srv.URL+"/dl/"+n))
			}
			pre := strings.HasSuffix(r.URL.Path, "v1.3.0")
			fmt.Fprintf(w, `{"draft":false,"prerelease":%v,"assets":[%s]}`, pre, strings.Join(assets, ","))
			return
		}
		if name, ok := strings.CutPrefix(r.URL.Path, "/dl/"); ok && !strings.Contains(name, "windows") {
			http.ServeFile(w, r, filepath.Join(src, name))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	dir := t.TempDir()
	if err := fetchRelease(context.Background(), srv.Client(), srv.URL, "1.2.3", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := readDist(dir, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "golauncher_1.2.3_windows_amd64.zip")); err == nil {
		t.Fatal("the Windows zip was downloaded")
	}
	if err := fetchRelease(context.Background(), srv.Client(), srv.URL, "1.3.0", t.TempDir()); err == nil || !strings.Contains(err.Error(), "pre-release") {
		t.Fatalf("a pre-release was fetched: %v", err)
	}
	if err := fetchRelease(context.Background(), srv.Client(), srv.URL, "9.9.9", t.TempDir()); err == nil {
		t.Fatal("a missing release was fetched")
	}
}
