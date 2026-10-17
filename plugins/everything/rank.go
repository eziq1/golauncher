package everything

import (
	"cmp"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/eziq1/golauncher/internal/winapi/everything"
)

// tier is how likely a path holds the user's own files (docs/everything.md
// §2.2). Higher is better.
type tier int

const (
	tierSystem  tier = iota // Windows, caches, package stores
	tierProgram             // installed programs and app data
	tierNormal              // anything else
	tierUser                // known user folders, the profile, other drives
)

// Score weights. A tier is worth more than all other terms together, so a
// result never ranks above one of a better tier.
const (
	tierWeight     = 2.0
	knownBonus     = 0.3 // inside Desktop, Documents, … rather than elsewhere in the profile
	nameWeight     = 0.9 // exact name
	recentWeight   = 0.5 // modified just now
	recentWindow   = 30 * 24 * time.Hour
	folderPenalty  = 0.05 // folders rank just below files of the same score
	exactQuality   = 1.0
	prefixQuality  = 0.7
	wordQuality    = 0.5
	containQuality = 0.25
)

// rulesEnv is what the path rules derive from; tests inject their own.
type rulesEnv struct {
	Home        string   // user profile, e.g. C:\Users\demo
	SystemRoot  string   // C:\Windows
	ProgramData string   // C:\ProgramData
	UserFolders []string // Desktop, Documents, … (see everything.UserFolders)
	// The platform's additions (platformRules): system folders (prefixes)
	// and folder names marking system and program files anywhere.
	SystemDirs, SystemSegments, ProgramSegments []string
}

// systemSegments are folders that mark everything below them as system or
// cache files, wherever they are.
var systemSegments = []string{
	`$Recycle.Bin`, `System Volume Information`, `AppData\Local\Temp`,
	`node_modules`, `.git`, `__pycache__`, `.cache`, `Cache`, `CacheStorage`,
	`site-packages`, `go\pkg\mod`, `.nuget\packages`, `WinSxS`, `Containers\Layers`,
	// Beyond the list of the design: virtual environments, Cargo build
	// output and WebView2 browser profiles.
	`.venv`, `target\debug`, `target\release`, `EBWebView`,
}

// programSegments mark installed programs and application data. All of
// Scoop counts, including scoop\persist (application settings and data).
var programSegments = []string{`Program Files`, `Program Files (x86)`, `AppData`, `scoop`}

// rules classifies paths. All strings are lower case; prefixes and segments
// are wrapped in backslashes (`c:\windows\`, `\node_modules\`).
type rules struct {
	sysPrefixes []string
	sysSegments []string
	progSegs    []string
	userDirs    []string
	home        string
	sysDrive    string // "c:"
}

// defaultEnv is the environment of this machine.
func defaultEnv() rulesEnv {
	home, _ := os.UserHomeDir()
	return rulesEnv{
		Home:        home,
		SystemRoot:  os.Getenv("SystemRoot"),
		ProgramData: os.Getenv("ProgramData"),
		UserFolders: everything.UserFolders(),

		SystemDirs:      platformSystemDirs,
		SystemSegments:  platformSystemSegments,
		ProgramSegments: platformProgramSegments,
	}
}

func newRules(env rulesEnv) *rules {
	r := &rules{}
	for _, p := range slices.Concat([]string{env.SystemRoot, env.ProgramData}, env.SystemDirs) {
		if p = normDir(p); p != "" {
			r.sysPrefixes = append(r.sysPrefixes, p)
		}
	}
	for _, s := range slices.Concat(systemSegments, env.SystemSegments) {
		r.sysSegments = append(r.sysSegments, segment(s))
	}
	for _, s := range slices.Concat(programSegments, env.ProgramSegments) {
		r.progSegs = append(r.progSegs, segment(s))
	}
	r.home = normDir(env.Home)
	for _, d := range env.UserFolders {
		if d = normDir(d); d != "" {
			r.userDirs = append(r.userDirs, d)
		}
	}
	switch root := normDir(env.SystemRoot); {
	case len(root) >= 2 && root[1] == ':':
		r.sysDrive = root[:2]
	default:
		r.sysDrive = "c:"
	}
	return r
}

