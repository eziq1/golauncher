package ui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// iconPaths are the bodies of the built-in icons, lucide-style strokes on a
// 24×24 grid, by plugin.Icon.Name.
var iconPaths = map[string]string{
	"app":          `<rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/>`,
	"calculator":   `<rect x="4" y="2" width="16" height="20" rx="2"/><path d="M8 6h8"/><path d="M16 10h.01M12 10h.01M8 10h.01M12 14h.01M8 14h.01M12 18h.01M8 18h.01M16 14v4"/>`,
	"clock":        `<circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/>`,
	"clipboard":    `<rect x="8" y="2" width="8" height="4" rx="1"/><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2"/>`,
	"text":         `<path d="M17 6H3M21 12H3M15 18H3"/>`,
	"image":        `<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21"/>`,
	"file":         `<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/>`,
	"package":      `<path d="M11 21.73a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73z"/><path d="M12 22V12"/><path d="m3.3 7 8.7 5 8.7-5"/><path d="m7.5 4.27 9 5.15"/>`,
	"folder":       `<path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>`,
	"command":      `<path d="M15 6v12a3 3 0 1 0 3-3H6a3 3 0 1 0 3 3V6a3 3 0 1 0-3 3h12a3 3 0 1 0-3-3"/>`,
	"settings":     `<path d="M20 7h-9M14 17H5"/><circle cx="17" cy="17" r="3"/><circle cx="7" cy="7" r="3"/>`,
	"search":       `<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>`,
	"pin":          `<path d="M12 17v5"/><path d="M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z"/>`,
	"trash":        `<path d="M3 6h18M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/>`,
	"activity":     `<path d="M22 12h-2.48a2 2 0 0 0-1.93 1.46l-2.35 8.36a.25.25 0 0 1-.48 0L9.24 2.18a.25.25 0 0 0-.48 0l-2.35 8.36A2 2 0 0 1 4.49 12H2"/>`,
	"chevron-left": `<path d="m15 18-6-6 6-6"/>`,
	"info":         `<circle cx="12" cy="12" r="10"/><path d="M12 16v-4M12 8h.01"/>`,
	"wrench":       `<path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/>`,
	"check":        `<path d="M20 6 9 17l-5-5"/>`,
	"x":            `<path d="M18 6 6 18M6 6l12 12"/>`,
	"alert":        `<circle cx="12" cy="12" r="10"/><path d="M12 8v4M12 16h.01"/>`,
	// Context actions (builtin.files).
	"terminal":    `<path d="m4 17 6-6-6-6"/><path d="M12 19h8"/>`,
	"code":        `<path d="m16 18 6-6-6-6"/><path d="m8 6-6 6 6 6"/>`,
	"folder-plus": `<path d="M12 10v6M9 13h6"/><path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>`,
	"file-plus":   `<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M9 15h6M12 18v-6"/>`,
	"archive":     `<rect x="2" y="3" width="20" height="5" rx="1"/><path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8"/><path d="M10 12h4"/>`,
	"hash":        `<path d="M4 9h16M4 15h16M10 3 8 21M16 3l-2 18"/>`,
	"shield":      `<path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/>`,
	// System commands (builtin.system).
	"lock":        `<rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>`,
	"moon":        `<path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"/>`,
	"snowflake":   `<path d="M2 12h20M12 2v20m8-10-3-3m3 3-3 3M4 12l3-3m-3 3 3 3m5-7-3-3m3 3 3-3m-3 11-3 3m3-3 3 3"/>`,
	"rotate-cw":   `<path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/>`,
	"power":       `<path d="M12 2v10"/><path d="M18.4 6.6a9 9 0 1 1-12.77.04"/>`,
	"log-out":     `<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><path d="m16 17 5-5-5-5M21 12H9"/>`,
	"contrast":    `<circle cx="12" cy="12" r="10"/><path d="M12 18a6 6 0 0 0 0-12v12z"/>`,
	"volume-x":    `<path d="M11 5 6 9H2v6h4l5 4z"/><path d="m22 9-6 6M16 9l6 6"/>`,
	"volume-1":    `<path d="M11 5 6 9H2v6h4l5 4z"/><path d="M15.5 8.5a5 5 0 0 1 0 7"/>`,
	"volume-2":    `<path d="M11 5 6 9H2v6h4l5 4z"/><path d="M15.5 8.5a5 5 0 0 1 0 7M19 5a10 10 0 0 1 0 14"/>`,
	"monitor":     `<rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8M12 17v4"/>`,
	"monitor-off": `<path d="M17 17H4a2 2 0 0 1-2-2V5c0-1.5 1-2 1-2M22 15V5a2 2 0 0 0-2-2H9M8 21h8M12 17v4M2 2l20 20"/>`,
	// AI chat (builtin.ai).
	"sparkles":       `<path d="M9.937 15.5A2 2 0 0 0 8.5 14.063l-6.135-1.582a.5.5 0 0 1 0-.962L8.5 9.936A2 2 0 0 0 9.937 8.5l1.582-6.135a.5.5 0 0 1 .963 0L14.063 8.5A2 2 0 0 0 15.5 9.937l6.135 1.581a.5.5 0 0 1 0 .964L15.5 14.063a2 2 0 0 0-1.437 1.437l-1.582 6.135a.5.5 0 0 1-.963 0z"/><path d="M20 3v4M22 5h-4M4 17v2M5 18H3"/>`,
	"history":        `<path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/><path d="M12 7v5l4 2"/>`,
	"message-square": `<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>`,
	// Terminal & WSL (builtin.terminal).
	"square-terminal":   `<path d="m7 11 2-2-2-2"/><path d="M11 13h4"/><rect x="3" y="3" width="18" height="18" rx="2"/>`,
	"circle-stop":       `<circle cx="12" cy="12" r="10"/><rect x="9" y="9" width="6" height="6" rx="1"/>`,
	"circle-arrow-down": `<circle cx="12" cy="12" r="10"/><path d="M12 8v8"/><path d="m8 12 4 4 4-4"/>`,
	// Network tools (builtin.network).
	"network": `<rect x="16" y="16" width="6" height="6" rx="1"/><rect x="2" y="16" width="6" height="6" rx="1"/><rect x="9" y="2" width="6" height="6" rx="1"/><path d="M5 16v-3a1 1 0 0 1 1-1h12a1 1 0 0 1 1 1v3"/><path d="M12 12V8"/>`,
	"wifi":    `<path d="M12 20h.01"/><path d="M2 8.82a15 15 0 0 1 20 0"/><path d="M5 12.859a10 10 0 0 1 14 0"/><path d="M8.5 16.429a5 5 0 0 1 7 0"/>`,
	"globe":   `<circle cx="12" cy="12" r="10"/><path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20"/><path d="M2 12h20"/>`,
	// Timers (builtin.timer).
	"timer":       `<path d="M10 2h4"/><path d="m12 14 3-3"/><circle cx="12" cy="14" r="8"/>`,
	"alarm-clock": `<circle cx="12" cy="13" r="8"/><path d="M12 9v4l2 2"/><path d="M5 3 2 6"/><path d="m22 6-3-3"/><path d="M6.38 18.7 4 21"/><path d="M17.64 18.67 20 21"/>`,
	"pause":       `<rect x="14" y="4" width="4" height="16" rx="1"/><rect x="6" y="4" width="4" height="16" rx="1"/>`,
	// Translator (builtin.translator).
	"languages": `<path d="m5 8 6 6"/><path d="m4 14 6-6 2-3"/><path d="M2 5h12"/><path d="M7 2h1"/><path d="m22 22-5-10-5 10"/><path d="M14 18h6"/>`,
	// Action Ring.
	"scissors":   `<circle cx="6" cy="6" r="3"/><path d="M8.12 8.12 12 12"/><path d="M20 4 8.12 15.88"/><circle cx="6" cy="18" r="3"/><path d="M14.8 14.8 20 20"/>`,
	"circle-dot": `<circle cx="12" cy="12" r="10"/><circle cx="12" cy="12" r="1"/>`,
}

