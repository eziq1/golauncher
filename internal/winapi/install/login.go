//go:build !darwin

package install

// SetLoginItem turns starting at login on or off with set (MyGo's login
// item: the Run key on Windows).
func SetLoginItem(on bool, set func(bool) error, _ func() bool) error { return set(on) }

// LoginItemOn reports whether golauncher starts at login.
func LoginItemOn(_ func(bool) error, enabled func() bool) bool { return enabled() }
