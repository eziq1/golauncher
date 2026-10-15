// Package files is the builtin.files plugin: actions for the folder the user
// is working in when the launcher opens (docs/context-awareness.md §4). It
// is a plugin.ContextProvider: it shows nothing until the host detected a
// folder (File Explorer, the desktop, a path in a window title) and then
// offers to open a terminal or VS Code there, create a folder or a text
// file, copy the path, and, for selected files, to copy their paths, open
// them in VS Code, compress them to a ZIP file, hash a file or run a
// program as administrator.
package files

import (
	"context"
	"embed"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// ID is the plugin ID.
const ID = "builtin.files"

// Callbacks.
const (
	cbNewFolder = "newFolder"
	cbNewFile   = "newFile"
	cbZip       = "zip"
	cbSHA256    = "sha256"
)

// argName is the argument of New Folder and New Text File.
const argName = "name"

//go:embed locales/*.json
var locales embed.FS

// Options configure the plugin.
type Options struct {
	// Which finds a program: "wt", "pwsh", "powershell" and "code" (VS Code;
	// its Code.exe when found). It returns "" when the program is missing.
	// The default looks at PATH and the app execution aliases.
	Which func(name string) string
}

// Plugin is the files plugin.
type Plugin struct {
	which func(string) string
	h     plugin.Host
	wg    sync.WaitGroup // background work (Host.Go)

	mu    sync.Mutex
	found map[string]string // Which results, looked up once
}

var (
	_ plugin.Plugin          = (*Plugin)(nil)
	_ plugin.ContextProvider = (*Plugin)(nil)
	_ plugin.ActionHandler   = (*Plugin)(nil)
	_ plugin.LocaleProvider  = (*Plugin)(nil)
)

// New returns the plugin for this computer.
func New() *Plugin { return NewWithOptions(Options{}) }

// NewWithOptions returns the plugin configured by o.
func NewWithOptions(o Options) *Plugin {
	which := o.Which
	if which == nil {
		which = systemWhich
	}
	return &Plugin{which: which, found: map[string]string{}}
}

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:            ID,
		Name:          "%plugin.name%",
		Description:   "%plugin.description%",
		Version:       "1.0.0",
		Icon:          plugin.Icon{Name: "folder"},
		DefaultLocale: "en",
		Permissions:   []plugin.Permission{plugin.PermClipboard, plugin.PermExec},
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

// Init implements plugin.Plugin.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.h = h
	return nil
}

// Close implements plugin.Plugin: it waits for compressions and hashes.
func (p *Plugin) Close() error {
	p.wg.Wait()
	return nil
}

// program returns where program name is, "" if missing; looked up once.
func (p *Plugin) program(name string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := p.found[name]; ok {
		return v
	}
	v := p.which(name)
	p.found[name] = v
	return v
}

// systemWhich finds programs on this computer.
func systemWhich(name string) string {
	switch name {
	case "wt":
		if p, err := exec.LookPath("wt.exe"); err == nil {
			return p
		}
		// Windows Terminal is an app execution alias, which LookPath may
		// not resolve.
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			p := filepath.Join(la, "Microsoft", "WindowsApps", "wt.exe")
			if _, err := os.Lstat(p); err == nil {
				return p
			}
		}
		return ""
	case "code":
		p, err := exec.LookPath("code")
		if err != nil {
			return ""
		}
		// PATH has bin\code.cmd; Code.exe next to bin\ opens without a
		// console window.
		if exe := filepath.Join(filepath.Dir(filepath.Dir(p)), "Code.exe"); strings.EqualFold(filepath.Base(filepath.Dir(p)), "bin") {
			if _, err := os.Stat(exe); err == nil {
				return exe
			}
		}
		return p
	default:
		p, err := exec.LookPath(name + ".exe")
		if err != nil {
			return ""
		}
		return p
	}
}

// applies reports whether the plugin offers anything for c.
func applies(c plugin.Context) bool {
	switch c.Kind {
	case plugin.ContextExplorer, plugin.ContextDesktop, plugin.ContextPathInTitle, plugin.ContextFileDialog:
		return c.Folder != "" || len(c.Selected) > 0
	}
	return false
}

