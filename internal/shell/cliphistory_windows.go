//go:build windows

package shell

import "golang.org/x/sys/windows/registry"

// ClipboardHistory implements ui.ClipboardHistoryHost: whether Windows
// keeps a clipboard history (Win+V) or syncs the clipboard to other devices.
func (h settingsHost) ClipboardHistory() (history, cloud bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Clipboard`, registry.QUERY_VALUE)
	if err != nil {
		return false, false
	}
	defer k.Close()
	on := func(name string) bool {
		v, _, err := k.GetIntegerValue(name)
		return err == nil && v != 0
	}
	return on("EnableClipboardHistory"), on("EnableCloudClipboard")
}
