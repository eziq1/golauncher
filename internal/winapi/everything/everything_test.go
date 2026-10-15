package everything

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"
	"unicode/utf16"
)

// list2 builds an EVERYTHING_IPC_LIST2 reply as Everything would send it.
type list2 struct {
	flags uint32
	items []item2
}

type item2 struct {
	flags                 uint32
	name, path, full, ext string
	size, created, mod    uint64
}

func (l list2) bytes() []byte {
	le := binary.LittleEndian
	head := make([]byte, 0, list2Header+item2Size*len(l.items))
	for _, v := range []uint32{uint32(len(l.items)) + 10, uint32(len(l.items)), 0, l.flags, uint32(SortNameAscending)} {
		head = le.AppendUint32(head, v)
	}
	var data []byte
	base := list2Header + item2Size*len(l.items)
	str := func(s string) {
		u := utf16.Encode([]rune(s))
		data = le.AppendUint32(data, uint32(len(u)))
		for _, c := range u {
			data = le.AppendUint16(data, c)
		}
		data = le.AppendUint16(data, 0)
	}
	for _, it := range l.items {
		head = le.AppendUint32(head, it.flags)
		head = le.AppendUint32(head, uint32(base+len(data)))
		if l.flags&reqName != 0 {
			str(it.name)
		}
		if l.flags&reqPath != 0 {
			str(it.path)
		}
		if l.flags&reqFullPath != 0 {
			str(it.full)
		}
		if l.flags&reqExtension != 0 {
			str(it.ext)
		}
		if l.flags&reqSize != 0 {
			data = le.AppendUint64(data, it.size)
		}
		if l.flags&reqDateCreated != 0 {
			data = le.AppendUint64(data, it.created)
		}
		if l.flags&reqDateModified != 0 {
			data = le.AppendUint64(data, it.mod)
		}
	}
	return append(head, data...)
}

func toFiletime(t time.Time) uint64 { return uint64(t.UnixNano()/100) + filetimeUnixOffset }

func TestParseList2(t *testing.T) {
	mod := time.Date(2026, 9, 1, 8, 30, 15, 0, time.UTC)
	reply := list2{flags: requestFlags, items: []item2{
		{name: "notepad.exe", path: `C:\Windows`, size: 360448, mod: toFiletime(mod)},
		{flags: itemFolder, name: "项目文档", path: `D:\工作`, size: ^uint64(0), mod: 0},
		{flags: itemFolder | 0x02, name: "C:", path: "", size: ^uint64(0)},
		{name: "😀.txt", path: `\\server\share\`, size: 0, mod: ^uint64(0)},
	}}.bytes()
	got, err := parseList2(reply)
	if err != nil {
		t.Fatal(err)
	}
	want := []Result{
		{Name: "notepad.exe", Path: `C:\Windows\notepad.exe`, Size: 360448, Modified: mod},
		{Name: "项目文档", Path: `D:\工作\项目文档`, IsFolder: true, Size: -1},
		{Name: "C:", Path: `C:\`, IsFolder: true, Size: -1},
		{Name: "😀.txt", Path: `\\server\share\😀.txt`, Size: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if g, w := got[i], want[i]; g.Name != w.Name || g.Path != w.Path || g.IsFolder != w.IsFolder || g.Size != w.Size || !g.Modified.Equal(w.Modified) {
			t.Errorf("result %d = %+v, want %+v", i, g, w)
		}
	}
}

// Everything may answer with more fields than asked for; they are skipped.
func TestParseList2ExtraFields(t *testing.T) {
	mod := time.Date(2025, 1, 2, 3, 4, 5, 600, time.UTC)
	reply := list2{flags: reqName | reqPath | reqFullPath | reqExtension | reqSize | reqDateCreated | reqDateModified, items: []item2{
		{name: "a.go", path: `C:\src`, full: `C:\src\a.go`, ext: "go", size: 42, created: toFiletime(mod.Add(-time.Hour)), mod: toFiletime(mod)},
	}}.bytes()
	got, err := parseList2(reply)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != `C:\src\a.go` || got[0].Size != 42 || !got[0].Modified.Equal(mod.Truncate(100)) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseList2Empty(t *testing.T) {
	got, err := parseList2(list2{flags: requestFlags}.bytes())
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestParseList2Malformed(t *testing.T) {
	good := list2{flags: requestFlags, items: []item2{{name: "a.txt", path: `C:\x`, size: 1}}}.bytes()
	bad := map[string][]byte{
		"short header": good[:10],
		"too many":     append(binary.LittleEndian.AppendUint32(append([]byte{}, good[:4]...), 1000), good[8:]...),
		"cut data":     good[:len(good)-3],
		"bad offset": func() []byte {
			b := append([]byte{}, good...)
			binary.LittleEndian.PutUint32(b[list2Header+4:], 1<<30)
			return b
		}(),
		"huge string": func() []byte {
			b := append([]byte{}, good...)
			binary.LittleEndian.PutUint32(b[list2Header+item2Size:], 1<<31)
			return b
		}(),
	}
	for name, b := range bad {
		if _, err := parseList2(b); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestEncodeQuery(t *testing.T) {
	b := encodeQuery(Query{Text: "文件 *.go", Offset: 5, MatchPath: true, MatchCase: true, WholeWord: true, Regex: true}, 0x1234, 77)
	le := binary.LittleEndian
	want := []uint32{0x1234, 77, flagMatchCase | flagWholeWord | flagMatchPath | flagRegex, 5, DefaultMax, requestFlags, uint32(SortNameAscending)}
	for i, w := range want {
		if g := le.Uint32(b[4*i:]); g != w {
			t.Errorf("field %d = %#x, want %#x", i, g, w)
		}
	}
	text := b[query2Header:]
	u := make([]uint16, len(text)/2)
	for i := range u {
		u[i] = le.Uint16(text[2*i:])
	}
	if u[len(u)-1] != 0 || string(utf16.Decode(u[:len(u)-1])) != "文件 *.go" {
		t.Errorf("search string = %v", u)
	}

	b = encodeQuery(Query{Text: "", Max: 7, Offset: -1, Sort: SortDateModifiedDescending}, 1, 2)
	if le.Uint32(b[8:]) != 0 || le.Uint32(b[12:]) != 0 || le.Uint32(b[16:]) != 7 || le.Uint32(b[24:]) != uint32(SortDateModifiedDescending) || len(b) != query2Header+2 {
		t.Errorf("encodeQuery = %x", b)
	}
}

func TestJoinPath(t *testing.T) {
	for _, c := range []struct{ dir, name, want string }{
		{`C:\Windows`, "a.exe", `C:\Windows\a.exe`},
		{`C:\`, "a.exe", `C:\a.exe`},
		{"", "D:", `D:\`},
		{"", "x", "x"},
	} {
		if got := JoinPath(c.dir, c.name); got != c.want {
			t.Errorf("JoinPath(%q, %q) = %q, want %q", c.dir, c.name, got, c.want)
		}
	}
}

func TestFiletime(t *testing.T) {
	if !filetime(0).IsZero() || !filetime(^uint64(0)).IsZero() || !filetime(1).IsZero() {
		t.Error("unknown times are not zero")
	}
	want := time.Date(2020, 2, 29, 12, 0, 0, 0, time.UTC)
	if got := filetime(toFiletime(want)); !got.Equal(want) {
		t.Errorf("filetime = %v, want %v", got, want)
	}
}

func TestErrNotRunning(t *testing.T) {
	if !errors.Is(ErrNotRunning, ErrNotRunning) || ErrNotRunning.Error() == "" {
		t.Fatal("bad ErrNotRunning")
	}
}
