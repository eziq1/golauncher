package cli

import (
	"fmt"
	"io"

	"github.com/eziq1/golauncher/internal/extproc"
)

// validate implements glctl plugin validate <dir>.
func (c *cli) validate(dir string) error {
	r := extproc.Validate(dir)
	if err := c.output(r, func(w io.Writer) {
		for _, s := range r.Errors {
			fmt.Fprintln(w, "error:", s)
		}
		for _, s := range r.Warnings {
			fmt.Fprintln(w, "warning:", s)
		}
		fmt.Fprintf(w, "%s: %d errors, %d warnings\n", dir, len(r.Errors), len(r.Warnings))
	}); err != nil {
		return err
	}
	if !r.OK {
		return fmt.Errorf("plugin validate: %d errors", len(r.Errors))
	}
	return nil
}
