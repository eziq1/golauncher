// Package selection is the built-in "Selected Text" plugin
// (docs/selected-text.md §5.1): when text was selected in the window that
// was active when the launcher opened, it shows a preview row at the top of
// the "Selected Text" section, with the application and the word count.
// Enter on it only shows the whole text and its statistics; nothing is sent
// anywhere. The actions on the text (translate, ask AI, search, calculate)
// come from the plugins that own them.
package selection

import (
	"context"
	"embed"
	"encoding/json/v2"
	"fmt"
	"io/fs"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// ID is the plugin's manifest ID.
const ID = "builtin.selection"

const (
	// viewText shows the whole selected text and its statistics.
	viewText = "text"
	// previewRunes is the length of the preview row's title.
	previewRunes = 60
)

//go:embed locales/*.json
var locales embed.FS

// Plugin is the selected text plugin.
type Plugin struct {
	h plugin.Host
}

var (
	_ plugin.Plugin            = (*Plugin)(nil)
	_ plugin.SelectionProvider = (*Plugin)(nil)
	_ plugin.ViewProvider      = (*Plugin)(nil)
	_ plugin.LocaleProvider    = (*Plugin)(nil)
)

// New returns the plugin.
func New() *Plugin { return &Plugin{} }

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:          ID,
		Name:        "%plugin.name%",
		Description: "%plugin.description%",
		Version:     "1.0.0",
		Icon:        plugin.Icon{Name: "text"},
		Permissions: []plugin.Permission{plugin.PermSelection},
	}
}

// Init implements plugin.Plugin.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.h = h
	return nil
}

// Close implements plugin.Plugin.
func (p *Plugin) Close() error { return nil }

// Locales implements plugin.LocaleProvider.
func (p *Plugin) Locales() fs.FS {
	sub, err := fs.Sub(locales, "locales")
	if err != nil {
		panic(err)
	}
	return sub
}

// textPayload is the payload of the "text" view: the selection is cleared
// when the launcher hides, so it carries everything the view shows.
type textPayload struct {
	Text      string `json:"text"`
	App       string `json:"app,omitempty"`
	Source    string `json:"source,omitempty"`
	Truncated bool   `json:"truncated,omitzero"`
	Editable  bool   `json:"editable,omitzero"`
}

// SelectionItems implements plugin.SelectionProvider: the preview row.
func (p *Plugin) SelectionItems(ctx context.Context, q plugin.SelectionQuery) ([]plugin.Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sel := q.Selection
	if strings.TrimSpace(sel.Text) == "" {
		return nil, nil
	}
	pl := textPayload{Text: sel.Text, App: sel.App.Name, Source: sel.Source, Truncated: sel.Truncated, Editable: sel.Editable}
	l := p.h.I18n()
	it := plugin.Item{
		ID:                "preview",
		Title:             previewTitle(sel.Text),
		Subtitle:          p.subtitle(pl),
		SubtitleSensitive: true,
		Icon:              plugin.Icon{Name: "text"},
		Keywords:          splitKeywords(l.T("preview.keywords")),
		Sensitive:         true,
		Transient:         true,
		Actions: []plugin.Action{{
			ID:    "show",
			Title: l.T("action.show"),
			Do:    plugin.ActionDo{Kind: plugin.ActionPush, Target: viewText, Payload: plugin.MustPayload(pl)},
		}},
	}
	if q.Text == "" {
		it.Score = 1
	}
	return []plugin.Item{it}, nil
}

// subtitle is "<app> · <N words>", with "· Truncated" when the text was cut.
func (p *Plugin) subtitle(pl textPayload) string {
	l := p.h.I18n()
	parts := make([]string, 0, 3)
	if app := appName(pl.App); app != "" {
		parts = append(parts, app)
	}
	parts = append(parts, l.N("words", countWords(pl.Text)))
	if pl.Truncated {
		parts = append(parts, l.T("preview.truncated"))
	}
	return strings.Join(parts, " · ")
}

