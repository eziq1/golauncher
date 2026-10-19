package backend

import (
	"context"
	"slices"
)

// The models and reasoning efforts a backend's CLI offers (R109, docs/ai.md
// §25): Codex lists its models (`codex debug models`), which depend on the
// account and the CLI version, each with the reasoning efforts it accepts;
// Claude Code takes one --effort for every model, with the levels its
// version knows.

// Model is a model a backend's CLI lists.
type Model struct {
	// ID is what is passed as the model (Codex's slug); Name is the CLI's
	// display name (a product name: not translated).
	ID, Name string
	// Efforts are the reasoning effort levels the model accepts, in the
	// CLI's order (none: it takes no effort level); DefaultEffort is the one
	// it uses by default.
	Efforts       []string
	DefaultEffort string
}

// ModelList is the models a backend's CLI offers, in the order to show them.
type ModelList struct {
	Models []Model
	// Bundled: the list for the account could not be had, so these are the
	// models built into the CLI.
	Bundled bool
}

// Find returns the model with id.
func (l ModelList) Find(id string) (Model, bool) {
	i := slices.IndexFunc(l.Models, func(m Model) bool { return m.ID == id })
	if i < 0 {
		return Model{}, false
	}
	return l.Models[i], true
}

// EffortsFor returns the effort levels usable with model: the model's own,
// or for "" (the CLI's default model, which the list does not name) the
// levels every listed model accepts, so that whichever model the CLI picks
// takes it. ok is false when model is not in the list.
func (l ModelList) EffortsFor(model string) (levels []string, ok bool) {
	if model != "" {
		m, ok := l.Find(model)
		return slices.Clone(m.Efforts), ok
	}
	for i, m := range l.Models {
		if i == 0 {
			levels = slices.Clone(m.Efforts)
			continue
		}
		levels = slices.DeleteFunc(levels, func(e string) bool { return !slices.Contains(m.Efforts, e) })
	}
	return levels, true
}

// ModelLister is implemented by backends whose models come from their CLI
// (Codex). Implementations must be safe for concurrent use.
type ModelLister interface {
	// Models lists them. It may run the CLI (up to some seconds; call it in
	// the background); the result is kept per program and version. The error
	// is a *Error: ErrNotInstalled, ErrVersion (the CLI cannot list its
	// models) or ErrFailed (the command failed; Message says why).
	Models(ctx context.Context) (ModelList, error)
	// Redetect drops the kept list: the next Models runs the CLI again.
	Redetect()
}

// EffortLister is implemented by backends whose CLI takes one reasoning
// effort for every model (Claude Code's --effort). Implementations must be
// safe for concurrent use.
type EffortLister interface {
	// Efforts returns the levels the CLI accepts, none when it has no way to
	// take one (an older version). The error tells why the CLI could not be
	// checked (a *Error, as Backend.Check's).
	Efforts(ctx context.Context) ([]string, error)
}
