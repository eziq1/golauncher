package winget

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// showRequest is the Payload of the details view.
type showRequest struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Name   string `json:"name,omitempty"`
}

// View implements plugin.ViewProvider.
func (p *Plugin) View(ctx context.Context, req plugin.ViewRequest) (plugin.View, error) {
	t := p.h.I18n()
	switch req.ID {
	case cmdSearch:
		return p.searchView(ctx, t, req.Query.Text)
	case cmdInstalled:
		return p.installedView(ctx, t), nil
	case cmdUpdates:
		return p.updatesView(ctx, t), nil
	case cmdSources:
		return p.sourcesView(t), nil
	case viewShow:
		var sr showRequest
		if err := json.Unmarshal(req.Payload, &sr); err != nil {
			return plugin.View{}, fmt.Errorf("winget: bad details payload %q", req.Payload)
		}
		return p.showView(ctx, t, sr)
	}
	return plugin.View{}, fmt.Errorf("winget: unknown view %q", req.ID)
}

func (p *Plugin) searchView(ctx context.Context, t plugin.Localizer, text string) (plugin.View, error) {
	v := plugin.View{
		Kind:        plugin.ViewList,
		Title:       t.T("view.search.title"),
		Placeholder: t.T("view.search.placeholder"),
		ShowDetail:  true,
	}
	p.ensureInstalled() // to mark installed packages
	ix, err := p.waitIndex(ctx)
	switch {
	case ix == nil && ctx.Err() != nil:
		return plugin.View{}, ctx.Err()
	case ix == nil:
		v.EmptyText = t.T("view.indexing")
		return v, nil
	case len(ix.Catalogs) == 0:
		v.EmptyText = t.T("view.search.noIndex")
		if err != nil && errors.Is(err, errSchema) {
			v.EmptyText = t.T("view.search.badIndex")
		}
		return v, nil
	}
	if strings.TrimSpace(text) == "" {
		v.EmptyText = t.T("view.search.hint", "packages", ix.PackageCount())
		return v, nil
	}
	found := ix.Search(ctx, text, p.maxResults())
	if err := ctx.Err(); err != nil {
		return plugin.View{}, err // superseded by a newer query
	}
	v.EmptyText = t.T("view.search.noResults")
	installed := p.Installed().byID()
	if len(found) > 0 {
		items := make([]plugin.Item, len(found))
		for i, pkg := range found {
			items[i] = p.packageItem(t, pkg, installed[strings.ToLower(pkg.ID)])
		}
		v.Sections = append(v.Sections, plugin.Section{Items: items})
	}
	return v, nil
}

// installedSorted returns the installed packages sorted by name.
func installedSorted(st *installedState) []*Installed {
	if st == nil {
		return nil
	}
	out := make([]*Installed, len(st.Items))
	for i := range st.Items {
		out[i] = &st.Items[i]
	}
	slices.SortStableFunc(out, func(a, b *Installed) int {
		return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.ID, b.ID))
	})
	return out
}

// listStatus is the empty text or warning of a view of installed packages.
func listStatus(t plugin.Localizer, st *installedState) string {
	switch {
	case st == nil:
		return t.T("view.listing")
	case st.Err != "" && len(st.Items) == 0:
		return t.T("view.listFailed", "error", st.Err)
	}
	return ""
}

// refreshItem lists the installed packages again.
func refreshItem(t plugin.Localizer, st *installedState, now func() string) plugin.Item {
	sub := t.T("refresh.subtitle")
	if st != nil && !st.At.IsZero() {
		sub = t.T("refresh.updated", "when", now())
	}
	if st != nil && st.Err != "" {
		sub = t.T("view.listFailed", "error", st.Err)
	}
	return plugin.Item{
		ID:       "refresh",
		Title:    t.T("refresh.title"),
		Subtitle: sub,
		Icon:     plugin.Icon{Name: "rotate-cw"},
		Actions: []plugin.Action{{ID: "refresh", Title: t.T("action.refresh"), Shortcut: "Ctrl+R", ReadOnly: true,
			Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackRefresh}}},
	}
}

