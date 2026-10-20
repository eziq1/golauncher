package agentrt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/winapi/proc"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// Defaults for Options.
const (
	DefaultMaxProcesses = 2
	DefaultGracePeriod  = 2 * time.Second
)

// interruptTimeout is how long an interrupted answer may take to end before
// the process is killed.
const interruptTimeout = 5 * time.Second

// checkTimeout caps the version check; handshakeTimeout a Handshaker.
const (
	checkTimeout     = 30 * time.Second
	handshakeTimeout = 60 * time.Second
)

// Options configure a Runtime.
type Options struct {
	// Path is the CLI to run (a "CLI path" preference); empty looks it up on
	// PATH and in the usual install locations (Info.Commands,
	// Info.InstallPaths).
	Path string
	// Dir is the working directory of the CLI; empty is
	// %LOCALAPPDATA%\golauncher\ai (plus Info.WorkDir). It is created when
	// missing.
	Dir string
	// Log receives lengths, durations and errors, never message content.
	Log *slog.Logger
	// PerTurn forces one process per turn (unless the CLI has no per-turn
	// mode).
	PerTurn bool
	// MaxProcesses caps the persistent processes kept across conversations
	// (DefaultMaxProcesses when 0). A busy one is never stopped, so the cap
	// can be exceeded for a while.
	MaxProcesses int
	// Env are NAME=value entries added to (or replacing in) the CLI's
	// environment.
	Env []string
	// GracePeriod is how long a released process may take to exit before it
	// is killed (DefaultGracePeriod when 0).
	GracePeriod time.Duration
	// Tools is golauncher's MCP tool server (docs/ai.md §4.3). With it, a
	// conversation opened with OpenOptions.LauncherTools lets the model use
	// the launcher's plugins through that server's tools, and no others.
	Tools *ToolServer
}

// Runtime is a backend.Backend running an agent CLI through an Adapter. It is
// safe for concurrent use.
type Runtime struct {
	a    Adapter
	info Info
	o    Options
	log  *slog.Logger

	mu       sync.Mutex
	cli      *cliInfo
	stale    bool               // a re-detection asked to check cli again (Resolved)
	disc     *backend.Discovery // the last discovery (R84)
	tried    string             // the CLI of the last failed check, for messages
	convs    map[*conversation]bool
	live     []*conversation // with a persistent process, least recently used first
	sessions *SessionMap
	closed   bool

	caps       capsCache  // the probed flags of the CLIs
	capsFileMu sync.Mutex // writes of the cache file
}

var _ backend.Backend = (*Runtime)(nil)

// cliInfo is what Check found.
type cliInfo struct {
	path     string
	version  string
	untested bool
	flags    Flags // nil when unknown
	fit      Fit
	// fallback: the configured CLI no longer exists and this is the
	// automatic choice (the chat says so, backend.NoticeProgramMissing).
	fallback bool
}

// New returns a runtime for the CLI of a, configured by o.
func New(a Adapter, o Options) *Runtime {
	if o.MaxProcesses <= 0 {
		o.MaxProcesses = DefaultMaxProcesses
	}
	if o.GracePeriod <= 0 {
		o.GracePeriod = DefaultGracePeriod
	}
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	info := a.Info()
	if o.Path != "" && info.ResolvePath != nil {
		o.Path = info.ResolvePath(o.Path)
	}
	return &Runtime{a: a, info: info, o: o, log: log.With("backend", info.ID), convs: map[*conversation]bool{}}
}

// Name implements backend.Backend.
func (r *Runtime) Name() string { return r.info.Name }

// Attachments implements backend.AttachmentCapser: Info.Attachments.
func (r *Runtime) Attachments() backend.AttachmentCaps { return r.info.Attachments }

// errorf is an internal error of this backend, prefixed with its ID.
func (r *Runtime) errorf(format string, args ...any) error {
	return fmt.Errorf(r.info.ID+": "+format, args...)
}

