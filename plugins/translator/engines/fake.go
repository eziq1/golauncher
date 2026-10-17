package engines

import (
	"context"
	"strings"
	"sync"
)

// Fake is an engine for tests and glctl's fake environment: it "translates"
// at once and offline into "[to] text", detecting the source language by
// the script (English for Latin text). Err, when set, is returned instead.
type Fake struct {
	// EngineID is the ID it reports (e.g. "google" to stand in for it).
	EngineID string
	Err      error
	// Dict is returned for single words.
	Dict *Dict

	mu    sync.Mutex
	calls []Request
}

// ID implements Engine.
func (f *Fake) ID() string { return f.EngineID }

// Translate implements Engine.
func (f *Fake) Translate(ctx context.Context, r Request) (Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, r)
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if f.Err != nil {
		return Result{}, f.Err
	}
	from := r.From
	if from == "" {
		from = firstNonEmpty(Guess(r.Text), English)
	}
	res := Result{Text: "[" + r.To + "] " + r.Text, From: from}
	if f.Dict != nil && !strings.ContainsAny(strings.TrimSpace(r.Text), " \t\n") {
		d := *f.Dict
		res.Dict = &d
	}
	return res, nil
}

// Calls returns the requests made so far.
func (f *Fake) Calls() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.calls...)
}
