package system

import (
	_ "embed"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/mozillazg/go-pinyin"

	winsys "github.com/eziq1/golauncher/internal/winapi/system"
	"github.com/eziq1/golauncher/pkg/plugin"
)

//go:embed settings.json
var settingsJSON []byte

// settingsMacJSON is the catalog of macOS System Settings panes, opened
// with x-apple.systempreferences: URLs. The pane IDs are those of the
// System Settings extensions of macOS 13 and later (checked on macOS 15);
// the Privacy_* anchors are the older pane's, which still open the right
// page.
//
//go:embed settings_macos.json
var settingsMacJSON []byte

// settingsPrefix starts the URI of every Windows Settings page, and
// macSettingsPrefix that of every macOS System Settings pane.
const (
	settingsPrefix    = "ms-settings:"
	macSettingsPrefix = "x-apple.systempreferences:"
)

// page is a Windows Settings page of the embedded catalog.
type page struct {
	URI      string   `json:"uri"` // "ms-settings:bluetooth"
	Category string   `json:"cat"` // key of the category title, "cat.<Category>"
	En       string   `json:"en"`
	Zh       string   `json:"zh"`
	Keywords []string `json:"kw"`
	// Win limits the page to Windows "10" or "11"; "" for both.
	Win string `json:"win,omitempty"`
}

// loadCatalog parses the embedded settings catalog of Windows, or of macOS
// when mac is set.
func loadCatalog(mac bool) ([]page, error) {
	data, name := settingsJSON, "settings.json"
	if mac {
		data, name = settingsMacJSON, "settings_macos.json"
	}
	var pages []page
	if err := json.Unmarshal(data, &pages); err != nil {
		return nil, fmt.Errorf("system: %s: %w", name, err)
	}
	return pages, nil
}

// pageID is the part of a settings URI that names the page.
func pageID(uri string) string {
	if id, ok := strings.CutPrefix(uri, macSettingsPrefix); ok {
		return id
	}
	return strings.TrimPrefix(uri, settingsPrefix)
}

// availableOn reports whether the page exists on Windows build build (0 when
// unknown: every page is shown).
func (pg page) availableOn(build int) bool {
	switch {
	case build == 0 || pg.Win == "":
		return true
	case pg.Win == "11":
		return build >= winsys.Win11Build
	case pg.Win == "10":
		return build < winsys.Win11Build
	}
	return true
}

// tool is a classic Windows tool (Control Panel, MMC snap-ins, …).
type tool struct {
	id       string
	title    string
	keywords string // comma-separated, from the catalog
	target   string // what ActionOpen opens
	args     string
	// shown is the command shown as the accessory, e.g. "devmgmt.msc".
	shown string
	// admin runs the tool elevated by default (the primary action).
	admin bool
	// pro marks tools missing from Home editions (gpedit.msc, lusrmgr.msc).
	pro  bool
	icon string
}

