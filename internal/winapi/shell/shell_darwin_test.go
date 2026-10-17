//go:build darwin

package shell

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// writeBundle creates a fake .app bundle with an XML Info.plist of the
// given keys and InfoPlist.strings per localization.
func writeBundle(t *testing.T, path string, info map[string]string, loc map[string]map[string]string) {
	t.Helper()
	plist := func(m map[string]string) string {
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<plist version="1.0"><dict>`)
		for k, v := range m {
			b.WriteString("<key>" + k + "</key><string>" + v + "</string>")
		}
		b.WriteString("</dict></plist>\n")
		return b.String()
	}
	contents := filepath.Join(path, "Contents")
	if err := os.MkdirAll(filepath.Join(contents, "Resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	if info != nil {
		os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist(info)), 0o644)
	}
	for l, m := range loc {
		dir := filepath.Join(contents, "Resources", l+".lproj")
		os.MkdirAll(dir, 0o755)
		// Old-style text, as most InfoPlist.strings are.
		var b strings.Builder
		for k, v := range m {
			b.WriteString(`"` + k + `" = "` + v + `";` + "\n")
		}
		os.WriteFile(filepath.Join(dir, "InfoPlist.strings"), []byte(b.String()), 0o644)
	}
}

func TestBundlePaths(t *testing.T) {
	root := t.TempDir()
	apps := filepath.Join(root, "Applications")
	for _, p := range []string{"A.app", "Utilities/U.app", "Vendor/V.app", "A.app/Contents/Helpers/Inner.app", "Deep/x/Too Deep.app"} {
		os.MkdirAll(filepath.Join(apps, p), 0o755)
	}
	os.WriteFile(filepath.Join(apps, "notes.txt"), nil, 0o644)
	os.Symlink(filepath.Join(apps, "A.app"), filepath.Join(apps, "Link to A.app"))
	extra := filepath.Join(root, "Finder.app")
	os.MkdirAll(extra, 0o755)

	got := bundlePaths(t.Context(), []string{apps, filepath.Join(root, "missing")}, extra, filepath.Join(root, "Gone.app"))
	for i := range got {
		got[i], _ = filepath.Rel(root, got[i])
	}
	want := []string{"Applications/A.app", "Applications/Utilities/U.app", "Applications/Vendor/V.app", "Finder.app"}
	if !slices.Equal(got, want) {
		t.Errorf("bundlePaths = %q, want %q", got, want)
	}
}

func TestAppOf(t *testing.T) {
	tests := []struct {
		name string
		path string
		info bundleInfo
		want App
		ok   bool
	}{
		{"localized", "/Applications/WeChat.app", bundleInfo{
			Plist:       map[string]string{"CFBundleIdentifier": "com.tencent.xinWeChat", "CFBundleName": "WeChat"},
			Localized:   map[string]map[string]string{"zh-Hans": {"CFBundleDisplayName": "微信"}, "zh-Hant": {"CFBundleDisplayName": "微信"}, "fr": {"CFBundleName": "WeChat FR"}},
			DisplayName: "微信",
		}, App{Name: "微信", ID: "com.tencent.xinWeChat", Path: "/Applications/WeChat.app"}, true}, // "WeChat" is the bundle's file name, which apps adds
		{"system app table", "/System/Applications/Calculator.app", bundleInfo{
			Plist:       map[string]string{"CFBundleIdentifier": "com.apple.calculator", "CFBundleName": "Calculator"},
			Localized:   map[string]map[string]string{"en": {"CFBundleName": "Calculator"}, "ja": {"CFBundleName": "計算機"}, "zh_CN": {"CFBundleName": "计算器"}, "zh_TW": {"CFBundleName": "計算機"}},
			DisplayName: "Calculator",
		}, App{Name: "Calculator", ID: "com.apple.calculator", Path: "/System/Applications/Calculator.app", Keywords: []string{"計算機", "计算器"}}, true},
		{"no identifier, Finder shows .app", "/Applications/Game.app", bundleInfo{DisplayName: "Game.app"},
			App{Name: "Game", ID: "/Applications/Game.app", Path: "/Applications/Game.app"}, true},
		{"no display name", "/Applications/Tool.app", bundleInfo{Plist: map[string]string{"CFBundleDisplayName": "Tool Pro", "CFBundleIdentifier": "t"}},
			App{Name: "Tool Pro", ID: "t", Path: "/Applications/Tool.app"}, true},
		{"background only", "/Applications/Agent.app", bundleInfo{Plist: map[string]string{"LSBackgroundOnly": "1"}, DisplayName: "Agent"}, App{}, false},
	}
	for _, tt := range tests {
		got, ok := appOf(tt.path, tt.info)
		if ok != tt.ok || got.Name != tt.want.Name || got.ID != tt.want.ID || got.Path != tt.want.Path || !slices.Equal(got.Keywords, tt.want.Keywords) {
			t.Errorf("%s: appOf = %+v, %v; want %+v, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

// TestInstalledAppsFake reads fake bundles with the real property list
// reader: XML Info.plist and old-style InfoPlist.strings.
func TestInstalledAppsFake(t *testing.T) {
	root := t.TempDir()
	writeBundle(t, filepath.Join(root, "WeChat.app"), map[string]string{"CFBundleIdentifier": "com.tencent.xinWeChat", "CFBundleName": "WeChat"},
		map[string]map[string]string{"zh-Hans": {"CFBundleDisplayName": "微信"}, "de": {"CFBundleDisplayName": "WeChat DE"}})
	writeBundle(t, filepath.Join(root, "Utilities", "Zed.app"), map[string]string{"CFBundleIdentifier": "dev.zed.Zed"}, nil)
	writeBundle(t, filepath.Join(root, "Zcopy", "WeChat.app"), map[string]string{"CFBundleIdentifier": "com.tencent.xinWeChat"}, nil)
	writeBundle(t, filepath.Join(root, "Broken.app"), nil, nil)
	os.MkdirAll(filepath.Join(root, "Empty.app"), 0o755) // no Contents: not a bundle

	apps, err := installedApps(t.Context(), readBundle, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range apps {
		names = append(names, a.Name+"|"+a.ID)
	}
	want := []string{"Broken|" + filepath.Join(root, "Broken.app"), "WeChat|com.tencent.xinWeChat", "Zed|dev.zed.Zed"}
	if !slices.Equal(names, want) {
		t.Fatalf("apps = %q, want %q", names, want)
	}
	if wx := apps[1]; !slices.Equal(wx.Keywords, []string{"微信"}) || wx.Path != filepath.Join(root, "WeChat.app") {
		t.Errorf("WeChat = %+v", wx)
	}
}

func TestKeywordLang(t *testing.T) {
	for loc, want := range map[string]bool{"en": true, "en_GB": true, "Base": true, "English": true, "zh-Hans": true, "zh_CN": true,
		"ja": true, "ko": true, "fr": false, "de_CH": false, "": false} {
		if got := keywordLang(loc); got != want {
			t.Errorf("keywordLang(%q) = %v", loc, got)
		}
	}
}

func TestSplitArgs(t *testing.T) {
	for s, want := range map[string][]string{
		``:                              {},
		`a b`:                           {"a", "b"},
		`  "https://x.com/?q=a b"  `:    {"https://x.com/?q=a b"},
		`--new-window 'it''s' "C:\a b"`: {"--new-window", "its", `C:\a b`},
		`"say \"hi\"" ''`:               {`say "hi"`, ""},
		"-x\t--flag=\"v w\"":            {"-x", "--flag=v w"},
		`'/Users/me/My Folder' --reuse`: {"/Users/me/My Folder", "--reuse"},
	} {
		if got := splitArgs(s); !slices.Equal(got, want) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("splitArgs(%q) = %q, want %q", s, got, want)
		}
	}
}

func TestOpenArgv(t *testing.T) {
	exists := func(p string) bool { return p == "/Users/me/proj" }
	for _, tt := range []struct {
		target string
		opts   plugin.OpenOptions
		want   string
	}{
		{"https://example.com", plugin.OpenOptions{}, "/usr/bin/open https://example.com"},
		{"/Applications/Safari.app", plugin.OpenOptions{}, "/usr/bin/open /Applications/Safari.app"},
		{"/Users/me/a.txt", plugin.OpenOptions{Reveal: true}, "/usr/bin/open -R /Users/me/a.txt"},
		{"/Applications/Firefox.app", plugin.OpenOptions{Args: `"https://x.com"`}, "/usr/bin/open -a /Applications/Firefox.app https://x.com"},
		{"/Applications/Code.app", plugin.OpenOptions{Args: `/Users/me/proj`}, "/usr/bin/open -a /Applications/Code.app /Users/me/proj"},
		{"/Applications/Chrome.app", plugin.OpenOptions{Args: `--incognito https://x.com`}, "/usr/bin/open -a /Applications/Chrome.app --args --incognito https://x.com"},
		{"/Applications/Code.app", plugin.OpenOptions{Args: `relative/dir`}, "/usr/bin/open -a /Applications/Code.app --args relative/dir"},
	} {
		if got := strings.Join(openArgv(tt.target, tt.opts, exists), " "); got != tt.want {
			t.Errorf("openArgv(%q, %+v) = %q, want %q", tt.target, tt.opts, got, tt.want)
		}
	}
	if err := Open("/x", plugin.OpenOptions{Verb: "runas"}); err == nil {
		t.Error("runas succeeded")
	}
	if err := Open("", plugin.OpenOptions{}); err == nil {
		t.Error("empty target succeeded")
	}
}

