package terminal

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestListVerbose parses `wsl --list --verbose` as wsl.exe writes it:
// UTF-16LE, with English or Chinese headers and states.
func TestListVerbose(t *testing.T) {
	tests := []struct {
		file string
		want map[string]State
		kind map[string]stateKind
	}{
		{"wsl-list-en.txt", map[string]State{
			"Ubuntu-22.04": {Text: "Running", Version: 2, Default: true},
			"Debian":       {Text: "Stopped", Version: 2},
		}, map[string]stateKind{"Ubuntu-22.04": stateRunning, "Debian": stateStopped}},
		{"wsl-list-zh.txt", map[string]State{
			"Ubuntu-22.04":   {Text: "正在运行", Version: 2, Default: true},
			"Debian":         {Text: "已停止", Version: 2},
			"docker-desktop": {Text: "正在转换", Version: 2},
		}, map[string]stateKind{"Ubuntu-22.04": stateRunning, "Debian": stateStopped, "docker-desktop": stateUnknown}},
		{"wsl-list-none-zh.txt", map[string]State{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got := ParseListVerbose(DecodeOutput(readFixture(t, tt.file)))
			if !maps.Equal(got, tt.want) {
				t.Errorf("got %+v\nwant %+v", got, tt.want)
			}
			for name, k := range tt.kind {
				if got[name].kind() != k {
					t.Errorf("%s: kind %d, want %d", name, got[name].kind(), k)
				}
			}
		})
	}
}

func TestDecodeOutput(t *testing.T) {
	en := readFixture(t, "wsl-list-en.txt")
	want := DecodeOutput(en)
	if want[:6] != "  NAME" {
		t.Fatalf("decoded %q", want)
	}
	// With a byte order mark.
	if got := DecodeOutput(append([]byte{0xff, 0xfe}, en...)); got != want {
		t.Errorf("BOM: %q", got)
	}
	// WSL_UTF8=1 makes wsl.exe write UTF-8.
	if got := DecodeOutput([]byte(want)); got != want {
		t.Errorf("UTF-8: %q", got)
	}
	if got := DecodeOutput([]byte("\xef\xbb\xbf名称")); got != "名称" {
		t.Errorf("UTF-8 BOM: %q", got)
	}
	if got := DecodeOutput(nil); got != "" {
		t.Errorf("empty: %q", got)
	}
}

func TestRegistryDefault(t *testing.T) {
	r := Registry{Default: "{ABC}", Distros: []Distro{{GUID: "{def}", Name: "A"}, {GUID: "{abc}", Name: "B"}}}
	if r.DefaultName() != "B" {
		t.Errorf("default = %q", r.DefaultName())
	}
	if (Registry{Distros: r.Distros}).DefaultName() != "" {
		t.Error("a default without DefaultDistribution")
	}
}
