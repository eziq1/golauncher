package quicklinks

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Form field IDs.
const (
	fieldName     = "name"
	fieldLink     = "link"
	fieldKeyword  = "keyword"
	fieldOpenWith = "openWith"
	fieldKeywords = "keywords"
	fieldURL      = "url"
	fieldSuggest  = "suggest"
	fieldFile     = "file"
	fieldJSON     = "json"
)

// View implements plugin.ViewProvider.
func (p *Plugin) View(_ context.Context, req plugin.ViewRequest) (plugin.View, error) {
	var pl idPayload
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &pl); err != nil {
			return plugin.View{}, err
		}
	}
	switch req.ID {
	case cmdLinks:
		return p.linksView(), nil
	case cmdCreate:
		return p.linkForm(Quicklink{}), nil
	case viewEdit:
		q, ok := p.findLink(pl.ID)
		if !ok {
			return plugin.View{}, errors.New(p.h.I18n().T("error.gone"))
		}
		return p.linkForm(q), nil
	case cmdEngines:
		return p.enginesView(), nil
	case viewEngine:
		return p.engineForm(pl.ID)
	case viewImport:
		return p.importForm(), nil
	case viewExport:
		return p.exportForm(), nil
	}
	return plugin.View{}, fmt.Errorf("quicklinks: unknown view %q", req.ID)
}

// manageItems are the entries that create, import and export, listed
// after the quicklinks and the engines.
func (p *Plugin) manageItems(withCreate, withEngine bool) []plugin.Item {
	l := p.h.I18n()
	push := func(view string) plugin.ActionDo { return plugin.ActionDo{Kind: plugin.ActionPush, Target: view} }
	var items []plugin.Item
	if withCreate {
		items = append(items, plugin.Item{ID: "manage:create", Title: l.T("command.create.title"), Icon: plugin.Icon{Name: "file-plus"},
			Actions: []plugin.Action{{ID: "create", Title: l.T("action.create"), Do: push(cmdCreate)}}})
	}
	if withEngine {
		items = append(items, plugin.Item{ID: "manage:engine", Title: l.T("manage.addEngine"), Icon: plugin.Icon{Name: "file-plus"},
			Actions: []plugin.Action{{ID: "create", Title: l.T("action.create"), Do: push(viewEngine)}}})
	}
	return append(items,
		plugin.Item{ID: "manage:import", Title: l.T("manage.import"), Icon: plugin.Icon{Name: "folder"},
			Actions: []plugin.Action{{ID: "import", Title: l.T("action.import"), Do: push(viewImport)}}},
		plugin.Item{ID: "manage:export", Title: l.T("manage.export"), Icon: plugin.Icon{Name: "archive"},
			Actions: []plugin.Action{
				{ID: "export", Title: l.T("action.export"), Do: push(viewExport)},
				{ID: "copyJSON", Title: l.T("action.copyJSON"), Shortcut: shortcutCopy, Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: p.exportJSON()}},
			}},
	)
}

// linksView lists the quicklinks.
func (p *Plugin) linksView() plugin.View {
	l := p.h.I18n()
	b := p.newBuilder()
	v := plugin.View{Kind: plugin.ViewList, Title: l.T("command.quicklinks.title"), Placeholder: l.T("view.links.placeholder"),
		FilterLocally: true, ShowDetail: true, EmptyText: l.T("view.links.empty")}
	var items []plugin.Item
	for _, q := range b.links {
		items = append(items, b.linkItem(q, false))
	}
	if len(items) > 0 {
		v.Sections = append(v.Sections, plugin.Section{Title: l.T("group.quicklinks"), Items: items})
	}
	v.Sections = append(v.Sections, plugin.Section{Title: l.T("section.manage"), Items: p.manageItems(true, false)})
	return v
}

