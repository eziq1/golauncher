package cli

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/control"
)

func configPath() (string, error) {
	dir, err := app.DefaultConfigDir()
	return filepath.Join(dir, "config.json"), err
}

// config implements glctl config path|get|set. Keys are dotted JSON paths
// such as "theme" or "plugins.builtin.apps.disabled"; a segment may contain
// dots when it names a plugin ID, so keys are matched greedily.
func (c *cli) config(args []string) error {
	fs := c.flagSet("config", nil)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return usagef("config: want path, get [key] or set <key> <value>")
	}
	path, err := configPath()
	if err != nil {
		return err
	}
	switch pos[0] {
	case "path":
		return c.output(map[string]string{"path": path}, func(w io.Writer) { fmt.Fprintln(w, path) })
	case "get":
		cfg, err := config.Load(path)
		if err != nil {
			return err
		}
		tree, err := toTree(cfg)
		if err != nil {
			return err
		}
		if len(pos) == 1 {
			return c.output(tree, nil)
		}
		v, ok := lookup(tree, pos[1])
		if !ok {
			return fmt.Errorf("config: no key %q", pos[1])
		}
		return c.output(v, nil)
	case "set":
		if len(pos) != 3 {
			return usagef("config set: want <key> <value>")
		}
		var value any
		if err := json.Unmarshal([]byte(pos[2]), &value); err != nil {
			value = pos[2] // a bare string
		}
		store, err := config.Open(path)
		if err != nil {
			return err
		}
		var setErr error
		err = store.Update(func(cfg *config.Config) {
			tree, err := toTree(*cfg)
			if err != nil {
				setErr = err
				return
			}
			setPath(tree, strings.Split(pos[1], "."), value)
			b, err := json.Marshal(tree)
			if err != nil {
				setErr = err
				return
			}
			var next config.Config
			if err := json.Unmarshal(b, &next, json.RejectUnknownMembers(true)); err != nil {
				setErr = fmt.Errorf("config set %s: %w", pos[1], err)
				return
			}
			*cfg = next
		})
		if setErr != nil {
			return setErr
		}
		return err
	default:
		return usagef("config: unknown subcommand %q", pos[0])
	}
}

func toTree(cfg config.Config) (map[string]any, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var tree map[string]any
	err = json.Unmarshal(b, &tree)
	return tree, err
}

// lookup resolves a dotted key, joining segments greedily so plugin IDs
// containing dots ("builtin.apps") work.
func lookup(tree any, key string) (any, bool) {
	if key == "" {
		return tree, true
	}
	m, ok := tree.(map[string]any)
	if !ok {
		return nil, false
	}
	parts := strings.Split(key, ".")
	for i := len(parts); i > 0; i-- {
		if v, ok := m[strings.Join(parts[:i], ".")]; ok {
			if r, ok := lookup(v, strings.Join(parts[i:], ".")); ok {
				return r, true
			}
		}
	}
	return nil, false
}

// setPath sets a dotted key, creating maps as needed. Under "plugins" the
// segments up to a plugin config field ("disabled", "preferences",
// "commands") form the plugin ID, which contains dots.
func setPath(m map[string]any, parts []string, value any) {
	if len(parts) == 1 {
		m[parts[0]] = value
		return
	}
	head, rest := parts[0], parts[1:]
	if head == "plugins" {
		if j := slices.IndexFunc(rest, func(s string) bool {
			return s == "disabled" || s == "preferences" || s == "commands"
		}); j > 0 {
			rest = append([]string{strings.Join(rest[:j], ".")}, rest[j:]...)
		}
	}
	child, _ := m[head].(map[string]any)
	if child == nil {
		child = map[string]any{}
	}
	setPath(child, rest, value)
	m[head] = child
}

// attach drives the running GUI through its control socket.
func (c *cli) attach(args []string) error {
	fs := c.flagSet("attach", nil)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return usagef("attach: want show|hide|type|key|select|snapshot")
	}
	req := control.Request{Cmd: pos[0]}
	arg := strings.Join(pos[1:], " ")
	switch req.Cmd {
	case "type":
		req.Text = arg
	case "key":
		req.Key = arg
	case "select":
		if req.Index, err = strconv.Atoi(arg); err != nil {
			return usagef("attach select: %v", err)
		}
	}
	dir, err := app.DefaultDataDir()
	if err != nil {
		return err
	}
	cl, err := control.Dial(filepath.Join(dir, "control.sock"))
	if err != nil {
		return err
	}
	defer cl.Close()
	resp, err := cl.Do(req)
	if resp.Snapshot != nil {
		res := stepResult{Step: strings.Join(pos, " "), Outcome: resp.Outcome, Error: resp.Error, Snapshot: *resp.Snapshot}
		if oerr := c.output(resp, func(w io.Writer) {
			writeStep(w, res)
			fmt.Fprintf(w, "window visible: %v\n", resp.Visible)
		}); oerr != nil {
			return oerr
		}
	}
	return err
}

// benchResult reports root search latency.
type benchResult struct {
	Queries []string `json:"queries"`
	Runs    int      `json:"runs"`
	P50Ms   float64  `json:"p50Ms"`
	P95Ms   float64  `json:"p95Ms"`
	MaxMs   float64  `json:"maxMs"`
}

func (c *cli) bench(ctx context.Context, args []string) error {
	var ef envFlags
	fs := c.flagSet("bench", &ef)
	n := fs.Int("n", 200, "runs per query")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		pos = []string{"c", "co", "cod", "code", "vsc", "wx", "12*3+4", "clip"}
	}
	e, err := c.build(ctx, ef)
	if err != nil {
		return err
	}
	defer e.Close()
	var durs []time.Duration
	for range *n {
		for _, q := range pos {
			start := time.Now()
			if _, err := e.Engine.RootSearch(ctx, q); err != nil {
				return err
			}
			durs = append(durs, time.Since(start))
		}
	}
	slices.Sort(durs)
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
	res := benchResult{
		Queries: pos, Runs: len(durs),
		P50Ms: ms(durs[len(durs)/2]), P95Ms: ms(durs[len(durs)*95/100]), MaxMs: ms(durs[len(durs)-1]),
	}
	return c.output(res, func(w io.Writer) {
		fmt.Fprintf(w, "%d searches: p50 %.2fms  p95 %.2fms  max %.2fms\n", res.Runs, res.P50Ms, res.P95Ms, res.MaxMs)
	})
}
