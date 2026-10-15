package install

import (
	"cmp"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/eziq1/golauncher/internal/install"
)

// System is install.System on Windows.
type System struct {
	// Key is the uninstall entry under HKEY_CURRENT_USER.
	Key string
}

var _ install.System = (*System)(nil)

// New returns the System of the real uninstall entry.
func New() *System { return &System{Key: install.UninstallKey} }

// Paths returns the per-user install locations: %LOCALAPPDATA%\Programs\golauncher
// and the Start menu and desktop shortcuts. ConfigFile and DataDirs are left
// to the caller.
func Paths() (install.Paths, error) {
	const kfFlagDontVerify = 0x4000 // the folder may not exist yet
	var p install.Paths
	progs, err := windows.KnownFolderPath(windows.FOLDERID_UserProgramFiles, kfFlagDontVerify)
	if err != nil {
		local, lerr := os.UserCacheDir() // %LOCALAPPDATA%
		if lerr != nil {
			return p, errors.Join(err, lerr)
		}
		progs = filepath.Join(local, "Programs")
	}
	p.DefaultDir = filepath.Join(progs, install.AppName)
	if start, err := windows.KnownFolderPath(windows.FOLDERID_Programs, kfFlagDontVerify); err == nil {
		p.StartMenuLink = filepath.Join(start, install.LinkName)
	} else {
		return p, err
	}
	if desk, err := windows.KnownFolderPath(windows.FOLDERID_Desktop, kfFlagDontVerify); err == nil {
		p.DesktopLink = filepath.Join(desk, install.LinkName)
	}
	return p, nil
}

// ReadEntry implements install.System.
func (s *System) ReadEntry() (install.Entry, bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, s.Key, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return install.Entry{}, false, nil
	}
	if err != nil {
		return install.Entry{}, false, fmt.Errorf("install: reading the uninstall entry: %w", err)
	}
	defer k.Close()
	str := func(name string) string {
		v, _, _ := k.GetStringValue(name)
		return v
	}
	e := install.Entry{
		Name:        str("DisplayName"),
		Version:     str("DisplayVersion"),
		Publisher:   str("Publisher"),
		Dir:         str("InstallLocation"),
		Icon:        str("DisplayIcon"),
		Uninstall:   str("UninstallString"),
		URL:         str("URLInfoAbout"),
		InstallDate: str("InstallDate"),
	}
	if n, _, err := k.GetIntegerValue("EstimatedSize"); err == nil {
		e.SizeKB = uint32(n)
	}
	return e, true, nil
}

// WriteEntry implements install.System.
func (s *System) WriteEntry(e install.Entry) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, s.Key, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("install: writing the uninstall entry: %w", err)
	}
	defer k.Close()
	var errs []error
	for _, v := range []struct{ name, value string }{
		{"DisplayName", e.Name},
		{"DisplayVersion", e.Version},
		{"Publisher", e.Publisher},
		{"InstallLocation", e.Dir},
		{"DisplayIcon", e.Icon},
		{"UninstallString", e.Uninstall},
		{"URLInfoAbout", e.URL},
		{"InstallDate", e.InstallDate},
	} {
		errs = append(errs, k.SetStringValue(v.name, v.value))
	}
	errs = append(errs,
		k.SetDWordValue("NoModify", 1),
		k.SetDWordValue("NoRepair", 1),
		k.SetDWordValue("EstimatedSize", e.SizeKB))
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("install: writing the uninstall entry: %w", err)
	}
	return nil
}

// DeleteEntry implements install.System.
func (s *System) DeleteEntry() error {
	err := registry.DeleteKey(registry.CURRENT_USER, s.Key)
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("install: deleting the uninstall entry: %w", err)
	}
	return nil
}

