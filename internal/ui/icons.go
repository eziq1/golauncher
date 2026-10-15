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
	"chevron-left": `<path d="m15 18-6-6 6-6"/>`,
	"info":         `<circle cx="12" cy="12" r="10"/><path d="M12 16v-4M12 8h.01"/>`,
	"wrench":       `<path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/>`,
	"check":        `<path d="M20 6 9 17l-5-5"/>`,
	"alert":        `<circle cx="12" cy="12" r="10"/><path d="M12 8v4M12 16h.01"/>`,
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

// icon builds an item's icon, size DIPs square.
func (l *Launcher) icon(c *mui.Context, ic plugin.Icon, size float32, fg mui.Color) {
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
	t := c.Theme()
	mui.Box(c).Size(size, size).Center().Radius(size / 4).Background(t.Text.Alpha(0.08)).Children(func() {
		mui.Icon(c, builtinIcon(name)).FontSize(size * 0.6).TextColor(fg)
	})
}
