package scripts

import (
	"bufio"
	"encoding/json/v2"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// metadataLines is how many lines at the top of a script are searched for
// @raycast metadata.
const metadataLines = 40

// Output modes of a script command.
const (
	modeSilent     = "silent"
	modeCompact    = "compact"
	modeFullOutput = "fullOutput"
	modeInline     = "inline"
)

// extensions are the script types that are scanned: on Unix also those of
// Raycast's Script Commands that only run there (unixExtensions).
var extensions = func() []string {
	ext := []string{".ps1", ".py", ".js", ".bat", ".cmd", ".sh"}
	if runtime.GOOS != "windows" {
		ext = append(ext, ".zsh", ".rb", ".swift", ".applescript")
	}
	return ext
}()

// Script is a parsed script command.
type Script struct {
	Path string
	// Shebang is the interpreter command line of a "#!" first line.
	Shebang           []string
	SchemaVersion     int
	Title             string
	Mode              string
	PackageName       string
	Icon              string
	IconDark          string
	Description       string
	Author            string
	AuthorURL         string
	CurrentDirectory  string
	NeedsConfirmation bool
	RefreshTime       string
	Arguments         []ScriptArgument
}

// ScriptArgument is one of @raycast.argument1..3.
type ScriptArgument struct {
	Index          int // 1..3
	Type           string
	Placeholder    string
	Optional       bool
	PercentEncoded bool
	Data           []ArgumentOption
}

// ArgumentOption is an entry of a dropdown argument's data.
type ArgumentOption struct {
	Title string `json:"title"`
	Value string `json:"value"`
}

// rawArgument is the JSON of an argument as written in the script.
type rawArgument struct {
	Type           string           `json:"type"`
	Placeholder    string           `json:"placeholder"`
	Optional       bool             `json:"optional"`
	PercentEncoded bool             `json:"percentEncoded"`
	Secure         bool             `json:"secure"` // deprecated: type password
	Data           []ArgumentOption `json:"data"`
}

// ParseScript reads the metadata of the script at path from r. It returns
// ok=false (and no error) when the file is not a script command, i.e. has no
// @raycast.title.
func ParseScript(path string, r io.Reader) (s Script, ok bool, err error) {
	s = Script{Path: path}
	batch := isBatch(path)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 64*1024)
	for n := 0; n < metadataLines && sc.Scan(); n++ {
		line := sc.Text()
		if n == 0 {
			line = strings.TrimPrefix(line, "\ufeff")
			if !batch && strings.HasPrefix(line, "#!") {
				s.Shebang = strings.Fields(line[2:])
				continue
			}
		}
		key, value, found := metadataLine(line)
		if !found {
			continue
		}
		if err := s.set(key, value); err != nil {
			return s, false, fmt.Errorf("%s:%d: %w", filepath.Base(path), n+1, err)
		}
	}
	if err := sc.Err(); err != nil {
		return s, false, err
	}
	if s.Title == "" {
		return s, false, nil
	}
	switch s.Mode {
	case modeSilent, modeCompact, modeFullOutput:
	case modeInline:
		// Inline output in the root search is not supported yet; Raycast
		// itself falls back to compact without a refreshTime.
		s.Mode = modeCompact
	default:
		s.Mode = modeCompact
	}
	if s.PackageName == "" {
		s.PackageName = filepath.Base(filepath.Dir(path))
	}
	slices.SortFunc(s.Arguments, func(a, b ScriptArgument) int { return a.Index - b.Index })
	return s, true, nil
}

// isBatch reports whether path is a cmd.exe batch file.
func isBatch(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".bat" || ext == ".cmd"
}

// commentPrefixes are the line comment markers metadata may follow.
var commentPrefixes = []string{"#", "//", "--", "::", "@rem", "rem", "<#"}