func (p *Plugin) installedView(ctx context.Context, t plugin.Localizer) plugin.View {
	st := p.waitInstalled(ctx)
	v := plugin.View{
		Kind:          plugin.ViewList,
		Title:         t.T("view.installed.title"),
		Placeholder:   t.T("view.installed.placeholder"),
		ShowDetail:    true,
		FilterLocally: true,
	}
	if s := listStatus(t, st); s != "" {
		v.EmptyText = s
		return v
	}
	ix := p.Index()
	var updates, sourced, other []plugin.Item
	for _, in := range installedSorted(st) {
		it := p.installedItem(t, ix, in)
		switch {
		case in.Available != "" && in.Source != "":
			updates = append(updates, it)
		case in.Source != "":
			sourced = append(sourced, it)
		default:
			other = append(other, it)
		}
	}
	for _, s := range []plugin.Section{
		{Title: t.T("section.updates"), Items: updates},
		{Title: t.T("section.installed"), Items: sourced},
		{Title: t.T("section.other"), Items: other},
		{Title: t.T("section.list"), Items: []plugin.Item{refreshItem(t, st, func() string { return t.RelativeTime(st.At, p.h.Now()) })}},
	} {
		if len(s.Items) > 0 {
			v.Sections = append(v.Sections, s)
		}
	}
	v.EmptyText = t.T("view.installed.empty")
	return v
}

func (p *Plugin) updatesView(ctx context.Context, t plugin.Localizer) plugin.View {
	st := p.waitInstalled(ctx)
	v := plugin.View{
		Kind:          plugin.ViewList,
		Title:         t.T("view.updates.title"),
		Placeholder:   t.T("view.updates.placeholder"),
		ShowDetail:    true,
		FilterLocally: true,
	}
	if s := listStatus(t, st); s != "" {
		v.EmptyText = s
		return v
	}
	ix := p.Index()
	var items []plugin.Item
	upgradable := 0
	for _, in := range installedSorted(st) {
		if in.Available == "" || in.Source == "" {
			continue
		}
		items = append(items, p.installedItem(t, ix, in))
		if p.pin(in.ID) == nil {
			upgradable++
		}
	}
	if upgradable > 0 {
		act := opAction("upgrade-all", t.T("action.upgradeAll"), "", opRequest{Kind: opUpgradeAll})
		act.Style = plugin.StyleDestructive
		all := plugin.Item{
			ID:       "upgrade-all",
			Title:    t.N("upgradeAll.title", upgradable),
			Subtitle: t.T("upgradeAll.subtitle"),
			Icon:     plugin.Icon{Name: "package"},
			Actions:  []plugin.Action{act},
		}
		items = append([]plugin.Item{all}, items...)
	}
	if len(items) > 0 {
		v.Sections = append(v.Sections, plugin.Section{Items: items})
	} else {
		v.EmptyText = t.T("view.updates.empty")
	}
	v.Sections = append(v.Sections, plugin.Section{Title: t.T("section.list"),
		Items: []plugin.Item{refreshItem(t, st, func() string { return t.RelativeTime(st.At, p.h.Now()) })}})
	return v
}

