// Package apps is the builtin.apps plugin: it lists the applications of the
// Windows Start menu in the root search and launches them.
package apps

import (
	"cmp"
	"context"
	"crypto/sha1"
	"embed"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/winapi/shell"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// ID is the plugin ID.
const ID = "builtin.apps"

const (
	iconSize     = 64
	staleAfter   = 60 * time.Second
	keyIndex     = "index"
	keyHidden    = "hidden"
	prefHidden   = "hiddenApps"
	cmdReload    = "reload"
	callbackHide = "hide"
)

//go:embed locales/*.json
var locales embed.FS

// Source lists the installed applications.
type Source func(ctx context.Context) ([]shell.App, error)

// IconFunc draws the icon of an app as a size×size PNG.
type IconFunc func(app shell.App, size int) ([]byte, error)

// FixtureSource returns a Source reading a JSON array of shell.App from path.
func FixtureSource(path string) Source {
	return func(context.Context) ([]shell.App, error) {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var apps []shell.App
		if err := json.Unmarshal(b, &apps); err != nil {
			return nil, fmt.Errorf("apps: fixture %s: %w", path, err)
		}
		return apps, nil
	}
}

// Plugin is the applications plugin.
type Plugin struct {
	src   Source
	icons IconFunc

	h       plugin.Host
	iconDir string
	ctx     context.Context
	cancel  context.CancelFunc
	unsub   func()
	wg      sync.WaitGroup

	refreshMu sync.Mutex // serializes refreshes
	iconMu    sync.Mutex // serializes icon generation

	mu        sync.Mutex
	idle      *sync.Cond // signaled when jobs drops to 0
	jobs      int
	closed    bool
	apps      []shell.App
	updated   time.Time
	hidden    []string // hidden by the "hide" action
	iconReady map[string]bool
}

var (
	_ plugin.Plugin         = (*Plugin)(nil)
	_ plugin.RootProvider   = (*Plugin)(nil)
	_ plugin.ActionHandler  = (*Plugin)(nil)
	_ plugin.LocaleProvider = (*Plugin)(nil)
)

// New returns the plugin listing the Start menu apps of this machine.
func New() *Plugin { return NewWithSource(shell.InstalledApps, shell.IconPNG) }

// NewWithSource returns the plugin listing the apps of src. icons may be nil,
// in which case no icon files are generated.
func NewWithSource(src Source, icons IconFunc) *Plugin {
	p := &Plugin{src: src, icons: icons, iconReady: map[string]bool{}}
	p.idle = sync.NewCond(&p.mu)
	return p
}

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:            ID,
		Name:          "%plugin.name%",
		Description:   "%plugin.description%",
		Version:       "1.0.0",
		Icon:          plugin.Icon{Name: "app"},
		DefaultLocale: "en",
		Permissions:   []plugin.Permission{plugin.PermExec, plugin.PermStorage},
		Commands: []plugin.Command{{
			Name:  cmdReload,
			Title: "%command.reload.title%",
			Mode:  plugin.ModeNoView,
			Icon:  plugin.Icon{Name: "app"},
		}},
		Preferences: []plugin.Preference{{
			Name:        prefHidden,
			Type:        plugin.PrefStringList,
			Title:       "%pref.hiddenApps.title%",
			Description: "%pref.hiddenApps.description%",
			Default:     jsontext.Value("[]"),
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

// index is the cached app list.
type index struct {
	Updated time.Time   `json:"updated"`
	Apps    []shell.App `json:"apps"`
}

// Init implements plugin.Plugin. It loads the cached index, so results are
// available at once, and refreshes it in the background.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.h = h
	p.iconDir = filepath.Join(h.CacheDir(), "icons")
	p.ctx, p.cancel = context.WithCancel(context.Background())

	if b, ok, err := h.Storage().Get(keyIndex); err != nil {
		h.Logger().Warn("apps: reading the cached index", "err", err)
	} else if ok {
		var idx index
		if err := json.Unmarshal(b, &idx); err != nil {
			h.Logger().Warn("apps: bad cached index", "err", err)
		} else {
			p.apps, p.updated = idx.Apps, idx.Updated
		}
	}
	if b, ok, err := h.Storage().Get(keyHidden); err == nil && ok {
		_ = json.Unmarshal(b, &p.hidden)
	}
	for _, a := range p.apps {
		if _, err := os.Stat(p.iconPath(a)); err == nil {
			p.iconReady[a.ID] = true
		}
	}

	p.unsub = h.Events().Subscribe(plugin.EventWindowShown, p.onWindowShown)
	p.spawn(func(ctx context.Context) { p.backgroundRefresh(ctx, true) })
	return nil
}

// Close implements plugin.Plugin. It stops background work and waits for it.
func (p *Plugin) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	if p.unsub != nil {
		p.unsub()
	}
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	return nil
}

// spawn runs fn in the background unless the plugin is closed.
func (p *Plugin) spawn(fn func(ctx context.Context)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.jobs++
	p.wg.Add(1)
	p.h.Go(func() {
		defer p.wg.Done()
		defer func() {
			p.mu.Lock()
			if p.jobs--; p.jobs == 0 {
				p.idle.Broadcast()
			}
			p.mu.Unlock()
		}()
		fn(p.ctx)
	})
}

// waitIdle waits until no background work is running.
func (p *Plugin) waitIdle() {
	p.mu.Lock()
	for p.jobs > 0 {
		p.idle.Wait()
	}
	p.mu.Unlock()
}

func (p *Plugin) onWindowShown() {
	p.mu.Lock()
	stale := p.h.Now().Sub(p.updated) > staleAfter
	p.mu.Unlock()
	if stale {
		p.spawn(func(ctx context.Context) { p.backgroundRefresh(ctx, false) })
	}
}

// backgroundRefresh refreshes the index unless a refresh is running.
func (p *Plugin) backgroundRefresh(ctx context.Context, force bool) {
	if !p.refreshMu.TryLock() {
		return
	}
	defer p.refreshMu.Unlock()
	if !force {
		p.mu.Lock()
		stale := p.h.Now().Sub(p.updated) > staleAfter
		p.mu.Unlock()
		if !stale {
			return
		}
	}
	if _, err := p.refreshLocked(ctx); err != nil && ctx.Err() == nil {
		p.h.Logger().Warn("apps: refreshing the index", "err", err)
	}
}

// refresh rebuilds the index, waiting for a running refresh first.
func (p *Plugin) refresh(ctx context.Context) (int, error) {
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	return p.refreshLocked(ctx)
}

func (p *Plugin) refreshLocked(ctx context.Context) (int, error) {
	apps, err := p.src(ctx)
	if err != nil {
		return 0, err
	}
	now := p.h.Now()
	p.mu.Lock()
	p.apps, p.updated = apps, now
	p.mu.Unlock()
	if b, err := json.Marshal(index{Updated: now, Apps: apps}); err == nil {
		if err := p.h.Storage().Set(keyIndex, b); err != nil {
			p.h.Logger().Warn("apps: saving the index", "err", err)
		}
	}
	p.h.Invalidate("") // the root results shown may have changed
	if p.icons != nil {
		p.spawn(p.generateIcons)
	}
	return len(apps), nil
}

// iconPath is the cached icon file of a.
func (p *Plugin) iconPath(a shell.App) string {
	sum := sha1.Sum([]byte(a.ID))
	return filepath.Join(p.iconDir, hex.EncodeToString(sum[:])+".png")
}

// generateIcons draws the missing icons of the indexed apps.
func (p *Plugin) generateIcons(ctx context.Context) {
	p.iconMu.Lock()
	defer p.iconMu.Unlock()
	p.mu.Lock()
	var todo []shell.App
	for _, a := range p.apps {
		if !p.iconReady[a.ID] {
			todo = append(todo, a)
		}
	}
	p.mu.Unlock()
	if len(todo) == 0 {
		return
	}
	if err := os.MkdirAll(p.iconDir, 0o755); err != nil {
		p.h.Logger().Warn("apps: creating the icon cache", "err", err)
		return
	}
	added := false
	defer func() {
		if added {
			p.h.Invalidate("") // show the new icons
		}
	}()
	for _, a := range todo {
		if ctx.Err() != nil {
			return
		}
		path := p.iconPath(a)
		if _, err := os.Stat(path); err != nil {
			b, err := p.icons(a, iconSize)
			if err != nil {
				p.h.Logger().Debug("apps: icon", "app", a.Name, "err", err)
				continue
			}
			if err := writeFileAtomic(path, b); err != nil {
				p.h.Logger().Warn("apps: saving an icon", "app", a.Name, "err", err)
				continue
			}
		}
		p.mu.Lock()
		p.iconReady[a.ID] = true
		p.mu.Unlock()
		added = true
	}
}

func writeFileAtomic(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".icon-*")
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	err = errors.Join(err, f.Close())
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		_ = os.Remove(f.Name())
	}
	return err
}

