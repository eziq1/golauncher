// Package homebrew is the builtin.homebrew plugin: it searches, installs,
// uninstalls and upgrades formulae and casks with Homebrew, the macOS
// counterpart of the Scoop and WinGet plugins. See docs/homebrew.md.
//
// Searching reads the API index brew keeps in its cache directory, so no
// process starts while the user types. The installed and outdated
// packages come from `brew info --json=v2 --installed`, run in the
// background and cached. Changes run brew as background tasks of the host,
// one at a time (docs/background-tasks.md).
package homebrew

import (
	"cmp"
	"context"
	"embed"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// ID is the plugin ID.
const ID = "builtin.homebrew"

const (
	cmdSearch     = "search"
	cmdInstalled  = "installed"
	cmdOutdated   = "outdated"
	cmdUpgradeAll = "upgrade-all"
	cmdUpdate     = "update"
	viewInfo      = "info"

	callbackOp      = "op"
	callbackRefresh = "refresh"

	prefShowInRoot = "showInRoot"
	prefMaxResults = "maxResults"
	prefAutoUpdate = "autoUpdate"

	defaultMax = 50
	limitMax   = 500
	rootMax    = 5
	// rootScore ranks results of an explicit "brew <query>" high.
	rootScore = 0.9

	// brewSite is where Homebrew is installed from.
	brewSite = "https://brew.sh"
	// brewInstall is the install command shown on brew.sh.
	brewInstall = `/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`
)

var (
	// indexWait is how long a view waits for the first index.
	indexWait = 5 * time.Second
	// listWait is how long a view waits for the first list of installed
	// packages (views have 10 seconds; brew info takes several).
	listWait = 7 * time.Second
	// staleAfter is the age after which opening a view lists the installed
	// packages again.
	staleAfter = 5 * time.Minute
	// listTimeout and infoTimeout bound the read-only brew commands.
	listTimeout = 2 * time.Minute
	infoTimeout = 9 * time.Second
)

//go:embed locales/*.json
var locales embed.FS

// Options configures the plugin. Zero values select the real system.
type Options struct {
	// Exe is brew; unless NoDetect it is found with FindBrew (and found
	// again when the launcher is shown, after Homebrew was installed).
	// Without it the views say that Homebrew is not installed.
	Exe      string
	NoDetect bool
	// Cache is Homebrew's cache directory, which holds the API index
	// (default: $HOMEBREW_CACHE, else `brew --cache`).
	Cache string
	// Getenv reads environment variables; nil means os.Getenv.
	Getenv func(string) string
	// Run runs read-only brew commands (default: Exe).
	Run Runner
	// Sync loads everything during Init and after operations before their
	// task counts as ended, which keeps tests deterministic.
	Sync bool
}

// installedState is the result of the last listing of installed packages.
type installedState struct {
	Items []Installed `json:"items"`
	At    time.Time   `json:"at"`
	Err   string      `json:"-"` // why the last listing failed, if it did
}

// byKey returns the installed packages by key(kind, name).
func (st *installedState) byKey() map[string]*Installed {
	m := map[string]*Installed{}
	if st == nil {
		return m
	}
	for i := range st.Items {
		m[key(st.Items[i].Kind, st.Items[i].Name)] = &st.Items[i]
	}
	return m
}

// Plugin is the Homebrew plugin.
type Plugin struct {
	opts Options

	h      plugin.Host
	ctx    context.Context // cancelled by Close
	cancel context.CancelFunc
	unsubs []func()
	wg     sync.WaitGroup

	indexMu sync.Mutex // serializes index refreshes

	mu         sync.Mutex
	closed     bool
	exe        string
	cache      string // Homebrew's cache directory, once known
	index      *Index
	indexErr   error
	indexReady chan struct{} // closed when the first index refresh ended
	indexDone  bool
	indexing   bool
	indexAgain bool
	inst       *installedState
	listReady  chan struct{} // closed when installed packages are known
	listDone   bool
	listing    bool
	listAgain  bool
	listTried  time.Time
	shows      map[string]*Details // by key
}

var (
	_ plugin.Plugin         = (*Plugin)(nil)
	_ plugin.RootProvider   = (*Plugin)(nil)
	_ plugin.ViewProvider   = (*Plugin)(nil)
	_ plugin.ActionHandler  = (*Plugin)(nil)
	_ plugin.LocaleProvider = (*Plugin)(nil)
)

// New returns the plugin for the Homebrew of this machine.
func New() *Plugin { return NewWithOptions(Options{}) }

// NewWithOptions returns a plugin configured by o.
func NewWithOptions(o Options) *Plugin {
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	return &Plugin{opts: o, indexReady: make(chan struct{}), listReady: make(chan struct{}), shows: map[string]*Details{}}
}

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	icon := plugin.Icon{Name: "package"}
	return plugin.Manifest{
		ID:            ID,
		Name:          "%plugin.name%",
		Description:   "%plugin.description%",
		Version:       "1.0.0",
		Icon:          icon,
		DefaultLocale: "en",
		Platforms:     []string{plugin.PlatformDarwin},
		Permissions:   []plugin.Permission{plugin.PermExec},
		Commands: []plugin.Command{
			{Name: cmdSearch, Title: "%command.search.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView, Icon: icon,
				Keywords: []string{"brew", "homebrew", "cask", "formula", "%command.search.keyword.install%", "%command.search.keyword.package%"}},
			{Name: cmdInstalled, Title: "%command.installed.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView, Icon: icon,
				Keywords: []string{"brew", "homebrew", "%command.installed.keyword%"}},
			{Name: cmdOutdated, Title: "%command.outdated.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView, Icon: icon,
				Keywords: []string{"brew", "homebrew", "outdated", "%command.outdated.keyword%"}},
			{Name: cmdUpgradeAll, Title: "%command.upgradeAll.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeNoView, Icon: icon,
				Keywords: []string{"brew", "homebrew", "%command.outdated.keyword%"}, Destructive: true},
			{Name: cmdUpdate, Title: "%command.update.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeNoView, Icon: plugin.Icon{Name: "rotate-cw"},
				Keywords: []string{"brew", "homebrew", "%command.update.keyword%"}},
		},
		Preferences: []plugin.Preference{
			{Name: prefShowInRoot, Type: plugin.PrefBool, Title: "%pref.showInRoot.title%",
				Description: "%pref.showInRoot.description%", Default: plugin.MustPayload(true)},
			{Name: prefMaxResults, Type: plugin.PrefNumber, Title: "%pref.maxResults.title%",
				Description: "%pref.maxResults.description%", Default: plugin.MustPayload(defaultMax)},
			{Name: prefAutoUpdate, Type: plugin.PrefBool, Title: "%pref.autoUpdate.title%",
				Description: "%pref.autoUpdate.description%", Default: plugin.MustPayload(true)},
		},
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

// Init implements plugin.Plugin. Without brew the plugin still starts: its
// views say that Homebrew is not installed and link to brew.sh, and brew
// is looked for again whenever the launcher is shown. The index is read in
// the background; the installed packages come from the cache and are
// listed again when a view needs them.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.h = h
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.exe = p.opts.Exe
	if p.exe == "" && !p.opts.NoDetect {
		p.exe = FindBrew(nil, nil)
	}
	if p.exe == "" {
		h.Logger().Info("homebrew: brew not found")
	}
	if st, err := loadInstalledCache(h.CacheDir()); err == nil {
		p.inst = st
		p.listDone = true
		close(p.listReady)
	} else if !errors.Is(err, fs.ErrNotExist) {
		h.Logger().Warn("homebrew: ignoring the cache of installed packages", "err", err)
	}
	if p.opts.Sync {
		p.refreshIndex(false)
		if p.brew() != "" {
			p.refreshInstalled(false)
		}
	} else {
		p.scheduleIndex()
	}
	p.unsubs = append(p.unsubs, h.Events().Subscribe(plugin.EventWindowShown, p.windowShown))
	return nil
}

// windowShown looks for brew again if it was missing and checks whether
// the index changed (a stat, unless it did).
func (p *Plugin) windowShown() {
	p.mu.Lock()
	missing := p.exe == "" && !p.opts.NoDetect
	p.mu.Unlock()
	if missing {
		if exe := FindBrew(nil, nil); exe != "" {
			p.mu.Lock()
			p.exe = exe
			p.mu.Unlock()
			p.h.Logger().Info("homebrew: brew found", "path", exe)
			p.h.Invalidate("")
		}
	}
	p.scheduleIndex()
}

// Close implements plugin.Plugin. It stops the read-only brew commands and
// waits for background work. Operations are tasks of the host, which stops
// them when golauncher quits.
func (p *Plugin) Close() error {
	p.mu.Lock()
	if p.closed || p.h == nil {
		p.closed = true
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	for _, u := range p.unsubs {
		u()
	}
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	return nil
}

// spawnLocked runs fn in the background through Host.Go unless the plugin
// is closed; p.mu is held. It reports whether fn was started.
func (p *Plugin) spawnLocked(fn func()) bool {
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

// brew returns the path of brew, or "" when Homebrew is not installed.
func (p *Plugin) brew() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exe
}

// run runs a read-only brew command.
func (p *Plugin) run(ctx context.Context, args []string) ([]byte, error) {
	exe := p.brew()
	if exe == "" {
		return nil, errNotInstalled
	}
	run := p.opts.Run
	if run == nil {
		run = ExecRunner(exe)
	}
	return run(ctx, args)
}

// errNotInstalled reports that brew was not found.
var errNotInstalled = errors.New("brew not found")

// cacheDir returns Homebrew's cache directory: Options.Cache,
// $HOMEBREW_CACHE, what `brew --cache` prints (asked once), else the
// default location.
func (p *Plugin) cacheDir() string {
	p.mu.Lock()
	dir := p.cache
	p.mu.Unlock()
	if dir != "" {
		return dir
	}
	dir = cmp.Or(p.opts.Cache, p.opts.Getenv("HOMEBREW_CACHE"))
	if dir == "" && p.brew() != "" {
		ctx, cancel := context.WithTimeout(p.ctx, infoTimeout)
		out, err := p.run(ctx, []string{"--cache"})
		cancel()
		if err == nil {
			dir = strings.TrimSpace(lastLine(string(out)))
		} else {
			p.h.Logger().Warn("homebrew: brew --cache", "err", err)
		}
	}
	if dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
	} else {
		home, _ := os.UserHomeDir()
		dir = defaultCache(home)
	}
	p.mu.Lock()
	if p.exe != "" || p.opts.Cache != "" {
		p.cache = dir // without brew, ask again once it is installed
	}
	p.mu.Unlock()
	return dir
}

// ---- index ----

// Index returns the current index, or nil before the first one is read.
func (p *Plugin) Index() *Index {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.index
}

// waitIndex returns the index, waiting at most indexWait for the first.
func (p *Plugin) waitIndex(ctx context.Context) (*Index, error) {
	p.mu.Lock()
	ix, err, done := p.index, p.indexErr, p.indexDone
	p.mu.Unlock()
	if ix != nil || done {
		return ix, err
	}
	timer := time.NewTimer(indexWait)
	defer timer.Stop()
	select {
	case <-p.indexReady:
	case <-ctx.Done():
	case <-timer.C:
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.index, p.indexErr
}

// scheduleIndex refreshes the index in the background; calls during a
// refresh cause one more.
func (p *Plugin) scheduleIndex() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.indexing {
		p.indexAgain = true
		return
	}
	p.indexing = p.spawnLocked(func() {
		for {
			p.refreshIndex(true)
			p.mu.Lock()
			if !p.indexAgain || p.closed {
				p.indexing, p.indexAgain = false, false
				p.mu.Unlock()
				return
			}
			p.indexAgain = false
			p.mu.Unlock()
		}
	})
}

// refreshIndex reads the index again if its files changed. With notify, a
// change is reported with Invalidate.
func (p *Plugin) refreshIndex(notify bool) {
	p.indexMu.Lock()
	defer p.indexMu.Unlock()
	log := p.h.Logger()
	old := p.Index()
	var ix *Index
	files, err := findIndex(p.cacheDir())
	changed := false
	if err == nil {
		if old != nil && old.Stamp == files.stamp {
			ix = old
		} else {
			start := time.Now()
			ix, err = readIndex(files, p.h.Now())
			if err == nil {
				changed = true
				f, c := ix.Counts()
				log.Info("homebrew: index read", "format", ix.Format, "formulae", f, "casks", c, "took", time.Since(start))
			}
		}
	}
	if err != nil {
		if !isNotExist(err) {
			log.Warn("homebrew: reading the index", "err", err)
		}
		ix = old // keep the last good index
	}
	p.mu.Lock()
	first := !p.indexDone
	p.indexDone = true
	if ix != old {
		p.index = ix
	}
	errChanged := (p.indexErr == nil) != (err == nil)
	p.indexErr = err
	p.mu.Unlock()
	if first {
		close(p.indexReady)
	}
	if notify && (changed || errChanged) {
		p.h.Invalidate("")
	}
}

// ---- installed packages ----

// Installed returns the last list of installed packages (nil before the
// first).
func (p *Plugin) Installed() *installedState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inst
}

