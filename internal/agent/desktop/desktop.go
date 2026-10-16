// Package desktop is the "Connect to Claude Desktop" helper (docs/ai.md §4.3):
// it registers golauncher's MCP server (`golauncher.exe mcp`) under
// mcpServers.golauncher in Claude Desktop's claude_desktop_config.json.
//
// The config file belongs to the user and to Claude Desktop, so the edit is
// surgical: the file is parsed with encoding/json/jsontext only to find byte
// offsets, and the new entry is spliced into the original bytes. Every other
// key and value keeps its exact bytes and order. The old file is backed up
// first and the new one is written atomically.
package desktop

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// ServerName is the key under mcpServers.
const ServerName = "golauncher"

// configName is the name of Claude Desktop's config file.
const configName = "claude_desktop_config.json"

// backupInfix is placed between the config file name and the timestamp in
// backup file names.
const backupInfix = ".golauncher-backup-"

// ErrNotInstalled is returned by Connect when the config file's folder does
// not exist.
var ErrNotInstalled = errors.New("desktop: Claude Desktop is not installed")

// ConfigPath returns the path of claude_desktop_config.json:
// %APPDATA%\Claude\claude_desktop_config.json. If that folder doesn't exist
// but the MSIX-packaged app's virtualized folder does
// (%LOCALAPPDATA%\Packages\Claude_<publisherhash>\LocalCache\Roaming\Claude),
// the path there is returned. When neither exists the %APPDATA% path is
// returned (the file may not exist yet).
func ConfigPath() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		d, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("desktop: locating %%APPDATA%%: %w", err)
		}
		appData = d
	}
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		if d, err := os.UserCacheDir(); err == nil {
			localAppData = d
		}
	}
	return configPath(appData, localAppData), nil
}

// configPath is ConfigPath with the two base folders given.
func configPath(appData, localAppData string) string {
	dir := filepath.Join(appData, "Claude")
	if isDir(dir) || localAppData == "" {
		return filepath.Join(dir, configName)
	}
	matches, _ := filepath.Glob(filepath.Join(localAppData, "Packages", "Claude_*"))
	slices.Sort(matches)
	for _, m := range matches {
		pkgDir := filepath.Join(m, "LocalCache", "Roaming", "Claude")
		if isDir(pkgDir) {
			return filepath.Join(pkgDir, configName)
		}
	}
	return filepath.Join(dir, configName)
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// Entry is the mcpServers.golauncher value written: {"command": exe, "args": ["mcp"]}.
type Entry struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// Change describes what Connect did, for showing the user.
type Change struct {
	Path    string // the config file
	Backup  string // the backup written ("" when the file did not exist yet)
	Created bool   // the file did not exist and was created
	// Previous is the old mcpServers.golauncher value as JSON text ("" when
	// there was none); New the value written (JSON text, indented).
	Previous string
	New      string
	// Unchanged is set when the entry was already exactly this; then nothing
	// is written and no backup made.
	Unchanged bool
}

// Connect adds or updates mcpServers.golauncher in the config at path to run
// `exe mcp`.
//
// When the file exists it is first copied to a backup next to it named
// claude_desktop_config.json.golauncher-backup-<YYYYMMDD-HHMMSS> (from now),
// then the new file is written atomically (temp file in the same folder +
// rename). Everything else in the file keeps its bytes and order. If the file
// is not valid JSON, or its root or mcpServers is not an object, an error is
// returned and nothing is written.
//
// If the folder doesn't exist Connect returns ErrNotInstalled. If the folder
// exists but the file doesn't, the file is created with just the entry.
func Connect(path, exe string, now time.Time) (Change, error) {
	ch := Change{Path: path}
	entry, err := json.Marshal(Entry{Command: exe, Args: []string{"mcp"}})
	if err != nil {
		return ch, fmt.Errorf("desktop: encoding entry: %w", err)
	}
	ch.New = indent(entry, "")

	dir := filepath.Dir(path)
	if !isDir(dir) {
		return ch, ErrNotInstalled
	}
	old, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		doc, _ := json.Marshal(map[string]any{"mcpServers": map[string]jsontext.Value{ServerName: entry}})
		out := []byte(indent(doc, "") + "\n")
		if err := writeAtomic(path, out, 0o644); err != nil {
			return ch, err
		}
		ch.Created = true
		return ch, nil
	case err != nil:
		return ch, fmt.Errorf("desktop: reading config: %w", err)
	}

	out, prev, unchanged, err := splice(old, entry)
	if err != nil {
		return ch, err
	}
	if prev != nil {
		ch.Previous = indent(prev, "")
	}
	if unchanged {
		ch.Unchanged = true
		return ch, nil
	}

	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	backup, err := writeBackup(path, old, now, mode)
	if err != nil {
		return ch, err
	}
	ch.Backup = backup
	if err := writeAtomic(path, out, mode); err != nil {
		return ch, err
	}
	return ch, nil
}

