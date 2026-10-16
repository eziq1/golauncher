package update_test

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/update"
	"github.com/eziq1/golauncher/internal/update/fakegithub"
)

// clock is a fake clock.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// harness is a service against a fake GitHub, with a fake clock and fake
// restarts.
type harness struct {
	t        *testing.T
	fake     *fakegithub.Server
	clock    *clock
	dir      string
	settings update.Settings
	busy     string
	restarts []string // plan files
	events   []update.Event
	mu       sync.Mutex
	s        *update.Service
	opts     update.Options
}

func newHarness(t *testing.T, latest, current string, edit func(*update.Options)) *harness {
	t.Helper()
	update.SetRetryWait(t, 0)
	h := &harness{t: t, clock: &clock{now: t0}, dir: t.TempDir(), settings: update.ParseSettings("", "")}
	h.fake = fakegithub.New(fakegithub.Options{Version: latest})
	t.Cleanup(h.fake.Close)
	self := filepath.Join(t.TempDir(), "golauncher.exe")
	os.WriteFile(self, []byte("running golauncher "+current), 0o755)
	h.opts = update.Options{
		Client:     client(h.fake),
		Dir:        h.dir,
		Current:    current,
		Arch:       "amd64",
		Self:       self,
		InstallDir: filepath.Join(t.TempDir(), "golauncher"),
		Settings:   func() update.Settings { return h.settings },
		Busy:       func(time.Time, bool) string { return h.busy },
		Restart: func(plan string) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.restarts = append(h.restarts, plan)
			return nil
		},
		Notify: func(e update.Event) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.events = append(h.events, e)
		},
		Now: h.clock.Now,
		PID: 4242,
	}
	if edit != nil {
		edit(&h.opts)
	}
	h.s = update.NewService(h.opts)
	return h
}

func (h *harness) tick() { h.s.Tick(h.t.Context()) }

func (h *harness) Events() []update.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]update.Event(nil), h.events...)
}

func TestServiceInstallsAutomatically(t *testing.T) {
	h := newHarness(t, "0.5.0", "0.4.0", nil)
	h.tick()
	if n := h.fake.APIRequests.Load(); n != 0 {
		t.Fatalf("checked %d times before the startup delay", n)
	}
	h.clock.Add(update.StartupDelay)
	h.busy = update.BusyRecent
	h.tick()
	st := h.s.Status()
	if h.fake.APIRequests.Load() != 1 || st.Latest != "0.5.0" || !st.Available || st.Phase != update.PhaseReady || st.Waiting != update.BusyRecent {
		t.Fatalf("after the first check: %+v (requests %d)", st, h.fake.APIRequests.Load())
	}
	if len(h.restarts) != 0 {
		t.Fatal("restarted while busy")
	}
	h.busy = ""
	h.clock.Add(update.TickEvery)
	h.tick()
	if len(h.restarts) != 1 {
		t.Fatalf("restarts = %v", h.restarts)
	}
	if n := h.fake.APIRequests.Load(); n != 1 {
		t.Fatalf("checked again: %d", n)
	}
	plan, err := update.ReadPlan(h.restarts[0])
	if err != nil {
		t.Fatal(err)
	}
	if plan.From != "0.4.0" || plan.To != "0.5.0" || plan.InstallDir != h.opts.InstallDir || plan.PID != 4242 || plan.UpdateDir != h.dir {
		t.Fatalf("plan = %+v", plan)
	}
	exe, _ := os.ReadFile(plan.Staged.Exe())
	if string(exe) != "fake golauncher 0.5.0 amd64" {
		t.Fatalf("staged golauncher.exe = %q", exe)
	}
	helper, _ := os.ReadFile(update.HelperPath(h.restarts[0]))
	if string(helper) != "running golauncher 0.4.0" {
		t.Fatalf("the helper is not a copy of the running program: %q", helper)
	}
	if st := h.s.Status(); st.Phase != update.PhaseInstalling {
		t.Fatalf("phase = %s", st.Phase)
	}
}

