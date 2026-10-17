// Package winget is the builtin.winget plugin: it searches, installs,
// uninstalls and upgrades packages with the Windows Package Manager
// (winget). See docs/winget.md.
//
// Searching reads the local SQLite index of the winget source, so no
// process starts while the user types. The installed and upgradable
// packages come from `winget list`, run in the background and cached.
// Changes run the winget command line as background tasks of the host, one
// at a time (docs/background-tasks.md).
package winget

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/pkg/plugin/bg"
)

// ID is the plugin ID.
const ID = "builtin.winget"

const (
	cmdSearch        = "search"
	cmdInstalled     = "installed"
	cmdUpdates       = "updates"
	cmdUpgradeAll    = "upgrade-all"
	cmdSources       = "sources"
	cmdUpdateSources = "update-sources"
	viewShow         = "show"

	callbackOp      = "op"
	callbackRefresh = "refresh"

	prefShowInRoot  = "showInRoot"
	prefMaxResults  = "maxResults"
	prefInstallMode = "installMode"

	modeDefault     = "default"
	modeSilent      = "silent"
	modeInteractive = "interactive"

	defaultMax = 50
	limitMax   = 500
	rootMax    = 5
	// rootScore ranks results of an explicit "winget <query>" high.
	rootScore = 0.9
)

var (
	// indexWait is how long a view waits for the first index.
	indexWait = 5 * time.Second
	// listWait is how long a view waits for the first `winget list` (views
	// have 10 seconds).
	listWait = 7 * time.Second
	// staleAfter is the age after which opening a view lists the installed
	// packages again.
	staleAfter = 5 * time.Minute
	// listTimeout and showTimeout bound the read-only winget commands.
	listTimeout = 2 * time.Minute
	showTimeout = 8 * time.Second
)

// errNotInstalled reports that something winget needs is missing.
var errNotInstalled = errors.New("not installed")

//go:embed locales/*.json
var locales embed.FS

// Options configures the plugin. Zero values select the real system.
type Options struct {
	// Exe is winget.exe; unless NoDetect it is found on PATH or in
	// %LOCALAPPDATA%\Microsoft\WindowsApps. Without it the plugin is
	// unavailable.
	Exe      string
	NoDetect bool
	// Getenv reads environment variables; nil means os.Getenv.
	Getenv func(string) string
	// Run runs read-only winget commands (default: Exe without a window).
	Run Runner
	// IndexStamp identifies the installed index of a pre-indexed source;
	// OpenIndex opens that index (default: the source's MSIX package).
	IndexStamp func(Source) (string, error)
	OpenIndex  func(Source) (*sql.DB, error)
	// OpenPins opens winget's pinning.db (default: in App Installer's
	// LocalState). It may return an error wrapping fs.ErrNotExist.
	OpenPins func() (*sql.DB, error)
	// Sync loads everything during Init and after operations before their
	// task counts as ended, which keeps glctl's fake environment and tests
	// deterministic.
	Sync bool
}

// installedState is the result of the last `winget list`.
type installedState struct {
	Items []Installed `json:"items"`
	At    time.Time   `json:"at"`
	Err   string      `json:"-"` // why the last listing failed, if it did
}

// Plugin is the WinGet plugin.
type Plugin struct {
	opts Options

	h      plugin.Host
	exe    string
	ctx    context.Context // cancelled by Close
	cancel context.CancelFunc
	unsubs []func()
	work   bg.Group // background work; closed by Close

	indexMu    sync.Mutex   // serializes index refreshes
	indexing   bg.Refresher // background index refreshes
	indexReady bg.Ready     // the first index refresh ended
	listing    bg.Refresher // background runs of `winget list`
	listReady  bg.Ready     // the installed packages are known

	mu        sync.Mutex
	sources   []Source
	index     *Index
	indexErr  error
	inst      *installedState
	listTried time.Time
	pins      map[string]Pin
	shows     map[string]*Details // by "source/id/version"
}

var (
	_ plugin.Plugin         = (*Plugin)(nil)
	_ plugin.RootProvider   = (*Plugin)(nil)
	_ plugin.ViewProvider   = (*Plugin)(nil)
	_ plugin.ActionHandler  = (*Plugin)(nil)
	_ plugin.LocaleProvider = (*Plugin)(nil)
)

// New returns the plugin for the winget of this machine.
func New() *Plugin { return NewWithOptions(Options{}) }

