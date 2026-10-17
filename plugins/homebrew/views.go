package homebrew

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

// infoRequest is the Payload of the details view.
type infoRequest struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Title string `json:"title,omitempty"`
}

// View implements plugin.ViewProvider.
func (p *Plugin) View(ctx context.Context, req plugin.ViewRequest) (plugin.View, error) {
	t := p.h.I18n()
	var title string
	switch req.ID {
	case cmdSearch:
		title = t.T("view.search.title")
	case cmdInstalled:
		title = t.T("view.installed.title")
	case cmdOutdated:
		title = t.T("view.outdated.title")
	case viewInfo:
		title = t.T("view.info.title")
	default:
		return plugin.View{}, fmt.Errorf("homebrew: unknown view %q", req.ID)
	}
	if p.brew() == "" {
		return notInstalledView(t, title), nil
	}
	switch req.ID {
	case cmdSearch:
		return p.searchView(ctx, t, req.Query.Text)
	case cmdInstalled:
		return p.installedView(ctx, t), nil
	case cmdOutdated:
		return p.outdatedView(ctx, t), nil
	}
	var ir infoRequest
	if err := json.Unmarshal(req.Payload, &ir); err != nil {
		return plugin.View{}, fmt.Errorf("homebrew: bad details payload %q", req.Payload)
	}
	return p.infoView(ctx, t, ir)
}

// notInstalledItem says that Homebrew is not installed and links to brew.sh.
func notInstalledItem(t plugin.Localizer) plugin.Item {
	return plugin.Item{
		ID:       "not-installed",
		Title:    t.T("notInstalled.title"),
		Subtitle: t.T("notInstalled.subtitle"),
		Icon:     plugin.Icon{Name: "package"},
		Actions: []plugin.Action{
			{ID: "open-brew-sh", Title: t.T("action.openBrewSh"), Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: brewSite}},
			{ID: "copy-install-brew", Title: t.T("action.copyBrewInstall"), Shortcut: "Ctrl+Shift+C",
				Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: brewInstall}},
		},
		Detail: &plugin.Detail{Markdown: t.T("notInstalled.detail") + "\n\n```\n" + brewInstall + "\n```"},
	}
}

// notInstalledView is every view of the plugin while brew is missing.
func notInstalledView(t plugin.Localizer, title string) plugin.View {
	return plugin.View{
		Kind:       plugin.ViewList,
		Title:      title,
		ShowDetail: true,
		Sections:   []plugin.Section{{Items: []plugin.Item{notInstalledItem(t)}}},
	}
}

// updateItem runs `brew update`, which downloads the index.
func updateItem(t plugin.Localizer) plugin.Item {
	return plugin.Item{
		ID:       "update",
		Title:    t.T("op.update"),
		Subtitle: CommandLine([]string{"update"}),
		Icon:     plugin.Icon{Name: "rotate-cw"},
		Actions:  []plugin.Action{opAction("update", t.T("action.update"), "", opRequest{Kind: opUpdate})},
	}
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
	case ix == nil && err != nil:
		v.EmptyText = t.T("view.search.noIndex")
		if !isNotExist(err) {
			v.EmptyText = t.T("view.search.badIndex", "error", err.Error())
		}
		v.Sections = []plugin.Section{{Items: []plugin.Item{updateItem(t)}}}
		return v, nil
	case ix == nil:
		v.EmptyText = t.T("view.indexing")
		return v, nil
	}
	if strings.TrimSpace(text) == "" {
		f, c := ix.Counts()
		v.EmptyText = t.T("view.search.hint", "formulae", f, "casks", c)
		return v, nil
	}
	found := ix.Search(ctx, text, p.maxResults())
	if err := ctx.Err(); err != nil {
		return plugin.View{}, err // superseded by a newer query
	}
	v.EmptyText = t.T("view.search.noResults")
	installed := p.Installed().byKey()
	if len(found) > 0 {
		items := make([]plugin.Item, len(found))
		for i, pkg := range found {
			items[i] = p.packageItem(t, pkg, installed[key(pkg.Kind, pkg.Name)])
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
		return cmp.Or(strings.Compare(strings.ToLower(a.Title()), strings.ToLower(b.Title())), strings.Compare(a.Kind, b.Kind))
	})
	return out
}