// Status reports whether the config at path has an mcpServers.golauncher
// entry (connected) and whether that entry runs `exe mcp` (sameExe). A missing
// file or folder is reported as false, false, nil.
func Status(path, exe string) (connected, sameExe bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("desktop: reading config: %w", err)
	}
	doc, err := parse(data)
	if err != nil {
		return false, false, err
	}
	m := doc.find(ServerName)
	if m == nil {
		return false, false, nil
	}
	var e Entry
	if err := json.Unmarshal(doc.buf[m.valStart:m.valEnd], &e); err != nil {
		// Present but not something we wrote: connected, but not to this exe.
		return true, false, nil
	}
	return true, samePath(e.Command, exe) && slices.Equal(e.Args, []string{"mcp"}), nil
}

// samePath reports whether a and b name the same file: case-insensitively
// after filepath.Clean on Windows.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// indent formats the JSON value v on multiple lines with two-space indents,
// each line after the first starting with prefix.
func indent(v []byte, prefix string) string {
	out, err := jsontext.AppendFormat(nil, v, jsontext.WithIndent("  "), jsontext.WithIndentPrefix(prefix))
	if err != nil {
		return string(v)
	}
	return string(out)
}

// member is one name/value pair of a JSON object, as offsets into the file.
type member struct {
	name     string
	keyStart int     // offset of the opening quote of the name
	valStart int     // offset of the first byte of the value
	valEnd   int     // offset just past the value
	obj      *object // the parsed value, for mcpServers
}

// object is a JSON object as offsets into the file.
type object struct {
	open, close int // offsets of '{' and '}'
	members     []member
}

// document is a parsed config file: the root object and, if present, its
// mcpServers object.
type document struct {
	buf     []byte // the file without a UTF-8 BOM
	root    *object
	servers *member // the mcpServers member of root, or nil
}

// find returns the member name of mcpServers, or nil.
func (d *document) find(name string) *member {
	if d.servers == nil {
		return nil
	}
	for i := range d.servers.obj.members {
		if d.servers.obj.members[i].name == name {
			return &d.servers.obj.members[i]
		}
	}
	return nil
}

var bom = []byte("\xef\xbb\xbf")