// View implements plugin.ViewProvider: the "text" view shows the whole text
// in the detail pane, with its statistics.
func (p *Plugin) View(ctx context.Context, req plugin.ViewRequest) (plugin.View, error) {
	if req.ID != viewText {
		return plugin.View{}, fmt.Errorf("selection: unknown view %q", req.ID)
	}
	var pl textPayload
	if len(req.Payload) == 0 {
		return plugin.View{}, fmt.Errorf("selection: missing payload")
	}
	if err := json.Unmarshal(req.Payload, &pl); err != nil {
		return plugin.View{}, err
	}
	if err := ctx.Err(); err != nil {
		return plugin.View{}, err
	}
	l := p.h.I18n()
	yesNo := func(b bool) string {
		if b {
			return l.T("detail.yes")
		}
		return l.T("detail.no")
	}
	method := l.T("detail.methodUIA")
	if pl.Source == plugin.SelectionClipboard {
		method = l.T("detail.methodClipboard")
	}
	md := []plugin.Metadata{}
	if app := appName(pl.App); app != "" {
		md = append(md, plugin.Metadata{Label: l.T("detail.app"), Value: app})
	}
	md = append(md,
		plugin.Metadata{Label: l.T("detail.words"), Value: l.Number(float64(countWords(pl.Text)))},
		plugin.Metadata{Label: l.T("detail.characters"), Value: l.Number(float64(utf8.RuneCountInString(pl.Text)))},
		plugin.Metadata{Label: l.T("detail.lines"), Value: l.Number(float64(countLines(pl.Text)))},
		plugin.Metadata{Label: l.T("detail.method"), Value: method},
		plugin.Metadata{Label: l.T("detail.truncated"), Value: yesNo(pl.Truncated)},
		plugin.Metadata{Label: l.T("detail.editable"), Value: yesNo(pl.Editable)},
	)
	item := plugin.Item{
		ID:                "text",
		Title:             previewTitle(pl.Text),
		Subtitle:          p.subtitle(pl),
		SubtitleSensitive: true,
		Icon:              plugin.Icon{Name: "text"},
		Sensitive:         true,
		Detail:            &plugin.Detail{Markdown: codeBlock(pl.Text), Metadata: md},
	}
	return plugin.View{
		Kind:       plugin.ViewList,
		Title:      l.T("view.title"),
		ShowDetail: true,
		Sections:   []plugin.Section{{Items: []plugin.Item{item}}},
	}, nil
}

// previewTitle is the first non-empty line of text with its whitespace
// collapsed, cut to previewRunes characters.
func previewTitle(text string) string {
	var line string
	for l := range strings.Lines(text) {
		if line = strings.Join(strings.Fields(l), " "); line != "" {
			break
		}
	}
	if utf8.RuneCountInString(line) <= previewRunes {
		return line
	}
	r := []rune(line)[:previewRunes]
	return strings.TrimRightFunc(string(r), unicode.IsSpace) + "…"
}

// appName is the application name without ".exe".
func appName(name string) string {
	if len(name) > 4 && strings.EqualFold(name[len(name)-4:], ".exe") {
		return name[:len(name)-4]
	}
	return name
}

// isCJK reports whether r is a character counted as one word on its own:
// Han, Hiragana, Katakana or Hangul.
func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// countWords counts the words of text: each Han, Kana or Hangul character is
// a word, and so is each other run of letters, digits and marks, separated
// by spaces, punctuation or symbols. An apostrophe inside a word ("don't")
// does not split it.
func countWords(text string) int {
	n := 0
	inWord := false
	for _, r := range text {
		switch {
		case isCJK(r):
			n++
			inWord = false
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r):
			if !inWord {
				n++
				inWord = true
			}
		case inWord && (r == '\'' || r == '’'):
			// Still the same word.
		default:
			inWord = false
		}
	}
	return n
}

// countLines counts the lines of text; a final line break does not start
// another line.
func countLines(text string) int {
	if text == "" {
		return 0
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.Count(strings.TrimSuffix(text, "\n"), "\n") + 1
}

// codeBlock puts text in a fenced Markdown code block whose fence is longer
// than any run of backticks in the text.
func codeBlock(text string) string {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	return fence + "\n" + text + "\n" + fence
}

// splitKeywords splits a comma-separated list of keywords.
func splitKeywords(s string) []string {
	var out []string
	for k := range strings.SplitSeq(s, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}