// listStatus is the empty text of a view of installed packages that has
// nothing to show yet.
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
func (p *Plugin) refreshItem(t plugin.Localizer, st *installedState) plugin.Item {
	sub := t.T("refresh.subtitle")
	if st != nil && !st.At.IsZero() {
		sub = t.T("refresh.updated", "when", t.RelativeTime(st.At, p.h.Now()))
	}
	if st != nil && st.Err != "" {
		sub = t.T("view.listFailed", "error", st.Err)
	}
	return plugin.Item{
		ID:       "refresh",
		Title:    t.T("refresh.title"),
		Subtitle: sub,
		Icon:     plugin.Icon{Name: "rotate-cw"},
		Actions:  []plugin.Action{refreshAction(t)},
	}
}

func refreshAction(t plugin.Localizer) plugin.Action {
	return plugin.Action{ID: "refresh", Title: t.T("action.refresh"), Shortcut: "Ctrl+R", ReadOnly: true,
		Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackRefresh}}
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
	var outdated, casks, formulae, deps []plugin.Item
	for _, in := range installedSorted(st) {
		it := p.installedItem(t, ix, in)
		switch {
		case in.Outdated:
			outdated = append(outdated, it)
		case in.Kind == KindCask:
			casks = append(casks, it)
		case in.OnRequest:
			formulae = append(formulae, it)
		default:
			deps = append(deps, it)
		}
	}
	for _, s := range []plugin.Section{
		{Title: t.T("section.outdated"), Items: outdated},
		{Title: t.T("section.casks"), Items: casks},
		{Title: t.T("section.formulae"), Items: formulae},
		{Title: t.T("section.dependencies"), Items: deps},
		{Title: t.T("section.list"), Items: []plugin.Item{p.refreshItem(t, st)}},
	} {
		if len(s.Items) > 0 {
			v.Sections = append(v.Sections, s)
		}
	}
	v.EmptyText = t.T("view.installed.empty")
	return v
}

func (p *Plugin) outdatedView(ctx context.Context, t plugin.Localizer) plugin.View {
	st := p.waitInstalled(ctx)
	v := plugin.View{
		Kind:          plugin.ViewList,
		Title:         t.T("view.outdated.title"),
		Placeholder:   t.T("view.outdated.placeholder"),
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
		if !in.Outdated {
			continue
		}
		items = append(items, p.installedItem(t, ix, in))
		if !in.Pinned {
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
		v.EmptyText = t.T("view.outdated.empty")
	}
	v.Sections = append(v.Sections, plugin.Section{Title: t.T("section.list"), Items: []plugin.Item{p.refreshItem(t, st)}})
	return v
}

// ---- items ----

// opAction is an action that starts a brew operation.
func opAction(id, title, shortcut string, req opRequest) plugin.Action {
	return plugin.Action{ID: id, Title: title, Shortcut: shortcut,
		Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackOp, Payload: plugin.MustPayload(req)}}
}

// target is a package as the actions see it.
type target struct {
	Kind, Name, Title, Homepage string
	Inst                        *Installed // nil when not installed
}

// actions are the actions of a package.
func (p *Plugin) actions(t plugin.Localizer, tg target) []plugin.Action {
	var acts []plugin.Action
	req := opRequest{Type: tg.Kind, Name: tg.Name, Title: tg.Title}
	with := func(kind string) opRequest { r := req; r.Kind = kind; return r }
	details := func() {
		acts = append(acts, plugin.Action{ID: "details", Title: t.T("action.details"), Shortcut: "Ctrl+D",
			Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewInfo,
				Payload: plugin.MustPayload(infoRequest{Kind: tg.Kind, Name: tg.Name, Title: tg.Title})}})
	}
	homepage := func() {
		if isURL(tg.Homepage) {
			acts = append(acts, plugin.Action{ID: "homepage", Title: t.T("action.homepage"), Shortcut: "Ctrl+O",
				Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: tg.Homepage}})
		}
	}
	copyName := func() {
		acts = append(acts, plugin.Action{ID: "copy-name", Title: t.T("action.copyName"), Shortcut: "Ctrl+Alt+C",
			Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: tg.Name}})
	}
	if tg.Inst == nil {
		acts = append(acts, opAction("install", t.T("action.install"), "", with(opInstall)))
		details()
		homepage()
		acts = append(acts, plugin.Action{ID: "copy-install", Title: t.T("action.copyInstall"), Shortcut: "Ctrl+Shift+C",
			Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: installCommand(tg.Kind, tg.Name)}})
		copyName()
		return acts
	}
	if tg.Inst.Outdated && !tg.Inst.Pinned {
		acts = append(acts, opAction("upgrade", t.T("action.upgrade"), "Ctrl+U", with(opUpgrade)))
	}
	details()
	homepage()
	copyName()
	acts = append(acts, refreshAction(t))
	un := opAction("uninstall", t.T("action.uninstall"), "Ctrl+X", with(opUninstall))
	un.Style = plugin.StyleDestructive
	acts = append(acts, un)
	if tg.Kind == KindCask {
		zap := opAction("zap", t.T("action.zap"), "Ctrl+Shift+X", with(opZap))
		zap.Style = plugin.StyleDestructive
		acts = append(acts, zap)
	}
	return acts
}

