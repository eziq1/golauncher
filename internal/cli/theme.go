package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/theme"
)

// themeInfo is a theme in glctl theme list.
type themeInfo struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Names        map[string]string `json:"names,omitempty"`
	Dark         bool              `json:"dark"`
	HighContrast bool              `json:"highContrast,omitzero"`
	Builtin      bool              `json:"builtin"`
	Extends      string            `json:"extends,omitempty"`
	Author       string            `json:"author,omitempty"`
	Source       string            `json:"source,omitempty"`
	License      string            `json:"license,omitempty"`
	Path         string            `json:"path,omitempty"`
}

// themeShow is glctl theme show: a theme with every color derived and
// its contrast checked.
type themeShow struct {
	themeInfo
	Material     theme.Material         `json:"material"`
	Opacity      float64                `json:"opacity"`
	Radius       float32                `json:"radius"`
	Density      theme.Density          `json:"density"`
	FontSize     float32                `json:"fontSize,omitzero"`
	SystemAccent bool                   `json:"systemAccent,omitzero"`
	Colors       map[string]string      `json:"colors"`
	Contrast     []theme.ContrastResult `json:"contrast"`
	Warnings     []string               `json:"warnings,omitempty"`
}

func infoOf(t *theme.Theme) themeInfo {
	return themeInfo{ID: t.ID, Name: t.Name, Names: t.Names, Dark: t.Dark, HighContrast: t.HighContrast, Builtin: t.Builtin,
		Extends: t.Extends, Author: t.Author, Source: t.Source, License: t.License, Path: t.Path}
}

// theme implements glctl theme list | show <id> | validate <file> | export <id>.
func (c *cli) theme(args []string) error {
	fs := c.flagSet("theme", nil)
	dir := fs.String("themes", "", "a folder of theme files to add to the built-in themes")
	real := fs.Bool("real", false, `also the user's themes (%APPDATA%\golauncher\themes)`)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return usagef("theme: want list, show <id>, validate <file> or export <id>")
	}
	themesDir := *dir
	if *real && themesDir == "" {
		cfgDir, err := app.DefaultConfigDir()
		if err != nil {
			return err
		}
		themesDir = filepath.Join(cfgDir, "themes")
	}
	lib := theme.NewLibrary(themesDir)
	if err := lib.Reload(); err != nil {
		return err
	}
	get := func() (*theme.Theme, error) {
		if len(pos) != 2 {
			return nil, usagef("theme %s: want <id>", pos[0])
		}
		t, ok := lib.Get(pos[1])
		if !ok {
			return nil, fmt.Errorf("no theme %q (see glctl theme list)", pos[1])
		}
		return t, nil
	}
	switch pos[0] {
	case "list":
		var out []themeInfo
		for _, t := range lib.Themes() {
			out = append(out, infoOf(t))
		}
		return c.output(out, func(w io.Writer) {
			for _, t := range out {
				mode := "light"
				if t.Dark {
					mode = "dark"
				}
				origin := "built-in"
				if !t.Builtin {
					origin = t.Path
				}
				fmt.Fprintf(w, "%-22s %-22s %-5s %s\n", t.ID, t.Name, mode, origin)
			}
			for _, p := range lib.Problems() {
				fmt.Fprintf(w, "problem: %s: %s %s\n", p.ID, p.Error, strings.Join(p.Warnings, "; "))
			}
		})
	case "show":
		t, err := get()
		if err != nil {
			return err
		}
		out := themeShow{themeInfo: infoOf(t), Material: t.Material, Opacity: t.Opacity, Radius: t.Radius, Density: t.Density,
			FontSize: t.FontSize, SystemAccent: t.SystemAccent, Colors: map[string]string{}, Contrast: theme.CheckContrast(t), Warnings: t.Warnings}
		for name, col := range t.Colors() {
			out.Colors[name] = theme.FormatColor(col)
		}
		return c.output(out, func(w io.Writer) {
			fmt.Fprintf(w, "%s (%s)\n", out.Name, out.ID)
			for _, name := range theme.ColorNames() {
				fmt.Fprintf(w, "  %-18s %s\n", name, out.Colors[name])
			}
			writeContrast(w, out.Contrast)
		})
	case "export":
		t, err := get()
		if err != nil {
			return err
		}
		data, err := theme.ExportTheme(t).Encode()
		if err != nil {
			return err
		}
		_, err = c.stdout.Write(data)
		return err
	case "validate":
		if len(pos) != 2 {
			return usagef("theme validate: want <file>")
		}
		data, err := os.ReadFile(pos[1])
		if err != nil {
			return err
		}
		r := lib.Validate(filepath.ToSlash(pos[1]), data)
		if err := c.output(r, func(w io.Writer) {
			for _, s := range r.Errors {
				fmt.Fprintln(w, "error:", s)
			}
			for _, s := range r.Warnings {
				fmt.Fprintln(w, "warning:", s)
			}
			writeContrast(w, r.Contrast)
			fmt.Fprintf(w, "%s: %d errors, %d warnings\n", r.File, len(r.Errors), len(r.Warnings))
		}); err != nil {
			return err
		}
		if !r.OK {
			return fmt.Errorf("theme validate: %d errors", len(r.Errors))
		}
		return nil
	}
	return usagef("theme: want list, show <id>, validate <file> or export <id>")
}

func writeContrast(w io.Writer, results []theme.ContrastResult) {
	for _, r := range results {
		mark := "ok"
		if !r.OK {
			mark = "LOW"
		}
		over := ""
		if r.Over != "" {
			over = " (over " + r.Over + ")"
		}
		fmt.Fprintf(w, "contrast %-28s %5.2f:1  min %.1f:1  %s%s\n", r.Pair, r.Ratio, r.Min, mark, over)
	}
}
