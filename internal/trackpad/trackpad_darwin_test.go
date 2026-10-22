//go:build darwin

package trackpad

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// TestRealTrackpad lists the multitouch devices and logs their frames and
// taps for a few seconds, on a real Mac: tap with three fingers, swipe, and
// read the log. It touches no input. Run it with
//
//	GOLAUNCHER_TRACKPAD_TESTS=1 go test -run TestRealTrackpad -v ./internal/trackpad
func TestRealTrackpad(t *testing.T) {
	if os.Getenv("GOLAUNCHER_TRACKPAD_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_TRACKPAD_TESTS=1 to read the trackpad")
	}
	f, err := mt()
	if err != nil {
		t.Fatal(err)
	}
	if list := f.createList(); list != 0 {
		t.Logf("%d multitouch devices", f.count(list))
		f.release(list)
	} else {
		t.Log("no device list")
	}

	var mu sync.Mutex
	frames, last := 0, -1
	log := func(dev uintptr, at time.Duration, touches []Touch) {
		mu.Lock()
		defer mu.Unlock()
		frames++
		if len(touches) == 0 && last == 0 {
			return
		}
		last = len(touches)
		line := ""
		for _, tc := range touches {
			line += " " + touchString(tc)
		}
		t.Logf("%8.3fs dev %#x:%s", at.Seconds(), dev, line)
	}
	debugFrame.Store(&log)
	defer debugFrame.Store(nil)

	taps := 0
	stop, err := Start(func() {
		mu.Lock()
		taps++
		mu.Unlock()
		t.Log("three-finger tap")
	})
	if err != nil {
		t.Fatal(err)
	}
	d := 10 * time.Second
	t.Logf("tap with three fingers, swipe, click… for %v", d)
	time.Sleep(d)
	stop()
	mu.Lock()
	defer mu.Unlock()
	t.Logf("%d frames, %d taps", frames, taps)
}

// touchString is a touch as logged: its identifier, state and position.
func touchString(tc Touch) string {
	return fmt.Sprintf("#%d/s%d@%.3f,%.3f", tc.ID, tc.State, tc.X, tc.Y)
}
