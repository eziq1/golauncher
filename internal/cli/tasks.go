package cli

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/eziq1/golauncher/internal/tasks"
)

// waitTasksTimeout bounds waiting for background tasks.
const waitTasksTimeout = time.Minute

// fakeTaskRunner is the task runner of the fake environment: it records
// commands and ends them at once. dir/tasks.json may configure the output
// and exit code by command line prefix ([{"match", "output", "exitCode"}]).
func fakeTaskRunner(dir string) (*tasks.FakeRunner, error) {
	r := &tasks.FakeRunner{}
	if dir == "" {
		return r, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "tasks.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &r.Results); err != nil {
		return nil, fmt.Errorf("tasks.json: %w", err)
	}
	return r, nil
}

// waitTasks lets the held fake tasks run, waits until no background task is
// queued or running, then re-renders the current view (views follow tasks
// through pushes that may still be on their way).
func waitTasks(ctx context.Context, e *env) error {
	ctx, cancel := context.WithTimeout(ctx, waitTasksTimeout)
	defer cancel()
	if e.runner != nil {
		e.runner.Hold(false)
		defer e.runner.Hold(true)
	}
	if err := e.Tasks.Wait(ctx); err != nil {
		return fmt.Errorf("waiting for background tasks: %w", err)
	}
	e.Controller.Wait()
	e.Controller.Refresh(ctx)
	e.Controller.Wait()
	return nil
}

// taskDetail is the output of glctl tasks show.
type taskDetail struct {
	tasks.Info
	Output    string `json:"output"`
	Truncated bool   `json:"truncated,omitzero"`
}

// tasksCmd implements glctl tasks list|show <id>: the background tasks of
// the environment (with --real, the history of the real launcher), after
// replaying a session (-s FILE) and waiting for its tasks.
func (c *cli) tasksCmd(ctx context.Context, args []string) error {
	var ef envFlags
	fs := c.flagSet("tasks", &ef)
	file := fs.String("s", "", "replay this session first")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 || (pos[0] == "list" && len(pos) != 1) || (pos[0] == "show" && len(pos) != 2) ||
		(pos[0] != "list" && pos[0] != "show") {
		return usagef("tasks: want list or show <id>")
	}
	var e *env
	if *file != "" {
		sess, err := readSession(*file)
		if err != nil {
			return err
		}
		if e, err = c.replay(ctx, sess); err != nil {
			return err
		}
	} else if e, err = c.build(ctx, ef); err != nil {
		return err
	}
	defer e.Close()
	if err := waitTasks(ctx, e); err != nil {
		return err
	}
	if pos[0] == "list" {
		list := e.Tasks.List()
		return c.output(list, func(w io.Writer) {
			for _, i := range list {
				line := fmt.Sprintf("%s  %-9s %s", i.ID, i.State, i.Title)
				if i.Cmd != nil {
					line += " — " + tasks.CommandLine(*i.Cmd)
				}
				fmt.Fprintln(w, line)
			}
			if len(list) == 0 {
				fmt.Fprintln(w, "(no tasks)")
			}
		})
	}
	info, ok := e.Tasks.Get(pos[1])
	if !ok {
		return fmt.Errorf("task %q not found", pos[1])
	}
	out, more := e.Tasks.Output(info.ID, 0)
	d := taskDetail{Info: info, Output: out, Truncated: more}
	return c.output(d, func(w io.Writer) {
		fmt.Fprintf(w, "%s  %s  %s\n", d.ID, d.State, d.Title)
		if d.Cmd != nil {
			fmt.Fprintf(w, "$ %s\n", tasks.CommandLine(*d.Cmd))
		}
		if d.Output != "" {
			fmt.Fprintln(w, d.Output)
		}
	})
}