// ensureInstalled lists the installed packages again in the background
// when the list is missing or older than staleAfter.
func (p *Plugin) ensureInstalled() {
	if p.brew() == "" {
		return
	}
	p.mu.Lock()
	stale := p.inst == nil || (!p.opts.Sync && p.h.Now().Sub(p.listTried) > staleAfter)
	p.mu.Unlock()
	if stale {
		p.scheduleInstalled()
	}
}

// waitInstalled returns the installed packages, waiting at most listWait
// for the first list.
func (p *Plugin) waitInstalled(ctx context.Context) *installedState {
	p.ensureInstalled()
	if st := p.Installed(); st != nil {
		return st
	}
	timer := time.NewTimer(listWait)
	defer timer.Stop()
	select {
	case <-p.listReady:
	case <-ctx.Done():
	case <-timer.C:
	}
	return p.Installed()
}

// scheduleInstalled lists the installed packages in the background; calls
// while it runs cause one more run afterwards.
func (p *Plugin) scheduleInstalled() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listing {
		p.listAgain = true
		return
	}
	p.listing = p.spawnLocked(func() {
		for {
			p.refreshInstalled(true)
			p.mu.Lock()
			if !p.listAgain || p.closed {
				p.listing, p.listAgain = false, false
				p.mu.Unlock()
				return
			}
			p.listAgain = false
			p.mu.Unlock()
		}
	})
}