func TestBundleOf(t *testing.T) {
	for p, want := range map[string]string{
		"/Applications/Code.app/Contents/MacOS/Electron": "/Applications/Code.app",
		"/Applications/Code.app":                         "/Applications/Code.app",
		"/usr/bin/ls":                                    "",
		"":                                               "",
	} {
		if got := bundleOf(p); got != want {
			t.Errorf("bundleOf(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestBrowsersOf(t *testing.T) {
	names := map[string]string{"/A/Safari.app": "Safari", "/A/Firefox.app": "Firefox.app", "/A/Nameless.app": ""}
	got := browsersOf([]string{"/A/Safari.app", "/A/Firefox.app", "/A/Nameless.app"}, "/A/Firefox.app", func(p string) string { return names[p] })
	want := []Browser{{Name: "Firefox", Path: "/A/Firefox.app"}, {Name: "Safari", Path: "/A/Safari.app"}}
	if !slices.Equal(got, want) {
		t.Errorf("browsersOf = %+v", got)
	}
}

// TestSystemApps reads this Mac's apps, read-only: the system apps are
// always there.
func TestSystemApps(t *testing.T) {
	apps, err := InstalledApps(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]App{}
	for _, a := range apps {
		if a.Name == "" || a.ID == "" || !strings.HasSuffix(a.Path, ".app") {
			t.Errorf("app %+v", a)
		}
		byID[a.ID] = a
	}
	finder, ok := byID["com.apple.finder"]
	if !ok {
		t.Fatalf("no Finder in %d apps", len(apps))
	}
	calc, ok := byID["com.apple.calculator"]
	if !ok {
		t.Fatal("no Calculator")
	}
	// Calculator has Chinese and Japanese names, one of them a keyword
	// unless it is the displayed name.
	if !slices.ContainsFunc(append(calc.Keywords, calc.Name), func(k string) bool { return k == "计算器" }) {
		t.Errorf("Calculator = %+v", calc)
	}
	b, err := IconPNG(finder, 32)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil || img.Bounds().Dx() != 32 || img.Bounds().Dy() != 32 {
		t.Errorf("icon: %v, %v", img.Bounds(), err)
	}
	if _, err := IconPNG(App{Path: "/no/such/thing"}, 32); err == nil {
		t.Error("icon of a missing file")
	}
	if !ProtocolRegistered("https") || ProtocolRegistered("no-such-scheme-golauncher") {
		t.Error("ProtocolRegistered")
	}
	if FileDescription("/System/Applications/Calculator.app/Contents/MacOS/Calculator") == "" {
		t.Error("FileDescription of Calculator")
	}
	for _, br := range Browsers() {
		if br.Name == "" || !strings.HasSuffix(br.Path, ".app") {
			t.Errorf("browser %+v", br)
		}
	}
}
