package ai

import (
	"context"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
)

var _ plugin.AIAsker = (*Plugin)(nil)

// AskAI implements plugin.AIAsker (plugin.ActionAskAI, e.g. the
// translator's "Translate with AI"): a new chat with the instruction and the
// text as a quote, like a selection action; the files join its draft as the
// root search's "Ask AI about <name>" does (R97). With AI off the chat view
// shows the prompt to turn it on and sends the message once it is.
func (p *Plugin) AskAI(_ context.Context, q plugin.AskAI) (plugin.Result, error) {
	pl := chatPayload{Title: q.Title, Files: q.Files}
	if strings.TrimSpace(q.Text) != "" {
		pl.Quote, pl.Instruction = q.Text, strings.TrimSpace(q.Instruction)
	} else {
		pl.Text = q.Instruction
	}
	if strings.TrimSpace(pl.Quote+pl.Text) == "" && len(pl.Files) == 0 {
		return plugin.Result{}, nil
	}
	return plugin.Result{Push: &plugin.ViewRequest{ID: viewChat, Payload: plugin.MustPayload(pl)}}, nil
}
