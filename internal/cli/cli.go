// Package cli implements glctl, the headless command-line front end used by
// agents and tests to drive the launcher engine. See docs/design.md §7.
package cli

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/tasks"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Main runs glctl with args (without the program name) and returns the
// process exit code.
func Main(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := &cli{stdout: stdout, stderr: stderr}
	if err := c.run(ctx, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, "glctl:", err)
		if _, ok := errors.AsType[usageError](err); ok {
			return 2
		}
		return 1
	}
	return 0
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error { return usageError{fmt.Sprintf(format, args...)} }

const usage = `glctl drives the golauncher engine without a GUI. Output is JSON unless --format text.

Commands:
  query <text>                       root search; prints ranked sections
  run <plugin>/<command> [--text t]  run a command; prints outcome, effects and view
  session new -s FILE                start a multi-step session (stores env flags)
  session type|key|select|action|show -s FILE [args]
                                     replay the session plus one step and print the result
  session wait-tasks -s FILE         wait until the background tasks ended
  session field -s FILE <id> <value> set a field of the open form
  session arg -s FILE <name> <value> set an argument of the selected entry
  tasks list | tasks show <id> [-s FILE]
                                     background tasks and their output (after
                                     replaying a session; --real: the history)
  plugin list | plugin info <id>     describe plugins
  plugin validate <dir>              check an external plugin (manifest, protocol,
                                     translations, run.command)
  config path | get [key] | set <key> <value>
                                     the user configuration (always the real file)
  i18n check [--src DIR]             check message catalogs and key references
  attach show|hide|type|key|select|snapshot [arg]
                                     drive the running GUI through its control socket
  mcp [--confirm deny|allow|session] [--ask-open]
                                     serve the AI tools as an MCP server on stdio
                                     (docs/ai.md §4); confirmations get --confirm
  bench [--n N] [queries...]         measure root search latency
  version                            print the glctl version

Environment flags (query, run, session new, tasks, plugin, mcp, bench):
  --real           use the real configuration, database, apps and clipboard
                   (default: an isolated in-memory environment)
  --exec           with --real, perform action side effects and run the programs of
                   background tasks (default: record only)
  --fixtures DIR   load apps.json / clipboard.json / tasks.json fixtures (fake mode;
                   tasks.json sets the fake output of background tasks)
  --plugins DIR    load external plugins from DIR (repeatable): a plugin directory
                   or a directory of plugin directories; GOLAUNCHER_PLUGIN_PATH
                   is also read. The real user plugin folder is used only with --real
  --now RFC3339    fix the clock (default 2026-10-14T10:00:00Z in fake mode)
  --locale TAG     UI locale (default "en"; "en-XA" is the pseudo-locale)
  --context-folder DIR   inject a context instead of detecting one: the folder
                   open in File Explorer (context-aware items appear in the root)
  --context-select FILE  a selected file of that context (repeatable)
  --context-app NAME     the foreground app of that context (default explorer.exe)
  --format json|text
`

type cli struct {
	stdout, stderr io.Writer
	format         string
}

// envFlags are the flags that describe an environment.
type envFlags struct {
	Real     bool     `json:"real,omitzero"`
	Exec     bool     `json:"exec,omitzero"`
	Fixtures string   `json:"fixtures,omitempty"`
	Plugins  []string `json:"plugins,omitempty"`
	Now      string   `json:"now,omitempty"`
	Locale   string   `json:"locale,omitempty"`
	// A context injected instead of detecting one (docs/context-awareness.md §5).
	ContextFolder string   `json:"contextFolder,omitempty"`
	ContextSelect []string `json:"contextSelect,omitempty"`
	ContextApp    string   `json:"contextApp,omitempty"`
}

