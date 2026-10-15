package shell

import (
	"slices"
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
		{App{Name: "Ryzen Master Help Guide", ID: `{6D80}\Userguide.html`}, true},
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
