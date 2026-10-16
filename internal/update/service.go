package update

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"
)

// Why a copy does not update itself (Status.Disabled).
const (
	DisabledPortable = "portable" // not the installed copy
	DisabledDev      = "dev"      // a development build
	DisabledEnv      = "env"      // GOLAUNCHER_NO_UPDATE=1
)

// EnvNoUpdate turns automatic updates off when set to 1 (tests, E2E).
const EnvNoUpdate = "GOLAUNCHER_NO_UPDATE"

// EnvAPI replaces the GitHub API's address (HTTPS, or HTTP to this
// computer), to rehearse updates with a fake server.
const EnvAPI = "GOLAUNCHER_UPDATE_API"

// Phase is what the updater is doing.
type Phase string

const (
	PhaseIdle        Phase = "idle"
	PhaseChecking    Phase = "checking"
	PhaseDownloading Phase = "downloading"
	PhaseReady       Phase = "ready"      // an update is downloaded and waits for a safe moment
	PhaseInstalling  Phase = "installing" // the helper was started; golauncher quits
)

// Status is the updater's state for the settings.
type Status struct {
	Phase   Phase
	Current string
	// Latest is the newest release known ("" before a check) and Page its
	// web page; Available reports that it is newer than Current.
	Latest    string
	Page      string
	Available bool
	// Skipped: the user skipped Latest. Blocked: Latest was rolled back or
	// failed to install too often; it is not installed automatically.
	Skipped bool
	Blocked bool
	// Progress of the download, 0 to 1 (-1 unknown).
	Progress float64
	// LastCheck is when a check last succeeded; Error why the last check or
	// download failed.
	LastCheck time.Time
	Error     string
	// Next is when the next automatic check is due (zero before the first
	// tick, or without automatic checks).
	Next time.Time
	// Disabled says why this copy does not update itself ("" when it does).
	Disabled string
	// Waiting says why a ready update waits (a SafeMoment reason).
	Waiting string
}

// EventKind is what Notify tells.
type EventKind string

const (
	EventAvailable  EventKind = "available"  // a new version, found by an automatic check in notify mode
	EventUpdated    EventKind = "updated"    // golauncher runs the new version
	EventRolledBack EventKind = "rolledBack" // the new version did not start
)

// Event is something to tell the user.
type Event struct {
	Kind    EventKind
	Version string // the new version
	From    string // the version before (EventUpdated, EventRolledBack)
	Page    string // the release's web page
	Err     string
}

// Options configure a Service.
type Options struct {
	Client *Client
	// Dir is the update folder (%LOCALAPPDATA%\golauncher\update).
	Dir string
	// Current is the running version; Arch its GOARCH (default runtime's).
	Current string
	Arch    string
	// Self is the running executable, copied to make the helper.
	Self string
	// InstallDir is the installation, when the running copy is the
	// installed one ("" otherwise: a portable copy).
	InstallDir string
	// Disabled forces Status.Disabled (e.g. DisabledEnv); otherwise it is
	// derived from InstallDir and Current.
	Disabled string
	// PublicKey requires signed checksums (default: none).
	PublicKey string
	// Settings returns the user's settings.
	Settings func() Settings
	// FileVersion reads the version resource of an executable; nil skips
	// that check.
	FileVersion func(exe string) (string, error)
	// Busy returns why golauncher should not restart now ("" when it may);
	// user is set when the user asked to restart. Nil: always.
	Busy func(now time.Time, user bool) string
	// Restart starts the helper with the plan file and quits golauncher.
	Restart func(planFile string) error
	// Notify tells the user something; it may be called from any goroutine.
	Notify func(Event)
	Now    func() time.Time
	Log    *slog.Logger
	// PID is this process (default os.Getpid()).
	PID int
}

// Service checks for, downloads and installs updates in the background.
type Service struct {
	o       Options
	started time.Time

	checkMu sync.Mutex // one check at a time
	mu      sync.Mutex
	state   State
	status  Status
	busyDL  bool // a download runs
	cleaned bool // leftovers were removed once
	changes []func()
}

// NewService returns the updater of o; Start runs it.
func NewService(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Settings == nil {
		o.Settings = func() Settings { return ParseSettings("", "") }
	}
	if o.PID == 0 {
		o.PID = os.Getpid()
	}
	if o.Disabled == "" {
		switch {
		case !Updatable(o.Current):
			o.Disabled = DisabledDev
		case o.InstallDir == "":
			o.Disabled = DisabledPortable
		}
	}
	s := &Service{o: o, started: o.Now()}
	s.state = loadState(s.path(stateFile))
	s.status = Status{Phase: PhaseIdle, Current: o.Current, Disabled: o.Disabled}
	s.refreshLocked()
	return s
}

