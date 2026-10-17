package system

import (
	"errors"
	"runtime"
	"sync"

	winsys "github.com/eziq1/golauncher/internal/winapi/system"
)

// System is the machine the plugin reads and acts on. The first group of
// methods only reads; the others change the state of the machine. Tests
// and glctl use a Fake, which records the changes instead of making them.
type System interface {
	RecycleBin() (winsys.RecycleBin, error)
	DarkMode() (bool, error)
	HibernateAvailable() bool
	OSBuild() int
	EditionID() string

	Lock() error
	Sleep() error
	Hibernate() error
	Restart() error
	ShutDown() error
	SignOut() error
	EmptyRecycleBin() error
	SetDarkMode(dark bool) error
	PressMediaKey(key winsys.MediaKey, times int) error
	DisplayOff() error
}

// macSystem is implemented by a System that is a Mac: the plugin then
// offers the macOS commands, System Settings panes and utilities instead of
// the Windows ones.
type macSystem interface{ MacOS() bool }

// isMac reports whether sys is a Mac.
func isMac(sys System) bool {
	m, ok := sys.(macSystem)
	return ok && m.MacOS()
}

// realSystem is this computer.
type realSystem struct{}

func (realSystem) MacOS() bool { return runtime.GOOS == "darwin" }

func (realSystem) RecycleBin() (winsys.RecycleBin, error) { return winsys.QueryRecycleBin() }
func (realSystem) DarkMode() (bool, error)                { return winsys.DarkMode() }
func (realSystem) HibernateAvailable() bool               { return winsys.HibernateAvailable() }
func (realSystem) OSBuild() int                           { return winsys.OSBuild() }
func (realSystem) EditionID() string                      { return winsys.EditionID() }
func (realSystem) Lock() error                            { return winsys.Lock() }
func (realSystem) Sleep() error                           { return winsys.Suspend(false) }
func (realSystem) Hibernate() error                       { return winsys.Suspend(true) }
func (realSystem) Restart() error                         { return winsys.ExitWindows(winsys.Reboot) }
func (realSystem) ShutDown() error                        { return winsys.ExitWindows(winsys.PowerOff) }
func (realSystem) SignOut() error                         { return winsys.ExitWindows(winsys.Logoff) }
func (realSystem) EmptyRecycleBin() error                 { return winsys.EmptyRecycleBin() }
func (realSystem) SetDarkMode(dark bool) error            { return winsys.SetDarkMode(dark) }
func (realSystem) DisplayOff() error                      { return winsys.DisplayOff() }
func (realSystem) PressMediaKey(k winsys.MediaKey, times int) error {
	return winsys.PressMediaKey(k, times)
}

// Fake is a System that changes nothing: it records the calls that would
// change the machine and answers reads from its fields. It is safe for
// concurrent use; set the fields before handing it to the plugin.
type Fake struct {
	Bin         winsys.RecycleBin
	Dark        bool
	Hibernation bool
	Build       int
	Edition     string
	// Mac makes it a Mac (MacOS); by default it is a Windows machine.
	Mac bool
	// Err, when set, is returned by every call that changes the machine.
	Err error

	mu    sync.Mutex
	calls []string
}

// NewFake returns a Fake of a Windows 11 Pro machine with hibernation
// enabled, the light theme and 3 items (1.5 MB) in the Recycle Bin.
func NewFake() *Fake {
	return &Fake{Bin: winsys.RecycleBin{Items: 3, Size: 1_572_864}, Hibernation: true, Build: 26100, Edition: "Professional"}
}

// DryRun returns a Fake that reads the state of this computer once and
// records the changes instead of making them (glctl --real without --exec).
func DryRun() *Fake {
	f := &Fake{Hibernation: winsys.HibernateAvailable(), Build: winsys.OSBuild(), Edition: winsys.EditionID(),
		Mac: runtime.GOOS == "darwin"}
	f.Bin, _ = winsys.QueryRecycleBin()
	f.Dark, _ = winsys.DarkMode()
	return f
}

// Calls returns the recorded calls, e.g. ["Lock", "SetDarkMode(true)"].
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// record records call and, unless Err is set, applies change (under the lock).
func (f *Fake) record(call string, change func() error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	if f.Err != nil {
		return f.Err
	}
	if change != nil {
		return change()
	}
	return nil
}

func (f *Fake) RecycleBin() (winsys.RecycleBin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Bin, nil
}

func (f *Fake) DarkMode() (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Dark, nil
}

func (f *Fake) MacOS() bool              { return f.Mac }
func (f *Fake) HibernateAvailable() bool { return f.Hibernation }
func (f *Fake) OSBuild() int             { return f.Build }
func (f *Fake) EditionID() string        { return f.Edition }
func (f *Fake) Lock() error              { return f.record("Lock", nil) }
func (f *Fake) Sleep() error             { return f.record("Sleep", nil) }
func (f *Fake) Hibernate() error         { return f.record("Hibernate", nil) }
func (f *Fake) Restart() error           { return f.record("Restart", nil) }
func (f *Fake) ShutDown() error          { return f.record("ShutDown", nil) }
func (f *Fake) SignOut() error           { return f.record("SignOut", nil) }
func (f *Fake) DisplayOff() error        { return f.record("DisplayOff", nil) }

func (f *Fake) EmptyRecycleBin() error {
	return f.record("EmptyRecycleBin", func() error {
		if f.Bin.Items == 0 {
			return errors.New("the recycle bin is empty") // as SHEmptyRecycleBinW (E_UNEXPECTED)
		}
		f.Bin = winsys.RecycleBin{}
		return nil
	})
}

func (f *Fake) SetDarkMode(dark bool) error {
	call := "SetDarkMode(false)"
	if dark {
		call = "SetDarkMode(true)"
	}
	return f.record(call, func() error { f.Dark = dark; return nil })
}

func (f *Fake) PressMediaKey(key winsys.MediaKey, times int) error {
	name := [...]string{winsys.VolumeMute: "Mute", winsys.VolumeDown: "VolumeDown", winsys.VolumeUp: "VolumeUp"}[key]
	return f.record("PressMediaKey("+name+")", nil)
}