// metadataLine extracts "key value" from a "<comment> @raycast.key value" line.
func metadataLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	matched := false
	for _, p := range commentPrefixes {
		if len(line) >= len(p) && strings.EqualFold(line[:len(p)], p) {
			rest := line[len(p):]
			// "rem" must be a word of its own: "remark" is not a comment.
			if (p == "rem" || p == "@rem") && rest != "" && rest[0] != ' ' && rest[0] != '\t' {
				continue
			}
			line, matched = strings.TrimSpace(rest), true
			break
		}
	}
	if !matched {
		return "", "", false
	}
	rest, found := strings.CutPrefix(line, "@raycast.")
	if !found {
		return "", "", false
	}
	key, value, _ = strings.Cut(rest, " ")
	key, _, _ = strings.Cut(key, "\t")
	return key, strings.TrimSpace(strings.TrimPrefix(rest, key)), key != ""
}

// set applies one metadata entry.
func (s *Script) set(key, value string) error {
	switch key {
	case "schemaVersion":
		s.SchemaVersion, _ = strconv.Atoi(value)
	case "title":
		s.Title = value
	case "mode":
		s.Mode = value
	case "packageName":
		s.PackageName = value
	case "icon":
		s.Icon = value
	case "iconDark":
		s.IconDark = value
	case "description":
		s.Description = value
	case "author":
		s.Author = value
	case "authorURL":
		s.AuthorURL = value
	case "currentDirectoryPath":
		s.CurrentDirectory = value
	case "needsConfirmation":
		s.NeedsConfirmation = value == "true"
	case "refreshTime":
		s.RefreshTime = value
	default:
		idx, found := strings.CutPrefix(key, "argument")
		if !found {
			return nil // unknown keys are ignored, as Raycast does
		}
		i, err := strconv.Atoi(idx)
		if err != nil || i < 1 || i > 3 {
			return nil
		}
		var raw rawArgument
		if err := json.Unmarshal([]byte(value), &raw); err != nil {
			return fmt.Errorf("@raycast.%s: %w", key, err)
		}
		a := ScriptArgument{
			Index:          i,
			Type:           raw.Type,
			Placeholder:    raw.Placeholder,
			Optional:       raw.Optional,
			PercentEncoded: raw.PercentEncoded,
			Data:           raw.Data,
		}
		if raw.Secure && a.Type != "dropdown" {
			a.Type = "password"
		}
		if a.Type == "" {
			a.Type = "text"
		}
		s.Arguments = slices.DeleteFunc(s.Arguments, func(x ScriptArgument) bool { return x.Index == i })
		s.Arguments = append(s.Arguments, a)
	}
	return nil
}

// argName is the name of argument i in plugin.Argument and ActionDo.Values.
func argName(i int) string { return "argument" + strconv.Itoa(i) }

// pluginArguments maps the script's arguments to SDK arguments.
func (s *Script) pluginArguments() []plugin.Argument {
	var out []plugin.Argument
	for _, a := range s.Arguments {
		pa := plugin.Argument{
			Name:        argName(a.Index),
			Type:        plugin.ArgText,
			Placeholder: a.Placeholder,
			Required:    !a.Optional,
		}
		switch a.Type {
		case "password":
			pa.Type = plugin.ArgPassword
		case "dropdown":
			pa.Type = plugin.ArgDropdown
			for _, o := range a.Data {
				pa.Options = append(pa.Options, plugin.Option{Value: o.Value, Title: o.Title})
			}
		}
		out = append(out, pa)
	}
	return out
}

// imageExts are icon files the host can draw.
var imageExts = []string{".png", ".svg"}

// icon maps @raycast.icon to an SDK icon. Emoji and URLs cannot be shown
// (plugin.Icon has no text or remote form), so they use the generic icon.
func (s *Script) icon() plugin.Icon {
	v := s.Icon
	if v != "" && !strings.Contains(v, "://") && slices.Contains(imageExts, strings.ToLower(filepath.Ext(v))) {
		if !filepath.IsAbs(v) {
			v = filepath.Join(filepath.Dir(s.Path), v)
		}
		return plugin.Icon{Path: v}
	}
	return plugin.Icon{Name: "command"}
}
