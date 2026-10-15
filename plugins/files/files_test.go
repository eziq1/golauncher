package files

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// which finds the programs in found.
func which(found ...string) func(string) string {
	return func(name string) string {
		if slices.Contains(found, name) {
			return name + ".exe"
		}
		return ""
	}
}

func newPlugin(t *testing.T, found ...string) (*Plugin, *hostfake.Host) {
	t.Helper()
	p := NewWithOptions(Options{Which: which(found...)})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p, h
}

func ids(items []plugin.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func find(t *testing.T, items []plugin.Item, id string) plugin.Item {
	t.Helper()
	i := slices.IndexFunc(items, func(it plugin.Item) bool { return it.ID == id })
	if i < 0 {
		t.Fatalf("no item %q in %v", id, ids(items))
	}
	return items[i]
}

func openOpts(t *testing.T, a plugin.Action) plugin.OpenOptions {
	t.Helper()
	var o plugin.OpenOptions
	if len(a.Do.Payload) > 0 {
		if err := json.Unmarshal(a.Do.Payload, &o); err != nil {
			t.Fatal(err)
		}
	}
	return o
}

func folderCtx(dir string) plugin.Context {
	return plugin.Context{Kind: plugin.ContextExplorer, Folder: dir, App: plugin.AppInfo{Name: "explorer.exe"}}
}

func TestItemsForKinds(t *testing.T) {
	p, _ := newPlugin(t, "wt", "code")
	dir := t.TempDir()
	for _, tt := range []struct {
		c    plugin.Context
		want int
	}{
		{plugin.Context{App: plugin.AppInfo{Name: "notepad.exe"}, Title: "Untitled"}, 0}, // no detector matched
		{plugin.Context{Kind: plugin.ContextExplorer}, 0},                                // a virtual folder
		{plugin.Context{Kind: "browser", Folder: dir}, 0},                                // not a file context
		{plugin.Context{Kind: plugin.ContextExplorer, Folder: filepath.Join(dir, "missing")}, 0},
		{folderCtx(dir), 7},
		{plugin.Context{Kind: plugin.ContextDesktop, Folder: dir}, 7},
		{plugin.Context{Kind: plugin.ContextPathInTitle, Folder: dir}, 7},
	} {
		items, err := p.ContextItems(t.Context(), tt.c)
		if err != nil || len(items) != tt.want {
			t.Errorf("%+v: %v (%v), want %d items", tt.c, ids(items), err, tt.want)
		}
	}
}

func TestFolderItems(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "My Project")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p, _ := newPlugin(t, "wt", "code")
	items, _ := p.ContextItems(t.Context(), folderCtx(dir))
	want := []string{"terminal", "cmd", "vscode", "new-folder", "new-file", "copy-path", "terminal-admin"}
	if got := ids(items); !slices.Equal(got[:len(want)], want) {
		t.Fatalf("items %v, want %v", got, want)
	}
	term := find(t, items, "terminal")
	if a := term.Actions[0]; a.Do.Target != "wt.exe" || openOpts(t, a).Args != `-d "`+dir+`"` {
		t.Errorf("terminal: %s %+v", a.Do.Target, openOpts(t, a))
	}
	admin := find(t, items, "terminal-admin").Actions[0]
	if o := openOpts(t, admin); o.Verb != "runas" || admin.Style != plugin.StyleDestructive || o.Args != `-d "`+dir+`"` {
		t.Errorf("admin terminal: %+v %s", o, admin.Style)
	}
	if o := openOpts(t, find(t, items, "cmd").Actions[0]); o.Dir != dir {
		t.Errorf("cmd: %+v", o)
	}
	if a := find(t, items, "vscode").Actions[0]; a.Do.Target != "code.exe" || openOpts(t, a).Args != `"`+dir+`"` {
		t.Errorf("vscode: %s %+v", a.Do.Target, openOpts(t, a))
	}
	if a := find(t, items, "copy-path").Actions[0]; a.Do.Kind != plugin.ActionCopyText || a.Do.Target != dir {
		t.Errorf("copy path: %+v", a.Do)
	}
	nf := find(t, items, "new-folder")
	if len(nf.Arguments) != 1 || nf.Arguments[0].Placeholder != "New folder" || nf.Arguments[0].Required {
		t.Errorf("new folder arguments %+v", nf.Arguments)
	}
}

