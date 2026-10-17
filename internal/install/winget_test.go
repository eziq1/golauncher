package install

import (
	"path/filepath"
	"testing"
)

func TestWingetManaged(t *testing.T) {
	p := filepath.FromSlash
	user := p("C:/Users/someone/AppData/Local/Microsoft/WinGet/Packages/eziq1.golauncher_Microsoft.Winget.Source_8wekyb3d8bbwe")
	custom := p("D:/Tools/golauncher")
	for _, tc := range []struct {
		name string
		exe  string
		locs []string
		want bool
	}{
		{"user scope", filepath.Join(user, "golauncher.exe"), nil, true},
		{"case", p("c:/users/someone/appdata/local/microsoft/winget/packages/EZIQ1.GOLAUNCHER_Microsoft.Winget.Source_8wekyb3d8bbwe/GOLAUNCHER.EXE"), nil, true},
		{"subfolder", filepath.Join(user, "bin", "golauncher.exe"), nil, true},
		{"machine scope", p("C:/Program Files/WinGet/Packages/eziq1.golauncher_Microsoft.Winget.Source_8wekyb3d8bbwe/golauncher.exe"), nil, true},
		{"other source", p("C:/Program Files (x86)/WinGet/Packages/eziq1.golauncher_mysource/golauncher.exe"), nil, true},
		{"--location", filepath.Join(custom, "golauncher.exe"), []string{"", custom}, true},
		{"--location, case", p("d:/tools/GOLAUNCHER/golauncher.exe"), []string{custom}, true},
		{"self-installed", p("C:/Users/someone/AppData/Local/Programs/golauncher/golauncher.exe"), []string{custom}, false},
		{"portable download", p("C:/Users/someone/Downloads/golauncher/golauncher.exe"), nil, false},
		{"another package", p("C:/Users/someone/AppData/Local/Microsoft/WinGet/Packages/eziq1.golauncher2_src/golauncher.exe"), nil, false},
		{"no source suffix", p("C:/Users/someone/AppData/Local/Microsoft/WinGet/Packages/eziq1.golauncher/golauncher.exe"), nil, false},
		{"not under WinGet", p("C:/Packages/eziq1.golauncher_src/golauncher.exe"), nil, false},
		{"sibling of location", p("D:/Tools/golauncher-old/golauncher.exe"), []string{custom}, false},
		{"the location itself", custom, []string{custom}, false},
		{"empty", "", []string{custom}, false},
	} {
		if got := WingetManaged(tc.exe, tc.locs); got != tc.want {
			t.Errorf("%s: WingetManaged(%q, %q) = %v, want %v", tc.name, tc.exe, tc.locs, got, tc.want)
		}
	}
}
