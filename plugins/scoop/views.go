package scoop

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// View implements plugin.ViewProvider.
func (p *Plugin) View(ctx context.Context, req plugin.ViewRequest) (plugin.View, error) {
	t := p.h.I18n()
	if req.ID == viewOutput {
		return p.outputView(t, req), nil
	}
	ix := p.waitIndex(ctx)
	if ix == nil {
		if err := ctx.Err(); err != nil {
			return plugin.View{}, err
		}
		return plugin.View{Kind: plugin.ViewList, Title: t.T("view.search.title"), EmptyText: t.T("view.indexing")}, nil
	}
	switch req.ID {
	case cmdSearch:
		return p.searchView(ctx, t, ix, req.Query.Text)
	case cmdInstalled:
		return p.installedView(t, ix), nil
	case cmdUpdates:
		return p.updatesView(t, ix), nil
	case cmdBuckets:
		return p.bucketsView(t, ix), nil
	case cmdCleanup:
		return p.cleanupView(ctx, t, ix)
	}
	return plugin.View{}, fmt.Errorf("scoop: unknown view %q", req.ID)
}

// opsSection lists the queued and running operations, if any.
func (p *Plugin) opsSection(t plugin.Localizer) []plugin.Section {
	ops := p.activeOps()
	if len(ops) == 0 {
		return nil
	}
	items := make([]plugin.Item, 0, len(ops))
	for _, o := range ops {
		items = append(items, plugin.Item{
			ID:       "op/" + o.id,
			Title:    o.title,
			Subtitle: p.statusText(t, o.state()),
			Icon:     plugin.Icon{Name: "package"},
			Actions: []plugin.Action{
				{ID: "show-output", Title: t.T("action.showOutput"),
					Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewOutput, Payload: plugin.MustPayload(stopPayload{Op: o.id})}},
				{ID: "stop", Title: t.T("action.stop"), Shortcut: "Ctrl+X",
					Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackStop, Payload: plugin.MustPayload(stopPayload{Op: o.id})}},
			},
		})
	}
	return []plugin.Section{{Title: t.T("section.operations"), Items: items}}
}

func (p *Plugin) searchView(ctx context.Context, t plugin.Localizer, ix *Index, text string) (plugin.View, error) {
	v := plugin.View{
		Kind:        plugin.ViewList,
		Title:       t.T("view.search.title"),
		Placeholder: t.T("view.search.placeholder"),
		ShowDetail:  true,
		Sections:    p.opsSection(t),
	}
	if strings.TrimSpace(text) == "" {
		v.EmptyText = t.T("view.search.hint", "packages", ix.PackageCount(), "buckets", len(ix.Buckets))
		return v, nil
	}
	matches := ix.Search(ctx, text, p.maxResults())
	if err := ctx.Err(); err != nil {
		return plugin.View{}, err // superseded by a newer query
	}
	v.EmptyText = t.T("view.search.noResults")
	if len(matches) > 0 {
		items := make([]plugin.Item, len(matches))
		for i, m := range matches {
			items[i] = p.packageItem(t, ix, m.Pkg)
		}
		v.Sections = append(v.Sections, plugin.Section{Items: items})
	}
	return v, nil
}

func (p *Plugin) installedView(t plugin.Localizer, ix *Index) plugin.View {
	v := plugin.View{
		Kind:          plugin.ViewList,
		Title:         t.T("view.installed.title"),
		Placeholder:   t.T("view.installed.placeholder"),
		ShowDetail:    true,
		FilterLocally: true,
		EmptyText:     t.T("view.installed.empty"),
		Sections:      p.opsSection(t),
	}
	var updates, current, global []plugin.Item
	for _, a := range sortedApps(ix.Apps) {
		it := p.appItem(t, ix, a)
		switch _, out := ix.Outdated(a); {
		case a.Global:
			global = append(global, it)
		case out:
			updates = append(updates, it)
		default:
			current = append(current, it)
		}
	}
	for _, s := range []plugin.Section{
		{Title: t.T("section.updates"), Items: updates},
		{Title: t.T("section.installed"), Items: current},
		{Title: t.T("section.global"), Items: global},
	} {
		if len(s.Items) > 0 {
			v.Sections = append(v.Sections, s)
		}
	}
	return v
}