func (p *Plugin) sourcesView(t plugin.Localizer) plugin.View {
	srcs := p.loadSources()
	ix := p.Index()
	v := plugin.View{
		Kind:          plugin.ViewList,
		Title:         t.T("view.sources.title"),
		Placeholder:   t.T("view.sources.placeholder"),
		ShowDetail:    true,
		FilterLocally: true,
	}
	var items []plugin.Item
	for _, s := range srcs {
		meta := []plugin.Metadata{
			{Label: t.T("meta.url"), Value: s.Arg},
			{Label: t.T("meta.type"), Value: s.Type},
		}
		acc := ""
		if ix != nil {
			if i := slices.IndexFunc(ix.Catalogs, func(c *Catalog) bool { return c.Source == s.Name }); i >= 0 {
				acc = t.N("packages.count", len(ix.Catalogs[i].Packages))
				meta = append(meta, plugin.Metadata{Label: t.T("meta.packages"), Value: acc})
			}
		}
		if s.Explicit {
			meta = append(meta, plugin.Metadata{Label: t.T("meta.status"), Value: t.T("meta.explicit")})
			acc = t.T("accessory.explicit")
		}
		acts := []plugin.Action{
			opAction("update-source", t.T("action.updateSource"), "", opRequest{Kind: opSourceUpdate, Source: s.Name}),
			opAction("update-sources", t.T("action.updateSources"), "Ctrl+U", opRequest{Kind: opSourceUpdate}),
		}
		acts = append(acts, plugin.Action{ID: "copy-url", Title: t.T("action.copyURL"), Shortcut: "Ctrl+Shift+C",
			Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: s.Arg}})
		items = append(items, plugin.Item{
			ID:        "source/" + s.Name,
			Title:     s.Name,
			Subtitle:  s.Arg,
			Icon:      plugin.Icon{Name: "globe"},
			Accessory: acc,
			Detail:    &plugin.Detail{Metadata: meta},
			Actions:   acts,
		})
	}
	if len(items) > 0 {
		v.Sections = append(v.Sections, plugin.Section{Items: items})
	}
	v.EmptyText = t.T("view.sources.empty")
	return v
}

// ---- items ----

// isURL reports whether s is a web address.
func isURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

// opAction is an action that starts a winget operation.
func opAction(id, title, shortcut string, req opRequest) plugin.Action {
	return plugin.Action{ID: id, Title: title, Shortcut: shortcut,
		Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackOp, Payload: plugin.MustPayload(req)}}
}

// target is a package as the actions see it.
type target struct {
	ID, Name, Source string
	Inst             *Installed // nil when not installed
	Pin              *Pin
}

// manifestURL is the folder of a package's manifests in the winget
// community repository.
func manifestURL(id string) string {
	return "https://github.com/microsoft/winget-pkgs/tree/master/manifests/" +
		strings.ToLower(id[:1]) + "/" + strings.ReplaceAll(id, ".", "/")
}

// actions are the actions of a package.
func (p *Plugin) actions(t plugin.Localizer, tg target) []plugin.Action {
	var acts []plugin.Action
	req := opRequest{ID: tg.ID, Name: tg.Name, Source: tg.Source}
	with := func(kind string) opRequest { r := req; r.Kind = kind; return r }
	details := func() {
		if tg.Source != "" {
			acts = append(acts, plugin.Action{ID: "details", Title: t.T("action.details"), Shortcut: "Ctrl+D",
				Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewShow,
					Payload: plugin.MustPayload(showRequest{ID: tg.ID, Source: tg.Source, Name: tg.Name})}})
		}
	}
	elevated := func(kind, title string) {
		if a, ok := p.elevatedAction(t, kind+"-admin", title, with(kind)); ok {
			acts = append(acts, a)
		}
	}
	manifest := func() {
		if strings.EqualFold(tg.Source, "winget") && tg.ID != "" {
			acts = append(acts, plugin.Action{ID: "manifest", Title: t.T("action.manifest"), Shortcut: "Ctrl+Shift+M",
				Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: manifestURL(tg.ID)}})
		}
	}
	copyID := func() {
		acts = append(acts, plugin.Action{ID: "copy-id", Title: t.T("action.copyID"), Shortcut: "Ctrl+Alt+C",
			Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: tg.ID}})
	}
	copyInstall := func() {
		if tg.Source != "" {
			acts = append(acts, plugin.Action{ID: "copy-install", Title: t.T("action.copyInstall"), Shortcut: "Ctrl+Shift+C",
				Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: CommandLine([]string{"install", "--id", tg.ID, "--exact", "--source", tg.Source})}})
		}
	}
	if tg.Inst == nil {
		acts = append(acts, opAction("install", t.T("action.install"), "", with(opInstall)))
		details()
		elevated(opInstall, t.T("action.installAdmin"))
		manifest()
		copyID()
		copyInstall()
		return acts
	}
	upgradable := tg.Inst.Available != "" && tg.Source != ""
	if upgradable {
		acts = append(acts, opAction("upgrade", t.T("action.upgrade"), "Ctrl+U", with(opUpgrade)))
	}
	if tg.Source == "" {
		copyID()
	}
	details()
	if upgradable {
		elevated(opUpgrade, t.T("action.upgradeAdmin"))
	}
	if tg.Source != "" {
		if tg.Pin != nil {
			acts = append(acts, opAction("unpin", t.T("action.unpin"), "Ctrl+Shift+H", with(opUnpin)))
		} else {
			acts = append(acts, opAction("pin", t.T("action.pin"), "Ctrl+Shift+H", with(opPin)))
		}
		manifest()
		copyID()
		copyInstall()
	}
	acts = append(acts, plugin.Action{ID: "refresh", Title: t.T("action.refresh"), Shortcut: "Ctrl+R", ReadOnly: true,
		Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackRefresh}})
	un := opAction("uninstall", t.T("action.uninstall"), "Ctrl+X", with(opUninstall))
	un.Style = plugin.StyleDestructive
	return append(acts, un)
}

