package update

import (
	"testing"
	"time"
)

// SetRetryWait makes the waits between download attempts d for a test.
func SetRetryWait(t *testing.T, d time.Duration) {
	old := retryWait
	retryWait = func(int) time.Duration { return d }
	t.Cleanup(func() { retryWait = old })
}

// SetStallTimeout makes downloads give up after d without data, for a test.
func SetStallTimeout(t *testing.T, d time.Duration) {
	old := stallTimeout
	stallTimeout = d
	t.Cleanup(func() { stallTimeout = old })
}
