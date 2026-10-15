package cli

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// fakeExtras are plugins that tests add to the fake environment, e.g. one
// with a form view.
var fakeExtras []plugin.Plugin

// sessionFile is a multi-step session. Each glctl session invocation
// rebuilds the environment and replays every step, so a session is fully
// deterministic in fake mode.
type sessionFile struct {
	Env   envFlags `json:"env"`
	Steps []step   `json:"steps"`
}

// step is one interaction.
type step struct {
	Op   string `json:"op"` // type, key, select, action, field, arg
	Arg  string `json:"arg,omitempty"`
	Arg2 string `json:"arg2,omitempty"`
}

func (s step) String() string {
	return strings.TrimSpace(fmt.Sprintf("%s %q %s", s.Op, s.Arg, s.Arg2))
}

func (c *cli) session(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usagef("session: want new|type|key|select|action|field|arg|show")
	}
	op := args[0]
	var ef envFlags
	fs := c.flagSet("session "+op, &ef)
	file := fs.String("s", "", "session file")
	pos, err := parse(fs, args[1:])
	if err != nil {
		return err
	}
	if *file == "" {
		return usagef("session: -s FILE is required")
	}
	if op == "new" {
		if ef.Real {
			return usagef("session: sessions always use the fake environment")
		}
		return writeSession(*file, sessionFile{Env: ef})
	}
	sess, err := readSession(*file)
	if err != nil {
		return err
	}
	var next *step
	switch op {
	case "show":
	case "type":
		next = &step{Op: "type", Arg: strings.Join(pos, " ")}
	case "key", "select":
		if len(pos) != 1 {
			return usagef("session %s: want one argument", op)
		}
		next = &step{Op: op, Arg: pos[0]}
	case "action":
		if len(pos) < 1 || len(pos) > 2 {
			return usagef("session action: want <entry key> [<action id>]")
		}
		next = &step{Op: "action", Arg: pos[0]}
		if len(pos) == 2 {
			next.Arg2 = pos[1]
		}
	case "field", "arg":
		if len(pos) < 1 {
			return usagef("session %s: want <name> <value>", op)
		}
		next = &step{Op: op, Arg: pos[0], Arg2: strings.Join(pos[1:], " ")}
	default:
		return usagef("session: unknown step %q", op)
	}

	e, err := c.build(ctx, sess.Env)
	if err != nil {
		return err
	}
	defer e.Close()
	e.Controller.Reset(ctx)
	e.Controller.Wait()
	for i, s := range sess.Steps {
		if _, err := apply(ctx, e, s); err != nil {
			return fmt.Errorf("replaying step %d (%s): %w", i+1, s, err)
		}
	}
	res := stepResult{}
	if next != nil {
		e.rec.Reset()
		res.Step = next.String()
		out, err := apply(ctx, e, *next)
		res.Outcome = out
		if err != nil {
			res.Error = err.Error()
		} else {
			sess.Steps = append(sess.Steps, *next)
			if err := writeSession(*file, sess); err != nil {
				return err
			}
		}
		res.Effects = e.rec.Effects()
	}
	res.Snapshot = e.Controller.Snapshot()
	if err := c.output(res, func(w io.Writer) { writeStep(w, res) }); err != nil {
		return err
	}
	if res.Error != "" {
		return errors.New(res.Error)
	}
	return nil
}

func apply(ctx context.Context, e *env, s step) (*core.Outcome, error) {
	ctrl := e.Controller
	defer ctrl.Wait()
	switch s.Op {
	case "type":
		ctrl.SetText(ctx, s.Arg)
		return nil, nil
	case "key":
		out, err := ctrl.Key(ctx, s.Arg)
		return &out, err
	case "select":
		i, err := strconv.Atoi(s.Arg)
		if err != nil {
			return nil, err
		}
		ctrl.Select(i)
		return nil, nil
	case "action":
		out, err := ctrl.RunAction(ctx, s.Arg, s.Arg2)
		return &out, err
	case "field":
		return nil, ctrl.SetField(s.Arg, s.Arg2)
	case "arg":
		return nil, ctrl.SetArgument(s.Arg, s.Arg2)
	default:
		return nil, fmt.Errorf("unknown step %q", s.Op)
	}
}

func readSession(path string) (sessionFile, error) {
	var s sessionFile
	data, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(data, &s)
	return s, err
}

func writeSession(path string, s sessionFile) error {
	b, err := json.Marshal(s, jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