func (c *cli) flagSet(name string, ef *envFlags) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	fs.Usage = func() { fmt.Fprint(c.stderr, usage) }
	fs.StringVar(&c.format, "format", "json", "output format: json or text")
	if ef != nil {
		fs.BoolVar(&ef.Real, "real", false, "use the real environment")
		fs.BoolVar(&ef.Exec, "exec", false, "perform side effects (with --real)")
		fs.StringVar(&ef.Fixtures, "fixtures", "", "fixtures directory")
		fs.StringVar(&ef.Now, "now", "", "fixed time (RFC 3339)")
		fs.StringVar(&ef.Locale, "locale", "en", "UI locale")
		fs.Func("plugins", "external plugin directory (repeatable)", func(s string) error {
			abs, err := filepath.Abs(s)
			ef.Plugins = append(ef.Plugins, abs)
			return err
		})
		fs.Func("context-folder", "inject a context: the folder open in File Explorer", func(s string) error {
			abs, err := filepath.Abs(s)
			ef.ContextFolder = abs
			return err
		})
		fs.Func("context-select", "inject a context: a selected file (repeatable)", func(s string) error {
			abs, err := filepath.Abs(s)
			ef.ContextSelect = append(ef.ContextSelect, abs)
			return err
		})
		fs.StringVar(&ef.ContextApp, "context-app", "", "inject a context: the foreground app (e.g. explorer.exe)")
	}
	return fs
}

// parse parses flags that may be interleaved with positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		if args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func (c *cli) run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(c.stdout, usage)
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "query":
		return c.query(ctx, rest)
	case "run":
		return c.runCommand(ctx, rest)
	case "session":
		return c.session(ctx, rest)
	case "tasks":
		return c.tasksCmd(ctx, rest)
	case "plugin":
		return c.plugin(ctx, rest)
	case "config":
		return c.config(rest)
	case "i18n":
		return c.i18n(ctx, rest)
	case "attach":
		return c.attach(rest)
	case "bench":
		return c.bench(ctx, rest)
	case "mcp":
		return c.mcp(ctx, rest)
	case "version":
		return c.version(rest)
	default:
		return usagef("unknown command %q (see glctl help)", cmd)
	}
}

// env is a built environment plus its observers.
type env struct {
	*app.Env
	rec  *app.Recorder
	clip *hostfake.Clipboard
	// runner is the fake task runner (fake mode). It holds tasks until a
	// wait-tasks step, so that snapshots do not depend on timing.
	runner *tasks.FakeRunner
}

// build creates the environment described by ef.
func (c *cli) build(ctx context.Context, ef envFlags) (*env, error) {
	now := hostfake.Fixed
	if ef.Now != "" {
		t, err := time.Parse(time.RFC3339, ef.Now)
		if err != nil {
			return nil, usagef("--now: %v", err)
		}
		now = t
	}
	e := &env{rec: &app.Recorder{}}
	opts := app.Options{Locale: ef.Locale, System: e.rec, Version: Version}
	if ef.Real {
		real, err := realPlatform(ef.Exec)
		if err != nil {
			return nil, err
		}
		opts.Clipboard = real.clipboard
		if ef.Exec {
			opts.System = real.system
		}
		opts.Plugins = realPlugins(ef.Plugins, ef.Exec)
		if !ef.Exec {
			// Programs are only recorded, and the real task history is read but
			// not written.
			opts.TaskRunner, opts.TasksReadOnly = &tasks.FakeRunner{}, true
		}
		if ef.Now != "" {
			opts.Now = func() time.Time { return now }
		}
	} else {
		e.clip = &hostfake.Clipboard{}
		e.rec.Clipboard = e.clip
		opts.Fake = true
		opts.Clipboard = e.clip
		opts.Now = func() time.Time { return now }
		plugins, err := fakePlugins(ef.Fixtures, ef.Plugins)
		if err != nil {
			return nil, err
		}
		opts.Plugins = append(plugins, fakeExtras...)
		if e.runner, err = fakeTaskRunner(ef.Fixtures); err != nil {
			return nil, err
		}
		e.runner.Hold(true)
		opts.TaskRunner = e.runner
	}
	var err error
	if e.Env, err = app.Build(ctx, opts); err != nil {
		return nil, err
	}
	if !ef.Real && ef.Fixtures != "" {
		if err := loadClipboardFixture(e, ef.Fixtures); err != nil {
			e.Close()
			return nil, err
		}
	}
	if c := ef.context(); c != nil {
		e.Engine.SetContext(c)
	}
	return e, nil
}

// context is the context the flags inject, or nil. A folder or selection
// makes it a File Explorer context; the folder defaults to the first
// selected file's.
func (ef envFlags) context() *plugin.Context {
	if ef.ContextFolder == "" && len(ef.ContextSelect) == 0 && ef.ContextApp == "" {
		return nil
	}
	c := &plugin.Context{App: plugin.AppInfo{Name: ef.ContextApp}, Folder: ef.ContextFolder, Selected: ef.ContextSelect}
	if c.Folder == "" && len(c.Selected) > 0 {
		c.Folder = filepath.Dir(c.Selected[0])
	}
	if c.Folder != "" {
		c.Kind = plugin.ContextExplorer
		c.App.Name = cmp.Or(c.App.Name, "explorer.exe")
	}
	return c
}

