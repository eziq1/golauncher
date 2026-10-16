package update_test

import (
	"context"
	"errors"
	"testing"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/update"
	"github.com/eziq1/golauncher/pkg/plugin"
)

type fakeChecker struct {
	st  update.Status
	err error
}

func (f fakeChecker) Check(context.Context) (update.Status, error) { return f.st, f.err }
func (f fakeChecker) CanInstall() bool                             { return f.st.Disabled == "" }

func TestPluginCheck(t *testing.T) {
	for _, tc := range []struct {
		name  string
		c     fakeChecker
		mode  update.Mode
		style plugin.ToastStyle
		title string
		msg   string
	}{
		{"up to date", fakeChecker{st: update.Status{Current: "0.5.0", Latest: "0.5.0"}}, update.ModeAuto, plugin.ToastSuccess,
			"golauncher is up to date", "Version 0.5.0 is the latest."},
		{"auto", fakeChecker{st: update.Status{Current: "0.4.0", Latest: "0.5.0", Available: true}}, update.ModeAuto, plugin.ToastInfo,
			"golauncher 0.5.0 is available", "It is being downloaded and installs when golauncher is idle."},
		{"notify", fakeChecker{st: update.Status{Current: "0.4.0", Latest: "0.5.0", Available: true}}, update.ModeNotify, plugin.ToastInfo,
			"golauncher 0.5.0 is available", "Install it in Settings › About."},
		{"portable", fakeChecker{st: update.Status{Current: "0.4.0", Latest: "0.5.0", Available: true, Disabled: update.DisabledPortable}}, update.ModeAuto, plugin.ToastInfo,
			"golauncher 0.5.0 is available", "This copy does not update itself: download the new version from GitHub."},
		{"failed", fakeChecker{err: errors.New("offline")}, update.ModeAuto, plugin.ToastFailure,
			"Couldn't check for updates", "offline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := update.NewPlugin(tc.c, func() update.Mode { return tc.mode })
			h, err := hostfake.New(p, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Init(t.Context(), h); err != nil {
				t.Fatal(err)
			}
			res, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: update.CommandCheck})
			if err != nil || res.Toast == nil {
				t.Fatalf("result = %+v, %v", res, err)
			}
			if got := *res.Toast; got.Style != tc.style || got.Title != tc.title || got.Message != tc.msg {
				t.Fatalf("toast = %+v", got)
			}
		})
	}
	m := update.NewPlugin(fakeChecker{}, nil).Manifest()
	if m.ID != update.PluginID || len(m.Commands) != 1 || m.Commands[0].Mode != plugin.ModeNoView {
		t.Fatalf("manifest = %+v", m)
	}
}