func TestTerminalFallbacks(t *testing.T) {
	dir := t.TempDir()
	p, _ := newPlugin(t, "powershell")
	items, _ := p.ContextItems(t.Context(), folderCtx(dir))
	term := find(t, items, "terminal")
	if term.Title != "Open PowerShell Here" || term.Actions[0].Do.Target != "powershell.exe" || openOpts(t, term.Actions[0]).Dir != dir {
		t.Errorf("terminal without wt: %+v", term)
	}
	admin := find(t, items, "terminal-admin").Actions[0]
	if o := openOpts(t, admin); o.Verb != "runas" || !strings.Contains(o.Args, "Set-Location -LiteralPath '"+dir+"'") {
		t.Errorf("admin PowerShell: %+v", o)
	}
	if slices.Contains(ids(items), "vscode") {
		t.Error("VS Code offered although missing")
	}

	p, _ = newPlugin(t, "pwsh", "powershell")
	items, _ = p.ContextItems(t.Context(), folderCtx(dir))
	if got := find(t, items, "terminal").Actions[0].Do.Target; got != "pwsh.exe" {
		t.Errorf("terminal %q, want PowerShell 7", got)
	}

	p, _ = newPlugin(t)
	items, _ = p.ContextItems(t.Context(), folderCtx(dir))
	if slices.Contains(ids(items), "terminal") || slices.Contains(ids(items), "terminal-admin") {
		t.Errorf("a terminal without any: %v", ids(items))
	}
}

func TestSelectionItems(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "setup.exe")
	doc := filepath.Join(dir, "notes.txt")
	sub := filepath.Join(dir, "sub")
	os.WriteFile(file, []byte("MZ"), 0o644)
	os.WriteFile(doc, []byte("x"), 0o644)
	os.Mkdir(sub, 0o755)
	p, _ := newPlugin(t, "code")
	sel := func(paths ...string) []plugin.Item {
		c := folderCtx(dir)
		c.Selected = paths
		items, _ := p.ContextItems(t.Context(), c)
		return items
	}
	items := sel(file)
	want := []string{"copy-selected", "vscode-selected", "zip", "sha256", "run-admin", "cmd"}
	if got := ids(items); !slices.Equal(got[:len(want)], want) {
		t.Errorf("one program: %v, want %v first", got, want)
	}
	if ra := find(t, items, "run-admin").Actions[0]; ra.Do.Target != file || openOpts(t, ra).Verb != "runas" {
		t.Errorf("run as admin: %+v", ra.Do)
	}
	items = sel(doc)
	if slices.Contains(ids(items), "run-admin") || !slices.Contains(ids(items), "sha256") {
		t.Errorf("one document: %v", ids(items))
	}
	items = sel(sub)
	if slices.Contains(ids(items), "sha256") {
		t.Errorf("a folder has no checksum: %v", ids(items))
	}
	items = sel(doc, file)
	cs := find(t, items, "copy-selected")
	if cs.Title != "Copy 2 Paths" || cs.Subtitle != "2 selected items" || cs.Actions[0].Do.Target != doc+"\r\n"+file {
		t.Errorf("copy two: %+v", cs)
	}
	if slices.Contains(ids(items), "sha256") {
		t.Error("checksum for two files")
	}
	if a := find(t, items, "vscode-selected").Actions[0]; openOpts(t, a).Args != quoteArgs(doc, file) {
		t.Errorf("vscode args %q", openOpts(t, a).Args)
	}
}

func values(kv ...string) map[string]jsontext.Value {
	m := map[string]jsontext.Value{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = plugin.MustPayload(kv[i+1])
	}
	return m
}

func run(t *testing.T, p *Plugin, target string, pl payload, v map[string]jsontext.Value) plugin.Result {
	t.Helper()
	r, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: target, Payload: plugin.MustPayload(pl), Values: v})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNewFolderAndFile(t *testing.T) {
	dir := t.TempDir()
	p, _ := newPlugin(t)
	for range 3 {
		r := run(t, p, cbNewFolder, payload{Dir: dir}, nil)
		if r.Toast == nil || r.Toast.Style != plugin.ToastSuccess || !r.Hide {
			t.Fatalf("result %+v", r)
		}
	}
	run(t, p, cbNewFolder, payload{Dir: dir}, values(argName, "  docs  "))
	run(t, p, cbNewFile, payload{Dir: dir}, nil)
	run(t, p, cbNewFile, payload{Dir: dir}, nil)
	run(t, p, cbNewFile, payload{Dir: dir}, values(argName, "todo"))
	run(t, p, cbNewFile, payload{Dir: dir}, values(argName, "todo"))
	run(t, p, cbNewFile, payload{Dir: dir}, values(argName, "README.md"))
	entries, _ := os.ReadDir(dir)
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	sort.Strings(got)
	want := []string{"New Text Document (2).txt", "New Text Document.txt", "New folder", "New folder (2)", "New folder (3)",
		"README.md", "docs", "todo (2).txt", "todo.txt"}
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Errorf("created %q, want %q", got, want)
	}
	if st, err := os.Stat(filepath.Join(dir, "New folder (2)")); err != nil || !st.IsDir() {
		t.Error("New folder (2) is not a folder")
	}

	for _, bad := range []string{`a\b`, "../x", "con", "CON.txt", "what?", "trailing.", "x:y"} {
		r := run(t, p, cbNewFolder, payload{Dir: dir}, values(argName, bad))
		if r.Toast == nil || r.Toast.Style != plugin.ToastFailure || r.Hide {
			t.Errorf("name %q: %+v", bad, r)
		}
	}
	r := run(t, p, cbNewFolder, payload{Dir: filepath.Join(dir, "missing")}, values(argName, "x"))
	if r.Toast == nil || r.Toast.Style != plugin.ToastFailure {
		t.Errorf("missing folder: %+v", r)
	}
}

