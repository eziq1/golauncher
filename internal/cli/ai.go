package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// aiBackendsResult is the output of glctl ai backends.
type aiBackendsResult struct {
	Backends []backend.Report `json:"backends"`
}

// ai runs "glctl ai backends": the AI chat's backends and the installations
// of their programs, as Settings › Plugins › AI › Backends shows them (R84,
// docs/ai.md §22). The fake environment's are made up; with --real the
// programs on this computer are probed (their version commands only, and
// Codex's sign-in check: nothing is changed, no model is asked).
func (c *cli) ai(ctx context.Context, args []string) error {
	var ef envFlags
	fs := c.flagSet("ai", &ef)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || pos[0] != "backends" {
		return usagef("ai: want backends")
	}
	e, err := c.build(ctx, ef)
	if err != nil {
		return err
	}
	defer e.Close()
	if e.AIBackends == nil {
		return errors.New("ai: the AI plugin is not available")
	}
	res := aiBackendsResult{Backends: e.AIBackends.BackendReports(ctx, true)}
	return c.output(res, func(w io.Writer) { writeBackends(w, res.Backends) })
}

// writeBackends is the text output of glctl ai backends.
func writeBackends(w io.Writer, reports []backend.Report) {
	for _, r := range reports {
		fmt.Fprintf(w, "%s (%s): %s", r.Name, r.ID, r.Status)
		if r.Message != "" {
			fmt.Fprintf(w, " — %s", r.Message)
		}
		fmt.Fprintln(w)
		if r.Configured != "" {
			missing := ""
			if r.ConfiguredMissing {
				missing = " (missing: using the automatic choice)"
			}
			fmt.Fprintf(w, "  program: %s%s\n", r.Configured, missing)
		}
		for i, in := range r.Installs {
			mark := "  "
			if i == r.InUse {
				mark = "* "
			}
			fmt.Fprintf(w, "  %s%s  [%s]", mark, in.Path, in.Source)
			switch {
			case in.Launcher:
				fmt.Fprint(w, " launcher script, not used")
			case in.VersionError != "":
				fmt.Fprintf(w, " error: %s", in.VersionError)
			default:
				fmt.Fprintf(w, " %s", in.Version)
			}
			if in.TooOld {
				fmt.Fprintf(w, " too old (needs %s)", r.MinVersion)
			}
			if in.Shadowed {
				fmt.Fprint(w, " not used: found later")
			}
			if i == r.Auto {
				fmt.Fprintf(w, " automatic (%s)", r.AutoReason)
			}
			fmt.Fprintln(w)
		}
	}
}