// ContextItems implements plugin.ContextProvider: the items for the
// selected files first, then those for the folder.
func (p *Plugin) ContextItems(_ context.Context, c plugin.Context) ([]plugin.Item, error) {
	if !applies(c) {
		return nil, nil
	}
	l := p.h.I18n()
	var items []plugin.Item
	if len(c.Selected) > 0 {
		items = append(items, p.selectionItems(l, c)...)
	}
	if c.Folder != "" {
		if st, err := os.Stat(c.Folder); err == nil && st.IsDir() {
			items = append(items, p.folderItems(l, c.Folder)...)
		}
	}
	return items, nil
}

// keywords splits a comma-separated message into search keywords.
func keywords(l plugin.Localizer, id string) []string {
	var out []string
	for w := range strings.SplitSeq(l.T(id), ",") {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, w)
		}
	}
	return out
}

func openAction(id, title, target string, opts plugin.OpenOptions) plugin.Action {
	a := plugin.Action{ID: id, Title: title, Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: target}}
	if opts != (plugin.OpenOptions{}) {
		a.Do.Payload = plugin.MustPayload(opts)
	}
	return a
}

// payload is the data of the callbacks.
type payload struct {
	Dir   string   `json:"dir,omitempty"`
	Paths []string `json:"paths,omitempty"`
}

func callback(id, title, target string, pl payload) plugin.Action {
	return plugin.Action{ID: id, Title: title, Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: target, Payload: plugin.MustPayload(pl)}}
}

// folderItems are the actions for the current folder dir.
func (p *Plugin) folderItems(l plugin.Localizer, dir string) []plugin.Item {
	var items []plugin.Item
	copyPath := plugin.Action{ID: "copy-path", Title: l.T("action.copyPath"), Shortcut: "Ctrl+Shift+C",
		Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: dir}}

	// A terminal: Windows Terminal when installed, else PowerShell.
	term := p.terminal(l, dir, false)
	if term.target != "" {
		items = append(items, plugin.Item{
			ID: "terminal", Title: term.title, Subtitle: dir, Icon: plugin.Icon{Name: "terminal"},
			Keywords: keywords(l, "keywords.terminal"),
			Actions:  []plugin.Action{openAction("open", l.T("action.open"), term.target, term.opts), copyPath},
		})
	}
	items = append(items, plugin.Item{
		ID: "cmd", Title: l.T("item.cmd.title"), Subtitle: dir, Icon: plugin.Icon{Name: "terminal"},
		Keywords: keywords(l, "keywords.cmd"),
		Actions:  []plugin.Action{openAction("open", l.T("action.open"), "cmd.exe", plugin.OpenOptions{Dir: dir}), copyPath},
	})
	if code := p.program("code"); code != "" {
		items = append(items, plugin.Item{
			ID: "vscode", Title: l.T("item.vscode.title"), Subtitle: dir, Icon: plugin.Icon{Name: "code"},
			Keywords: keywords(l, "keywords.vscode"),
			Actions:  []plugin.Action{openAction("open", l.T("action.open"), code, plugin.OpenOptions{Args: quoteArgs(dir), Dir: dir}), copyPath},
		})
	}
	items = append(items,
		plugin.Item{
			ID: "new-folder", Title: l.T("item.newFolder.title"), Subtitle: dir, Icon: plugin.Icon{Name: "folder-plus"},
			Keywords:  keywords(l, "keywords.newFolder"),
			Arguments: []plugin.Argument{{Name: argName, Type: plugin.ArgText, Placeholder: l.T("default.folderName")}},
			Actions:   []plugin.Action{callback("create", l.T("action.create"), cbNewFolder, payload{Dir: dir})},
		},
		plugin.Item{
			ID: "new-file", Title: l.T("item.newFile.title"), Subtitle: dir, Icon: plugin.Icon{Name: "file-plus"},
			Keywords:  keywords(l, "keywords.newFile"),
			Arguments: []plugin.Argument{{Name: argName, Type: plugin.ArgText, Placeholder: l.T("default.fileName")}},
			Actions:   []plugin.Action{callback("create", l.T("action.create"), cbNewFile, payload{Dir: dir})},
		},
		plugin.Item{
			ID: "copy-path", Title: l.T("item.copyPath.title"), Subtitle: dir, Icon: plugin.Icon{Name: "clipboard"},
			Keywords: keywords(l, "keywords.copyPath"),
			Actions:  []plugin.Action{{ID: "copy", Title: l.T("action.copy"), Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: dir}}},
		},
	)
	if admin := p.terminal(l, dir, true); admin.target != "" {
		items = append(items, plugin.Item{
			ID: "terminal-admin", Title: admin.title, Subtitle: dir, Icon: plugin.Icon{Name: "shield"},
			Keywords: slices.Concat(keywords(l, "keywords.terminal"), keywords(l, "keywords.admin")),
			Actions: []plugin.Action{{ID: "open", Title: l.T("action.openAdmin"), Style: plugin.StyleDestructive,
				Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: admin.target, Payload: plugin.MustPayload(admin.opts)}}},
		})
	}
	return items
}

