package ui_test

import (
	"errors"
	"path/filepath"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/internal/install"
	"github.com/eziq1/golauncher/internal/ui"
)

// fakeInstallerHost records the installer's requests. Install finishes at
// once with err, after reporting the copy step.
type fakeInstallerHost struct {
	folder   string // what the folder dialog answers
	err      error
	installs []install.Options
	portable int
	canceled int
}

func (h *fakeInstallerHost) ChooseFolder(start string, done func(string, error)) { done(h.folder, nil) }

func (h *fakeInstallerHost) Install(o install.Options, progress func(install.Step), done func(error)) {
	h.installs = append(h.installs, o)
	progress(install.StepCopying)
	done(h.err)
}

func (h *fakeInstallerHost) RunPortable() { h.portable++ }
func (h *fakeInstallerHost) Cancel()      { h.canceled++ }

func coreLocalizer(t *testing.T) *i18n.Localizer {
	t.Helper()
	m := i18n.NewManager(i18n.Source, nil)
	if err := m.Register(i18n.CoreNamespace, i18n.CoreLocales()); err != nil {
		t.Fatal(err)
	}
	return m.Localizer(i18n.CoreNamespace)
}

func newInstaller(t *testing.T, h *fakeInstallerHost, info ui.InstallerInfo) (*ui.Installer, *mui.Tester) {
	t.Helper()
	in := ui.NewInstaller(coreLocalizer(t), h, info)
	tt := mui.NewTester(in.View, ui.InstallerWidth, ui.InstallerHeight)
	tt.Frame()
	return in, tt
}

func click(t *testing.T, tt *mui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatalf("click %q: %v (texts %q)", label, err, tt.Texts())
	}
	tt.Frame()
}

func wantTexts(t *testing.T, tt *mui.Tester, texts ...string) {
	t.Helper()
	for _, s := range texts {
		if !tt.HasText(s) {
			t.Fatalf("no %q in %q", s, tt.Texts())
		}
	}
}

func TestInstallerFresh(t *testing.T) {
	dir := filepath.Join(`C:\Users\me\AppData\Local\Programs`, "golauncher")
	h := &fakeInstallerHost{folder: `D:\Apps`}
	in, tt := newInstaller(t, h, ui.InstallerInfo{Version: "0.2.0", State: install.State{Kind: install.KindFresh}, Dir: dir})
	wantTexts(t, tt, "Install golauncher 0.2.0", "Install location", "Launch at login", "Create a desktop shortcut", "Run without installing")

	click(t, tt, "Create a desktop shortcut")
	click(t, tt, "Browse…")
	click(t, tt, "Install")
	if len(h.installs) != 1 {
		t.Fatalf("installs = %+v", h.installs)
	}
	o := h.installs[0]
	if o.Dir != filepath.Join(`D:\Apps`, "golauncher") || !o.LaunchAtLogin || !o.DesktopShortcut || o.Upgrade || o.Description == "" {
		t.Fatalf("options = %+v", o)
	}
	if !in.Busy() {
		t.Fatal("not busy after installing")
	}
	wantTexts(t, tt, "Starting golauncher…")
	click(t, tt, "Install") // disabled now
	if len(h.installs) != 1 {
		t.Fatal("installed twice")
	}
}

func TestInstallerErrors(t *testing.T) {
	h := &fakeInstallerHost{err: install.ErrStillRunning}
	in, tt := newInstaller(t, h, ui.InstallerInfo{Version: "0.2.0", State: install.State{Kind: install.KindFresh}, Dir: `C:\x\golauncher`})
	click(t, tt, "Launch at login")
	click(t, tt, "Install")
	if o := h.installs[0]; o.LaunchAtLogin || o.DesktopShortcut {
		t.Fatalf("options = %+v", o)
	}
	if in.Busy() {
		t.Fatal("busy after a failure")
	}
	wantTexts(t, tt, "golauncher is still running and did not quit. Quit it from its tray icon, then try again.")

	h.err = errors.New("disk full")
	click(t, tt, "Install")
	wantTexts(t, tt, "Installation failed: disk full")

	click(t, tt, "Run without installing")
	click(t, tt, "Cancel")
	if h.portable != 1 || h.canceled != 1 {
		t.Fatalf("portable %d, canceled %d", h.portable, h.canceled)
	}
}

func TestInstallerChange(t *testing.T) {
	for _, tc := range []struct {
		order   int
		ordered bool
		title   string
		prompt  string
		button  string
	}{
		{1, true, "Upgrade golauncher", "golauncher 0.1.0 is installed. Upgrade to 0.2.0?", "Upgrade"},
		{-1, true, "Downgrade golauncher", "golauncher 0.1.0 is installed. Downgrade to 0.2.0?", "Downgrade"},
		{0, false, "Replace golauncher", "golauncher 0.1.0 is installed. Replace it with 0.2.0?", "Replace"},
	} {
		h := &fakeInstallerHost{}
		st := install.State{Kind: install.KindChange, Installed: true, Dir: `C:\p\golauncher`, Version: "0.1.0", Order: tc.order, Ordered: tc.ordered}
		_, tt := newInstaller(t, h, ui.InstallerInfo{Version: "0.2.0", State: st, Dir: `C:\default`})
		wantTexts(t, tt, tc.title, tc.prompt, `Installed in C:\p\golauncher`, "Run without installing")
		if tt.HasText("Install location") {
			t.Fatal("an upgrade asks for the location")
		}
		click(t, tt, tc.button)
		if len(h.installs) != 1 || h.installs[0].Dir != st.Dir || !h.installs[0].Upgrade {
			t.Fatalf("%s: installs = %+v", tc.button, h.installs)
		}
	}
}
