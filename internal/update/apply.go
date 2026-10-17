package update

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/eziq1/golauncher/internal/install"
)

// EnvHealth names the file a golauncher started by the update helper
// writes once it runs well (MarkHealthy).
const EnvHealth = "GOLAUNCHER_UPDATE_HEALTH"

// HealthyAfter is how long a new version must run before it reports itself
// healthy.
var HealthyAfter = 10 * time.Second

// FlagApply is the helper's command line flag: --apply-update <plan.json>.
const FlagApply = "--apply-update"

// Plan is what the helper is asked to do (plan.json).
type Plan struct {
	ID         string `json:"id"`
	InstallDir string `json:"installDir"`
	From       string `json:"from"`
	To         string `json:"to"`
	Staged     Staged `json:"staged"`
	// UpdateDir is the update folder: result.json, previous\, the health
	// marker.
	UpdateDir string `json:"updateDir"`
	// PID is the golauncher that hands over; the helper waits for it to
	// quit.
	PID int `json:"pid"`
	// Args start golauncher (default --background).
	Args []string `json:"args,omitempty"`
}

// ReadPlan reads a plan file.
func ReadPlan(path string) (Plan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Plan{}, err
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return Plan{}, fmt.Errorf("update: reading %s: %w", path, err)
	}
	if p.ID == "" || p.InstallDir == "" || p.To == "" || p.Staged.Dir == "" || p.UpdateDir == "" {
		return Plan{}, fmt.Errorf("update: %s is incomplete", path)
	}
	return p, nil
}

// Process is a started program.
type Process interface {
	// Exited is closed when the program has ended.
	Exited() <-chan struct{}
	Kill() error
}

// HelperSystem is what the helper needs from the system.
type HelperSystem interface {
	// WaitExit waits until process pid has ended (at once if there is
	// none); it fails after timeout.
	WaitExit(pid int, timeout time.Duration) error
	// Start starts exe with args, with env added to this process's
	// environment.
	Start(exe string, args []string, env []string) (Process, error)
}

// Helper installs an update in its own process (docs/auto-update.md §5).
type Helper struct {
	Sys HelperSystem
	// NewInstaller returns the self-installer of the copy self of version.
	NewInstaller func(self, version string) *install.Installer
	Log          *slog.Logger
	Now          func() time.Time
	// WaitOld bounds the wait for the old golauncher to quit; Health the
	// wait for the new one to report itself healthy; Poll is how often the
	// marker is looked for.
	WaitOld, Health, Poll time.Duration
}