// parse parses a config file, requiring the root and mcpServers (if present)
// to be objects. Duplicate member names are rejected.
func parse(data []byte) (*document, error) {
	buf := bytes.TrimPrefix(data, bom)
	dec := jsontext.NewDecoder(bytes.NewReader(buf))
	if dec.PeekKind() != '{' {
		if _, err := dec.ReadToken(); err != nil {
			return nil, fmt.Errorf("desktop: invalid JSON in config: %w", err)
		}
		return nil, errors.New("desktop: config root is not a JSON object")
	}
	doc := &document{buf: buf}
	root, err := readObject(dec, buf, func(name string) (bool, error) {
		if name != "mcpServers" {
			return false, nil
		}
		if dec.PeekKind() != '{' {
			if _, err := dec.ReadValue(); err != nil {
				return false, err
			}
			return false, errors.New("desktop: mcpServers in config is not a JSON object")
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	if _, err := dec.ReadToken(); err != io.EOF {
		if err == nil {
			err = errors.New("unexpected data after the root object")
		}
		return nil, fmt.Errorf("desktop: invalid JSON in config: %w", err)
	}
	doc.root = root
	for i := range root.members {
		if root.members[i].obj != nil {
			doc.servers = &root.members[i]
		}
	}
	return doc, nil
}

// readObject reads a JSON object from dec, whose input is buf. descend is
// called with each member name before its value is read; when it returns true
// the value (an object) is parsed into member.obj without descending further.
func readObject(dec *jsontext.Decoder, buf []byte, descend func(string) (bool, error)) (*object, error) {
	wrap := func(err error) error {
		var se *jsontext.SyntacticError
		if errors.As(err, &se) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return fmt.Errorf("desktop: invalid JSON in config: %w", err)
		}
		return err
	}
	if _, err := dec.ReadToken(); err != nil { // '{'
		return nil, wrap(err)
	}
	obj := &object{open: int(dec.InputOffset()) - 1}
	for dec.PeekKind() != '}' {
		prev := int(dec.InputOffset())
		tok, err := dec.ReadToken()
		if err != nil {
			return nil, wrap(err)
		}
		m := member{name: tok.String(), keyStart: skip(buf, prev, " \t\r\n,")}
		m.valStart = skip(buf, int(dec.InputOffset()), " \t\r\n:")
		down := false
		if descend != nil {
			if down, err = descend(m.name); err != nil {
				return nil, wrap(err)
			}
		}
		if down {
			if m.obj, err = readObject(dec, buf, nil); err != nil {
				return nil, err
			}
		} else if err := dec.SkipValue(); err != nil {
			return nil, wrap(err)
		}
		m.valEnd = int(dec.InputOffset())
		obj.members = append(obj.members, m)
	}
	if _, err := dec.ReadToken(); err != nil { // '}'
		return nil, wrap(err)
	}
	obj.close = int(dec.InputOffset()) - 1
	return obj, nil
}

// skip returns the first offset at or after i whose byte is not in set.
func skip(buf []byte, i int, set string) int {
	for i < len(buf) && strings.IndexByte(set, buf[i]) >= 0 {
		i++
	}
	return i
}

// splice returns data with mcpServers.golauncher set to entry (compact JSON),
// the previous value (nil if none) and whether it was already equal.
func splice(data, entry []byte) (out, prev []byte, unchanged bool, err error) {
	doc, err := parse(data)
	if err != nil {
		return nil, nil, false, err
	}
	buf := doc.buf
	nl := "\n"
	if bytes.Contains(buf, []byte("\r\n")) {
		nl = "\r\n"
	}

	var start, end int
	var text string
	switch m := doc.find(ServerName); {
	case m != nil:
		prev = slices.Clone(buf[m.valStart:m.valEnd])
		if equalJSON(prev, entry) {
			return nil, prev, true, nil
		}
		start, end = m.valStart, m.valEnd
		if ind, own := lineIndent(buf, m.keyStart); own {
			text = indent(entry, ind)
		} else {
			text = string(entry)
		}
	case doc.servers != nil:
		start, end, text = insert(buf, doc.servers.obj, doc.servers.keyStart, ServerName, entry)
	default:
		servers, _ := json.Marshal(map[string]jsontext.Value{ServerName: entry})
		start, end, text = insert(buf, doc.root, doc.root.open, "mcpServers", servers)
	}
	if nl != "\n" {
		text = strings.ReplaceAll(text, "\n", nl)
	}

	out = make([]byte, 0, len(data)+len(text))
	out = append(out, data[:len(data)-len(buf)]...) // the BOM, if any
	out = append(out, buf[:start]...)
	out = append(out, text...)
	out = append(out, buf[end:]...)

	// Sanity check: the result must parse and carry exactly the entry.
	check, err := parse(out)
	if err != nil {
		return nil, nil, false, fmt.Errorf("desktop: internal error editing config: %w", err)
	}
	if m := check.find(ServerName); m == nil || !equalJSON(check.buf[m.valStart:m.valEnd], entry) {
		return nil, nil, false, errors.New("desktop: internal error editing config: entry not written")
	}
	return out, prev, false, nil
}

// insert returns the replacement of buf[start:end] that adds the member
// name: value to obj. ownerStart is the offset where obj's own line content
// starts (its key, or the root's '{'), used to work out indentation.
func insert(buf []byte, obj *object, ownerStart int, name string, value []byte) (start, end int, text string) {
	key, _ := jsontext.AppendQuote(nil, name)
	ownerIndent, _ := lineIndent(buf, ownerStart)
	if len(obj.members) == 0 {
		// Replace the whole empty object (it may hold whitespace).
		member, _ := json.Marshal(map[string]jsontext.Value{name: value})
		return obj.open, obj.close + 1, indent(member, ownerIndent)
	}
	last := obj.members[len(obj.members)-1]
	ind, own := lineIndent(buf, last.keyStart)
	if !own {
		// A single-line object: stay on one line.
		return last.valEnd, last.valEnd, ", " + string(key) + ": " + string(value)
	}
	unit := "  "
	if rest, ok := strings.CutPrefix(ind, ownerIndent); ok && rest != "" {
		unit = rest
	}
	v, err := jsontext.AppendFormat(nil, value, jsontext.WithIndent(unit), jsontext.WithIndentPrefix(ind))
	if err != nil {
		v = value
	}
	return last.valEnd, last.valEnd, ",\n" + ind + string(key) + ": " + string(v)
}

// lineIndent returns the whitespace at the start of the line containing
// offset i, and whether only whitespace precedes i on that line.
func lineIndent(buf []byte, i int) (indent string, own bool) {
	ls := bytes.LastIndexByte(buf[:i], '\n') + 1
	j := skip(buf, ls, " \t")
	return string(buf[ls:j]), j == i
}

// equalJSON reports whether a and b are the same JSON value, ignoring
// formatting and member order.
func equalJSON(a, b []byte) bool {
	ca, cb := jsontext.Value(slices.Clone(a)), jsontext.Value(slices.Clone(b))
	if ca.Canonicalize() != nil || cb.Canonicalize() != nil {
		return false
	}
	return bytes.Equal(ca, cb)
}

// writeBackup copies data to a new backup file next to path and returns its
// path. An existing backup is never overwritten.
func writeBackup(path string, data []byte, now time.Time, mode fs.FileMode) (string, error) {
	base := path + backupInfix + now.Format("20060102-150405")
	for i := 0; ; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if errors.Is(err, fs.ErrExist) && i < 100 {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("desktop: writing backup: %w", err)
		}
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(name)
			return "", fmt.Errorf("desktop: writing backup: %w", err)
		}
		return name, nil
	}
}

// writeAtomic writes data to path through a temp file in the same folder
// and a rename.
func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("desktop: writing config: %w", err)
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, mode)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("desktop: writing config: %w", err)
	}
	return nil
}
