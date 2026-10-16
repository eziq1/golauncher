package agent_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/agent"
	"github.com/eziq1/golauncher/internal/core"
)

// The process manager through the AI tools (docs/process-manager.md §6 and
// §14): lists and details are read-only, window titles and command lines
// never reach the AI, Force end is destructive (asked every time, never
// allowed for the session), End task of an app is a change. The processes
// are the invented ones of testdata/fixtures/processes.json.

// secrets are window titles and command line parts of the fixture.
var secrets = []string{"New Tab - Fabrikam", "Quarterly plan", "会议记录", "Trips - Margie", "main.go - shop", "Project files",
	"server.js", "abc123", "hunter2", "nw-secret", "--background", "renderer-client-id"}

// rawCall runs a tool and returns its raw text.
func rawCall(t *testing.T, s *agent.Session, tool string, args any) string {
	t.Helper()
	b, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Call(t.Context(), tool, jsontext.Value(b))
	if err != nil {
		t.Fatal(err)
	}
	return res.Text
}

func noSecrets(t *testing.T, what, text string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(text, s) {
			t.Errorf("%s gives the AI %q:\n%s", what, s, text)
		}
	}
}

func TestProcessesHideTitlesAndCommandLines(t *testing.T) {
	w := newWorld(t, nil)
	s := w.chat()
	noSecrets(t, "search", rawCall(t, s, agent.ToolSearch, map[string]any{"query": "ps fab", "limit": 50}))
	text := rawCall(t, s, agent.ToolRunCommand, map[string]any{"plugin": "builtin.processes", "command": "processes"})
	noSecrets(t, "the Processes view", text)
	if !strings.Contains(text, "Fabrikam Browser (8)") || !strings.Contains(text, "CPU ") {
		t.Errorf("the view lacks the browser or its use:\n%s", text)
	}
	// The details of every item.
	r := w.mustCall(s, agent.ToolRunCommand, map[string]any{"plugin": "builtin.processes", "command": "processes", "query": ""})
	n := 0
	for _, it := range items(r) {
		if it["details"] == true {
			n++
			noSecrets(t, "get_detail of "+it["title"].(string), rawCall(t, s, agent.ToolGetDetail, map[string]any{"handle": it["handle"]}))
		}
	}
	if n == 0 {
		t.Error("no item with details")
	}
}

func TestProcessesRisks(t *testing.T) {
	w := newWorld(t, nil)
	s := w.chat()
	r := w.mustCall(s, agent.ToolSearch, map[string]any{"query": "kill writer"})
	var writer map[string]any
	for _, it := range items(r) {
		if it["plugin"] == "builtin.processes" {
			writer = it
			break
		}
	}
	if writer == nil {
		t.Fatalf("no process: %v", r)
	}
	risks := map[string]string{}
	for _, a := range writer["actions"].([]any) {
		a := a.(map[string]any)
		risks[a["id"].(string)] = a["risk"].(string)
	}
	for id, want := range map[string]string{"end-task": "change", "force-end": "destructive", "switch": "change", "copy-pid": "open"} {
		if risks[id] != want {
			t.Errorf("%s: risk %q, want %q (%v)", id, risks[id], want, risks)
		}
	}
	// Force end: asked every time, with the plugin's text, never for the
	// session; declined, nothing runs.
	w.decide(core.DecisionDeny)
	if out, isErr := w.call(s, agent.ToolRunAction, map[string]any{"handle": writer["handle"], "action": "force-end"}); !isErr || out["status"] != "declined" {
		t.Fatalf("force end = %v", out)
	}
	asked := w.takeAsked()
	if len(asked) != 1 || !asked[0].Destructive || asked[0].AllowSession || !strings.Contains(asked[0].Message, "unsaved work is lost") {
		t.Errorf("asked = %+v", asked)
	}
}

// TestProcessesEndTaskAsksEveryTime: End task is asked every time, without
// "Allow for this session", even after the user allowed it once (R60).
func TestProcessesEndTaskAsksEveryTime(t *testing.T) {
	w := newWorld(t, nil)
	s := w.chat()
	r := w.mustCall(s, agent.ToolSearch, map[string]any{"query": "kill writer"})
	var writer map[string]any
	for _, it := range items(r) {
		if it["plugin"] == "builtin.processes" {
			writer = it
			break
		}
	}
	if writer == nil {
		t.Fatalf("no process: %v", r)
	}
	for i := range 2 {
		// The user answers "allow for this session"; it must not stick.
		w.decide(core.DecisionAllowSession)
		w.call(s, agent.ToolRunAction, map[string]any{"handle": writer["handle"], "action": "end-task"})
		asked := w.takeAsked()
		if len(asked) != 1 || asked[0].AllowSession {
			t.Fatalf("call %d: asked = %+v", i+1, asked)
		}
	}
}
