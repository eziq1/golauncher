package scoop

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// OldVersions are the versions of an app that scoop cleanup removes.
type OldVersions struct {
	App      string
	Dir      string // apps\<name>
	Versions []string
	Size     int64
}

// Reclaim is what scoop cleanup * and scoop cache rm * would free.
type Reclaim struct {
	Apps       []OldVersions
	OldSize    int64
	CacheFiles int
	CacheSize  int64
}

// Total is the number of bytes that can be freed.
func (r Reclaim) Total() int64 { return r.OldSize + r.CacheSize }

// dirSize adds up the sizes of the files under dir. Links (junctions) are
// not followed.
func dirSize(ctx context.Context, dir string) (size int64, files int) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				size += info.Size()
				files++
			}
		}
		return nil
	})
	return size, files
}

// computeReclaim measures the old versions of the user's apps (every
// version folder but the one "current" points to) and the download cache.
// Global apps are left out: cleaning them needs administrator rights.
func computeReclaim(ctx context.Context, root string, apps []App) Reclaim {
	var r Reclaim
	for _, a := range apps {
		if a.Global || ctx.Err() != nil {
			continue
		}
		appDir := filepath.Dir(a.Dir)
		current := ""
		if target, err := filepath.EvalSymlinks(a.Dir); err == nil {
			current = filepath.Base(target)
		}
		entries, err := os.ReadDir(appDir)
		if err != nil {
			continue
		}
		ov := OldVersions{App: a.Name, Dir: appDir}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || e.Type()&fs.ModeSymlink != 0 || strings.EqualFold(name, "current") ||
				strings.EqualFold(name, current) || strings.EqualFold(name, a.Version) {
				continue
			}
			size, _ := dirSize(ctx, filepath.Join(appDir, name))
			ov.Versions = append(ov.Versions, name)
			ov.Size += size
		}
		if len(ov.Versions) > 0 {
			slices.SortFunc(ov.Versions, CompareVersions)
			r.Apps = append(r.Apps, ov)
			r.OldSize += ov.Size
		}
	}
	slices.SortFunc(r.Apps, func(a, b OldVersions) int {
		if a.Size != b.Size {
			if a.Size > b.Size {
				return -1
			}
			return 1
		}
		return strings.Compare(a.App, b.App)
	})
	r.CacheSize, r.CacheFiles = dirSize(ctx, filepath.Join(root, "cache"))
	return r
}