// sortedApps returns pointers to apps sorted by name.
func sortedApps(apps []App) []*App {
	out := make([]*App, len(apps))
	for i := range apps {
		out[i] = &apps[i]
	}
	slices.SortStableFunc(out, func(a, b *App) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out
}

func (p *Plugin) updatesView(t plugin.Localizer, ix *Index) plugin.View {
	v := plugin.View{
		Kind:          plugin.ViewList,
		Title:         t.T("view.updates.title"),
		Placeholder:   t.T("view.updates.placeholder"),
		ShowDetail:    true,
		FilterLocally: true,
		Sections:      p.opsSection(t),
	}
	var items []plugin.Item
	updatable := 0
	for _, a := range sortedApps(ix.Apps) {
		if _, out := ix.Outdated(a); out {
			items = append(items, p.appItem(t, ix, a))
			if !a.Held && !a.Global {
				updatable++
			}
		}
	}
	if updatable > 0 {
		all := plugin.Item{
			ID:       "update-all",
			Title:    t.N("updateAll.title", updatable),
			Subtitle: t.T("updateAll.subtitle"),
			Icon:     plugin.Icon{Name: "package"},
			Actions: []plugin.Action{{ID: "update-all", Title: t.T("action.updateAll"),
				Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackOp, Payload: plugin.MustPayload(opRequest{Kind: opUpdateAll})}}},
		}
		items = append([]plugin.Item{all}, items...)
	}
	if len(items) > 0 {
		v.Sections = append(v.Sections, plugin.Section{Items: items})
	}
	v.Sections = append(v.Sections, plugin.Section{Title: t.T("section.buckets"), Items: []plugin.Item{{
		ID:       "update-buckets",
		Title:    t.T("updateBuckets.title"),
		Subtitle: t.T("updateBuckets.subtitle"),
		Icon:     plugin.Icon{Name: "folder"},
		Actions: []plugin.Action{{ID: "update-buckets", Title: t.T("action.updateBuckets"),
			Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackOp, Payload: plugin.MustPayload(opRequest{Kind: opUpdateBuckets})}}},
	}}})
	if updatable == 0 && len(items) == 0 {
		v.EmptyText = t.T("view.updates.empty")
	}
	return v
}

// ---- items ----

// packageItem is the row of a bucket package in search results.
func (p *Plugin) packageItem(t plugin.Localizer, ix *Index, pkg *Package) plugin.Item {
	if a, ok := ix.App(pkg.Name); ok && (a.Bucket == "" || strings.EqualFold(a.Bucket, pkg.Bucket)) {
		it := p.appItem(t, ix, a)
		it.ID = "pkg/" + pkg.Bucket + "/" + pkg.Name
		return it
	}
	return plugin.Item{
		ID:        "pkg/" + pkg.Bucket + "/" + pkg.Name,
		Title:     pkg.Name,
		Subtitle:  truncate(pkg.Description, 100),
		Icon:      plugin.Icon{Name: "package"},
		Keywords:  pkg.Bin,
		Accessory: t.T("accessory.available", "version", pkg.Version, "bucket", pkg.Bucket),
		Actions:   p.actions(t, pkg, nil, false),
		Detail:    p.detail(t, pkg, nil, ""),
	}
}

// appItem is the row of an installed app.
func (p *Plugin) appItem(t plugin.Localizer, ix *Index, a *App) plugin.Item {
	pkg := &a.Info
	if latest, ok := ix.Latest(a); ok {
		pkg = latest
	}
	newer, out := ix.Outdated(a)
	var acc string
	switch {
	case out:
		acc = t.T("accessory.update", "from", a.Version, "to", newer)
	default:
		acc = t.T("accessory.installed", "version", a.Version)
	}
	if a.Held {
		acc = t.T("accessory.held", "status", acc)
	}
	if a.Global {
		acc = t.T("accessory.global", "status", acc)
	}
	prefix := "app/"
	if a.Global {
		prefix = "global/"
	}
	return plugin.Item{
		ID:        prefix + a.Name,
		Title:     a.Name,
		Subtitle:  truncate(cmp.Or(pkg.Description, a.Info.Description), 100),
		Icon:      plugin.Icon{Name: "package"},
		Keywords:  pkg.Bin,
		Accessory: acc,
		Actions:   p.actions(t, pkg, a, out),
		Detail:    p.detail(t, pkg, a, newer),
	}
}