// pinText describes a pin.
func pinText(t plugin.Localizer, pin *Pin) string {
	switch {
	case pin.Type == pinBlocking:
		return t.T("pin.blocking")
	case pin.Type == pinGating && pin.Version != "":
		return t.T("pin.gating", "version", pin.Version)
	}
	return t.T("pin.pinning")
}

// statusAccessory is the accessory of an installed package.
func statusAccessory(t plugin.Localizer, in *Installed, pin *Pin) string {
	acc := t.T("accessory.installed", "version", in.Version)
	if in.Available != "" && in.Source != "" {
		acc = t.T("accessory.update", "from", in.Version, "to", in.Available)
	}
	if pin != nil {
		acc = t.T("accessory.pinned", "status", acc)
	}
	return acc
}

// packageItem is the row of an index package in search results.
func (p *Plugin) packageItem(t plugin.Localizer, pkg *Package, in *Installed) plugin.Item {
	if in != nil && !strings.EqualFold(in.Source, pkg.Source) {
		in = nil // the same ID from another source
	}
	tg := target{ID: pkg.ID, Name: pkg.Name, Source: pkg.Source, Inst: in}
	acc := t.T("accessory.available", "version", pkg.Version)
	if in != nil {
		tg.Pin = p.pin(pkg.ID)
		acc = statusAccessory(t, in, tg.Pin)
	}
	return plugin.Item{
		ID:        "pkg/" + pkg.Source + "/" + pkg.ID,
		Title:     pkg.Name,
		Subtitle:  pkg.ID,
		Icon:      plugin.Icon{Name: "package"},
		Keywords:  keywords(pkg),
		Accessory: acc,
		Actions:   p.actions(t, tg),
		Detail:    p.detail(t, pkg, in, tg.Pin),
	}
}

// installedItem is the row of an installed package.
func (p *Plugin) installedItem(t plugin.Localizer, ix *Index, in *Installed) plugin.Item {
	pkg, _ := ix.Package(in.Source, in.ID)
	tg := target{ID: in.ID, Name: in.Name, Source: in.Source, Inst: in}
	if in.Source != "" {
		tg.Pin = p.pin(in.ID)
	}
	it := plugin.Item{
		ID:        "app/" + in.ID,
		Title:     in.Name,
		Subtitle:  in.ID,
		Icon:      plugin.Icon{Name: "package"},
		Accessory: statusAccessory(t, in, tg.Pin),
		Actions:   p.actions(t, tg),
	}
	if pkg != nil {
		it.Keywords = keywords(pkg)
	}
	if pkg == nil {
		pkg = &Package{ID: in.ID, Name: in.Name, Source: in.Source}
	}
	it.Detail = p.detail(t, pkg, in, tg.Pin)
	return it
}

