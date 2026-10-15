package extproc

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const minimal = `{"id":"%s","name":"N","version":"1","protocol":1,"run":{"command":"x.exe"}}`

func TestFindAndLoad(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a", ManifestFile), strings.Replace(minimal, "%s", "x.a", 1))
	writeFile(t, filepath.Join(root, "b", ManifestFile), strings.Replace(minimal, "%s", "x.b", 1))
	writeFile(t, filepath.Join(root, "dup", ManifestFile), strings.Replace(minimal, "%s", "x.a", 1))
	writeFile(t, filepath.Join(root, "reserved", ManifestFile), strings.Replace(minimal, "%s", "builtin.calc", 1))
	writeFile(t, filepath.Join(root, "broken", ManifestFile), `{"id":`)
	writeFile(t, filepath.Join(root, "noid", ManifestFile), `{"name":"x"}`)
	os.MkdirAll(filepath.Join(root, "empty"), 0o755)
	single := t.TempDir()
	writeFile(t, filepath.Join(single, ManifestFile), strings.Replace(minimal, "%s", "x.single", 1))

	dirs := Find(root, single, filepath.Join(root, "missing"))
	if len(dirs) != 7 || dirs[6] != single {
		t.Fatalf("Find = %v", dirs)
	}
	var ids []string
	for _, p := range Load([]string{root, single}, []string{"builtin.calc"}, Options{}, quiet) {
		ids = append(ids, p.Manifest().ID)
	}
	if !slices.Equal(ids, []string{"x.a", "x.b", "x.single"}) {
		t.Fatalf("loaded %v", ids)
	}
}

func TestEnvDirs(t *testing.T) {
	t.Setenv(EnvPluginPath, ` C:\a ;;C:\b;`)
	if got := EnvDirs(); !slices.Equal(got, []string{`C:\a`, `C:\b`}) {
		t.Fatalf("EnvDirs = %q", got)
	}
}

func TestLocalesSkipBadCatalogs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "locales", "en.json"), `{"a":"A"}`)
	writeFile(t, filepath.Join(dir, "locales", "de.json"), `{"a": 1}`)
	fsys := loadLocales(dir, "x", quiet)
	names, _ := fs.Glob(fsys, "*.json")
	if !slices.Equal(names, []string{"en.json"}) {
		t.Fatalf("catalogs = %v", names)
	}
	// Without catalogs an empty source catalog is supplied.
	names, _ = fs.Glob(loadLocales(t.TempDir(), "x", quiet), "*.json")
	if !slices.Equal(names, []string{"en.json"}) {
		t.Fatalf("catalogs = %v", names)
	}
}

func TestResolveCommand(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tool.exe"), "")
	for _, c := range []string{"tool.exe", "tool"} {
		if p, err := resolveCommand(dir, c); err != nil || filepath.Base(p) != "tool.exe" {
			t.Errorf("%s: %q %v", c, p, err)
		}
	}
	for _, c := range []string{"", "missing.exe", `sub\missing.exe`, "no-such-program-on-path"} {
		if _, err := resolveCommand(dir, c); err == nil {
			t.Errorf("%q resolved", c)
		}
	}
}

func TestValidate(t *testing.T) {
	r := Validate(goHelloDir)
	if !r.OK || len(r.Warnings) != 0 || r.ID != "example.go-hello" {
		t.Fatalf("go-hello: %+v", r)
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ManifestFile), `{
		"id": "Bad ID", "version": "", "protocol": 2, "extra": true,
		"run": {"command": "nope.exe"},
		"permissions": ["clipboard", "root"],
		"commands": [{"name": "a", "title": "%cmd.a%", "mode": "inline"}, {"name": "a", "title": "%cmd.b%", "mode": "view"}],
		"preferences": [{"name": "p", "type": "color", "title": "%pref.p%"}]
	}`)
	writeFile(t, filepath.Join(dir, "locales", "en.json"), `{"cmd.a": "A", "cmd.b": "B"}`)
	writeFile(t, filepath.Join(dir, "locales", "fr.json"), `{"cmd.a": "A", "only.fr": "x"}`)
	r = Validate(dir)
	if r.OK {
		t.Fatal("bad plugin validated")
	}
	all := strings.Join(r.Errors, "\n")
	for _, want := range []string{"id \"Bad ID\"", "name is missing", "version is missing", "protocol 2", "run.command \"nope.exe\"",
		"unknown permission \"root\"", "duplicate name \"a\"", "mode must be", "unknown type \"color\"", "%pref.p% is not defined"} {
		if !strings.Contains(all, want) {
			t.Errorf("errors lack %q:\n%s", want, all)
		}
	}
	warns := strings.Join(r.Warnings, "\n")
	for _, want := range []string{"extra", `"only.fr" is not in en.json`, "%cmd.b% is missing"} {
		if !strings.Contains(warns, want) {
			t.Errorf("warnings lack %q:\n%s", want, warns)
		}
	}
	if r := Validate(t.TempDir()); r.OK {
		t.Fatal("empty dir validated")
	}
}