func TestServiceChecksDailyWithETag(t *testing.T) {
	h := newHarness(t, "0.4.0", "0.4.0", nil)
	h.clock.Add(update.StartupDelay)
	h.tick()
	if h.fake.APIRequests.Load() != 1 || h.s.Status().Available {
		t.Fatalf("first: %+v", h.s.Status())
	}
	for range 23 {
		h.clock.Add(time.Hour)
		h.tick()
	}
	if n := h.fake.APIRequests.Load(); n != 1 {
		t.Fatalf("checked %d times within a day", n)
	}
	h.clock.Add(time.Hour)
	h.tick()
	if h.fake.APIRequests.Load() != 2 || h.fake.NotModified.Load() != 1 {
		t.Fatalf("after a day: requests %d, 304s %d", h.fake.APIRequests.Load(), h.fake.NotModified.Load())
	}
	if st := h.s.Status(); st.Latest != "0.4.0" || st.LastCheck != h.clock.Now() {
		t.Fatalf("after a 304: %+v", st)
	}
	// A new service (golauncher restarted) reuses the state: no check right
	// after a restart, but at the next start an hour later.
	h2 := update.NewService(h.opts)
	h.clock.Add(update.StartupDelay)
	h2.Tick(t.Context())
	if n := h.fake.APIRequests.Load(); n != 2 {
		t.Fatalf("checked right after a restart: %d", n)
	}
}

func TestServiceWeekly(t *testing.T) {
	h := newHarness(t, "0.4.0", "0.4.0", nil)
	h.settings = update.ParseSettings("auto", "weekly")
	h.clock.Add(update.StartupDelay)
	h.tick()
	h.clock.Add(6 * update.Daily)
	h.tick()
	if n := h.fake.APIRequests.Load(); n != 1 {
		t.Fatalf("checked %d times within a week", n)
	}
	h.clock.Add(update.Daily)
	h.tick()
	if n := h.fake.APIRequests.Load(); n != 2 {
		t.Fatalf("checked %d times after a week", n)
	}
}

func TestServiceRetriesFailedCheck(t *testing.T) {
	h := newHarness(t, "0.5.0", "0.4.0", nil)
	h.settings = update.ParseSettings("notify", "")
	h.fake.Status = http.StatusBadGateway
	h.clock.Add(update.StartupDelay)
	h.tick()
	if st := h.s.Status(); st.Error == "" || !st.LastCheck.IsZero() {
		t.Fatalf("after a failure: %+v", st)
	}
	h.fake.Status = 0
	h.clock.Add(30 * time.Minute)
	h.tick()
	if n := h.fake.APIRequests.Load(); n != 1 {
		t.Fatalf("retried too soon: %d", n)
	}
	h.clock.Add(30 * time.Minute)
	h.tick()
	if st := h.s.Status(); h.fake.APIRequests.Load() != 2 || st.Error != "" || !st.Available {
		t.Fatalf("after the retry: %+v", st)
	}
}

func TestServiceRateLimit(t *testing.T) {
	h := newHarness(t, "0.5.0", "0.4.0", nil)
	h.fake.Status = http.StatusTooManyRequests
	h.fake.Header = http.Header{"Retry-After": {"7200"}}
	h.clock.Add(update.StartupDelay)
	h.tick()
	h.fake.Status = 0
	// Neither the automatic checks nor the user's reach GitHub before the
	// limit ends.
	h.clock.Add(time.Hour + time.Minute)
	h.tick()
	if _, err := h.s.Check(t.Context()); !errors.Is(err, update.ErrRateLimited) {
		t.Fatalf("manual check: err = %v", err)
	}
	if n := h.fake.APIRequests.Load(); n != 1 {
		t.Fatalf("requests while limited: %d", n)
	}
	h.clock.Add(time.Hour)
	h.tick()
	if n := h.fake.APIRequests.Load(); n != 2 {
		t.Fatalf("requests after the limit: %d", n)
	}
}

func TestServiceNotifyMode(t *testing.T) {
	h := newHarness(t, "0.5.0", "0.4.0", nil)
	h.settings = update.ParseSettings("notify", "")
	h.clock.Add(update.StartupDelay)
	h.tick()
	h.clock.Add(update.Daily)
	h.tick()
	ev := h.Events()
	if len(ev) != 1 || ev[0].Kind != update.EventAvailable || ev[0].Version != "0.5.0" || ev[0].Page == "" {
		t.Fatalf("events = %+v", ev)
	}
	if st := h.s.Status(); st.Phase != update.PhaseIdle || !st.Available || len(h.restarts) != 0 {
		t.Fatalf("notify mode downloaded or installed: %+v", st)
	}
	// The user installs it from the settings.
	if err := h.s.InstallNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(h.restarts) != 1 {
		t.Fatalf("restarts = %v", h.restarts)
	}
}