// linkForm creates (empty ID) or edits a quicklink.
func (p *Plugin) linkForm(q Quicklink) plugin.View {
	l := p.h.I18n()
	title := l.T("form.create.title")
	if q.ID != "" {
		title = l.T("form.edit.title", "name", q.Name)
	}
	return plugin.View{Kind: plugin.ViewForm, Title: title, Form: &plugin.Form{
		Fields: []plugin.Field{
			{ID: fieldName, Type: plugin.FieldText, Title: l.T("form.name"), Placeholder: l.T("form.name.placeholder"), Required: true,
				Default: plugin.MustPayload(q.Name)},
			{ID: fieldLink, Type: plugin.FieldText, Title: l.T("form.link"), Placeholder: l.T("form.link.placeholder"), Required: true,
				Info: l.T("form.link.info"), Default: plugin.MustPayload(q.Link)},
			{ID: fieldKeyword, Type: plugin.FieldText, Title: l.T("form.keyword"), Placeholder: l.T("form.keyword.placeholder"),
				Info: l.T("form.keyword.info"), Default: plugin.MustPayload(q.Keyword)},
			{ID: fieldOpenWith, Type: plugin.FieldText, Title: l.T("form.openWith"), Placeholder: l.T("form.openWith.placeholder"),
				Info: l.T("form.openWith.info"), Default: plugin.MustPayload(q.OpenWith)},
		},
		Submit: plugin.Action{ID: "save", Title: l.T("action.save"), Do: callback(cbSave, idPayload{ID: q.ID})},
	}}
}

// enginesView lists the search engines.
func (p *Plugin) enginesView() plugin.View {
	l := p.h.I18n()
	v := plugin.View{Kind: plugin.ViewList, Title: l.T("command.engines.title"), Placeholder: l.T("view.engines.placeholder"),
		FilterLocally: true, ShowDetail: true}
	def := ""
	if ds := p.defaultEngines(); len(ds) > 0 {
		def = ds[0].ID
	}
	var builtin, custom []plugin.Item
	for _, e := range p.engineList() {
		it := p.engineRow(l, e, e.ID == def)
		if e.Custom {
			custom = append(custom, it)
		} else {
			builtin = append(builtin, it)
		}
	}
	if len(custom) > 0 {
		v.Sections = append(v.Sections, plugin.Section{Title: l.T("section.custom"), Items: custom})
	}
	v.Sections = append(v.Sections,
		plugin.Section{Title: l.T("section.builtin"), Items: builtin},
		plugin.Section{Title: l.T("section.manage"), Items: p.manageItems(false, true)})
	return v
}

// engineRow is an engine in the Search Engines view.
func (p *Plugin) engineRow(l plugin.Localizer, e Engine, isDefault bool) plugin.Item {
	id := idPayload{ID: e.ID}
	it := plugin.Item{
		ID:       "engine:" + e.ID,
		Title:    e.Name,
		Subtitle: strings.Join(e.Keywords, ", "),
		Icon:     plugin.Icon{Name: "search"},
		Keywords: e.Keywords,
		Detail: &plugin.Detail{Metadata: []plugin.Metadata{
			{Label: l.T("detail.keywords"), Value: strings.Join(e.Keywords, ", ")},
			{Label: l.T("detail.url"), Value: e.URL},
		}},
	}
	if e.Suggest != "" {
		it.Detail.Metadata = append(it.Detail.Metadata, plugin.Metadata{Label: l.T("detail.suggest"), Value: e.Suggest})
	}
	switch {
	case e.Disabled:
		it.Accessory = l.T("engine.disabled")
		it.Icon.Tint = plugin.TintMuted
	case isDefault:
		it.Accessory = l.T("engine.default")
		it.Icon.Tint = plugin.TintAccent
	}
	toggle := l.T("action.disable")
	if e.Disabled {
		toggle = l.T("action.enable")
	}
	it.Actions = []plugin.Action{
		{ID: "toggle", Title: toggle, Do: callback(cbToggle, id)},
		{ID: "edit", Title: l.T("action.edit"), Shortcut: shortcutEdit, Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewEngine, Payload: plugin.MustPayload(id)}},
	}
	if !e.Disabled && !isDefault {
		it.Actions = append(it.Actions, plugin.Action{ID: "default", Title: l.T("action.setDefault"), Shortcut: shortcutDefault, Do: callback(cbSetDefault, id)})
	}
	it.Actions = append(it.Actions, plugin.Action{ID: "home", Title: l.T("action.openHome"), Do: openAction(homepage(e.URL), "")})
	if e.Custom {
		it.Actions = append(it.Actions, plugin.Action{ID: "delete", Title: l.T("action.delete"), Shortcut: shortcutDelete,
			Style: plugin.StyleDestructive, Do: callback(cbDeleteEngine, id)})
	} else {
		it.Actions = append(it.Actions, plugin.Action{ID: "reset", Title: l.T("action.reset"), Do: callback(cbReset, id)})
	}
	return it
}

