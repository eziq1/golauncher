package shell

import (
	"context"
	"os"

	"github.com/eziq1/golauncher/internal/agent"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/winapi/pipe"
)

// The AI tool layer on the desktop (docs/ai.md §4.3, §4.4, §5): the running
// launcher serves the tools on its pipe, `golauncher mcp` bridges stdio to
// it, and confirmations show the launcher window.

// commandMCP is the subcommand that runs the stdio MCP bridge.
const commandMCP = "mcp"

// runBridge runs `golauncher mcp` until stdin ends or the launcher goes
// away. It never shows a window.
func runBridge(o Options) error {
	return agent.Bridge(context.Background(), os.Stdin, os.Stdout, agent.BridgeOptions{
		Start:   startBackground,
		Token:   os.Getenv(agent.EnvToken),
		Session: os.Getenv(agent.EnvSession),
		Agent:   os.Getenv(agent.EnvAgent),
		Version: o.Version,
	})
}

// serveAgent serves the AI tools on the launcher's pipe until the launcher
// quits. A pipe owned by another launcher of the same user is left alone.
func (s *Shell) serveAgent() {
	name, err := pipe.AgentName()
	if err != nil {
		s.log.Error("ai tools pipe", "err", err)
		return
	}
	ln, err := pipe.Listen(name)
	if err != nil {
		s.log.Error("ai tools pipe", "err", err)
		return
	}
	s.log.Info("ai tools serving")
	go func() {
		if err := s.env.Agent.Serve(s.ctx, ln); err != nil {
			s.log.Warn("ai tools pipe", "err", err)
		}
	}()
}

// externalConfirm shows the launcher for a confirmation an AI client asked
// for: over the chat as it is when the launcher's own chat asks, else as
// when the launcher is summoned.
func (s *Shell) externalConfirm(req core.ExternalRequest) {
	s.post(func() {
		if s.panel == nil {
			return
		}
		if req.InPlace && !s.panel.IsVisible() {
			s.panel.Show()
			s.panel.Focus()
			s.panel.Invalidate()
			return
		}
		s.showLauncher()
		s.panel.Invalidate()
	})
}

// OpenCommand opens a launcher command from the settings (AI Activity).
func (h settingsHost) OpenCommand(pluginID, name string) {
	h.s.post(func() {
		h.s.showLauncherThen(func() { h.s.launcher.OpenCommand(pluginID, name) })
	})
}