// Check implements backend.Backend: it finds the CLI and checks its version
// (and, with Info.LoginArgs, that it is signed in). Of several candidates
// (Info.Candidates) the one with the highest acceptable version is used.
func (r *Runtime) Check(ctx context.Context) (backend.Status, error) {
	configured := r.configured()
	tiers, missing, err := r.findAll(configured)
	if err != nil {
		return backend.Status{}, err
	}
	if missing {
		r.log.Warn("the configured CLI no longer exists; using the automatic choice")
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	var best *cliInfo
	var firstErr error
	tried := ""
	var paths []string
	for _, t := range tiers {
		for _, path := range t.paths {
			paths = append(paths, path)
			cli, err := r.checkOne(ctx, path)
			if err != nil {
				if firstErr == nil || backend.KindOf(firstErr) != backend.ErrVersion && backend.KindOf(err) == backend.ErrVersion {
					firstErr, tried = err, path
				}
				continue
			}
			if best == nil || CompareVersions(cli.version, best.version) > 0 {
				best = cli
			}
		}
		if best != nil {
			break
		}
	}
	if best == nil {
		r.mu.Lock()
		r.tried = tried
		r.mu.Unlock()
		return backend.Status{}, firstErr
	}
	best.fallback = missing
	// Which kind of installation runs, never its path (docs/ai.md §9).
	r.log.Debug("agent CLI chosen", "source", r.sourceOf(best.path), "configured", configured != "" && !missing, "candidates", len(paths))
	if len(r.info.LoginArgs) > 0 {
		out, code, err := r.run(ctx, best.path, r.info.LoginArgs)
		if err != nil {
			return backend.Status{}, &backend.Error{Kind: backend.ErrFailed, Message: err.Error()}
		}
		if code != 0 {
			return backend.Status{}, &backend.Error{Kind: backend.ErrNotLoggedIn, Message: strings.TrimSpace(out)}
		}
	}
	r.mu.Lock()
	r.cli, r.tried, r.stale = best, "", false
	r.mu.Unlock()
	r.log.Info("agent CLI found", "version", best.version, "untested", best.untested, "candidates", len(paths),
		"probed", best.flags != nil, "perTurn", best.fit.PerTurn)
	return backend.Status{Version: best.version, Untested: best.untested, Reduced: slices.Clone(best.fit.Reduced)}, nil
}

// checkOne checks the version of the CLI at path.
func (r *Runtime) checkOne(ctx context.Context, path string) (*cliInfo, error) {
	out, code, err := r.run(ctx, path, r.info.VersionArgs)
	if err != nil {
		return nil, &backend.Error{Kind: backend.ErrFailed, Message: err.Error()}
	}
	parse := r.info.ParseVersion
	if parse == nil {
		parse = ParseVersion
	}
	ver := parse(out)
	if code != 0 || ver == "" {
		msg := strings.TrimSpace(out)
		if msg == "" {
			msg = fmt.Sprintf("%s: exit code %d", strings.Join(append([]string{r.command()}, r.info.VersionArgs...), " "), code)
		}
		return nil, &backend.Error{Kind: backend.ErrFailed, Message: msg}
	}
	if r.info.MinVersion != "" && CompareVersions(ver, r.info.MinVersion) < 0 {
		return nil, &backend.Error{Kind: backend.ErrVersion, Version: ver,
			Message: fmt.Sprintf("%s %s was found; golauncher needs %s or later.", r.info.Name, ver, r.info.MinVersion)}
	}
	flags, fresh := r.probeFlags(ctx, path, ver)
	fit, err := r.fit(ver, flags)
	if err != nil {
		if fresh {
			r.log.Info("the agent CLI lacks what golauncher needs", "version", ver, "probed", flags != nil, "error", err.Error())
		}
		return nil, err
	}
	if fresh && (len(fit.Dropped) > 0 || len(fit.Reduced) > 0 || fit.PerTurn) {
		// Once per probe: what this CLI is run without.
		r.log.Info("older agent CLI: running it with fewer flags", "version", ver, "probed", flags != nil,
			"dropped", strings.Join(fit.Dropped, "; "), "reduced", strings.Join(fit.Reduced, ","), "perTurn", fit.PerTurn)
	}
	return &cliInfo{path: path, version: ver, untested: MajorOf(ver) != r.info.TestedMajor, flags: flags, fit: fit}, nil
}

// Forget implements backend.Forgetter: the CLI's session ID of a deleted
// chat is dropped from the session map (CLIs that assign their own IDs).
// The CLI's own session files are left alone.
func (r *Runtime) Forget(chatID string) {
	m := r.sessionMap()
	if m == nil {
		return
	}
	if err := m.Delete(chatID); err != nil {
		r.log.Warn("forgetting the session ID failed", "error", err)
	}
}

// command is the CLI's command name, for messages.
func (r *Runtime) command() string {
	if len(r.info.Commands) > 0 {
		return r.info.Commands[0]
	}
	return r.info.ID
}

// resolve returns what the last Check found, running it when needed.
func (r *Runtime) resolve(ctx context.Context) (*cliInfo, error) {
	r.mu.Lock()
	cli := r.cli
	r.mu.Unlock()
	if cli != nil && isFile(cli.path) { // a CLI that was removed is looked for again
		return cli, nil
	}
	if _, err := r.Check(ctx); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cli, nil
}

// CLI is the CLI a check chose (Resolved).
type CLI struct {
	Path, Version string
	// Flags are its probed flags (nil when unknown).
	Flags Flags
}

// Resolved returns the CLI the last check chose, checking first when there
// was none, it was removed, or the installations were detected again since
// (Discover with refresh), so that an update is noticed. Settings that
// depend on the CLI (its models, R109) use it.
func (r *Runtime) Resolved(ctx context.Context) (CLI, error) {
	r.mu.Lock()
	cli, stale := r.cli, r.stale
	r.mu.Unlock()
	if cli == nil || stale || !isFile(cli.path) {
		if _, err := r.Check(ctx); err != nil {
			return CLI{}, err
		}
		r.mu.Lock()
		cli = r.cli
		r.mu.Unlock()
	}
	return CLI{Path: cli.path, Version: cli.version, Flags: cli.flags}, nil
}

// RunCLI runs the CLI at path with args the way the checks do (the CLI's
// working directory and environment, no console window) and returns its
// standard output, the end of its standard error and its exit code. More
// than maxOut bytes of output is an error. It is for read-only commands
// that print something to parse (Codex's model list, R109).
func (r *Runtime) RunCLI(ctx context.Context, path string, args []string, maxOut int) (stdout, stderr string, code int, err error) {
	dir, err := r.dir()
	if err != nil {
		return "", "", 0, err
	}
	out := &capped{max: maxOut}
	errOut := &tail{max: 4096}
	p, err := proc.Start(proc.Options{Argv: append([]string{path}, args...), Dir: dir, Env: r.envFor(path), Output: out, Stderr: errOut})
	if err != nil {
		return "", "", 0, err
	}
	stop := context.AfterFunc(ctx, p.Stop)
	defer stop()
	code, err = p.Wait()
	if err != nil {
		return "", "", 0, err
	}
	if ctx.Err() != nil {
		return "", "", 0, ctx.Err()
	}
	s, over := out.result()
	if over {
		return "", errOut.String(), code, fmt.Errorf("%s printed more than %d bytes", r.info.Name, maxOut)
	}
	return s, errOut.String(), code, nil
}

func (c *capped) result() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.b.String(), c.over
}