// hiddenSet is the IDs hidden by preference or by the "hide" action.
// p.mu must be held.
func (p *Plugin) hiddenSet() map[string]bool {
	set := map[string]bool{}
	for _, id := range p.hidden {
		set[id] = true
	}
	for _, id := range p.h.Preferences().Strings(prefHidden) {
		set[id] = true
	}
	return set
}

// RootItems implements plugin.RootProvider. It returns every visible app and
// lets the host match them; it never waits for a refresh.
func (p *Plugin) RootItems(_ context.Context, q plugin.Query) ([]plugin.Item, error) {
	if strings.TrimSpace(q.Text) == "" {
		return nil, nil
	}
	p.mu.Lock()
	apps := p.apps
	hidden := p.hiddenSet()
	ready := maps.Clone(p.iconReady)
	p.mu.Unlock()

	t := p.h.I18n()
	accessory, group := t.T("accessory"), t.T("group")
	items := make([]plugin.Item, 0, len(apps))
	for _, a := range apps {
		if hidden[a.ID] {
			continue
		}
		it := plugin.Item{
			ID:        a.ID,
			Title:     a.Name,
			Icon:      plugin.Icon{Name: "app"},
			Accessory: accessory,
			Group:     group,
			Actions:   p.actions(t, a),
		}
		if ready[a.ID] {
			it.Icon = plugin.Icon{Path: p.iconPath(a)}
		}
		if a.Path != "" {
			it.Keywords = []string{strings.TrimSuffix(filepath.Base(a.Path), filepath.Ext(a.Path))}
		}
		items = append(items, it)
	}
	return items, nil
}