func (s *Service) path(name ...string) string {
	return filepath.Join(append([]string{s.o.Dir}, name...)...)
}

// CanInstall reports whether this copy updates itself.
func (s *Service) CanInstall() bool { return s.o.Disabled == "" }

// OnChange calls fn (from any goroutine) after the status changed.
func (s *Service) OnChange(fn func()) {
	s.mu.Lock()
	s.changes = append(s.changes, fn)
	s.mu.Unlock()
}

// Status returns the current status.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// update changes the state and the status under s.mu, saves the state and
// tells the listeners.
func (s *Service) update(fn func(st *State, status *Status)) {
	s.mu.Lock()
	fn(&s.state, &s.status)
	s.refreshLocked()
	if err := writeJSON(s.path(stateFile), s.state); err != nil {
		s.o.Log.Warn("update: saving the state", "err", err)
	}
	changes := slices.Clone(s.changes)
	s.mu.Unlock()
	for _, fn := range changes {
		fn()
	}
}

// setProgress reports the download's progress (the state is not saved).
func (s *Service) setProgress(p float64) {
	s.mu.Lock()
	s.status.Progress = p
	changes := slices.Clone(s.changes)
	s.mu.Unlock()
	for _, fn := range changes {
		fn()
	}
}

// refreshLocked derives the status from the state.
func (s *Service) refreshLocked() {
	st, status := &s.state, &s.status
	status.LastCheck = st.LastCheck
	status.Latest, status.Page, status.Available = "", "", false
	if st.Latest != nil {
		if v, err := st.Latest.Version(); err == nil {
			status.Latest, status.Page = v, st.Latest.Page()
			status.Available = Newer(v, s.o.Current)
		}
	}
	status.Skipped = status.Available && st.Skipped == status.Latest
	status.Blocked = status.Available && st.blocked(status.Latest)
	if st.Staged != nil && !Newer(st.Staged.Version, s.o.Current) {
		st.Staged = nil // installed meanwhile, or older
	}
	if status.Phase == PhaseIdle || status.Phase == PhaseReady {
		status.Phase = PhaseIdle
		if st.Staged != nil && s.CanInstall() {
			status.Phase = PhaseReady
		}
	}
}

// Start reports what the update helper did, cleans up after it and runs
// the automatic checks until ctx ends. It returns at once.
func (s *Service) Start(ctx context.Context) {
	go func() {
		s.Tick(ctx)
		t := time.NewTicker(TickEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.Tick(ctx)
			}
		}
	}()
}

// Tick does what is due: it reports the helper's result, checks for
// updates when a check is due, downloads a new version and installs a
// ready one at a safe moment, as the settings say. Start calls it every
// TickEvery; tests call it with a fake clock.
func (s *Service) Tick(ctx context.Context) {
	s.report()
	if !s.CanInstall() {
		return
	}
	settings := s.o.Settings()
	if settings.Mode == ModeOff {
		return
	}
	now := s.o.Now()
	s.mu.Lock()
	due := NextCheck(s.state, settings, s.started, now)
	s.status.Next = due
	s.mu.Unlock()
	if !now.Before(due) {
		_, _ = s.check(ctx, false)
	}
	if settings.Mode != ModeAuto {
		return
	}
	s.download(ctx, false)
	s.install(false)
}

// Check checks for updates as the user asked. With automatic updates, a
// new version is then downloaded in the background.
func (s *Service) Check(ctx context.Context) (Status, error) {
	st, err := s.check(ctx, true)
	if err == nil && s.CanInstall() && s.o.Settings().Mode == ModeAuto {
		go s.download(context.WithoutCancel(ctx), false)
	}
	return st, err
}

// check asks GitHub for the latest release.
func (s *Service) check(ctx context.Context, user bool) (Status, error) {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	now := s.o.Now()
	s.mu.Lock()
	etag, limited := s.state.ETag, s.state.LimitedUntil
	s.mu.Unlock()
	if now.Before(limited) {
		return s.Status(), &RateLimitError{Until: limited}
	}
	s.update(func(_ *State, st *Status) {
		if st.Phase == PhaseIdle {
			st.Phase = PhaseChecking
		}
	})
	cctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	res, err := s.o.Client.Latest(cctx, etag)
	cancel()
	var notify *Event
	s.update(func(st *State, status *Status) {
		if status.Phase == PhaseChecking {
			status.Phase = PhaseIdle
		}
		st.LastAttempt = now
		if err != nil {
			st.LastError = err.Error()
			status.Error = st.LastError
			if rl, ok := errors.AsType[*RateLimitError](err); ok {
				st.LimitedUntil = rl.Until
			}
			return
		}
		st.LastCheck, st.LastError, status.Error, st.LimitedUntil = now, "", "", time.Time{}
		if !res.NotModified {
			r := res.Release
			st.Latest, st.ETag = &r, res.ETag
		}
		if st.Latest == nil {
			return
		}
		v, verr := st.Latest.Version()
		if verr != nil || !Newer(v, s.o.Current) {
			return
		}
		// The user who asked sees the answer; otherwise, in notify mode, a
		// new version is announced once.
		if !user && s.o.Settings().Mode == ModeNotify && st.Notified != v && st.Skipped != v && s.CanInstall() {
			st.Notified = v
			notify = &Event{Kind: EventAvailable, Version: v, Page: st.Latest.Page()}
		}
	})
	status := s.Status()
	if notify != nil {
		s.notify(*notify)
	}
	if err == nil {
		s.o.Log.Info("update: checked", "latest", status.Latest, "current", s.o.Current, "cached", res.NotModified)
	} else {
		s.o.Log.Warn("update: check failed", "err", err)
	}
	return status, err
}

