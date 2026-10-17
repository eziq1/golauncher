package install

import (
	"fmt"
	"os"
	"slices"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestWingetLocationsReads(t *testing.T) {
	_ = WingetLocations() // the real entries: never fails
}

func TestWingetLocations(t *testing.T) {
	integration(t)
	path := fmt.Sprintf(`Software\golauncher-test-winget-%d`, os.Getpid())
	t.Cleanup(func() { _ = registry.DeleteKey(registry.CURRENT_USER, path) })
	for name, values := range map[string][2]string{
		"eziq1.golauncher_Microsoft.Winget.Source_8wekyb3d8bbwe": {"eziq1.golauncher", `D:\Tools\golauncher`},
		"EZIQ1.GOLAUNCHER_custom":                                {"EZIQ1.golauncher", `E:\gl`},
		"eziq1.golauncher_other":                                 {"someone.else", `F:\x`},
		"eziq1.golauncher_noloc":                                 {"eziq1.golauncher", ""},
		"BurntSushi.ripgrep.MSVC_src":                            {"eziq1.golauncher", `G:\rg`},
	} {
		k, _, err := registry.CreateKey(registry.CURRENT_USER, path+`\`+name, registry.SET_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		_ = k.SetStringValue("WinGetPackageIdentifier", values[0])
		_ = k.SetStringValue("InstallLocation", values[1])
		k.Close()
		t.Cleanup(func() { _ = registry.DeleteKey(registry.CURRENT_USER, path+`\`+name) })
	}
	got := wingetLocations(registry.CURRENT_USER, path)
	slices.Sort(got)
	if want := []string{`D:\Tools\golauncher`, `E:\gl`}; !slices.Equal(got, want) {
		t.Fatalf("locations = %q, want %q", got, want)
	}
	if got := wingetLocations(registry.CURRENT_USER, path+`\missing`); got != nil {
		t.Fatalf("missing key: %q", got)
	}
}
