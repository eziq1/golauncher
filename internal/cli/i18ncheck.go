package cli

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"golauncher/internal/i18n"
)

// i18nReport is the result of glctl i18n check.
type i18nReport struct {
	OK       bool     `json:"ok"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
	Keys     int      `json:"keys"`
	Refs     int      `json:"refs"`
}

var (
	// Calls like loc.T("key") / .N("key", n) / core.T("key").
	refRe = regexp.MustCompile(`\.(?:T|N)\(\s*"([A-Za-z0-9_.\-]+)"`)
	// Manifest references like "%plugin.name%".
	manifestRe = regexp.MustCompile(`"%([A-Za-z0-9_.\-]+)%"`)
	fieldRe    = regexp.MustCompile(`\{\{\s*\.(\w+)`)
)

func (c *cli) i18n(ctx context.Context, args []string) error {
	fs := c.flagSet("i18n", nil)
	src := fs.String("src", ".", "source tree to scan for message references")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || pos[0] != "check" {
		return usagef("i18n: want check")
	}
	e, err := c.build(ctx, envFlags{Locale: "en"})
	if err != nil {
		return err
	}
	defer e.Close()
	rep := checkCatalogs(e.I18n)
	if err := checkReferences(e.I18n, *src, &rep); err != nil {
		return err
	}
	rep.OK = len(rep.Errors) == 0
	if err := c.output(rep, func(w io.Writer) {
		for _, s := range rep.Errors {
			fmt.Fprintln(w, "error:", s)
		}
		for _, s := range rep.Warnings {
			fmt.Fprintln(w, "warning:", s)
		}
		fmt.Fprintf(w, "%d keys, %d references, %d errors, %d warnings\n", rep.Keys, rep.Refs, len(rep.Errors), len(rep.Warnings))
	}); err != nil {
		return err
	}
	if !rep.OK {
		return fmt.Errorf("i18n check found %d errors", len(rep.Errors))
	}
	return nil
}

// checkCatalogs compares every locale with the source locale and checks
// that all messages are valid templates, also after pseudo-localization.
func checkCatalogs(m *i18n.Manager) i18nReport {
	var rep i18nReport
	for _, ns := range m.Namespaces() {
		src := m.Keys(ns, i18n.Source)
		rep.Keys += len(src)
		for _, id := range src {
			for _, text := range m.Texts(ns, i18n.Source, id) {
				for _, t := range []string{text, i18n.Pseudolocalize(text)} {
					if _, err := template.New("").Parse(t); err != nil {
						rep.Errors = append(rep.Errors, fmt.Sprintf("%s:%s: bad template: %v", ns, id, err))
					}
				}
			}
		}
		for _, loc := range m.Locales(ns) {
			if loc == i18n.Source {
				continue
			}
			keys := m.Keys(ns, loc)
			for _, id := range src {
				if !slices.Contains(keys, id) {
					rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s:%s: missing in %s (falls back to %s)", ns, id, loc, i18n.Source))
					continue
				}
				if a, b := fields(m.Texts(ns, i18n.Source, id)), fields(m.Texts(ns, loc, id)); !slices.Equal(a, b) {
					rep.Errors = append(rep.Errors, fmt.Sprintf("%s:%s: %s uses arguments %v, %s uses %v", ns, id, i18n.Source, a, loc, b))
				}
			}
			for _, id := range keys {
				if !slices.Contains(src, id) {
					rep.Errors = append(rep.Errors, fmt.Sprintf("%s:%s: only in %s", ns, id, loc))
				}
			}
		}
	}
	return rep
}

// fields returns the sorted, unique template arguments of texts.
func fields(texts []string) []string {
	var out []string
	for _, t := range texts {
		for _, m := range fieldRe.FindAllStringSubmatch(t, -1) {
			out = append(out, m[1])
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// checkReferences scans Go sources for message keys and reports keys that
// do not exist (errors) and catalog keys never referenced (warnings).
// Code under plugins/<dir> resolves keys in its plugin's namespace
// ("builtin.<dir>") and then in core, like the plugin's Localizer.
func checkReferences(m *i18n.Manager, root string, rep *i18nReport) error {
	used := map[string]bool{} // "ns:key"
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "testdata" || name == ".git" || (strings.HasPrefix(name, ".") && path != root) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		ns := namespaceOf(root, path)
		var keys []string
		for line := range strings.Lines(string(data)) {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue // examples in comments are not references
			}
			for _, re := range []*regexp.Regexp{refRe, manifestRe} {
				for _, sm := range re.FindAllStringSubmatch(line, -1) {
					keys = append(keys, sm[1])
				}
			}
		}
		for _, key := range keys {
			rep.Refs++
			switch {
			case m.Texts(ns, i18n.Source, key) != nil:
				used[ns+":"+key] = true
			case m.Texts(i18n.CoreNamespace, i18n.Source, key) != nil:
				used[i18n.CoreNamespace+":"+key] = true
			default:
				rel, _ := filepath.Rel(root, path)
				rep.Errors = append(rep.Errors, fmt.Sprintf("%s: key %q not found in %s or core", filepath.ToSlash(rel), key, ns))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, ns := range m.Namespaces() {
		for _, id := range m.Keys(ns, i18n.Source) {
			if !used[ns+":"+id] {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s:%s: never referenced (or built dynamically)", ns, id))
			}
		}
	}
	return nil
}

func namespaceOf(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return i18n.CoreNamespace
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) > 2 && parts[0] == "plugins" {
		return "builtin." + parts[1]
	}
	return i18n.CoreNamespace
}