// opAction is an action that queues a Scoop operation.
func opAction(id, title, shortcut string, req opRequest) plugin.Action {
	return plugin.Action{ID: id, Title: title, Shortcut: shortcut,
		Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackOp, Payload: plugin.MustPayload(req)}}
}

// actions are the actions of a package (pkg, which may be the installed
// manifest) and of its installed app a (nil when not installed).
func (p *Plugin) actions(t plugin.Localizer, pkg *Package, a *App, out bool) []plugin.Action {
	var acts []plugin.Action
	homepage := func() {
		if strings.HasPrefix(pkg.Homepage, "http://") || strings.HasPrefix(pkg.Homepage, "https://") {
			acts = append(acts, plugin.Action{ID: "homepage", Title: t.T("action.homepage"), Shortcut: "Ctrl+O",
				Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: pkg.Homepage}})
		}
	}
	copyInstall := func() {
		target := pkg.Name
		switch {
		case a != nil && a.URL != "":
			target = a.URL
		case pkg.Bucket != "":
			target = pkg.Bucket + "/" + pkg.Name
		}
		acts = append(acts, plugin.Action{ID: "copy-install", Title: t.T("action.copyInstall"), Shortcut: "Ctrl+Shift+C",
			Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: CommandLine([]string{"install", target})}})
	}
	reveal := func() {
		if pkg.File != "" {
			acts = append(acts, plugin.Action{ID: "reveal-manifest", Title: t.T("action.revealManifest"), Shortcut: "Ctrl+Shift+M",
				Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: pkg.File, Payload: plugin.MustPayload(plugin.OpenOptions{Reveal: true})}})
		}
	}
	if a == nil {
		acts = append(acts, opAction("install", t.T("action.install"), "", opRequest{Kind: opInstall, Name: pkg.Name, Bucket: pkg.Bucket}))
		homepage()
		copyInstall()
		reveal()
		return acts
	}
	update := opAction("update", t.T("action.update"), "Ctrl+U", opRequest{Kind: opUpdate, Name: a.Name})
	if out && !a.Held && !a.Global {
		acts = append(acts, update)
	}
	acts = append(acts, plugin.Action{ID: "open-folder", Title: t.T("action.openFolder"), Shortcut: "Ctrl+Shift+F",
		Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: a.Dir}})
	homepage()
	if !a.Global {
		if a.Held {
			acts = append(acts, opAction("unhold", t.T("action.unhold"), "Ctrl+Shift+H", opRequest{Kind: opUnhold, Name: a.Name}))
		} else {
			acts = append(acts, opAction("hold", t.T("action.hold"), "Ctrl+Shift+H", opRequest{Kind: opHold, Name: a.Name}))
		}
	}
	copyInstall()
	reveal()
	if !a.Global {
		un := opAction("uninstall", t.T("action.uninstall"), "Ctrl+X", opRequest{Kind: opUninstall, Name: a.Name})
		un.Style = plugin.StyleDestructive
		acts = append(acts, un)
	}
	return acts
}

// fenced puts text in a Markdown code block.
func fenced(text string) string {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	return fence + "text\n" + text + "\n" + fence
}

