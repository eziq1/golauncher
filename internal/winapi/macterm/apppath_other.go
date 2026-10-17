//go:build !darwin

package macterm

// AppPath finds no macOS app outside macOS.
func AppPath(string) string { return "" }
