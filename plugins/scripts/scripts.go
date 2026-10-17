// Package scripts is the builtin.scripts plugin: Raycast-compatible Script
// Commands. Every script with @raycast metadata comments in the configured
// directories becomes a command in the root search.
package scripts

import (
	"context"
	"embed"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/eziq1/golauncher/internal/winapi/shell"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// ID is the plugin ID.
const ID = "builtin.scripts"

const (
	prefDirectories = "directories"
	cmdNew          = "new"
	cmdOpenFolder   = "open-folder"
	callbackRun     = "run"
)

// DefaultDirectory is the default scripts directory; %VAR% is expanded.
// On macOS it is in ~/Library/Application Support, elsewhere on Unix in
// ~/.config.
var DefaultDirectory = func() string {
	switch runtime.GOOS {
	case "windows":
		return `%APPDATA%\golauncher\scripts`
	case "darwin":
		return "%HOME%/Library/Application Support/golauncher/scripts"
	}
	return "%HOME%/.config/golauncher/scripts"
}()

//go:embed locales/*.json
var locales embed.FS

// Options configures the plugin.
type Options struct {
	// Directories is the default of the "directories" preference.
	Directories []string
	// System finds interpreters; zero means RealSystem.
	System System
	// Open opens a file or folder in its default app (used by the "new" and
	// "open-folder" commands); nil means shell.Open.
	Open func(target string, opts plugin.OpenOptions) error
}

// Plugin is the Script Commands plugin.
type Plugin struct {
	opts Options

	h      plugin.Host
	ctx    context.Context
	cancel context.CancelFunc
	unsubs []func()
	wg     sync.WaitGroup

	scanMu sync.Mutex // serializes scans

	mu      sync.Mutex
	closed  bool
	scripts []Script
	stamp   uint64          // signature of the scanned directories
	runs    map[string]*run // compact and silent runs
	runSeq  int
	// lastTask is the background task of each fullOutput script's last run.
	lastTask map[string]string
}

var (
	_ plugin.Plugin         = (*Plugin)(nil)
	_ plugin.RootProvider   = (*Plugin)(nil)
	_ plugin.ActionHandler  = (*Plugin)(nil)
	_ plugin.LocaleProvider = (*Plugin)(nil)
)

// New returns the plugin for the real system, reading scripts from
// %APPDATA%\golauncher\scripts by default.
func New() *Plugin {
	return NewWithOptions(Options{Directories: []string{DefaultDirectory}})
}

// NewWithOptions returns a plugin configured by o.
func NewWithOptions(o Options) *Plugin {
	if o.System.LookPath == nil {
		o.System = RealSystem()
	}
	if o.Open == nil {
		o.Open = shell.Open
	}
	return &Plugin{opts: o, runs: map[string]*run{}, lastTask: map[string]string{}}
}

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	dirs := p.opts.Directories
	if dirs == nil {
		dirs = []string{}
	}
	return plugin.Manifest{
		ID:            ID,
		Name:          "%plugin.name%",
		Description:   "%plugin.description%",
		Version:       "1.0.0",
		Icon:          plugin.Icon{Name: "command"},
		DefaultLocale: "en",
		Permissions:   []plugin.Permission{plugin.PermExec},
		Commands: []plugin.Command{
			{Name: cmdNew, Title: "%command.new.title%", Mode: plugin.ModeNoView, Icon: plugin.Icon{Name: "command"}},
			{Name: cmdOpenFolder, Title: "%command.openFolder.title%", Mode: plugin.ModeNoView, Icon: plugin.Icon{Name: "folder"}},
		},
		Preferences: []plugin.Preference{{
			Name:        prefDirectories,
			Type:        plugin.PrefStringList,
			Title:       "%pref.directories.title%",
			Description: "%pref.directories.description%",
			Default:     jsontext.Value(plugin.MustPayload(dirs)),
		}},
	}
}

// Locales implements plugin.LocaleProvider.
func (p *Plugin) Locales() fs.FS {
	sub, err := fs.Sub(locales, "locales")
	if err != nil {
		panic(err)
	}
	return sub
}

// Init implements plugin.Plugin. It scans the script directories, creating
// the default one if needed.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.mu.Lock()
	if p.closed { // Init after Close starts again (docs/plugin-guide.md "Lifecycle")
		p.closed, p.unsubs, p.runs = false, nil, map[string]*run{}
	}
	p.mu.Unlock()
	p.h = h
	p.ctx, p.cancel = context.WithCancel(context.Background())
	if dirs := p.directories(); len(dirs) > 0 && slices.Contains(p.opts.Directories, DefaultDirectory) &&
		dirs[0] == p.expand(DefaultDirectory) {
		if err := os.MkdirAll(dirs[0], 0o755); err != nil {
			h.Logger().Warn("scripts: creating the scripts directory", "dir", dirs[0], "err", err)
		}
	}
	p.rescan(false)
	rescan := func() { p.spawn(func() { p.rescan(true) }) }
	p.unsubs = append(p.unsubs,
		h.Events().Subscribe(plugin.EventWindowShown, rescan),
		h.Events().Subscribe(plugin.EventConfigChanged, rescan),
	)
	return nil
}