// engineForm adds a custom engine (empty id) or edits an engine: only the
// keywords of a built-in one.
func (p *Plugin) engineForm(id string) (plugin.View, error) {
	l := p.h.I18n()
	var e Engine
	if id != "" {
		var ok bool
		if e, ok = p.engineByID(id); !ok {
			return plugin.View{}, errors.New(l.T("error.gone"))
		}
	}
	keywords := plugin.Field{ID: fieldKeywords, Type: plugin.FieldText, Title: l.T("form.keywords"), Placeholder: l.T("form.keywords.placeholder"),
		Info: l.T("form.keywords.info"), Default: plugin.MustPayload(strings.Join(e.Keywords, " "))}
	submit := plugin.Action{ID: "save", Title: l.T("action.save"), Do: callback(cbSaveEngine, idPayload{ID: id})}
	if id != "" && !e.Custom {
		keywords.Info = l.T("form.builtin.info", "url", e.URL)
		return plugin.View{Kind: plugin.ViewForm, Title: l.T("form.engine.edit", "name", e.Name),
			Form: &plugin.Form{Fields: []plugin.Field{keywords}, Submit: submit}}, nil
	}
	title := l.T("form.engine.create")
	if id != "" {
		title = l.T("form.engine.edit", "name", e.Name)
	}
	return plugin.View{Kind: plugin.ViewForm, Title: title, Form: &plugin.Form{
		Fields: []plugin.Field{
			{ID: fieldName, Type: plugin.FieldText, Title: l.T("form.name"), Placeholder: l.T("form.engineName.placeholder"), Required: true,
				Default: plugin.MustPayload(e.Name)},
			keywords,
			{ID: fieldURL, Type: plugin.FieldText, Title: l.T("form.url"), Placeholder: l.T("form.url.placeholder"), Required: true,
				Info: l.T("form.url.info"), Default: plugin.MustPayload(e.URL)},
			{ID: fieldSuggest, Type: plugin.FieldText, Title: l.T("form.suggest"), Placeholder: l.T("form.suggest.placeholder"),
				Info: l.T("form.suggest.info"), Default: plugin.MustPayload(e.Suggest)},
		},
		Submit: submit,
	}}, nil
}

func (p *Plugin) importForm() plugin.View {
	l := p.h.I18n()
	return plugin.View{Kind: plugin.ViewForm, Title: l.T("manage.import"), Form: &plugin.Form{
		Fields: []plugin.Field{
			{ID: fieldFile, Type: plugin.FieldFile, Title: l.T("form.importFile"), Info: l.T("form.importFile.info")},
			{ID: fieldJSON, Type: plugin.FieldTextArea, Title: l.T("form.importJSON"), Placeholder: l.T("form.importJSON.placeholder")},
		},
		Submit: plugin.Action{ID: "import", Title: l.T("action.import"), Do: callback(cbImport, idPayload{})},
	}}
}

func (p *Plugin) exportForm() plugin.View {
	l := p.h.I18n()
	return plugin.View{Kind: plugin.ViewForm, Title: l.T("manage.export"), Form: &plugin.Form{
		Fields: []plugin.Field{
			{ID: fieldFile, Type: plugin.FieldFile, Title: l.T("form.exportFile"), Placeholder: l.T("form.exportFile.placeholder"),
				Info: l.T("form.exportFile.info"), Required: true},
		},
		Submit: plugin.Action{ID: "export", Title: l.T("action.export"), Do: callback(cbExport, idPayload{})},
	}}
}

// formValue returns a string field of a form submit.
func formValue(a plugin.ActionDo, id string) string {
	var s string
	if raw, ok := a.Values[id]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	return strings.TrimSpace(s)
}

