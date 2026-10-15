package ui

import (
	"errors"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/internal/install"
)

// Installer window size, in DIPs.
const (
	InstallerWidth  = 560
	InstallerHeight = 440
)

// InstallerHost carries out what the installer view asks for. Its methods
// are called on the UI thread; the callbacks must run there too.
type InstallerHost interface {
	// ChooseFolder asks for an install location, starting at start, and
	// returns at once; done runs later with the folder, or "" when the
	// user canceled.
	ChooseFolder(start string, done func(dir string, err error))
	// Install installs with o and returns at once. progress reports each
	// step; done runs at the end. On success the host starts the installed
	// program and quits.
	Install(o install.Options, progress func(install.Step), done func(err error))
	// RunPortable runs this copy without installing it; Cancel quits.
	RunPortable()
	Cancel()
}

// InstallerInfo describes what the installer offers.
type InstallerInfo struct {
	// Version is the running version.
	Version string
	// State is the installation found (install.KindFresh or KindChange).
	State install.State
	// Dir is the suggested install location of a new installation.
	Dir string
}

// Installer is the installer window's view (docs/self-install.md): a new
// installation, or an upgrade or downgrade of the installed version.
type Installer struct {
	loc  *i18n.Localizer
	host InstallerHost
	info InstallerInfo

	dir           string
	launchAtLogin bool
	desktop       bool

	busy    bool
	started bool         // installed; the host starts the program
	step    install.Step // while busy
	err     string
}

// NewInstaller returns the installer view.
func NewInstaller(loc *i18n.Localizer, host InstallerHost, info InstallerInfo) *Installer {
	return &Installer{loc: loc, host: host, info: info, dir: info.Dir, launchAtLogin: true}
}

// Busy reports whether an installation is under way; the window should not
// close then. UI thread only.
func (in *Installer) Busy() bool { return in.busy || in.started }

// change reports whether an installation is being replaced.
func (in *Installer) change() bool { return in.info.State.Kind == install.KindChange }

// direction is "upgrade", "downgrade" or "replace" (unknown order).
func (in *Installer) direction() string {
	st := in.info.State
	switch {
	case !st.Ordered:
		return "replace"
	case st.Order < 0:
		return "downgrade"
	}
	return "upgrade"
}

// View builds the installer window.
func (in *Installer) View(c *mui.Context) {
	t := c.Theme()
	c.Root().Background(t.Background)
	mui.Column(c).Fill().Padding(24, 28).Gap(14).Children(func() {
		mui.Row(c).Gap(14).AlignItems(mui.Center).Children(func() {
			mui.Box(c).Size(48, 48).Center().Radius(12).Background(t.Accent).Children(func() {
				mui.Icon(c, builtinIcon("command")).FontSize(26).TextColor(t.AccentText)
			})
			mui.Text(c, in.title()).FontSize(20).Bold().Level(1)
		})
		if in.change() {
			in.changeForm(c)
		} else {
			in.freshForm(c)
		}
		mui.Spacer(c)
		in.status(c)
		in.buttons(c)
	})
}

func (in *Installer) title() string {
	if !in.change() {
		return in.loc.T("install.title.fresh", "version", in.info.Version)
	}
	switch in.direction() {
	case "downgrade":
		return in.loc.T("install.title.downgrade")
	case "replace":
		return in.loc.T("install.title.replace")
	}
	return in.loc.T("install.title.upgrade")
}

func (in *Installer) freshForm(c *mui.Context) {
	t := c.Theme()
	mui.Text(c, in.loc.T("install.welcome"))
	mui.Field(c, in.loc.T("install.location"), func() {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			field := mui.TextInput(c, &in.dir).Grow(1).Shrink(1).Disabled(in.busy).Label(in.loc.T("install.location"))
			if field.Submitted() {
				in.install()
			}
			if mui.Button(c, in.loc.T("install.browse")).Disabled(in.busy).Clicked() {
				in.host.ChooseFolder(in.dir, func(dir string, err error) {
					switch {
					case err != nil:
						in.err = in.loc.T("install.error", "err", err.Error())
					case dir != "":
						in.dir = install.FolderFor(dir)
						in.err = ""
					}
				})
			}
		})
	})
	mui.Text(c, in.loc.T("install.perUser")).FontSize(12).TextColor(t.TextMuted)
	mui.Checkbox(c, &in.launchAtLogin, in.loc.T("install.launchAtLogin")).Disabled(in.busy)
	mui.Checkbox(c, &in.desktop, in.loc.T("install.desktopShortcut")).Disabled(in.busy)
}

