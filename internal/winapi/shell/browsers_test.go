package shell

import (
	"runtime"
	"testing"
)

func TestCommandProgram(t *testing.T) {
	for cmd, want := range map[string]string{
		`"C:\Program Files\Mozilla Firefox\firefox.exe" -osint -url "%1"`: `C:\Program Files\Mozilla Firefox\firefox.exe`,
		`"C:\Apps\b.exe"`: `C:\Apps\b.exe`,
		`C:\Program Files\App\app.exe --single-argument %1`: `C:\Program Files\App\app.exe`,
		`browser %1`: `browser`,
		``:           ``,
	} {
		if got := commandProgram(cmd); got != want {
			t.Errorf("commandProgram(%q) = %q, want %q", cmd, got, want)
		}
	}
}

// TestBrowsers only checks that reading the registry works; which browsers
// are installed depends on the machine.
func TestBrowsers(t *testing.T) {
	bs := Browsers()
	if runtime.GOOS != "windows" && bs != nil {
		t.Fatal("browsers outside Windows")
	}
	for _, b := range bs {
		if b.Name == "" || b.Path == "" {
			t.Errorf("incomplete browser %+v", b)
		}
	}
}