// capped keeps up to max bytes written to it and notes when there was more.
type capped struct {
	max  int
	mu   sync.Mutex
	b    strings.Builder
	over bool
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.b.Len()+len(p) > c.max {
		c.over = true
		return len(p), nil
	}
	c.b.Write(p)
	return len(p), nil
}

// Open implements backend.Backend. The process is started by the first Send.
func (r *Runtime) Open(_ context.Context, o backend.OpenOptions) (backend.Conversation, error) {
	if o.SessionID == "" {
		return nil, r.errorf("no session ID")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, r.errorf("backend closed")
	}
	c := &conversation{r: r, oo: o, closedCh: make(chan struct{})}
	r.convs[c] = true
	return c, nil
}

// Close ends every conversation and kills their processes.
func (r *Runtime) Close() error {
	r.mu.Lock()
	r.closed = true
	convs := make([]*conversation, 0, len(r.convs))
	for c := range r.convs {
		convs = append(convs, c)
	}
	r.mu.Unlock()
	for _, c := range convs {
		c.Close()
	}
	return nil
}

// Reopen lets a closed runtime start conversations again: the AI chat
// plugin was turned off (Close) and on again (docs/plugin-guide.md
// "Lifecycle").
func (r *Runtime) Reopen() {
	r.mu.Lock()
	r.closed = false
	r.mu.Unlock()
}

// LiveCount is the number of conversations with a persistent process.
func (r *Runtime) LiveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.live)
}

// Command is the command line, and the variables added to the environment,
// of a process of a conversation opened with o, for the CLI at path
// (diagnostics and tests; nothing is written or started).
func (r *Runtime) Command(o backend.OpenOptions, path string, persistent, resume bool) (Command, error) {
	return r.CommandAt(o, path, "", persistent, resume)
}

// CommandAt is Command for a CLI of the given version.
func (r *Runtime) CommandAt(o backend.OpenOptions, path, version string, persistent, resume bool) (Command, error) {
	return r.CommandFor(o, path, version, nil, persistent, resume)
}

