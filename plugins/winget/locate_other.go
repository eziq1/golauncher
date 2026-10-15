//go:build !windows

package winget

// packageFullName needs the Windows package APIs.
func packageFullName(string) (string, error) { return "", errNotInstalled }

// packagePath needs the Windows package APIs.
func packagePath(string) (string, error) { return "", errNotInstalled }