// download fetches the latest release when it is newer and may be
// installed (user: the user asked, so a skipped version is fine). It does
// nothing when a download runs or the update is ready.
func (s *Service) download(ctx context.Context, user bool) {
	s.mu.Lock()
	st := s.state
	status := s.status
	retryLater := st.FailedDownload == status.Latest && s.o.Now().Before(st.FailedAt.Add(RetryAfter))
	if s.busyDL || !s.CanInstall() || !status.Available || st.Latest == nil ||
		st.Staged != nil && st.Staged.Version == status.Latest ||
		!user && (status.Skipped || status.Blocked || retryLater) {
		s.mu.Unlock()
		return
	}
	s.busyDL = true
	release := *st.Latest
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busyDL = false
		s.mu.Unlock()
	}()

	s.update(func(_ *State, st *Status) { st.Phase, st.Progress, st.Error = PhaseDownloading, 0, "" })
	version := status.Latest
	dir := s.path(downloadsDir, version)
	res, err := s.o.Client.Prepare(ctx, release, PrepareOptions{
		Arch:      s.o.Arch,
		Dir:       dir,
		PublicKey: s.o.PublicKey,
		Progress: func(done, total int64) {
			p := -1.0
			if total > 0 {
				p = float64(done) / float64(total)
			}
			s.setProgress(p)
		},
	})
	if err == nil && s.o.FileVersion != nil {
		if got, ferr := s.o.FileVersion(res.Staged.Exe()); ferr != nil || got != version {
			err = fmt.Errorf("update: the downloaded golauncher.exe is version %q, not %s (%v)", got, version, ferr)
		}
	}
	if err != nil {
		s.o.Log.Warn("update: download failed", "version", version, "err", err)
		_ = removeAll(dir)
		now := s.o.Now()
		s.update(func(st *State, status *Status) {
			status.Phase, status.Error = PhaseIdle, err.Error()
			st.FailedDownload, st.FailedAt = version, now // retried after RetryAfter
		})
		return
	}
	s.o.Log.Info("update: ready", "version", version, "sha256", res.SHA256, "signed", res.Signed)
	staged := res.Staged
	s.update(func(st *State, status *Status) {
		st.Staged = &staged
		status.Phase, status.Progress = PhaseIdle, 1
	})
}

// InstallNow downloads the latest version if needed and installs it at
// once, as the user asked: it waits only for background tasks and AI
// answers (the reason is in Status.Waiting).
func (s *Service) InstallNow(ctx context.Context) error {
	s.update(func(st *State, status *Status) {
		if st.Skipped == status.Latest {
			st.Skipped = ""
		}
	})
	s.download(ctx, true)
	if s.Status().Phase != PhaseReady {
		if e := s.Status().Error; e != "" {
			return errors.New(e)
		}
		return errors.New("update: nothing to install")
	}
	return s.install(true)
}

// install starts the helper for the ready update when it is a safe
// moment.
func (s *Service) install(user bool) error {
	s.mu.Lock()
	staged := s.state.Staged
	phase := s.status.Phase
	s.mu.Unlock()
	if staged == nil || phase != PhaseReady {
		return nil
	}
	if s.o.Busy != nil {
		if why := s.o.Busy(s.o.Now(), user); why != "" {
			s.update(func(_ *State, st *Status) { st.Waiting = why })
			return nil
		}
	}
	s.update(func(_ *State, st *Status) { st.Waiting, st.Phase = "", PhaseInstalling })
	err := s.startHelper(*staged)
	if err != nil {
		s.o.Log.Error("update: starting the helper", "err", err)
		s.update(func(st *State, status *Status) {
			status.Phase, status.Error = PhaseIdle, err.Error()
			st.Attempts = addAttempt(st.Attempts, staged.Version)
			if st.blocked(staged.Version) {
				st.Staged = nil
			}
		})
	}
	return err
}

