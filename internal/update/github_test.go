package update_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/update"
	"github.com/eziq1/golauncher/internal/update/fakegithub"
)

func client(fake *fakegithub.Server) *update.Client {
	return &update.Client{HTTP: fake.Client(), API: fake.API(), UserAgent: "golauncher/test"}
}

func TestReleaseVersion(t *testing.T) {
	for _, tc := range []struct {
		r    update.Release
		want string
	}{
		{update.Release{Tag: "v0.5.0"}, "0.5.0"},
		{update.Release{Tag: "v1.2.0-rc.1"}, ""},
		{update.Release{Tag: "v1.2.0", Prerelease: true}, ""},
		{update.Release{Tag: "v1.2.0", Draft: true}, ""},
		{update.Release{Tag: "1.2.0"}, ""},
		{update.Release{Tag: "v1.2"}, ""},
		{update.Release{Tag: "desktop-v1.2.0"}, ""},
	} {
		got, err := tc.r.Version()
		if got != tc.want || (err == nil) != (tc.want != "") {
			t.Errorf("%+v: Version = %q, %v; want %q", tc.r, got, err, tc.want)
		}
		if tc.want == "" && !errors.Is(err, update.ErrBadRelease) {
			t.Errorf("%+v: err = %v, want ErrBadRelease", tc.r, err)
		}
	}
}

// A release as GitHub's API returns it, trimmed.
const releaseJSON = `{
  "url": "https://api.github.com/repos/eziq1/golauncher/releases/1",
  "html_url": "https://github.com/eziq1/golauncher/releases/tag/v0.5.0",
  "id": 1,
  "author": {"login": "someone"},
  "tag_name": "v0.5.0",
  "target_commitish": "main",
  "name": "golauncher v0.5.0",
  "draft": false,
  "prerelease": false,
  "created_at": "2026-10-20T08:00:00Z",
  "published_at": "2026-10-20T08:05:00Z",
  "assets": [
    {"name": "checksums.txt", "size": 196, "content_type": "text/plain", "browser_download_url": "https://github.com/eziq1/golauncher/releases/download/v0.5.0/checksums.txt"},
    {"name": "golauncher_0.5.0_windows_amd64.zip", "size": 17700000, "browser_download_url": "https://github.com/eziq1/golauncher/releases/download/v0.5.0/golauncher_0.5.0_windows_amd64.zip"},
    {"name": "golauncher_0.5.0_windows_arm64.zip", "size": 16200000, "browser_download_url": "https://github.com/eziq1/golauncher/releases/download/v0.5.0/golauncher_0.5.0_windows_arm64.zip"}
  ],
  "body": "### Added\n\n- Automatic updates."
}`

func TestParseRelease(t *testing.T) {
	r, err := update.ParseRelease([]byte(releaseJSON))
	if err != nil {
		t.Fatal(err)
	}
	if v, err := r.Version(); v != "0.5.0" || err != nil {
		t.Fatalf("Version = %q, %v", v, err)
	}
	if r.Page() != "https://github.com/eziq1/golauncher/releases/tag/v0.5.0" || len(r.Assets) != 3 || !r.PublishedAt.Equal(time.Date(2026, 10, 20, 8, 5, 0, 0, time.UTC)) {
		t.Fatalf("release = %+v", r)
	}
	if _, err := update.ParseRelease([]byte(`{"message":"Not Found"}`)); err == nil {
		t.Fatal("a release without a tag was accepted")
	}
	if _, err := update.ParseRelease([]byte(`[1,2]`)); err == nil {
		t.Fatal("garbage was accepted")
	}
}

