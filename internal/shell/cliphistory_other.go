//go:build !windows

package shell

// ClipboardHistory implements ui.ClipboardHistoryHost.
func (h settingsHost) ClipboardHistory() (history, cloud bool) { return false, false }
