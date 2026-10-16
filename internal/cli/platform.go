package cli

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/eziq1/golauncher/internal/builtin"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/extproc"
	"github.com/eziq1/golauncher/internal/winapi/clip"
	"github.com/eziq1/golauncher/internal/winapi/shell"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/processes"
	"github.com/eziq1/golauncher/plugins/system"
)

// realPlatform holds the real clipboard and system for --real.
type platform struct {
	clipboard plugin.Clipboard
	system    core.System
}

func realPlatform(bool) (platform, error) {
	return platform{clipboard: sysClipboard{}, system: sysSystem{}}, nil
}

// realPlugins returns the real plugins plus the external ones in extra.
// (Without --exec the programs they run as background tasks are only
// recorded: build gives the environment a fake task runner. The system
// commands, such as Shut Down, then only read the real state and record
// what they would do.)
func realPlugins(extra []string, exec bool) []plugin.Plugin {
	builtin.HostVersion = Version
	all := builtin.All()
	if !exec {
		for i, p := range all {
			switch p.(type) {
			case *system.Plugin:
				all[i] = system.NewWithOptions(system.Options{System: system.DryRun()})
			case *processes.Plugin:
				all[i] = processes.NewDryRun() // reads the real processes, ends none
			}
		}
	}
	return append(all, builtin.External(extra, all, extproc.Options{})...)
}

// fakePlugins returns the built-in plugins fed from the fixtures in dir
// (apps.json, files.json; see testdata/fixtures) and the external plugins
// under pluginRoots.
func fakePlugins(dir string, pluginRoots []string) ([]plugin.Plugin, error) {
	return builtin.Fake(dir, pluginRoots...), nil
}

// clipFixture is one entry of clipboard.json: content copied at a time.
type clipFixture struct {
	Text      string   `json:"text,omitempty"`
	Files     []string `json:"files,omitempty"`
	ImagePath string   `json:"image,omitempty"` // PNG relative to the fixtures dir
	SourceApp string   `json:"sourceApp,omitempty"`
	Exclude   bool     `json:"exclude,omitzero"`
	At        string   `json:"at,omitempty"` // RFC 3339; default: the env clock
}

// loadClipboardFixture replays dir/clipboard.json into the fake clipboard,
// publishing a change event after each entry.
func loadClipboardFixture(e *env, dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, "clipboard.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var entries []clipFixture
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("clipboard.json: %w", err)
	}
	clock := e.Services.Now
	defer func() { e.Services.Now = clock }()
	for _, f := range entries {
		c := plugin.ClipboardContent{Text: f.Text, Files: f.Files, SourceApp: f.SourceApp, Exclude: f.Exclude}
		if f.ImagePath != "" {
			if c.Image, err = os.ReadFile(filepath.Join(dir, f.ImagePath)); err != nil {
				return err
			}
		}
		if f.At != "" {
			t, err := time.Parse(time.RFC3339, f.At)
			if err != nil {
				return fmt.Errorf("clipboard.json: %w", err)
			}
			e.Services.Now = func() time.Time { return t }
		}
		e.clip.Set(c)
		e.Services.Bus.PublishSync(plugin.EventClipboardChanged)
	}
	return nil
}

// sysClipboard is the Windows clipboard.
type sysClipboard struct{}

func (sysClipboard) Read() (plugin.ClipboardContent, error) { return clip.Read() }
func (sysClipboard) WriteText(s string) error               { return clip.WriteText(s) }
func (sysClipboard) WriteSecret(s string) error             { return clip.WriteSecret(s) }
func (sysClipboard) WriteImage(png []byte) error            { return clip.WriteImage(png) }
func (sysClipboard) WriteFiles(paths []string) error        { return clip.WriteFiles(paths) }

// sysSystem performs real side effects. There is no launcher window to hide,
// so Paste pastes into the current foreground window (the terminal).
type sysSystem struct{}

func (sysSystem) Open(target string, opts plugin.OpenOptions) error { return shell.Open(target, opts) }
func (sysSystem) CopyText(text string) error                        { return clip.WriteText(text) }
func (sysSystem) CopyFiles(paths []string) error                    { return clip.WriteFiles(paths) }

func (sysSystem) CopyImage(path string) error {
	png, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return clip.WriteImage(png)
}

func (s sysSystem) Paste(text string, content plugin.PasteContent) error {
	var err error
	switch {
	case text != "":
		err = s.CopyText(text)
	case content.ImagePath != "":
		err = s.CopyImage(content.ImagePath)
	default:
		err = s.CopyFiles(content.Files)
	}
	if err != nil {
		return err
	}
	return clip.PasteTo(clip.ForegroundWindow())
}