// HandleAction implements plugin.ActionHandler.
func (p *Plugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	l := p.h.I18n()
	var pl idPayload
	if len(a.Payload) > 0 {
		if err := json.Unmarshal(a.Payload, &pl); err != nil {
			return plugin.Result{}, err
		}
	}
	ok := func(title string) plugin.Result {
		return plugin.Result{Refresh: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: title}}
	}
	gone := plugin.Result{Refresh: true, Toast: &plugin.Toast{Style: plugin.ToastFailure, Title: l.T("error.gone")}}
	switch a.Target {
	case cbOpen:
		q, found := p.findLink(pl.ID)
		if !found {
			return gone, nil
		}
		query := pl.Query
		if v := formValue(a, argQuery); v != "" {
			query = v
		}
		t := parseLink(q.Link).resolve(values{query: query, now: p.h.Now(), clipboard: p.clipboardText})
		do := t.openDo(q.OpenWith)
		return plugin.Result{Do: &do}, nil
	case cbSearch:
		e, found := p.engineByID(pl.ID)
		if !found {
			return gone, nil
		}
		do := openAction(homepage(e.URL), "")
		if query := formValue(a, argQuery); query != "" {
			do = openAction(searchURL(e, query), "")
		}
		return plugin.Result{Do: &do}, nil
	case cbSave:
		return p.saveLink(a, pl.ID)
	case cbDuplicate:
		q, found := p.findLink(pl.ID)
		if !found {
			return gone, nil
		}
		q.ID, q.Keyword, q.Name = "", "", l.T("duplicate.name", "name", q.Name)
		if _, err := p.putLink(q); err != nil {
			return plugin.Result{}, err
		}
		return ok(l.T("toast.duplicated", "name", q.Name)), nil
	case cbDelete:
		q, err := p.deleteLink(pl.ID)
		if errors.Is(err, errNotFound) {
			return gone, nil
		} else if err != nil {
			return plugin.Result{}, err
		}
		return ok(l.T("toast.deleted", "name", q.Name)), nil
	case cbToggle:
		e, found := p.engineByID(pl.ID)
		if !found {
			return gone, nil
		}
		var err error
		if e.Custom {
			err = p.setCustomDisabled(e.ID, !e.Disabled)
		} else {
			err = p.updateBuiltin(e.ID, func(o *override) { o.Disabled = !e.Disabled })
		}
		if err != nil {
			return plugin.Result{}, err
		}
		if e.Disabled {
			return ok(l.T("toast.enabled", "name", e.Name)), nil
		}
		return ok(l.T("toast.disabled", "name", e.Name)), nil
	case cbSetDefault:
		e, found := p.engineByID(pl.ID)
		if !found {
			return gone, nil
		}
		r := ok(l.T("toast.default", "name", e.Name))
		r.Preferences = map[string]jsontext.Value{prefDefault: plugin.MustPayload(e.ID)}
		return r, nil
	case cbReset:
		e, found := p.engineByID(pl.ID)
		if !found {
			return gone, nil
		}
		if err := p.updateBuiltin(e.ID, func(o *override) { *o = override{} }); err != nil {
			return plugin.Result{}, err
		}
		return ok(l.T("toast.reset", "name", e.Name)), nil
	case cbDeleteEngine:
		e, err := p.deleteCustom(pl.ID)
		if errors.Is(err, errNotFound) {
			return gone, nil
		} else if err != nil {
			return plugin.Result{}, err
		}
		return ok(l.T("toast.deleted", "name", e.Name)), nil
	case cbSaveEngine:
		return p.saveEngine(a, pl.ID)
	case cbImport:
		return p.doImport(a)
	case cbExport:
		path := formValue(a, fieldFile)
		if path == "" {
			return plugin.Result{FieldErrors: map[string]string{fieldFile: l.T("error.required")}}, nil
		}
		path = expandEnv(path)
		if err := os.WriteFile(path, []byte(p.exportJSON()+"\n"), 0o644); err != nil {
			return plugin.Result{FieldErrors: map[string]string{fieldFile: l.T("error.write", "error", err.Error())}}, nil
		}
		return plugin.Result{PopToRoot: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("toast.exported"), Message: filepath.Clean(path)}}, nil
	}
	return plugin.Result{}, fmt.Errorf("quicklinks: unknown action %q", a.Target)
}

// clipboardText reads the clipboard's text for {clipboard}.
func (p *Plugin) clipboardText() string {
	c, err := p.h.Clipboard().Read()
	if err != nil {
		p.h.Logger().Warn("quicklinks: read clipboard", "err", err)
		return ""
	}
	return c.Text
}

// keywordError checks a keyword typed in a form: no spaces, not used by
// another quicklink or an enabled engine.
func (p *Plugin) keywordError(kw, skipLink, skipEngine string) string {
	l := p.h.I18n()
	if strings.ContainsFunc(kw, isSpace) {
		return l.T("error.keywordSpace")
	}
	if owner, used := p.keywordOwner(kw, skipLink, skipEngine); used {
		return l.T("error.keywordUsed", "keyword", kw, "owner", owner)
	}
	return ""
}