// keywords are the extra words a package is found by in filtered lists.
func keywords(pkg *Package) []string {
	var k []string
	if pkg.Moniker != "" {
		k = append(k, pkg.Moniker)
	}
	return append(k, pkg.Commands...)
}

// maxTags caps the tags shown in the detail pane.
const maxTags = 12

// detail is the detail pane of a package from what is known without
// running winget: the index entry and the installed package.
func (p *Plugin) detail(t plugin.Localizer, pkg *Package, in *Installed, pin *Pin) *plugin.Detail {
	var md strings.Builder
	md.WriteString("## " + pkg.Name + "\n")
	if pkg.Source != "" {
		md.WriteString("\n" + t.T("detail.more") + "\n")
	} else {
		md.WriteString("\n" + t.T("detail.local") + "\n")
	}
	var meta []plugin.Metadata
	add := func(label, value string) {
		if value != "" {
			meta = append(meta, plugin.Metadata{Label: label, Value: value})
		}
	}
	add(t.T("meta.id"), pkg.ID)
	if in != nil {
		add(t.T("meta.installed"), in.Version)
		if in.Source != "" {
			add(t.T("meta.available"), in.Available)
		}
		if pin != nil {
			add(t.T("meta.status"), pinText(t, pin))
		}
		if pkg.Version != "" && pkg.Version != in.Available && pkg.Version != in.Version {
			add(t.T("meta.latest"), pkg.Version)
		}
	} else {
		add(t.T("meta.version"), pkg.Version)
	}
	add(t.T("meta.source"), pkg.Source)
	add(t.T("meta.moniker"), pkg.Moniker)
	add(t.T("meta.commands"), strings.Join(pkg.Commands, ", "))
	tags := pkg.Tags
	if len(tags) > maxTags {
		tags = tags[:maxTags]
	}
	add(t.T("meta.tags"), strings.Join(tags, ", "))
	return &plugin.Detail{Markdown: strings.TrimSpace(md.String()), Metadata: meta}
}

// ---- details view (winget show) ----

// details runs `winget show` for the package, caching the result.
func (p *Plugin) details(ctx context.Context, sr showRequest, version string) (*Details, error) {
	key := strings.ToLower(sr.Source + "/" + sr.ID + "/" + version)
	p.mu.Lock()
	d := p.shows[key]
	p.mu.Unlock()
	if d != nil {
		return d, nil
	}
	if !idRe.MatchString(sr.ID) || !sourceRe.MatchString(sr.Source) {
		return nil, errors.New(p.h.I18n().T("error.badID", "id", sr.ID))
	}
	ctx, cancel := context.WithTimeout(ctx, showTimeout)
	defer cancel()
	out, err := p.opts.Run(ctx, []string{"show", "--id", sr.ID, "--exact", "--source", sr.Source,
		"--accept-source-agreements", "--disable-interactivity"})
	if err != nil {
		var ee *ExitError
		if errors.As(err, &ee) {
			return nil, errors.New(lastLine(ee.Output))
		}
		return nil, err
	}
	d, err = parseShow(out)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if len(p.shows) > 100 {
		clear(p.shows)
	}
	p.shows[key] = d
	p.mu.Unlock()
	return d, nil
}

// longField reports whether a show field goes into the Markdown body
// rather than the metadata: text of several lines or a long one.
func longField(f Field) bool {
	return strings.Contains(f.Value, "\n") || len([]rune(f.Value)) > 80 || f.Value == ""
}

