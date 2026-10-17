// Package calc is the built-in calculator plugin. It evaluates math
// expressions and converts units typed in the root search and offers the
// result as an item.
//
// Expressions use a locale-independent syntax ("." as decimal point, English
// function names) and are evaluated with 128-bit math/big floats. Numbers
// may carry units ("10 km in mi", "5 GB / 100 Mbps", "30 ℃"); see
// docs/unit-conversion.md. Results are displayed with the locale's digit
// grouping but copied and pasted in their plain machine form.
package calc

import (
	"cmp"
	"context"
	"embed"
	"errors"
	"io/fs"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/calc/units"
)

// ID is the plugin's manifest ID.
const ID = "builtin.calc"

// Preference names.
const (
	prefUnitSystem   = "unitSystem"
	prefChinese      = "chineseUnits"
	prefSigDigits    = "significantDigits"
	prefDPI          = "dpi"
	prefRemBase      = "remBase"
	prefProvider     = "currencyProvider"
	prefCurrencies   = "currencies"
	prefYenMeansJPY  = "yenMeansJPY"
	systemMetric     = "metric"
	systemImperial   = "imperial"
	viewConversions  = "conversions"
	maxSuggestions   = 5
	defaultSigDigits = 6
)

var defaultCurrencies = []string{"USD", "CNY", "EUR", "JPY", "GBP", "HKD"}

//go:embed locales/*.json
var locales embed.FS

// Options change where the plugin gets exchange rates and the time zone of
// timestamps; tests and glctl replace them.
type Options struct {
	// Client downloads exchange rates (default: a client with a 10 s
	// timeout).
	Client *http.Client
	// ERAPIURL and FrankfurterURL are the rate endpoints (default
	// DefaultERAPIURL and DefaultFrankfurterURL).
	ERAPIURL, FrankfurterURL string
	// UserAgent is sent with rate requests (default "golauncher/dev").
	UserAgent string
	// Rates, when set, are used instead of downloading (glctl fixtures).
	Rates *Rates
	// Offline never downloads rates (glctl fake mode without fixtures).
	Offline bool
	// Location is the local time zone of timestamps (default time.Local).
	Location *time.Location
}

// Plugin is the calculator plugin.
type Plugin struct {
	opts Options
	h    plugin.Host

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	ratesMu    sync.Mutex
	closed     bool
	rates      *Rates
	cacheRead  bool
	refreshing bool
	lastTry    time.Time
	lastErr    error
}

var (
	_ plugin.Plugin         = (*Plugin)(nil)
	_ plugin.RootProvider   = (*Plugin)(nil)
	_ plugin.ViewProvider   = (*Plugin)(nil)
	_ plugin.LocaleProvider = (*Plugin)(nil)
)

// New returns the calculator plugin, downloading exchange rates from the
// network.
func New() *Plugin { return NewWithOptions(Options{}) }

// NewWithOptions returns the plugin with o.
func NewWithOptions(o Options) *Plugin {
	if o.Client == nil {
		o.Client = &http.Client{Timeout: fetchTimeout}
	}
	o.ERAPIURL = cmp.Or(o.ERAPIURL, DefaultERAPIURL)
	o.FrankfurterURL = cmp.Or(o.FrankfurterURL, DefaultFrankfurterURL)
	o.UserAgent = cmp.Or(o.UserAgent, "golauncher/dev")
	if o.Location == nil {
		o.Location = time.Local
	}
	return &Plugin{opts: o}
}

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:          ID,
		Name:        "%plugin.name%",
		Description: "%plugin.description%",
		Version:     "1.1.0",
		Icon:        plugin.Icon{Name: "calculator"},
		Permissions: []plugin.Permission{plugin.PermNetwork, plugin.PermSelection},
		Preferences: []plugin.Preference{
			{Name: prefUnitSystem, Type: plugin.PrefSelect, Title: "%pref.unitSystem.title%",
				Description: "%pref.unitSystem.description%", Default: plugin.MustPayload(systemMetric), Group: "%pref.group.units%",
				Options: []plugin.Option{{Value: systemMetric, Title: "%pref.unitSystem.metric%"}, {Value: systemImperial, Title: "%pref.unitSystem.imperial%"}}},
			{Name: prefChinese, Type: plugin.PrefBool, Title: "%pref.chineseUnits.title%",
				Description: "%pref.chineseUnits.description%", Default: plugin.MustPayload(false), Group: "%pref.group.units%"},
			{Name: prefSigDigits, Type: plugin.PrefNumber, Title: "%pref.significantDigits.title%",
				Description: "%pref.significantDigits.description%", Default: plugin.MustPayload(defaultSigDigits), Group: "%pref.group.units%"},
			{Name: prefDPI, Type: plugin.PrefNumber, Title: "%pref.dpi.title%",
				Description: "%pref.dpi.description%", Default: plugin.MustPayload(96), Group: "%pref.group.units%"},
			{Name: prefRemBase, Type: plugin.PrefNumber, Title: "%pref.remBase.title%",
				Description: "%pref.remBase.description%", Default: plugin.MustPayload(16), Group: "%pref.group.units%"},
			{Name: prefProvider, Type: plugin.PrefSelect, Title: "%pref.currencyProvider.title%",
				Description: "%pref.currencyProvider.description%", Default: plugin.MustPayload(providerERAPI), Group: "%pref.group.currency%",
				Options: []plugin.Option{
					{Value: providerERAPI, Title: "%pref.currencyProvider.erapi%"},
					{Value: providerFrankfurter, Title: "%pref.currencyProvider.frankfurter%"},
					{Value: providerOff, Title: "%pref.currencyProvider.off%"},
				}},
			{Name: prefCurrencies, Type: plugin.PrefStringList, Title: "%pref.currencies.title%",
				Description: "%pref.currencies.description%", Default: plugin.MustPayload(defaultCurrencies), Group: "%pref.group.currency%"},
			{Name: prefYenMeansJPY, Type: plugin.PrefBool, Title: "%pref.yenMeansJPY.title%",
				Description: "%pref.yenMeansJPY.description%", Default: plugin.MustPayload(false), Group: "%pref.group.currency%"},
		},
	}
}