// output writes v as JSON, or with text when --format text.
func (c *cli) output(v any, text func(w io.Writer)) error {
	if c.format == "text" && text != nil {
		text(c.stdout)
		return nil
	}
	b, err := json.Marshal(v, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		return err
	}
	_, err = c.stdout.Write(append(b, '\n'))
	return err
}

// queryResult is the output of glctl query.
type queryResult struct {
	Query     string         `json:"query"`
	ElapsedMs int64          `json:"elapsedMs"`
	Sections  []core.Section `json:"sections"`
}

func (c *cli) query(ctx context.Context, args []string) error {
	var ef envFlags
	fs := c.flagSet("query", &ef)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	text := strings.Join(pos, " ")
	e, err := c.build(ctx, ef)
	if err != nil {
		return err
	}
	defer e.Close()
	start := time.Now()
	sections, err := e.Engine.RootSearch(ctx, text)
	if err != nil {
		return err
	}
	res := queryResult{Query: text, ElapsedMs: time.Since(start).Milliseconds(), Sections: sections}
	return c.output(res, func(w io.Writer) { writeSections(w, sections, 0) })
}

// stepResult is the output of commands that change the controller state.
type stepResult struct {
	Step     string        `json:"step,omitempty"`
	Outcome  *core.Outcome `json:"outcome,omitempty"`
	Error    string        `json:"error,omitempty"`
	Effects  []app.Effect  `json:"effects,omitempty"`
	Snapshot core.Snapshot `json:"snapshot"`
}

func (c *cli) runCommand(ctx context.Context, args []string) error {
	var ef envFlags
	fs := c.flagSet("run", &ef)
	text := fs.String("text", "", "search text to type after the command opens")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("run: want <plugin>/<command>")
	}
	pluginID, name, ok := cutLast(pos[0], "/")
	if !ok {
		return usagef("run: want <plugin>/<command>, got %q", pos[0])
	}
	e, err := c.build(ctx, ef)
	if err != nil {
		return err
	}
	defer e.Close()
	res := stepResult{Step: "run " + pos[0]}
	out, err := e.Controller.OpenCommand(ctx, pluginID, name)
	res.Outcome = &out
	if err != nil {
		res.Error = err.Error()
	}
	e.Controller.Wait()
	if *text != "" {
		e.Controller.SetText(ctx, *text)
		e.Controller.Wait()
	}
	res.Effects = e.rec.Effects()
	res.Snapshot = e.Controller.Snapshot()
	if err := c.output(res, func(w io.Writer) { writeStep(w, res) }); err != nil {
		return err
	}
	if res.Error != "" {
		return errors.New(res.Error)
	}
	return nil
}

// cutLast splits s around the last sep.
func cutLast(s, sep string) (before, after string, ok bool) {
	return strings.CutLast(s, sep)
}

func writeSections(w io.Writer, sections []core.Section, selected int) {
	i := 0
	for _, s := range sections {
		if s.Title != "" {
			fmt.Fprintf(w, "[%s]\n", s.Title)
		}
		for _, it := range s.Items {
			mark := "  "
			if i == selected {
				mark = "> "
			}
			line := mark + it.Title
			if it.Subtitle != "" {
				line += " — " + it.Subtitle
			}
			if it.Accessory != "" {
				line += "  (" + it.Accessory + ")"
			}
			var acts []string
			for _, a := range it.Actions {
				acts = append(acts, a.ID+cmpShortcut(a.Shortcut))
			}
			fmt.Fprintf(w, "%s   %s  [%s]\n", line, it.Key, strings.Join(acts, ", "))
			i++
		}
	}
	if i == 0 {
		fmt.Fprintln(w, "(no results)")
	}
}

func cmpShortcut(s string) string {
	if s == "" {
		return ""
	}
	return " " + s
}