// isURL reports whether s is a web address.
func isURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

// shortVersion is a version as shown at the right edge of a row: casks
// add build numbers and hashes after a comma ("1.34.0,a622c65b…"), which
// the detail pane shows in full.
func shortVersion(v string) string {
	short, _, _ := strings.Cut(v, ",")
	return short
}

// statusAccessory is the accessory of an installed package.
func statusAccessory(t plugin.Localizer, in *Installed) string {
	acc := t.T("accessory.installed", "version", shortVersion(in.Version))
	if in.Outdated && in.Latest != "" {
		acc = t.T("accessory.update", "from", shortVersion(in.Version), "to", shortVersion(in.Latest))
	}
	if in.Pinned {
		acc = t.T("accessory.pinned", "status", acc)
	}
	return acc
}

// kindText is "Formula" or "Cask".
func kindText(t plugin.Localizer, kind string) string {
	if kind == KindCask {
		return t.T("kind.cask")
	}
	return t.T("kind.formula")
}

// subtitle is "Cask · Web browser".
func subtitle(t plugin.Localizer, kind, desc string) string {
	k := kindText(t, kind)
	if desc == "" {
		return k
	}
	return k + " · " + truncate(desc, 100)
}

// keywords are the extra words a package is found by in filtered lists.
func keywords(pkg *Package, name string) []string {
	k := []string{name}
	if pkg != nil {
		k = append(k, pkg.Aliases...)
		k = append(k, pkg.Executables...)
		if len(pkg.Names) > 1 {
			k = append(k, pkg.Names[1:]...)
		}
	}
	return k
}

// packageItem is the row of an index package in search results.
func (p *Plugin) packageItem(t plugin.Localizer, pkg *Package, in *Installed) plugin.Item {
	tg := target{Kind: pkg.Kind, Name: pkg.Name, Title: pkg.Title(), Homepage: pkg.Homepage, Inst: in}
	acc := shortVersion(pkg.Version)
	if in != nil {
		acc = statusAccessory(t, in)
		tg.Name = in.Ref()
	}
	return plugin.Item{
		ID:        pkg.Kind + "/" + pkg.Name,
		Title:     pkg.Title(),
		Subtitle:  subtitle(t, pkg.Kind, pkg.Desc),
		Icon:      plugin.Icon{Name: "package"},
		Keywords:  keywords(pkg, pkg.Name),
		Accessory: acc,
		Actions:   p.actions(t, tg),
		Detail:    p.detail(t, pkg, in),
	}
}

// installedItem is the row of an installed package.
func (p *Plugin) installedItem(t plugin.Localizer, ix *Index, in *Installed) plugin.Item {
	pkg, _ := ix.Package(in.Kind, in.Name)
	if pkg == nil {
		pkg = &Package{Kind: in.Kind, Name: in.Name, Tap: in.Tap, Names: in.Names, Desc: in.Desc, Homepage: in.Homepage, Version: in.Latest}
	}
	tg := target{Kind: in.Kind, Name: in.Ref(), Title: in.Title(), Homepage: cmp.Or(in.Homepage, pkg.Homepage), Inst: in}
	return plugin.Item{
		ID:        in.Kind + "/" + in.Name,
		Title:     in.Title(),
		Subtitle:  subtitle(t, in.Kind, cmp.Or(in.Desc, pkg.Desc)),
		Icon:      plugin.Icon{Name: "package"},
		Keywords:  keywords(pkg, in.Name),
		Accessory: statusAccessory(t, in),
		Actions:   p.actions(t, tg),
		Detail:    p.detail(t, pkg, in),
	}
}

