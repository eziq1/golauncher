package extproc

import (
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestLoadPlatforms(t *testing.T) {
	root := t.TempDir()
	plat := `{"id":"%s","name":"N","version":"1","protocol":1,"run":{"command":"x.exe"},"platforms":%s}`
	for id, platforms := range map[string]string{
		"x.all": `[]`, "x.win": `["windows"]`, "x.mac": `["darwin"]`, "x.unix": `["darwin","linux"]`, "x.bad": `["macos"]`,
	} {
		writeFile(t, filepath.Join(root, id, ManifestFile), strings.Replace(strings.Replace(plat, "%s", id, 1), "%s", platforms, 1))
	}
	writeFile(t, filepath.Join(root, "x.none", ManifestFile), strings.Replace(minimal, "%s", "x.none", 1))
	for platform, want := range map[string][]string{
		"windows": {"x.all", "x.none", "x.win"},
		"darwin":  {"x.all", "x.mac", "x.none", "x.unix"},
		"linux":   {"x.all", "x.none", "x.unix"},
	} {
		if got := loadIDs(root, Options{Platform: platform}); !slices.Equal(got, want) {
			t.Errorf("%s: loaded %v, want %v", platform, got, want)
		}
	}
	// Without Options.Platform the system's platform counts.
	ids := loadIDs(root, Options{})
	if slices.Contains(ids, "x.win") != (runtime.GOOS == "windows") || slices.Contains(ids, "x.mac") != (runtime.GOOS == "darwin") {
		t.Errorf("default platform %s: loaded %v", runtime.GOOS, ids)
	}
	if _, err := ReadManifest(filepath.Join(root, "x.bad")); err == nil || !strings.Contains(err.Error(), `unknown platform "macos"`) {
		t.Errorf("ReadManifest(x.bad) = %v", err)
	}
}

func loadIDs(root string, o Options) []string {
	var ids []string
	for _, p := range Load([]string{root}, nil, o, quiet) {
		ids = append(ids, p.Manifest().ID)
	}
	slices.Sort(ids)
	return ids
}

func TestValidatePlatforms(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "x.exe"), "")
	writeFile(t, filepath.Join(dir, ManifestFile), `{"id":"x","name":"N","version":"1","protocol":1,"run":{"command":"x.exe"},"platforms":["windows","macos"]}`)
	r := Validate(dir)
	if r.OK || !strings.Contains(strings.Join(r.Errors, "\n"), `platforms: unknown platform "macos" (valid: windows, darwin, linux)`) {
		t.Fatalf("unknown platform: %+v", r)
	}
	other := "darwin"
	if runtime.GOOS == "darwin" {
		other = "windows"
	}
	writeFile(t, filepath.Join(dir, ManifestFile), `{"id":"x","name":"N","version":"1","protocol":1,"run":{"command":"x.exe"},"platforms":["`+other+`"]}`)
	r = Validate(dir)
	if !r.OK || !strings.Contains(strings.Join(r.Warnings, "\n"), "not loaded here") {
		t.Fatalf("other platform: %+v", r)
	}
}
