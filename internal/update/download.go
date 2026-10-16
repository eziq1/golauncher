package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Download attempts and the waits between them.
var (
	downloadAttempts = 3
	retryWait        = func(attempt int) time.Duration { return time.Duration(attempt) * 2 * time.Second }
)

// fetch downloads a small file whole, refusing more than limit bytes.
func (c *Client) fetch(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	if err := checkURL(rawURL); err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 1; attempt <= downloadAttempts; attempt++ {
		if attempt > 1 {
			if err := sleep(ctx, retryWait(attempt-1)); err != nil {
				return nil, err
			}
		}
		data, err := c.fetchOnce(ctx, rawURL, limit)
		if err == nil || !retryable(err) {
			return data, err
		}
		lastErr = err
	}
	return nil, lastErr
}

func (c *Client) fetchOnce(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, &netError{err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, &netError{err}
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("update: %s is larger than %d bytes", rawURL, limit)
	}
	return data, nil
}

// Progress reports how much of a download arrived (total -1 when unknown).
type Progress func(done, total int64)

// download writes the file at rawURL to dst, which must be size bytes
// (-1: unknown, up to MaxArchiveSize). It goes through dst+".part": a
// partial file from an earlier attempt is resumed when the server allows
// (Range), and failed attempts are retried.
func (c *Client) download(ctx context.Context, rawURL, dst string, size int64, progress Progress) error {
	if err := checkURL(rawURL); err != nil {
		return err
	}
	if size > MaxArchiveSize {
		return fmt.Errorf("update: the download is %d bytes, more than %d", size, MaxArchiveSize)
	}
	part := dst + ".part"
	var lastErr error
	for attempt := 1; attempt <= downloadAttempts; attempt++ {
		if attempt > 1 {
			if err := sleep(ctx, retryWait(attempt-1)); err != nil {
				return err
			}
		}
		err := c.downloadOnce(ctx, rawURL, part, size, progress)
		if err == nil {
			return os.Rename(part, dst)
		}
		if !retryable(err) {
			_ = os.Remove(part)
			return err
		}
		lastErr = err
	}
	return lastErr
}

func (c *Client) downloadOnce(ctx context.Context, rawURL, part string, size int64, progress Progress) error {
	limit := int64(MaxArchiveSize)
	if size >= 0 {
		limit = size
	}
	var have int64
	if fi, err := os.Stat(part); err == nil {
		have = fi.Size()
	}
	if have > limit {
		_ = os.Remove(part)
		have = 0
	}
	if size >= 0 && have == size {
		return nil // complete: an earlier attempt failed only at the rename
	}
	// A download that stops sending is abandoned after stallTimeout (and
	// retried), rather than waited for forever.
	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stalled atomic.Bool
	timer := time.AfterFunc(stallTimeout, func() { stalled.Store(true); cancel() })
	defer timer.Stop()
	req, err := http.NewRequestWithContext(actx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	if have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	resp, err := c.http().Do(req)
	if err != nil {
		if stalled.Load() && ctx.Err() == nil {
			return &netError{errors.New("no answer")}
		}
		return &netError{err}
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch {
	case resp.StatusCode == http.StatusPartialContent && have > 0 && contentRangeStart(resp) == have:
		flags |= os.O_APPEND
	case resp.StatusCode == http.StatusOK:
		flags |= os.O_TRUNC // no resuming: from the start
		have = 0
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		_ = os.Remove(part)
		return &netError{errors.New(resp.Status)}
	default:
		return statusError(resp)
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	total := size
	if total < 0 {
		total = -1
	}
	w := &progressWriter{w: f, done: have, total: total, report: progress}
	body := &idleReader{r: resp.Body, timer: timer}
	_, err = io.Copy(w, io.LimitReader(body, limit-have+1))
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		if stalled.Load() && ctx.Err() == nil {
			return &netError{errors.New("the download stalled")}
		}
		return &netError{err}
	}
	if w.done > limit {
		_ = os.Remove(part)
		return fmt.Errorf("update: the download is larger than %d bytes", limit)
	}
	if size >= 0 && w.done != size {
		return &netError{fmt.Errorf("update: the download ended at %d of %d bytes", w.done, size)}
	}
	return nil
}

// contentRangeStart returns the first byte of a 206 response.
func contentRangeStart(resp *http.Response) int64 {
	s, ok := strings.CutPrefix(resp.Header.Get("Content-Range"), "bytes ")
	if !ok {
		return -1
	}
	start, _, _ := strings.Cut(s, "-")
	n, err := strconv.ParseInt(start, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// stallTimeout is how long a download may receive nothing.
var stallTimeout = 30 * time.Second

// idleReader restarts timer at every read.
type idleReader struct {
	r     io.Reader
	timer *time.Timer
}

func (r *idleReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.timer.Reset(stallTimeout)
	return n, err
}

type progressWriter struct {
	w           io.Writer
	done, total int64
	report      Progress
	last        time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	if p.report != nil && (time.Since(p.last) > 100*time.Millisecond || p.done == p.total) {
		p.last = time.Now()
		p.report(p.done, p.total)
	}
	return n, err
}

// netError is a failure worth retrying: the connection, a timeout, a
// server error.
type netError struct{ err error }

func (e *netError) Error() string { return "update: downloading: " + e.err.Error() }
func (e *netError) Unwrap() error { return e.err }

func retryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, fs.ErrPermission) {
		return false
	}
	_, ok := errors.AsType[*netError](err)
	return ok
}

// statusError describes a refused download; server errors are retried.
func statusError(resp *http.Response) error {
	err := fmt.Errorf("update: downloading %s: %s", resp.Request.URL, resp.Status)
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return &netError{errors.New(resp.Status)}
	}
	return err
}

// sleep waits d or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