// Close implements plugin.Plugin. It stops running compact and silent
// scripts and waits for background work. fullOutput scripts are tasks of the
// host, which stops them when golauncher quits.
func (p *Plugin) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	runs := make([]*run, 0, len(p.runs))
	for _, r := range p.runs {
		runs = append(runs, r)
	}
	p.mu.Unlock()
	for _, u := range p.unsubs {
		u()
	}
	for _, r := range runs {
		r.stop()
	}
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	return nil
}

// spawn runs fn in the background through Host.Go unless the plugin is
// closed. It reports whether fn was started.
func (p *Plugin) spawn(fn func()) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	p.wg.Add(1)
	p.h.Go(func() {
		defer p.wg.Done()
		fn()
	})
	return true
}

// expand replaces %NAME% with environment variables.
func (p *Plugin) expand(s string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			break
		}
		j := strings.IndexByte(s[i+1:], '%')
		if j < 0 {
			break
		}
		name := s[i+1 : i+1+j]
		if v := p.opts.System.Getenv(name); name != "" && v != "" {
			b.WriteString(s[:i])
			b.WriteString(v)
		} else {
			b.WriteString(s[:i+2+j])
		}
		s = s[i+2+j:]
	}
	b.WriteString(s)
	return b.String()
}

// directories returns the configured script directories, expanded and
// without duplicates.
func (p *Plugin) directories() []string {
	var out []string
	for _, d := range p.h.Preferences().Strings(prefDirectories) {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		d = filepath.Clean(p.expand(d))
		if !slices.ContainsFunc(out, func(x string) bool { return strings.EqualFold(x, d) }) {
			out = append(out, d)
		}
	}
	return out
}

// candidate reports whether a directory entry may be a script command.
// Raycast's "*.template.*" scripts need editing before use and are skipped.
func candidate(name string) bool {
	return !strings.HasPrefix(name, ".") && !strings.Contains(strings.ToLower(name), ".template.") &&
		slices.Contains(extensions, strings.ToLower(filepath.Ext(name)))
}

// signature summarizes the script files of dirs: names, sizes and
// modification times. It changes when a script is added, removed or edited.
func signature(dirs []string) uint64 {
	hash := fnv.New64a()
	for _, d := range dirs {
		fmt.Fprintf(hash, "%s\x00", d)
		entries, err := os.ReadDir(d)
		if err != nil {
			fmt.Fprintf(hash, "!%v\x00", errors.Is(err, fs.ErrNotExist))
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !candidate(e.Name()) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			fmt.Fprintf(hash, "%s|%d|%d\x00", e.Name(), info.Size(), info.ModTime().UnixNano())
		}
	}
	return hash.Sum64()
}

// rescan re-reads the scripts when the directories changed. With notify, a
// change is reported to the host with Invalidate.
func (p *Plugin) rescan(notify bool) {
	p.scanMu.Lock()
	defer p.scanMu.Unlock()
	dirs := p.directories()
	stamp := signature(dirs)
	p.mu.Lock()
	same := stamp == p.stamp && p.scripts != nil
	p.mu.Unlock()
	if same {
		return
	}
	scripts := p.scan(dirs)
	p.mu.Lock()
	p.scripts, p.stamp = scripts, stamp
	p.mu.Unlock()
	if notify {
		p.h.Invalidate("")
	}
}

// scan parses the script commands in dirs.
func (p *Plugin) scan(dirs []string) []Script {
	scripts := []Script{}
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				p.h.Logger().Warn("scripts: reading a directory", "dir", d, "err", err)
			}
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !candidate(e.Name()) {
				continue
			}
			path := filepath.Join(d, e.Name())
			s, ok, err := parseFile(path)
			if err != nil {
				p.h.Logger().Warn("scripts: bad script metadata", "path", path, "err", err)
				continue
			}
			if ok {
				scripts = append(scripts, s)
			}
		}
	}
	return scripts
}

// parseFile reads the metadata of the script at path.
func parseFile(path string) (Script, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return Script{}, false, err
	}
	defer f.Close()
	return ParseScript(path, f)
}

// Scripts returns the scanned script commands.
func (p *Plugin) Scripts() []Script {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.scripts)
}

// RootItems implements plugin.RootProvider: every script command, matched
// by the host. Nothing is shown for an empty query.
func (p *Plugin) RootItems(_ context.Context, q plugin.Query) ([]plugin.Item, error) {
	if strings.TrimSpace(q.Text) == "" {
		return nil, nil
	}
	p.mu.Lock()
	scripts := p.scripts
	p.mu.Unlock()
	t := p.h.I18n()
	group := t.T("group")
	items := make([]plugin.Item, 0, len(scripts))
	for i := range scripts {
		items = append(items, p.item(t, &scripts[i], group))
	}
	return items, nil
}

// runPayload is the Payload of the "run" callback.
type runPayload struct {
	Path string `json:"path"`
}

