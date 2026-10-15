package scoop

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"path/filepath"
	"slices"
	"strings"
)

// Package is what the plugin knows about one manifest of a bucket (or the
// manifest an installed app was installed from).
type Package struct {
	Name        string   `json:"name"`
	Bucket      string   `json:"bucket,omitempty"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	License     string   `json:"license,omitempty"`
	Bin         []string `json:"bin,omitempty"` // command names (shims)
	Notes       string   `json:"notes,omitempty"`
	// File is the manifest file.
	File string `json:"file,omitempty"`
}

// rawManifest is the part of a Scoop manifest the plugin reads. Fields with
// several forms are decoded by hand.
type rawManifest struct {
	Version      jsontext.Value `json:"version"`
	Description  jsontext.Value `json:"description"`
	Homepage     jsontext.Value `json:"homepage"`
	License      jsontext.Value `json:"license"`
	Bin          jsontext.Value `json:"bin"`
	Notes        jsontext.Value `json:"notes"`
	Architecture jsontext.Value `json:"architecture"`
}

type rawArchPart struct {
	Bin jsontext.Value `json:"bin"`
}

// lenient accepts what real manifests contain: duplicate keys and invalid
// UTF-8 are not worth losing a package over.
var lenient = json.JoinOptions(jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))

// ParseManifest parses the manifest of app name from data. Fields of the
// wrong type are left empty rather than failing the whole manifest.
func ParseManifest(name string, data []byte) (Package, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var m rawManifest
	if err := json.Unmarshal(data, &m, lenient); err != nil {
		return Package{}, err
	}
	p := Package{
		Name:        name,
		Version:     scalar(m.Version),
		Description: joined(m.Description, " "),
		Homepage:    scalar(m.Homepage),
		License:     license(m.License),
		Notes:       joined(m.Notes, "\n"),
	}
	p.Bin = binNames(m.Bin, nil)
	// Architecture-specific bins: 64bit first, as that is what is installed
	// on almost every machine.
	var archs map[string]rawArchPart
	if m.Architecture.Kind() == '{' {
		_ = json.Unmarshal(m.Architecture, &archs, lenient)
	}
	for _, arch := range []string{"64bit", "arm64", "32bit"} {
		if a, ok := archs[arch]; ok {
			p.Bin = binNames(a.Bin, p.Bin)
		}
	}
	return p, nil
}

// scalar returns a JSON string, or the text of a number, or "".
func scalar(v jsontext.Value) string {
	switch v.Kind() {
	case '"':
		var s string
		if json.Unmarshal(v, &s, lenient) == nil {
			return strings.TrimSpace(s)
		}
	case '0':
		return string(v)
	}
	return ""
}

// joined returns a string, or the strings of an array joined by sep.
func joined(v jsontext.Value, sep string) string {
	if v.Kind() != '[' {
		return scalar(v)
	}
	var items []jsontext.Value
	if json.Unmarshal(v, &items, lenient) != nil {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, scalar(it))
	}
	return strings.TrimSpace(strings.Join(parts, sep))
}

// license returns a license string or the identifier of a license object.
func license(v jsontext.Value) string {
	if v.Kind() == '{' {
		var l struct {
			Identifier jsontext.Value `json:"identifier"`
		}
		if json.Unmarshal(v, &l, lenient) == nil {
			return scalar(l.Identifier)
		}
		return ""
	}
	return scalar(v)
}

// binNames appends the command names of a "bin" value to names: "x.exe",
// ["a.exe", "b.ps1"] or [["c.exe", "alias", "args"]]. A shim is named after
// the alias when there is one, else after the file without extension.
func binNames(v jsontext.Value, names []string) []string {
	add := func(n string) {
		n = strings.TrimSpace(n)
		if n != "" && !slices.ContainsFunc(names, func(x string) bool { return strings.EqualFold(x, n) }) {
			names = append(names, n)
		}
	}
	fromPath := func(p string) string {
		base := filepath.Base(strings.ReplaceAll(p, `\`, "/"))
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	switch v.Kind() {
	case '"':
		add(fromPath(scalar(v)))
	case '[':
		var items []jsontext.Value
		if json.Unmarshal(v, &items, lenient) != nil {
			return names
		}
		for _, it := range items {
			switch it.Kind() {
			case '"':
				add(fromPath(scalar(it)))
			case '[':
				var parts []jsontext.Value
				if json.Unmarshal(it, &parts, lenient) != nil || len(parts) == 0 {
					continue
				}
				if len(parts) > 1 && scalar(parts[1]) != "" {
					add(scalar(parts[1]))
				} else {
					add(fromPath(scalar(parts[0])))
				}
			}
		}
	}
	return names
}
