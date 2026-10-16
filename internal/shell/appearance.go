package shell

import (
	mui "github.com/egoist/mygo/ui"

	"github.com/egoist/mygo"

	"github.com/eziq1/golauncher/internal/theme"
)

// themeSource is MyGo's appearance for a light or dark theme.
func themeSource(dark bool) mygo.ThemeSource {
	if dark {
		return mygo.ThemeDark
	}
	return mygo.ThemeLight
}

// vibrancy is the window material of a theme material.
func vibrancy(m theme.Material) mygo.Vibrancy {
	switch m {
	case theme.MaterialMica:
		return mygo.VibrancyMica
	case theme.MaterialNone:
		return mygo.VibrancyNone
	}
	return mygo.VibrancyAcrylic
}

// watchAppearance follows what the appearance depends on besides the
// configuration: the theme files, a setting being previewed, and the
// system's light or dark mode, accent color, transparency effects and
// contrast themes. Main thread only, once the windows exist.
func (s *Shell) watchAppearance() {
	s.env.Themes.OnChange(func() { s.post(s.appearanceChanged) })
	mygo.Theme.OnUpdated(func() { s.post(s.appearanceChanged) })
	go s.env.Themes.Library().Watch(s.ctx, theme.PollInterval)
	s.applyAppearance()
}

// appearanceChanged applies the appearance and redraws the windows.
func (s *Shell) appearanceChanged() {
	s.applyAppearance()
	s.panel.Invalidate()
	if s.settingsWin != nil {
		s.settingsWin.Invalidate()
	}
}

// systemState reads the system's state that the appearance follows. The
// windows read their own light or dark mode and accent; the transparency
// effects only the app can.
func (s *Shell) systemState() theme.System {
	p := mygo.Theme.Preferences()
	s.env.Themes.SetReduceTransparency(p.ReduceTransparency)
	return theme.System{
		Dark:               mygo.Theme.IsDark(),
		Accent:             mui.RGBA(p.Accent.R, p.Accent.G, p.Accent.B, float32(p.Accent.A)/255),
		ReduceTransparency: p.ReduceTransparency,
		HighContrast:       p.HighContrast,
		TextScale:          float32(p.TextScale),
		NoMaterial:         s.env.Themes.NoMaterial(),
	}
}

// applyAppearance gives the windows the appearance of the configuration
// (or of the preview): MyGo's light or dark mode, which the title bars,
// the material and the settings follow, the launcher's material and its
// window opacity. Only what changed is set. Main thread only.
func (s *Shell) applyAppearance() {
	a := s.env.Config.Get().Appearance
	// Following the system, MyGo follows it too; a fixed theme fixes it.
	source := mygo.ThemeSystem
	if !a.FollowSystem {
		if t, ok := s.env.Themes.Library().Get(a.Theme); ok {
			source = themeSource(t.Dark)
		}
	}
	if source != s.source {
		s.source = source
		mygo.Theme.SetSource(source)
	}
	r := s.env.Themes.Resolve(a, s.systemState())
	if s.panel == nil {
		return
	}
	if r.Material != s.material {
		s.material = r.Material
		s.panel.SetVibrancy(vibrancy(r.Material))
		s.log.Info("launcher material", "material", r.Material)
	}
	if r.WindowOpacity != s.windowOpacity {
		s.windowOpacity = r.WindowOpacity
		s.panel.SetOpacity(r.WindowOpacity)
	}
}

// previewLauncher shows the launcher beside the settings window without
// taking the focus from it, so that a change of the appearance shows at
// once (docs/themes.md §7). Main thread only.
func (s *Shell) previewLauncher() {
	if s.panel == nil || s.settingsWin == nil || s.panel.IsVisible() {
		return
	}
	sx, sy := s.settingsWin.Position()
	sw, _ := s.settingsWin.Size()
	pw, _ := s.panel.Size()
	d := mygo.Screen.DisplayNearestPoint(mygo.Point{X: sx + sw/2, Y: sy})
	const gap = 12
	x := sx + sw + gap // to the right of the settings
	if x+pw > d.WorkArea.X+d.WorkArea.Width {
		x = sx - pw - gap // or to their left
	}
	if x < d.WorkArea.X {
		x = d.WorkArea.X + d.WorkArea.Width - pw // or over them, at the edge
	}
	s.panel.SetPosition(x, max(sy, d.WorkArea.Y))
	s.launcher.Shown()
	s.panel.ShowInactive()
	s.previewing = true
}