func (in *Installer) changeForm(c *mui.Context) {
	t := c.Theme()
	st := in.info.State
	args := []any{"installed", st.Version, "version", in.info.Version}
	switch in.direction() {
	case "downgrade":
		mui.Text(c, in.loc.T("install.downgradePrompt", args...))
	case "replace":
		mui.Text(c, in.loc.T("install.replacePrompt", args...))
	default:
		mui.Text(c, in.loc.T("install.upgradePrompt", args...))
	}
	mui.Text(c, in.loc.T("install.installedIn", "dir", st.Dir)).FontSize(12).TextColor(t.TextMuted)
	mui.Text(c, in.loc.T("install.keepsSettings")).FontSize(12).TextColor(t.TextMuted)
	if in.direction() == "downgrade" {
		mui.Text(c, in.loc.T("install.downgradeWarning")).FontSize(12).TextColor(t.TextMuted)
	}
}

// status shows the progress or the error.
func (in *Installer) status(c *mui.Context) {
	t := c.Theme()
	switch {
	case in.busy || in.started:
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Spinner(c)
			mui.Text(c, in.stepText())
		})
	case in.err != "":
		mui.Text(c, in.err).TextColor(t.Danger)
	}
}

func (in *Installer) stepText() string {
	if in.started {
		return in.loc.T("install.starting")
	}
	switch in.step {
	case install.StepStopping:
		return in.loc.T("install.step.stopping")
	case install.StepShortcuts:
		return in.loc.T("install.step.shortcuts")
	case install.StepRegistering:
		return in.loc.T("install.step.registering")
	}
	return in.loc.T("install.step.copying")
}

func (in *Installer) buttons(c *mui.Context) {
	disabled := in.busy || in.started
	mui.Row(c).Gap(8).Justify(mui.End).AlignItems(mui.Center).Children(func() {
		if mui.Button(c, in.loc.T("install.cancel")).Disabled(disabled).Clicked() {
			in.host.Cancel()
		}
		if mui.Button(c, in.loc.T("install.runPortable")).Disabled(disabled).Clicked() {
			in.host.RunPortable()
		}
		if mui.PrimaryButton(c, in.primaryLabel()).Disabled(disabled).Clicked() {
			in.install()
		}
	})
}

func (in *Installer) primaryLabel() string {
	if !in.change() {
		return in.loc.T("install.install")
	}
	switch in.direction() {
	case "downgrade":
		return in.loc.T("install.downgrade")
	case "replace":
		return in.loc.T("install.replace")
	}
	return in.loc.T("install.upgrade")
}

// install starts the installation.
func (in *Installer) install() {
	if in.busy || in.started {
		return
	}
	o := install.Options{
		Dir:             in.dir,
		LaunchAtLogin:   in.launchAtLogin,
		DesktopShortcut: in.desktop,
		Description:     in.loc.T("install.shortcutDescription"),
	}
	if in.change() {
		o = install.Options{Dir: in.info.State.Dir, Upgrade: true, Description: o.Description}
	}
	in.busy, in.err, in.step = true, "", install.StepStopping
	in.host.Install(o, func(s install.Step) { in.step = s }, func(err error) {
		in.busy = false
		if err == nil {
			in.started = true
			return
		}
		in.err = in.errorText(err)
	})
}

func (in *Installer) errorText(err error) string {
	switch {
	case errors.Is(err, install.ErrStillRunning):
		return in.loc.T("install.errorRunning")
	case errors.Is(err, install.ErrInvalidDir):
		return in.loc.T("install.errorInvalidDir")
	}
	return in.loc.T("install.error", "err", err.Error())
}
