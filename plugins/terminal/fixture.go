package terminal

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// FixtureOptions returns options that read everything from the fixture
// directory root/terminal instead of the system (glctl --fixtures):
//
//   - LocalAppData/ and ProgramData/: the folders settings.json and the
//     fragments are looked for in, laid out as on Windows;
//   - wsl.json: the WSL registry, a Registry in JSON;
//   - wsl-list-verbose.txt: the output of `wsl --list --verbose` (UTF-16LE,
//     as wsl.exe writes it).
//
// Windows Terminal counts as installed when a settings.json exists. The
// WSL commands run nothing: they succeed without doing anything. With root
// "" or without root/terminal, nothing is installed and the plugin is
// unavailable.
func FixtureOptions(root string) Options {
	dir := ""
	if root != "" {
		if st, err := os.Stat(filepath.Join(root, "terminal")); err == nil && st.IsDir() {
			dir = filepath.Join(root, "terminal")
		}
	}
	sub := func(name string) string {
		if dir == "" {
			return ""
		}
		return filepath.Join(dir, name)
	}
	return Options{
		Getenv:       func(string) string { return "" },
		LocalAppData: sub("LocalAppData"),
		ProgramData:  sub("ProgramData"),
		Which:        func(string) string { return "" },
		Registry: func() (Registry, error) {
			var r Registry
			if dir == "" {
				return r, nil
			}
			data, err := os.ReadFile(filepath.Join(dir, "wsl.json"))
			if errors.Is(err, fs.ErrNotExist) {
				return r, nil
			}
			if err != nil {
				return r, err
			}
			err = json.Unmarshal(data, &r)
			return r, err
		},
		Run: func(_ context.Context, argv ...string) ([]byte, error) {
			if dir != "" && slices.Equal(argv, []string{wslExe, "--list", "--verbose"}) {
				return os.ReadFile(filepath.Join(dir, "wsl-list-verbose.txt"))
			}
			return nil, nil
		},
		Sync: true,
	}
}
