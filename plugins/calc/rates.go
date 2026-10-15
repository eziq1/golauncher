package calc

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eziq1/golauncher/plugins/calc/units"
)

// Exchange rates (docs/unit-conversion.md §5.3). Rates are kept in memory
// and in CacheDir/rates.json. RootItems only reads them; a background
// refresh (Host.Go) runs on first use and when they are older than
// rateMaxAge, and asks the host to pull the root results again when done.

// Currency providers (preference currencyProvider).
const (
	providerERAPI       = "er-api"
	providerFrankfurter = "frankfurter"
	providerOff         = "off"
)

// Default endpoints. Both are free and need no API key (checked 2026-10-15;
// api.frankfurter.app redirects to api.frankfurter.dev/v1).
const (
	DefaultERAPIURL       = "https://open.er-api.com/v6/latest/USD"
	DefaultFrankfurterURL = "https://api.frankfurter.dev/v1/latest?base=USD"
)

const (
	rateMaxAge   = 6 * time.Hour
	retryAfter   = 5 * time.Minute // after a failed refresh
	fetchTimeout = 10 * time.Second
	maxBody      = 1 << 20
	ratesFile    = "rates.json"
)

// Rates are exchange rates against the US dollar.
type Rates struct {
	Provider string    `json:"provider"`
	Updated  time.Time `json:"updated"` // when the provider published them
	Fetched  time.Time `json:"fetched"` // when they were downloaded
	// Rates maps ISO 4217 codes to units per US dollar.
	Rates map[string]float64 `json:"rates"`
}

// rate returns units of code per US dollar.
func (r *Rates) rate(code string) (*big.Float, error) {
	if r == nil || len(r.Rates) == 0 {
		return nil, units.ErrNoRates
	}
	v, ok := r.Rates[code]
	if !ok || v <= 0 {
		return nil, fmt.Errorf("%w %s", units.ErrNoRate, code)
	}
	return newF().SetFloat64(v), nil
}

// erAPIResponse is the body of open.er-api.com/v6/latest/USD.
type erAPIResponse struct {
	Result         string             `json:"result"`
	TimeLastUpdate int64              `json:"time_last_update_unix"`
	BaseCode       string             `json:"base_code"`
	Rates          map[string]float64 `json:"rates"`
}

// frankfurterResponse is the body of api.frankfurter.dev/v1/latest.
type frankfurterResponse struct {
	Base  string             `json:"base"`
	Date  string             `json:"date"`
	Rates map[string]float64 `json:"rates"`
}

// parseERAPI decodes an open.er-api.com response.
func parseERAPI(data []byte) (*Rates, error) {
	var r erAPIResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if r.Result != "success" || r.BaseCode != "USD" || len(r.Rates) == 0 {
		return nil, fmt.Errorf("calc: er-api: result %q, base %q, %d rates", r.Result, r.BaseCode, len(r.Rates))
	}
	return &Rates{Provider: providerERAPI, Updated: time.Unix(r.TimeLastUpdate, 0).UTC(), Rates: r.Rates}, nil
}

// parseFrankfurter decodes a Frankfurter response.
func parseFrankfurter(data []byte) (*Rates, error) {
	var r frankfurterResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	day, err := time.Parse(time.DateOnly, r.Date)
	if err != nil || r.Base != "USD" || len(r.Rates) == 0 {
		return nil, fmt.Errorf("calc: frankfurter: base %q, date %q, %d rates", r.Base, r.Date, len(r.Rates))
	}
	r.Rates["USD"] = 1
	return &Rates{Provider: providerFrankfurter, Updated: day, Rates: r.Rates}, nil
}

// FixtureRates reads rates saved from open.er-api.com (glctl fixtures).
func FixtureRates(path string) (*Rates, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	r, err := parseERAPI(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	r.Fetched = r.Updated
	return r, nil
}

// currentRates returns the rates in memory, reading the cache file the
// first time. It never touches the network.
func (p *Plugin) currentRates() *Rates {
	p.ratesMu.Lock()
	defer p.ratesMu.Unlock()
	if p.opts.Rates != nil {
		return p.opts.Rates
	}
	if !p.cacheRead {
		p.cacheRead = true
		if data, err := os.ReadFile(filepath.Join(p.h.CacheDir(), ratesFile)); err == nil {
			var r Rates
			if err := json.Unmarshal(data, &r); err == nil && len(r.Rates) > 0 {
				p.rates = &r
			} else {
				p.h.Logger().Warn("calc: ignoring bad rates cache", "err", err)
			}
		}
	}
	return p.rates
}

// refreshFailed reports whether the last refresh failed.
func (p *Plugin) refreshFailed() bool {
	p.ratesMu.Lock()
	defer p.ratesMu.Unlock()
	return p.lastErr != nil
}

// maybeRefresh starts a background refresh of the rates if they are
// missing or older than rateMaxAge, none is running, and the last attempt
// did not fail within retryAfter.
func (p *Plugin) maybeRefresh(provider string) {
	if p.opts.Rates != nil || p.opts.Offline || provider == providerOff {
		return
	}
	now := p.h.Now()
	p.ratesMu.Lock()
	defer p.ratesMu.Unlock()
	switch {
	case p.closed || p.refreshing:
		return
	case p.rates != nil && now.Sub(p.rates.Fetched) < rateMaxAge:
		return
	case p.lastErr != nil && now.Sub(p.lastTry) < retryAfter:
		return
	}
	p.refreshing, p.lastTry = true, now
	p.wg.Add(1)
	p.h.Go(func() {
		defer p.wg.Done()
		defer func() {
			p.ratesMu.Lock()
			p.refreshing = false
			p.ratesMu.Unlock()
		}()
		r, err := p.fetchRates(p.ctx, provider)
		p.ratesMu.Lock()
		p.lastErr = err
		if err == nil {
			r.Fetched = p.h.Now()
			p.rates = r
		}
		p.ratesMu.Unlock()
		if err != nil {
			p.h.Logger().Warn("calc: refreshing exchange rates", "err", err)
		} else if err := p.saveRates(r); err != nil {
			p.h.Logger().Warn("calc: saving exchange rates", "err", err)
		}
		p.h.Invalidate("") // show the rates, or that they failed
	})
}

// saveRates writes the cache file atomically.
func (p *Plugin) saveRates(r *Rates) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	path := filepath.Join(p.h.CacheDir(), ratesFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// fetchRates downloads rates from provider, then from the other one.
func (p *Plugin) fetchRates(ctx context.Context, provider string) (*Rates, error) {
	order := []string{providerERAPI, providerFrankfurter}
	if provider == providerFrankfurter {
		order = []string{providerFrankfurter, providerERAPI}
	}
	var errs []error
	for _, name := range order {
		url, parse := p.opts.ERAPIURL, parseERAPI
		if name == providerFrankfurter {
			url, parse = p.opts.FrankfurterURL, parseFrankfurter
		}
		data, err := p.get(ctx, url)
		if err == nil {
			var r *Rates
			if r, err = parse(data); err == nil {
				return r, nil
			}
		}
		errs = append(errs, fmt.Errorf("%s: %w", name, err))
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errors.Join(errs...)
}

// get downloads url with a short timeout.
func (p *Plugin) get(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", p.opts.UserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := p.opts.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "json") && len(data) > 0 && data[0] != '{' {
		return nil, fmt.Errorf("not JSON: %q", resp.Header.Get("Content-Type"))
	}
	return data, nil
}