// detail is the detail pane of a package and its installed app (if any).
func (p *Plugin) detail(t plugin.Localizer, pkg *Package, a *App, newer string) *plugin.Detail {
	var md strings.Builder
	md.WriteString("## " + pkg.Name + "\n\n")
	if pkg.Description != "" {
		md.WriteString(pkg.Description + "\n")
	}
	notes := pkg.Notes
	if a != nil && notes == "" {
		notes = a.Info.Notes
	}
	if notes != "" {
		md.WriteString("\n### " + t.T("detail.notes") + "\n\n" + fenced(notes) + "\n")
	}
	var meta []plugin.Metadata
	add := func(label, value string) {
		if value != "" {
			meta = append(meta, plugin.Metadata{Label: label, Value: value})
		}
	}
	if a != nil {
		add(t.T("meta.installed"), a.Version)
		add(t.T("meta.latest"), newer)
		if a.Held {
			add(t.T("meta.status"), t.T("meta.held"))
		}
		if a.Global {
			add(t.T("meta.scope"), t.T("meta.global"))
		}
	} else {
		add(t.T("meta.version"), pkg.Version)
	}
	bucket := pkg.Bucket
	if a != nil && a.Bucket != "" {
		bucket = a.Bucket
	}
	add(t.T("meta.bucket"), bucket)
	if a != nil && a.URL != "" {
		add(t.T("meta.source"), a.URL)
	}
	add(t.T("meta.license"), pkg.License)
	add(t.T("meta.homepage"), pkg.Homepage)
	add(t.T("meta.commands"), strings.Join(pkg.Bin, ", "))
	if a != nil {
		add(t.T("meta.folder"), a.Dir)
	}
	add(t.T("meta.manifest"), pkg.File)
	return &plugin.Detail{Markdown: strings.TrimSpace(md.String()), Metadata: meta}
}

// ---- buckets ----

// knownBuckets reads the buckets Scoop knows by name (scoop bucket known).
func knownBuckets(root string) map[string]string {
	data, err := os.ReadFile(filepath.Join(root, "apps", "scoop", "current", "buckets.json"))
	if err != nil {
		return nil
	}
	var m map[string]string
	if json.Unmarshal(data, &m, lenient) != nil {
		return nil
	}
	return m
}

func (p *Plugin) bucketsView(t plugin.Localizer, ix *Index) plugin.View {
	v := plugin.View{
		Kind:          plugin.ViewList,
		Title:         t.T("view.buckets.title"),
		Placeholder:   t.T("view.buckets.placeholder"),
		ShowDetail:    true,
		FilterLocally: true,
		Sections:      p.opsSection(t),
	}
	now := p.h.Now()
	var added []plugin.Item
	for _, b := range ix.Buckets {
		var meta []plugin.Metadata
		meta = append(meta, plugin.Metadata{Label: t.T("meta.packages"), Value: t.N("packages.count", len(b.Packages))})
		if !b.Updated.IsZero() {
			meta = append(meta, plugin.Metadata{Label: t.T("meta.updated"), Value: t.RelativeTime(b.Updated, now)})
		}
		if b.Source != "" {
			meta = append(meta, plugin.Metadata{Label: t.T("meta.source"), Value: b.Source})
		}
		meta = append(meta, plugin.Metadata{Label: t.T("meta.folder"), Value: b.Dir})
		rm := opAction("remove-bucket", t.T("action.removeBucket"), "Ctrl+X", opRequest{Kind: opBucketRemove, Name: b.Name})
		rm.Style = plugin.StyleDestructive
		added = append(added, plugin.Item{
			ID:        "bucket/" + b.Name,
			Title:     b.Name,
			Subtitle:  b.Source,
			Icon:      plugin.Icon{Name: "folder"},
			Accessory: t.N("packages.count", len(b.Packages)),
			Detail:    &plugin.Detail{Metadata: meta},
			Actions: []plugin.Action{
				opAction("update-buckets", t.T("action.updateBuckets"), "", opRequest{Kind: opUpdateBuckets}),
				{ID: "open-folder", Title: t.T("action.openFolder"), Shortcut: "Ctrl+Shift+F",
					Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: b.Dir}},
				rm,
			},
		})
	}
	if len(added) > 0 {
		v.Sections = append(v.Sections, plugin.Section{Title: t.T("section.addedBuckets"), Items: added})
	}
	known := knownBuckets(ix.Root)
	names := make([]string, 0, len(known))
	for name := range known {
		if !slices.ContainsFunc(ix.Buckets, func(b *Bucket) bool { return strings.EqualFold(b.Name, name) }) {
			names = append(names, name)
		}
	}
	slices.SortFunc(names, func(a, b string) int {
		return cmp.Or(cmp.Compare(bucketRank(a), bucketRank(b)), strings.Compare(a, b))
	})
	var more []plugin.Item
	for _, name := range names {
		more = append(more, plugin.Item{
			ID:       "known/" + name,
			Title:    name,
			Subtitle: known[name],
			Icon:     plugin.Icon{Name: "folder"},
			Detail:   &plugin.Detail{Metadata: []plugin.Metadata{{Label: t.T("meta.source"), Value: known[name]}}},
			Actions: []plugin.Action{
				opAction("add-bucket", t.T("action.addBucket"), "", opRequest{Kind: opBucketAdd, Name: name}),
				{ID: "open-source", Title: t.T("action.openSource"), Shortcut: "Ctrl+O",
					Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: known[name]}},
			},
		})
	}
	more = append(more, plugin.Item{
		ID:       "custom",
		Title:    t.T("customBucket.title"),
		Subtitle: t.T("customBucket.subtitle"),
		Icon:     plugin.Icon{Name: "folder"},
		Arguments: []plugin.Argument{
			{Name: "name", Type: plugin.ArgText, Placeholder: t.T("customBucket.name"), Required: true},
			{Name: "url", Type: plugin.ArgText, Placeholder: t.T("customBucket.url"), Required: true},
		},
		Actions: []plugin.Action{opAction("add-bucket", t.T("action.addBucket"), "", opRequest{Kind: opBucketAdd})},
	})
	v.Sections = append(v.Sections, plugin.Section{Title: t.T("section.moreBuckets"), Items: more})
	return v
}

