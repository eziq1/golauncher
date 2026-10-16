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

// MaterialTint is the material's own layer as the contrast checks assume
// it, for a light or dark window.
func MaterialTint(dark bool) mui.Color {
	if dark {
		return materialDark.Alpha(materialLayerOpacity)
	}
	return materialLight.Alpha(materialLayerOpacity)
}

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
	layer := MaterialTint(dark)
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

// textOnAccent is the text derived for an accent: white, or near black where
// white would not stand out, as on yellow (MyGo's rule), or where only near
// black reaches MinTextContrast, as on cyan.
func textOnAccent(a mui.Color) mui.Color {
	a = opaque(a)
	if gray(a) > 165 || ContrastRatio(white, a) < MinTextContrast && ContrastRatio(darkAccentText, a) >= MinTextContrast {
		return darkAccentText
	}
	return white
}

var darkAccentText = mui.RGB(24, 24, 27)

// rowSelectedAlpha is the opacity of the selected row derived from the
// accent.
const rowSelectedAlpha = 0.9

// Steps of readableAccent: it moves the accent toward black or white by
// 1/accentSteps at a time, and darkens it under white text by up to
// accentDarkenFirst steps before it considers dark text instead.
const (
	accentSteps       = 50
	accentDarkenFirst = 15
)

// readableAccent returns the Windows accent a as a theme with background bg
// uses it: unchanged when the text on it (textOnAccent) and on the selected
// row (the accent at rowSelectedAlpha) reach min; else the accent darkened
// under white text, in small steps, just until they do. Where that takes
// more than 30% of the way to black and dark text needs less (a light
// cyan or green), the accent is kept, lightened if need be, under dark
// text. The selected row is checked over every backdrop the background can
// have (any opacity, over the material over white or black, docs/themes.md
// §13.2), so the accent stays readable whatever the system's accent and the
// background opacity.
func readableAccent(a, bg mui.Color, dark bool, min float64) mui.Color {
	a = opaque(a)
	bgs, _ := backdrops(bg, dark, MaterialAcrylic, false, 0)
	bgs = append(bgs, opaque(bg))
	// steps is how far toward the color the accent must go for text on it.
	steps := func(toward, text mui.Color) (mui.Color, int) {
		for i := range accentSteps + 1 {
			c := a.Mix(toward, float32(i)/accentSteps)
			if accentReadable(c, text, bgs, min) {
				return c, i
			}
		}
		return toward, accentSteps + 1
	}
	if gray(a) > 165 { // dark text already (MyGo's rule)
		c, _ := steps(white, darkAccentText)
		return c
	}
	cw, nw := steps(black, white)
	if nw <= accentDarkenFirst {
		return cw
	}
	if cd, nd := steps(white, darkAccentText); nd < nw {
		return cd
	}
	return cw
}

// accentReadable reports whether text reaches min on accent c and on the
// selected row over each of bgs, and is the text derived for c.
func accentReadable(c, text mui.Color, bgs []mui.Color, min float64) bool {
	if textOnAccent(c) != text || ContrastRatio(text, c) < min {
		return false
	}
	for _, bg := range bgs {
		if ContrastRatio(text, over(c.Alpha(rowSelectedAlpha), bg)) < min {
			return false
		}
	}
	return true
}

// CheckContrast checks theme t as it comes, at its own opacity, with
// nothing adjusted.
func CheckContrast(t *Theme) []ContrastResult {
	c := t.Colors()
	return checkContrast(c, t.Dark, t.Material, t.HighContrast, t.Opacity, t.HighContrast)
}
