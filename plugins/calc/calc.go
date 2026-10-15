// Package calc is the built-in calculator plugin. It evaluates math
// expressions typed in the root search and offers the result as an item.
//
// Expressions use a locale-independent syntax ("." as decimal point, English
// function names) and are evaluated with 128-bit math/big floats. Results
// are displayed with the locale's digit grouping but copied and pasted in
// their plain machine form.
package calc

import (
	"context"
	"embed"
	"io/fs"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// ID is the plugin's manifest ID.
const ID = "builtin.calc"

//go:embed locales/*.json
var locales embed.FS

// Message keys.
// Plugin is the calculator plugin.
type Plugin struct {
	h plugin.Host
}

var (
	_ plugin.Plugin         = (*Plugin)(nil)
	_ plugin.RootProvider   = (*Plugin)(nil)
	_ plugin.LocaleProvider = (*Plugin)(nil)
)

// New returns the calculator plugin.
func New() *Plugin { return &Plugin{} }

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:          ID,
		Name:        "%plugin.name%",
		Description: "%plugin.description%",
		Version:     "1.0.0",
		Icon:        plugin.Icon{Name: "calculator"},
	}
}

// Init implements plugin.Plugin.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.h = h
	return nil
}

// Close implements plugin.Plugin.
func (p *Plugin) Close() error { return nil }

// Locales implements plugin.LocaleProvider.
func (p *Plugin) Locales() fs.FS {
	sub, err := fs.Sub(locales, "locales")
	if err != nil {
		panic(err)
	}
	return sub
}

// RootItems implements plugin.RootProvider. It returns a single result item
// when the query is a valid expression and nothing otherwise.
func (p *Plugin) RootItems(ctx context.Context, q plugin.Query) ([]plugin.Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := calculate(q.Text)
	if err != nil {
		return nil, nil
	}
	l := p.h.I18n()
	title := c.raw
	if c.base == "" || c.base == "dec" {
		title = display(l, c.raw)
	}
	icon := plugin.Icon{Name: "calculator"}
	return []plugin.Item{{
		ID:       "result",
		Title:    title,
		Subtitle: c.expr,
		Icon:     icon,
		Group:    l.T("group"),
		Score:    1,
		Actions: []plugin.Action{
			{
				ID:    "copy",
				Title: l.T("action.copy"),
				Do:    plugin.ActionDo{Kind: plugin.ActionCopyText, Target: c.raw},
			},
			{
				ID:       "copy-expression",
				Title:    l.T("action.copyExpression"),
				Shortcut: "Ctrl+Shift+C",
				Do:       plugin.ActionDo{Kind: plugin.ActionCopyText, Target: c.expr + " = " + c.raw},
			},
			{
				ID:       "paste",
				Title:    l.T("action.paste"),
				Shortcut: "Ctrl+Shift+V",
				Do:       plugin.ActionDo{Kind: plugin.ActionPaste, Target: c.raw},
			},
		},
	}}, nil
}

// display groups the digits of a machine-form decimal for the locale. The
// exponent of scientific notation is left as is.
func display(l plugin.Localizer, raw string) string {
	if mant, exp, ok := strings.Cut(raw, "e"); ok {
		return l.GroupDigits(mant) + "e" + exp
	}
	return l.GroupDigits(raw)
}