// FileVersion implements install.System: the ProductVersion string of the
// version resource, else its fixed product version.
func (s *System) FileVersion(exe string) (string, error) {
	size, err := windows.GetFileVersionInfoSize(exe, nil)
	if err != nil {
		return "", err
	}
	buf := make([]byte, size)
	if err := windows.GetFileVersionInfo(exe, 0, size, unsafe.Pointer(&buf[0])); err != nil {
		return "", err
	}
	// query returns the bytes of a value inside buf.
	query := func(sub string) []byte {
		var p uintptr
		var n uint32
		if windows.VerQueryValue(unsafe.Pointer(&buf[0]), sub, unsafe.Pointer(&p), &n) != nil || p == 0 {
			return nil
		}
		off := int(p - uintptr(unsafe.Pointer(&buf[0])))
		if off < 0 || off >= len(buf) {
			return nil
		}
		return buf[off:]
	}
	langs := []string{"040904b0", "000004b0", "040904e4"}
	if t := query(`\VarFileInfo\Translation`); len(t) >= 4 {
		langs = slices.Insert(langs, 0, fmt.Sprintf("%04x%04x", binary.LittleEndian.Uint16(t), binary.LittleEndian.Uint16(t[2:])))
	}
	for _, l := range langs {
		if b := query(`\StringFileInfo\` + l + `\ProductVersion`); b != nil {
			if v := strings.TrimSpace(utf16String(b)); v != "" {
				return v, nil
			}
		}
	}
	// VS_FIXEDFILEINFO: dwProductVersionMS and LS at offsets 16 and 20.
	if b := query(`\`); len(b) >= 24 {
		ms, ls := binary.LittleEndian.Uint32(b[16:]), binary.LittleEndian.Uint32(b[20:])
		return fmt.Sprintf("%d.%d.%d", ms>>16, ms&0xffff, ls>>16), nil
	}
	return "", errors.New("install: no version resource")
}

// utf16String decodes a NUL-terminated little-endian UTF-16 string.
func utf16String(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return windows.UTF16ToString(u)
}

// processNames are the image names a running golauncher may have.
func processNames(quitter string) []string {
	names := []string{strings.ToLower(install.ExeName)}
	if b := strings.ToLower(filepath.Base(quitter)); b != names[0] {
		names = append(names, b)
	}
	return names
}

// StopInstances implements install.System.
func (s *System) StopInstances(quitter string, timeout time.Duration) error {
	names := processNames(quitter)
	if len(others(names)) == 0 {
		return nil
	}
	deadline := time.Now().Add(timeout)
	cmd := exec.Command(quitter, "--quit")
	cmd.Dir = filepath.Dir(quitter)
	cmd.Env = append(os.Environ(), install.EnvNoInstall+"=1")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("install: asking golauncher to quit: %w", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Until(deadline)):
		return install.ErrStillRunning
	}
	for {
		if len(others(names)) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return install.ErrStillRunning
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// others lists the processes of this session, other than this one, whose
// image is named one of names (lower case).
func others(names []string) []uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	self := windows.GetCurrentProcessId()
	var session uint32
	if err := windows.ProcessIdToSessionId(self, &session); err != nil {
		return nil
	}
	var out []uint32
	pe := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err := windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if pe.ProcessID == self || !slices.Contains(names, strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))) {
			continue
		}
		var sid uint32
		if windows.ProcessIdToSessionId(pe.ProcessID, &sid) == nil && sid == session {
			out = append(out, pe.ProcessID)
		}
	}
	return out
}

// Start implements install.System.
func (s *System) Start(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Dir = filepath.Dir(exe)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("install: starting %s: %w", exe, err)
	}
	return cmd.Process.Release()
}

// ScheduleRemoval implements install.System: a hidden cmd.exe deletes each
// file, retrying every second for up to 30 seconds while it is still in use
// (the uninstaller may show a last message before it exits), then removes
// dir if it is empty. It waits with ping: timeout fails without a console.
func (s *System) ScheduleRemoval(dir string, files []string) error {
	for _, f := range append(slices.Clone(files), dir) {
		if strings.ContainsAny(f, "\"\r\n") {
			return fmt.Errorf("install: unusable path %q", f)
		}
	}
	var b strings.Builder
	for _, f := range files {
		// The parentheses end the loop body, which would take the rest of
		// the line.
		fmt.Fprintf(&b, `(for /l %%i in (1,1,30) do @if exist "%s" (ping -n 2 127.0.0.1 >nul & del /f /q "%s" 2>nul)) & `, f, f)
	}
	b.WriteString(`rmdir "` + dir + `" 2>nul`)
	sysDir, err := windows.GetSystemDirectory()
	if err != nil {
		return err
	}
	comspec := cmp.Or(os.Getenv("ComSpec"), filepath.Join(sysDir, "cmd.exe"))
	cmd := exec.Command(comspec)
	cmd.Dir = os.TempDir() // not dir, which could then not be removed
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       `cmd.exe /d /s /c "` + b.String() + `"`,
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("install: scheduling the removal of %s: %w", dir, err)
	}
	return cmd.Process.Release()
}
