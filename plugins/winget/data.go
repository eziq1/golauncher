package winget

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

// Installed is a row of `winget list`: software installed on the machine,
// whether or not winget installed it.
type Installed struct {
	Name      string `json:"name"`
	ID        string `json:"id"`
	Version   string `json:"version"`
	Available string `json:"available,omitempty"` // a newer version, if any
	Source    string `json:"source,omitempty"`    // "" for software no source knows
}

// errNoTable reports output without a table.
var errNoTable = errors.New("winget: no table in the output")

// parseList reads the output of `winget list` (or `winget upgrade`). The
// columns are name, ID, version, available version and source, in this
// order whatever the language; with four columns the last one is the source
// if all its values are known source names, else the available version.
func parseList(out []byte, sources []string) ([]Installed, error) {
	_, rows, ok := parseTable(out)
	if !ok {
		if len(bytes.TrimSpace(out)) == 0 {
			return nil, nil
		}
		return nil, errNoTable
	}
	if len(rows) == 0 {
		return nil, nil
	}
	n := len(rows[0])
	if n < 3 {
		return nil, errNoTable
	}
	lastIsSource := n >= 5
	if n == 4 {
		lastIsSource = true
		for _, r := range rows {
			if r[3] != "" && !slices.ContainsFunc(sources, func(s string) bool { return strings.EqualFold(s, r[3]) }) {
				lastIsSource = false
				break
			}
		}
	}
	items := make([]Installed, 0, len(rows))
	for _, r := range rows {
		it := Installed{Name: r[0], ID: r[1], Version: r[2]}
		switch {
		case n >= 5:
			it.Available, it.Source = r[3], r[4]
		case n == 4 && lastIsSource:
			it.Source = r[3]
		case n == 4:
			it.Available = r[3]
		}
		if it.ID == "" {
			continue
		}
		items = append(items, it)
	}
	return items, nil
}

// Source is a configured winget source, as `winget source export` prints
// it (one JSON object per line).
type Source struct {
	Name       string `json:"Name"`
	Arg        string `json:"Arg"`
	Type       string `json:"Type"`
	Data       string `json:"Data"`
	Identifier string `json:"Identifier"`
	Explicit   bool   `json:"Explicit"`
}

// typePreIndexed is the type of sources shipped as an MSIX package holding
// a SQLite index (the "winget" source).
const typePreIndexed = "Microsoft.PreIndexed.Package"

// defaultSource is assumed when the sources cannot be listed.
var defaultSource = Source{
	Name:       "winget",
	Arg:        "https://cdn.winget.microsoft.com/cache",
	Type:       typePreIndexed,
	Data:       "Microsoft.Winget.Source_8wekyb3d8bbwe",
	Identifier: "Microsoft.Winget.Source_8wekyb3d8bbwe",
}

// Indexed reports whether the source has a local index the plugin can
// search: a pre-indexed package that is searched by default.
func (s Source) Indexed() bool { return s.Type == typePreIndexed && !s.Explicit && s.Data != "" }

// parseSources reads the output of `winget source export`.
func parseSources(out []byte) ([]Source, error) {
	var srcs []Source
	for _, line := range outputLines(out) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var s Source
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			return nil, err
		}
		if s.Name != "" {
			srcs = append(srcs, s)
		}
	}
	if len(srcs) == 0 {
		return nil, errors.New("winget: no sources in the output")
	}
	return srcs, nil
}

// Pin kinds (the type column of pinning.db, winget's PinType).
const (
	pinByManifest = 1
	pinPinning    = 2
	pinGating     = 3
	pinBlocking   = 4
)

// Pin is a package pin: `winget upgrade --all` skips the package.
type Pin struct {
	ID       string `json:"id"`
	SourceID string `json:"sourceId"`
	Type     int    `json:"type"`
	Version  string `json:"version,omitempty"` // the version range of a gating pin
}

// readPins reads the pins from winget's pinning.db, by lower-case package
// ID.
func readPins(ctx context.Context, db *sql.DB) (map[string]Pin, error) {
	rows, err := db.QueryContext(ctx, `SELECT package_id, source_id, type, version FROM pin`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pins := map[string]Pin{}
	for rows.Next() {
		var p Pin
		if err := rows.Scan(&p.ID, &p.SourceID, &p.Type, &p.Version); err != nil {
			return nil, err
		}
		pins[strings.ToLower(p.ID)] = p
	}
	return pins, rows.Err()
}

// Field is a "Label: value" entry of `winget show`. Labels are in the
// language of winget's UI; values may span several (indented) lines.
type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Details is the parsed output of `winget show`.
type Details struct {
	Title  string  `json:"title"` // "Found Contoso TextPad [Contoso.TextPad]" (localized)
	Fields []Field `json:"fields"`
}

// Get returns the value of the field labeled label (English), if any.
func (d *Details) Get(label string) string {
	for _, f := range d.Fields {
		if strings.EqualFold(f.Label, label) {
			return f.Value
		}
	}
	return ""
}

// cutLabel splits a top-level show line at its colon ("Version: 1.0",
// "Description:"); full-width colons are accepted too.
func cutLabel(line string) (label, value string, ok bool) {
	i := strings.IndexAny(line, ":：")
	if i <= 0 {
		return "", "", false
	}
	label = strings.TrimSpace(line[:i])
	rest := line[i:]
	if strings.HasPrefix(rest, "://") || label == "" {
		return "", "", false
	}
	_, size := utf8.DecodeRuneInString(rest)
	return label, strings.TrimSpace(rest[size:]), true
}

// parseShow reads the output of `winget show`: a title line, then fields
// at the start of a line, continued by indented lines.
func parseShow(out []byte) (*Details, error) {
	d := &Details{}
	for _, line := range outputLines(out) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indented := line[0] == ' ' || line[0] == '\t'
		if d.Title == "" && !indented && strings.HasSuffix(strings.TrimSpace(line), "]") && strings.Contains(line, " [") {
			d.Title = strings.TrimSpace(line)
			continue
		}
		if d.Title == "" {
			continue // progress output before the title
		}
		if indented && len(d.Fields) > 0 {
			f := &d.Fields[len(d.Fields)-1]
			// Keep the indentation of nested lines beyond the first level.
			text := strings.TrimRight(line, " \t")
			text = strings.TrimPrefix(text, "  ")
			if f.Value != "" {
				f.Value += "\n"
			}
			f.Value += text
			continue
		}
		if label, value, ok := cutLabel(line); ok {
			d.Fields = append(d.Fields, Field{Label: label, Value: value})
		}
	}
	if d.Title == "" {
		return nil, errors.New("winget: no package in the output")
	}
	return d, nil
}

// titleName returns the package name of a show title "Found Name [Id]".
// The word before the name is localized; the name is what precedes " [".
func titleName(title, fallback string) string {
	i := strings.LastIndex(title, " [")
	if i < 0 {
		return fallback
	}
	head := title[:i]
	if fallback != "" && strings.HasSuffix(head, fallback) {
		return fallback
	}
	if _, rest, ok := strings.Cut(head, " "); ok {
		return rest
	}
	return fallback
}
