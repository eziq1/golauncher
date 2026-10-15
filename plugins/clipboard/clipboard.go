// Package clipboard is the built-in clipboard history plugin. It records
// what the user copies (text, images and file lists) in a SQLite database
// and lets them search, pin and paste it again.
//
// The plugin never touches Win32 itself: the host watches the clipboard and
// publishes plugin.EventClipboardChanged, after which the plugin reads the
// content through Host.Clipboard.
package clipboard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// ID is the plugin id.
const ID = "builtin.clipboard"

// viewHistory is the ID of the history view (the command's name).
const viewHistory = "history"

//go:embed locales/*.json
var locales embed.FS

// Plugin is the clipboard history plugin.
type Plugin struct {
	mu    sync.Mutex // guards the fields below
	h     plugin.Host
	db    *store
	unsub func()

	recordMu sync.Mutex // serializes recording
}

var (
	_ plugin.Plugin         = (*Plugin)(nil)
	_ plugin.ViewProvider   = (*Plugin)(nil)
	_ plugin.ActionHandler  = (*Plugin)(nil)
	_ plugin.LocaleProvider = (*Plugin)(nil)
)

// New returns the clipboard plugin.
func New() *Plugin { return &Plugin{} }

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:            ID,
		Name:          "%plugin.name%",
		Description:   "%plugin.description%",
		Version:       "1.0.0",
		Icon:          plugin.Icon{Name: "clipboard"},
		DefaultLocale: "en",
		Permissions:   []plugin.Permission{plugin.PermClipboard, plugin.PermStorage},
		Commands: []plugin.Command{
			{
				Name:  "history",
				Title: "%command.history.title%",
				Mode:  plugin.ModeView,
				Icon:  plugin.Icon{Name: "clipboard"},
				Keywords: []string{
					"%command.history.keyword.clip%",
					"%command.history.keyword.paste%",
					"%command.history.keyword.history%",
				},
				// Not Ctrl+Shift+V: a global hotkey would steal "paste as
				// plain text" from every application.
				DefaultHotkey: "Alt+Shift+V",
			},
			{
				Name:        "clear",
				Title:       "%command.clear.title%",
				Mode:        plugin.ModeNoView,
				Icon:        plugin.Icon{Name: "trash"},
				Destructive: true,
			},
		},
		Preferences: []plugin.Preference{
			{Name: "maxItems", Type: plugin.PrefNumber, Title: "%pref.maxItems.title%",
				Description: "%pref.maxItems.description%", Default: plugin.MustPayload(1000)},
			{Name: "maxDays", Type: plugin.PrefNumber, Title: "%pref.maxDays.title%",
				Description: "%pref.maxDays.description%", Default: plugin.MustPayload(90)},
			{Name: "recordImages", Type: plugin.PrefBool, Title: "%pref.recordImages.title%",
				Description: "%pref.recordImages.description%", Default: plugin.MustPayload(true)},
			{Name: "ignoreApps", Type: plugin.PrefStringList, Title: "%pref.ignoreApps.title%",
				Description: "%pref.ignoreApps.description%",
				Default:     plugin.MustPayload([]string{"KeePassXC.exe", "1Password.exe", "Bitwarden.exe", "KeePass.exe"})},
		},
	}
}

// Locales implements plugin.LocaleProvider.
func (p *Plugin) Locales() fs.FS {
	sub, err := fs.Sub(locales, "locales")
	if err != nil {
		panic(err)
	}
	return sub
}

// Init implements plugin.Plugin. It opens the history database and starts
// recording clipboard changes.
func (p *Plugin) Init(ctx context.Context, h plugin.Host) error {
	if err := os.MkdirAll(imagesDir(h), 0o755); err != nil {
		return err
	}
	db, err := openStore(filepath.Join(h.DataDir(), "clipboard.db"))
	if err != nil {
		return err
	}
	// Limits may have been lowered, or time may have passed, while the
	// plugin was not running.
	if err := p.prune(h, db); err != nil {
		h.Logger().Warn("clipboard: prune failed", "err", err)
	}
	p.mu.Lock()
	p.h, p.db = h, db
	p.mu.Unlock()
	unsubClip := h.Events().Subscribe(plugin.EventClipboardChanged, p.onClipboardChanged)
	unsubConfig := h.Events().Subscribe(plugin.EventConfigChanged, p.onConfigChanged)
	p.mu.Lock()
	p.unsub = func() { unsubClip(); unsubConfig() }
	p.mu.Unlock()
	return nil
}

// Close implements plugin.Plugin.
func (p *Plugin) Close() error {
	p.mu.Lock()
	unsub, db := p.unsub, p.db
	p.unsub, p.db = nil, nil
	p.mu.Unlock()
	if unsub != nil {
		unsub()
	}
	if db != nil {
		return db.Close()
	}
	return nil
}

