// Package scoop is the builtin.scoop plugin: it searches, installs,
// uninstalls and updates packages of the Scoop package manager
// (https://scoop.sh). See docs/scoop.md.
//
// Everything it shows is read from Scoop's own files (bucket manifests and
// installed apps), which is fast enough to search as the user types. Changes
// run the scoop command line as background tasks of the host, one at a time
// (docs/background-tasks.md).
package scoop

import (
	"context"
	"embed"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
	"weak"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/pkg/plugin/bg"
)

// ID is the plugin ID.
const ID = "builtin.scoop"

const (
	cmdSearch        = "search"
	cmdInstalled     = "installed"
	cmdUpdates       = "updates"
	cmdUpdateAll     = "update-all"
	cmdUpdateBuckets = "update-buckets"
	cmdBuckets       = "buckets"
	cmdCleanup       = "cleanup"

	callbackOp = "op"

	prefScoopPath          = "scoopPath"
	prefShowInRoot         = "showInRoot"
	prefMaxResults         = "maxResults"
	prefUpdateBucketsFirst = "updateBucketsFirst"

	defaultMax = 50
	limitMax   = 500
	rootMax    = 5
	// rootScore ranks results of an explicit "scoop <query>" high.
	rootScore = 0.9
)

// indexWait is how long a view waits for the first index.
var indexWait = 5 * time.Second

//go:embed locales/*.json
var locales embed.FS

// Options configures the plugin.
type Options struct {
	// Root is the Scoop root. Unless NoDetect is set it is found instead:
	// the scoopPath preference, $SCOOP, then %USERPROFILE%\scoop.
	Root string
	// Global is the directory of globally installed apps; unless NoDetect
	// it is $SCOOP_GLOBAL or %ProgramData%\scoop.
	Global string
	// NoDetect uses Root and Global as given (fake environments).
	NoDetect bool
	// Shell is the PowerShell that runs Scoop commands (default: pwsh.exe if
	// installed, else powershell.exe).
	Shell string
	// Getenv reads environment variables; nil means os.Getenv.
	Getenv func(string) string
	// Sync builds the index during Init and reads it again before an
	// operation's task counts as ended, which keeps glctl's fake environment
	// and tests deterministic.
	Sync bool
}

// Plugin is the Scoop plugin.
type Plugin struct {
	opts Options

	h      plugin.Host
	root   string
	global string
	unsubs []func()
	work   bg.Group // background work; closed by Close

	refreshMu  sync.Mutex      // serializes reading the index and the cache
	index      bg.Lazy[*Index] // read on first use, dropped when idle
	refreshing bg.Refresher    // background index refreshes
	shellOnce  sync.Once
	shellPath  string

	mu sync.Mutex
	// The last cleanup measurement (see reclaimFor); weak, so that it does
	// not keep a dropped index alive.
	reclaim   Reclaim
	reclaimIx weak.Pointer[Index]
	reclaimAt time.Time
}

var (
	_ plugin.Plugin         = (*Plugin)(nil)
	_ plugin.RootProvider   = (*Plugin)(nil)
	_ plugin.ViewProvider   = (*Plugin)(nil)
	_ plugin.ActionHandler  = (*Plugin)(nil)
	_ plugin.LocaleProvider = (*Plugin)(nil)
)

// New returns the plugin for the Scoop of this machine.
func New() *Plugin { return NewWithOptions(Options{}) }