// listArgs are the arguments of the command that lists the installed
// packages, with whether each is outdated.
var listArgs = []string{"info", "--json=v2", "--installed"}

// refreshInstalled lists the installed packages. With notify, open views
// are refreshed.
func (p *Plugin) refreshInstalled(notify bool) {
	ctx, cancel := context.WithTimeout(p.ctx, listTimeout)
	defer cancel()
	log := p.h.Logger()
	start := time.Now()
	out, err := p.run(ctx, listArgs)
	var items []Installed
	if err == nil {
		items, err = ParseInstalled(out)
	}
	now := p.h.Now()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	first := !p.listDone
	p.listDone = true
	p.listTried = now
	switch {
	case err == nil:
		p.inst = &installedState{Items: items, At: now}
	case p.inst == nil:
		p.inst = &installedState{Err: err.Error(), At: now}
	default:
		st := *p.inst
		st.Err = err.Error()
		p.inst = &st
	}
	st := p.inst
	p.mu.Unlock()
	if first {
		close(p.listReady)
	}
	if err != nil {
		log.Warn("homebrew: listing installed packages", "err", err)
	} else {
		log.Info("homebrew: installed packages listed", "packages", len(items), "took", time.Since(start))
		if err := saveInstalledCache(p.h.CacheDir(), st); err != nil {
			log.Warn("homebrew: saving the cache of installed packages", "err", err)
		}
	}
	if notify {
		p.h.Invalidate("")
	}
}