// ---- cleanup ----

// reclaimTTL is how long a measured cleanup size is reused: measuring
// walks every old version folder, which takes seconds on a large install.
const reclaimTTL = time.Minute

// reclaimFor returns what cleanup would free, measured at most once per
// reclaimTTL for the same index and between operations.
func (p *Plugin) reclaimFor(ctx context.Context, ix *Index) (Reclaim, error) {
	p.mu.Lock()
	if p.reclaimIx == ix && time.Since(p.reclaimAt) < reclaimTTL {
		r := p.reclaim
		p.mu.Unlock()
		return r, nil
	}
	p.mu.Unlock()
	r := computeReclaim(ctx, ix.Root, ix.Apps)
	if err := ctx.Err(); err != nil {
		return Reclaim{}, err
	}
	p.mu.Lock()
	p.reclaim, p.reclaimIx, p.reclaimAt = r, ix, time.Now()
	p.mu.Unlock()
	return r, nil
}

func (p *Plugin) cleanupView(ctx context.Context, t plugin.Localizer, ix *Index) (plugin.View, error) {
	r, err := p.reclaimFor(ctx, ix)
	if err != nil {
		return plugin.View{}, err
	}
	v := plugin.View{
		Kind:          plugin.ViewList,
		Title:         t.T("view.cleanup.title"),
		ShowDetail:    true,
		FilterLocally: true,
		Sections:      p.opsSection(t),
	}
	versions := 0
	for _, a := range r.Apps {
		versions += len(a.Versions)
	}
	clean := opAction("cleanup", t.T("action.cleanup"), "", opRequest{Kind: opCleanup})
	clean.Style = plugin.StyleDestructive
	summary := plugin.Item{
		ID:        "cleanup",
		Title:     t.T("cleanup.title"),
		Subtitle:  t.T("cleanup.subtitle", "size", formatSize(t, r.Total())),
		Icon:      plugin.Icon{Name: "trash"},
		Accessory: formatSize(t, r.Total()),
		Actions:   []plugin.Action{clean},
		Detail: &plugin.Detail{
			Markdown: t.T("cleanup.detail"),
			Metadata: []plugin.Metadata{
				{Label: t.T("meta.oldVersions"), Value: t.T("cleanup.oldVersions", "versions", t.N("versions.count", versions),
					"apps", t.N("apps.count", len(r.Apps)), "size", formatSize(t, r.OldSize))},
				{Label: t.T("meta.cache"), Value: t.T("cleanup.cache", "files", t.N("files.count", r.CacheFiles), "size", formatSize(t, r.CacheSize))},
				{Label: t.T("meta.total"), Value: formatSize(t, r.Total())},
			},
		},
	}
	v.Sections = append(v.Sections, plugin.Section{Items: []plugin.Item{summary}})
	var apps []plugin.Item
	for _, a := range r.Apps {
		apps = append(apps, plugin.Item{
			ID:        "old/" + a.App,
			Title:     a.App,
			Subtitle:  strings.Join(a.Versions, ", "),
			Icon:      plugin.Icon{Name: "package"},
			Accessory: formatSize(t, a.Size),
			Actions: []plugin.Action{{ID: "open-folder", Title: t.T("action.openFolder"), Shortcut: "Ctrl+Shift+F",
				Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: a.Dir}}},
		})
	}
	if len(apps) > 0 {
		v.Sections = append(v.Sections, plugin.Section{Title: t.T("section.oldVersions"), Items: apps})
	}
	return v, nil
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

