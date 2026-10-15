// Package everything is the builtin.everything plugin: it searches the files
// and folders of the computer through voidtools Everything.
//
// It relies on an Everything installed by the user; when there is none the
// plugin stays disabled (plugin.ErrUnavailable). When Everything is
// installed but not running, the plugin starts it in the background.
package everything

import (
	"context"
	"embed"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/eziq1/golauncher/internal/winapi/everything"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// ID is the plugin ID.
const ID = "builtin.everything"

const (
	cmdSearch     = "search"
	callbackStart = "start"

	prefMaxResults = "maxResults"
	prefMatchPath  = "matchPath"
	prefShowInRoot = "showInRoot"
	// prefHideSystem leaves out the system tier (docs/everything.md §2.2).
	prefHideSystem = "hideSystemFiles"
	// prefExtraLowPriority adds paths or folder names to the system tier.
	prefExtraLowPriority = "extraLowPriorityPaths"

	defaultMax   = 100
	limitMax     = 1000
	rootMax      = 5
	rootMinRunes = 3
	// rootScore ranks file results below apps and commands.
	rootScore    = 0.4
	startTimeout = 5 * time.Second
)

//go:embed locales/*.json
var locales embed.FS

// Backend is the Everything instance the plugin talks to.
type Backend interface {
	// Installed returns Everything's executable and whether it is installed.
	Installed() (exePath string, ok bool)
	Running() bool
	// Start launches Everything in the background.
	Start(exePath string) error
	// Search returns everything.ErrNotRunning when Everything is not running.
	Search(ctx context.Context, q everything.Query) ([]everything.Result, error)
}

// system is the Everything of this machine.
type system struct{}

func (system) Installed() (string, bool) { return everything.Installed() }
func (system) Running() bool             { return everything.Running() }
func (system) Start(exe string) error    { return everything.Start(exe) }
func (system) Search(ctx context.Context, q everything.Query) ([]everything.Result, error) {
	return everything.Search(ctx, q)
}

// Plugin is the Everything file search plugin.
type Plugin struct {
	b    Backend
	home string // user profile, shown as "~"
	base *rules // path tiers, before the extraLowPriorityPaths preference
	pred predictor

	h    plugin.Host
	exe  string
	wg   sync.WaitGroup
	stop chan struct{} // closed by Close
}

var (
	_ plugin.Plugin         = (*Plugin)(nil)
	_ plugin.RootProvider   = (*Plugin)(nil)
	_ plugin.ViewProvider   = (*Plugin)(nil)
	_ plugin.ActionHandler  = (*Plugin)(nil)
	_ plugin.LocaleProvider = (*Plugin)(nil)
)

// New returns the plugin searching with the Everything of this machine.
func New() *Plugin { return NewWithBackend(system{}) }

// NewWithBackend returns the plugin searching with b.
func NewWithBackend(b Backend) *Plugin {
	env := defaultEnv()
	return &Plugin{b: b, home: env.Home, base: newRules(env)}
}

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:            ID,
		Name:          "%plugin.name%",
		Description:   "%plugin.description%",
		Version:       "1.0.0",
		Icon:          plugin.Icon{Name: "file"},
		DefaultLocale: "en",
		Permissions:   []plugin.Permission{plugin.PermExec},
		Commands: []plugin.Command{{
			Name:     cmdSearch,
			Title:    "%command.search.title%",
			Subtitle: "%command.search.subtitle%",
			Mode:     plugin.ModeView,
			Icon:     plugin.Icon{Name: "file"},
			Keywords: []string{
				"%command.search.keyword.files%",
				"%command.search.keyword.find%",
				"%command.search.keyword.everything%",
				"文件",
			},
		}},
		Preferences: []plugin.Preference{
			{Name: prefMaxResults, Type: plugin.PrefNumber, Title: "%pref.maxResults.title%",
				Description: "%pref.maxResults.description%", Default: plugin.MustPayload(defaultMax)},
			{Name: prefMatchPath, Type: plugin.PrefBool, Title: "%pref.matchPath.title%",
				Description: "%pref.matchPath.description%", Default: plugin.MustPayload(false)},
			{Name: prefShowInRoot, Type: plugin.PrefBool, Title: "%pref.showInRoot.title%",
				Description: "%pref.showInRoot.description%", Default: plugin.MustPayload(false)},
			{Name: prefHideSystem, Type: plugin.PrefBool, Title: "%pref.hideSystemFiles.title%",
				Description: "%pref.hideSystemFiles.description%", Default: plugin.MustPayload(false)},
			{Name: prefExtraLowPriority, Type: plugin.PrefStringList, Title: "%pref.extraLowPriorityPaths.title%",
				Description: "%pref.extraLowPriorityPaths.description%", Default: plugin.MustPayload([]string{})},
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

// Init implements plugin.Plugin. Without Everything the plugin is
// unavailable; when Everything is installed but not running it is started
// in the background.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	exe, ok := p.b.Installed()
	if !ok {
		return fmt.Errorf("%s: %w", h.I18n().T("unavailable.notInstalled"), plugin.ErrUnavailable)
	}
	p.h, p.exe, p.stop = h, exe, make(chan struct{})
	if !p.b.Running() {
		p.wg.Add(1)
		h.Go(func() {
			defer p.wg.Done()
			if err := p.b.Start(exe); err != nil {
				h.Logger().Warn("everything: starting Everything", "exe", exe, "err", err)
				return
			}
			// An open search view shows "not running" until it is pulled again.
			if p.waitRunning() {
				h.Invalidate(cmdSearch)
			}
		})
	}
	return nil
}

// waitRunning waits until Everything runs, for at most startTimeout or until
// the plugin closes, and reports whether it runs.
func (p *Plugin) waitRunning() bool {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(startTimeout)
	for !p.b.Running() {
		select {
		case <-p.stop:
			return false
		case <-deadline:
			return false
		case <-tick.C:
		}
	}
	return true
}

// Close implements plugin.Plugin.
func (p *Plugin) Close() error {
	if p.stop != nil {
		select {
		case <-p.stop: // closed before
		default:
			close(p.stop)
		}
	}
	p.wg.Wait()
	return nil
}

// maxResults is the maxResults preference, clamped to [1, limitMax].
func (p *Plugin) maxResults() int {
	n := p.h.Preferences().Int(prefMaxResults)
	if n <= 0 {
		return defaultMax
	}
	return min(n, limitMax)
}

// RootItems implements plugin.RootProvider. Unless the showInRoot preference
// is set it returns nothing; otherwise a few files for queries of at least
// three characters, scored below apps and commands. Only the user and normal
// tiers are offered here.
func (p *Plugin) RootItems(ctx context.Context, q plugin.Query) ([]plugin.Item, error) {
	text := strings.TrimSpace(q.Text)
	if !p.h.Preferences().Bool(prefShowInRoot) || utf8.RuneCountInString(text) < rootMinRunes {
		return nil, nil
	}
	res, err := p.fetchRoot(ctx, text, p.options(rootMax))
	if errors.Is(err, everything.ErrNotRunning) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	l := p.h.I18n()
	group := l.T("group")
	items := make([]plugin.Item, len(res))
	for i, r := range res {
		items[i] = p.item(l, r.Result)
		items[i].Score = rootScore
		items[i].Group = group
		items[i].Detail = nil
	}
	return items, nil
}

// View implements plugin.ViewProvider for the "search" view.
func (p *Plugin) View(ctx context.Context, req plugin.ViewRequest) (plugin.View, error) {
	if req.ID != cmdSearch {
		return plugin.View{}, fmt.Errorf("everything: unknown view %q", req.ID)
	}
	l := p.h.I18n()
	v := plugin.View{
		Kind:        plugin.ViewList,
		Title:       l.T("view.title"),
		Placeholder: l.T("view.placeholder"),
		ShowDetail:  true,
		EmptyText:   l.T("view.hint"),
	}
	text := strings.TrimSpace(req.Query.Text)
	if text == "" {
		if !p.b.Running() {
			v.Sections = []plugin.Section{{Items: []plugin.Item{p.notRunningItem(l)}}}
		}
		return v, nil
	}
	res, err := p.fetch(ctx, text, p.options(p.maxResults()))
	switch {
	case errors.Is(err, everything.ErrNotRunning):
		v.Sections = []plugin.Section{{Items: []plugin.Item{p.notRunningItem(l)}}}
		return v, nil
	case err != nil && ctx.Err() != nil:
		return plugin.View{}, err // superseded by a newer query
	case err != nil:
		p.h.Logger().Warn("everything: search", "err", err)
		v.EmptyText = l.T("view.error", "error", err.Error())
		return v, nil
	}
	v.EmptyText = l.T("view.noResults")
	items := make([]plugin.Item, len(res))
	for i, r := range res {
		items[i] = p.item(l, r.Result)
	}
	v.Sections = []plugin.Section{{Items: items}}
	return v, nil
}

// notRunningItem offers to start Everything.
func (p *Plugin) notRunningItem(l plugin.Localizer) plugin.Item {
	return plugin.Item{
		ID:       "not-running",
		Title:    l.T("notRunning.title"),
		Subtitle: l.T("notRunning.subtitle"),
		Icon:     plugin.Icon{Name: "file"},
		Actions: []plugin.Action{{
			ID: "start", Title: l.T("action.start"),
			Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackStart},
		}},
	}
}

