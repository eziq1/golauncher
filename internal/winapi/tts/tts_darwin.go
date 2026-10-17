package tts

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

type sayer struct {
	once   sync.Once
	voices []sayVoice
}

// New returns the system's speaker: SAPI on Windows, the `say` command on macOS; elsewhere one returning ErrUnsupported.
func New() Speaker { return &sayer{} }

// loadVoices lists the installed voices once; a failure leaves the list
// empty, so speech still works with the default voice. The listing ignores
// cancellation so that one cancelled Speak does not empty the cache for good.
func (s *sayer) loadVoices() []sayVoice {
	s.once.Do(func() {
		out, err := exec.Command("say", "-v", "?").Output()
		if err == nil {
			s.voices = parseSayVoices(string(out))
		}
	})
	return s.voices
}

func (s *sayer) Speak(ctx context.Context, text, lang string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if _, err := exec.LookPath("say"); err != nil {
		return ErrUnsupported
	}
	// The text goes on stdin: command-line arguments are visible to other processes.
	args := []string{"-f", "-"}
	if v := chooseSayVoice(s.loadVoices(), lang); v != "" {
		args = append([]string{"-v", v}, args...)
	}
	cmd := exec.CommandContext(ctx, "say", args...)
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("tts: say: %w", err)
	}
	return nil
}
