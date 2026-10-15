package calc

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Colors (docs/unit-conversion.md §5.2): "#ff8800 to rgb",
// "rgb(255,136,0) in hsl", "hsl(32,100%,50%) to hex", or a color alone for
// all three formats. The detail pane shows a swatch: a small PNG written
// once per color to CacheDir/swatches.

// specialSplit matches the first conversion keyword of a special
// conversion (colors, timestamps, sizes).
var specialSplit = regexp.MustCompile(`(?i)\s+(?:in|to|as)\s+|\s*(?:=>|→|转换为|转换成|换算成|换算为|换成|转成|转为)\s*`)

// splitSpecial splits text at its first conversion keyword.
func splitSpecial(text string) (src, target string, ok bool) {
	loc := specialSplit.FindStringIndex(text)
	if loc == nil {
		return strings.TrimSpace(text), "", false
	}
	return strings.TrimSpace(text[:loc[0]]), strings.TrimSpace(text[loc[1]:]), true
}

var (
	hexColorRe  = regexp.MustCompile(`^#?([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)
	funcColorRe = regexp.MustCompile(`(?i)^(rgba?|hsla?)\s*\(([^)]*)\)$`)
)

// rgb is a color with 8-bit channels.
type rgb struct{ r, g, b uint8 }

// colorFormats are the conversion targets, in display order.
var colorFormats = []string{"hex", "rgb", "hsl"}

// parseColor parses a color: "#f80", "#ff8800" ("ff8800" when bareHex),
// "rgb(255, 136, 0)", "rgb(100% 50% 0%)", "hsl(32, 100%, 50%)". It returns
// the color and its format.
func parseColor(s string, bareHex bool) (rgb, string, bool) {
	if m := hexColorRe.FindStringSubmatch(s); m != nil && (bareHex || strings.HasPrefix(s, "#")) {
		h := m[1]
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		n, _ := strconv.ParseUint(h, 16, 32)
		return rgb{uint8(n >> 16), uint8(n >> 8), uint8(n)}, "hex", true
	}
	m := funcColorRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return rgb{}, "", false
	}
	args := strings.FieldsFunc(m[2], func(r rune) bool { return r == ',' || r == ' ' || r == '/' })
	if len(args) != 3 && len(args) != 4 { // the alpha is ignored
		return rgb{}, "", false
	}
	if strings.HasPrefix(strings.ToLower(m[1]), "rgb") {
		var c [3]uint8
		for i := range 3 {
			v, ok := channel(args[i])
			if !ok {
				return rgb{}, "", false
			}
			c[i] = v
		}
		return rgb{c[0], c[1], c[2]}, "rgb", true
	}
	h, err1 := strconv.ParseFloat(strings.TrimSuffix(strings.ToLower(args[0]), "deg"), 64)
	sat, ok2 := percent(args[1])
	lig, ok3 := percent(args[2])
	if err1 != nil || !ok2 || !ok3 {
		return rgb{}, "", false
	}
	return hslToRGB(h, sat, lig), "hsl", true
}

// channel parses an RGB channel: 0–255 or a percentage.
func channel(s string) (uint8, bool) {
	if strings.HasSuffix(s, "%") {
		p, ok := percent(s)
		return uint8(math.Round(p * 255)), ok
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 || v > 255 {
		return 0, false
	}
	return uint8(math.Round(v)), true
}

// percent parses "50%" (or "50") as 0.5.
func percent(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil || v < 0 || v > 100 {
		return 0, false
	}
	return v / 100, true
}

// hslToRGB converts hue (degrees), saturation and lightness (0–1).
func hslToRGB(h, s, l float64) rgb {
	h = math.Mod(math.Mod(h, 360)+360, 360)
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g = c, x
	case h < 120:
		r, g = x, c
	case h < 180:
		g, b = c, x
	case h < 240:
		g, b = x, c
	case h < 300:
		r, b = x, c
	default:
		r, b = c, x
	}
	conv := func(v float64) uint8 { return uint8(math.Round((v + m) * 255)) }
	return rgb{conv(r), conv(g), conv(b)}
}

// hsl returns the hue (degrees), saturation and lightness (percent),
// rounded to integers.
func (c rgb) hsl() (int, int, int) {
	r, g, b := float64(c.r)/255, float64(c.g)/255, float64(c.b)/255
	mx, mn := max(r, g, b), min(r, g, b)
	l := (mx + mn) / 2
	d := mx - mn
	var h, s float64
	if d != 0 {
		s = d / (1 - math.Abs(2*l-1))
		switch mx {
		case r:
			h = 60 * math.Mod((g-b)/d, 6)
		case g:
			h = 60 * ((b-r)/d + 2)
		default:
			h = 60 * ((r-g)/d + 4)
		}
		if h < 0 {
			h += 360
		}
	}
	hi := int(math.Round(h)) % 360
	return hi, int(math.Round(s * 100)), int(math.Round(l * 100))
}

// format spells c in a format.
func (c rgb) format(f string) string {
	switch f {
	case "rgb":
		return fmt.Sprintf("rgb(%d, %d, %d)", c.r, c.g, c.b)
	case "hsl":
		h, s, l := c.hsl()
		return fmt.Sprintf("hsl(%d, %d%%, %d%%)", h, s, l)
	}
	return fmt.Sprintf("#%02X%02X%02X", c.r, c.g, c.b)
}

// colorResult converts a color, or returns nil if text is not one.
func (p *Plugin) colorResult(text string) *result {
	if !strings.HasPrefix(text, "#") && !strings.ContainsRune(text, '(') && !specialSplit.MatchString(text) {
		return nil // fast path for ordinary searches
	}
	src, target, conv := splitSpecial(text)
	target = strings.ToLower(target)
	if conv && target != "hex" && target != "rgb" && target != "hsl" {
		return nil
	}
	// A bare "ff8800" is a color only when converted to rgb or hsl, so
	// that "255 in hex" stays a number.
	c, from, ok := parseColor(src, conv && target != "hex" && len(src) == 6)
	if !ok {
		return nil
	}
	l := p.h.I18n()
	labels := map[string]string{"hex": l.T("color.hex"), "rgb": l.T("color.rgb"), "hsl": l.T("color.hsl")}
	r := &result{source: c.format(from), image: p.swatch(c)}
	order := []string{target}
	if !conv {
		order = nil
		for _, f := range colorFormats {
			if f != from {
				order = append(order, f)
			}
		}
	}
	for _, f := range append(order, colorFormats...) {
		if slices.ContainsFunc(r.rows, func(x row) bool { return x.id == f }) {
			continue
		}
		s := c.format(f)
		r.rows = append(r.rows, row{id: f, title: s, raw: s, full: s, label: labels[f], shown: s})
	}
	r.root = len(order)
	return r
}

// swatch returns a PNG of the color in CacheDir/swatches, writing it if
// needed, or "" if that fails.
func (p *Plugin) swatch(c rgb) string {
	dir := filepath.Join(p.h.CacheDir(), "swatches")
	path := filepath.Join(dir, fmt.Sprintf("%02x%02x%02x.png", c.r, c.g, c.b))
	if _, err := os.Stat(path); err == nil {
		return path
	} else if !errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	img := image.NewRGBA(image.Rect(0, 0, 160, 96))
	fill := color.RGBA{c.r, c.g, c.b, 255}
	for y := range 96 {
		for x := range 160 {
			img.SetRGBA(x, y, fill)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	tmp, err := os.CreateTemp(dir, "swatch-*.tmp")
	if err != nil {
		return ""
	}
	err = png.Encode(tmp, img)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
		p.h.Logger().Warn("calc: writing color swatch", "err", err)
		return ""
	}
	return path
}
