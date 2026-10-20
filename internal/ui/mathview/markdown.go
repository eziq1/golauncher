package mathview

import (
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// Markdown shows Markdown with math as markdown.View does, but for its
// math: display math laid out (Display), and math within text, which
// markdown.View can only show as its TeX, as Unicode text. Copied, the
// math is the TeX it was written as. Keep one in the app's state for the
// views of a kind, as markdown.Options.
type Markdown struct {
	// Options are the views' as markdown.View takes them. Their Math, when
	// nil, is Display in the theme's font, size and text color.
	Options markdown.Options
	// CopyAsMarkdown is the label of the context menu's Copy as Markdown,
	// "Copy as Markdown" when empty.
	CopyAsMarkdown string

	// shared are Options, sharing the selection with the container
	// Markdown puts around the view to copy math as its TeX.
	shared markdown.Options
	// doc is the source last shown, parsed again only where it changes.
	doc *parser.Document
	// in and out are the source last shown and what it showed as.
	in, out string
	repl    []Replaced
}

// Replaced is math within text that Inline replaced.
type Replaced struct {
	// TeX is its source, with its delimiters, as $x^2$; Text is its
	// Unicode text.
	TeX, Text string
}

// Markers around the Unicode text of math within text, invisible, by
// which the copy finds the math it stands for.
const (
	markStart = '⁣' // invisible separator
	markEnd   = '⁤' // invisible plus
)

// View shows src, Markdown, as markdown.View does.
func (m *Markdown) View(c *ui.Context, src string) ui.Element {
	if m.Options.Math == nil {
		m.Options.Math = func(c *ui.Context, tex string) { m.display(c, tex) }
	}
	out, repl := m.inline(src)
	w := ui.Column(c)
	if len(repl) == 0 || m.Options.ShareSelection {
		// markdown.View copies as it does.
		return w.Children(func() { markdown.View(c, out, &m.Options) })
	}
	m.syncShared()
	w.Selectable()
	w.CopyData(func(parts []ui.SelectedText) transfer.Data {
		s := m.copyText(parts)
		return transfer.New(transfer.NewItem(
			transfer.Bytes(transfer.Text, []byte(s)),
			transfer.Bytes(transfer.Markdown, []byte(s)),
		))
	})
	copyMarkdown := func() {
		s := m.in
		if parts := w.SelectedTexts(); len(parts) > 0 {
			s = m.copyText(parts)
		}
		c.Services().WriteClipboardData(transfer.New(transfer.NewItem(
			transfer.Bytes(transfer.Text, []byte(s)),
			transfer.Bytes(transfer.Markdown, []byte(s)),
		)))
	}
	label := m.CopyAsMarkdown
	if label == "" {
		label = "Copy as Markdown"
	}
	w.ContextMenu(func(menu *ui.Menu) {
		menu.EditItems()
		if menu.Item(label).Chosen() {
			copyMarkdown()
		}
	})
	if w.FocusWithin() && w.Shortcut(ui.Cmd|ui.Shift, ui.KeyC) {
		copyMarkdown()
	}
	return w.Children(func() { markdown.View(c, out, &m.shared) })
}

// display builds display math in the Markdown theme's font and colors.
func (m *Markdown) display(c *ui.Context, tex string) {
	th := m.Options.Theme
	def := markdown.DefaultTheme(c.Theme())
	o := Options{Font: def.Font, Size: def.FontSize, Color: def.Text}
	if th != nil {
		if th.Font != "" {
			o.Font = th.Font
		}
		if th.FontSize > 0 {
			o.Size = th.FontSize
		}
		if th.Text != (ui.Color{}) {
			o.Color = th.Text
		}
	}
	// Display math is set a little larger than text, as TeX's is.
	o.Size *= 1.1
	Display(c, tex, &o)
}

// syncShared copies Options to shared, but for sharing the selection. The
// fields are copied one by one: Options keeps the images it loaded.
func (m *Markdown) syncShared() {
	o, s := &m.Options, &m.shared
	s.Theme, s.Extensions = o.Theme, o.Extensions
	s.OnLink, s.LoadImage = o.OnLink, o.LoadImage
	s.CodeBlock, s.Code, s.WrapCode = o.CodeBlock, o.Code, o.WrapCode
	s.Math, s.HTML, s.Block = o.Math, o.HTML, o.Block
	s.Reveal = o.Reveal
	s.ShareSelection = true
}

// inline returns src with its math within text as Unicode text, and the
// math it replaced, from the last frame's when src is the same.
func (m *Markdown) inline(src string) (string, []Replaced) {
	ext := m.Options.Extensions
	if ext == 0 {
		ext = parser.Default
	}
	if m.doc == nil || m.doc.Extensions() != ext {
		m.doc = parser.New(ext)
		m.in, m.out, m.repl = "", "", nil
	} else if src == m.in && m.out != "" {
		return m.out, m.repl
	}
	m.doc.Set(src)
	m.in = src
	m.out, m.repl = Inline(m.doc)
	return m.out, m.repl
}

// Inline returns the source of d with its math within text (parser.Math
// inlines, also those between $$ within a paragraph) as Unicode text,
// escaped for Markdown and between invisible markers, and the math it
// replaced. Math in code spans and escaped dollars are not math to the
// parser and stay as they are.
func Inline(d *parser.Document) (string, []Replaced) {
	src := d.Source()
	var b strings.Builder
	var repl []Replaced
	at := 0
	for i := 1; i < d.Len(); i++ {
		in := d.Inlines(i)
		for k := range in {
			n := &in[k]
			if n.Kind != parser.Math || n.Start < at || n.End > len(src) {
				continue
			}
			text := Unicode(strings.ReplaceAll(n.Text, "\n", " "))
			if text == "" {
				text = strings.TrimSpace(n.Text)
			}
			b.WriteString(src[at:n.Start])
			b.WriteRune(markStart)
			b.WriteString(escape(text))
			b.WriteRune(markEnd)
			repl = append(repl, Replaced{TeX: src[n.Start:n.End], Text: text})
			at = n.End
		}
	}
	if repl == nil {
		return src, nil
	}
	b.WriteString(src[at:])
	return b.String(), repl
}

// escape escapes the ASCII punctuation of s, so that Markdown shows it as
// it is.
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x80 && (r >= '!' && r <= '/' || r >= ':' && r <= '@' || r >= '[' && r <= '`' || r >= '{' && r <= '~') {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// copyText returns the selected text with the math in it as its TeX.
func (m *Markdown) copyText(parts []ui.SelectedText) string {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(Restore(runeSlice(p.Text, p.Start, p.End), m.repl))
	}
	return b.String()
}

// Restore returns text, shown text of a source Inline replaced math in,
// with that math as its TeX: also math whose text text starts or ends in.
func Restore(text string, repl []Replaced) string {
	if !strings.ContainsRune(text, markStart) && !strings.ContainsRune(text, markEnd) {
		return text
	}
	find := func(match func(shown string) bool) string {
		for _, r := range repl {
			if match(r.Text) {
				return r.TeX
			}
		}
		return ""
	}
	var b strings.Builder
	// Text starting within math: its end marker comes first.
	if e := strings.IndexRune(text, markEnd); e >= 0 {
		if s := strings.IndexRune(text, markStart); s < 0 || e < s {
			part := text[:e]
			b.WriteString(find(func(t string) bool { return strings.HasSuffix(t, part) }))
			text = text[e+utf8.RuneLen(markEnd):]
		}
	}
	for {
		s := strings.IndexRune(text, markStart)
		if s < 0 {
			break
		}
		b.WriteString(text[:s])
		text = text[s+utf8.RuneLen(markStart):]
		e := strings.IndexRune(text, markEnd)
		if e < 0 {
			// Text ending within math.
			part := text
			b.WriteString(find(func(t string) bool { return strings.HasPrefix(t, part) }))
			text = ""
			break
		}
		inner := text[:e]
		if tex := find(func(t string) bool { return t == inner }); tex != "" {
			b.WriteString(tex)
		} else {
			b.WriteString(inner)
		}
		text = text[e+utf8.RuneLen(markEnd):]
	}
	b.WriteString(text)
	return b.String()
}

// runeSlice returns runes from to to of s.
func runeSlice(s string, from, to int) string {
	i, n := 0, 0
	a, z := len(s), len(s)
	for i < len(s) {
		if n == from {
			a = i
		}
		if n == to {
			z = i
			break
		}
		_, w := utf8.DecodeRuneInString(s[i:])
		i += w
		n++
	}
	if a > z {
		return ""
	}
	return s[a:z]
}