// ---- output ----

// viewTail is how much output the output view shows.
const viewTail = 32 << 10

// statusText describes the state of an operation.
func (p *Plugin) statusText(t plugin.Localizer, s opState) string {
	switch {
	case s.status == opQueued:
		return t.T("status.queued")
	case s.status == opRunning:
		return t.T("status.running")
	case s.stopped:
		return t.T("status.stopped")
	case s.err != nil:
		return t.T("status.error", "error", s.err.Error())
	case s.exitCode != 0:
		return t.T("status.failed", "code", s.exitCode)
	}
	return t.T("status.succeeded")
}

// outputView shows the output and status of one operation.
func (p *Plugin) outputView(t plugin.Localizer, req plugin.ViewRequest) plugin.View {
	var sp stopPayload
	_ = json.Unmarshal(req.Payload, &sp)
	p.mu.Lock()
	o := p.ops[sp.Op]
	p.mu.Unlock()
	if o == nil {
		return plugin.View{Kind: plugin.ViewList, Title: t.T("view.output.title"), EmptyText: t.T("view.output.gone")}
	}
	s := o.state()
	status := p.statusText(t, s)
	var duration string
	if !s.started.IsZero() {
		end := s.ended
		if s.status != opDone {
			end = p.h.Now()
		}
		duration = end.Sub(s.started).Round(100 * time.Millisecond).String()
	}
	text := o.out.String()
	if len(text) > viewTail {
		text = text[len(text)-viewTail:]
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
		text = t.T("view.output.truncated") + "\n" + text
	} else if o.out.Truncated() {
		text = t.T("view.output.truncated") + "\n" + text
	}
	if text == "" {
		text = t.T("view.output.empty")
	}
	var commands []string
	for _, args := range o.steps {
		commands = append(commands, CommandLine(args))
	}
	var actions []plugin.Action
	if s.status != opDone {
		actions = append(actions, plugin.Action{ID: "stop", Title: t.T("action.stop"), Shortcut: "Ctrl+X",
			Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackStop, Payload: plugin.MustPayload(stopPayload{Op: o.id})}})
	}
	actions = append(actions, plugin.Action{ID: "copy-output", Title: t.T("action.copyOutput"), Shortcut: "Ctrl+Shift+C",
		Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: o.out.String()}})
	meta := []plugin.Metadata{
		{Label: t.T("meta.status"), Value: status},
		{Label: t.T("meta.command"), Value: strings.Join(commands, "\n")},
	}
	if duration != "" {
		meta = append(meta, plugin.Metadata{Label: t.T("meta.duration"), Value: duration})
	}
	item := plugin.Item{
		ID:        "output",
		Title:     status,
		Subtitle:  o.title,
		Icon:      plugin.Icon{Name: "package"},
		Accessory: duration,
		Actions:   actions,
		Detail:    &plugin.Detail{Markdown: fenced(text), Metadata: meta},
	}
	return plugin.View{
		Kind:       plugin.ViewList,
		Title:      o.title,
		ShowDetail: true,
		Sections:   []plugin.Section{{Items: []plugin.Item{item}}},
	}
}