// NewWithOptions returns a plugin configured by o.
func NewWithOptions(o Options) *Plugin {
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	return &Plugin{opts: o, shows: map[string]*Details{}}
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
		Permissions:   []plugin.Permission{plugin.PermExec},
		Platforms:     []string{plugin.PlatformWindows}, // WinGet exists on Windows only
		Commands: []plugin.Command{
			{Name: cmdSearch, Title: "%command.search.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView, Icon: icon,
				Keywords: []string{"winget", "%command.search.keyword.install%", "%command.search.keyword.package%"}},
			{Name: cmdInstalled, Title: "%command.installed.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView, Icon: icon,
				Keywords: []string{"winget", "%command.installed.keyword%"}},
			{Name: cmdUpdates, Title: "%command.updates.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView, Icon: icon,
				Keywords: []string{"winget", "%command.updates.keyword%"}},
			{Name: cmdUpgradeAll, Title: "%command.upgradeAll.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeNoView, Icon: icon,
				Keywords: []string{"winget", "%command.updates.keyword%"}, Destructive: true},
			{Name: cmdSources, Title: "%command.sources.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView, Icon: plugin.Icon{Name: "globe"},
				Keywords: []string{"winget", "%command.sources.keyword%"}},
			{Name: cmdUpdateSources, Title: "%command.updateSources.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeNoView, Icon: plugin.Icon{Name: "rotate-cw"},
				Keywords: []string{"winget", "%command.sources.keyword%"}},
		},
		Preferences: []plugin.Preference{
			{Name: prefShowInRoot, Type: plugin.PrefBool, Title: "%pref.showInRoot.title%",
				Description: "%pref.showInRoot.description%", Default: plugin.MustPayload(true)},
			{Name: prefMaxResults, Type: plugin.PrefNumber, Title: "%pref.maxResults.title%",
				Description: "%pref.maxResults.description%", Default: plugin.MustPayload(defaultMax)},
			{Name: prefInstallMode, Type: plugin.PrefSelect, Title: "%pref.installMode.title%",
				Description: "%pref.installMode.description%", Default: plugin.MustPayload(modeDefault),
				Options: []plugin.Option{
					{Value: modeDefault, Title: "%pref.installMode.default%"},
					{Value: modeSilent, Title: "%pref.installMode.silent%"},
					{Value: modeInteractive, Title: "%pref.installMode.interactive%"},
				}},
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

// Init implements plugin.Plugin. Without winget the plugin is unavailable.
// The index is read in the background; the installed packages come from
// the cache and are listed again when a view needs them.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.mu.Lock()
	if p.work.Closed() {
		// Init after Close starts again (docs/plugin-guide.md "Lifecycle"):
		// as a new plugin, its background work having ended in Close.
		p.work, p.unsubs, p.sources, p.pins = bg.Group{}, nil, nil, nil
		p.index, p.indexErr, p.indexing, p.indexReady = nil, nil, bg.Refresher{}, bg.Ready{}
		p.inst, p.listTried, p.listing, p.listReady = nil, time.Time{}, bg.Refresher{}, bg.Ready{}
		p.shows = map[string]*Details{}
	}
	p.mu.Unlock()
	p.h = h
	t := h.I18n()
	p.exe = p.opts.Exe
	if p.exe == "" && !p.opts.NoDetect {
		p.exe = FindWinget(p.opts.Getenv)
	}
	if p.exe == "" {
		return fmt.Errorf("%s: %w", t.T("unavailable.notInstalled"), plugin.ErrUnavailable)
	}
	if p.opts.Run == nil {
		p.opts.Run = ExecRunner(p.exe)
	}
	if p.opts.IndexStamp == nil {
		p.opts.IndexStamp = realIndexStamp
	}
	if p.opts.OpenIndex == nil {
		p.opts.OpenIndex = realOpenIndex
	}
	if p.opts.OpenPins == nil {
		p.opts.OpenPins = realOpenPins(p.opts.Getenv)
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	if st, err := loadInstalledCache(h.CacheDir()); err == nil {
		p.inst = st
		p.listReady.Done()
	} else if !errors.Is(err, fs.ErrNotExist) {
		h.Logger().Warn("winget: ignoring the cache of installed packages", "err", err)
	}
	if p.opts.Sync {
		p.loadSources()
		p.refreshIndex(false)
		p.refreshInstalled(false)
	} else {
		p.scheduleIndex(true)
	}
	p.unsubs = append(p.unsubs, h.Events().Subscribe(plugin.EventWindowShown, func() { p.scheduleIndex(false) }))
	return nil
}

// Close implements plugin.Plugin. It stops the read-only winget commands
// and waits for background work. Operations are tasks of the host, which
// stops them when golauncher quits.
func (p *Plugin) Close() error {
	if !p.work.Close() || p.h == nil {
		return nil
	}
	for _, u := range p.unsubs {
		u()
	}
	if p.cancel != nil {
		p.cancel()
	}
	p.work.Wait()
	return nil
}

// ---- sources and index ----

// Sources returns the configured sources (the default "winget" source when
// they could not be listed).
func (p *Plugin) Sources() []Source {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sources
}

// loadSources lists the sources with `winget source export`.
func (p *Plugin) loadSources() []Source {
	ctx, cancel := context.WithTimeout(p.ctx, showTimeout)
	defer cancel()
	out, err := p.opts.Run(ctx, []string{"source", "export"})
	srcs, perr := parseSources(out)
	if err != nil || perr != nil {
		p.h.Logger().Warn("winget: listing the sources", "err", errors.Join(err, perr))
		srcs = []Source{defaultSource}
	}
	p.mu.Lock()
	p.sources = srcs
	p.mu.Unlock()
	return srcs
}

// Index returns the current index, or nil before the first one is read.
func (p *Plugin) Index() *Index {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.index
}

// waitIndex returns the index, waiting at most indexWait for the first.
func (p *Plugin) waitIndex(ctx context.Context) (*Index, error) {
	p.mu.Lock()
	ix, err := p.index, p.indexErr
	p.mu.Unlock()
	if ix != nil {
		return ix, err
	}
	p.indexReady.Wait(ctx, indexWait)
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.index, cmpErr(p.indexErr, err)
}

func cmpErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// scheduleIndex refreshes the index in the background (listing the sources
// first with withSources); calls during a refresh cause one more.
// (The runs requested during a refresh do not list the sources again.)
func (p *Plugin) scheduleIndex(withSources bool) {
	first := true
	p.indexing.Request(&p.work, p.h, func() {
		if first && (withSources || p.Sources() == nil) {
			p.loadSources()
		}
		first = false
		p.refreshIndex(true)
	})
}

// refreshIndex reads the index of every indexed source whose package
// changed since the last read. With notify, a change is reported with
// Invalidate.
func (p *Plugin) refreshIndex(notify bool) {
	p.indexMu.Lock()
	defer p.indexMu.Unlock()
	log := p.h.Logger()
	old := p.Index()
	srcs := p.Sources()
	if srcs == nil {
		srcs = []Source{defaultSource}
	}
	var cats []*Catalog
	var errs []error
	changed := old == nil
	for _, s := range srcs {
		if !s.Indexed() {
			continue
		}
		stamp, err := p.opts.IndexStamp(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("source %s: %w", s.Name, err))
			continue
		}
		if old != nil {
			if i := slices.IndexFunc(old.Catalogs, func(c *Catalog) bool { return c.Source == s.Name }); i >= 0 && old.Catalogs[i].Stamp == stamp {
				cats = append(cats, old.Catalogs[i])
				continue
			}
		}
		changed = true
		start := time.Now()
		c, err := p.readCatalog(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("source %s: %w", s.Name, err))
			continue
		}
		c.Stamp = stamp
		cats = append(cats, c)
		log.Info("winget: index read", "source", s.Name, "packages", len(c.Packages), "took", time.Since(start))
	}
	if old != nil && len(old.Catalogs) != len(cats) {
		changed = true
	}
	err := errors.Join(errs...)
	if err != nil {
		log.Warn("winget: reading the index", "err", err)
	}
	p.mu.Lock()
	if changed || p.index == nil {
		p.index = newIndex(cats)
	}
	if len(cats) == 0 {
		p.indexErr = cmpErr(err, errNotInstalled)
	} else {
		p.indexErr = nil
	}
	p.mu.Unlock()
	p.indexReady.Done()
	if notify && changed {
		p.h.Invalidate("")
	}
}

