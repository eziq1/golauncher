package shell

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/eziq1/golauncher/internal/app"
)

// maxLogSize is the size past which the log is moved to golauncher.log.old
// at start.
const maxLogSize = 5 << 20

// openLog opens %LOCALAPPDATA%\golauncher\logs\golauncher.log for appending.
func openLog() (*os.File, *slog.LevelVar, error) {
	cache, err := app.DefaultCacheDir()
	if err != nil {
		return nil, nil, err
	}
	dir := filepath.Join(cache, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	path := filepath.Join(dir, "golauncher.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxLogSize {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}
	return f, new(slog.LevelVar), nil
}

// setLevel sets the log level from the configuration ("info" when unknown).
func setLevel(v *slog.LevelVar, level string) {
	switch level {
	case "debug":
		v.Set(slog.LevelDebug)
	case "warn":
		v.Set(slog.LevelWarn)
	case "error":
		v.Set(slog.LevelError)
	default:
		v.Set(slog.LevelInfo)
	}
}

// trayIcon draws the tray icon: a magnifying glass in a rounded blue tile,
// 32×32 pixels, legible on light and dark taskbars.
func trayIcon() []byte {
	const n = 32
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	tile := color.NRGBA{R: 59, G: 130, B: 246, A: 255}
	ink := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	inTile := func(x, y int) bool {
		const r = 7 // corner radius
		cx := min(max(x, r), n-1-r)
		cy := min(max(y, r), n-1-r)
		dx, dy := x-cx, y-cy
		return dx*dx+dy*dy <= r*r
	}
	for y := range n {
		for x := range n {
			if !inTile(x, y) {
				continue
			}
			img.SetNRGBA(x, y, tile)
			dx, dy := float64(x)-13.5, float64(y)-13.5
			if d := dx*dx + dy*dy; d >= 4.5*4.5 && d <= 7*7 {
				img.SetNRGBA(x, y, ink)
			}
			// The handle, along the diagonal from the ring outwards.
			if x >= 18 && x <= 25 && y >= 18 && y <= 25 && abs(x-y) <= 1 {
				img.SetNRGBA(x, y, ink)
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func abs(v int) int { return max(v, -v) }
