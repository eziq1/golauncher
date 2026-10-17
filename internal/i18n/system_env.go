//go:build !windows

package i18n

import "strings"

// envLanguages reads the POSIX locale environment (LC_ALL, LC_MESSAGES,
// LANG), e.g. "zh_CN.UTF-8" is "zh-CN". "C" and "POSIX" name no language.
func envLanguages(getenv func(string) string) []string {
	for _, v := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if s := getenv(v); s != "" {
			s, _, _ = strings.Cut(s, ".")
			if s == "C" || s == "POSIX" {
				return nil
			}
			return []string{strings.ReplaceAll(s, "_", "-")}
		}
	}
	return nil
}
