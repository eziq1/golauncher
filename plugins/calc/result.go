package calc

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/calc/units"
)

// row is one conversion of a result: a value in a unit or format.
type row struct {
	id    string
	title string // shown, with the locale's digit grouping: "6.21371 mi"
	raw   string // copied and pasted: "6.21371"
	full  string // copied with the unit: "6.21371 mi"
	label string // detail label: "mi"
	shown string // detail value: "6.21371"
}

// result is a calculation with units, a color, a timestamp or a size.
type result struct {
	source string // as shown in subtitles: "10 km"
	rows   []row
	root   int    // rows shown in the root search (the others are in the detail)
	note   string // appended to subtitles: "rates from 2026-10-15"
	// markdown is extra detail text (rate attribution); image a swatch.
	markdown, image string
}

// payload is the Payload of the "Show All Conversions" action.
type payload struct {
	Text string `json:"text"`
}

// detail is the detail pane of r: the source, the other conversions and
// the attribution or swatch.
func (r *result) detail() *plugin.Detail {
	d := &plugin.Detail{Markdown: "**" + escapeMarkdown(r.source) + "**", Image: r.image}
	if r.markdown != "" {
		d.Markdown += "\n\n" + r.markdown
	}
	for _, x := range r.rows {
		d.Metadata = append(d.Metadata, plugin.Metadata{Label: x.label, Value: x.shown})
	}
	return d
}

func escapeMarkdown(s string) string {
	return strings.NewReplacer("*", `\*`, "_", `\_`, "[", `\[`, "`", "\\`").Replace(s)
}

// subtitle is the full equation of a row.
func (r *result) subtitle(x row) string {
	s := r.source + " = " + x.title
	if r.note != "" {
		s += " · " + r.note
	}
	return s
}

// resultItems returns the root items of r: up to r.root rows.
func (p *Plugin) resultItems(r *result, text string) []plugin.Item {
	l := p.h.I18n()
	detail := r.detail()
	items := make([]plugin.Item, 0, r.root)
	for i, x := range r.rows[:r.root] {
		items = append(items, plugin.Item{
			ID:       "convert:" + x.id,
			Title:    x.title,
			Subtitle: r.subtitle(x),
			Icon:     plugin.Icon{Name: "calculator"},
			Group:    l.T("group"),
			Score:    1 - float64(float64(i)*0.01),
			Actions:  append(p.rowActions(x), p.conversionsAction(text)),
			Detail:   detail,
		})
	}
	return items
}

// rowActions copy, copy with unit (the secondary action, Ctrl+Enter) and
// paste a row.
func (p *Plugin) rowActions(x row) []plugin.Action {
	l := p.h.I18n()
	return []plugin.Action{
		{ID: "copy", Title: l.T("action.copyValue"), Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: x.raw}},
		{ID: "copy-unit", Title: l.T("action.copyWithUnit"), Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: x.full}},
		{ID: "paste", Title: l.T("action.pasteValue"), Shortcut: "Ctrl+Shift+V", Do: plugin.ActionDo{Kind: plugin.ActionPaste, Target: x.raw}},
	}
}

func (p *Plugin) conversionsAction(text string) plugin.Action {
	return plugin.Action{ID: "conversions", Title: p.h.I18n().T("action.showConversions"), Shortcut: "Ctrl+Shift+A",
		Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewConversions, Payload: plugin.MustPayload(payload{Text: text})}}
}

// View implements plugin.ViewProvider: the "conversions" view lists every
// conversion of the text in its payload, with the detail pane.
func (p *Plugin) View(ctx context.Context, req plugin.ViewRequest) (plugin.View, error) {
	if req.ID != viewConversions {
		return plugin.View{}, fmt.Errorf("calc: unknown view %q", req.ID)
	}
	var pl payload
	if err := unmarshalPayload(req.Payload, &pl); err != nil {
		return plugin.View{}, err
	}
	if err := ctx.Err(); err != nil {
		return plugin.View{}, err
	}
	l := p.h.I18n()
	v := plugin.View{Kind: plugin.ViewList, Title: l.T("view.conversions.title"), ShowDetail: true, FilterLocally: true,
		EmptyText: l.T("view.conversions.empty")}
	r, _, err := p.evaluate(pl.Text, p.settings())
	if err != nil || r == nil {
		return v, nil
	}
	v.Title = r.source
	detail := r.detail()
	var items []plugin.Item
	for _, x := range r.rows {
		items = append(items, plugin.Item{
			ID: "convert:" + x.id, Title: x.title, Subtitle: r.subtitle(x),
			Icon: plugin.Icon{Name: "calculator"}, Actions: p.rowActions(x), Detail: detail,
		})
	}
	v.Sections = []plugin.Section{{Items: items}}
	return v, nil
}

