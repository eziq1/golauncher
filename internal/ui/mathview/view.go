package mathview

import (
	"sync"

	"github.com/egoist/mygo/ui"
)

// Options are how Display shows math.
type Options struct {
	// Font is the family of the math's text, the system's when empty, and
	// Size its size in DIPs, 14 when 0.
	Font string
	Size float32
	// Color colors it: the theme's text color when zero.
	Color ui.Color
	// Measurer measures its text: MyGo's text system when nil.
	Measurer Measurer
}

// Formula is a formula ready to show: laid out, or, where it cannot be,
// as Unicode text.
type Formula struct {
	// TeX is its source.
	TeX string
	// Box is its layout, nil when it shows as Text.
	Box *Box
	// Text is it as Unicode text, which shows when Box is nil and names it
	// to assistive technology.
	Text string
	// Err is why it is not laid out.
	Err error
}

// Prepare lays out tex in display style, or, where treeblood fails or the
// MathML has an element the layout does not support, makes it Unicode
// text, the whole of it.
func Prepare(tex string, s Style, m Measurer) *Formula {
	f := &Formula{TeX: tex}
	n, err := Parse(tex)
	if err == nil {
		err = Check(n)
	}
	if err != nil {
		f.Err = err
		if n != nil {
			f.Text = TreeUnicode(n)
		} else {
			f.Text = TeXUnicode(tex)
		}
		return f
	}
	f.Text = TreeUnicode(n)
	if m == nil {
		m = shaper{}
	}
	f.Box = Layout(n, s, m)
	return f
}

type formulaKey struct {
	tex  string
	font string
	size float32
}

var formulas = struct {
	sync.Mutex
	m map[formulaKey]*Formula
}{m: map[formulaKey]*Formula{}}

// prepared returns the formula of tex, laid out once for each font.
func prepared(tex string, s Style, m Measurer) *Formula {
	if m != nil {
		return Prepare(tex, s, m)
	}
	k := formulaKey{tex, s.Font, s.Size}
	formulas.Lock()
	f, ok := formulas.m[k]
	formulas.Unlock()
	if ok {
		return f
	}
	f = Prepare(tex, s, nil)
	formulas.Lock()
	if len(formulas.m) >= cacheSize {
		clear(formulas.m)
	}
	formulas.m[k] = f
	formulas.Unlock()
	return f
}

// Display shows tex, display math without its delimiters, centered on
// lines of its own: laid out, or, where it cannot be, as Unicode text. A
// formula wider than the view scrolls sideways. Selected with the text
// around it, it copies as its TeX between $$.
//
//	md.Math = func(c *ui.Context, tex string) { mathview.Display(c, tex, nil) }
func Display(c *ui.Context, tex string, o *Options) ui.Element {
	var opt Options
	if o != nil {
		opt = *o
	}
	if opt.Size <= 0 {
		opt.Size = c.Theme().FontSize
		if opt.Size <= 0 {
			opt.Size = 14
		}
	}
	if opt.Font == "" {
		opt.Font = c.Theme().Font
	}
	color := opt.Color
	if color == (ui.Color{}) {
		color = c.Theme().Text
	}
	f := prepared(tex, Style{Font: opt.Font, Size: opt.Size, Display: true}, opt.Measurer)
	body := func() {
		if f.Box == nil {
			ui.Box(c).Margin(0, ui.Auto).Children(func() {
				el := ui.Text(c, f.Text).FontSize(opt.Size).TextColor(color).NoWrap().Unselectable()
				if opt.Font != "" {
					el.Font(opt.Font)
				}
				source(c, tex)
			})
			return
		}
		b := f.Box
		pad := opt.Size * 0.2
		h := b.Ascent + b.Descent + 2*pad
		box := ui.Box(c).Size(b.Width, h).Shrink(0).Margin(0, ui.Auto).Label(f.Text)
		box.Draw(func(p *ui.Painter, r ui.Rect) { b.Paint(p, r.X, r.Y+pad+b.Ascent, color) })
		box.Children(func() { source(c, tex) })
	}
	// Centered, or scrolling sideways when wider than the view.
	outer := ui.Column(c).AlignSelf(ui.Stretch).AlignItems(ui.Center)
	return outer.Children(func() { ui.ScrollHorizontal(c).MaxWidthPercent(100).Children(body) })
}

// source is the TeX a formula copies as, there to be selected with the
// text around it but not seen.
func source(c *ui.Context, tex string) {
	ui.Text(c, "$$"+tex+"$$").Absolute().Left(0).Top(0).FontSize(1).MaxWidth(1).Opacity(0)
}

// Paint paints the box with its baseline's left end at (x, y), in color
// where its parts have none of their own.
func (a *Box) Paint(p *ui.Painter, x, y float32, color ui.Color) {
	for i := range a.items {
		it := &a.items[i]
		c := it.color
		if c == (ui.Color{}) {
			c = color
		}
		switch it.kind {
		case itemText:
			g := it.glyphs
			if g == nil {
				g = ui.Shape(it.text, it.font)
				it.glyphs = g
			}
			p.Glyphs(g, x+it.x, y+it.y, c)
		case itemRect:
			p.Fill(ui.Rect{X: x + it.x, Y: y + it.y, W: it.w, H: it.h}, c, 0)
		case itemStroke, itemFill:
			path := &ui.Path{}
			for _, s := range it.path {
				switch s.op {
				case 'M':
					path.MoveTo(x+s.pts[0][0], y+s.pts[0][1])
				case 'L':
					path.LineTo(x+s.pts[0][0], y+s.pts[0][1])
				case 'Q':
					path.QuadTo(x+s.pts[0][0], y+s.pts[0][1], x+s.pts[1][0], y+s.pts[1][1])
				case 'C':
					path.CubeTo(x+s.pts[0][0], y+s.pts[0][1], x+s.pts[1][0], y+s.pts[1][1], x+s.pts[2][0], y+s.pts[2][1])
				case 'Z':
					path.Close()
				}
			}
			if it.kind == itemFill {
				p.FillPath(path, c)
			} else {
				p.StrokePath(path, it.width, c)
			}
		}
	}
}
