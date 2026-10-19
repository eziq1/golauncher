package codex

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// Codex's models (R109, docs/ai.md §25). They depend on the account and the
// CLI version, so they are read from the CLI: `codex debug models` prints
// the catalog as JSON ({"models":[…]}, each with slug, display_name,
// visibility "list" or "hide", priority, supported_reasoning_levels
// [{effort, description}], default_reasoning_level). Without --bundled it
// first refreshes the catalog for the signed-in account; with it, it prints
// the catalog built into the binary, offline. Verified with 0.130 and 0.163.
//
// `debug models` has no --ignore-user-config, so the user's config.toml is
// read; -c model_provider="openai" keeps a custom provider there from
// changing the list (the chats ignore that file and use OpenAI's), and the
// update check is off as in the chats.

// modelsTimeout caps one `codex debug models` run (a variable for the tests).
var modelsTimeout = 15 * time.Second

// modelsOutputMax caps its output (0.163's catalog is about 650 KB: every
// model carries its instructions).
const modelsOutputMax = 16 << 20

// modelsArgs are the arguments of the catalog command; --bundled is added
// for the offline fallback.
var modelsArgs = []string{"debug", "models", "-c", `model_provider="openai"`, "-c", "check_for_update_on_startup=false"}

// catalog is the part of `codex debug models`'s JSON that is used.
type catalog struct {
	Models []catalogModel `json:"models"`
}

type catalogModel struct {
	Slug                  string            `json:"slug"`
	DisplayName           string            `json:"display_name"`
	Visibility            string            `json:"visibility"`
	Priority              *int              `json:"priority"`
	DefaultReasoningLevel string            `json:"default_reasoning_level"`
	ReasoningLevels       []json.RawMessage `json:"supported_reasoning_levels"`
}

// parseCatalog returns the models of a catalog the user may choose
// (visibility "list"), by priority (the order of Codex's own model picker;
// the catalog's order is close to it but not the same), the catalog's order
// among equal or missing priorities (missing ones last). Hidden models
// (internal ones such as the auto reviewer) and entries without a slug are
// left out. A text that is not a catalog is an error.
func parseCatalog(out string) (backend.ModelList, error) {
	s := strings.TrimSpace(out)
	if i := strings.IndexByte(s, '{'); i > 0 {
		s = s[i:] // a line printed before the JSON
	}
	var c catalog
	dec := json.NewDecoder(strings.NewReader(s))
	if err := dec.Decode(&c); err != nil {
		return backend.ModelList{}, fmt.Errorf("the model list is not JSON: %w", err)
	}
	if c.Models == nil {
		return backend.ModelList{}, errors.New("the model list has no models")
	}
	type ranked struct {
		m        backend.Model
		priority *int
	}
	var list []ranked
	for _, m := range c.Models {
		if m.Visibility != "list" || m.Slug == "" {
			continue
		}
		bm := backend.Model{ID: m.Slug, Name: cmp.Or(strings.TrimSpace(m.DisplayName), m.Slug), DefaultEffort: m.DefaultReasoningLevel}
		for _, raw := range m.ReasoningLevels {
			if e := effortOf(raw); validEffort(e) && !slices.Contains(bm.Efforts, e) {
				bm.Efforts = append(bm.Efforts, e)
			}
		}
		list = append(list, ranked{bm, m.Priority})
	}
	slices.SortStableFunc(list, func(a, b ranked) int {
		switch {
		case a.priority == nil && b.priority == nil:
			return 0
		case a.priority == nil:
			return 1
		case b.priority == nil:
			return -1
		}
		return cmp.Compare(*a.priority, *b.priority)
	})
	out2 := backend.ModelList{Models: make([]backend.Model, len(list))}
	for i, r := range list {
		out2.Models[i] = r.m
	}
	return out2, nil
}

// effortOf is the level of a supported_reasoning_levels entry:
// {"effort":"high","description":…}, or a plain "high".
func effortOf(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		Effort string `json:"effort"`
	}
	if json.Unmarshal(raw, &o) == nil {
		return o.Effort
	}
	return ""
}

// validEffort reports whether e can be passed as a level: lower-case letters,
// digits, '_' and '-' (it goes into -c model_reasoning_effort="…").
func validEffort(e string) bool {
	if e == "" || len(e) > 32 {
		return false
	}
	for _, r := range e {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// runner runs the CLI (agentrt.Runtime.RunCLI).
type runner func(ctx context.Context, path string, args []string, maxOut int) (stdout, stderr string, code int, err error)

// fetchModels runs `codex debug models` at path: the account's list, else
// the bundled one (Bundled), else an error saying why. version names the CLI
// in the message when it cannot list its models.
func fetchModels(ctx context.Context, run runner, path, version string) (backend.ModelList, error) {
	try := func(args []string) (backend.ModelList, error) {
		ctx, cancel := context.WithTimeout(ctx, modelsTimeout)
		defer cancel()
		out, stderr, code, err := run(ctx, path, args, modelsOutputMax)
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return backend.ModelList{}, fmt.Errorf("`codex %s` did not finish within %s", strings.Join(args[:2], " "), modelsTimeout)
		case err != nil:
			return backend.ModelList{}, err
		case code != 0:
			msg := lastLine(stderr)
			if unknownCommand(stderr) {
				return backend.ModelList{}, &backend.Error{Kind: backend.ErrVersion, Version: version,
					Message: fmt.Sprintf("Codex %s cannot list its models (no `codex debug models`).", cmp.Or(version, "this version"))}
			}
			if msg == "" {
				msg = fmt.Sprintf("exit code %d", code)
			}
			return backend.ModelList{}, fmt.Errorf("`codex %s` failed: %s", strings.Join(args[:2], " "), msg)
		}
		return parseCatalog(out)
	}
	list, err := try(modelsArgs)
	if err == nil {
		return list, nil
	}
	if backend.KindOf(err) == backend.ErrVersion {
		return backend.ModelList{}, err
	}
	bundled, berr := try(append(slices.Clone(modelsArgs), "--bundled"))
	if berr == nil {
		bundled.Bundled = true
		return bundled, nil
	}
	if e, ok := errors.AsType[*backend.Error](berr); ok {
		return backend.ModelList{}, e
	}
	return backend.ModelList{}, &backend.Error{Kind: backend.ErrFailed, Message: err.Error()}
}

// unknownCommand reports whether stderr is clap's complaint about a
// subcommand or flag this version does not have.
func unknownCommand(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "unrecognized subcommand") || strings.Contains(s, "unexpected argument")
}