// readCatalog opens and reads the index of s.
func (p *Plugin) readCatalog(s Source) (*Catalog, error) {
	db, err := p.opts.OpenIndex(s)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return ReadCatalog(p.ctx, db, s.Name)
}

// ---- installed packages ----

// Installed returns the last list of installed packages (nil before the
// first).
func (p *Plugin) Installed() *installedState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inst
}

// pin returns the pin of the package id.
func (p *Plugin) pin(id string) *Pin {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pin, ok := p.pins[strings.ToLower(id)]; ok {
		return &pin
	}
	return nil
}

// ensureInstalled lists the installed packages again in the background
// when the list is missing or older than staleAfter.
func (p *Plugin) ensureInstalled() {
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
	p.listReady.Wait(ctx, listWait)
	return p.Installed()
}

// scheduleInstalled runs `winget list` in the background; calls while it
// runs cause one more run afterwards.
func (p *Plugin) scheduleInstalled() {
	p.listing.Request(&p.work, p.h, func() { p.refreshInstalled(true) })
}

// listArgs are the arguments of the `winget list` that reads the installed
// packages.
var listArgs = []string{"list", "--accept-source-agreements", "--disable-interactivity"}

// refreshInstalled lists the installed packages and reads the pins. With
// notify, open views are refreshed.
func (p *Plugin) refreshInstalled(notify bool) {
	ctx, cancel := context.WithTimeout(p.ctx, listTimeout)
	defer cancel()
	log := p.h.Logger()
	start := time.Now()
	out, err := p.opts.Run(ctx, listArgs)
	var names []string
	for _, s := range p.Sources() {
		names = append(names, s.Name)
	}
	items, perr := parseList(out, names)
	if err != nil && perr == nil && len(items) > 0 {
		// Some sources failed, the others answered.
		log.Warn("winget: list ended with an error", "err", err)
		err = nil
	}
	err = cmpErr(err, perr)
	pins := p.loadPins(ctx)
	now := p.h.Now()
	p.mu.Lock()
	if p.work.Closed() {
		p.mu.Unlock()
		return
	}
	p.listTried = now
	p.pins = pins
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
	p.listReady.Done()
	if err != nil {
		log.Warn("winget: listing installed packages", "err", err)
	} else {
		log.Info("winget: installed packages listed", "packages", len(items), "took", time.Since(start))
		if err := saveInstalledCache(p.h.CacheDir(), st); err != nil {
			log.Warn("winget: saving the cache of installed packages", "err", err)
		}
	}
	if notify {
		p.h.Invalidate("")
	}
}