// CommandFor is Command for a CLI of the given version and probed flags
// (nil: unknown).
func (r *Runtime) CommandFor(o backend.OpenOptions, path, version string, flags Flags, persistent, resume bool) (Command, error) {
	return r.CommandWith(o, path, version, flags, persistent, resume, nil)
}

// CommandWith is CommandFor for a per-turn process whose message has the
// attachments atts (Launch.Attachments).
func (r *Runtime) CommandWith(o backend.OpenOptions, path, version string, flags Flags, persistent, resume bool, atts []backend.Attachment) (Command, error) {
	c := &conversation{r: r, oo: o}
	dir := r.o.Dir
	l := c.launch(path, dir, persistent, resume, "")
	l.Version, l.Flags = version, flags
	if !persistent {
		l.Attachments = atts
	}
	if l.Tools != nil {
		if b, err := r.a.MCPConfig(*l.Tools); err == nil && b != nil {
			l.MCPConfig = filepath.Join(dir, MCPConfigName(o.SessionID))
		}
	}
	return r.a.Command(l)
}

// forget removes a closed conversation.
func (r *Runtime) forget(c *conversation) {
	r.mu.Lock()
	delete(r.convs, c)
	r.live = slices.DeleteFunc(r.live, func(x *conversation) bool { return x == c })
	r.mu.Unlock()
}

// acquire records that c started a persistent process and returns the idle
// conversations whose processes must be released to stay within
// MaxProcesses, least recently used first.
func (r *Runtime) acquire(c *conversation) []*conversation {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.live = slices.DeleteFunc(r.live, func(x *conversation) bool { return x == c })
	r.live = append(r.live, c)
	var victims []*conversation
	excess := len(r.live) - r.o.MaxProcesses
	for _, x := range r.live {
		if excess <= 0 {
			break
		}
		if x != c && !x.busy.Load() {
			victims = append(victims, x)
			excess--
		}
	}
	return victims
}

// touch marks c as the most recently used, if it has a process.
func (r *Runtime) touch(c *conversation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i := slices.Index(r.live, c); i >= 0 {
		r.live = append(slices.Delete(r.live, i, i+1), c)
	}
}

// drop records that c no longer has a persistent process.
func (r *Runtime) drop(c *conversation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.live = slices.DeleteFunc(r.live, func(x *conversation) bool { return x == c })
}

// tier is a group of CLIs the automatic choice chooses from: the first tier
// with a usable CLI is used, and in it the highest version (autoTiers).
type tier struct {
	paths []string
	// def: the CLIs under a version manager's default Node.js version.
	def bool
}

// findAll locates the CLIs to choose from: the configured one, else the
// automatic choice's (autoTiers). When the configured CLI no longer exists
// (missing), the automatic choice is used instead (R84); when that finds
// nothing either, the error names the configured path.
func (r *Runtime) findAll(configured string) (tiers []tier, missing bool, err error) {
	if configured != "" {
		if isFile(configured) {
			return []tier{{paths: []string{configured}}}, false, nil
		}
		tiers, err := r.autoTiers()
		if err != nil {
			return nil, true, &backend.Error{Kind: backend.ErrNotInstalled, Message: configured}
		}
		return tiers, true, nil
	}
	tiers, err = r.autoTiers()
	return tiers, false, err
}

// autoTiers are the CLIs the automatic choice chooses from (docs/ai.md
// §22.7), in tiers tried in order:
//
//  1. the adapter's candidates (Info.Candidates) that are not under a Node.js
//     version manager, or, without candidates, the first of Info.Commands
//     found on PATH, in Info.InstallPaths or in the usual install folders
//     (as exec.LookPath, then the install paths);
//  2. those under a version manager's default Node.js version (Found.Default);
//  3. the others under a version manager.
//
// A version manager's installations are so only used when nothing on PATH
// or in the usual folders can be.
func (r *Runtime) autoTiers() ([]tier, error) {
	info := r.a.Info() // the install locations may depend on the environment
	var plain, def, managed []string
	if c := info.Candidates; c != nil {
		paths, err := c()
		if len(paths) == 0 {
			if _, ok := errors.AsType[*backend.Error](err); ok {
				return nil, err
			}
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			return nil, &backend.Error{Kind: backend.ErrNotInstalled, Message: msg}
		}
		var found []Found
		if info.Discover != nil {
			found = info.Discover()
		}
		for _, p := range paths {
			i := slices.IndexFunc(found, func(f Found) bool { return samePath(f.Path, p) })
			switch {
			case i < 0 || found[i].Manager == "":
				plain = append(plain, p)
			case found[i].Default:
				def = append(def, p)
			default:
				managed = append(managed, p)
			}
		}
	} else {
		for _, f := range r.found() {
			switch {
			case f.Launcher:
			case f.Manager == "":
				if len(plain) == 0 {
					plain = append(plain, f.Path)
				}
			case f.Default:
				def = append(def, f.Path)
			default:
				managed = append(managed, f.Path)
			}
		}
	}
	var tiers []tier
	for _, t := range []tier{{paths: plain}, {paths: def, def: true}, {paths: managed}} {
		if len(t.paths) > 0 {
			tiers = append(tiers, t)
		}
	}
	if len(tiers) == 0 {
		return nil, &backend.Error{Kind: backend.ErrNotInstalled}
	}
	return tiers, nil
}