func writeStep(w io.Writer, r stepResult) {
	s := r.Snapshot
	if r.Step != "" {
		fmt.Fprintf(w, "$ %s\n", r.Step)
	}
	if r.Error != "" {
		fmt.Fprintf(w, "error: %s\n", r.Error)
	}
	for _, e := range r.Effects {
		fmt.Fprintf(w, "effect: %s %s\n", e.Kind, e.Target)
	}
	if r.Outcome != nil && r.Outcome.Hide {
		fmt.Fprintln(w, "window: hidden")
	}
	title := s.Title
	if title == "" {
		title = "root"
	}
	fmt.Fprintf(w, "view: %s (depth %d)  text: %q\n", title, s.Depth, s.Text)
	if f := s.Form; f != nil {
		for _, fl := range f.Fields {
			mark := "  "
			if fl.ID == f.Focused {
				mark = "> "
			}
			fmt.Fprintf(w, "%s%s (%s, %s) = %q", mark, fl.Title, fl.ID, fl.Type, fl.Value)
			if fl.Error != "" {
				fmt.Fprintf(w, "  error: %s", fl.Error)
			}
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "submit: %s (Ctrl+Enter)\n", f.Submit)
	} else if s.Layout == string(plugin.ViewDetail) {
		// A detail view shows its content; its first item has the actions.
		if s.Detail != nil {
			for line := range strings.Lines(s.Detail.Markdown) {
				fmt.Fprintf(w, "| %s", line)
			}
			fmt.Fprintln(w)
			for _, m := range s.Detail.Metadata {
				fmt.Fprintf(w, "| %s: %s\n", m.Label, m.Value)
			}
		}
		writeSections(w, s.Sections, s.Selected)
	} else {
		writeSections(w, s.Sections, s.Selected)
	}
	if a := s.Arguments; a != nil {
		fmt.Fprintf(w, "arguments of %s:\n", a.Entry)
		for _, f := range a.Fields {
			mark := "  "
			if f.Name == a.Focused {
				mark = "> "
			}
			req := ""
			if f.Required {
				req = ", required"
			}
			fmt.Fprintf(w, "%s%s (%s%s) = %q\n", mark, f.Name, f.Type, req, f.Value)
		}
	}
	if s.Panel != nil {
		fmt.Fprintf(w, "actions for %q:\n", s.Panel.Title)
		for i, a := range s.Panel.Actions {
			mark := "  "
			if i == s.Panel.Selected {
				mark = "> "
			}
			fmt.Fprintf(w, "%s%s%s\n", mark, a.Title, cmpShortcut(a.Shortcut))
		}
	}
	if s.Toast != nil {
		fmt.Fprintf(w, "toast: [%s] %s %s\n", s.Toast.Style, s.Toast.Title, s.Toast.Message)
	}
	if s.Tasks != nil {
		fmt.Fprintf(w, "tasks: %s\n", s.Tasks.Label)
	}
}

func (c *cli) plugin(ctx context.Context, args []string) error {
	var ef envFlags
	fs := c.flagSet("plugin", &ef)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return usagef("plugin: want list, info <id> or validate <dir>")
	}
	if pos[0] == "validate" {
		if len(pos) != 2 {
			return usagef("plugin validate: want <dir>")
		}
		return c.validate(pos[1])
	}
	e, err := c.build(ctx, ef)
	if err != nil {
		return err
	}
	defer e.Close()
	infos := e.Engine.Plugins()
	switch {
	case pos[0] == "list":
		return c.output(infos, func(w io.Writer) {
			for _, p := range infos {
				state := "enabled"
				if !p.Enabled {
					state = "disabled"
				}
				if p.State != "" {
					state += ", " + p.State
				}
				fmt.Fprintf(w, "%-20s %-24s %-8s %s %s\n", p.ID, p.Name, p.Origin, p.Version, state)
			}
		})
	case pos[0] == "info" && len(pos) == 2:
		i := slices.IndexFunc(infos, func(p core.PluginInfo) bool { return p.ID == pos[1] })
		if i < 0 {
			return fmt.Errorf("plugin %q not found", pos[1])
		}
		return c.output(infos[i], nil)
	default:
		return usagef("plugin: want list or info <id>")
	}
}

// Version is the glctl version, set by cmd/glctl from its build-time
// version (-ldflags "-X main.version=...").
var Version = "dev"

func (c *cli) version(args []string) error {
	pos, err := parse(c.flagSet("version", nil), args)
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return usagef("version: takes no arguments")
	}
	return c.output(struct {
		Version string `json:"version"`
	}{Version}, func(w io.Writer) { fmt.Fprintln(w, Version) })
}

// exit is a hook for tests.
var exit = os.Exit