// markdownText renders a multi-line show value: lines become paragraphs,
// "- " list items stay list items.
func markdownText(s string) string {
	var b strings.Builder
	prevList := false
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		list := strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ")
		if b.Len() > 0 {
			if list && prevList {
				b.WriteString("\n")
			} else {
				b.WriteString("\n\n")
			}
		}
		b.WriteString(line)
		prevList = list
	}
	return b.String()
}

func (p *Plugin) showView(ctx context.Context, t plugin.Localizer, sr showRequest) (plugin.View, error) {
	ix, _ := p.waitIndex(ctx)
	pkg, _ := ix.Package(sr.Source, sr.ID)
	version := ""
	name := cmp.Or(sr.Name, sr.ID)
	if pkg != nil {
		version, name = pkg.Version, pkg.Name
	}
	v := plugin.View{Kind: plugin.ViewList, Title: name, ShowDetail: true}
	d, err := p.details(ctx, sr, version)
	if err != nil {
		if ctx.Err() != nil {
			return plugin.View{}, ctx.Err()
		}
		v.EmptyText = t.T("view.show.failed", "error", err.Error())
		return v, nil
	}
	name = titleName(d.Title, name)
	v.Title = name
	in := p.Installed().byID()[strings.ToLower(sr.ID)]
	if in != nil && !strings.EqualFold(in.Source, sr.Source) {
		in = nil
	}
	tg := target{ID: sr.ID, Name: name, Source: sr.Source, Inst: in}
	if in != nil {
		tg.Pin = p.pin(sr.ID)
	}
	var md strings.Builder
	md.WriteString("## " + name + "\n")
	var meta []plugin.Metadata
	meta = append(meta, plugin.Metadata{Label: t.T("meta.id"), Value: sr.ID})
	if in != nil {
		meta = append(meta, plugin.Metadata{Label: t.T("meta.installed"), Value: in.Version})
		if pin := tg.Pin; pin != nil {
			meta = append(meta, plugin.Metadata{Label: t.T("meta.status"), Value: pinText(t, pin)})
		}
	}
	var urls []plugin.Action
	homepage := -1
	for _, f := range d.Fields {
		if longField(f) {
			if f.Value != "" {
				md.WriteString("\n### " + f.Label + "\n\n" + markdownText(f.Value) + "\n")
			}
			continue
		}
		meta = append(meta, plugin.Metadata{Label: f.Label, Value: f.Value})
		if isURL(f.Value) {
			if homepage < 0 && strings.EqualFold(f.Label, "Homepage") {
				homepage = len(urls)
			}
			urls = append(urls, plugin.Action{ID: "open-" + strings.ToLower(strings.ReplaceAll(f.Label, " ", "-")),
				Title: t.T("action.openField", "field", f.Label), Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: f.Value}})
		}
	}
	meta = append(meta, plugin.Metadata{Label: t.T("meta.source"), Value: sr.Source})
	// The homepage (or, in other languages, the first web address) comes
	// first, as Ctrl+O.
	if len(urls) > 0 {
		if homepage < 0 {
			homepage = 0
		}
		home := urls[homepage]
		home.ID, home.Title, home.Shortcut = "homepage", t.T("action.homepage"), "Ctrl+O"
		urls = append([]plugin.Action{home}, slices.Delete(urls, homepage, homepage+1)...)
	}
	acts := p.actions(t, tg)
	// Show Details is this view; the web addresses follow the main action.
	acts = slices.DeleteFunc(acts, func(a plugin.Action) bool { return a.ID == "details" })
	pos := min(1, len(acts))
	acts = slices.Insert(acts, pos, urls...)
	v.Sections = []plugin.Section{{Items: []plugin.Item{{
		ID:        "show/" + sr.Source + "/" + sr.ID,
		Title:     name,
		Subtitle:  sr.ID,
		Icon:      plugin.Icon{Name: "package"},
		Accessory: cmp.Or(d.Get("Version"), version),
		Actions:   acts,
		Detail:    &plugin.Detail{Markdown: strings.TrimSpace(md.String()), Metadata: meta},
	}}}}
	return v, nil
}
