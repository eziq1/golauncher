package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

const (
	root   = "../.."
	module = "github.com/eziq1/golauncher"
	// allowMarker on a line exempts a literal from the i18n check.
	allowMarker = "lint:allow-literal"
)

// goFiles parses the non-test Go files under dir (relative to the repo root).
func goFiles(t *testing.T, dir string) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		files[path] = f
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fset, files
}

// TestPluginImports enforces that built-in plugins use only the public SDK
// and platform helpers, never the host's internals (design §5.1).
func TestPluginImports(t *testing.T) {
	allowedModule := []string{
		module + "/pkg/",
		module + "/internal/winapi/",
	}
	allowedThirdParty := []string{"golang.org/x/", "modernc.org/sqlite", "github.com/mozillazg/go-pinyin"}
	fset, files := goFiles(t, "plugins")
	if len(files) == 0 {
		t.Fatal("no plugin sources found")
	}
	for path, f := range files {
		// A plugin may import its own subpackages (plugins/calc/units).
		rel, _ := filepath.Rel(filepath.Join(root, "plugins"), path)
		own, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
		allowed := append(slices.Clone(allowedModule), module+"/plugins/"+own+"/")
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			first, _, _ := strings.Cut(p, "/")
			switch {
			case !strings.Contains(first, "."): // standard library
			case strings.HasPrefix(p, module+"/"):
				if !slices.ContainsFunc(allowed, func(a string) bool { return strings.HasPrefix(p, a) }) {
					t.Errorf("%s: plugin imports host internals %q; use pkg/plugin", fset.Position(imp.Pos()), p)
				}
			default:
				if !slices.ContainsFunc(allowedThirdParty, func(a string) bool { return strings.HasPrefix(p, a) }) {
					t.Errorf("%s: plugin imports %q, which is not on the allowlist in internal/lint", fset.Position(imp.Pos()), p)
				}
			}
		}
	}
}

// uiCalls are functions and methods whose string arguments are shown to the
// user (MyGo ui constructors and Element methods).
var uiCalls = map[string]bool{
	"Text": true, "Textf": true, "Placeholder": true, "Label": true, "Tooltip": true,
	"Button": true, "PrimaryButton": true, "Toast": true, "Announce": true,
}

// shownFields are struct fields of SDK and UI types that are shown to the user.
var shownFields = map[string]bool{
	"Title": true, "Subtitle": true, "Message": true, "Accessory": true, "ConfirmMessage": true,
	"Placeholder": true, "EmptyText": true, "Label": true, "ToolTip": true, "Description": true,
}

// TestNoHardcodedUIStrings flags string literals with letters passed to UI
// text functions or assigned to user-visible fields (design §10.5). Message
// keys ("%key%") and lines marked with lint:allow-literal are fine.
func TestNoHardcodedUIStrings(t *testing.T) {
	for _, dir := range []string{"internal/ui", "internal/shell", "plugins"} {
		fset, files := goFiles(t, dir)
		for _, f := range files {
			for _, problem := range hardcodedStrings(fset, f) {
				t.Error(problem)
			}
		}
	}
}

// TestCheckerFindsLiterals makes sure the checker itself works.
func TestCheckerFindsLiterals(t *testing.T) {
	src := `package p
func f() {
	ui.Text(c, "Hello")                // flagged
	ui.Text(c, "↵")                    // no letters
	x := Item{Title: "Open", ID: "a"}  // flagged (Title), ID is fine
	y := Command{Title: "%cmd.title%"} // message key
	ui.Text(c, "Ctrl") // lint:allow-literal
}`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	got := hardcodedStrings(fset, f)
	if len(got) != 2 || !strings.Contains(got[0], `"Hello"`) || !strings.Contains(got[1], `"Open"`) {
		t.Fatalf("findings = %q", got)
	}
}

// hardcodedStrings reports string literals with letters passed to UI text
// functions or assigned to user-visible fields.
func hardcodedStrings(fset *token.FileSet, f *ast.File) []string {
	var out []string
	allowed := allowedLines(fset, f)
	check := func(lit *ast.BasicLit, what string) {
		if lit == nil || lit.Kind != token.STRING {
			return
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil || !userText(s) {
			return
		}
		pos := fset.Position(lit.Pos())
		if allowed[pos.Line] {
			return
		}
		out = append(out, fmt.Sprintf("%s: hard-coded user-visible string %q in %s; use the Localizer (or mark the line %s)", pos, s, what, allowMarker))
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if name := calleeName(n.Fun); uiCalls[name] {
				for _, a := range n.Args {
					lit, _ := a.(*ast.BasicLit)
					check(lit, name+"()")
				}
			}
		case *ast.KeyValueExpr:
			if k, ok := n.Key.(*ast.Ident); ok && shownFields[k.Name] {
				lit, _ := n.Value.(*ast.BasicLit)
				check(lit, "field "+k.Name)
			}
		}
		return true
	})
	return out
}
func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

// userText reports whether s looks like prose: it has a letter and is not a
// "%key%" manifest reference.
func userText(s string) bool {
	if len(s) > 2 && s[0] == '%' && s[len(s)-1] == '%' {
		return false
	}
	return strings.IndexFunc(s, unicode.IsLetter) >= 0
}

// allowedLines returns the lines carrying the allow marker in a comment.
func allowedLines(fset *token.FileSet, f *ast.File) map[int]bool {
	out := map[int]bool{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.Contains(c.Text, allowMarker) {
				out[fset.Position(c.Pos()).Line] = true
			}
		}
	}
	return out
}

// TestPluginGoroutines enforces that plugins start background work through
// plugin.Host.Go, which contains panics (docs/reliability.md §3.1): no `go`
// statements and no WaitGroup.Go in plugin code.
func TestPluginGoroutines(t *testing.T) {
	fset, files := goFiles(t, "plugins")
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.GoStmt:
				t.Errorf("%s: go statement in a plugin; use Host.Go so a panic cannot crash the launcher", fset.Position(n.Pos()))
			case *ast.CallExpr:
				sel, ok := n.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Go" {
					return true
				}
				recv := sel.X
				if s, ok := recv.(*ast.SelectorExpr); ok {
					recv = s.Sel
				}
				if id, ok := recv.(*ast.Ident); ok && strings.Contains(strings.ToLower(id.Name), "wg") {
					t.Errorf("%s: %s.Go in a plugin; use wg.Add(1) and Host.Go", fset.Position(n.Pos()), id.Name)
				}
			}
			return true
		})
	}
}
