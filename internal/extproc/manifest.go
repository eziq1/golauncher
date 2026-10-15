// Package extproc runs external plugins: programs in any language that live
// in their own process and talk to the launcher over JSON-RPC 2.0 on stdio
// (docs/m5-plugin-platform.md §5, docs/plugin-protocol.md).
//
// Each plugin directory holds a plugin.json manifest and optional
// locales/<lang>.json catalogs. A Proxy implements the same plugin
// interfaces as a built-in plugin, so the engine treats both alike; it
// starts the process lazily, restarts it after crashes with a backoff,
// turns it off when it crashes too often, kills it when it hangs and stops
// it when idle.
package extproc

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing/fstest"

	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// ManifestFile is the name of the manifest in a plugin directory.
const ManifestFile = "plugin.json"

// EnvPluginPath names the environment variable with extra plugin
// directories, separated by ";".
const EnvPluginPath = "GOLAUNCHER_PLUGIN_PATH"

// Manifest is the content of plugin.json: a plugin.Manifest plus the
// protocol version and how to run the plugin.
type Manifest struct {
	plugin.Manifest
	// Protocol is the major protocol version the plugin speaks.
	Protocol int `json:"protocol"`
	Run      Run `json:"run"`
}

// Run tells how to start the plugin process.
type Run struct {
	// Command is a path relative to the plugin directory (e.g. "hello.exe")
	// or a program found on PATH (e.g. "python").
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// ReadManifest reads dir/plugin.json.
func ReadManifest(dir string) (Manifest, error) {
	var m Manifest
	data, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("%s: %w", filepath.Join(dir, ManifestFile), err)
	}
	if m.ID == "" {
		return m, fmt.Errorf("%s: missing id", filepath.Join(dir, ManifestFile))
	}
	return m, nil
}

// UserDir returns %APPDATA%\golauncher\plugins, where users install
// external plugins (one subdirectory each).
func UserDir() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "golauncher", "plugins"), nil
}

// EnvDirs returns the directories listed in GOLAUNCHER_PLUGIN_PATH.
func EnvDirs() []string {
	var out []string
	for d := range strings.SplitSeq(os.Getenv(EnvPluginPath), ";") {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// DefaultRoots returns the user plugin directory followed by EnvDirs.
func DefaultRoots() []string {
	var out []string
	if d, err := UserDir(); err == nil {
		out = append(out, d)
	}
	return append(out, EnvDirs()...)
}

// Find returns the plugin directories under roots. A root that contains a
// plugin.json is itself a plugin directory (handy during development);
// otherwise each of its subdirectories with a plugin.json is one. Missing
// roots are skipped.
func Find(roots ...string) []string {
	var out []string
	for _, root := range roots {
		if isPluginDir(root) {
			out = append(out, root)
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if d := filepath.Join(root, e.Name()); e.IsDir() && isPluginDir(d) {
				out = append(out, d)
			}
		}
	}
	return out
}

func isPluginDir(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, ManifestFile))
	return err == nil && !st.IsDir()
}

// Load creates proxies for the plugins under roots (see Find). Plugins whose
// manifest cannot be read, and plugins whose ID is in reserved or was
// already loaded, are skipped with a warning to log. Other problems make
// the plugin unavailable with a reason the settings page shows.
func Load(roots []string, reserved []string, o Options, log *slog.Logger) []*Proxy {
	if log == nil {
		log = slog.Default()
	}
	seen := map[string]bool{}
	for _, id := range reserved {
		seen[id] = true
	}
	var out []*Proxy
	for _, dir := range Find(roots...) {
		m, err := ReadManifest(dir)
		if err != nil {
			log.Warn("external plugin skipped", "dir", dir, "err", err)
			continue
		}
		if seen[m.ID] {
			log.Warn("external plugin skipped: duplicate id", "dir", dir, "plugin", m.ID)
			continue
		}
		seen[m.ID] = true
		out = append(out, New(dir, m, o, log))
	}
	return out
}

// loadLocales returns the plugin's catalogs from dir/locales, leaving out
// files that do not parse so one bad translation cannot keep the plugin
// (or the launcher) from loading. Without a usable en.json an empty one is
// supplied, as the host requires the source locale.
func loadLocales(dir, id string, log *slog.Logger) fs.FS {
	out := fstest.MapFS{}
	ldir := filepath.Join(dir, "locales")
	entries, _ := os.ReadDir(ldir)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(ldir, e.Name()))
		if err == nil {
			err = checkCatalog(data)
		}
		if err != nil {
			log.Warn("external plugin catalog ignored", "plugin", id, "file", e.Name(), "err", err)
			continue
		}
		out[e.Name()] = &fstest.MapFile{Data: data}
	}
	if _, ok := out[i18n.Source+".json"]; !ok {
		out[i18n.Source+".json"] = &fstest.MapFile{Data: []byte("{}")}
	}
	return out
}

// checkCatalog reports whether data is a valid message catalog.
func checkCatalog(data []byte) error {
	return i18n.NewManager(i18n.Source, nil).Register("check", fstest.MapFS{i18n.Source + ".json": {Data: data}})
}

// errNotFound reports a run.command that cannot be found.
var errNotFound = errors.New("not found")

// resolveCommand finds the program of run.command: a file in the plugin
// directory (with or without ".exe") or a program on PATH.
func resolveCommand(dir, command string) (string, error) {
	if command == "" {
		return "", fmt.Errorf("run.command is empty: %w", errNotFound)
	}
	cands := []string{command}
	if !filepath.IsAbs(command) {
		cands = []string{filepath.Join(dir, command)}
	}
	if filepath.Ext(command) == "" {
		cands = append(cands, cands[0]+".exe")
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return filepath.Abs(c)
		}
	}
	if filepath.IsAbs(command) || strings.ContainsAny(command, `/\`) {
		return "", fmt.Errorf("%s: %w", command, errNotFound)
	}
	p, err := lookPath(command)
	if err != nil {
		return "", fmt.Errorf("%s: %w", command, errNotFound)
	}
	return p, nil
}

// manifestKeys returns the "%key%" references in the manifest, sorted.
func manifestKeys(data []byte) ([]string, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	var keys []string
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			if k, ok := i18n.ManifestKey(v); ok {
				keys = append(keys, k)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		case map[string]any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(v)
	slices.Sort(keys)
	return slices.Compact(keys), nil
}
