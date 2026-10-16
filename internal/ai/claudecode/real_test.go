package claudecode

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// TestRealCLI chats through the installed Claude Code CLI, which uses the
// subscription. It only runs with GOLAUNCHER_AI_TESTS=1.
func TestRealCLI(t *testing.T) {
	if os.Getenv("GOLAUNCHER_AI_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_AI_TESTS=1 to chat with the real Claude Code CLI")
	}
	b := New(Options{Dir: t.TempDir()})
	defer b.Close()
	st, err := b.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Claude Code %s (untested %v)", st.Version, st.Untested)
	var u [16]byte
	rand.Read(u[:])
	u[6], u[8] = u[6]&0x0f|0x40, u[8]&0x3f|0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:])
	c, err := b.Open(context.Background(), backend.OpenOptions{SessionID: id, SystemPrompt: "Answer as briefly as possible."})
	if err != nil {
		t.Fatal(err)
	}
	for i, prompt := range []string{"Reply with OK", "Reply with OK again", "release"} {
		if prompt == "release" {
			c.Release()
			prompt = "Reply with OK once more"
		}
		a := send(t, c, prompt)
		if a.final.Kind != backend.EventDone || !strings.Contains(strings.ToUpper(a.text), "OK") {
			t.Fatalf("turn %d: answer %q, final %+v", i, a.text, a.final)
		}
		t.Logf("turn %d: %q, usage %+v", i, a.text, a.final.Usage)
	}
}