// startHelper copies this program as the helper, writes the plan and has
// Restart start the helper and quit.
func (s *Service) startHelper(staged Staged) error {
	if _, err := os.Stat(staged.Exe()); err != nil {
		return fmt.Errorf("update: the downloaded update is gone: %w", err)
	}
	dir := s.path(helperDir)
	if err := removeAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	helper := filepath.Join(dir, HelperName)
	if err := copyFile(s.o.Self, helper); err != nil {
		return err
	}
	plan := Plan{
		ID:         strconv.FormatInt(s.o.Now().UnixNano(), 36),
		InstallDir: s.o.InstallDir,
		From:       s.o.Current,
		To:         staged.Version,
		Staged:     staged,
		UpdateDir:  s.o.Dir,
		PID:        s.o.PID,
	}
	planPath := filepath.Join(dir, planFile)
	if err := writeJSON(planPath, plan); err != nil {
		return err
	}
	s.o.Log.Info("update: installing", "from", plan.From, "to", plan.To)
	if s.o.Restart == nil {
		return errors.New("update: cannot restart")
	}
	return s.o.Restart(planPath)
}

// HelperPath returns the helper executable that goes with a plan file
// (Options.Restart starts it with FlagApply and the plan).
func HelperPath(planFile string) string { return filepath.Join(filepath.Dir(planFile), HelperName) }

func addAttempt(m map[string]int, v string) map[string]int {
	if m == nil {
		m = map[string]int{}
	}
	m[v]++
	return m
}

// Skip skips version: it is not downloaded or installed automatically.
func (s *Service) Skip(version string) {
	s.update(func(st *State, _ *Status) {
		st.Skipped = version
		if st.Staged != nil && st.Staged.Version == version {
			st.Staged = nil
		}
	})
}

// Refresh reports a result of the update helper that arrived since the
// last tick (golauncher calls it after it reported itself healthy).
func (s *Service) Refresh() { s.report() }

// staleApplying is how long a result may say "applying" before the helper
// is taken for dead.
const staleApplying = 10 * time.Minute

// report tells the user what the helper did, once, and cleans up after it.
func (s *Service) report() {
	r, ok := ReadResult(s.o.Dir)
	s.mu.Lock()
	reported := s.state.Reported
	s.mu.Unlock()
	if !ok || r.ID == reported {
		s.cleanup(false)
		return
	}
	if r.Outcome == OutcomeApplying {
		if s.o.Now().Sub(r.Time) < staleApplying {
			return // the helper is at work, e.g. waiting for this process to be healthy
		}
		r.Outcome, r.Error = OutcomeFailed, cmp.Or(r.Error, "the update helper stopped")
	}
	var ev *Event
	s.update(func(st *State, _ *Status) {
		st.Reported = r.ID
		switch r.Outcome {
		case OutcomeUpdated:
			st.Staged = nil
			if r.To == s.o.Current {
				ev = &Event{Kind: EventUpdated, Version: r.To, From: r.From, Page: r.Page}
			}
		case OutcomeRolledBack:
			st.Staged = nil
			if !slices.Contains(st.Bad, r.To) {
				st.Bad = append(st.Bad, r.To)
			}
			ev = &Event{Kind: EventRolledBack, Version: r.To, From: r.From, Page: r.Page, Err: r.Error}
		case OutcomeFailed:
			st.Attempts = addAttempt(st.Attempts, r.To)
			if st.blocked(r.To) {
				st.Staged = nil
			}
		}
	})
	s.o.Log.Info("update: helper result", "from", r.From, "to", r.To, "outcome", r.Outcome, "err", r.Error)
	if ev != nil {
		s.notify(*ev)
	}
	s.cleanup(true)
}

// cleanup removes the helper and the downloads other than the ready
// update's (always is false on every tick: only once per start).
func (s *Service) cleanup(always bool) {
	s.mu.Lock()
	if s.cleaned && !always {
		s.mu.Unlock()
		return
	}
	s.cleaned = true
	keep := ""
	if s.state.Staged != nil {
		keep = s.state.Staged.Version
	}
	s.mu.Unlock()
	_ = removeAll(s.path(helperDir))
	entries, _ := os.ReadDir(s.path(downloadsDir))
	for _, e := range entries {
		if e.Name() != keep {
			_ = removeAll(s.path(downloadsDir, e.Name()))
		}
	}
	if r, ok := ReadResult(s.o.Dir); !ok || r.Outcome != OutcomeApplying {
		matches, _ := filepath.Glob(s.path("healthy-*"))
		for _, m := range matches {
			_ = os.Remove(m)
		}
	}
}

func (s *Service) notify(e Event) {
	if s.o.Notify != nil {
		s.o.Notify(e)
	}
}
