package agent_test

import (
	"testing"

	"github.com/eziq1/golauncher/internal/agent"
	"github.com/eziq1/golauncher/internal/core"
)

// TestChatPermission: an agent CLI's permission request is asked over the
// chat in the tool layer's dialog and recorded in the activity log.
func TestChatPermission(t *testing.T) {
	w := newWorld(t, nil)
	for _, d := range []core.Decision{core.DecisionAllow, core.DecisionDeny} {
		w.decide(d)
		got := w.ag.ConfirmChatPermission(t.Context(), core.ExternalRequest{Client: "Gemini CLI (AI Chat)", Action: "Fetching content from: example.com",
			Destructive: true, AllowSession: true}, "web_fetch")
		if got != d {
			t.Fatalf("decision %v, want %v", got, d)
		}
	}
	asked := w.takeAsked()
	if len(asked) != 2 || !asked[0].InPlace || asked[0].AllowSession || asked[0].Client != "Gemini CLI (AI Chat)" {
		t.Fatalf("asked %+v", asked)
	}
	entries := w.ag.Activity().Entries() // the newest first
	if len(entries) != 2 || entries[1].Tool != "web_fetch" || entries[1].Client != "Gemini CLI (AI Chat)" ||
		entries[1].Summary != "Fetching content from: example.com" || entries[1].Detail != "permission request: allow" ||
		entries[1].Outcome != agent.OutcomeOK || entries[0].Outcome != agent.OutcomeDeclined || entries[0].Detail != "permission request: deny" {
		t.Fatalf("activity %+v", entries)
	}
}

// TestChatClientName: the chat's tool calls name the agent CLI that answers
// (R72); Claude Code's keep their name.
func TestChatClientName(t *testing.T) {
	w := newWorld(t, nil)
	for agentName, want := range map[string]string{"": "Claude (AI Chat)", "Claude Code": "Claude (AI Chat)", "Gemini CLI": "Gemini CLI (AI Chat)"} {
		if got := w.ag.DisplayName(agent.Client{Chat: true, ChatSession: "c1", ChatAgent: agentName}); got != want {
			t.Errorf("%q: %q, want %q", agentName, got, want)
		}
	}
}
