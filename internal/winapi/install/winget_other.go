//go:build !windows

package install

// WingetLocations returns nothing: winget is Windows only.
func WingetLocations() []string { return nil }
