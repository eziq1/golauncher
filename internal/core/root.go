package core

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"slices"
	"strings"
	"sync"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/match"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Ranking weights; see docs/design.md §5.6.
const (
	weightMatch    = 0.7
	weightFrecency = 0.3
	aliasBonus     = 1.0
	// destructivePenalty orders destructive entries after equal matches.
	destructivePenalty = 0.05
	// frecencyHalf is the use count at which frecency contributes half its weight.
	frecencyHalf = 2.0
	maxRootItems = 50
	maxRecent    = 6
)

// RootSearch returns the root search results for text.
func (e *Engine) RootSearch(ctx context.Context, text string) ([]Section, error) {
	cfg := e.cfg.Get()
	text = strings.TrimSpace(text)
	if text == "" {
		return e.rootEmpty(cfg)
	}
	freq, err := e.svc.DB.Frecency(e.svc.Now())
	if err != nil {
		return nil, err
	}
	candidates := e.commandEntries(cfg)
	candidates = append(candidates, e.pluginRootItems(ctx, cfg, text)...)

	var ranked []Entry
	for _, c := range candidates {
		m := c.Score
		if m == 0 {
			m = entryMatch(text, c)
		}
		if m == 0 {
			continue
		}
		f := freq[c.Key]
		c.Rank = weightMatch*m + weightFrecency*f/(f+frecencyHalf)
		if c.Score > 0 {
			// Pre-matched results (e.g. a calculation) answer the query directly.
			c.Rank += aliasBonus
		}
		if a := aliasOf(cfg, c); a != "" && strings.EqualFold(a, text) {
			c.Rank += aliasBonus
		}
		if len(c.Actions) > 0 && c.Actions[0].Style == plugin.StyleDestructive {
			// Never let a destructive primary action win a tie.
			c.Rank -= destructivePenalty
		}
		ranked = append(ranked, c)
	}
	slices.SortStableFunc(ranked, func(a, b Entry) int {
		if c := cmp.Compare(b.Rank, a.Rank); c != 0 {
			return c
		}
		return cmp.Compare(a.Title, b.Title)
	})
	if len(ranked) > maxRootItems {
		ranked = ranked[:maxRootItems]
	}
	return group(ranked), nil
}

// group splits ranked entries into sections by Group, ordering sections by
// their best entry.
func group(ranked []Entry) []Section {
	var out []Section
	idx := map[string]int{}
	for _, it := range ranked {
		i, ok := idx[it.Group]
		if !ok {
			i = len(out)
			idx[it.Group] = i
			out = append(out, Section{Title: it.Group})
		}
		out[i].Items = append(out[i].Items, it)
	}
	return out
}

// entryMatch scores text against an entry's title, keywords and subtitle.
func entryMatch(text string, it Entry) float64 {
	best := 0.0
	for _, k := range match.Keys(it.Title) {
		best = max(best, match.Score(text, k))
	}
	for _, kw := range it.Keywords {
		best = max(best, 0.95*match.Score(text, kw))
	}
	if it.Subtitle != "" {
		best = max(best, 0.6*match.Score(text, it.Subtitle))
	}
	return best
}

// pluginRootItems collects RootItems from all enabled plugins concurrently,
// dropping plugins that exceed the root timeout.
func (e *Engine) pluginRootItems(ctx context.Context, cfg config.Config, text string) []Entry {
	ctx, cancel := context.WithTimeout(ctx, e.rootTimeout)
	defer cancel()
	var (
		mu  sync.Mutex
		out []Entry
		wg  sync.WaitGroup
	)
	for _, l := range e.enabled(cfg) {
		rp, ok := l.p.(plugin.RootProvider)
		if !ok {
			continue
		}
		wg.Go(func() {
			done := make(chan []plugin.Item, 1)
			go func() {
				items, err := guard(e, l.m.ID, func() ([]plugin.Item, error) { return rp.RootItems(ctx, plugin.Query{Text: text}) })
				if err != nil {
					e.log.Warn("root items", "plugin", l.m.ID, "err", err)
				}
				done <- items
			}()
			select {
			case items := <-done:
				mu.Lock()
				for _, it := range items {
					out = append(out, newEntry(l.m.ID, it))
				}
				mu.Unlock()
			case <-ctx.Done():
				e.log.Warn("root items timed out", "plugin", l.m.ID)
			}
		})
	}
	wg.Wait()
	return out
}