// tools returns the classic tools with their titles in the current locale.
func tools(l plugin.Localizer) []tool {
	return []tool{
		{id: "control", title: l.T("tool.control.title"), keywords: l.T("tool.control.keywords"), target: "control.exe", shown: "control", icon: "settings"},
		{id: "appwiz", title: l.T("tool.appwiz.title"), keywords: l.T("tool.appwiz.keywords"), target: "control.exe", args: "appwiz.cpl", shown: "appwiz.cpl", icon: "package"},
		{id: "ncpa", title: l.T("tool.ncpa.title"), keywords: l.T("tool.ncpa.keywords"), target: "control.exe", args: "ncpa.cpl", shown: "ncpa.cpl", icon: "settings"},
		{id: "sysdm", title: l.T("tool.sysdm.title"), keywords: l.T("tool.sysdm.keywords"), target: "control.exe", args: "sysdm.cpl", shown: "sysdm.cpl", icon: "settings"},
		{id: "envvars", title: l.T("tool.envvars.title"), keywords: l.T("tool.envvars.keywords"), target: "rundll32.exe", args: "sysdm.cpl,EditEnvironmentVariables", shown: "rundll32 sysdm.cpl,EditEnvironmentVariables", icon: "settings"},
		{id: "devmgmt", title: l.T("tool.devmgmt.title"), keywords: l.T("tool.devmgmt.keywords"), target: "devmgmt.msc", shown: "devmgmt.msc", icon: "wrench"},
		{id: "services", title: l.T("tool.services.title"), keywords: l.T("tool.services.keywords"), target: "services.msc", shown: "services.msc", icon: "wrench"},
		{id: "diskmgmt", title: l.T("tool.diskmgmt.title"), keywords: l.T("tool.diskmgmt.keywords"), target: "diskmgmt.msc", shown: "diskmgmt.msc", icon: "wrench"},
		{id: "eventvwr", title: l.T("tool.eventvwr.title"), keywords: l.T("tool.eventvwr.keywords"), target: "eventvwr.msc", shown: "eventvwr.msc", icon: "wrench"},
		{id: "taskschd", title: l.T("tool.taskschd.title"), keywords: l.T("tool.taskschd.keywords"), target: "taskschd.msc", shown: "taskschd.msc", icon: "wrench"},
		{id: "compmgmt", title: l.T("tool.compmgmt.title"), keywords: l.T("tool.compmgmt.keywords"), target: "compmgmt.msc", shown: "compmgmt.msc", icon: "wrench"},
		{id: "gpedit", title: l.T("tool.gpedit.title"), keywords: l.T("tool.gpedit.keywords"), target: "gpedit.msc", shown: "gpedit.msc", pro: true, icon: "wrench"},
		{id: "lusrmgr", title: l.T("tool.lusrmgr.title"), keywords: l.T("tool.lusrmgr.keywords"), target: "lusrmgr.msc", shown: "lusrmgr.msc", pro: true, icon: "wrench"},
		{id: "regedit", title: l.T("tool.regedit.title"), keywords: l.T("tool.regedit.keywords"), target: "regedit.exe", shown: "regedit", icon: "wrench"},
		{id: "taskmgr", title: l.T("tool.taskmgr.title"), keywords: l.T("tool.taskmgr.keywords"), target: "taskmgr.exe", shown: "taskmgr", icon: "activity"},
		{id: "resmon", title: l.T("tool.resmon.title"), keywords: l.T("tool.resmon.keywords"), target: "resmon.exe", shown: "resmon", icon: "activity"},
		{id: "msconfig", title: l.T("tool.msconfig.title"), keywords: l.T("tool.msconfig.keywords"), target: "msconfig.exe", shown: "msconfig", icon: "wrench"},
		{id: "msinfo32", title: l.T("tool.msinfo32.title"), keywords: l.T("tool.msinfo32.keywords"), target: "msinfo32.exe", shown: "msinfo32", icon: "info"},
		{id: "cleanmgr", title: l.T("tool.cleanmgr.title"), keywords: l.T("tool.cleanmgr.keywords"), target: "cleanmgr.exe", shown: "cleanmgr", icon: "trash"},
		{id: "cmdAdmin", title: l.T("tool.cmdAdmin.title"), keywords: l.T("tool.cmdAdmin.keywords"), target: "cmd.exe", shown: "cmd", admin: true, icon: "square-terminal"},
	}
}

