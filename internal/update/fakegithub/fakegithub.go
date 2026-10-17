// Package fakegithub is a fake of the GitHub API and release downloads for
// testing updates (internal/update) and for glctl update without a
// network: one repository whose latest release holds golauncher archives
// for each architecture, checksums.txt and, when signed, its signature.
package fakegithub

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eziq1/golauncher/internal/install"
	"github.com/eziq1/golauncher/internal/update"
)

// Options describe the latest release.
type Options struct {
	// Version of the latest release (default "1.0.0"); its tag is "v"+Version.
	Version string
	// Arches have an archive each (default amd64 and arm64).
	Arches []string
	// Exe returns the content of golauncher.exe in an archive (default
	// "fake golauncher <version> <arch>").
	Exe func(arch string) []byte
	// SigningKey, a base64 ed25519 seed, signs checksums.txt.
	SigningKey string
	// Published is the release date (default 2026-10-01).
	Published time.Time
	// Darwin adds the macOS archive (universal, with golauncher.app).
	Darwin bool
}

// Server is the fake. Its fields may be changed between requests (under
// Lock/Unlock when requests may run).
type Server struct {
	*httptest.Server
	sync.Mutex
	// Release is what the API answers; Files the downloads by name.
	Release update.Release
	Files   map[string][]byte
	// Status, when not 0, is the API's answer instead (with Header).
	Status int
	Header http.Header
	// FailDownloads makes the next downloads fail with 503 (counted down).
	FailDownloads int
	// TruncateOnce cuts the next download of a file in half (then closes
	// the connection), to test resuming.
	TruncateOnce string
	// StallOnce sends half of the next download of a file, then nothing
	// until the client gives up.
	StallOnce string

	// APIRequests and NotModified count the API's answers; Ranges the
	// downloads that asked for a range.
	APIRequests, NotModified, Ranges atomic.Int32
	// Downloads counts the requests for files.
	Downloads atomic.Int32
	// UserAgent is the last User-Agent seen.
	UserAgent atomic.Value
}

// New starts the fake. Close it when done.
func New(o Options) *Server {
	s := &Server{Files: map[string][]byte{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	s.SetLatest(o)
	return s
}

// API is the base URL of the fake API (update.Client.API).
func (s *Server) API() string { return s.URL + "/api" }

// SetLatest replaces the latest release.
func (s *Server) SetLatest(o Options) {
	if o.Version == "" {
		o.Version = "1.0.0"
	}
	if len(o.Arches) == 0 {
		o.Arches = []string{"amd64", "arm64"}
	}
	if o.Exe == nil {
		o.Exe = func(arch string) []byte { return []byte("fake golauncher " + o.Version + " " + arch) }
	}
	if o.Published.IsZero() {
		o.Published = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	}
	files := map[string][]byte{}
	var sums strings.Builder
	for _, arch := range o.Arches {
		name := update.ArchiveName(o.Version, arch)
		files[name] = Archive(map[string][]byte{
			"golauncher.exe": o.Exe(arch),
			"glctl.exe":      []byte("fake glctl " + o.Version + " " + arch),
			"README.md":      []byte("# golauncher\n"),
		})
	}
	if o.Darwin {
		files[update.BundleArchiveName(o.Version, update.Universal)] = BundleArchive(o.Version, o.Exe(update.Universal))
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(files[n]), n)
	}
	files[update.ChecksumsName] = []byte(sums.String())
	if o.SigningKey != "" {
		sig, err := update.Sign(o.SigningKey, files[update.ChecksumsName])
		if err != nil {
			panic(err)
		}
		files[update.SignatureName] = []byte(sig + "\n")
	}
	tag := "v" + o.Version
	r := update.Release{
		Tag:         tag,
		Name:        "golauncher " + tag,
		HTMLURL:     "https://github.com/" + update.Repo + "/releases/tag/" + tag,
		Body:        "## Added\n\n- Things.\n",
		PublishedAt: o.Published,
	}
	s.Lock()
	defer s.Unlock()
	s.Files = files
	s.Release = r
	s.syncAssets()
}

// syncAssets lists Files as the release's assets. s is locked.
func (s *Server) syncAssets() {
	s.Release.Assets = nil
	names := make([]string, 0, len(s.Files))
	for n := range s.Files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		s.Release.Assets = append(s.Release.Assets, update.Asset{Name: n, Size: int64(len(s.Files[n])), URL: s.URL + "/download/" + n})
	}
}

// SetFile replaces (or with nil removes) a download and updates the
// assets.
func (s *Server) SetFile(name string, data []byte) {
	s.Lock()
	defer s.Unlock()
	if data == nil {
		delete(s.Files, name)
	} else {
		s.Files[name] = data
	}
	s.syncAssets()
}

// Archive returns a zip archive of files, the same bytes every time.
func Archive(files map[string][]byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate, Modified: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			panic(err)
		}
		_, _ = w.Write(files[n])
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// BundleArchive returns a macOS release archive of version, the same bytes
// every time: golauncher.app with its Info.plist, the executable exe and
// glctl, and a LICENSE beside it.
func BundleArchive(version string, exe []byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	app := install.BundleName + "/Contents/"
	for _, f := range []struct {
		name string
		mode os.FileMode
		data []byte
	}{
		{install.BundleName + "/", os.ModeDir | 0o755, nil},
		{app + "Info.plist", 0o644, install.InfoPlist(install.BundleInfo{Version: version})},
		{app + "MacOS/golauncher", 0o755, exe},
		{app + "MacOS/glctl", 0o755, []byte("fake glctl " + version + " universal")},
		{app + "Resources/golauncher.icns", 0o644, []byte("icns")},
		{"LICENSE", 0o644, []byte("MIT\n")},
	} {
		h := &zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
		h.SetMode(f.mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			panic(err)
		}
		_, _ = w.Write(f.data)
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.UserAgent.Store(r.Header.Get("User-Agent"))
	switch {
	case r.URL.Path == "/api/repos/"+update.Repo+"/releases/latest":
		s.serveLatest(w, r)
	case strings.HasPrefix(r.URL.Path, "/download/"):
		s.serveDownload(w, r, strings.TrimPrefix(r.URL.Path, "/download/"))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveLatest(w http.ResponseWriter, r *http.Request) {
	s.APIRequests.Add(1)
	s.Lock()
	status, header, rel := s.Status, s.Header, s.Release
	s.Unlock()
	if status != 0 {
		for k, v := range header {
			w.Header()[k] = v
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"message":"fake error"}`))
		return
	}
	data, err := json.Marshal(rel, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	etag := fmt.Sprintf(`"%x"`, sha256.Sum256(data))
	w.Header().Set("ETag", etag)
	w.Header().Set("X-RateLimit-Remaining", "59")
	if r.Header.Get("If-None-Match") == etag {
		s.NotModified.Add(1)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (s *Server) serveDownload(w http.ResponseWriter, r *http.Request, name string) {
	s.Downloads.Add(1)
	s.Lock()
	data, ok := s.Files[name]
	fail := s.FailDownloads > 0
	if fail {
		s.FailDownloads--
	}
	truncate := s.TruncateOnce == name
	if truncate {
		s.TruncateOnce = ""
	}
	stall := s.StallOnce == name
	if stall {
		s.StallOnce = ""
	}
	s.Unlock()
	switch {
	case !ok:
		http.NotFound(w, r)
		return
	case fail:
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Header.Get("Range") != "" {
		s.Ranges.Add(1)
	}
	if stall {
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data[:len(data)/2])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
		return
	}
	if truncate {
		// Promise everything, send half, hang up.
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data[:len(data)/2])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		panic(http.ErrAbortHandler)
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