func TestSelectAssets(t *testing.T) {
	r, _ := update.ParseRelease([]byte(releaseJSON))
	for _, arch := range []string{"amd64", "arm64"} {
		a, err := update.SelectAssets(r, arch)
		if err != nil || a.Archive.Name != "golauncher_0.5.0_windows_"+arch+".zip" || a.Checksums.Name != "checksums.txt" || a.Signature != nil {
			t.Fatalf("%s: %+v, %v", arch, a, err)
		}
	}
	if _, err := update.SelectAssets(r, "386"); !errors.Is(err, update.ErrNoAsset) {
		t.Fatalf("386: err = %v", err)
	}
	r.Assets = slices.DeleteFunc(r.Assets, func(a update.Asset) bool { return a.Name == "checksums.txt" })
	if _, err := update.SelectAssets(r, "amd64"); !errors.Is(err, update.ErrNoAsset) {
		t.Fatalf("without checksums: err = %v", err)
	}
	r.Assets = append(r.Assets, update.Asset{Name: "checksums.txt"}, update.Asset{Name: "checksums.txt.sig"})
	if a, err := update.SelectAssets(r, "amd64"); err != nil || a.Signature == nil {
		t.Fatalf("with a signature: %+v, %v", a, err)
	}
}

func TestUpdatableAndNewer(t *testing.T) {
	for v, want := range map[string]bool{
		"0.4.0": true, "1.0.0-rc.1": true, "v1.2.3": true,
		"dev": false, "": false, "0.4.0-3-gabc1234": false, "0.4.0-dirty": false, "0.4.0-3-gabc1234-dirty": false,
		"0.0.0-abc1234": false, "0.4": false, "0.4.0+build": false,
	} {
		if got := update.Updatable(v); got != want {
			t.Errorf("Updatable(%q) = %v", v, got)
		}
	}
	for _, tc := range []struct {
		latest, current string
		want            bool
	}{
		{"0.5.0", "0.4.0", true},
		{"0.4.0", "0.4.0", false},
		{"0.3.0", "0.4.0", false}, // never a downgrade
		{"1.0.0", "1.0.0-rc.1", true},
		{"0.10.0", "0.9.0", true},
		{"0.5.0", "dev", false},
		{"0.5.0", "0.0.0-abc1234", false},
	} {
		if got := update.Newer(tc.latest, tc.current); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v", tc.latest, tc.current, got)
		}
	}
}

func TestParseChecksums(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	sums, err := update.ParseChecksums([]byte(sum + "  a.zip\n" + strings.Repeat("cd", 32) + " *b.zip\n\n"))
	if err != nil || len(sums) != 2 || sums["a.zip"][0] != 0xab || sums["b.zip"][0] != 0xcd {
		t.Fatalf("sums = %x, %v", sums, err)
	}
	for _, bad := range []string{"zz  a.zip\n", sum + "\n", "abcd  a.zip\n"} {
		if _, err := update.ParseChecksums([]byte(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSignature(t *testing.T) {
	seed, pub, err := update.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := update.PublicKeyOf(seed); got != pub {
		t.Fatalf("PublicKeyOf = %s, want %s", got, pub)
	}
	data := []byte("checksums")
	sig, err := update.Sign(seed, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := update.VerifySignature(pub, data, []byte(sig+"\n")); err != nil {
		t.Fatalf("good signature: %v", err)
	}
	if err := update.VerifySignature(pub, []byte("checksumz"), []byte(sig)); !errors.Is(err, update.ErrSignature) {
		t.Fatalf("other data: %v", err)
	}
	_, other, _ := update.GenerateKey(rand.Reader)
	if err := update.VerifySignature(other, data, []byte(sig)); !errors.Is(err, update.ErrSignature) {
		t.Fatalf("other key: %v", err)
	}
	if err := update.VerifySignature(pub, data, []byte("not base64!")); !errors.Is(err, update.ErrSignature) {
		t.Fatalf("garbage: %v", err)
	}
}

func TestLatestETag(t *testing.T) {
	fake := fakegithub.New(fakegithub.Options{Version: "0.5.0"})
	defer fake.Close()
	c := client(fake)
	res, err := c.Latest(t.Context(), "")
	if err != nil || res.NotModified || res.ETag == "" || res.Release.Tag != "v0.5.0" {
		t.Fatalf("first = %+v, %v", res, err)
	}
	if ua, _ := fake.UserAgent.Load().(string); ua != "golauncher/test" {
		t.Fatalf("User-Agent = %q", ua)
	}
	again, err := c.Latest(t.Context(), res.ETag)
	if err != nil || !again.NotModified || again.ETag != res.ETag {
		t.Fatalf("second = %+v, %v", again, err)
	}
	if fake.NotModified.Load() != 1 {
		t.Fatalf("304s = %d", fake.NotModified.Load())
	}
	fake.SetLatest(fakegithub.Options{Version: "0.6.0"})
	third, err := c.Latest(t.Context(), res.ETag)
	if err != nil || third.NotModified || third.Release.Tag != "v0.6.0" {
		t.Fatalf("after a new release = %+v, %v", third, err)
	}
}

func TestLatestRateLimit(t *testing.T) {
	fake := fakegithub.New(fakegithub.Options{})
	defer fake.Close()
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	c := client(fake)
	c.Now = func() time.Time { return now }

	fake.Status = http.StatusForbidden
	fake.Header = http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"1792152000"}}
	_, err := c.Latest(t.Context(), "")
	rl, ok := errors.AsType[*update.RateLimitError](err)
	if !ok || !errors.Is(err, update.ErrRateLimited) || !rl.Until.Equal(time.Unix(1792152000, 0)) {
		t.Fatalf("403: err = %v", err)
	}
	fake.Status, fake.Header = http.StatusTooManyRequests, http.Header{"Retry-After": {"120"}}
	_, err = c.Latest(t.Context(), "")
	if rl, ok := errors.AsType[*update.RateLimitError](err); !ok || !rl.Until.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("429: err = %v", err)
	}
	// A 403 that is not about the rate limit is a plain failure.
	fake.Status, fake.Header = http.StatusForbidden, nil
	if _, err = c.Latest(t.Context(), ""); err == nil || errors.Is(err, update.ErrRateLimited) {
		t.Fatalf("plain 403: err = %v", err)
	}
	fake.Status = http.StatusNotFound
	if _, err = c.Latest(t.Context(), ""); err == nil {
		t.Fatal("404 accepted")
	}
}

func TestLatestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	c := &update.Client{HTTP: srv.Client(), API: srv.URL, UserAgent: "x"}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.Latest(ctx, ""); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the timeout was not kept")
	}
}

