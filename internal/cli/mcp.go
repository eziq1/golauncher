package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/eziq1/golauncher/internal/agent"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
)

// stdin is glctl mcp's input (tests replace it).
var stdin io.Reader = os.Stdin

// mcp serves the AI tools (docs/ai.md §4) over stdio as an MCP server, on
// the environment the flags describe: by default the fake one, where every
// action is only recorded. Requests are handled one at a time so that the
// output is reproducible. Confirmations are answered by --confirm.
func (c *cli) mcp(ctx context.Context, args []string) error {
	var ef envFlags
	fs := c.flagSet("mcp", &ef)
	confirm := fs.String("confirm", "deny", "answer to confirmations: deny, allow or session")
	askOpen := fs.Bool("ask-open", false, "ask before \"open\" actions")
	trace := fs.String("trace", "", "append each tool call (client, tool, outcome) to this file")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return usagef("mcp: takes no arguments")
	}
	decision := map[string]core.Decision{"deny": core.DecisionDeny, "allow": core.DecisionAllow, "session": core.DecisionAllowSession}
	d, ok := decision[*confirm]
	if !ok {
		return usagef("mcp: --confirm must be deny, allow or session")
	}
	e, err := c.build(ctx, ef)
	if err != nil {
		return err
	}
	defer e.Close()
	if !ef.Real {
		// The fake environment stands for a user who allowed AI apps.
		err := e.Config.Update(func(cfg *config.Config) {
			cfg.Agent.External = true
			cfg.Agent.AskOpen = *askOpen
		})
		if err != nil {
			return err
		}
	}
	e.Agent.SetConfirmer(func(_ context.Context, req core.ExternalRequest) core.Decision {
		if d == core.DecisionAllowSession && !req.AllowSession {
			return core.DecisionAllow
		}
		return d
	})
	if *trace != "" {
		// Each tool call as it ends, for whoever runs glctl mcp under an AI
		// client (the output stays MCP only).
		log := e.Agent.Activity()
		log.OnChange(func() {
			if en := log.Entries(); len(en) > 0 {
				f, err := os.OpenFile(*trace, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
				if err == nil {
					fmt.Fprintf(f, "%s\t%s\t%s\t%s\n", en[0].Client, en[0].Tool, en[0].Outcome, en[0].Summary)
					f.Close()
				}
			}
		})
	}
	return e.Agent.ServeMCP(ctx, stdin, c.stdout, agent.Client{}, true)
}