// item is the list row of r.
func (p *Plugin) item(l plugin.Localizer, r everything.Result) plugin.Item {
	now := p.h.Now()
	it := plugin.Item{
		ID:       r.Path,
		Title:    r.Name,
		Subtitle: p.shorten(parentDir(r.Path)),
		Icon:     plugin.Icon{Name: "file"},
		Actions:  actions(l, r),
	}
	kind := l.T("kind.file")
	if r.IsFolder {
		it.Icon = plugin.Icon{Name: "folder"}
		kind = l.T("kind.folder")
	}
	meta := []plugin.Metadata{
		{Label: l.T("meta.path"), Value: r.Path},
		{Label: l.T("meta.kind"), Value: kind},
	}
	if r.Size >= 0 {
		meta = append(meta, plugin.Metadata{Label: l.T("meta.size"), Value: formatSize(l, r.Size)})
	}
	if !r.Modified.IsZero() {
		rel := l.RelativeTime(r.Modified, now)
		it.Accessory = rel
		meta = append(meta, plugin.Metadata{Label: l.T("meta.modified"),
			Value: l.T("meta.modifiedValue", "date", r.Modified.In(now.Location()).Format("2006-01-02 15:04"), "relative", rel)})
	}
	it.Detail = &plugin.Detail{Metadata: meta}
	return it
}

