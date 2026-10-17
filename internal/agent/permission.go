package agent

import (
	"context"

	"github.com/eziq1/golauncher/internal/core"
)

// ConfirmChatPermission asks the user about a permission request of the
// agent CLI that answers in the launcher's own chat (an ACP agent's
// session/request_permission, docs/ai.md §21), in the same dialog as the
// tool layer's confirmations, and records it in the activity log under the
// agent's tool. req.InPlace is set: the dialog shows over the chat.
func (a *Agent) ConfirmChatPermission(ctx context.Context, req core.ExternalRequest, tool string) core.Decision {
	req.InPlace = true
	if req.Destructive {
		req.AllowSession = false
	}
	a.mu.Lock()
	confirm := a.confirm
	a.mu.Unlock()
	d := core.DecisionDeny
	if confirm != nil {
		d = confirm(ctx, req)
	}
	if a.o.Activity != nil {
		outcome := OutcomeOK
		if d == core.DecisionDeny {
			outcome = OutcomeDeclined
		}
		if tool == "" {
			tool = "permission"
		}
		a.o.Activity.Add(Entry{
			Time:    a.now(),
			Client:  req.Client,
			Tool:    truncate(tool, maxLogSummary),
			Summary: truncate(req.Action, maxLogSummary),
			Outcome: outcome,
			Detail:  "permission request: " + d.String(),
		})
	}
	return d
}