// NewWithOptions returns a plugin configured by o.
func NewWithOptions(o Options) *Plugin {
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	return &Plugin{opts: o}
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
		Platforms:     []string{plugin.PlatformWindows}, // Scoop is a Windows package manager
		Commands: []plugin.Command{
			{Name: cmdSearch, Title: "%command.search.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView, Icon: icon,
				Keywords: []string{"scoop", "%command.search.keyword.install%", "%command.search.keyword.package%"}},
			{Name: cmdInstalled, Title: "%command.installed.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView, Icon: icon,
				Keywords: []string{"scoop", "%command.installed.keyword%"}},
			{Name: cmdUpdates, Title: "%command.updates.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView, Icon: icon,
				Keywords: []string{"scoop", "%command.updates.keyword%"}},
			{Name: cmdUpdateAll, Title: "%command.updateAll.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeNoView, Icon: icon,
				Keywords: []string{"scoop", "%command.updates.keyword%"}},
			{Name: cmdUpdateBuckets, Title: "%command.updateBuckets.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeNoView, Icon: icon,
				Keywords: []string{"scoop"}},
			{Name: cmdBuckets, Title: "%command.buckets.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView, Icon: plugin.Icon{Name: "folder"},
				Keywords: []string{"scoop"}},
			{Name: cmdCleanup, Title: "%command.cleanup.title%", Subtitle: "%command.subtitle%", Mode: plugin.ModeView, Icon: plugin.Icon{Name: "trash"},
				Keywords: []string{"scoop", "%command.cleanup.keyword%"}},
		},
		Preferences: []plugin.Preference{
			{Name: prefScoopPath, Type: plugin.PrefPath, Title: "%pref.scoopPath.title%",
				Description: "%pref.scoopPath.description%", Default: plugin.MustPayload("")},
			{Name: prefShowInRoot, Type: plugin.PrefBool, Title: "%pref.showInRoot.title%",
				Description: "%pref.showInRoot.description%", Default: plugin.MustPayload(false)},
			{Name: prefMaxResults, Type: plugin.PrefNumber, Title: "%pref.maxResults.title%",
				Description: "%pref.maxResults.description%", Default: plugin.MustPayload(defaultMax)},
			{Name: prefUpdateBucketsFirst, Type: plugin.PrefBool, Title: "%pref.updateBucketsFirst.title%",
				Description: "%pref.updateBucketsFirst.description%", Default: plugin.MustPayload(true)},
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

// Init implements plugin.Plugin. Without Scoop the plugin is unavailable.
// The index is loaded from the cache (or built) when first needed and
// dropped when idle (bg.Lazy); the cache is brought up to date in the
// background.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.mu.Lock()
	if p.work.Closed() {
		// Init after Close starts again (docs/plugin-guide.md "Lifecycle"):
		// as a new plugin, its background work having ended in Close.
		p.work, p.unsubs, p.refreshing = bg.Group{}, nil, bg.Refresher{}
		p.reclaim, p.reclaimIx, p.reclaimAt = Reclaim{}, weak.Pointer[Index]{}, time.Time{}
	}
	p.mu.Unlock()
	p.h = h
	t := h.I18n()
	if p.opts.NoDetect {
		if IsRoot(p.opts.Root) {
			p.root, p.global = p.opts.Root, p.opts.Global
		}
	} else {
		configured := h.Preferences().String(prefScoopPath)
		root, bad := DetectRoot(configured, p.opts.Getenv)
		if bad {
			h.Logger().Warn("scoop: the configured Scoop folder has no apps\\scoop\\current", "path", configured, "using", root)
		}
		p.root, p.global = root, DetectGlobal(p.opts.Getenv)
	}
	if p.root == "" {
		return fmt.Errorf("%s: %w", t.T("unavailable.notInstalled"), plugin.ErrUnavailable)
	}
	p.index = bg.Lazy[*Index]{Host: h, Group: &p.work, Load: p.loadIndex,
		OnLate: func() { h.Invalidate("") }}
	if p.opts.Sync {
		p.index.Get(context.Background())
	} else {
		p.scheduleRefresh()
	}
	p.unsubs = append(p.unsubs, h.Events().Subscribe(plugin.EventWindowShown, p.scheduleRefresh))
	return nil
}

// Close implements plugin.Plugin. It waits for background work. Operations
// are tasks of the host, which stops them when golauncher quits.
func (p *Plugin) Close() error {
	if !p.work.Close() || p.h == nil {
		return nil
	}
	for _, u := range p.unsubs {
		u()
	}
	p.work.Wait()
	return nil
}

// goFn runs fn through Host.Go; it is used for parallel manifest parsing,
// which the caller waits for.
func (p *Plugin) goFn(fn func()) { p.h.Go(fn) }

// Root returns the Scoop root in use.
func (p *Plugin) Root() string { return p.root }

// Index returns the index, loading it first if it is not loaded.
func (p *Plugin) Index() *Index {
	ix, _ := p.index.Get(context.Background())
	return ix
}

// waitIndex returns the index, waiting at most indexWait for it to be
// loaded.
func (p *Plugin) waitIndex(ctx context.Context) *Index {
	ctx, cancel := context.WithTimeout(ctx, indexWait)
	defer cancel()
	ix, _ := p.index.Get(ctx)
	return ix
}

// scheduleRefresh refreshes the index in the background; calls during a
// refresh cause one more afterwards.
func (p *Plugin) scheduleRefresh() {
	p.refreshing.Request(&p.work, p.h, func() { p.refresh(true) })
}

// loadIndex builds the index from the cache, reading the buckets whose
// stamp changed and the installed apps (bg.Lazy's Load).
func (p *Plugin) loadIndex() (*Index, error) {
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	start := time.Now()
	buckets, _ := p.updateBuckets(p.cachedBuckets())
	apps := append(readApps(p.root, false), readApps(p.global, true)...)
	ix := newIndex(p.root, p.global, buckets, apps)
	p.h.Logger().Info("scoop: index loaded", "packages", ix.PackageCount(), "apps", len(apps), "took", time.Since(start))
	return ix, nil
}

// refresh brings the index up to date: buckets whose stamp changed are read
// again, installed apps always. An index that is not loaded stays so: only
// the cache is brought up to date, and it is not even read while the stamps
// saved with it match the buckets. With notify, a change of the loaded index
// is reported with Invalidate.
func (p *Plugin) refresh(notify bool) {
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	old, ok := p.index.Peek()
	if !ok {
		if !cacheCurrent(p.h.CacheDir(), p.root) {
			p.updateBuckets(p.cachedBuckets())
		}
		return
	}
	buckets, changed := p.updateBuckets(old.Buckets)
	apps := append(readApps(p.root, false), readApps(p.global, true)...)
	if !changed && reflect.DeepEqual(old.Apps, apps) {
		return
	}
	if p.index.Set(newIndex(p.root, p.global, buckets, apps), nil) && notify {
		p.h.Invalidate("")
	}
}

// cachedBuckets returns the buckets of the cache, or nil without one.
func (p *Plugin) cachedBuckets() []*Bucket {
	cached, err := loadCache(p.h.CacheDir(), p.root)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		p.h.Logger().Warn("scoop: ignoring the index cache", "err", err)
	}
	return cached
}

