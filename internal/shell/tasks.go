package shell

import (
	"github.com/egoist/mygo"

	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/internal/ui"
)

// Background tasks on the desktop (docs/background-tasks.md §3, §4.1): the
// tray tooltip counts them, quitting while they run asks first, and their
// notifications show in the launcher when it is visible and as Windows
// notifications otherwise.

// setUpTasks connects the background tasks to the tray, the launcher and
// the notifications. Main thread only.
func (s *Shell) setUpTasks() {
	s.env.Tasks.OnChange(func(state bool) {
		if state {
			s.post(s.updateTrayToolTip)
		}
	})
	s.env.Engine.SetNotifier(func(n core.Notice) { s.post(func() { s.notify(n) }) })
}

// trayToolTip is the tray icon's tooltip: the app name, with the number of
// running background tasks.
func trayToolTip(l *i18n.Localizer, active int) string {
	if active > 0 {
		return l.N("tray.tooltipTasks", active)
	}
	return l.T("tray.tooltip")
}

func (s *Shell) updateTrayToolTip() {
	if s.tray != nil {
		s.tray.SetToolTip(trayToolTip(s.loc, s.env.Tasks.Active()))
	}
}

// notify shows a notice (Host.Toast, a finished task): in the launcher's
// footer when it is visible, else as a Windows notification; clicking the
// notification of a task opens it. Main thread only.
func (s *Shell) notify(n core.Notice) {
	if s.panel != nil && s.panel.IsVisible() {
		s.env.Controller.ShowToast(n.Toast)
		return
	}
	if !mygo.NotificationsSupported() && scriptNotify(n.Toast.Title, n.Toast.Message, s.log) {
		return // macOS outside a .app bundle
	}
	id := ""
	if n.TaskID != "" {
		id = "task-" + n.TaskID
	}
	notification := mygo.NewNotification(mygo.NotificationOptions{ID: id, Title: n.Toast.Title, Body: n.Toast.Message, Silent: n.Toast.Silent})
	notification.OnClick(func() {
		switch {
		case n.TaskID != "":
			s.showTask(n.TaskID)
		case n.Settings == core.SettingsPermissions:
			s.showSettings(ui.PagePermissions)
		default:
			s.showLauncher()
		}
	})
	go func() {
		if err := notification.Show(); err != nil {
			s.log.Warn("notification", "err", err)
		}
	}()
}

// showTask shows the launcher with Background Tasks open at task id. Main
// thread only.
func (s *Shell) showTask(id string) {
	if s.panel == nil {
		return
	}
	if s.panel.IsVisible() {
		s.launcher.OpenTasks(id)
		return
	}
	s.showLauncherThen(func() { s.launcher.OpenTasks(id) })
}

// quitPrompt asks whether to quit while active background tasks run.
func quitPrompt(l *i18n.Localizer, active int) mygo.MessageOptions {
	return mygo.MessageOptions{
		Type:          mygo.MessageWarning,
		Title:         l.T("quit.tasks.title"),
		Message:       l.N("quit.tasks.message", active),
		Buttons:       []string{l.T("quit.tasks.quit"), l.T("quit.tasks.cancel")},
		DefaultButton: 1,
		CancelButton:  1,
	}
}

// beforeQuit asks before quitting while background tasks run; quitting
// then stops them (Env.Close). Main thread only.
func (s *Shell) beforeQuit(e *mygo.QuitEvent) {
	if active := s.env.Tasks.Active(); active > 0 && !s.quitConfirmed {
		e.PreventDefault()
		if s.askingQuit {
			return
		}
		s.askingQuit = true
		opts := quitPrompt(s.loc, active)
		go func() {
			res, err := mygo.Dialog.Message(opts)
			s.post(func() {
				s.askingQuit = false
				if err != nil {
					s.log.Warn("quit dialog", "err", err)
					return
				}
				if res.Button == 0 {
					s.quitConfirmed = true
					mygo.App.Quit()
				}
			})
		}()
		return
	}
	s.quitting = true
}
