package system

import (
	_ "embed"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/mozillazg/go-pinyin"

	winsys "github.com/eziq1/golauncher/internal/winapi/system"
	"github.com/eziq1/golauncher/pkg/plugin"
)

//go:embed settings.json
var settingsJSON []byte

// settingsPrefix starts the URI of every Windows Settings page.
const settingsPrefix = "ms-settings:"

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

// loadCatalog parses the embedded settings catalog.
func loadCatalog() ([]page, error) {
	var pages []page
	if err := json.Unmarshal(settingsJSON, &pages); err != nil {
		return nil, fmt.Errorf("system: settings.json: %w", err)
	}
	return pages, nil
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
		{id: "cmdAdmin", title: l.T("tool.cmdAdmin.title"), keywords: l.T("tool.cmdAdmin.keywords"), target: "cmd.exe", shown: "cmd", admin: true, icon: "terminal"},
	}
}

// categories returns the titles of the settings categories by key.
func categories(l plugin.Localizer) map[string]string {
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