// updateBuckets returns the buckets, reusing those of old whose stamp did
// not change, and saves them to the cache when they changed (or the cache's
// stamps are missing). The caller holds refreshMu.
func (p *Plugin) updateBuckets(old []*Bucket) ([]*Bucket, bool) {
	log := p.h.Logger()
	start := time.Now()
	buckets, changed, errs := refreshBuckets(p.root, old, p.goFn)
	for _, err := range errs {
		log.Warn("scoop: reading a bucket", "err", err)
	}
	if changed {
		n := 0
		for _, b := range buckets {
			n += len(b.Packages)
		}
		log.Info("scoop: buckets read", "buckets", len(buckets), "packages", n, "took", time.Since(start))
	}
	if changed || !cacheCurrent(p.h.CacheDir(), p.root) {
		if err := saveCache(p.h.CacheDir(), p.root, buckets); err != nil {
			log.Warn("scoop: saving the index cache", "err", err)
		}
	}
	return buckets, changed
}

// maxResults is the maxResults preference, clamped to [1, limitMax].
func (p *Plugin) maxResults() int {
	n := p.h.Preferences().Int(prefMaxResults)
	if n <= 0 {
		return defaultMax
	}
	return min(n, limitMax)
}

// rootQuery returns the search text after a "scoop " or "sc " prefix.
func rootQuery(text string) (string, bool) {
	text = strings.TrimSpace(text)
	head, rest, ok := strings.Cut(text, " ")
	if !ok || !(strings.EqualFold(head, "scoop") || strings.EqualFold(head, "sc")) {
		return "", false
	}
	rest = strings.TrimSpace(rest)
	return rest, rest != ""
}

// RootItems implements plugin.RootProvider. With the showInRoot preference,
// "scoop <query>" and "sc <query>" list the best matching packages.
func (p *Plugin) RootItems(ctx context.Context, q plugin.Query) ([]plugin.Item, error) {
	if !p.h.Preferences().Bool(prefShowInRoot) {
		return nil, nil
	}
	text, ok := rootQuery(q.Text)
	if !ok {
		return nil, nil
	}
	ix, ok := p.index.TryGet() // if not loaded, Invalidate follows the read
	if !ok {
		return nil, nil
	}
	t := p.h.I18n()
	group := t.T("group")
	var items []plugin.Item
	for i, m := range ix.Search(ctx, text, rootMax) {
		it := p.packageItem(t, ix, m.Pkg)
		it.Score = rootScore - float64(float64(i)*0.01)
		it.Group = group
		it.Detail = nil
		items = append(items, it)
	}
	return items, nil
}

// HandleAction implements plugin.ActionHandler. Scoop operations start as
// background tasks; the action does not wait for them.
func (p *Plugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	switch {
	case a.Kind == plugin.ActionRunCommand && a.Target == cmdUpdateAll:
		return p.start(opRequest{Kind: opUpdateAll})
	case a.Kind == plugin.ActionRunCommand && a.Target == cmdUpdateBuckets:
		return p.start(opRequest{Kind: opUpdateBuckets})
	case a.Kind == plugin.ActionCallback && a.Target == callbackOp:
		var req opRequest
		if err := json.Unmarshal(a.Payload, &req); err != nil {
			return plugin.Result{}, fmt.Errorf("scoop: bad operation payload %q", a.Payload)
		}
		// The custom bucket item takes its name and URL as arguments.
		if v, ok := a.Values["name"]; ok && req.Name == "" {
			_ = json.Unmarshal(v, &req.Name)
		}
		if v, ok := a.Values["url"]; ok && req.URL == "" {
			_ = json.Unmarshal(v, &req.URL)
		}
		return p.start(req)
	}
	return plugin.Result{}, fmt.Errorf("scoop: unknown action %s %q", a.Kind, a.Target)
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