// cacheFile is the cache of the last list of installed packages.
const cacheFile = "installed.json"

// cacheVersion changes when the cache format changes.
const cacheVersion = 1

type cacheData struct {
	Version int         `json:"version"`
	Items   []Installed `json:"items"`
	At      time.Time   `json:"at"`
}

func loadInstalledCache(dir string) (*installedState, error) {
	b, err := os.ReadFile(filepath.Join(dir, cacheFile))
	if err != nil {
		return nil, err
	}
	var c cacheData
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.Version != cacheVersion {
		return nil, fs.ErrNotExist
	}
	return &installedState{Items: c.Items, At: c.At}, nil
}

func saveInstalledCache(dir string, st *installedState) error {
	b, err := json.Marshal(cacheData{Version: cacheVersion, Items: st.Items, At: st.At})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, cacheFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, cacheFile))
}

// ---- root search and actions ----

// maxResults is the maxResults preference, clamped to [1, limitMax].
func (p *Plugin) maxResults() int {
	n := p.h.Preferences().Int(prefMaxResults)
	if n <= 0 {
		return defaultMax
	}
	return min(n, limitMax)
}

// rootQuery returns the search text after a "brew " prefix.
func rootQuery(text string) (string, bool) {
	text = strings.TrimSpace(text)
	head, rest, ok := strings.Cut(text, " ")
	if !ok || !strings.EqualFold(head, "brew") {
		return "", false
	}
	rest = strings.TrimSpace(rest)
	return rest, rest != ""
}

