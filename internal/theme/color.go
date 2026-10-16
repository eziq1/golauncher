package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	mui "github.com/egoist/mygo/ui"
)

// ParseColor parses a theme color: "#rgb", "#rgba", "#rrggbb",
// "#rrggbbaa", "rgb(r, g, b)" or "rgba(r, g, b, a)", where r, g and b are
// 0–255 (or percentages) and a is 0–1 (or a percentage). "transparent" is
// the color of nothing.
func ParseColor(s string) (mui.Color, error) {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	switch {
	case lower == "transparent":
		return mui.Transparent, nil
	case strings.HasPrefix(lower, "#"):
		return parseHex(lower)
	case strings.HasPrefix(lower, "rgba(") || strings.HasPrefix(lower, "rgb("):
		return parseFunc(lower)
	}
	return mui.Color{}, fmt.Errorf("invalid color %q: want #rrggbb, #rrggbbaa or rgba(…)", s)
}

func parseHex(s string) (mui.Color, error) {
	h := s[1:]
	switch len(h) {
	case 3, 4, 6, 8:
	default:
		return mui.Color{}, fmt.Errorf("invalid color %q: want #rgb, #rgba, #rrggbb or #rrggbbaa", s)
	}
	for _, r := range h {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return mui.Color{}, fmt.Errorf("invalid color %q: %q is not a hexadecimal digit", s, r)
		}
	}
	return mui.Hex(s), nil
}

func parseFunc(s string) (mui.Color, error) {
	open, close := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if close != len(s)-1 {
		return mui.Color{}, fmt.Errorf("invalid color %q: missing )", s)
	}
	parts := strings.FieldsFunc(s[open+1:close], func(r rune) bool { return r == ',' || r == ' ' || r == '/' })
	if len(parts) != 3 && len(parts) != 4 {
		return mui.Color{}, fmt.Errorf("invalid color %q: want 3 or 4 numbers", s)
	}
	var rgb [3]uint8
	for i := range 3 {
		v, pct, err := number(parts[i])
		if err != nil {
			return mui.Color{}, fmt.Errorf("invalid color %q: %w", s, err)
		}
		if pct {
			v = v / 100 * 255
		}
		if v < 0 || v > 255 {
			return mui.Color{}, fmt.Errorf("invalid color %q: %s is not within 0–255", s, parts[i])
		}
		rgb[i] = uint8(math.Round(v))
	}
	alpha := 1.0
	if len(parts) == 4 {
		v, pct, err := number(parts[3])
		if err != nil {
			return mui.Color{}, fmt.Errorf("invalid color %q: %w", s, err)
		}
		if pct {
			v /= 100
		}
		if v < 0 || v > 1 {
			return mui.Color{}, fmt.Errorf("invalid color %q: alpha %s is not within 0–1", s, parts[3])
		}
		alpha = v
	}
	return mui.RGBA(rgb[0], rgb[1], rgb[2], float32(alpha)), nil
}

// number parses a number, maybe followed by %.
func number(s string) (v float64, percent bool, err error) {
	s, percent = strings.CutSuffix(s, "%")
	v, err = strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false, fmt.Errorf("%q is not a number", s)
	}
	return v, percent, nil
}

// FormatColor writes c as "#rrggbb", or "#rrggbbaa" when it is not opaque.
func FormatColor(c mui.Color) string {
	if c.A == 255 {
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	}
	return fmt.Sprintf("#%02x%02x%02x%02x", c.R, c.G, c.B, c.A)
}

// luminance is the relative luminance of the opaque color c (WCAG 2.x).
func luminance(c mui.Color) float64 {
	ch := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
}

// ContrastRatio is the WCAG contrast ratio of two opaque colors, 1 to 21.
func ContrastRatio(a, b mui.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// over composites c over the opaque color base.
func over(c, base mui.Color) mui.Color {
	a := float64(c.A) / 255
	mix := func(f, b uint8) uint8 { return uint8(math.Round(float64(f)*a + float64(b)*(1-a))) }
	return mui.RGB(mix(c.R, base.R), mix(c.G, base.G), mix(c.B, base.B))
}

var (
	white = mui.RGB(255, 255, 255)
	black = mui.RGB(0, 0, 0)
)

// gray is the luminance of c in 0–255, as MyGo measures it to choose the
// text on an accent color.
func gray(c mui.Color) uint8 {
	return uint8(0.2126*float32(c.R) + 0.7152*float32(c.G) + 0.0722*float32(c.B) + 0.5)
}