func TestServiceOff(t *testing.T) {
	h := newHarness(t, "0.5.0", "0.4.0", nil)
	h.settings = update.ParseSettings("off", "")
	for range 3 {
		h.clock.Add(update.Daily)
		h.tick()
	}
	if n := h.fake.APIRequests.Load(); n != 0 {
		t.Fatalf("checked %d times with updates off", n)
	}
	// The user may still check.
	st, err := h.s.Check(t.Context())
	if err != nil || !st.Available || st.Latest != "0.5.0" {
		t.Fatalf("manual check: %+v, %v", st, err)
	}
}

func TestServicePortableAndDev(t *testing.T) {
	for _, tc := range []struct {
		name, current, installDir, want string
	}{
		{"portable", "0.4.0", "", update.DisabledPortable},
		{"dev", "dev", "x", update.DisabledDev},
		{"between tags", "0.4.0-3-gabc1234", "x", update.DisabledDev},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, "0.5.0", tc.current, func(o *update.Options) { o.InstallDir = tc.installDir })
			h.clock.Add(update.Daily)
			h.tick()
			if n := h.fake.APIRequests.Load(); n != 0 {
				t.Fatalf("checked automatically: %d", n)
			}
			st, err := h.s.Check(t.Context())
			if err != nil || st.Disabled != tc.want || h.s.CanInstall() {
				t.Fatalf("manual check: %+v, %v", st, err)
			}
			if err := h.s.InstallNow(t.Context()); err == nil || len(h.restarts) != 0 {
				t.Fatal("a copy that does not update itself installed an update")
			}
		})
	}
}

func TestServiceSkip(t *testing.T) {
	h := newHarness(t, "0.5.0", "0.4.0", nil)
	h.busy = update.BusyRecent
	h.clock.Add(update.StartupDelay)
	h.tick()
	if h.s.Status().Phase != update.PhaseReady {
		t.Fatal("not downloaded")
	}
	h.s.Skip("0.5.0")
	h.busy = ""
	h.clock.Add(update.Daily)
	h.tick()
	if st := h.s.Status(); !st.Skipped || st.Phase != update.PhaseIdle || len(h.restarts) != 0 {
		t.Fatalf("a skipped version was installed: %+v", st)
	}
	// A newer release is not skipped.
	h.fake.SetLatest(fakegithub.Options{Version: "0.5.1"})
	h.clock.Add(update.Daily)
	h.tick()
	if len(h.restarts) != 1 {
		t.Fatalf("0.5.1 was not installed: %+v", h.s.Status())
	}
}

func TestServiceRefusesBadDownload(t *testing.T) {
	h := newHarness(t, "0.5.0", "0.4.0", nil)
	name := update.ArchiveName("0.5.0", "amd64")
	h.fake.SetFile(name, fakegithub.Archive(map[string][]byte{"golauncher.exe": []byte("tampered")}))
	h.clock.Add(update.StartupDelay)
	h.tick()
	st := h.s.Status()
	if st.Phase != update.PhaseIdle || st.Error == "" || len(h.restarts) != 0 {
		t.Fatalf("a mismatching download: %+v", st)
	}
	if entries, _ := os.ReadDir(filepath.Join(h.dir, "downloads")); len(entries) != 0 {
		t.Fatalf("the download was kept: %v", entries)
	}
	// Not downloaded again at every tick, but after RetryAfter.
	n := h.fake.Downloads.Load()
	h.clock.Add(update.TickEvery)
	h.tick()
	if got := h.fake.Downloads.Load(); got != n {
		t.Fatalf("downloaded again after a minute: %d -> %d", n, got)
	}
	h.clock.Add(update.RetryAfter)
	h.tick()
	if got := h.fake.Downloads.Load(); got == n {
		t.Fatal("not downloaded again after RetryAfter")
	}
}

func TestServiceChecksFileVersion(t *testing.T) {
	h := newHarness(t, "0.5.0", "0.4.0", func(o *update.Options) {
		o.FileVersion = func(string) (string, error) { return "0.4.9", nil }
	})
	h.clock.Add(update.StartupDelay)
	h.tick()
	if st := h.s.Status(); st.Phase != update.PhaseIdle || st.Error == "" || len(h.restarts) != 0 {
		t.Fatalf("an archive with the wrong version: %+v", st)
	}
}

