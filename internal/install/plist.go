package install

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

// Property lists of the macOS app bundle: its Info.plist, the localized
// InfoPlist.strings, and the launch agent that starts it at login when the
// system's login items cannot. They are generated here rather than kept
// as files so that they are tested, and scripts/macos/build.sh writes them
// with scripts/macos/macbundle.

// BundleIcon is the icon file in Contents/Resources (CFBundleIconFile).
const BundleIcon = AppName + ".icns"

// usage are the purpose strings macOS shows when it asks the user for a
// permission, in English (Info.plist) and by localization
// (<lang>.lproj/InfoPlist.strings). Accessibility, which pasting needs,
// has no such string: macOS shows its own prompt.
var usage = map[string]map[string]string{
	"en": {
		"NSAppleEventsUsageDescription":          "golauncher sends Apple events to Finder, System Events and terminal apps to read the current folder and selection, empty the Trash, switch between light and dark, and restart, shut down or log out when you ask it to.",
		"NSDesktopFolderUsageDescription":        "golauncher searches and opens the files on your Desktop that you look for.",
		"NSDocumentsFolderUsageDescription":      "golauncher searches and opens the documents you look for.",
		"NSDownloadsFolderUsageDescription":      "golauncher searches and opens the downloads you look for.",
		"NSRemovableVolumesUsageDescription":     "golauncher searches and opens the files you look for on removable drives.",
		"NSNetworkVolumesUsageDescription":       "golauncher searches and opens the files you look for on network drives.",
		"NSLocalNetworkUsageDescription":         "golauncher's network tools reach devices on your local network when you ping a host or check a port.",
		"NSLocationWhenInUseUsageDescription":    "golauncher shows the name of the Wi-Fi network you are connected to, which macOS treats as your location.",
		"NSLocationUsageDescription":             "golauncher shows the name of the Wi-Fi network you are connected to, which macOS treats as your location.",
		"NSSystemAdministrationUsageDescription": "golauncher asks for an administrator to flush the DNS cache when you ask it to.",
	},
	"zh-Hans": {
		"NSAppleEventsUsageDescription":          "golauncher 会向访达、System Events 和终端应用发送 Apple 事件，用来读取当前文件夹和选中的项目、清空废纸篓、切换浅色和深色，以及在你要求时重启、关机或退出登录。",
		"NSDesktopFolderUsageDescription":        "golauncher 会搜索并打开你在桌面上查找的文件。",
		"NSDocumentsFolderUsageDescription":      "golauncher 会搜索并打开你查找的文稿。",
		"NSDownloadsFolderUsageDescription":      "golauncher 会搜索并打开你查找的下载项。",
		"NSRemovableVolumesUsageDescription":     "golauncher 会在可移除磁盘上搜索并打开你查找的文件。",
		"NSNetworkVolumesUsageDescription":       "golauncher 会在网络磁盘上搜索并打开你查找的文件。",
		"NSLocalNetworkUsageDescription":         "在你 Ping 主机或检测端口时，golauncher 的网络工具会访问本地网络上的设备。",
		"NSLocationWhenInUseUsageDescription":    "golauncher 会显示你连接的 Wi-Fi 网络名称，macOS 把它视为位置信息。",
		"NSLocationUsageDescription":             "golauncher 会显示你连接的 Wi-Fi 网络名称，macOS 把它视为位置信息。",
		"NSSystemAdministrationUsageDescription": "在你要求刷新 DNS 缓存时，golauncher 需要管理员授权。",
	},
}

// Localizations are the app's localizations (CFBundleLocalizations), as
// .lproj folder names; the first is the development region.
var Localizations = []string{"en", "zh-Hans"}

// BundleInfo describes the app bundle for its Info.plist.
type BundleInfo struct {
	// Version is golauncher's version ("0.6.0", "0.6.0-rc.1",
	// "0.0.0-abc1234-dirty").
	Version string
}

// BundleVersion returns the CFBundleShortVersionString of a version:
// MAJOR.MINOR.PATCH, numbers only, as macOS requires; "0.0.0" when the
// version has none. The full version is under VersionKey.
func BundleVersion(version string) string {
	m := bundleVersionRe.FindStringSubmatch(normalize(version))
	if m == nil {
		return "0.0.0"
	}
	if m[3] == "" {
		m[3] = "0"
	}
	return m[1] + "." + m[2] + "." + m[3]
}

var bundleVersionRe = regexp.MustCompile(`^(\d+)\.(\d+)(?:\.(\d+))?`)

