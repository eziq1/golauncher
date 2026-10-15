package scoop

import (
	"os"
	"path/filepath"
	"strings"
)

// IsRoot reports whether dir is a Scoop root: it holds apps\scoop\current.
func IsRoot(dir string) bool {
	if dir == "" {
		return false
	}
	st, err := os.Stat(filepath.Join(dir, "apps", "scoop", "current"))
	return err == nil && st.IsDir()
}

// DetectRoot finds the Scoop root: the configured path, then $SCOOP, then
// %USERPROFILE%\scoop. It returns "" when none is a Scoop root. A configured
// path that is not a Scoop root is skipped (and reported in badPref).
func DetectRoot(configured string, getenv func(string) string) (root string, badPref bool) {
	configured = strings.TrimSpace(expandEnv(configured, getenv))
	if configured != "" {
		if IsRoot(configured) {
			return filepath.Clean(configured), false
		}
		badPref = true
	}
	for _, d := range []string{getenv("SCOOP"), joinIf(getenv("USERPROFILE"), "scoop")} {
		if IsRoot(d) {
			return filepath.Clean(d), badPref
		}
	}
	return "", badPref
}

// DetectGlobal returns the global Scoop directory ($SCOOP_GLOBAL or
// %ProgramData%\scoop) when it has installed apps, else "".
func DetectGlobal(getenv func(string) string) string {
	for _, d := range []string{getenv("SCOOP_GLOBAL"), joinIf(getenv("ProgramData"), "scoop")} {
		if d == "" {
			continue
		}
		if st, err := os.Stat(filepath.Join(d, "apps")); err == nil && st.IsDir() {
			return filepath.Clean(d)
		}
		return ""
	}
	return ""
}

func joinIf(dir, name string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, name)
}

// expandEnv replaces %NAME% with environment variables.
func expandEnv(s string, getenv func(string) string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			break
		}
		j := strings.IndexByte(s[i+1:], '%')
		if j < 0 {
			break
		}
		name := s[i+1 : i+1+j]
		if v := getenv(name); name != "" && v != "" {
			b.WriteString(s[:i])
			b.WriteString(v)
		} else {
			b.WriteString(s[:i+2+j])
		}
		s = s[i+2+j:]
	}
	b.WriteString(s)
	return b.String()
}
