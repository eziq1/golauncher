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