// actions are the actions of a result; opening it is the primary one.
func actions(l plugin.Localizer, r everything.Result) []plugin.Action {
	return []plugin.Action{
		{ID: "open", Title: l.T("action.open"),
			Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: r.Path}},
		{ID: "reveal", Title: l.T("action.reveal"), Shortcut: "Ctrl+Shift+F",
			Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: r.Path,
				Payload: plugin.MustPayload(plugin.OpenOptions{Reveal: true})}},
		{ID: "copy-path", Title: l.T("action.copyPath"), Shortcut: "Ctrl+Shift+C",
			Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: r.Path}},
		{ID: "copy-file", Title: l.T("action.copyFile"), Shortcut: "Ctrl+Alt+C",
			Do: plugin.ActionDo{Kind: plugin.ActionCopyFiles, Payload: plugin.MustPayload([]string{r.Path})}},
	}
}

// HandleAction implements plugin.ActionHandler: the "start" callback starts
// Everything and waits briefly for it to come up.
func (p *Plugin) HandleAction(ctx context.Context, a plugin.ActionDo) (plugin.Result, error) {
	if a.Kind != plugin.ActionCallback || a.Target != callbackStart {
		return plugin.Result{}, fmt.Errorf("everything: unknown action %s %q", a.Kind, a.Target)
	}
	l := p.h.I18n()
	failed := func(err error) (plugin.Result, error) {
		return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastFailure, Title: l.T("toast.startFailed"), Message: err.Error()}}, nil
	}
	if !p.b.Running() {
		if err := p.b.Start(p.exe); err != nil {
			return failed(err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for !p.b.Running() {
		select {
		case <-ctx.Done():
			return failed(ctx.Err())
		case <-tick.C:
		}
	}
	return plugin.Result{Refresh: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("toast.started")}}, nil
}

// parentDir is the folder of a Windows path, or "" for a drive root.
func parentDir(path string) string {
	path = strings.TrimRight(path, `\/`)
	i := strings.LastIndexAny(path, `\/`)
	if i < 0 {
		return ""
	}
	if i == 2 && path[1] == ':' { // C:\x
		return path[:3]
	}
	return path[:i]
}

// shorten replaces the user profile at the start of dir with "~".
func (p *Plugin) shorten(dir string) string {
	h := strings.TrimRight(p.home, `\/`)
	if h == "" || len(dir) < len(h) || !strings.EqualFold(dir[:len(h)], h) {
		return dir
	}
	if rest := dir[len(h):]; rest == "" || rest[0] == '\\' || rest[0] == '/' {
		return "~" + rest
	}
	return dir
}

// formatSize formats a byte count for the current locale.
func formatSize(l plugin.Localizer, n int64) string {
	const k = 1024
	round := func(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }
	switch {
	case n < k:
		return l.N("size.bytes", int(n))
	case n < k*k:
		return l.T("size.kb", "size", l.Number(round(float64(n)/k)))
	case n < k*k*k:
		return l.T("size.mb", "size", l.Number(round(float64(n)/(k*k))))
	default:
		return l.T("size.gb", "size", l.Number(round(float64(n)/(k*k*k))))
	}
}

// FixtureBackend returns a Backend searching the JSON array of
// everything.Result in the file path, for glctl --fixtures and tests. It
// matches like Everything: every space-separated term must occur in the name
// (or the full path with matchPath), ignoring case. An empty path is an
// index with no files.
func FixtureBackend(path string) Backend { return fixture{path: path} }

type fixture struct{ path string }

func (fixture) Installed() (string, bool) { return "Everything.exe", true }
func (fixture) Running() bool             { return true }
func (fixture) Start(string) error        { return nil }

func (f fixture) Search(ctx context.Context, q everything.Query) ([]everything.Result, error) {
	if f.path == "" {
		return nil, ctx.Err()
	}
	b, err := os.ReadFile(f.path)
	if err != nil {
		return nil, err
	}
	var all []everything.Result
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, fmt.Errorf("everything: fixture %s: %w", f.path, err)
	}
	terms := splitTerms(strings.ToLower(q.Text), ' ')
	var out []everything.Result
	for _, r := range all {
		if matchAll(r, terms, q.MatchPath) {
			out = append(out, r)
		}
	}
	out = out[min(max(q.Offset, 0), len(out)):]
	if n := q.Max; n > 0 && len(out) > n {
		out = out[:n]
	}
	return out, ctx.Err()
}

