// Package shell lists the installed applications of the Windows Start menu,
// draws their icons and opens files, URLs and apps through the Windows shell.
//
// Every function may be called from any goroutine; none needs a GUI message
// loop. On other platforms the functions return errors.ErrUnsupported.
//
// The COM code is adapted from the MyGo framework
// (github.com/egoist/mygo, internal/windows, MIT License).
package shell

import (
	"cmp"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// AppsFolderPrefix is the shell path prefix of the Start menu's Applications
// folder. AppsFolderPrefix + App.ID names an app as a shell item.
const AppsFolderPrefix = `shell:AppsFolder\`

// App is an application of the Start menu's "All apps" list.
type App struct {
	Name string `json:"name"`
	// ID is the AppUserModelID of packaged apps, or the parsing name of the
	// item in the Applications folder. It is unique.
	ID string `json:"id"`
	// Path is the target executable, or the shortcut when the target cannot
	// be resolved. It is empty for packaged apps.
	Path string `json:"path,omitempty"`
	// LinkPath is the Start menu shortcut, if any.
	LinkPath string `json:"linkPath,omitempty"`
	Args     string `json:"args,omitempty"`
	// Packaged is set for UWP/MSIX apps.
	Packaged bool `json:"packaged,omitzero"`
	// Keywords are other names of the app: the shortcut's file name when the
	// Start menu shows a localized name, and the non-Latin names of the
	// executable's version information (a Chinese FileDescription, say).
	Keywords []string `json:"keywords,omitempty"`
}

// ShellPath returns the Applications folder item of the app,
// shell:AppsFolder\<ID>, which the shell can open and draw.
func (a App) ShellPath() string { return AppsFolderPrefix + a.ID }

// skipExts are target extensions of documentation and web links.
var skipExts = []string{".url", ".chm", ".txt", ".html", ".htm", ".pdf", ".rtf", ".md", ".hlp"}

// skipName matches names of uninstallers, help, readme and website links.
var skipName = regexp.MustCompile(`(?i)\b(uninstall|help|readme|read me|website|documentation|license)\b|on the web|卸载|帮助|网站|說明|卸載`)

// skipApp reports whether a is an uninstaller, a help/readme/website link or
// otherwise not an application worth listing.
func skipApp(a App) bool {
	if a.Name == "" || a.ID == "" {
		return true
	}
	if a.Packaged {
		return false
	}
	target := cmp.Or(a.Path, a.ID)
	if strings.Contains(target, "://") {
		return true
	}
	if slices.Contains(skipExts, strings.ToLower(filepath.Ext(target))) {
		return true
	}
	if base := strings.ToLower(filepath.Base(target)); strings.HasPrefix(base, "unins") {
		return true
	}
	return skipName.MatchString(a.Name)
}

// clean filters, dedupes and sorts apps by name.
func clean(apps []App) []App {
	seenID := map[string]bool{}
	seenTarget := map[string]bool{}
	out := make([]App, 0, len(apps))
	for _, a := range apps {
		a.Path = strings.Trim(a.Path, `"`)
		if skipApp(a) || seenID[a.ID] {
			continue
		}
		seenID[a.ID] = true
		if a.Path != "" {
			// The same shortcut target under the same name is listed once.
			k := strings.ToLower(a.Name) + "\x00" + strings.ToLower(a.Path) + "\x00" + a.Args
			if seenTarget[k] {
				continue
			}
			seenTarget[k] = true
		}
		out = append(out, a)
	}
	slices.SortStableFunc(out, func(x, y App) int {
		return cmp.Or(cmp.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)), cmp.Compare(x.ID, y.ID))
	})
	return out
}

// maxNameLen bounds the length, in runes, of a version-information name
// used as a keyword; longer strings are descriptions, not names.
const maxNameLen = 40

// addKeywords sets the Keywords of apps: the file name of the shortcut when
// it differs from the displayed name, and the names versionNames finds in
// the version information of the target executable. Version names are used
// only when they contain a non-Latin letter (a Chinese or Japanese name, say:
// English ones are mostly the displayed name again, or generic, such as
// "Electron"), and only for executables outside winDir that no other app
// targets, since the version information of a shared host (cmd.exe, a
// runtime) does not describe the app. versionNames may be nil.
func addKeywords(apps []App, winDir string, versionNames func(exe string) []string) {
	targets := map[string]int{}
	for _, a := range apps {
		if !a.Packaged && a.Path != "" {
			targets[strings.ToLower(a.Path)]++
		}
	}
	winDir = strings.ToLower(filepath.Clean(winDir))
	for i := range apps {
		a := &apps[i]
		if a.Packaged {
			continue
		}
		add := func(name string) {
			name = strings.TrimSpace(name)
			if name == "" || strings.EqualFold(name, a.Name) ||
				slices.ContainsFunc(a.Keywords, func(k string) bool { return strings.EqualFold(k, name) }) {
				return
			}
			a.Keywords = append(a.Keywords, name)
		}
		if a.LinkPath != "" {
			add(strings.TrimSuffix(filepath.Base(a.LinkPath), filepath.Ext(a.LinkPath)))
		}
		p := strings.ToLower(a.Path)
		if versionNames == nil || !strings.HasSuffix(p, ".exe") || targets[p] > 1 ||
			winDir != "" && winDir != "." && strings.HasPrefix(p, winDir+`\`) {
			continue
		}
		for _, n := range versionNames(a.Path) {
			if nonLatin(n) && utf8.RuneCountInString(n) <= maxNameLen {
				add(n)
			}
		}
	}
}

// nonLatin reports whether s contains a letter of a non-Latin script.
func nonLatin(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) && !unicode.Is(unicode.Latin, r) {
			return true
		}
	}
	return false
}