// saveLink creates or updates a quicklink from the form.
func (p *Plugin) saveLink(a plugin.ActionDo, id string) (plugin.Result, error) {
	l := p.h.I18n()
	q := Quicklink{ID: id, Name: formValue(a, fieldName), Link: formValue(a, fieldLink),
		Keyword: normKeyword(formValue(a, fieldKeyword)), OpenWith: formValue(a, fieldOpenWith)}
	errs := map[string]string{}
	if q.Name == "" {
		errs[fieldName] = l.T("error.required")
	}
	if q.Link == "" {
		errs[fieldLink] = l.T("error.required")
	}
	if msg := p.keywordError(q.Keyword, id, ""); q.Keyword != "" && msg != "" {
		errs[fieldKeyword] = msg
	}
	if len(errs) > 0 {
		return plugin.Result{FieldErrors: errs}, nil
	}
	if _, err := p.putLink(q); errors.Is(err, errNotFound) {
		return plugin.Result{PopToRoot: true, Toast: &plugin.Toast{Style: plugin.ToastFailure, Title: l.T("error.gone")}}, nil
	} else if err != nil {
		return plugin.Result{}, err
	}
	title := l.T("toast.created", "name", q.Name)
	if id != "" {
		title = l.T("toast.saved", "name", q.Name)
	}
	return plugin.Result{PopToRoot: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: title}}, nil
}

// saveEngine adds or updates an engine from its form.
func (p *Plugin) saveEngine(a plugin.ActionDo, id string) (plugin.Result, error) {
	l := p.h.I18n()
	var e Engine
	if id != "" {
		var found bool
		if e, found = p.engineByID(id); !found {
			return plugin.Result{PopToRoot: true, Toast: &plugin.Toast{Style: plugin.ToastFailure, Title: l.T("error.gone")}}, nil
		}
	}
	errs := map[string]string{}
	kws := parseKeywords(formValue(a, fieldKeywords))
	for _, k := range kws {
		if msg := p.keywordError(k, "", id); msg != "" {
			errs[fieldKeywords] = msg
			break
		}
	}
	if id != "" && !e.Custom {
		if len(errs) > 0 {
			return plugin.Result{FieldErrors: errs}, nil
		}
		if err := p.updateBuiltin(id, func(o *override) { o.Keywords = append([]string{}, kws...) }); err != nil {
			return plugin.Result{}, err
		}
		return plugin.Result{PopToRoot: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("toast.saved", "name", e.Name)}}, nil
	}
	e.Name, e.URL, e.Suggest, e.Keywords = formValue(a, fieldName), formValue(a, fieldURL), formValue(a, fieldSuggest), kws
	if e.Name == "" {
		errs[fieldName] = l.T("error.required")
	}
	switch {
	case e.URL == "":
		errs[fieldURL] = l.T("error.required")
	case !strings.Contains(strings.ToLower(e.URL), "{query}") || parseLink(e.URL).kind != kindURL:
		errs[fieldURL] = l.T("error.engineURL")
	}
	if e.Suggest != "" && (!strings.Contains(e.Suggest, "{query}") || !strings.HasPrefix(strings.ToLower(e.Suggest), "http")) {
		errs[fieldSuggest] = l.T("error.engineURL")
	}
	if len(errs) > 0 {
		return plugin.Result{FieldErrors: errs}, nil
	}
	e.URL = parseLink(e.URL).link
	if _, err := p.putCustom(e); err != nil {
		return plugin.Result{}, err
	}
	title := l.T("toast.created", "name", e.Name)
	if id != "" {
		title = l.T("toast.saved", "name", e.Name)
	}
	return plugin.Result{PopToRoot: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: title}}, nil
}

// doImport imports a file or pasted JSON.
func (p *Plugin) doImport(a plugin.ActionDo) (plugin.Result, error) {
	l := p.h.I18n()
	data := []byte(formValue(a, fieldJSON))
	field := fieldJSON
	if path := formValue(a, fieldFile); path != "" {
		field = fieldFile
		b, err := os.ReadFile(expandEnv(path))
		if err != nil {
			return plugin.Result{FieldErrors: map[string]string{fieldFile: l.T("error.read", "error", err.Error())}}, nil
		}
		data = b
	}
	if len(data) == 0 {
		return plugin.Result{FieldErrors: map[string]string{fieldFile: l.T("error.importEmpty")}}, nil
	}
	r, err := p.importJSON(data)
	if errors.Is(err, errFormat) {
		return plugin.Result{FieldErrors: map[string]string{field: l.T("error.format")}}, nil
	} else if err != nil {
		return plugin.Result{}, err
	}
	msg := l.T("toast.importedDetail", "links", r.Links, "engines", r.Engines+r.Builtins, "skipped", r.Skipped)
	if r.DroppedKeywords > 0 {
		msg += " " + l.N("toast.droppedKeywords", r.DroppedKeywords)
	}
	return plugin.Result{PopToRoot: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("toast.imported"), Message: msg}}, nil
}