// statusText describes the state flags of a package, "" for none.
func statusText(t plugin.Localizer, pkg *Package, in *Installed) string {
	var s []string
	if pkg.Disabled {
		s = append(s, t.T("status.disabled"))
	} else if pkg.Deprecated {
		s = append(s, t.T("status.deprecated"))
	}
	if in != nil && in.Pinned {
		s = append(s, t.T("status.pinned"))
	}
	if in != nil && in.Kind == KindFormula && !in.OnRequest {
		s = append(s, t.T("status.dependency"))
	}
	if pkg.AutoUpdates || in != nil && in.AutoUpdates {
		s = append(s, t.T("status.autoUpdates"))
	}
	return strings.Join(s, " · ")
}

// detail is the detail pane of a package from what is known without
// running brew: the index entry and the installed package.
func (p *Plugin) detail(t plugin.Localizer, pkg *Package, in *Installed) *plugin.Detail {
	var md strings.Builder
	md.WriteString("## " + pkg.Title() + "\n")
	if pkg.Desc != "" {
		md.WriteString("\n" + pkg.Desc + "\n")
	}
	md.WriteString("\n" + t.T("detail.more") + "\n")
	var meta []plugin.Metadata
	add := func(label, value string) {
		if value != "" {
			meta = append(meta, plugin.Metadata{Label: label, Value: value})
		}
	}
	add(t.T("meta.name"), pkg.Name)
	add(t.T("meta.type"), kindText(t, pkg.Kind))
	if in != nil {
		add(t.T("meta.installed"), in.Version)
		if in.Outdated {
			add(t.T("meta.latest"), cmp.Or(in.Latest, pkg.Version))
		}
	} else {
		add(t.T("meta.version"), pkg.Version)
	}
	add(t.T("meta.status"), statusText(t, pkg, in))
	add(t.T("meta.tap"), pkg.Tap)
	add(t.T("meta.license"), pkg.License)
	add(t.T("meta.aliases"), strings.Join(pkg.Aliases, ", "))
	if len(pkg.Names) > 1 {
		add(t.T("meta.names"), strings.Join(pkg.Names, ", "))
	}
	add(t.T("meta.commands"), strings.Join(pkg.Executables, ", "))
	add(t.T("meta.homepage"), cmp.Or(pkg.Homepage, func() string {
		if in != nil {
			return in.Homepage
		}
		return ""
	}()))
	return &plugin.Detail{Markdown: strings.TrimSpace(md.String()), Metadata: meta}
}

// ---- details view (brew info) ----

// details runs `brew info --json=v2` for the package, caching the result.
func (p *Plugin) details(ctx context.Context, ir infoRequest) (*Details, error) {
	k := key(ir.Kind, ir.Name)
	p.mu.Lock()
	d := p.shows[k]
	p.mu.Unlock()
	if d != nil {
		return d, nil
	}
	if !nameRe.MatchString(ir.Name) || (ir.Kind != KindFormula && ir.Kind != KindCask) {
		return nil, errors.New(p.h.I18n().T("error.badName", "name", ir.Name))
	}
	ctx, cancel := context.WithTimeout(ctx, infoTimeout)
	defer cancel()
	out, err := p.run(ctx, []string{"info", "--json=v2", "--" + ir.Kind, ir.Name})
	if err != nil {
		var ee *ExitError
		if errors.As(err, &ee) {
			return nil, errors.New(cmp.Or(lastLine(ee.Output), ee.Error()))
		}
		return nil, err
	}
	d, err = ParseDetails(out)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if len(p.shows) > 100 {
		clear(p.shows)
	}
	p.shows[k] = d
	p.mu.Unlock()
	return d, nil
}

