// Package everything searches files through a running instance of
// voidtools Everything (https://www.voidtools.com).
//
// It speaks Everything's IPC protocol (everything_ipc.h, Everything 1.4.1 and
// later, including 1.5 alpha) directly: a EVERYTHING_IPC_QUERY2 request is
// sent with WM_COPYDATA to Everything's taskbar notification window and the
// EVERYTHING_IPC_LIST2 reply comes back to a message-only window. Nothing of
// Everything is bundled: no Everything64.dll, no es.exe, no cgo.
//
// On other platforms the functions return errors.ErrUnsupported or report
// that Everything is not installed.
package everything

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"
)

// ErrNotRunning reports that no Everything IPC window was found.
var ErrNotRunning = errors.New("everything: Everything is not running")

// Result is a file or folder found by Everything.
type Result struct {
	// Name is the file name, e.g. "notepad.exe". For a drive it is "C:".
	Name string `json:"name"`
	// Path is the full path including Name, e.g. `C:\Windows\notepad.exe`.
	Path     string `json:"path"`
	IsFolder bool   `json:"isFolder,omitzero"`
	// Size in bytes; -1 when unknown (e.g. folder sizes not indexed).
	Size int64 `json:"size"`
	// Modified is the last write time; zero when unknown.
	Modified time.Time `json:"modified,omitzero"`
}

// SortOrder is the order of the results.
type SortOrder uint32

// Sort orders (EVERYTHING_IPC_SORT_*). Sorting by name ascending is always
// fast; other orders are fast only when indexed in Everything's options.
const (
	SortNameAscending          SortOrder = 1
	SortNameDescending         SortOrder = 2
	SortPathAscending          SortOrder = 3
	SortPathDescending         SortOrder = 4
	SortSizeAscending          SortOrder = 5
	SortSizeDescending         SortOrder = 6
	SortDateModifiedAscending  SortOrder = 13
	SortDateModifiedDescending SortOrder = 14
	SortRunCountDescending     SortOrder = 20
)

// Query is a search request.
type Query struct {
	// Text uses Everything's search syntax.
	Text string `json:"text"`
	// Max is the maximum number of results; <= 0 means 100.
	Max    int `json:"max,omitzero"`
	Offset int `json:"offset,omitzero"`
	// Sort defaults to SortNameAscending.
	Sort      SortOrder `json:"sort,omitzero"`
	MatchPath bool      `json:"matchPath,omitzero"`
	MatchCase bool      `json:"matchCase,omitzero"`
	WholeWord bool      `json:"wholeWord,omitzero"`
	Regex     bool      `json:"regex,omitzero"`
}

// DefaultMax is the number of results returned when Query.Max is not set.
const DefaultMax = 100

// IPC constants from everything_ipc.h.
const (
	copyDataQuery2W = 18 // EVERYTHING_IPC_COPYDATA_QUERY2W

	flagMatchCase = 0x01
	flagWholeWord = 0x02
	flagMatchPath = 0x04
	flagRegex     = 0x08

	reqName         = 0x01
	reqPath         = 0x02
	reqFullPath     = 0x04
	reqExtension    = 0x08
	reqSize         = 0x10
	reqDateCreated  = 0x20
	reqDateModified = 0x40

	itemFolder = 0x01

	query2Header = 7 * 4 // EVERYTHING_IPC_QUERY2 without the search string
	list2Header  = 5 * 4 // EVERYTHING_IPC_LIST2
	item2Size    = 2 * 4 // EVERYTHING_IPC_ITEM2
)

// requestFlags are the fields asked for in every query.
const requestFlags = reqName | reqPath | reqSize | reqDateModified

// encodeQuery builds a packed EVERYTHING_IPC_QUERY2 followed by the
// NUL-terminated UTF-16 search string. All fields are DWORDs (#pragma pack 1),
// so the layout is the same for every architecture.
func encodeQuery(q Query, replyHwnd, replyID uint32) []byte {
	var flags uint32
	if q.MatchCase {
		flags |= flagMatchCase
	}
	if q.WholeWord {
		flags |= flagWholeWord
	}
	if q.MatchPath {
		flags |= flagMatchPath
	}
	if q.Regex {
		flags |= flagRegex
	}
	maxResults := q.Max
	if maxResults <= 0 {
		maxResults = DefaultMax
	}
	sort := q.Sort
	if sort == 0 {
		sort = SortNameAscending
	}
	text := utf16.Encode([]rune(q.Text))
	b := make([]byte, 0, query2Header+2*(len(text)+1))
	for _, v := range []uint32{replyHwnd, replyID, flags, uint32(max(q.Offset, 0)), uint32(maxResults), requestFlags, uint32(sort)} {
		b = binary.LittleEndian.AppendUint32(b, v)
	}
	for _, c := range text {
		b = binary.LittleEndian.AppendUint16(b, c)
	}
	return binary.LittleEndian.AppendUint16(b, 0)
}

