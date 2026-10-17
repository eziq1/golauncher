// Package assets holds the golauncher icon: the designer's files (icon.svg,
// icon-small.svg for small sizes, icon.ico and icon-<size>.png). The same
// images are embedded as Windows resources in the executables (cmd/*/winres,
// regenerated with scripts/winres.ps1); scripts/build-macos.sh --app turns
// the PNGs into the app bundle's golauncher.icns. icon-menubar.png is drawn
// by scripts/genmenubaricon.
package assets

import _ "embed"

// Icon32 is the 32×32 PNG icon, used for the tray.
//
//go:embed icon-32.png
var Icon32 []byte

// MenuBarIcon is the macOS menu bar icon: the arc and arrow of
// icon-small.svg in black on transparent, 44×36 pixels (22×18 points), a
// template image that macOS tints to match the menu bar.
//
//go:embed icon-menubar.png
var MenuBarIcon []byte