// terminalLaunch is how to open a terminal in a folder.
type terminalLaunch struct {
	title  string // the item title
	target string // "" when no terminal was found
	opts   plugin.OpenOptions
}

// terminal returns how to open a terminal in dir: Windows Terminal (wt -d
// dir) when installed, else PowerShell 7 or Windows PowerShell started in
// dir. An elevated process does not keep the working directory, so as
// administrator PowerShell changes to dir itself.
func (p *Plugin) terminal(l plugin.Localizer, dir string, admin bool) terminalLaunch {
	verb := ""
	title := l.T("item.terminal.title")
	if admin {
		verb, title = "runas", l.T("item.terminalAdmin.title")
	}
	if wt := p.program("wt"); wt != "" {
		return terminalLaunch{title: title, target: wt, opts: plugin.OpenOptions{Args: "-d " + quoteArg(dir), Dir: dir, Verb: verb}}
	}
	ps := p.program("pwsh")
	if ps == "" {
		ps = p.program("powershell")
	}
	if ps == "" {
		return terminalLaunch{}
	}
	if admin {
		cd := "Set-Location -LiteralPath '" + strings.ReplaceAll(dir, "'", "''") + "'"
		return terminalLaunch{title: l.T("item.powershellAdmin.title"), target: ps,
			opts: plugin.OpenOptions{Args: "-NoExit -Command " + quoteArg(cd), Dir: dir, Verb: verb}}
	}
	return terminalLaunch{title: l.T("item.powershell.title"), target: ps, opts: plugin.OpenOptions{Dir: dir}}
}

// selectionItems are the actions for the selected files and folders.
func (p *Plugin) selectionItems(l plugin.Localizer, c plugin.Context) []plugin.Item {
	sel := c.Selected
	n := len(sel)
	subtitle := l.N("subtitle.selected", n, "name", filepath.Base(sel[0]))
	dir := c.Folder
	if dir == "" {
		dir = filepath.Dir(sel[0])
	}
	items := []plugin.Item{{
		ID: "copy-selected", Title: l.N("item.copySelected.title", n), Subtitle: subtitle, Icon: plugin.Icon{Name: "clipboard"},
		Keywords: keywords(l, "keywords.copyPath"),
		Actions: []plugin.Action{
			{ID: "copy", Title: l.T("action.copy"), Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: strings.Join(sel, "\r\n")}},
			{ID: "copy-files", Title: l.T("action.copyFiles"), Shortcut: "Ctrl+Alt+C",
				Do: plugin.ActionDo{Kind: plugin.ActionCopyFiles, Payload: plugin.MustPayload(sel)}},
		},
	}}
	if code := p.program("code"); code != "" {
		items = append(items, plugin.Item{
			ID: "vscode-selected", Title: l.T("item.vscodeSelected.title"), Subtitle: subtitle, Icon: plugin.Icon{Name: "code"},
			Keywords: keywords(l, "keywords.vscode"),
			Actions:  []plugin.Action{openAction("open", l.T("action.open"), code, plugin.OpenOptions{Args: quoteArgs(sel...), Dir: dir})},
		})
	}
	items = append(items, plugin.Item{
		ID: "zip", Title: l.T("item.zip.title"), Subtitle: subtitle, Icon: plugin.Icon{Name: "archive"},
		Keywords: keywords(l, "keywords.zip"),
		Actions:  []plugin.Action{callback("compress", l.T("action.compress"), cbZip, payload{Dir: dir, Paths: sel})},
	})
	if n == 1 {
		if st, err := os.Stat(sel[0]); err == nil && st.Mode().IsRegular() {
			items = append(items, plugin.Item{
				ID: "sha256", Title: l.T("item.sha256.title"), Subtitle: subtitle, Icon: plugin.Icon{Name: "hash"},
				Keywords: keywords(l, "keywords.sha256"),
				Actions:  []plugin.Action{callback("hash", l.T("action.copy"), cbSHA256, payload{Paths: sel})},
			})
			if runnable(sel[0]) {
				items = append(items, plugin.Item{
					ID: "run-admin", Title: l.T("item.runAdmin.title"), Subtitle: subtitle, Icon: plugin.Icon{Name: "shield"},
					Keywords: keywords(l, "keywords.admin"),
					Actions: []plugin.Action{openAction("run", l.T("action.runAdmin"), sel[0],
						plugin.OpenOptions{Dir: filepath.Dir(sel[0]), Verb: "runas"})},
				})
			}
		}
	}
	return items
}

