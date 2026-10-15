package install

import (
	"cmp"
	"regexp"
	"strconv"
	"strings"
)

// version is a parsed version string: SemVer, optionally with the suffix
// git describe adds after a tag ("-<commits>-g<hash>", "-dirty").
type version struct {
	core    [3]int
	pre     []string // pre-release identifiers
	commits int      // commits after the tag (git describe)
	ordered bool     // false for a bare commit hash or a dirty tree
}

var (
	// describeRe matches the git describe suffix of a pre-release:
	// "[<pre>-]<commits>-g<hash>".
	describeRe = regexp.MustCompile(`^(?:(.+)-)?(\d+)-g([0-9a-f]{4,40})$`)
	// hashRe matches a bare commit hash, as in "0.0.0-<hash>" (no tag yet).
	hashRe = regexp.MustCompile(`^g?[0-9a-f]{7,40}$`)
)

// normalize trims spaces and a leading "v".
func normalize(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 1 && (s[0] == 'v' || s[0] == 'V') && s[1] >= '0' && s[1] <= '9' {
		s = s[1:]
	}
	return s
}

// parseVersion parses "MAJOR.MINOR[.PATCH][-PRE][+BUILD]".
func parseVersion(s string) (version, bool) {
	v := version{ordered: true}
	s, _, _ = strings.Cut(normalize(s), "+")
	if rest, ok := strings.CutSuffix(s, "-dirty"); ok {
		s, v.ordered = rest, false
	}
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v.core[i] = n
	}
	if !hasPre {
		return v, true
	}
	if pre == "" {
		return v, false
	}
	if m := describeRe.FindStringSubmatch(pre); m != nil {
		v.commits, _ = strconv.Atoi(m[2])
		pre = m[1]
	} else if hashRe.MatchString(pre) {
		v.ordered = false
	}
	if pre != "" {
		v.pre = strings.Split(pre, ".")
	}
	return v, true
}

// SameVersion reports whether a and b name the same version (ignoring a
// leading "v").
func SameVersion(a, b string) bool { return normalize(a) == normalize(b) }

// CompareVersions compares two versions: -1 when a is older than b, +1 when
// newer. ok is false when the versions differ but cannot be ordered (an
// unparsable version, a bare commit hash, a dirty build, or versions that
// differ only in build metadata). Equal versions return 0, true.
//
// The git describe suffix sorts after its tag, unlike a SemVer pre-release:
// 0.1.0 < 0.1.0-2-gabc < 0.1.0-3-gdef < 0.1.1.
func CompareVersions(a, b string) (order int, ok bool) {
	if SameVersion(a, b) {
		return 0, true
	}
	va, okA := parseVersion(a)
	vb, okB := parseVersion(b)
	if !okA || !okB || !va.ordered || !vb.ordered {
		return 0, false
	}
	for i := range va.core {
		if c := cmp.Compare(va.core[i], vb.core[i]); c != 0 {
			return c, true
		}
	}
	if c := comparePre(va.pre, vb.pre); c != 0 {
		return c, true
	}
	if c := cmp.Compare(va.commits, vb.commits); c != 0 {
		return c, true
	}
	return 0, false
}

// comparePre compares pre-release identifiers by SemVer's rules: a version
// without them is newer, numeric identifiers compare as numbers and sort
// before alphanumeric ones, and a longer list wins a tie.
func comparePre(a, b []string) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1
	case len(b) == 0:
		return -1
	}
	for i := range min(len(a), len(b)) {
		na, errA := strconv.Atoi(a[i])
		nb, errB := strconv.Atoi(b[i])
		var c int
		switch {
		case errA == nil && errB == nil:
			c = cmp.Compare(na, nb)
		case errA == nil:
			c = -1
		case errB == nil:
			c = 1
		default:
			c = strings.Compare(a[i], b[i])
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a), len(b))
}
