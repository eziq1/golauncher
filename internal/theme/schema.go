package theme

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
)

// colorPattern matches the color formats ParseColor reads.
const colorPattern = `^\s*(#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})|[rR][gG][bB][aA]?\(.*\)|transparent)\s*$`

// Schema returns the JSON Schema of theme files, docs/theme.schema.json.
func Schema() ([]byte, error) {
	type obj = map[string]any
	color := func(desc string) obj { return obj{"type": "string", "pattern": colorPattern, "description": desc} }
	colors := obj{}
	for _, k := range colorKeys {
		desc := k.help
		if k.required {
			desc += " (required without extends)"
		}
		colors[k.name] = color(desc)
	}
	s := obj{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"$id":         SchemaURL,
		"title":       "golauncher theme",
		"description": "A golauncher theme (docs/themes.md). Put it in %APPDATA%\\golauncher\\themes; its file name without .json is its ID.",
		"type":        "object",
		"patternProperties": obj{
			`^name\.[A-Za-z]{2,3}(-[A-Za-z0-9]+)*$`: obj{"type": "string", "description": "The name in a language, e.g. name.zh-CN"},
		},
		"additionalProperties": false,
		"properties": obj{
			"$schema":      obj{"type": "string"},
			"name":         obj{"type": "string", "description": "The name shown in the settings"},
			"author":       obj{"type": "string"},
			"source":       obj{"type": "string", "description": "Where the palette comes from"},
			"license":      obj{"type": "string"},
			"extends":      obj{"type": "string", "description": "The ID of the theme this one changes: a built-in theme (golauncher-dark, nord, …) or another file's name without .json"},
			"dark":         obj{"type": "boolean", "description": "A dark theme (inherited)"},
			"highContrast": obj{"type": "boolean", "description": "Checked at 7:1 and always opaque"},
			"systemAccent": obj{"type": "boolean", "description": "Use the Windows accent color unless the settings choose another"},
			"colors": obj{
				"type":        "object",
				"description": "Colors: #rgb, #rgba, #rrggbb, #rrggbbaa, rgb(…) or rgba(…). Leave out what the base theme has or what derives from another color.",
				"properties":  colors,
				"patternProperties": obj{
					`^tint\.[a-z]+$`: color("an icon tint of another name"),
				},
				"additionalProperties": false,
			},
			"material": obj{"enum": []string{"acrylic", "mica", "none"}, "description": "What shows through the background"},
			"opacity":  obj{"type": "number", "minimum": 0, "maximum": 1, "description": "How much of the background covers the material: 0 is the material alone, 1 is solid"},
			"radius":   obj{"type": "number", "minimum": MinRadius, "maximum": MaxRadius, "description": "Corner radius in DIPs (6)"},
			"density":  obj{"enum": []string{"compact", "standard", "comfortable"}},
			"fontSize": obj{"type": "number", "minimum": MinFontSize, "maximum": MaxFontSize, "description": "Text size in DIPs (the system's)"},
		},
	}
	b, err := json.Marshal(s, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	v := jsontext.Value(b)
	if err := v.Indent(jsontext.WithIndent("  ")); err != nil {
		return nil, err
	}
	return append(v, '\n'), nil
}
