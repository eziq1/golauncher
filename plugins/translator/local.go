package translator

import (
	"context"
	"encoding/json/v2"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/translator/engines"
)

// Phase 3: custom engines (an HTTP template per line of a preference), a
// local model through Ollama, and the history's callbacks.

// customEngines are the user's engines, from prefCustom; lines that do not
// parse are skipped (and logged without their content: they may hold a
// key).
func (p *Plugin) customEngines(env engines.Env) []engines.Engine {
	var out []engines.Engine
	seen := map[string]bool{}
	for i, line := range p.h.Preferences().Strings(prefCustom) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		c, err := engines.ParseCustom(line)
		if err != nil {
			p.h.Logger().Warn("translator: custom engine skipped", "line", i+1, "err", err)
			continue
		}
		id := strings.ToLower(c.ID())
		if seen[id] {
			continue
		}
		seen[id] = true
		if p.opts.Engine != nil {
			if e := p.opts.Engine(c.ID()); e != nil {
				out = append(out, e)
			}
			continue
		}
		c.Env = env
		out = append(out, &c)
	}
	return out
}

// firstOf is the first non-empty string.
func firstOf(s ...string) string {
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			return strings.TrimSpace(x)
		}
	}
	return ""
}

// PreferenceOptions implements plugin.PreferenceOptionsProvider: the models
// the Ollama server has, after "None" (Ollama unused). An unreachable server
// is reported under the field.
func (p *Plugin) PreferenceOptions(ctx context.Context, name string) ([]plugin.Option, error) {
	if name != prefOllamaModel {
		return nil, nil
	}
	l := p.h.I18n()
	opts := []plugin.Option{{Value: "", Title: l.T("pref.ollamaModel.none")}}
	o := &engines.Ollama{Env: engines.Env{Client: p.opts.Client}, BaseURL: firstOf(p.opts.BaseURLs[engOllama], p.h.Preferences().String(prefOllamaURL))}
	models, err := o.Models(ctx)
	if err != nil {
		return opts, &optionsError{msg: l.T("pref.ollamaModel.unavailable", "url", o.BaseURL)}
	}
	cur := p.h.Preferences().String(prefOllamaModel)
	for _, m := range models {
		opts = append(opts, plugin.Option{Value: m, Title: m})
	}
	if cur != "" && !containsOption(opts, cur) {
		// Keep the chosen model listed even when the server lost it.
		opts = append(opts, plugin.Option{Value: cur, Title: cur})
	}
	return opts, nil
}

// optionsError is a message shown under a preference.
type optionsError struct{ msg string }

func (e *optionsError) Error() string { return e.msg }

func containsOption(opts []plugin.Option, v string) bool {
	for _, o := range opts {
		if o.Value == v {
			return true
		}
	}
	return false
}

// historyAction runs the history's callbacks.
func (p *Plugin) historyAction(a plugin.ActionDo) (plugin.Result, error) {
	l := p.h.I18n()
	switch a.Target {
	case cbUse:
		var u usePayload
		if err := json.Unmarshal(a.Payload, &u); err != nil {
			return plugin.Result{}, err
		}
		if p.historyOn() {
			if err := p.record(u.Entry); err != nil {
				p.h.Logger().Warn("translator: cannot record the history", "err", err)
			}
		}
		do := u.Do
		if do.Kind != plugin.ActionCopyText && do.Kind != plugin.ActionPaste {
			return plugin.Result{}, nil
		}
		return plugin.Result{Do: &do}, nil
	case cbForget:
		var e historyEntry
		if err := json.Unmarshal(a.Payload, &e); err != nil {
			return plugin.Result{}, err
		}
		return plugin.Result{Refresh: true}, p.forget(e)
	case cbClearHistory:
		if err := p.clearHistory(); err != nil {
			return plugin.Result{}, err
		}
		return plugin.Result{Refresh: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("toast.historyCleared")}}, nil
	}
	return plugin.Result{}, errUnknownAction
}