// withExtra returns r with the extraLowPriorityPaths preference added to the
// system tier: an absolute path ("D:\VMs") is a prefix, anything else
// ("vendor", "build\out") a folder name matched anywhere.
func (r *rules) withExtra(extra []string) *rules {
	if len(extra) == 0 {
		return r
	}
	c := *r
	c.sysPrefixes = slices.Clone(r.sysPrefixes)
	c.sysSegments = slices.Clone(r.sysSegments)
	for _, e := range extra {
		e = strings.Map(func(r rune) rune {
			if strings.ContainsRune(`"<>|`, r) {
				return -1
			}
			return r
		}, strings.TrimSpace(e))
		e = strings.Trim(strings.ReplaceAll(e, "/", `\`), `\ `)
		switch {
		case e == "":
		case len(e) >= 2 && e[1] == ':' || strings.HasPrefix(e, `\\`):
			if len(e) == 2 { // "D:"
				e += `\`
			}
			c.sysPrefixes = append(c.sysPrefixes, normDir(e))
		default:
			c.sysSegments = append(c.sysSegments, segment(e))
		}
	}
	return &c
}

// normDir lower-cases a folder, strips a \\?\ prefix and adds a trailing
// backslash; "" stays "".
func normDir(p string) string {
	p = normPath(p)
	if p == "" {
		return ""
	}
	return strings.TrimRight(p, `\`) + `\`
}

// normPath lower-cases a path, uses backslashes and strips the \\?\ and
// \\?\UNC\ prefixes.
func normPath(p string) string {
	p = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(p), "/", `\`))
	switch {
	case strings.HasPrefix(p, `\\?\unc\`):
		p = `\\` + p[len(`\\?\unc\`):]
	case strings.HasPrefix(p, `\\?\`):
		p = p[len(`\\?\`):]
	}
	return p
}

func segment(s string) string { return `\` + strings.ToLower(strings.Trim(s, `\`)) + `\` }

// tier classifies path; known reports a path inside one of the user's known
// folders.
func (r *rules) tier(path string) (t tier, known bool) {
	p := normPath(path) + `\` // so that a folder matches its own rules
	for _, s := range r.sysPrefixes {
		if strings.HasPrefix(p, s) {
			return tierSystem, false
		}
	}
	for _, s := range r.sysSegments {
		if strings.Contains(p, s) {
			return tierSystem, false
		}
	}
	for _, d := range r.userDirs {
		if strings.HasPrefix(p, d) {
			return tierUser, true
		}
	}
	for _, s := range r.progSegs {
		if strings.Contains(p, s) {
			return tierProgram, false
		}
	}
	if r.home != "" && strings.HasPrefix(p, r.home) {
		if rest := p[len(r.home):]; strings.HasPrefix(rest, ".") { // ~\.vscode, ~\.cargo, …
			return tierProgram, false
		}
		return tierUser, false
	}
	if len(p) >= 3 && p[1] == ':' && p[2] == '\\' && p[:2] != r.sysDrive {
		return tierUser, false
	}
	return tierNormal, false
}

// exclusions is the Everything search text leaving out the system tier,
// appended to the user's text for query A: ` !"c:\windows\" !"\node_modules\"`.
// Everything matches a term containing a backslash against the full path.
func (r *rules) exclusions() string {
	var b strings.Builder
	for _, s := range r.terms() {
		b.WriteString(` !"`)
		b.WriteString(s)
		b.WriteByte('"')
	}
	return b.String()
}

// inclusion is the complement of exclusions for query B: only the system
// tier, `<"c:\windows\"|"\node_modules\">`.
func (r *rules) inclusion() string {
	var b strings.Builder
	b.WriteString(" <")
	for i, s := range r.terms() {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteByte('"')
		b.WriteString(s)
		b.WriteByte('"')
	}
	b.WriteByte('>')
	return b.String()
}

func (r *rules) terms() []string { return slices.Concat(r.sysPrefixes, r.sysSegments) }

// searchText is the Everything search text for the user's text plus suffix
// (exclusions or inclusion). An unbalanced quote is closed so that it cannot
// swallow the suffix, and an OR is grouped so that the suffix applies to all
// of it.
func searchText(text, suffix string) string {
	if suffix == "" {
		return text
	}
	if strings.Count(text, `"`)%2 == 1 {
		text += `"`
	}
	if strings.Contains(text, "|") {
		text = "<" + text + ">"
	}
	return text + suffix
}

// ranked is a result with its tier and score.
type ranked struct {
	everything.Result
	tier  tier
	score float64
}

// rank scores results for the user's text and sorts them, best first; equal
// scores keep Everything's order. With hideSystem the system tier is dropped.
func rank(r *rules, res []everything.Result, text string, now time.Time, hideSystem bool) []ranked {
	terms := plainTerms(text)
	out := make([]ranked, 0, len(res))
	for _, x := range res {
		t, known := r.tier(x.Path)
		if hideSystem && t == tierSystem {
			continue
		}
		s := float64(t)*tierWeight + nameWeight*nameQuality(x.Name, terms) + recency(x.Modified, now)
		if known {
			s += knownBonus
		}
		if x.IsFolder {
			s -= folderPenalty
		}
		out = append(out, ranked{Result: x, tier: t, score: s})
	}
	slices.SortStableFunc(out, func(a, b ranked) int { return cmp.Compare(b.score, a.score) })
	return out
}

// plainTerms are the lower-case words of text that are plain name matches,
// not Everything operators, paths or wildcards.
func plainTerms(text string) []string {
	var out []string
	for _, f := range strings.Fields(strings.ToLower(text)) {
		if !strings.ContainsAny(f, `\:!"*?|<>`) {
			out = append(out, f)
		}
	}
	return out
}

// nameQuality is how well name matches terms, in [0, 1]: the whole query
// equal to the name (with or without extension) is 1; otherwise the mean of
// each term's best match: exact, prefix, start of a word, anywhere.
func nameQuality(name string, terms []string) float64 {
	if len(terms) == 0 {
		return 0
	}
	n := strings.ToLower(name)
	stem := n
	if i := strings.LastIndexByte(n, '.'); i > 0 {
		stem = n[:i]
	}
	if q := strings.Join(terms, " "); n == q || stem == q {
		return exactQuality
	}
	var sum float64
	for _, t := range terms {
		switch {
		case n == t || stem == t:
			sum += exactQuality
		case strings.HasPrefix(n, t):
			sum += prefixQuality
		case wordStart(n, t):
			sum += wordQuality
		case strings.Contains(n, t):
			sum += containQuality
		}
	}
	return sum / float64(len(terms))
}

// wordStart reports whether t occurs in n right after a separator ("my
// report", "q3_report", "2026-report").
func wordStart(n, t string) bool {
	for i := 0; ; {
		j := strings.Index(n[i:], t)
		if j < 0 {
			return false
		}
		i += j
		prev, _ := utf8.DecodeLastRuneInString(n[:i])
		if !unicode.IsLetter(prev) && !unicode.IsDigit(prev) {
			return true
		}
		i++
	}
}

// recency is recentWeight for a file modified now, decaying to 0 after
// recentWindow.
func recency(mod, now time.Time) float64 {
	if mod.IsZero() {
		return 0
	}
	age := now.Sub(mod)
	if age >= recentWindow {
		return 0
	}
	return recentWeight * (1 - max(age, 0).Seconds()/recentWindow.Seconds())
}