var errShort = errors.New("everything: truncated reply")

// parseList2 decodes an EVERYTHING_IPC_LIST2 reply to a Unicode query. The
// fields of each item follow in the order of the request flag bits; only the
// flags actually set in the reply are present.
func parseList2(b []byte) ([]Result, error) {
	if len(b) < list2Header {
		return nil, errShort
	}
	le := binary.LittleEndian
	num := int(le.Uint32(b[4:]))
	flags := le.Uint32(b[12:])
	if num < 0 || num > (len(b)-list2Header)/item2Size {
		return nil, fmt.Errorf("everything: bad item count %d in a %d byte reply", num, len(b))
	}
	out := make([]Result, 0, num)
	for i := range num {
		it := b[list2Header+i*item2Size:]
		r := Result{IsFolder: le.Uint32(it)&itemFolder != 0, Size: -1}
		d := &reader{b: b, off: int(le.Uint32(it[4:]))}
		var dir string
		if flags&reqName != 0 {
			r.Name = d.str()
		}
		if flags&reqPath != 0 {
			dir = d.str()
		}
		if flags&reqFullPath != 0 {
			r.Path = d.str()
		}
		if flags&reqExtension != 0 {
			d.str()
		}
		if flags&reqSize != 0 {
			if v := int64(d.u64()); v >= 0 {
				r.Size = v
			}
		}
		if flags&reqDateCreated != 0 {
			d.u64()
		}
		if flags&reqDateModified != 0 {
			r.Modified = filetime(d.u64())
		}
		if d.err != nil {
			return nil, fmt.Errorf("everything: item %d: %w", i, d.err)
		}
		if r.Path == "" {
			r.Path = JoinPath(dir, r.Name)
		}
		out = append(out, r)
	}
	return out, nil
}

// reader reads little-endian fields at an offset of a reply, recording the
// first out-of-bounds read.
type reader struct {
	b   []byte
	off int
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil || r.off < 0 || n < 0 || r.off+n > len(r.b) {
		r.err = errShort
		return nil
	}
	s := r.b[r.off : r.off+n]
	r.off += n
	return s
}

func (r *reader) u64() uint64 {
	if s := r.take(8); s != nil {
		return binary.LittleEndian.Uint64(s)
	}
	return 0
}

// str reads a DWORD length in characters followed by that many UTF-16 code
// units and a NUL terminator.
func (r *reader) str() string {
	s := r.take(4)
	if s == nil {
		return ""
	}
	n := int(binary.LittleEndian.Uint32(s))
	if n > len(r.b) {
		r.err = errShort
		return ""
	}
	s = r.take(2 * (n + 1))
	if s == nil {
		return ""
	}
	u := make([]uint16, n)
	for i := range n {
		u[i] = binary.LittleEndian.Uint16(s[2*i:])
	}
	return string(utf16.Decode(u))
}

// FILETIME epoch (1601-01-01) to Unix epoch, in 100 ns intervals.
const filetimeUnixOffset = 116444736000000000

// filetime converts a FILETIME; 0 and all-ones mean unknown.
func filetime(ft uint64) time.Time {
	if ft == 0 || ft == ^uint64(0) || ft < filetimeUnixOffset {
		return time.Time{}
	}
	d := ft - filetimeUnixOffset
	return time.Unix(int64(d/1e7), int64(d%1e7)*100)
}

// JoinPath joins a Windows folder and a name: JoinPath(`C:\Windows`,
// "notepad.exe") is `C:\Windows\notepad.exe`. A drive with an empty folder
// becomes its root, `C:\`.
func JoinPath(dir, name string) string {
	switch {
	case dir == "" && strings.HasSuffix(name, ":"):
		return name + `\`
	case dir == "":
		return name
	case strings.HasSuffix(dir, `\`):
		return dir + name
	}
	return dir + `\` + name
}