// dir returns the CLI's working directory, creating it.
func (r *Runtime) dir() (string, error) {
	d := r.o.Dir
	if d == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		d = filepath.Join(cache, "golauncher", "ai")
		if r.info.WorkDir != "" {
			d = filepath.Join(d, r.info.WorkDir)
		}
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return d, nil
}

// sessionMap returns the map of the CLI's session IDs, nil when the chat's
// ID is the CLI's.
func (r *Runtime) sessionMap() *SessionMap {
	if !r.info.AssignsSessionID {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sessions == nil {
		dir, err := r.dir()
		if err != nil {
			dir = os.TempDir()
		}
		r.sessions = NewSessionMap(filepath.Join(dir, "sessions.json"))
	}
	return r.sessions
}

// Env returns the CLI's environment: the launcher's without Info.EnvDrop,
// with Options.Env.
func (r *Runtime) Env() []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return slices.ContainsFunc(r.info.EnvDrop, func(d string) bool { return strings.EqualFold(k, d) })
	})
	return proc.MergeEnv(env, r.o.Env)
}

// envFor is Env with the variables the CLI at path needs (Info.PathEnv), and
// with the CLI's folder first on PATH when PATH lacks it (R111): a CLI found
// outside PATH (a Node.js version manager's folder, npm's prefix, a program
// the user chose) is often a Node.js script ("#!/usr/bin/env node", an npm
// .cmd shim) whose node is next to it. Every process of the CLI gets this
// environment: the version, help and sign-in checks and the conversations.
func (r *Runtime) envFor(path string) []string {
	env := r.Env()
	if f := r.info.PathEnv; f != nil {
		env = proc.MergeEnv(env, f(path))
	}
	return withDirOnPath(env, path, runtime.GOOS)
}

// withDirOnPath returns env with the folder of the program path first on
// PATH, unless path is not absolute or PATH has the folder already.
func withDirOnPath(env []string, path, goos string) []string {
	if !filepath.IsAbs(path) {
		return env
	}
	dir := filepath.Clean(filepath.Dir(path))
	cur := proc.Getenv(env, "PATH")
	for _, d := range filepath.SplitList(cur) {
		d = filepath.Clean(strings.Trim(strings.TrimSpace(d), `"`))
		if d == dir || goos == "windows" && strings.EqualFold(d, dir) {
			return env
		}
	}
	v := dir
	if cur != "" {
		v += string(os.PathListSeparator) + cur
	}
	return proc.MergeEnv(env, []string{"PATH=" + v})
}

// run runs the CLI with args (the version or login check) and returns its
// output and exit code.
func (r *Runtime) run(ctx context.Context, path string, args []string) (string, int, error) {
	return r.runMax(ctx, path, args, 4096)
}

// runMax is run keeping the last max bytes of the output.
func (r *Runtime) runMax(ctx context.Context, path string, args []string, max int) (string, int, error) {
	dir, err := r.dir()
	if err != nil {
		return "", 0, err
	}
	out := &tail{max: max}
	p, err := proc.Start(proc.Options{Argv: append([]string{path}, args...), Dir: dir, Env: r.envFor(path), Output: out})
	if err != nil {
		return "", 0, err
	}
	stop := context.AfterFunc(ctx, p.Stop)
	defer stop()
	code, err := p.Wait()
	if err != nil {
		return "", 0, err
	}
	if ctx.Err() != nil {
		return "", 0, ctx.Err()
	}
	return out.String(), code, nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