// InfoPlist returns Contents/Info.plist: the bundle's identity and
// version, no Dock icon (LSUIElement, a menu bar app), the minimum macOS
// version, the icon and the permission purpose strings.
func InfoPlist(b BundleInfo) []byte {
	short := BundleVersion(b.Version)
	d := map[string]any{
		"CFBundleName":                         AppName,
		"CFBundleDisplayName":                  AppName,
		"CFBundleIdentifier":                   BundleID,
		"CFBundleExecutable":                   BundleExeName,
		"CFBundleIconFile":                     BundleIcon,
		"CFBundlePackageType":                  "APPL",
		"CFBundleInfoDictionaryVersion":        "6.0",
		"CFBundleShortVersionString":           short,
		"CFBundleVersion":                      short,
		"CFBundleDevelopmentRegion":            Localizations[0],
		"CFBundleLocalizations":                stringsAny(Localizations),
		VersionKey:                             normalize(b.Version),
		"LSApplicationCategoryType":            "public.app-category.productivity",
		"LSMinimumSystemVersion":               MinSystemVersion,
		"LSUIElement":                          true,
		"NSHighResolutionCapable":              true,
		"NSPrincipalClass":                     "NSApplication",
		"NSSupportsAutomaticGraphicsSwitching": true,
		"NSHumanReadableCopyright":             "Copyright © " + Publisher + ". MIT License.",
	}
	for k, v := range usage["en"] {
		d[k] = v
	}
	return plistDoc(d)
}

// InfoPlistStrings returns <lang>.lproj/InfoPlist.strings, the purpose
// strings in the language of a localization (a property list, which
// .strings files may be).
func InfoPlistStrings(lang string) ([]byte, error) {
	if !slices.Contains(Localizations, lang) {
		return nil, fmt.Errorf("install: no localization %q", lang)
	}
	d := map[string]any{}
	for k, v := range usage[lang] {
		d[k] = v
	}
	return plistDoc(d), nil
}

// LaunchAgentLabel is the launch agent's label and file name (without
// .plist) in ~/Library/LaunchAgents.
const LaunchAgentLabel = BundleID + ".login"

// LaunchAgentPlist returns a launch agent that opens the app bundle app
// with args when the user logs in, through Launch Services (open), so
// that the app runs as itself rather than as a child of launchd's job.
func LaunchAgentPlist(app string, args []string) []byte {
	prog := []any{"/usr/bin/open", "-a", app}
	if len(args) > 0 {
		prog = append(prog, "--args")
		prog = append(prog, stringsAny(args)...)
	}
	return plistDoc(map[string]any{
		"Label":            LaunchAgentLabel,
		"ProgramArguments": prog,
		"RunAtLoad":        true,
		"ProcessType":      "Interactive",
		"LimitLoadToSessionType": []any{
			"Aqua",
		},
	})
}

func stringsAny(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// plistDoc returns d as an XML property list, keys sorted.
func plistDoc(d map[string]any) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
`)
	writePlist(&b, d, "")
	b.WriteString("</plist>\n")
	return b.Bytes()
}

func writePlist(b *bytes.Buffer, v any, indent string) {
	esc := func(s string) string {
		var e bytes.Buffer
		_ = xml.EscapeText(&e, []byte(s))
		return e.String()
	}
	switch v := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		b.WriteString(indent + "<dict>\n")
		for _, k := range keys {
			fmt.Fprintf(b, "%s\t<key>%s</key>\n", indent, esc(k))
			writePlist(b, v[k], indent+"\t")
		}
		b.WriteString(indent + "</dict>\n")
	case []any:
		b.WriteString(indent + "<array>\n")
		for _, x := range v {
			writePlist(b, x, indent+"\t")
		}
		b.WriteString(indent + "</array>\n")
	case bool:
		if v {
			b.WriteString(indent + "<true/>\n")
		} else {
			b.WriteString(indent + "<false/>\n")
		}
	case string:
		fmt.Fprintf(b, "%s<string>%s</string>\n", indent, esc(v))
	default:
		panic(fmt.Sprintf("install: no plist type for %T", v))
	}
}

// ErrBinaryPlist: the property list is in the binary format, which
// PlistStrings does not read (plutil can convert it).
var ErrBinaryPlist = errors.New("install: binary property list")

// PlistStrings returns the string values of the top-level dictionary of an
// XML property list (an Info.plist), by key; other values are skipped.
func PlistStrings(data []byte) (map[string]string, error) {
	if bytes.HasPrefix(data, []byte("bplist")) {
		return nil, ErrBinaryPlist
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	out := map[string]string{}
	depth := 0 // of dict and array elements
	key := ""
	inDict := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("install: reading a property list: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "plist":
			case "dict", "array":
				depth++
				if depth == 1 && t.Name.Local == "dict" {
					inDict = true
				}
				key = ""
			case "key":
				var s string
				if err := dec.DecodeElement(&s, &t); err != nil {
					return nil, err
				}
				if depth == 1 {
					key = s
				}
			case "string":
				var s string
				if err := dec.DecodeElement(&s, &t); err != nil {
					return nil, err
				}
				if depth == 1 && key != "" {
					out[key] = s
				}
				key = ""
			default:
				if err := dec.Skip(); err != nil {
					return nil, err
				}
				key = ""
			}
		case xml.EndElement:
			if t.Name.Local == "dict" || t.Name.Local == "array" {
				depth--
				key = ""
			}
		}
	}
	if !inDict {
		return nil, errors.New("install: the property list has no dictionary")
	}
	return out, nil
}

// PlistVersion returns golauncher's version from an Info.plist: VersionKey,
// else CFBundleShortVersionString.
func PlistVersion(info map[string]string) string {
	if v := strings.TrimSpace(info[VersionKey]); v != "" {
		return v
	}
	return strings.TrimSpace(info["CFBundleShortVersionString"])
}