func (p *Plugin) infoView(ctx context.Context, t plugin.Localizer, ir infoRequest) (plugin.View, error) {
	name := cmp.Or(ir.Title, ir.Name)
	v := plugin.View{Kind: plugin.ViewList, Title: name, ShowDetail: true}
	d, err := p.details(ctx, ir)
	if err != nil {
		if ctx.Err() != nil {
			return plugin.View{}, ctx.Err()
		}
		v.EmptyText = t.T("view.info.failed", "error", err.Error())
		return v, nil
	}
	if d.Kind == KindCask && len(d.Names) > 0 {
		name = d.Names[0]
	}
	v.Title = name
	in := p.Installed().byKey()[key(d.Kind, d.Name)]
	var md strings.Builder
	md.WriteString("## " + name + "\n")
	if d.Desc != "" {
		md.WriteString("\n" + d.Desc + "\n")
	}
	if d.Disabled != "" {
		md.WriteString("\n> " + t.T("info.disabled", "reason", d.Disabled) + "\n")
	} else if d.Deprecated != "" {
		md.WriteString("\n> " + t.T("info.deprecated", "reason", d.Deprecated) + "\n")
	}
	if d.Caveats != "" {
		md.WriteString("\n### " + t.T("info.caveats") + "\n\n```\n" + d.Caveats + "\n```\n")
	}
	if len(d.Deps) > 0 {
		md.WriteString("\n### " + t.T("info.dependencies") + "\n\n" + strings.Join(d.Deps, ", ") + "\n")
	}
	if len(d.BuildDeps) > 0 {
		md.WriteString("\n### " + t.T("info.buildDependencies") + "\n\n" + strings.Join(d.BuildDeps, ", ") + "\n")
	}
	var meta []plugin.Metadata
	add := func(label, value string) {
		if value != "" {
			meta = append(meta, plugin.Metadata{Label: label, Value: value})
		}
	}
	add(t.T("meta.name"), cmp.Or(d.FullName, d.Name))
	add(t.T("meta.type"), kindText(t, d.Kind))
	add(t.T("meta.version"), d.Version)
	add(t.T("meta.installed"), strings.Join(d.Installed, ", "))
	var status []string
	if d.Pinned {
		status = append(status, t.T("status.pinned"))
	}
	if d.KegOnly {
		status = append(status, t.T("status.kegOnly"))
	}
	if d.AutoUpd {
		status = append(status, t.T("status.autoUpdates"))
	}
	add(t.T("meta.status"), strings.Join(status, " · "))
	add(t.T("meta.tap"), d.Tap)
	add(t.T("meta.license"), d.License)
	add(t.T("meta.aliases"), strings.Join(d.Aliases, ", "))
	if len(d.Names) > 1 {
		add(t.T("meta.names"), strings.Join(d.Names, ", "))
	}
	add(t.T("meta.apps"), strings.Join(d.Apps, ", "))
	add(t.T("meta.requires"), d.Requires)
	add(t.T("meta.conflicts"), strings.Join(d.Conflicts, ", "))
	add(t.T("meta.homepage"), d.Homepage)

	if in == nil && len(d.Installed) > 0 {
		// Installed since the last listing.
		in = &Installed{Kind: d.Kind, Name: d.Name, Version: d.Installed[len(d.Installed)-1], Latest: d.Version,
			Outdated: d.Outdated, Pinned: d.Pinned, OnRequest: true}
	}
	ref := d.Name
	if in != nil {
		ref = in.Ref()
	}
	acts := p.actions(t, target{Kind: d.Kind, Name: ref, Title: name, Homepage: d.Homepage, Inst: in})
	acts = slices.DeleteFunc(acts, func(a plugin.Action) bool { return a.ID == "details" })
	acc := shortVersion(d.Version)
	if in != nil {
		acc = statusAccessory(t, in)
	}
	v.Sections = []plugin.Section{{Items: []plugin.Item{{
		ID:        "info/" + d.Kind + "/" + d.Name,
		Title:     name,
		Subtitle:  subtitle(t, d.Kind, d.Desc),
		Icon:      plugin.Icon{Name: "package"},
		Accessory: acc,
		Actions:   acts,
		Detail:    &plugin.Detail{Markdown: strings.TrimSpace(md.String()), Metadata: meta},
	}}}}
	return v, nil
}