// macTools returns the macOS utilities with their titles in the current
// locale. Keychain Access moved to CoreServices in macOS 15.
func macTools(l plugin.Localizer) []tool {
	const utilities = "/System/Applications/Utilities/"
	keychain := "/System/Library/CoreServices/Applications/Keychain Access.app"
	if _, err := os.Stat(keychain); err != nil {
		keychain = utilities + "Keychain Access.app"
	}
	app := func(id, title, keywords, path, icon string) tool {
		return tool{id: id, title: title, keywords: keywords, target: path,
			shown: strings.TrimSuffix(filepath.Base(path), ".app"), icon: icon}
	}
	return []tool{
		app("activityMonitor", l.T("mac.tool.activityMonitor.title"), l.T("mac.tool.activityMonitor.keywords"), utilities+"Activity Monitor.app", "activity"),
		app("diskUtility", l.T("mac.tool.diskUtility.title"), l.T("mac.tool.diskUtility.keywords"), utilities+"Disk Utility.app", "wrench"),
		app("console", l.T("mac.tool.console.title"), l.T("mac.tool.console.keywords"), utilities+"Console.app", "wrench"),
		app("keychain", l.T("mac.tool.keychain.title"), l.T("mac.tool.keychain.keywords"), keychain, "lock"),
		app("systemInformation", l.T("mac.tool.systemInformation.title"), l.T("mac.tool.systemInformation.keywords"), utilities+"System Information.app", "info"),
		app("terminal", l.T("mac.tool.terminal.title"), l.T("mac.tool.terminal.keywords"), utilities+"Terminal.app", "terminal"),
		app("scriptEditor", l.T("mac.tool.scriptEditor.title"), l.T("mac.tool.scriptEditor.keywords"), utilities+"Script Editor.app", "wrench"),
		app("systemSettings", l.T("mac.tool.systemSettings.title"), l.T("mac.tool.systemSettings.keywords"), "/System/Applications/System Settings.app", "settings"),
	}
}

// categories returns the titles of the settings categories by key.
func categories(l plugin.Localizer, mac bool) map[string]string {
	if mac {
		c := categories(l, false)
		c["update"], c["tools"] = l.T("mac.cat.update"), l.T("mac.cat.tools")
		return c
	}
	return map[string]string{
		"system":          l.T("cat.system"),
		"devices":         l.T("cat.devices"),
		"network":         l.T("cat.network"),
		"personalization": l.T("cat.personalization"),
		"apps":            l.T("cat.apps"),
		"accounts":        l.T("cat.accounts"),
		"time":            l.T("cat.time"),
		"gaming":          l.T("cat.gaming"),
		"accessibility":   l.T("cat.accessibility"),
		"privacy":         l.T("cat.privacy"),
		"update":          l.T("cat.update"),
		"tools":           l.T("cat.tools"),
	}
}

// splitKeywords splits a comma-separated keyword list.
func splitKeywords(s string) []string {
	var out []string
	for w := range strings.SplitSeq(s, ",") {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// withPinyin returns words followed by the pinyin of those holding Chinese
// characters, spelled with spaces between syllables ("更新" -> "geng xin"),
// so the host matches "gengxin", "geng" and the initials "gx" against them.
// Duplicates and empty words are left out.
func withPinyin(words []string) []string {
	out := make([]string, 0, 2*len(words))
	add := func(w string) {
		if w != "" && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	for _, w := range words {
		add(w)
	}
	for _, w := range words {
		add(pinyinOf(w))
	}
	return out
}

// withTitlePinyin returns words followed by the pinyin of the first one
// holding Chinese characters: the Chinese name of a tool.
func withTitlePinyin(words []string) []string {
	for _, w := range words {
		if py := pinyinOf(w); py != "" {
			return append(slices.Clone(words), py)
		}
	}
	return words
}

var pinyinArgs = pinyin.NewArgs()

// pinyinOf spells the Chinese characters of s in pinyin, one word per
// syllable, keeping other runs as words ("Windows 更新" -> "windows geng
// xin"). It returns "" when s has no Chinese characters.
func pinyinOf(s string) string {
	var words []string
	var run strings.Builder
	han := false
	flush := func() {
		if run.Len() > 0 {
			words = append(words, strings.ToLower(run.String()))
			run.Reset()
		}
	}
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			if p := pinyin.SinglePinyin(r, pinyinArgs); len(p) > 0 {
				flush()
				words = append(words, p[0])
				han = true
				continue
			}
		}
		if unicode.IsSpace(r) || strings.ContainsRune("（）()&,", r) {
			flush()
			continue
		}
		run.WriteRune(r)
	}
	flush()
	if !han {
		return ""
	}
	return strings.Join(words, " ")
}
