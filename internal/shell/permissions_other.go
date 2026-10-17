//go:build !darwin

package shell

import "log/slog"

// onboardingSupported reports false: only macOS has privacy permissions to
// grant.
func onboardingSupported() bool { return false }

// scriptNotify reports false: MyGo's notifications are the only ones.
func scriptNotify(string, string, *slog.Logger) bool { return false }
