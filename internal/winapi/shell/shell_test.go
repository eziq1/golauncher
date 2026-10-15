package shell

import (
	"slices"
	"strings"
	"testing"
)

func TestSkipApp(t *testing.T) {
	tests := []struct {
		app  App
		skip bool
	}{
		{App{Name: "7-Zip", ID: "x", Path: `C:\7zip\7zFM.exe`}, false},
		{App{Name: "7-Zip Help", ID: "x", Path: `C:\7zip\7-zip.chm`}, true},
		{App{Name: "Uninstall IDM", ID: "x", Path: `C:\IDM\Uninstall.exe`}, true},
		{App{Name: "Uninstall", ID: "x", Path: `C:\GCC\uninst.exe`}, true},
		{App{Name: "Sandboxie", ID: "x", Path: `C:\Sandboxie\unins000.exe`}, true},
		{App{Name: "卸载 Sandboxie-Plus", ID: "x", Path: `C:\S\x.exe`}, true},
		{App{Name: "Geek Uninstaller", ID: "x", Path: `C:\geek\geek.exe`}, false},
		{App{Name: "Website", ID: "x", Path: `C:\GCC\GCC.url`}, true},
		{App{Name: "PixPin on the Web", ID: "https://pixpin.cn"}, true},
		{App{Name: "Sandboxie-Plus 网站", ID: "http://sandboxie-plus.com/"}, true},
		{App{Name: "license", ID: "x", Path: `C:\IDM\license.txt`}, true},
		{App{Name: "Readme", ID: "x", Path: `C:\a\readme.exe`}, true},
		{App{Name: "Contoso Help Guide", ID: `{6D80}\Userguide.html`}, true},
		{App{Name: "Get Help", ID: "Microsoft.GetHelp_8wekyb3d8bbwe!App", Packaged: true}, false},
		{App{Name: "", ID: "x"}, true},
	}
	for _, tt := range tests {
		if got := skipApp(tt.app); got != tt.skip {
			t.Errorf("skipApp(%+v) = %v, want %v", tt.app, got, tt.skip)
		}
	}
}

func TestClean(t *testing.T) {
	got := clean([]App{
		{Name: "zed", ID: "z", Path: `"C:\zed\zed.exe"`},
		{Name: "Alpha", ID: "a", Path: `C:\a.exe`},
		{Name: "Alpha", ID: "a"},                    // same ID
		{Name: "Alpha", ID: "a2", Path: `C:\A.EXE`}, // same name and target
		{Name: "Uninstall Alpha", ID: "u", Path: `C:\unins000.exe`},
		{Name: "Beta", ID: "b!App", Packaged: true},
	})
	var ids []string
	for _, a := range got {
		ids = append(ids, a.ID)
	}
	if want := []string{"a", "b!App", "z"}; !slices.Equal(ids, want) {
		t.Fatalf("clean IDs = %v, want %v", ids, want)
	}
	if got[2].Path != `C:\zed\zed.exe` {
		t.Errorf("quotes not trimmed: %q", got[2].Path)
	}
	if p := (App{ID: "b!App"}).ShellPath(); p != `shell:AppsFolder\b!App` {
		t.Errorf("ShellPath = %q", p)
	}
}

func TestAddKeywords(t *testing.T) {
	versions := map[string][]string{
		`C:\Apps\Contoso\ContosoChat.exe`: {"Contoso Chat", "康托聊天", "康托聊天", "Contoso® Chat™"},
		`C:\Apps\Fabrikam\fab.exe`:        {"Fabrikam", "ファブリカム"},
		`C:\Apps\Shared\host.exe`:         {"共享宿主"},
		`C:\WINDOWS\system32\tool.exe`:    {"系统工具"},
		`C:\Apps\Long\long.exe`:           {"这是一个非常非常长的说明文字而不是一个应用的名称，这是一个非常非常长的说明文字，还有更多的说明"},
		`C:\Apps\Same\same.exe`:           {"同名"},
	}
	var asked []string
	apps := []App{
		{Name: "Contoso Chat", ID: "c", Path: `C:\Apps\Contoso\ContosoChat.exe`},
		// The Start menu shows the localized name of "Sketch Pad.lnk".
		{Name: "画板", ID: "s", Path: `C:\Apps\Sketch\sketch.exe`, LinkPath: `C:\Start\Sketch Pad.lnk`},
		{Name: "Fabrikam", ID: "f", Path: `C:\Apps\Fabrikam\fab.exe`, LinkPath: `C:\Start\Fabrikam.lnk`},
		{Name: "Host A", ID: "h1", Path: `C:\Apps\Shared\host.exe`, Args: "-a"},
		{Name: "Host B", ID: "h2", Path: `C:\Apps\Shared\HOST.exe`, Args: "-b"},
		{Name: "Tool", ID: "t", Path: `C:\WINDOWS\system32\tool.exe`},
		{Name: "Long", ID: "l", Path: `C:\Apps\Long\long.exe`},
		{Name: "同名", ID: "n", Path: `C:\Apps\Same\same.exe`},
		{Name: "Doc", ID: "d", Path: `C:\Apps\Doc\doc.lnk`},
		{Name: "Store App", ID: "x!App", Packaged: true},
	}
	addKeywords(apps, `C:\Windows`, func(exe string) []string {
		asked = append(asked, exe)
		return versions[exe]
	})
	want := map[string][]string{
		"c": {"康托聊天"},
		"s": {"Sketch Pad"},
		"f": {"ファブリカム"},
	}
	for _, a := range apps {
		if !slices.Equal(a.Keywords, want[a.ID]) {
			t.Errorf("%s keywords = %q, want %q", a.ID, a.Keywords, want[a.ID])
		}
	}
	for _, exe := range asked {
		if strings.Contains(strings.ToLower(exe), `\shared\`) || strings.Contains(strings.ToLower(exe), `\windows\`) || strings.HasSuffix(exe, ".lnk") {
			t.Errorf("version information of %s was read", exe)
		}
	}
	addKeywords(apps[:1:1], "", nil) // no resolver: no panic
}

func TestNonLatin(t *testing.T) {
	for s, want := range map[string]bool{
		"WeChat": false, "Café": false, "Microsoft® Windows®": false, "": false,
		"微信": true, "Contoso++主程序": true, "ファイル": true, "Привет": true,
	} {
		if got := nonLatin(s); got != want {
			t.Errorf("nonLatin(%q) = %v, want %v", s, got, want)
		}
	}
}