// Apply carries out p: it waits for the old golauncher to quit, keeps a
// copy of the installed program, installs the update with the
// self-installer, starts it and waits for it to report itself healthy.
// When it does not, the copy is installed again and started. Apply records
// the outcome in result.json and returns it.
func (h *Helper) Apply(ctx context.Context, p Plan) Result {
	log := h.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := h.Now
	if now == nil {
		now = time.Now
	}
	waitOld, health, poll := cmpDur(h.WaitOld, 30*time.Second), cmpDur(h.Health, 90*time.Second), cmpDur(h.Poll, 250*time.Millisecond)
	args := p.Args
	if len(args) == 0 {
		args = []string{"--background"}
	}
	res := Result{ID: p.ID, From: p.From, To: p.To, Outcome: OutcomeApplying, Page: p.Staged.Page, Time: now()}
	save := func() {
		res.Time = now()
		if err := writeJSON(filepath.Join(p.UpdateDir, ResultFile), res); err != nil {
			log.Error("update: writing the result", "err", err)
		}
	}
	save()
	bundle := p.Staged.Bundle
	installed := programIn(p.InstallDir, bundle)
	newInstaller := func(self, version string) *install.Installer {
		in := h.NewInstaller(self, version)
		in.Bundle = in.Bundle || bundle
		return in
	}
	restartOld := func() {
		if _, err := h.Sys.Start(installed, args, nil); err != nil {
			log.Error("update: starting golauncher again", "err", err)
		}
	}
	fail := func(err error) Result {
		log.Error("update: not installed", "to", p.To, "err", err)
		res.Outcome, res.Error = OutcomeFailed, err.Error()
		save()
		restartOld()
		return res
	}

	// 1. The old golauncher quits.
	if p.PID > 0 {
		if err := h.Sys.WaitExit(p.PID, waitOld); err != nil {
			res.Outcome, res.Error = OutcomeFailed, "golauncher did not quit: "+err.Error()
			save()
			return res // it still runs: nothing to start
		}
	}
	// 2. A copy of the installed program, to roll back to.
	prev := filepath.Join(p.UpdateDir, previousDir)
	if err := backup(p.InstallDir, prev, bundle); err != nil {
		return fail(fmt.Errorf("keeping a copy of the installed version: %w", err))
	}
	// 3. Install with the self-installer (it restores the files itself on
	// failure).
	if _, err := newInstaller(p.Staged.Exe(), p.To).Install(install.Options{Dir: p.InstallDir, Upgrade: true}, nil); err != nil {
		return fail(err)
	}
	log.Info("update: installed", "from", p.From, "to", p.To)

	// 4. Start it and wait for it to report itself healthy.
	marker := filepath.Join(p.UpdateDir, "healthy-"+p.ID)
	_ = os.Remove(marker)
	proc, err := h.Sys.Start(installed, args, []string{EnvHealth + "=" + marker})
	if err == nil {
		err = waitHealthy(ctx, proc, marker, health, poll)
	}
	_ = os.Remove(marker)
	if err == nil {
		res.Outcome = OutcomeUpdated
		save()
		log.Info("update: the new version runs", "version", p.To)
		return res
	}

	// 5. Roll back.
	log.Error("update: the new version did not start; rolling back", "version", p.To, "err", err)
	res.Error = err.Error()
	if proc != nil {
		_ = proc.Kill()
		select {
		case <-proc.Exited():
		case <-time.After(5 * time.Second):
		}
	}
	if _, rerr := newInstaller(programIn(prev, bundle), p.From).Install(install.Options{Dir: p.InstallDir, Upgrade: true}, nil); rerr != nil {
		res.Error += "; rolling back: " + rerr.Error()
		log.Error("update: rolling back", "err", rerr)
	}
	res.Outcome = OutcomeRolledBack
	save()
	restartOld()
	return res
}

// waitHealthy waits for proc to write marker. It fails when proc ends
// first or after timeout.
func waitHealthy(ctx context.Context, proc Process, marker string, timeout, poll time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		if exists(marker) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-proc.Exited():
			if exists(marker) {
				return nil
			}
			return errors.New("the new version quit before it was ready")
		case <-deadline.C:
			return fmt.Errorf("the new version was not ready after %s", timeout)
		case <-tick.C:
		}
	}
}

// backup copies the program files of dir (with bundle, its
// golauncher.app) into prev, replacing what was there.
func backup(dir, prev string, bundle bool) error {
	if err := removeAll(prev); err != nil {
		return err
	}
	if err := os.MkdirAll(prev, 0o755); err != nil {
		return err
	}
	if bundle {
		app := install.BundleIn(dir)
		if !exists(install.BundleExe(app)) {
			return fmt.Errorf("%s is missing", install.BundleExe(app))
		}
		return install.CopyTree(app, install.BundleIn(prev))
	}
	for _, name := range []string{install.ExeName, install.CtlName} {
		src := filepath.Join(dir, name)
		if !exists(src) {
			if name == install.ExeName {
				return fmt.Errorf("%s is missing", src)
			}
			continue
		}
		if err := copyFile(src, filepath.Join(prev, name)); err != nil {
			return err
		}
	}
	return nil
}

// HealthMarker returns the marker file the update helper asked for
// (EnvHealth), "" when golauncher was not started by the helper, and
// removes the variable from the environment, so that programs golauncher
// starts do not inherit it. golauncher calls it first thing and
// WriteMarker once it has run well for HealthyAfter.
func HealthMarker() string {
	path := os.Getenv(EnvHealth)
	_ = os.Unsetenv(EnvHealth)
	return path
}

// WriteMarker writes the health marker at path ("" for none).
func WriteMarker(path string) error {
	if path == "" {
		return nil
	}
	return os.WriteFile(path, []byte("ok\n"), 0o644)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Sync(), out.Close())
}

func cmpDur(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}
