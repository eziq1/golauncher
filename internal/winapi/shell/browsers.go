package shell

import "strings"

// Browser is a web browser registered with Windows.
type Browser struct {
	// Name is the browser's display name, e.g. "Firefox".
	Name string `json:"name"`
	// Path is the browser's program.
	Path string `json:"path"`
}

// commandProgram returns the program of a command line such as
// `"C:\Program Files\App\app.exe" -osint -url "%1"`.
func commandProgram(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if rest, ok := strings.CutPrefix(cmd, `"`); ok {
		prog, _, _ := strings.Cut(rest, `"`)
		return prog
	}
	if i := strings.Index(strings.ToLower(cmd), ".exe"); i >= 0 {
		return cmd[:i+len(".exe")]
	}
	prog, _, _ := strings.Cut(cmd, " ")
	return prog
}