// Init implements plugin.Plugin. The unit table is built in the background
// so that the first search does not wait for it.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.ratesMu.Lock()
	p.closed = false // Init after Close starts again (docs/plugin-guide.md "Lifecycle")
	p.ratesMu.Unlock()
	p.h = h
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.wg.Add(1)
	h.Go(func() {
		defer p.wg.Done()
		units.Default()
	})
	return nil
}

// Close implements plugin.Plugin. It cancels a running rate refresh.
func (p *Plugin) Close() error {
	p.ratesMu.Lock()
	p.closed = true
	p.ratesMu.Unlock()
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	return nil
}

// Locales implements plugin.LocaleProvider.
func (p *Plugin) Locales() fs.FS {
	sub, err := fs.Sub(locales, "locales")
	if err != nil {
		panic(err)
	}
	return sub
}

// settings are the preference values.
type settings struct {
	imperial, chinese bool
	sig               int
	dpi, rem          float64
	provider          string
	currencies        []string
	yenJPY            bool
}

func (p *Plugin) settings() settings {
	pr := p.h.Preferences()
	s := settings{
		imperial:   pr.String(prefUnitSystem) == systemImperial,
		chinese:    pr.Bool(prefChinese),
		sig:        pr.Int(prefSigDigits),
		dpi:        float64(pr.Int(prefDPI)),
		rem:        float64(pr.Int(prefRemBase)),
		provider:   cmp.Or(pr.String(prefProvider), providerERAPI),
		currencies: pr.Strings(prefCurrencies),
		yenJPY:     pr.Bool(prefYenMeansJPY),
	}
	if s.sig < 1 || s.sig > 30 {
		s.sig = defaultSigDigits
	}
	if len(s.currencies) == 0 {
		s.currencies = defaultCurrencies
	}
	return s
}

// evalEnv returns the evaluation environment for s, with the cached rates.
func (p *Plugin) evalEnv(s settings) *evalEnv {
	env := &evalEnv{units: &units.Env{DPI: s.dpi, RemBase: s.rem}, yenJPY: s.yenJPY, noCurrency: s.provider == providerOff}
	if !env.noCurrency {
		env.units.Rate = func(code string) (*big.Float, error) { return p.currentRates().rate(code) }
	}
	return env
}

// RootItems implements plugin.RootProvider. It returns the result of an
// expression or conversion, the suggested conversions of a bare quantity
// ("10 km"), and nothing for text that does not look like a calculation.
func (p *Plugin) RootItems(ctx context.Context, q plugin.Query) ([]plugin.Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := p.settings()
	r, c, err := p.evaluate(q.Text, s)
	if err != nil {
		if errors.Is(err, units.ErrNoRates) {
			return []plugin.Item{p.noRatesItem()}, nil
		}
		return nil, nil
	}
	if r == nil {
		return []plugin.Item{p.plainItem(c)}, nil
	}
	return p.resultItems(r, q.Text), nil
}

// evaluate computes text: a special conversion (color, timestamp, size) or
// a calculation. A nil result with no error is a plain math result in c.
func (p *Plugin) evaluate(text string, s settings) (*result, calculation, error) {
	if r := p.special(text, s); r != nil {
		return r, calculation{}, nil
	}
	env := p.evalEnv(s)
	c, err := calculateEnv(text, env)
	if env.usedCurrency {
		p.maybeRefresh(s.provider)
	}
	if err != nil {
		return nil, c, err
	}
	if c.val == nil {
		return nil, c, nil
	}
	r, err := p.unitResult(c, s, env)
	return r, c, err
}

// special returns the result of a color, timestamp or clothing/shoe size
// conversion, or nil.
func (p *Plugin) special(text string, s settings) *result {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > maxInput {
		return nil
	}
	if r := p.colorResult(text); r != nil {
		return r
	}
	if r := p.timeResult(text); r != nil {
		return r
	}
	return p.sizeResult(text)
}

// plainItem is the item of a math result without units.
func (p *Plugin) plainItem(c calculation) plugin.Item {
	l := p.h.I18n()
	title := c.raw
	if c.base == "" || c.base == "dec" {
		title = display(l, c.raw)
	}
	return plugin.Item{
		ID:       "result",
		Title:    title,
		Subtitle: c.expr,
		Icon:     plugin.Icon{Name: "calculator"},
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
	}
}

// noRatesItem tells the user that exchange rates are not there yet.
func (p *Plugin) noRatesItem() plugin.Item {
	l := p.h.I18n()
	sub := l.T("currency.noRates.subtitle")
	if p.refreshFailed() || p.opts.Offline {
		sub = l.T("currency.noRates.failed")
	}
	return plugin.Item{
		ID:       "no-rates",
		Title:    l.T("currency.noRates.title"),
		Subtitle: sub,
		Icon:     plugin.Icon{Name: "calculator", Tint: plugin.TintMuted},
		Group:    l.T("group"),
		Score:    1,
	}
}

// display groups the digits of a machine-form decimal for the locale. The
// exponent of scientific notation is left as is.
func display(l plugin.Localizer, raw string) string {
	if mant, exp, ok := strings.Cut(raw, "e"); ok {
		return l.GroupDigits(mant) + "e" + exp
	}
	return l.GroupDigits(raw)
}
