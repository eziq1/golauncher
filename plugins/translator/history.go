package translator

import (
	"encoding/json/v2"
	"slices"
	"strconv"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Translation history (docs/translator.md §8, phase 3): off by default;
// when on, a translation is recorded when the user copies or pastes it (not
// while typing). It lives in the plugin's local storage only: never in the
// configuration, never synced or sent anywhere, pruned after the retention
// period, cleared with a button.

const (
	keyHistory = "history"
	// maxHistory is the most entries kept.
	maxHistory = 200
	// defaultHistoryDays is the default retention.
	defaultHistoryDays = 30
	viewHistory        = "history"
	cbUse              = "use"
	cbForget           = "forget"
	cbClearHistory     = "clearHistory"
)

// historyEntry is one recorded translation.
type historyEntry struct {
	Text        string    `json:"text"`
	Translation string    `json:"translation"`
	From        string    `json:"from,omitempty"`
	To          string    `json:"to"`
	Engine      string    `json:"engine"`
	At          time.Time `json:"at"`
}

// usePayload is the payload of cbUse: what to record and the action the
// host then carries out (CopyText or Paste).
type usePayload struct {
	Entry historyEntry    `json:"entry"`
	Do    plugin.ActionDo `json:"do"`
}

func (p *Plugin) historyOn() bool { return p.h.Preferences().Bool(prefHistory) }

// retention is how long entries are kept.
func (p *Plugin) retention() time.Duration {
	days, err := strconv.Atoi(p.h.Preferences().String(prefHistoryDays))
	if err != nil || days <= 0 {
		days = defaultHistoryDays
	}
	return time.Duration(days) * 24 * time.Hour
}

// loadHistory reads the entries, newest first, without those older than
// the retention period.
func (p *Plugin) loadHistory() []historyEntry {
	b, ok, err := p.h.Storage().Get(keyHistory)
	if err != nil || !ok {
		return nil
	}
	var list []historyEntry
	if json.Unmarshal(b, &list) != nil {
		p.h.Logger().Warn("translator: cannot read the history")
		return nil
	}
	cut := p.h.Now().Add(-p.retention())
	return slices.DeleteFunc(list, func(e historyEntry) bool { return e.At.Before(cut) })
}

func (p *Plugin) saveHistory(list []historyEntry) error {
	if len(list) == 0 {
		return p.h.Storage().Delete(keyHistory)
	}
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return p.h.Storage().Set(keyHistory, b)
}

// record adds an entry (an equal one moves to the top).
func (p *Plugin) record(e historyEntry) error {
	p.histMu.Lock()
	defer p.histMu.Unlock()
	e.At = p.h.Now()
	list := slices.DeleteFunc(p.loadHistory(), func(x historyEntry) bool {
		return x.Text == e.Text && x.Translation == e.Translation && x.To == e.To
	})
	list = append([]historyEntry{e}, list...)
	if len(list) > maxHistory {
		list = list[:maxHistory]
	}
	return p.saveHistory(list)
}

// pruneHistory drops the entries older than the retention period, and all
// of them when the history is off.
func (p *Plugin) pruneHistory() {
	p.histMu.Lock()
	defer p.histMu.Unlock()
	if !p.historyOn() {
		if _, ok, _ := p.h.Storage().Get(keyHistory); ok {
			_ = p.h.Storage().Delete(keyHistory)
		}
		return
	}
	_ = p.saveHistory(p.loadHistory())
}

// recording wraps a copy or paste of a translation into cbUse when the
// history is on, so that it is recorded when the user picks it.
func (p *Plugin) recording(a plugin.Action, e historyEntry) plugin.Action {
	if !p.historyOn() {
		return a
	}
	a.Do = plugin.ActionDo{Kind: plugin.ActionCallback, Target: cbUse, Payload: plugin.MustPayload(usePayload{Entry: e, Do: a.Do})}
	return a
}

// historyView lists the recorded translations.
func (p *Plugin) historyView(req plugin.ViewRequest) plugin.View {
	l := p.h.I18n()
	v := plugin.View{Kind: plugin.ViewList, Title: l.T("history.title"), Placeholder: l.T("history.placeholder"), ShowDetail: true,
		FilterLocally: true}
	if !p.historyOn() {
		v.EmptyText = l.T("history.off")
		return v
	}
	p.histMu.Lock()
	list := p.loadHistory()
	p.histMu.Unlock()
	if len(list) == 0 {
		v.EmptyText = l.T("history.empty")
		return v
	}
	now := p.h.Now()
	var items []plugin.Item
	for i, e := range list {
		id := strconv.Itoa(i) + ":" + e.At.UTC().Format(time.RFC3339Nano)
		items = append(items, plugin.Item{
			ID:        id,
			Title:     oneLine(e.Translation, titleRunes),
			Subtitle:  oneLine(e.Text, 60),
			Icon:      plugin.Icon{Name: "history"},
			Keywords:  []string{e.Text},
			Accessory: l.RelativeTime(e.At, now),
			Detail: &plugin.Detail{Markdown: markdownText(e.Translation) + "\n\n---\n\n" + markdownText(e.Text), Metadata: []plugin.Metadata{
				{Label: l.T("detail.engine"), Value: engineName(l, e.Engine)},
				{Label: l.T("detail.from"), Value: langName(l, e.From)},
				{Label: l.T("detail.to"), Value: langName(l, e.To)},
			}},
			Actions: []plugin.Action{
				{ID: "copy", Title: l.T("action.copy"), Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: e.Translation}},
				{ID: "paste", Title: l.T("action.paste"), Do: plugin.ActionDo{Kind: plugin.ActionPaste, Target: e.Translation}},
				{ID: "again", Title: l.T("action.translateAgain"),
					Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewTranslate, Payload: plugin.MustPayload(viewPayload{Text: e.Text, From: e.From, To: e.To})}},
				{ID: "copy-original", Title: l.T("action.copyOriginal"), Shortcut: "Ctrl+Shift+C", Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: e.Text}},
				{ID: "forget", Title: l.T("action.forget"), Shortcut: "Ctrl+X", Style: plugin.StyleDestructive,
					Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: cbForget, Payload: plugin.MustPayload(e)}},
			},
		})
	}
	v.Sections = []plugin.Section{{Items: items}}
	return v
}

// forget removes one entry.
func (p *Plugin) forget(e historyEntry) error {
	p.histMu.Lock()
	defer p.histMu.Unlock()
	list := slices.DeleteFunc(p.loadHistory(), func(x historyEntry) bool {
		return x.Text == e.Text && x.Translation == e.Translation && x.At.Equal(e.At)
	})
	return p.saveHistory(list)
}

// clearHistory removes every entry.
func (p *Plugin) clearHistory() error {
	p.histMu.Lock()
	defer p.histMu.Unlock()
	return p.h.Storage().Delete(keyHistory)
}