// loadPins reads pinning.db; no file means no pins.
func (p *Plugin) loadPins(ctx context.Context) map[string]Pin {
	db, err := p.opts.OpenPins()
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			p.h.Logger().Warn("winget: opening pinning.db", "err", err)
		}
		return nil
	}
	defer db.Close()
	pins, err := readPins(ctx, db)
	if err != nil {
		p.h.Logger().Warn("winget: reading pinning.db", "err", err)
		return nil
	}
	return pins
}

// cacheFile is the cache of the last `winget list`.
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
	tmp := filepath.Join(dir, cacheFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, cacheFile))
}

// installedByID returns the installed packages by lower-case ID.
func (st *installedState) byID() map[string]*Installed {
	m := map[string]*Installed{}
	if st == nil {
		return m
	}
	for i := range st.Items {
		m[strings.ToLower(st.Items[i].ID)] = &st.Items[i]
	}
	return m
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

// rootQuery returns the search text after a "winget " or "wg " prefix.
func rootQuery(text string) (string, bool) {
	text = strings.TrimSpace(text)
	head, rest, ok := strings.Cut(text, " ")
	if !ok || !(strings.EqualFold(head, "winget") || strings.EqualFold(head, "wg")) {
		return "", false
	}
	rest = strings.TrimSpace(rest)
	return rest, rest != ""
}

// RootItems implements plugin.RootProvider. With the showInRoot preference,
// "winget <query>" and "wg <query>" list the best matching packages.
func (p *Plugin) RootItems(ctx context.Context, q plugin.Query) ([]plugin.Item, error) {
	if !p.h.Preferences().Bool(prefShowInRoot) {
		return nil, nil
	}
	text, ok := rootQuery(q.Text)
	ix := p.Index()
	if !ok || ix == nil {
		return nil, nil
	}
	t := p.h.I18n()
	group := t.T("group")
	installed := p.Installed().byID()
	var items []plugin.Item
	for i, pkg := range ix.Search(ctx, text, rootMax) {
		it := p.packageItem(t, pkg, installed[strings.ToLower(pkg.ID)])
		it.Score = rootScore - float64(float64(i)*0.01)
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
	case a.Kind == plugin.ActionRunCommand && a.Target == cmdUpdateSources:
		return p.start(opRequest{Kind: opSourceUpdate})
	case a.Kind == plugin.ActionCallback && a.Target == callbackOp:
		var req opRequest
		if err := json.Unmarshal(a.Payload, &req); err != nil {
			return plugin.Result{}, fmt.Errorf("winget: bad operation payload %q", a.Payload)
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
	return plugin.Result{}, fmt.Errorf("winget: unknown action %s %q", a.Kind, a.Target)
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