var errClosed = errors.New("clipboard: plugin is not initialized")

func (p *Plugin) state() (plugin.Host, *store, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.db == nil {
		return nil, nil, errClosed
	}
	return p.h, p.db, nil
}

func imagesDir(h plugin.Host) string { return filepath.Join(h.DataDir(), "images") }

func (p *Plugin) onClipboardChanged() {
	h, db, err := p.state()
	if err != nil {
		return
	}
	c, err := h.Clipboard().Read()
	if err != nil {
		h.Logger().Warn("clipboard: read failed", "err", err)
		return
	}
	if err := p.record(h, db, c); err != nil {
		h.Logger().Warn("clipboard: record failed", "err", err)
	}
}

// onConfigChanged applies possibly changed history limits.
func (p *Plugin) onConfigChanged() {
	h, db, err := p.state()
	if err != nil {
		return
	}
	p.recordMu.Lock()
	defer p.recordMu.Unlock()
	if err := p.prune(h, db); err != nil {
		h.Logger().Warn("clipboard: prune failed", "err", err)
	}
	h.Invalidate(viewHistory) // lower limits may have removed clips
}

// prune enforces the maxItems and maxDays preferences and removes the
// image files of deleted clips. Callers other than Init hold recordMu.
func (p *Plugin) prune(h plugin.Host, db *store) error {
	prefs := h.Preferences()
	images, err := db.prune(h.Now(), prefs.Int("maxItems"), prefs.Int("maxDays"))
	removeImages(h, images)
	return err
}

// maxTextBytes is the largest text that is recorded; larger copies are
// skipped so that the database and every list view stay bounded.
const maxTextBytes = 1 << 20

// record stores c in the history unless it is filtered out.
func (p *Plugin) record(h plugin.Host, db *store, c plugin.ClipboardContent) error {
	prefs := h.Preferences()
	if c.Exclude {
		return nil
	}
	if c.SourceApp != "" && slices.ContainsFunc(prefs.Strings("ignoreApps"), func(app string) bool {
		return strings.EqualFold(strings.TrimSpace(app), c.SourceApp)
	}) {
		return nil
	}

	var (
		e       = clip{SourceApp: c.SourceApp}
		sum     [sha256.Size]byte
		body    string
		pngData []byte
	)
	switch {
	case len(c.Files) > 0:
		e.Kind, e.Files, e.Size = kindFiles, c.Files, int64(len(c.Files))
		body = ftsBody(strings.Join(c.Files, "\n"))
		sum = hash(kindFiles, []byte(strings.Join(c.Files, "\x00")))
	case strings.TrimSpace(c.Text) != "":
		if len(c.Text) > maxTextBytes {
			h.Logger().Info("clipboard: text too large, not recorded", "bytes", len(c.Text), "limit", maxTextBytes)
			return nil
		}
		e.Kind, e.Text, e.Size = kindText, c.Text, int64(len(c.Text))
		body = ftsBody(c.Text)
		sum = hash(kindText, []byte(c.Text))
	case len(c.Image) > 0:
		if !prefs.Bool("recordImages") {
			return nil
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(c.Image))
		if err != nil {
			return fmt.Errorf("clipboard: image: %w", err)
		}
		e.Kind, e.Width, e.Height, e.Size = kindImage, cfg.Width, cfg.Height, int64(len(c.Image))
		sum = hash(kindImage, c.Image)
		pngData = c.Image
	default:
		return nil
	}
	hexSum := hex.EncodeToString(sum[:])

	p.recordMu.Lock()
	defer p.recordMu.Unlock()
	if pngData != nil {
		e.Image = hexSum + ".png"
		if err := writeFileOnce(filepath.Join(imagesDir(h), e.Image), pngData); err != nil {
			return err
		}
	}
	now := h.Now()
	_, added, err := db.add(e, hexSum, body, now)
	if err != nil || !added {
		return err
	}
	err = p.prune(h, db)
	h.Invalidate(viewHistory) // an open history view shows the new clip
	return err
}

func hash(kind string, data []byte) [sha256.Size]byte {
	s := sha256.New()
	s.Write([]byte(kind))
	s.Write([]byte{0})
	s.Write(data)
	return [sha256.Size]byte(s.Sum(nil))
}

// writeFileOnce writes data to path unless the file exists. The file is
// written under a temporary name first so readers never see a partial image.
func writeFileOnce(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func removeImages(h plugin.Host, names []string) {
	for _, name := range names {
		if err := os.Remove(filepath.Join(imagesDir(h), name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			h.Logger().Warn("clipboard: remove image", "file", name, "err", err)
		}
	}
}