// runnable reports whether path is a program that can run as administrator.
func runnable(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".exe", ".bat", ".cmd", ".msi":
		return true
	}
	return false
}

// HandleAction implements plugin.ActionHandler for the callbacks.
func (p *Plugin) HandleAction(ctx context.Context, a plugin.ActionDo) (plugin.Result, error) {
	if a.Kind != plugin.ActionCallback {
		return plugin.Result{}, fmt.Errorf("files: unknown action %s %q", a.Kind, a.Target)
	}
	var pl payload
	if err := json.Unmarshal(a.Payload, &pl); err != nil {
		return plugin.Result{}, fmt.Errorf("files: %w", err)
	}
	l := p.h.I18n()
	switch a.Target {
	case cbNewFolder, cbNewFile:
		name := argument(a, argName)
		folder := a.Target == cbNewFolder
		if name == "" {
			name = l.T("default.fileName")
			if folder {
				name = l.T("default.folderName")
			}
		}
		if !folder && filepath.Ext(name) == "" {
			name += ".txt"
		}
		if !validName(name) {
			return failure(l.T("toast.invalidName", "name", name), ""), nil
		}
		path, err := create(pl.Dir, name, folder)
		if err != nil {
			return failure(l.T("toast.createFailed", "name", name), err.Error()), nil
		}
		return plugin.Result{Hide: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("toast.created", "name", filepath.Base(path))}}, nil
	case cbZip:
		if len(pl.Paths) == 0 {
			return plugin.Result{}, errors.New("files: nothing to compress")
		}
		return p.background(ctx, func() plugin.Result {
			out, err := zipPaths(pl.Dir, pl.Paths)
			if err != nil {
				return failure(l.T("toast.zipFailed"), err.Error())
			}
			return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("toast.created", "name", filepath.Base(out))}}
		}), nil
	case cbSHA256:
		if len(pl.Paths) != 1 {
			return plugin.Result{}, errors.New("files: hash one file")
		}
		return p.background(ctx, func() plugin.Result {
			sum, err := sha256File(pl.Paths[0])
			if err == nil {
				err = p.h.Clipboard().WriteText(sum)
			}
			if err != nil {
				return failure(l.T("toast.hashFailed"), err.Error())
			}
			return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("toast.hashCopied"), Message: sum}}
		}), nil
	}
	return plugin.Result{}, fmt.Errorf("files: unknown callback %q", a.Target)
}

// background runs work through Host.Go, so a panic cannot take the launcher
// down, and waits for it while the host waits for the action. If the host
// gives up first, the work goes on and its result is dropped.
//
// TODO(R24): report through a background task instead.
func (p *Plugin) background(ctx context.Context, work func() plugin.Result) plugin.Result {
	done := make(chan plugin.Result, 1)
	p.wg.Add(1)
	p.h.Go(func() {
		defer p.wg.Done()
		done <- work()
	})
	select {
	case r := <-done:
		return r
	case <-ctx.Done():
		l := p.h.I18n()
		return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: l.T("toast.stillWorking")}}
	}
}

func failure(title, msg string) plugin.Result {
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastFailure, Title: title, Message: msg}}
}

// argument returns the trimmed string value of argument name.
func argument(a plugin.ActionDo, name string) string {
	v, ok := a.Values[name]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}