// rootEmpty is the root list before anything is typed: recently used items
// and all commands.
func (e *Engine) rootEmpty(cfg config.Config) ([]Section, error) {
	var out []Section
	recent, err := e.svc.DB.Recent(maxRecent * 2)
	if err != nil {
		return nil, err
	}
	commands := e.commandEntries(cfg)
	rec := Section{Title: e.core.T("section.recent")}
	for _, b := range recent {
		var ent Entry
		if err := json.Unmarshal(b, &ent); err != nil {
			continue
		}
		if _, ok := e.byID[ent.Plugin]; !ok || cfg.Plugin(ent.Plugin).Disabled {
			continue
		}
		if name, ok := strings.CutPrefix(ent.ID, cmdPrefix); ok {
			// Rebuild commands so their titles follow the current locale.
			if ent, ok = e.commandEntry(cfg, ent.Plugin, name); !ok {
				continue
			}
		}
		ent.Rank = 0
		rec.Items = append(rec.Items, ent)
		if len(rec.Items) == maxRecent {
			break
		}
	}
	if len(rec.Items) > 0 {
		out = append(out, rec)
	}
	if len(commands) > 0 {
		slices.SortStableFunc(commands, func(a, b Entry) int { return cmp.Compare(a.Title, b.Title) })
		out = append(out, Section{Title: e.core.T("section.commands"), Items: commands})
	}
	return out, nil
}

const cmdPrefix = "cmd:"

// commandEntries lists the root entries of all visible commands.
func (e *Engine) commandEntries(cfg config.Config) []Entry {
	var out []Entry
	for _, l := range e.enabled(cfg) {
		for _, c := range l.m.Commands {
			if cfg.Plugin(l.m.ID).Commands[c.Name].HideFromRoot {
				continue
			}
			if ent, ok := e.commandEntry(cfg, l.m.ID, c.Name); ok {
				out = append(out, ent)
			}
		}
	}
	return out
}

// commandEntry builds the root entry of one command.
func (e *Engine) commandEntry(cfg config.Config, pluginID, name string) (Entry, bool) {
	l, ok := e.byID[pluginID]
	if !ok {
		return Entry{}, false
	}
	i := slices.IndexFunc(l.m.Commands, func(c plugin.Command) bool { return c.Name == name })
	if i < 0 {
		return Entry{}, false
	}
	c := l.m.Commands[i]
	icon := c.Icon
	if icon == (plugin.Icon{}) {
		icon = l.m.Icon
	}
	var keywords []string
	for _, k := range c.Keywords {
		// A keyword may be a %key% holding several comma-separated words.
		for w := range strings.SplitSeq(l.loc.Resolve(k), ",") {
			if w = strings.TrimSpace(w); w != "" {
				keywords = append(keywords, w)
			}
		}
	}
	if a := cfg.Plugin(pluginID).Commands[name].Alias; a != "" {
		keywords = append(keywords, a)
	}
	do := plugin.ActionDo{Kind: plugin.ActionPush, Target: c.Name}
	if c.Mode == plugin.ModeNoView {
		do = plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: c.Name}
	}
	style := plugin.StyleDefault
	if c.Destructive {
		style = plugin.StyleDestructive
	}
	it := plugin.Item{
		ID:        cmdPrefix + c.Name,
		Title:     l.loc.Resolve(c.Title),
		Subtitle:  l.loc.Resolve(l.m.Name),
		Icon:      icon,
		Keywords:  keywords,
		Accessory: e.core.T("accessory.command"),
		Group:     e.core.T("section.commands"),
		Actions:   []plugin.Action{{ID: "run", Title: e.core.T("action.openCommand"), Style: style, Do: do}},
	}
	for _, a := range c.Arguments {
		a.Placeholder = l.loc.Resolve(a.Placeholder)
		a.Options = resolveOptions(l, a.Options)
		it.Arguments = append(it.Arguments, a)
	}
	return newEntry(pluginID, it), true
}

// aliasOf returns the alias configured for a command entry.
func aliasOf(cfg config.Config, ent Entry) string {
	name, ok := strings.CutPrefix(ent.ID, cmdPrefix)
	if !ok {
		return ""
	}
	return cfg.Plugin(ent.Plugin).Commands[name].Alias
}
