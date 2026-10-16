package agentrt

import (
	"strconv"
	"strings"
)

// ParseVersion returns the first x.y.z in out, e.g. "2.1.296" from
// "2.1.296 (Claude Code)".
func ParseVersion(out string) string {
	for f := range strings.FieldsSeq(out) {
		f = strings.TrimPrefix(f, "v")
		parts := strings.Split(f, ".")
		if len(parts) < 3 {
			continue
		}
		ok := true
		for _, p := range parts[:3] {
			if _, err := strconv.Atoi(p); err != nil {
				ok = false
			}
		}
		if ok {
			return strings.Join(parts[:3], ".")
		}
	}
	return ""
}

// versionParts returns the numbers of a x.y.z version.
func versionParts(v string) [3]int {
	var n [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		n[i], _ = strconv.Atoi(p)
	}
	return n
}

// CompareVersions compares two x.y.z versions like strings.Compare.
func CompareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// MajorOf returns the major version.
func MajorOf(v string) int { return versionParts(v)[0] }
