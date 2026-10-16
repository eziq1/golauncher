// Package update keeps an installed golauncher up to date from GitHub
// Releases (docs/auto-update.md): it checks for the latest stable release,
// downloads the archive for the running architecture, verifies it against
// the release's checksums.txt (and, when the program carries a public key,
// its ed25519 signature), and has a helper process install it with the
// self-installer, restart golauncher and roll back when the new version does
// not start.
//
// The package holds the decisions and the file and network work; the
// desktop glue is in internal/shell. Everything is tested against an
// httptest fake GitHub and temporary directories.
package update

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Repository and API.
const (
	// Repo is the GitHub repository whose releases are the updates.
	Repo = "eziq1/golauncher"
	// DefaultAPI is the GitHub REST API.
	DefaultAPI = "https://api.github.com"
	// ReleasesURL lists the releases on the web.
	ReleasesURL = "https://github.com/" + Repo + "/releases"
	// ChecksumsName and SignatureName are the release assets holding the
	// SHA-256 of the archives and the optional ed25519 signature of
	// ChecksumsName.
	ChecksumsName = "checksums.txt"
	SignatureName = ChecksumsName + ".sig"
)

// Limits.
const (
	// CheckTimeout bounds a check (one API request).
	CheckTimeout = 15 * time.Second
	// maxReleaseJSON bounds the API response.
	maxReleaseJSON = 4 << 20
	// maxChecksums bounds checksums.txt and its signature.
	maxChecksums = 64 << 10
	// MaxArchiveSize is the largest archive downloaded.
	MaxArchiveSize = 100 << 20
)

// Errors.
var (
	// ErrRateLimited: GitHub refused the request until Until.
	ErrRateLimited = errors.New("update: GitHub rate limit exceeded")
	// ErrNoAsset: the release has no archive for this architecture, or no
	// checksums.txt.
	ErrNoAsset = errors.New("update: the release has no archive for this computer")
	// ErrChecksum: the archive does not match checksums.txt.
	ErrChecksum = errors.New("update: the download does not match its checksum")
	// ErrSignature: checksums.txt is not signed with the program's key.
	ErrSignature = errors.New("update: the release is not signed with golauncher's key")
	// ErrBadRelease: the release is not a stable version.
	ErrBadRelease = errors.New("update: the latest release is not a stable version")
)

// RateLimitError is ErrRateLimited with the time the limit resets.
type RateLimitError struct{ Until time.Time }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("%v until %s", ErrRateLimited, e.Until.Format(time.RFC3339))
}

func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// Release is a GitHub release, as much of it as updates need.
type Release struct {
	Tag         string    `json:"tag_name"`
	Name        string    `json:"name,omitempty"`
	HTMLURL     string    `json:"html_url,omitempty"`
	Body        string    `json:"body,omitempty"`
	Draft       bool      `json:"draft,omitzero"`
	Prerelease  bool      `json:"prerelease,omitzero"`
	PublishedAt time.Time `json:"published_at,omitzero"`
	Assets      []Asset   `json:"assets,omitempty"`
}

// Asset is a file of a release.
type Asset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"browser_download_url"`
}

// tagRe matches the tag of a stable release.
var tagRe = regexp.MustCompile(`^v(\d+\.\d+\.\d+)$`)

// Version returns the release's version ("0.5.0" for tag "v0.5.0"), or
// ErrBadRelease when it is a draft, a pre-release or not tagged
// vMAJOR.MINOR.PATCH.
func (r Release) Version() (string, error) {
	m := tagRe.FindStringSubmatch(r.Tag)
	if m == nil || r.Draft || r.Prerelease {
		return "", fmt.Errorf("%w (%q)", ErrBadRelease, r.Tag)
	}
	return m[1], nil
}

// Asset returns the asset named name.
func (r Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Page returns the release's web page.
func (r Release) Page() string {
	if strings.HasPrefix(r.HTMLURL, "https://") {
		return r.HTMLURL
	}
	return ReleasesURL + "/tag/" + url.PathEscape(r.Tag)
}

// ParseRelease decodes a release from the GitHub API.
func ParseRelease(data []byte) (Release, error) {
	var r Release
	if err := json.Unmarshal(data, &r); err != nil {
		return Release{}, fmt.Errorf("update: reading the release: %w", err)
	}
	if r.Tag == "" {
		return Release{}, errors.New("update: reading the release: no tag")
	}
	return r, nil
}

// Client talks to GitHub.
type Client struct {
	// HTTP makes the requests (default: NewHTTPClient(nil)).
	HTTP *http.Client
	// API is the REST API's base URL (default DefaultAPI).
	API string
	// Repo is "owner/name" (default Repo).
	Repo string
	// UserAgent identifies golauncher; GitHub requires one.
	UserAgent string
	// Now returns the time (default time.Now), for rate limits.
	Now func() time.Time
}

// NewHTTPClient returns an HTTP client for updates: proxy chooses the proxy
// (nil: the environment's, http.ProxyFromEnvironment); connections time
// out, but a whole download may take as long as it needs (the context
// bounds a check).
func NewHTTPClient(proxy func(*http.Request) (*url.URL, error)) *http.Client {
	if proxy == nil {
		proxy = http.ProxyFromEnvironment
	}
	t := &http.Transport{
		Proxy:                 proxy,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       60 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{Transport: t, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("update: too many redirects")
		}
		return checkURL(req.URL.String()) // never from HTTPS down to HTTP
	}}
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return NewHTTPClient(nil)
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// LatestResult is the answer to Latest.
type LatestResult struct {
	Release Release
	// ETag identifies the response, for the next request.
	ETag string
	// NotModified: the release is the one of the previous ETag (304), and
	// Release is empty: use the cached one.
	NotModified bool
}

// Latest asks for the latest stable release. etag is the ETag of the
// previous answer, "" for none.
func (c *Client) Latest(ctx context.Context, etag string) (LatestResult, error) {
	api := strings.TrimSuffix(c.API, "/")
	if api == "" {
		api = DefaultAPI
	}
	repo := c.Repo
	if repo == "" {
		repo = Repo
	}
	if err := checkURL(api); err != nil {
		return LatestResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return LatestResult{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", c.UserAgent)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return LatestResult{}, fmt.Errorf("update: checking for updates: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotModified:
		return LatestResult{ETag: etag, NotModified: true}, nil
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotFound:
		return LatestResult{}, errors.New("update: checking for updates: no release found")
	default:
		if until, ok := rateLimited(resp, c.now()); ok {
			return LatestResult{}, &RateLimitError{Until: until}
		}
		return LatestResult{}, fmt.Errorf("update: checking for updates: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxReleaseJSON))
	if err != nil {
		return LatestResult{}, fmt.Errorf("update: checking for updates: %w", err)
	}
	r, err := ParseRelease(data)
	if err != nil {
		return LatestResult{}, err
	}
	return LatestResult{Release: r, ETag: resp.Header.Get("ETag")}, nil
}

// rateLimited reports whether a refused request hit GitHub's rate limit,
// and until when.
func rateLimited(resp *http.Response, now time.Time) (time.Time, bool) {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return time.Time{}, false
	}
	if s := resp.Header.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			return now.Add(time.Duration(n) * time.Second), true
		}
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.StatusCode == http.StatusTooManyRequests {
		if n, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			return time.Unix(n, 0), true
		}
		return now.Add(time.Hour), true
	}
	return time.Time{}, false
}

// checkURL refuses anything but HTTPS, or plain HTTP to this computer (a
// fake server in tests).
func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	if u.Scheme == "https" || u.Scheme == "http" && isLoopback(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("update: refusing %s: updates need HTTPS", raw)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
