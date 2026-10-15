package system

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	powrprof = windows.NewLazySystemDLL("powrprof.dll")

	procLockWorkStation       = user32.NewProc("LockWorkStation")
	procKeybdEvent            = user32.NewProc("keybd_event")
	procPostMessageW          = user32.NewProc("PostMessageW")
	procSendMessageTimeoutW   = user32.NewProc("SendMessageTimeoutW")
	procSHQueryRecycleBinW    = shell32.NewProc("SHQueryRecycleBinW")
	procSHEmptyRecycleBinW    = shell32.NewProc("SHEmptyRecycleBinW")
	procSetSuspendState       = powrprof.NewProc("SetSuspendState")
	procIsPwrHibernateAllowed = powrprof.NewProc("IsPwrHibernateAllowed")
)

const (
	hwndBroadcast   = 0xFFFF
	wmSettingChange = 0x001A
	wmSysCommand    = 0x0112
	scMonitorPower  = 0xF170
	monitorOff      = 2
	smtoAbortIfHung = 0x0002

	keyeventfExtendedKey = 0x0001
	keyeventfKeyUp       = 0x0002
	vkVolumeMute         = 0xAD
	vkVolumeDown         = 0xAE
	vkVolumeUp           = 0xAF

	sherbNoConfirmation = 0x1

	personalizeKey = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`
)

// Lock locks the workstation (LockWorkStation).
func Lock() error {
	if r, _, err := procLockWorkStation.Call(); r == 0 {
		return fmt.Errorf("system: LockWorkStation: %w", err)
	}
	return nil
}

// enableShutdownPrivilege enables SeShutdownPrivilege in the process token,
// which ExitWindowsEx (restart, shut down) and SetSuspendState need.
func enableShutdownPrivilege() error {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok); err != nil {
		return fmt.Errorf("system: OpenProcessToken: %w", err)
	}
	defer tok.Close()
	var luid windows.LUID
	name, _ := windows.UTF16PtrFromString("SeShutdownPrivilege")
	if err := windows.LookupPrivilegeValue(nil, name, &luid); err != nil {
		return fmt.Errorf("system: LookupPrivilegeValue: %w", err)
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
	if err := windows.AdjustTokenPrivileges(tok, false, &tp, 0, nil, nil); err != nil {
		return fmt.Errorf("system: AdjustTokenPrivileges: %w", err)
	}
	return nil
}

// Suspend puts the computer to sleep, or hibernates it
// (SetSuspendState). It returns after the computer woke up.
func Suspend(hibernate bool) error {
	if err := enableShutdownPrivilege(); err != nil {
		return err
	}
	var h uintptr
	if hibernate {
		h = 1
	}
	if r, _, err := procSetSuspendState.Call(h, 0, 0); r == 0 {
		return fmt.Errorf("system: SetSuspendState: %w", err)
	}
	return nil
}

// ExitWindows signs out, restarts or shuts down (ExitWindowsEx). Apps may
// still refuse to close; nothing is forced.
func ExitWindows(kind ExitKind) error {
	var flags uint32
	switch kind {
	case Logoff:
		flags = windows.EWX_LOGOFF
	case Reboot:
		flags = windows.EWX_REBOOT
	case PowerOff:
		flags = windows.EWX_POWEROFF
	default:
		return fmt.Errorf("system: unknown exit kind %d", kind)
	}
	if kind != Logoff {
		if err := enableShutdownPrivilege(); err != nil {
			return err
		}
	}
	reason := uint32(windows.SHTDN_REASON_MAJOR_OTHER | windows.SHTDN_REASON_MINOR_OTHER | windows.SHTDN_REASON_FLAG_PLANNED)
	if err := windows.ExitWindowsEx(flags, reason); err != nil {
		return fmt.Errorf("system: ExitWindowsEx: %w", err)
	}
	return nil
}

// shQueryRBInfo is SHQUERYRBINFO. shellapi.h packs it to 1 byte on 32-bit
// Windows only, which matches Go's alignment of int64 there (4 bytes, size
// 20); on 64-bit both use natural alignment (size 24).
type shQueryRBInfo struct {
	size     uint32
	bytes    int64
	numItems int64
}

// QueryRecycleBin returns the number of items and the size of the Recycle
// Bins of all drives (SHQueryRecycleBinW). It only reads.
func QueryRecycleBin() (RecycleBin, error) {
	info := shQueryRBInfo{}
	info.size = uint32(unsafe.Sizeof(info))
	if hr, _, _ := procSHQueryRecycleBinW.Call(0, uintptr(unsafe.Pointer(&info))); int32(uint32(hr)) < 0 {
		return RecycleBin{}, fmt.Errorf("system: SHQueryRecycleBinW: HRESULT 0x%08X", uint32(hr))
	}
	return RecycleBin{Items: info.numItems, Size: info.bytes}, nil
}

// EmptyRecycleBin empties the Recycle Bins of all drives without asking
// (SHEmptyRecycleBinW); the caller asks the user first.
func EmptyRecycleBin() error {
	if hr, _, _ := procSHEmptyRecycleBinW.Call(0, 0, sherbNoConfirmation); int32(uint32(hr)) < 0 {
		return fmt.Errorf("system: SHEmptyRecycleBinW: HRESULT 0x%08X", uint32(hr))
	}
	return nil
}

// DarkMode reports whether apps use the dark theme (AppsUseLightTheme is 0).
// A missing value means light. It only reads.
func DarkMode() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, personalizeKey, registry.QUERY_VALUE)
	if err != nil {
		return false, fmt.Errorf("system: theme: %w", err)
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	if err == registry.ErrNotExist {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("system: theme: %w", err)
	}
	return v == 0, nil
}

// SetDarkMode switches apps and the system (taskbar, Start) to the dark or
// light theme and tells running windows (WM_SETTINGCHANGE "ImmersiveColorSet").
func SetDarkMode(dark bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, personalizeKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("system: theme: %w", err)
	}
	defer k.Close()
	light := uint32(1)
	if dark {
		light = 0
	}
	for _, name := range []string{"AppsUseLightTheme", "SystemUsesLightTheme"} {
		if err := k.SetDWordValue(name, light); err != nil {
			return fmt.Errorf("system: theme: %w", err)
		}
	}
	param, _ := windows.UTF16PtrFromString("ImmersiveColorSet")
	var result uintptr
	procSendMessageTimeoutW.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(param)),
		smtoAbortIfHung, 200, uintptr(unsafe.Pointer(&result)))
	return nil
}

// PressMediaKey presses and releases a volume key times times. Each press
// changes the volume by 2%.
func PressMediaKey(key MediaKey, times int) error {
	var vk uintptr
	switch key {
	case VolumeMute:
		vk = vkVolumeMute
	case VolumeDown:
		vk = vkVolumeDown
	case VolumeUp:
		vk = vkVolumeUp
	default:
		return fmt.Errorf("system: unknown media key %d", key)
	}
	for range max(times, 1) {
		procKeybdEvent.Call(vk, 0, keyeventfExtendedKey, 0)
		procKeybdEvent.Call(vk, 0, keyeventfExtendedKey|keyeventfKeyUp, 0)
	}
	return nil
}

// DisplayOff turns the displays off (WM_SYSCOMMAND SC_MONITORPOWER). Moving
// the mouse or pressing a key turns them on again.
func DisplayOff() error {
	if r, _, err := procPostMessageW.Call(hwndBroadcast, wmSysCommand, scMonitorPower, monitorOff); r == 0 {
		return fmt.Errorf("system: PostMessageW: %w", err)
	}
	return nil
}

// HibernateAvailable reports whether hibernation is enabled
// (IsPwrHibernateAllowed). It only reads.
func HibernateAvailable() bool {
	if procIsPwrHibernateAllowed.Find() != nil {
		return false
	}
	r, _, _ := procIsPwrHibernateAllowed.Call()
	return r&0xFF != 0
}

// OSBuild returns the build number of Windows, e.g. 26100 (RtlGetVersion).
func OSBuild() int {
	return int(windows.RtlGetVersion().BuildNumber)
}

// EditionID returns the Windows edition from the registry, e.g. "Core"
// (Home) or "Professional"; "" when unknown.
func EditionID() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue("EditionID")
	if err != nil {
		return ""
	}
	return v
}