// RootItems implements plugin.RootProvider. With the showInRoot preference,
// "brew <query>" lists the best matching packages.
func (p *Plugin) RootItems(ctx context.Context, q plugin.Query) ([]plugin.Item, error) {
	if !p.h.Preferences().Bool(prefShowInRoot) {
		return nil, nil
	}
	text, ok := rootQuery(q.Text)
	if !ok {
		return nil, nil
	}
	t := p.h.I18n()
	group := t.T("group")
	if p.brew() == "" {
		it := notInstalledItem(t)
		it.Score, it.Group = rootScore, group
		return []plugin.Item{it}, nil
	}
	ix := p.Index()
	if ix == nil {
		return nil, nil
	}
	installed := p.Installed().byKey()
	var items []plugin.Item
	for i, pkg := range ix.Search(ctx, text, rootMax) {
		it := p.packageItem(t, pkg, installed[key(pkg.Kind, pkg.Name)])
		it.Score = rootScore - float64(i)*0.01
		it.Group = group
		it.Detail = nil
		items = append(items, it)
	}
	return items, nil
}

// HandleAction implements plugin.ActionHandler. Operations start as
// background tasks; the action does not wait for them.
func (p *Plugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	switch {
	case a.Kind == plugin.ActionRunCommand && a.Target == cmdUpgradeAll:
		return p.start(opRequest{Kind: opUpgradeAll})
	case a.Kind == plugin.ActionRunCommand && a.Target == cmdUpdate:
		return p.start(opRequest{Kind: opUpdate})
	case a.Kind == plugin.ActionCallback && a.Target == callbackOp:
		var req opRequest
		if err := json.Unmarshal(a.Payload, &req); err != nil {
			return plugin.Result{}, fmt.Errorf("homebrew: bad operation payload %q", a.Payload)
		}
		return p.start(req)
	case a.Kind == plugin.ActionCallback && a.Target == callbackRefresh:
		if p.opts.Sync {
			p.refreshInstalled(true)
		} else {
			p.scheduleInstalled()
		}
		t := p.h.I18n()
		return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: t.T("toast.refreshing")}}, nil
	}
	return plugin.Result{}, fmt.Errorf("homebrew: unknown action %s %q", a.Kind, a.Target)
}

// failure is a Result with a failure toast.
func failure(title, msg string) plugin.Result {
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastFailure, Title: title, Message: msg}}
}

// truncate shortens s to at most n runes, adding an ellipsis.
func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