func TestZip(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "report.txt"), []byte("hello"), 0o644)
	os.MkdirAll(filepath.Join(dir, "data", "deep"), 0o755)
	os.WriteFile(filepath.Join(dir, "data", "deep", "a.csv"), []byte("1,2"), 0o644)
	p, h := newPlugin(t)
	sel := []string{filepath.Join(dir, "report.txt"), filepath.Join(dir, "data")}
	for _, want := range []string{"report.zip", "report (2).zip"} {
		// The action returns at once; the archive is made by a background task.
		r := run(t, p, cbZip, payload{Dir: dir, Paths: sel}, nil)
		if r.Toast == nil || r.Toast.Style != plugin.ToastInfo || !r.Hide {
			t.Fatalf("result %+v", r)
		}
		h.WaitTasks()
		last := h.Tasks.List()[0]
		if last.State != plugin.TaskSucceeded || last.Title != "Compress 2 items" || last.Notify != plugin.NotifyAlways {
			t.Fatalf("task %+v", last)
		}
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Fatalf("%s not created: %v", want, err)
		}
	}
	zr, err := zip.OpenReader(filepath.Join(dir, "report.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if want := []string{"report.txt", "data/", "data/deep/", "data/deep/a.csv"}; !slices.Equal(names, want) {
		t.Errorf("archive holds %q, want %q", names, want)
	}
	rc, _ := zr.File[0].Open()
	b := make([]byte, 5)
	rc.Read(b)
	rc.Close()
	if string(b) != "hello" {
		t.Errorf("content %q", b)
	}

	// A folder is named after itself, and a failure leaves no archive.
	run(t, p, cbZip, payload{Dir: dir, Paths: []string{filepath.Join(dir, "data"), filepath.Join(dir, "missing")}}, nil)
	h.WaitTasks()
	if last := h.Tasks.List()[0]; last.State != plugin.TaskFailed || !strings.Contains(last.Error, "Could not create the ZIP file") {
		t.Errorf("missing file: task %+v", last)
	}
	if _, err := os.Stat(filepath.Join(dir, "data.zip")); err == nil {
		t.Error("a failed compression left data.zip behind")
	}
}

func TestSHA256(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.bin")
	os.WriteFile(file, []byte("golauncher"), 0o644)
	p, h := newPlugin(t)
	r := run(t, p, cbSHA256, payload{Paths: []string{file}}, nil)
	sum := sha256.Sum256([]byte("golauncher"))
	want := hex.EncodeToString(sum[:])
	if r.Toast == nil || r.Toast.Message != want {
		t.Fatalf("result %+v", r.Toast)
	}
	if c, _ := h.Clip.Read(); c.Text != want {
		t.Errorf("clipboard %q, want the checksum", c.Text)
	}
	r = run(t, p, cbSHA256, payload{Paths: []string{filepath.Join(dir, "missing")}}, nil)
	if r.Toast == nil || r.Toast.Style != plugin.ToastFailure {
		t.Errorf("missing file: %+v", r.Toast)
	}
}

func TestQuoteArg(t *testing.T) {
	for in, want := range map[string]string{
		`C:\work`:     `C:\work`,
		`C:\My Files`: `"C:\My Files"`,
		`C:\`:         `C:\`,
		`D:\a b\`:     `"D:\a b\\"`,
		``:            `""`,
		`say "hi"`:    `"say \"hi\""`,
		`C:\x y\"q`:   `"C:\x y\\\"q"`,
		`D:\项目\季度 报告`: `"D:\项目\季度 报告"`,
	} {
		if got := quoteArg(in); got != want {
			t.Errorf("quoteArg(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestValidName(t *testing.T) {
	for name, want := range map[string]bool{
		"New folder": true, "报告.docx": true, ".gitignore": true, "a..b": true,
		"": false, ".": false, "..": false, "a/b": false, `a\b`: false, "a:b": false, "a*": false,
		"nul": false, "COM1.txt": false, "lpt9": false, "com10": true, "dot.": false, "space ": false, "tab\tx": false,
	} {
		if got := validName(name); got != want {
			t.Errorf("validName(%q) = %v, want %v", name, got, want)
		}
	}
}
