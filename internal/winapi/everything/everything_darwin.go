package everything

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// On macOS the package searches with Spotlight instead of Everything: each
// Search runs mdfind once (docs/macos-port.md §4.2). The search syntax is
// translated in spotlight.go.

// mdfind is Spotlight's command-line search.
const mdfind = "/usr/bin/mdfind"

// scanFactor bounds how many paths mdfind may print per wanted result
// before the search stops: most are only filtered out by negated path terms
// (the system tier) when the text is short.
const (
	scanFactor = 20
	scanMin    = 5000
)

// Search runs q with mdfind. Cancelling ctx kills mdfind. The results are
// in q.Sort order among those found, not over all of Spotlight's matches:
// mdfind stops once Offset+Max of them passed the filter.
func Search(ctx context.Context, q Query) ([]Result, error) {
	n := q.Max
	if n <= 0 {
		n = DefaultMax
	}
	want := max(q.Offset, 0) + n
	sq := parseSpotlight(q)
	query, onlyIn := sq.mdfindQuery()
	args := []string{"-0"}
	if onlyIn != "" {
		args = append(args, "-onlyin", onlyIn)
	}
	args = append(args, query)

	mctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(mctx, mdfind, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("everything: starting mdfind: %w", err)
	}
	var res []Result
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	sc.Split(splitNUL)
	scanned, limit := 0, max(scanMin, scanFactor*want)
	for len(res) < want && scanned < limit && sc.Scan() {
		scanned++
		if r, ok := stat(sc.Text()); ok && sq.filter(r.Path, r.IsFolder) {
			res = append(res, r)
		}
	}
	stopped := len(res) >= want || scanned >= limit
	if stopped {
		cancel() // enough: stop mdfind
	}
	werr := cmd.Wait()
	switch {
	case ctx.Err() != nil:
		return nil, ctx.Err() // superseded by a newer query
	case sc.Err() != nil && !stopped:
		return nil, fmt.Errorf("everything: reading mdfind: %w", sc.Err())
	case werr != nil && !stopped:
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("everything: mdfind: %s", msg)
		}
		return nil, fmt.Errorf("everything: mdfind: %w", werr)
	}
	sortResults(res, q.Sort)
	return res[min(max(q.Offset, 0), len(res)):], nil
}

// stat is the Result of the file at p; ok is false when it no longer
// exists (Spotlight's index lags behind deletions).
func stat(p string) (Result, bool) {
	if p == "" {
		return Result{}, false
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return Result{}, false
	}
	r := Result{Name: filepath.Base(p), Path: p, IsFolder: fi.IsDir(), Size: fi.Size(), Modified: fi.ModTime()}
	if r.IsFolder {
		r.Size = -1
	}
	return r, true
}

// Installed reports whether Spotlight's mdfind is there; its path stands in
// for Everything's executable.
func Installed() (exePath string, ok bool) {
	if fi, err := os.Stat(mdfind); err != nil || fi.IsDir() {
		return "", false
	}
	return mdfind, true
}

// Running reports whether Spotlight can be asked: always when mdfind is
// there (a volume whose indexing is off just finds nothing; see
// `mdutil -s /`).
func Running() bool {
	_, ok := Installed()
	return ok
}

// Start is not supported: Spotlight is part of the system.
func Start(exePath string) error { return errors.ErrUnsupported }

// UserFolders returns the user's Desktop, Documents, Downloads, Pictures,
// Movies and Music folders and iCloud Drive, those that exist.
func UserFolders() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range []string{"Desktop", "Documents", "Downloads", "Pictures", "Movies", "Music",
		filepath.Join("Library", "Mobile Documents", "com~apple~CloudDocs")} {
		p := filepath.Join(home, d)
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			out = append(out, p)
		}
	}
	return out
}