// actions returns the actions of a; the first is the primary one.
func (p *Plugin) actions(t plugin.Localizer, a shell.App) []plugin.Action {
	// Launch as the Start menu does: through the shortcut, which carries the
	// working directory, arguments and AppUserModelID, else through the
	// Applications folder item; the target executable is the last resort.
	open := plugin.ActionDo{Kind: plugin.ActionOpen}
	switch {
	case a.Packaged || a.LinkPath == "" && a.ID != "":
		open.Target = a.ShellPath()
	case a.LinkPath != "":
		open.Target = a.LinkPath
	default:
		open.Target = a.Path
		if a.Args != "" {
			open.Payload = plugin.MustPayload(plugin.OpenOptions{Args: a.Args})
		}
	}
	acts := []plugin.Action{{ID: "open", Title: t.T("action.open"), Do: open}}
	if !a.Packaged && (a.LinkPath != "" || a.Path != "") {
		opts := plugin.OpenOptions{Verb: "runas"}
		target := a.LinkPath
		if target == "" {
			target, opts.Args = a.Path, a.Args
		}
		acts = append(acts, plugin.Action{
			ID: "runas", Title: t.T("action.runAsAdmin"), Shortcut: "Ctrl+Shift+Enter",
			Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: target, Payload: plugin.MustPayload(opts)},
		})
	}
	if file := cmp.Or(a.Path, a.LinkPath); !a.Packaged && file != "" {
		acts = append(acts, plugin.Action{
			ID: "reveal", Title: t.T("action.reveal"), Shortcut: "Ctrl+Shift+F",
			Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: file,
				Payload: plugin.MustPayload(plugin.OpenOptions{Reveal: true})},
		})
	}
	path := a.ShellPath()
	if !a.Packaged {
		path = cmp.Or(a.Path, a.LinkPath, path)
	}
	acts = append(acts,
		plugin.Action{
			ID: "copy-path", Title: t.T("action.copyPath"), Shortcut: "Ctrl+Shift+C",
			Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: path},
		},
		plugin.Action{
			ID: "hide", Title: t.T("action.hide"),
			Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackHide,
				Payload: plugin.MustPayload(hidePayload{ID: a.ID})},
		},
	)
	return acts
}

// hidePayload is the Payload of the "hide" callback.
type hidePayload struct {
	ID string `json:"id"`
}

// HandleAction implements plugin.ActionHandler: the "reload" command and the
// "hide" callback.
func (p *Plugin) HandleAction(ctx context.Context, a plugin.ActionDo) (plugin.Result, error) {
	t := p.h.I18n()
	switch {
	case a.Kind == plugin.ActionRunCommand && a.Target == cmdReload:
		n, err := p.refresh(ctx)
		if err != nil {
			return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastFailure, Title: t.T("toast.reloadFailed"), Message: err.Error()}}, nil
		}
		return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: t.N("toast.reloaded", n)}}, nil

	case a.Kind == plugin.ActionCallback && a.Target == callbackHide:
		var hp hidePayload
		if err := json.Unmarshal(a.Payload, &hp); err != nil || hp.ID == "" {
			return plugin.Result{}, fmt.Errorf("apps: bad hide payload %q", a.Payload)
		}
		p.mu.Lock()
		if !slices.Contains(p.hidden, hp.ID) {
			p.hidden = append(p.hidden, hp.ID)
		}
		hidden := slices.Clone(p.hidden)
		name := hp.ID
		if i := slices.IndexFunc(p.apps, func(x shell.App) bool { return x.ID == hp.ID }); i >= 0 {
			name = p.apps[i].Name
		}
		p.mu.Unlock()
		b, err := json.Marshal(hidden)
		if err == nil {
			err = p.h.Storage().Set(keyHidden, b)
		}
		if err != nil {
			return plugin.Result{}, fmt.Errorf("apps: saving hidden apps: %w", err)
		}
		return plugin.Result{Refresh: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: t.T("toast.hidden", "name", name)}}, nil
	}
	return plugin.Result{}, fmt.Errorf("apps: unknown action %s %q", a.Kind, a.Target)
}