// icons are the parsed built-in icons.
var icons = func() map[string]*mui.SVG {
	m := make(map[string]*mui.SVG, len(iconPaths))
	for name, body := range iconPaths {
		m[name] = mui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`))
	}
	return m
}()

// builtinIcon returns the built-in icon called name, or the generic app icon.
func builtinIcon(name string) *mui.SVG {
	if s, ok := icons[name]; ok {
		return s
	}
	return icons["app"]
}

// maxCachedImages bounds the image cache; it is emptied when it grows past.
const maxCachedImages = 2000

// imageCache loads image files (PNG, JPEG, SVG, …) on goroutines of their
// own and keeps them by path. It is safe for concurrent use.
type imageCache struct {
	mu     sync.Mutex
	m      map[string]*imageEntry
	loaded func() // called after an image finished loading
}

type imageEntry struct {
	src  mui.ImageSource // nil while loading, or when the file is no image
	done bool
}

func newImageCache(loaded func()) *imageCache {
	return &imageCache{m: map[string]*imageEntry{}, loaded: loaded}
}

// get returns the image at path, or nil while it loads or if it cannot be read.
func (ic *imageCache) get(path string) mui.ImageSource {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if e, ok := ic.m[path]; ok {
		return e.src
	}
	if len(ic.m) >= maxCachedImages {
		clear(ic.m)
	}
	e := &imageEntry{}
	ic.m[path] = e
	go func() {
		src := loadImage(path)
		ic.mu.Lock()
		e.src, e.done = src, true
		ic.mu.Unlock()
		if src != nil && ic.loaded != nil {
			ic.loaded()
		}
	}()
	return nil
}

func loadImage(path string) mui.ImageSource {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if strings.EqualFold(filepath.Ext(path), ".svg") {
		if s, err := mui.ParseSVG(data); err == nil {
			return s
		}
		return nil
	}
	if b, err := mui.DecodeBitmap(data); err == nil {
		return b
	}
	return nil
}

// icon builds an item's icon, size DIPs square, in fg unless its tint
// gives it another color; keep keeps fg (an icon on an accent-colored row).
func (l *Launcher) icon(c *mui.Context, ic plugin.Icon, size float32, fg mui.Color, keep bool) {
	if ic.Path != "" {
		if src := l.images.get(ic.Path); src != nil {
			mui.Image(c, src).Size(size, size).Fit(mui.Contain)
			return
		}
	}
	name := ic.Name
	if name == "" {
		name = "app"
	}
	tk := &l.res.Tokens
	if !keep && ic.Tint != "" {
		// Plugins name a meaning; the theme has its color ("tint.<name>").
		fg = tk.Tint(string(ic.Tint), fg)
	}
	mui.Box(c).Size(size, size).Center().Radius(size / 4).Background(tk.Control).Children(func() {
		mui.Icon(c, builtinIcon(name)).FontSize(size * 0.6).TextColor(fg)
	})
}
