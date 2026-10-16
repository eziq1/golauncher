package theme

import (
	"math"

	mui "github.com/egoist/mygo/ui"
)

// Contrast minimums (WCAG 2.x): body text 4.5:1 (AA), 7:1 (AAA) in a high
// contrast theme; secondary text 3:1, 4.5:1 in a high contrast theme.
const (
	MinTextContrast      = 4.5
	MinTextContrastHigh  = 7
	MinMutedContrast     = 3
	MinMutedContrastHigh = 4.5
	materialLayerOpacity = 0.8
)

// The material's own tint, as Windows draws its Acrylic and Mica
// backdrops: a layer of about this color over what is behind the window,
// before the theme's background. Windows's own layer is at least as opaque
// as materialLayerOpacity (its luminosity opacity), so the worst case is
// computed with it.
var (
	materialLight = mui.Hex("#f3f3f3")
	materialDark  = mui.Hex("#202020")
)

// ContrastResult is the contrast of a text color over its background.
type ContrastResult struct {
	// Pair is "text/background" and so on: the text color, then what it
	// is on.
	Pair string `json:"pair"`
	// Ratio is the lowest ratio: with a translucent background, over pure
	// white and pure black behind the window, whichever is worse.
	Ratio float64 `json:"ratio"`
	Min   float64 `json:"min"`
	OK    bool    `json:"ok"`
	// Over is what was behind the window for Ratio: "white", "black", or
	// "" for an opaque background.
	Over string `json:"over,omitempty"`
}

// backdrops returns the background bg as it shows: opaque, or at opacity
// over the material over white and over black.
func backdrops(bg mui.Color, dark bool, material Material, isOpaque bool, opacity float64) ([]mui.Color, []string) {
	if isOpaque || opacity >= 1 || material == MaterialNone {
		return []mui.Color{opaque(bg)}, []string{""}
	}
	layer := materialLight
	if dark {
		layer = materialDark
	}
	layer = layer.Alpha(materialLayerOpacity)
	var out []mui.Color
	for _, behind := range []mui.Color{white, black} {
		out = append(out, over(bg.Alpha(float32(opacity)), over(layer, behind)))
	}
	return out, []string{"white", "black"}
}

// checkContrast checks the text colors of c over their backgrounds.
func checkContrast(c Colors, dark bool, material Material, isOpaque bool, opacity float64, high bool) []ContrastResult {
	minText, minMuted := float64(MinTextContrast), float64(MinMutedContrast)
	if high {
		minText, minMuted = MinTextContrastHigh, MinMutedContrastHigh
	}
	bgs, overs := backdrops(c["background"], dark, material, isOpaque, opacity)
	check := func(pair, fg string, fill string, min float64) ContrastResult {
		res := ContrastResult{Pair: pair, Ratio: math.Inf(1), Min: min}
		for i, bg := range bgs {
			if fill != "" {
				bg = over(c[fill], bg)
			}
			if r := ContrastRatio(over(c[fg], bg), bg); r < res.Ratio {
				res.Ratio, res.Over = r, overs[i]
			}
		}
		res.OK = res.Ratio >= min
		res.Ratio = math.Floor(res.Ratio*100) / 100 // never shown higher than it is
		return res
	}
	return []ContrastResult{
		check("text/background", "text", "", minText),
		check("searchText/background", "searchText", "", minText),
		check("textMuted/background", "textMuted", "", minMuted),
		check("accentText/accent", "accentText", "accent", minText),
		check("rowSelectedText/rowSelected", "rowSelectedText", "rowSelected", minText),
	}
}

// CheckContrast checks theme t as it comes, at its own opacity, with
// nothing adjusted.
func CheckContrast(t *Theme) []ContrastResult {
	c := t.Colors()
	return checkContrast(c, t.Dark, t.Material, t.HighContrast, t.Opacity, t.HighContrast)
}
