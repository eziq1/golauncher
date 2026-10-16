//go:build windows

package shell

import "golang.org/x/sys/windows/registry"

// ProtocolRegistered reports whether an app handles links of the URL scheme
// (e.g. "claude" for claude:// links): a key HKEY_CLASSES_ROOT\<scheme>
// with a "URL Protocol" value, as packaged and desktop apps register them.
func ProtocolRegistered(scheme string) bool {
	if scheme == "" {
		return false
	}
	k, err := registry.OpenKey(registry.CLASSES_ROOT, scheme, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue("URL Protocol")
	return err == nil
}