func TestRefusesPlainHTTP(t *testing.T) {
	c := &update.Client{API: "http://example.com", UserAgent: "x"}
	if _, err := c.Latest(t.Context(), ""); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("err = %v", err)
	}
	r, _ := update.ParseRelease([]byte(releaseJSON))
	for i := range r.Assets {
		r.Assets[i].URL = strings.Replace(r.Assets[i].URL, "https://", "http://", 1)
	}
	if _, err := c.Prepare(t.Context(), r, update.PrepareOptions{Arch: "amd64", Dir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("Prepare: err = %v", err)
	}
}

func TestPrepare(t *testing.T) {
	fake := fakegithub.New(fakegithub.Options{Version: "0.5.0"})
	defer fake.Close()
	dir := t.TempDir()
	var progressed bool
	res, err := client(fake).Prepare(t.Context(), fake.Release, update.PrepareOptions{
		Arch: "arm64", Dir: dir, Progress: func(done, total int64) { progressed = done > 0 && total > 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Staged.Version != "0.5.0" || res.Archive != "golauncher_0.5.0_windows_arm64.zip" || res.Signed || !slices.Equal(res.Files, []string{"glctl.exe", "golauncher.exe"}) || !progressed {
		t.Fatalf("result = %+v (progress %v)", res, progressed)
	}
	exe, _ := os.ReadFile(res.Staged.Exe())
	if string(exe) != "fake golauncher 0.5.0 arm64" {
		t.Fatalf("golauncher.exe = %q", exe)
	}
	if _, err := os.Stat(filepath.Join(dir, res.Archive)); err == nil {
		t.Fatal("the archive was kept")
	}
	if _, err := os.Stat(filepath.Join(res.Staged.Dir, "README.md")); err == nil {
		t.Fatal("other files were extracted")
	}
}

func TestPrepareChecksums(t *testing.T) {
	fake := fakegithub.New(fakegithub.Options{Version: "0.5.0"})
	defer fake.Close()
	name := update.ArchiveName("0.5.0", "amd64")
	c := client(fake)

	// The archive changed after checksums.txt was made.
	good := fake.Files[name]
	bad := fakegithub.Archive(map[string][]byte{"golauncher.exe": []byte("evil")})
	fake.SetFile(name, bad)
	dir := t.TempDir()
	if _, err := c.Prepare(t.Context(), fake.Release, update.PrepareOptions{Arch: "amd64", Dir: dir}); !errors.Is(err, update.ErrChecksum) {
		t.Fatalf("mismatch: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "files", "golauncher.exe")); err == nil {
		t.Fatal("a mismatching archive was extracted")
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
		t.Fatal("a mismatching archive was kept")
	}
	fake.SetFile(name, good)

	// checksums.txt does not list the archive.
	sums := fake.Files[update.ChecksumsName]
	fake.SetFile(update.ChecksumsName, []byte(strings.Repeat("00", 32)+"  other.zip\n"))
	if _, err := c.Prepare(t.Context(), fake.Release, update.PrepareOptions{Arch: "amd64", Dir: t.TempDir()}); !errors.Is(err, update.ErrChecksum) {
		t.Fatalf("not listed: err = %v", err)
	}
	// No checksums.txt at all.
	fake.SetFile(update.ChecksumsName, nil)
	if _, err := c.Prepare(t.Context(), fake.Release, update.PrepareOptions{Arch: "amd64", Dir: t.TempDir()}); !errors.Is(err, update.ErrNoAsset) {
		t.Fatalf("missing: err = %v", err)
	}
	fake.SetFile(update.ChecksumsName, sums)
	if _, err := c.Prepare(t.Context(), fake.Release, update.PrepareOptions{Arch: "amd64", Dir: t.TempDir()}); err != nil {
		t.Fatalf("restored: %v", err)
	}
}

func TestPrepareSignature(t *testing.T) {
	seed, pub, _ := update.GenerateKey(rand.Reader)
	fake := fakegithub.New(fakegithub.Options{Version: "0.5.0", SigningKey: seed})
	defer fake.Close()
	c := client(fake)
	opts := func() update.PrepareOptions {
		return update.PrepareOptions{Arch: "amd64", Dir: t.TempDir(), PublicKey: pub}
	}
	if res, err := c.Prepare(t.Context(), fake.Release, opts()); err != nil || !res.Signed {
		t.Fatalf("signed: %+v, %v", res, err)
	}
	// Without a key in the program, the signature is not needed.
	if res, err := c.Prepare(t.Context(), fake.Release, update.PrepareOptions{Arch: "amd64", Dir: t.TempDir()}); err != nil || res.Signed {
		t.Fatalf("no key: %+v, %v", res, err)
	}
	// Signed with another key.
	other, _, _ := update.GenerateKey(rand.Reader)
	sig, _ := update.Sign(other, fake.Files[update.ChecksumsName])
	fake.SetFile(update.SignatureName, []byte(sig))
	if _, err := c.Prepare(t.Context(), fake.Release, opts()); !errors.Is(err, update.ErrSignature) {
		t.Fatalf("other key: err = %v", err)
	}
	// checksums.txt changed (with a matching archive) but not re-signed.
	fake.SetLatest(fakegithub.Options{Version: "0.5.0", SigningKey: seed})
	fake.SetFile(update.ChecksumsName, append(bytes.Clone(fake.Files[update.ChecksumsName]), "# x\n"...))
	if _, err := c.Prepare(t.Context(), fake.Release, opts()); !errors.Is(err, update.ErrSignature) {
		t.Fatalf("changed checksums: err = %v", err)
	}
	// The signature was removed: refused, since the program has a key.
	fake.SetFile(update.SignatureName, nil)
	if _, err := c.Prepare(t.Context(), fake.Release, opts()); !errors.Is(err, update.ErrSignature) {
		t.Fatalf("no signature: err = %v", err)
	}
}

func TestDownloadRetriesAndResumes(t *testing.T) {
	update.SetRetryWait(t, 0)
	fake := fakegithub.New(fakegithub.Options{Version: "0.5.0"})
	defer fake.Close()
	c := client(fake)
	fake.FailDownloads = 2 // checksums.txt fails twice, then works
	if _, err := c.Prepare(t.Context(), fake.Release, update.PrepareOptions{Arch: "amd64", Dir: t.TempDir()}); err != nil {
		t.Fatalf("after failures: %v", err)
	}
	fake.TruncateOnce = update.ArchiveName("0.5.0", "amd64")
	if _, err := c.Prepare(t.Context(), fake.Release, update.PrepareOptions{Arch: "amd64", Dir: t.TempDir()}); err != nil {
		t.Fatalf("after a cut: %v", err)
	}
	if fake.Ranges.Load() == 0 {
		t.Fatal("the cut download was not resumed")
	}
	fake.FailDownloads = 10
	if _, err := c.Prepare(t.Context(), fake.Release, update.PrepareOptions{Arch: "amd64", Dir: t.TempDir()}); err == nil {
		t.Fatal("no error after every attempt failed")
	}
}

func TestDownloadSizeLimit(t *testing.T) {
	fake := fakegithub.New(fakegithub.Options{Version: "0.5.0"})
	defer fake.Close()
	r := fake.Release
	for i := range r.Assets {
		r.Assets[i].Size = update.MaxArchiveSize + 1
	}
	if _, err := client(fake).Prepare(t.Context(), r, update.PrepareOptions{Arch: "amd64", Dir: t.TempDir()}); err == nil {
		t.Fatal("an oversized archive was downloaded")
	}
}

func TestExtractIgnoresOtherPaths(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.zip")
	os.WriteFile(archive, fakegithub.Archive(map[string][]byte{
		"../golauncher.exe":   []byte("escape"),
		"sub/golauncher.exe":  []byte("nested"),
		`..\golauncher.exe`:   []byte("escape"),
		"glctl.exe":           []byte("ctl"),
		"golauncher.exe/evil": []byte("x"),
	}), 0o644)
	if _, err := update.Extract(archive, filepath.Join(dir, "out")); err == nil {
		t.Fatal("an archive without golauncher.exe at its root was accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "golauncher.exe")); err == nil {
		t.Fatal("a file escaped the folder")
	}
}

func TestSystemProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("NO_PROXY", "")
	req := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u}
	}
	proxyOf := func(f func(*http.Request) (*url.URL, error), raw string) string {
		u, err := f(req(raw))
		if err != nil {
			t.Fatal(err)
		}
		if u == nil {
			return ""
		}
		return u.String()
	}
	all := update.SystemProxy("proxy.example:8080", "<local>;*.corp.example;10.*")
	for raw, want := range map[string]string{
		"https://api.github.com/x":   "http://proxy.example:8080",
		"https://intranet/x":         "",
		"https://git.corp.example/x": "",
		"https://10.1.2.3/x":         "",
		"http://127.0.0.1:9/x":       "",
	} {
		if got := proxyOf(all, raw); got != want {
			t.Errorf("all: %s -> %q, want %q", raw, got, want)
		}
	}
	per := update.SystemProxy("http=h:1;https=s:2", "")
	if got := proxyOf(per, "https://api.github.com/"); got != "http://s:2" {
		t.Errorf("per scheme: %q", got)
	}
	if got := proxyOf(update.SystemProxy("http=h:1", ""), "https://api.github.com/"); got != "" {
		t.Errorf("no https proxy: %q", got)
	}
}

func TestDownloadStalls(t *testing.T) {
	update.SetRetryWait(t, 0)
	update.SetStallTimeout(t, 300*time.Millisecond)
	fake := fakegithub.New(fakegithub.Options{Version: "0.5.0"})
	defer fake.Close()
	fake.StallOnce = update.ArchiveName("0.5.0", "amd64")
	start := time.Now()
	if _, err := client(fake).Prepare(t.Context(), fake.Release, update.PrepareOptions{Arch: "amd64", Dir: t.TempDir()}); err != nil {
		t.Fatalf("after a stall: %v", err)
	}
	// checksums.txt, the stalled archive, the archive again.
	if fake.Downloads.Load() != 3 || time.Since(start) > 10*time.Second {
		t.Fatalf("the stalled download was not retried in time (%d downloads, %s)", fake.Downloads.Load(), time.Since(start))
	}
}
