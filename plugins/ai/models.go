package ai

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// Model and reasoning effort choices that come from the backend's CLI
// (R109, docs/ai.md §25): Codex lists its models, which depend on the
// account and the CLI version, each with the efforts it accepts; Claude Code
// takes one --effort for every model. The settings page loads them as the
// dynamic options of the preferences; a chat passes an effort only when the
// chosen model accepts it, so that a combination the CLI would refuse never
// breaks a chat.

// PreferenceOptions implements plugin.PreferenceOptionsProvider: the
// choices of the model and effort preferences whose options come from the
// backend. What could not be had is explained under the field (the error),
// with the choices that remain.
func (p *Plugin) PreferenceOptions(ctx context.Context, name string) ([]plugin.Option, error) {
	// The host gives up after a while (10 s); a list that takes longer is
	// still fetched, for the next time the page opens.
	ctx, cancel := context.WithTimeout(ctx, optionsWait)
	defer cancel()
	p.applyPrograms() // the program just chosen on the page
	for _, o := range p.be.opts {
		switch {
		case o.DynamicModels && name == modelPref(o):
			return p.modelOptions(ctx, o)
		case o.Efforts && name == effortPref(o):
			return p.effortOptions(ctx, o)
		}
	}
	return nil, nil
}

// optionsWait is how long the settings page waits for a model list.
var optionsWait = 8 * time.Second

// optionsNote is a note shown under a preference's field.
type optionsNote struct{ msg string }

func (n *optionsNote) Error() string { return n.msg }

// modelOptions are "Default", then the models the backend lists; a chosen
// model that is no longer listed stays, marked as such.
func (p *Plugin) modelOptions(ctx context.Context, o BackendOption) ([]plugin.Option, error) {
	l := p.h.I18n()
	opts := []plugin.Option{{Value: "", Title: l.T("pref.model.default")}}
	var note error
	list, err := p.models(ctx, o)
	if err != nil {
		note = p.modelsNote(o, err)
	} else {
		for _, m := range list.Models {
			opts = append(opts, plugin.Option{Value: m.ID, Title: m.Name})
		}
		if list.Bundled {
			note = &optionsNote{l.T("pref.models.bundled", "backend", o.Backend.Name())}
		}
	}
	if cur := p.h.Preferences().String(modelPref(o)); cur != "" && !hasOption(opts, cur) {
		opts = append(opts, plugin.Option{Value: cur, Title: l.T("pref.model.notListed", "model", cur)})
	}
	return opts, note
}

// effortOptions are "Default", then the levels the chosen model accepts
// (with the default model, those every listed model accepts), or the levels
// the CLI takes; a chosen level outside them stays, marked as not passed.
func (p *Plugin) effortOptions(ctx context.Context, o BackendOption) ([]plugin.Option, error) {
	l := p.h.I18n()
	opts := []plugin.Option{{Value: "", Title: l.T("pref.model.default")}}
	var levels []string
	var note error
	if o.DynamicModels {
		list, err := p.models(ctx, o)
		if err != nil {
			note = p.modelsNote(o, err)
		} else {
			model := p.h.Preferences().String(modelPref(o))
			var known bool
			if levels, known = list.EffortsFor(model); !known {
				note = &optionsNote{l.T("pref.effort.modelNotListed", "model", model)}
			}
		}
	} else if el, ok := o.Backend.(backend.EffortLister); ok {
		var err error
		levels, err = el.Efforts(ctx)
		switch {
		case err != nil:
			note = p.modelsNote(o, err)
		case len(levels) == 0:
			note = &optionsNote{l.T("pref.effort.noFlag", "backend", o.Backend.Name())}
		}
	}
	for _, lv := range levels {
		opts = append(opts, plugin.Option{Value: lv, Title: effortTitle(l, lv)})
	}
	if cur := p.h.Preferences().String(effortPref(o)); cur != "" && !hasOption(opts, cur) {
		opts = append(opts, plugin.Option{Value: cur, Title: l.T("pref.effort.unsupported", "level", effortTitle(l, cur))})
	}
	return opts, note
}

// models lists the models of o's backend.
func (p *Plugin) models(ctx context.Context, o BackendOption) (backend.ModelList, error) {
	ml, ok := o.Backend.(backend.ModelLister)
	if !ok {
		return backend.ModelList{}, &backend.Error{Kind: backend.ErrFailed}
	}
	return ml.Models(ctx)
}

// modelsNote explains why o's backend could not list its choices.
func (p *Plugin) modelsNote(o BackendOption, err error) error {
	l := p.h.I18n()
	name := o.Backend.Name()
	if errors.Is(err, context.DeadlineExceeded) {
		return &optionsNote{l.T("pref.models.pending", "backend", name)}
	}
	switch backend.KindOf(err) {
	case backend.ErrNotInstalled:
		return &optionsNote{l.T("pref.models.notInstalled", "backend", name)}
	case backend.ErrVersion:
		return &optionsNote{l.T("pref.models.tooOld", "backend", name)}
	}
	return &optionsNote{l.T("pref.models.failed", "backend", name, "err", errorText(err))}
}

// effort is the reasoning effort a chat passes to its backend with model
// ("" for the default): the chosen level when the backend takes one and,
// for a backend that lists its models, the model accepts it. A level the
// model does not accept, or that cannot be checked, is left out (the
// preference says so) instead of failing the chat. Claude Code's adapter
// leaves out a level its version does not list.
func (p *Plugin) effort(ctx context.Context, sess Session, model string) string {
	id, _ := p.backendOf(sess)
	o, ok := p.be.option(id)
	if !ok || !o.Efforts {
		return ""
	}
	e := strings.TrimSpace(p.h.Preferences().String(effortPref(o)))
	if e == "" || !o.DynamicModels {
		return e
	}
	list, err := p.models(ctx, o)
	if err != nil {
		return ""
	}
	if levels, _ := list.EffortsFor(model); !slices.Contains(levels, e) {
		return ""
	}
	return e
}

// effortTitle is the localized name of a reasoning effort level; a level
// golauncher does not know shows as it is.
func effortTitle(l plugin.Localizer, level string) string {
	switch level {
	case "none":
		return l.T("pref.effort.none")
	case "minimal":
		return l.T("pref.effort.minimal")
	case "low":
		return l.T("pref.effort.low")
	case "medium":
		return l.T("pref.effort.medium")
	case "high":
		return l.T("pref.effort.high")
	case "xhigh":
		return l.T("pref.effort.xhigh")
	case "max":
		return l.T("pref.effort.max")
	case "ultra":
		return l.T("pref.effort.ultra")
	}
	return level
}

func hasOption(opts []plugin.Option, v string) bool {
	return slices.ContainsFunc(opts, func(o plugin.Option) bool { return o.Value == v })
}