// lastLine is the last non-empty line of s.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// modelsKey identifies a CLI binary: another program, version, size or
// modification time lists its models again.
type modelsKey struct {
	path, version string
	size, mtime   int64
}

// modelsCache keeps the lists in memory. A list is fetched by one run at a
// time per CLI (Settings loads the model and effort options at once), in the
// background: a caller that gives up (the settings page waits a few
// seconds) leaves it running, and the next caller finds it.
type modelsCache struct {
	mu      sync.Mutex
	m       map[modelsKey]modelsEntry
	pending map[modelsKey]*modelsFlight
	gen     int // raised by Redetect: fetches started before do not store
}

type modelsEntry struct {
	list backend.ModelList
	err  error
}

// modelsFlight is a fetch in progress; done closes when its entry is set.
type modelsFlight struct {
	done  chan struct{}
	entry modelsEntry
}

// maxModelsEntries caps the lists kept (older installations drop out).
const maxModelsEntries = 4

var _ backend.ModelLister = (*Backend)(nil)

// inUse is the CLI the chats run, as the Backends section shows it (R84):
// the program chosen in the settings or the automatic choice, signed in or
// not (the bundled list needs no sign-in).
func (b *Backend) inUse(ctx context.Context) (path, version string, err error) {
	d := b.Discover(ctx, false)
	in, ok := d.InUseInstall()
	switch {
	case ok && in.Usable():
		return in.Path, in.Version, nil
	case d.Status == backend.StatusTooOld || ok && in.TooOld:
		return "", "", &backend.Error{Kind: backend.ErrVersion, Version: in.Version,
			Message: fmt.Sprintf("Codex %s was found; golauncher needs %s or later.", cmp.Or(in.Version, "(unknown)"), MinVersion)}
	case d.Status == backend.StatusFailed:
		return "", "", &backend.Error{Kind: backend.ErrFailed, Message: cmp.Or(d.Message, in.VersionError)}
	}
	return "", "", &backend.Error{Kind: backend.ErrNotInstalled, Message: d.Message}
}

// Models implements backend.ModelLister: the models of the CLI in use, from
// the cache or by running `codex debug models` (a failure is kept too, until
// Redetect or another CLI binary). When ctx ends first, the error wraps
// ctx's and the list is still fetched for the next call.
func (b *Backend) Models(ctx context.Context) (backend.ModelList, error) {
	path, version, err := b.inUse(ctx)
	if err != nil {
		return backend.ModelList{}, err
	}
	key := modelsKey{path: path, version: version}
	if fi, err := os.Stat(path); err == nil {
		key.size, key.mtime = fi.Size(), fi.ModTime().UnixNano()
	}
	c := &b.models
	c.mu.Lock()
	if e, ok := c.m[key]; ok {
		c.mu.Unlock()
		return cloneList(e.list), e.err
	}
	f, running := c.pending[key]
	if !running {
		f = &modelsFlight{done: make(chan struct{})}
		if c.pending == nil {
			c.pending = map[modelsKey]*modelsFlight{}
		}
		c.pending[key] = f
		go b.fetch(key, f, c.gen)
	}
	c.mu.Unlock()
	select {
	case <-f.done:
		return cloneList(f.entry.list), f.entry.err
	case <-ctx.Done():
		return backend.ModelList{}, fmt.Errorf("codex: still listing the models: %w", ctx.Err())
	}
}

// fetch runs the catalog command for key and stores the result (unless
// Redetect came meanwhile).
func (b *Backend) fetch(key modelsKey, f *modelsFlight, gen int) {
	start := time.Now()
	list, err := fetchModels(context.Background(), b.RunCLI, key.path, key.version)
	b.log.Info("codex models listed", "models", len(list.Models), "bundled", list.Bundled, "error", err != nil, "duration", time.Since(start))
	c := &b.models
	c.mu.Lock()
	f.entry = modelsEntry{list: list, err: err}
	if c.pending[key] == f {
		delete(c.pending, key)
	}
	if c.gen == gen {
		if c.m == nil || len(c.m) >= maxModelsEntries {
			c.m = map[modelsKey]modelsEntry{}
		}
		c.m[key] = f.entry
	}
	c.mu.Unlock()
	close(f.done)
}

// Redetect implements backend.ModelLister.
func (b *Backend) Redetect() {
	b.models.mu.Lock()
	b.models.m = nil
	b.models.pending = nil // later callers start a fetch of their own
	b.models.gen++
	b.models.mu.Unlock()
}

func cloneList(l backend.ModelList) backend.ModelList {
	l.Models = slices.Clone(l.Models)
	return l
}

// effortConfig is the -c setting that passes a reasoning effort level
// (Codex's config key model_reasoning_effort; there is no flag for it).
func effortConfig(level string) string { return "model_reasoning_effort=" + tomlString(level) }