func TestServiceReportsHelperResult(t *testing.T) {
	writeResult := func(dir string, r update.Result) {
		t.Helper()
		data := `{"id":"` + r.ID + `","from":"` + r.From + `","to":"` + r.To + `","outcome":"` + string(r.Outcome) + `","page":"https://example.test/r","time":"` + r.Time.Format(time.RFC3339) + `"}`
		if err := os.WriteFile(filepath.Join(dir, update.ResultFile), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("updated", func(t *testing.T) {
		h := newHarness(t, "0.5.0", "0.5.0", nil)
		writeResult(h.dir, update.Result{ID: "a", From: "0.4.0", To: "0.5.0", Outcome: update.OutcomeApplying, Time: t0})
		h.tick()
		if len(h.Events()) != 0 {
			t.Fatal("reported while the helper is at work")
		}
		writeResult(h.dir, update.Result{ID: "a", From: "0.4.0", To: "0.5.0", Outcome: update.OutcomeUpdated, Time: t0})
		h.s.Refresh()
		h.s.Refresh() // once only
		ev := h.Events()
		if len(ev) != 1 || ev[0].Kind != update.EventUpdated || ev[0].Version != "0.5.0" || ev[0].From != "0.4.0" || ev[0].Page != "https://example.test/r" {
			t.Fatalf("events = %+v", ev)
		}
		// A new start does not report it again.
		update.NewService(h.opts).Refresh()
		if len(h.Events()) != 1 {
			t.Fatal("reported twice")
		}
	})
	t.Run("rolled back", func(t *testing.T) {
		h := newHarness(t, "0.5.0", "0.4.0", nil)
		writeResult(h.dir, update.Result{ID: "b", From: "0.4.0", To: "0.5.0", Outcome: update.OutcomeRolledBack, Time: t0})
		h.tick()
		ev := h.Events()
		if len(ev) != 1 || ev[0].Kind != update.EventRolledBack {
			t.Fatalf("events = %+v", ev)
		}
		// 0.5.0 is not installed automatically any more.
		h.clock.Add(update.StartupDelay)
		h.tick()
		if st := h.s.Status(); !st.Blocked || st.Phase != update.PhaseIdle || len(h.restarts) != 0 {
			t.Fatalf("a rolled back version: %+v", st)
		}
		// The next version is.
		h.fake.SetLatest(fakegithub.Options{Version: "0.5.1"})
		h.clock.Add(update.Daily)
		h.tick()
		if len(h.restarts) != 1 {
			t.Fatalf("0.5.1: %+v", h.s.Status())
		}
	})
	t.Run("failed three times", func(t *testing.T) {
		h := newHarness(t, "0.5.0", "0.4.0", nil)
		for i, id := range []string{"c1", "c2", "c3"} {
			writeResult(h.dir, update.Result{ID: id, From: "0.4.0", To: "0.5.0", Outcome: update.OutcomeFailed, Time: t0})
			h.s.Refresh()
			if blocked := !h.s.CanInstall() || h.s.Status().Blocked; i < 2 && blocked {
				t.Fatalf("blocked after %d failures", i+1)
			}
		}
		h.clock.Add(update.StartupDelay)
		h.tick()
		if st := h.s.Status(); !st.Blocked || len(h.restarts) != 0 {
			t.Fatalf("after 3 failures: %+v", st)
		}
	})
	t.Run("stale", func(t *testing.T) {
		h := newHarness(t, "0.5.0", "0.4.0", nil)
		writeResult(h.dir, update.Result{ID: "d", From: "0.4.0", To: "0.5.0", Outcome: update.OutcomeApplying, Time: t0.Add(-time.Hour)})
		h.tick()
		if r, _ := update.ReadResult(h.dir); r.ID != "d" {
			t.Fatal("result lost")
		}
		h.clock.Add(update.StartupDelay)
		h.tick()
		if len(h.restarts) != 1 {
			t.Fatalf("a dead helper blocked the update: %+v", h.s.Status())
		}
	})
}

func TestServiceCleansUp(t *testing.T) {
	h := newHarness(t, "0.5.0", "0.4.0", nil)
	for _, p := range []string{"helper/golauncher-update.exe", "downloads/0.3.0/files/golauncher.exe", "healthy-x"} {
		path := filepath.Join(h.dir, p)
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte("x"), 0o644)
	}
	h.tick()
	for _, p := range []string{"helper", "downloads/0.3.0", "healthy-x"} {
		if _, err := os.Stat(filepath.Join(h.dir, p)); err == nil {
			t.Errorf("%s was left", p)
		}
	}
}
