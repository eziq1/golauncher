package core

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// askAI carries out plugin.ActionAskAI: the first enabled plugin
// implementing plugin.AIAsker (builtin.ai) answers, usually by pushing its
// chat view; without one, a toast says that AI is not available.
func (e *Engine) askAI(ctx context.Context, do plugin.ActionDo) (Outcome, error) {
	var q plugin.AskAI
	if err := json.Unmarshal(do.Payload, &q); err != nil {
		return Outcome{}, fmt.Errorf("core: bad AskAI payload: %w", err)
	}
	for _, l := range e.enabled(e.cfg.Get()) {
		a, ok := l.p.(plugin.AIAsker)
		if !ok {
			continue
		}
		r, err := callPlugin(ctx, e, l.m.ID, actionTimeout, func(ctx context.Context) (plugin.Result, error) { return a.AskAI(ctx, q) })
		if err != nil {
			return Outcome{}, err
		}
		if r.Do != nil && r.Do.Kind == plugin.ActionAskAI {
			return Outcome{}, fmt.Errorf("core: plugin %q answered AskAI with AskAI", l.m.ID)
		}
		return e.result(ctx, l, r)
	}
	return Outcome{Toast: &plugin.Toast{Style: plugin.ToastFailure, Title: e.core.T("toast.aiUnavailable"),
		Message: e.core.T("toast.aiUnavailable.message")}}, nil
}