// unitResult builds the rows of a calculation with units: the target
// unit, the suggested conversions of a bare quantity, or the result in its
// natural unit; then the other units of its category for the detail.
func (p *Plugin) unitResult(c calculation, s settings, env *evalEnv) (*result, error) {
	v := c.val
	l := p.h.I18n()
	r := &result{source: v.text}
	money := v.dim == units.Of(units.Currency, 1)
	if money {
		if rates := p.currentRates(); rates != nil {
			r.note = l.T("currency.ratesFrom", "date", rates.Updated.In(p.opts.Location).Format("2006-01-02"))
			r.markdown = attribution(l, rates.Provider)
		}
	}
	mk := func(u *uexpr) (row, error) {
		x, isDelta, err := inUnit(v, u)
		if err != nil {
			return row{}, err
		}
		raw := formatSig(x, s.sig)
		if money {
			raw = formatMoney(x, s.sig)
		}
		shown := display(l, raw)
		if isDelta {
			shown = "Δ" + shown
		}
		return row{id: u.text, title: withUnit(shown, u.text), raw: raw, full: withUnit(raw, u.text), label: u.text, shown: shown}, nil
	}
	cat := v.u.cat
	if c.target != nil && c.target.cat != units.CatNone {
		cat = c.target.cat // "1 in in px": typography
	}
	cands := candidates(v, cat, s, env)
	var first []*uexpr
	switch {
	case c.target != nil:
		first = []*uexpr{c.target}
	case c.listing:
		first = suggest(v, cands)
		if len(first) == 0 {
			first = []*uexpr{v.u}
		}
	default:
		first = []*uexpr{naturalUnit(v, cands)}
	}
	seen := map[string]bool{}
	for _, u := range append(first, cands...) {
		if seen[u.text] {
			continue
		}
		x, err := mk(u)
		if err != nil {
			if len(r.rows) < len(first) {
				return nil, err
			}
			continue
		}
		seen[u.text] = true
		r.rows = append(r.rows, x)
	}
	r.root = min(len(first), maxSuggestions)
	return r, nil
}

// attribution credits the rate provider (Markdown); ExchangeRate-API's
// terms ask for it.
func attribution(l plugin.Localizer, provider string) string {
	if provider == providerFrankfurter {
		return l.T("currency.attribution.frankfurter")
	}
	return l.T("currency.attribution.erapi")
}

// unmarshalPayload decodes an action payload.
func unmarshalPayload(data plugin.Payload, v any) error {
	if len(data) == 0 {
		return fmt.Errorf("calc: missing payload")
	}
	return json.Unmarshal(data, v)
}

// candidates are the units of v's category that v converts to, in the
// preferred order: the favorite currencies for money.
func candidates(v *value, cat units.Category, s settings, env *evalEnv) []*uexpr {
	var list []*units.Unit
	tab := units.Default()
	if cat == units.CatCurrency {
		for _, code := range s.currencies {
			if u := tab.BySym(strings.ToUpper(strings.TrimSpace(code))); u != nil && u.Cat == units.CatCurrency {
				list = append(list, u)
			}
		}
	} else {
		chinese := s.chinese || v.u.unit != nil && v.u.unit.Chinese
		list = units.Listing(cat, s.imperial, chinese)
	}
	var out []*uexpr
	for _, u := range list {
		if u.Dim != v.dim && !units.Reciprocal(v.dim, u.Dim) {
			continue
		}
		if u.Offset != nil && u.Offset.Sign() != 0 && !v.abs && v.dim == u.Dim && v.u.offset == nil {
			continue // a kelvin difference is not shown in ℃
		}
		e, err := single(u, env.units)
		if err != nil {
			continue // no rate for this currency
		}
		out = append(out, e)
	}
	return out
}

// suggest picks up to maxSuggestions conversions of a bare quantity: other
// units than its own, in the preferred order or, for data sizes and
// durations, the ones with the most readable numbers.
func suggest(v *value, cands []*uexpr) []*uexpr {
	var out []*uexpr
	for _, u := range cands {
		if u.text == v.u.text || units.Equal(u.factor, v.u.factor) && u.dim == v.u.dim && sameOffset(u, v.u) {
			continue
		}
		out = append(out, u)
	}
	switch {
	case v.u.cat == units.CatCurrency: // the favorites, in order
	case units.ByMagnitude(v.u.cat):
		slices.SortStableFunc(out, func(a, b *uexpr) int {
			return cmpFloat(readability(v, a), readability(v, b))
		})
	default:
		// Keep the preferred order, but numbers like 1,000,000 cm last.
		slices.SortStableFunc(out, func(a, b *uexpr) int {
			return cmpBool(hardToRead(readability(v, a)), hardToRead(readability(v, b)))
		})
	}
	return out[:min(len(out), maxSuggestions)]
}

// maxReadability: numbers from 10,000 up or 0.001 down are hard to read
// (see readability).
const maxReadability = 3

// hardToRead reports whether a readability is past maxReadability. 10,000
// is exactly at it, but math.Log10 may be off by an ulp (it is on arm64), so
// the comparison leaves a margin.
func hardToRead(r float64) bool { return r > maxReadability+1e-9 }

func cmpBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// withUnit joins a number and a unit symbol: "10 km", but "90°".
func withUnit(num, sym string) string {
	switch sym {
	case "°", "′", "″":
		return num + sym
	}
	return num + " " + sym
}

func sameOffset(a, b *uexpr) bool {
	if a.offset == nil || b.offset == nil {
		return a.offset == nil && b.offset == nil
	}
	return a.offset.Cmp(b.offset) == 0
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// readability is how far the number of v in u is from 10 on a log scale;
// smaller is better.
func readability(v *value, u *uexpr) float64 {
	x, _, err := inUnit(v, u)
	if err != nil || x.Sign() == 0 {
		return math.Inf(1)
	}
	f, _ := new(big.Float).Abs(x).Float64()
	return math.Abs(math.Log10(f) - 1)
}

// naturalUnit is the unit a computed quantity is shown in: the unit it was
// written in ("1 mi + 200 m" in miles), a named unit with the same factor
// ("kg·m/s²" is N), or the most readable unit of its category ("5 GB /
// 100 Mbps" in minutes).
func naturalUnit(v *value, cands []*uexpr) *uexpr {
	if v.u.unit != nil || v.u.cat == units.CatNone {
		return v.u
	}
	var best *uexpr
	score := math.Inf(1)
	for _, u := range cands {
		if u.dim != v.dim {
			continue
		}
		if !units.ByMagnitude(v.u.cat) {
			return u
		}
		if s := readability(v, u); s < score {
			best, score = u, s
		}
	}
	if best == nil {
		return v.u
	}
	return best
}