// item is the root search entry of s.
func (p *Plugin) item(t plugin.Localizer, s *Script, group string) plugin.Item {
	name := filepath.Base(s.Path)
	run := plugin.Action{
		ID: "run", Title: t.T("action.run"),
		Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackRun, Payload: plugin.MustPayload(runPayload{Path: s.Path})},
	}
	if s.NeedsConfirmation {
		run.Style = plugin.StyleDestructive
	}
	actions := []plugin.Action{
		run,
		{
			ID: "reveal", Title: t.T("action.reveal"), Shortcut: "Ctrl+Shift+F",
			Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: s.Path, Payload: plugin.MustPayload(plugin.OpenOptions{Reveal: true})},
		},
		{
			ID: "copy-path", Title: t.T("action.copyPath"), Shortcut: "Ctrl+Shift+C",
			Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: s.Path},
		},
	}
	if s.Mode == modeFullOutput {
		// The output of the last run (or, before one, all tasks).
		p.mu.Lock()
		last := p.lastTask[s.Path]
		p.mu.Unlock()
		actions = append(actions, plugin.Action{
			ID: "show-task", Title: t.T("action.showTask"), Shortcut: "Ctrl+T",
			Do: plugin.ActionDo{Kind: plugin.ActionShowTask, Target: last},
		})
	}
	return plugin.Item{
		ID:        s.Path,
		Title:     s.Title,
		Subtitle:  s.PackageName,
		Icon:      s.icon(),
		Keywords:  []string{strings.TrimSuffix(name, filepath.Ext(name))},
		Group:     group,
		Arguments: s.pluginArguments(),
		Actions:   actions,
	}
}

// HandleAction implements plugin.ActionHandler.
func (p *Plugin) HandleAction(ctx context.Context, a plugin.ActionDo) (plugin.Result, error) {
	switch {
	case a.Kind == plugin.ActionRunCommand && a.Target == cmdNew:
		return p.newScript()
	case a.Kind == plugin.ActionRunCommand && a.Target == cmdOpenFolder:
		return p.openFolder()
	case a.Kind == plugin.ActionCallback && a.Target == callbackRun:
		var rp runPayload
		if err := json.Unmarshal(a.Payload, &rp); err != nil || rp.Path == "" {
			return plugin.Result{}, fmt.Errorf("scripts: bad run payload %q", a.Payload)
		}
		return p.runScript(ctx, rp.Path, a.Values)
	}
	return plugin.Result{}, fmt.Errorf("scripts: unknown action %s %q", a.Kind, a.Target)
}

// failure is a Result with a failure toast.
func failure(title, msg string) plugin.Result {
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastFailure, Title: title, Message: msg}}
}

// firstDirectory returns the first scripts directory, created if missing.
func (p *Plugin) firstDirectory() (string, error) {
	dirs := p.directories()
	if len(dirs) == 0 {
		return "", errors.New(p.h.I18n().T("error.noDirectory"))
	}
	if err := os.MkdirAll(dirs[0], 0o755); err != nil {
		return "", err
	}
	return dirs[0], nil
}

// scriptTemplate is written by the "new" command. The BOM makes Windows
// PowerShell 5.1 read the file as UTF-8.
const scriptTemplate = "\ufeff" + `# Required parameters:
# @raycast.schemaVersion 1
# @raycast.title {{TITLE}}
# @raycast.mode fullOutput

# Optional parameters:
# @raycast.packageName Custom
# @raycast.argument1 { "type": "text", "placeholder": "Name", "optional": true }
# @raycast.needsConfirmation false

# Documentation:
# @raycast.description What this script does
# @raycast.author

param([string]$Name = "World")

Write-Output "Hello, $Name!"
`

// newScript writes a script from the template and opens it.
func (p *Plugin) newScript() (plugin.Result, error) {
	t := p.h.I18n()
	dir, err := p.firstDirectory()
	if err != nil {
		return failure(t.T("toast.newFailed"), err.Error()), nil
	}
	var path string
	for i := 1; ; i++ {
		name := "script-command.ps1"
		if i > 1 {
			name = "script-command-" + strconv.Itoa(i) + ".ps1"
		}
		path = filepath.Join(dir, name)
		content := strings.ReplaceAll(scriptTemplate, "{{TITLE}}", t.T("template.title", "n", i))
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err == nil {
			_, err = f.WriteString(content)
			err = errors.Join(err, f.Close())
		}
		if err != nil {
			return failure(t.T("toast.newFailed"), err.Error()), nil
		}
		break
	}
	p.rescan(true)
	if err := p.opts.Open(path, plugin.OpenOptions{}); err != nil {
		return failure(t.T("toast.openFailed"), err.Error()), nil
	}
	return plugin.Result{Hide: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: t.T("toast.created", "name", filepath.Base(path))}}, nil
}

// openFolder opens the first scripts directory in File Explorer.
func (p *Plugin) openFolder() (plugin.Result, error) {
	t := p.h.I18n()
	dir, err := p.firstDirectory()
	if err == nil {
		err = p.opts.Open(dir, plugin.OpenOptions{})
	}
	if err != nil {
		return failure(t.T("toast.openFailed"), err.Error()), nil
	}
	return plugin.Result{Hide: true}, nil
}