// matchAll reports whether r matches every term of the fixture syntax, a
// subset of Everything's: "!" negates a term, <a|b> matches either, quotes
// group, and a term with a backslash matches the full path.
func matchAll(r everything.Result, terms []string, matchPath bool) bool {
	for _, t := range terms {
		neg := strings.HasPrefix(t, "!")
		t = strings.TrimPrefix(t, "!")
		var ok bool
		if strings.HasPrefix(t, "<") && strings.HasSuffix(t, ">") {
			for _, alt := range splitTerms(t[1:len(t)-1], '|') {
				if matchTerm(r, alt, matchPath) {
					ok = true
					break
				}
			}
		} else {
			ok = matchTerm(r, t, matchPath)
		}
		if ok == neg {
			return false
		}
	}
	return true
}

func matchTerm(r everything.Result, t string, matchPath bool) bool {
	t = strings.ReplaceAll(t, `"`, "")
	hay := r.Name
	if matchPath || strings.Contains(t, `\`) {
		hay = r.Path
	}
	return strings.Contains(strings.ToLower(hay), t)
}

// splitTerms splits s at sep outside quotes and <> groups, dropping empty
// terms.
func splitTerms(s string, sep rune) []string {
	var out []string
	var cur strings.Builder
	quoted, depth := false, 0
	for _, c := range s {
		switch {
		case c == '"':
			quoted = !quoted
		case c == '<' && !quoted:
			depth++
		case c == '>' && !quoted && depth > 0:
			depth--
		case c == sep && !quoted && depth == 0:
			if cur.Len() > 0 {
				out = append(out, cur.String())
			}
			cur.Reset()
			continue
		}
		cur.WriteRune(c)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
