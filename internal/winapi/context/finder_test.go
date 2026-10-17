package wincontext

import (
	"errors"
	"slices"
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
)

func TestFinderResult(t *testing.T) {
	for _, tt := range []struct {
		out      string
		kind     string
		folder   string
		selected []string
		ok       bool
	}{
		{"window\n/Users/a/Documents/\n", plugin.ContextExplorer, "/Users/a/Documents", nil, true},
		{"window\n/Users/a/Documents/\n/Users/a/Documents/x y.txt\n/Users/a/Documents/Sub/\n",
			plugin.ContextExplorer, "/Users/a/Documents", []string{"/Users/a/Documents/x y.txt", "/Users/a/Documents/Sub"}, true},
		{"desktop\n/Users/a/Desktop/\n/Users/a/Desktop/a.png", plugin.ContextDesktop, "/Users/a/Desktop", []string{"/Users/a/Desktop/a.png"}, true},
		{"window\n/\n", plugin.ContextExplorer, "/", nil, true},
		// Recents, AirDrop, a search without selection: nothing to offer.
		{"window\n\n", "", "", nil, false},
		{"window\n", "", "", nil, false},
		// A search with a selection: the items only.
		{"window\n\n/Volumes/USB/r.pdf\n", plugin.ContextExplorer, "", []string{"/Volumes/USB/r.pdf"}, true},
		{"", "", "", nil, false},
		{"garbage\n/x", "", "", nil, false},
		{"window\r\n/Users/a/\r\n", plugin.ContextExplorer, "/Users/a", nil, true},
	} {
		kind, folder, sel, ok := finderResult(tt.out)
		if kind != tt.kind || folder != tt.folder || !slices.Equal(sel, tt.selected) || ok != tt.ok {
			t.Errorf("finderResult(%q) = %q %q %q %v", tt.out, kind, folder, sel, ok)
		}
	}
}

func TestScriptError(t *testing.T) {
	for in, want := range map[string]error{
		"94:110: execution error: Not authorized to send Apple events to Finder. (-1743)\n": ErrNotAuthorized,
		"execution error: A privilege violation occurred. (-10004)":                         ErrNotAuthorized,
	} {
		if got := scriptError(in); !errors.Is(got, want) {
			t.Errorf("scriptError(%q) = %v", in, got)
		}
	}
	for in, want := range map[string]string{
		"execution error: Finder got an error: AppleEvent timed out. (-1712)": "wincontext: AppleScript error -1712",
		"":     "wincontext: osascript failed",
		"boom": "wincontext: osascript: boom",
	} {
		if got := scriptError(in); got == nil || got.Error() != want || errors.Is(got, ErrNotAuthorized) {
			t.Errorf("scriptError(%q) = %v", in, got)
		}
	}
}

func TestOnProblem(t *testing.T) {
	var got []error
	OnProblem(func(err error) { got = append(got, err) })
	problem(ErrNotAuthorized)
	OnProblem(nil)
	problem(ErrNotAuthorized)
	if len(got) != 1 {
		t.Errorf("problems = %v", got)
	}
}

func TestFileURLPath(t *testing.T) {
	for in, want := range map[string]string{
		"file:///Users/a/b%20c/":             "/Users/a/b c",
		"file://localhost/tmp/x.txt":         "/tmp/x.txt",
		"file:///":                           "/",
		"https://example.com/a":              "",
		"file://server/share/x":              "",
		"":                                   "",
		"/Users/a":                           "",
		"file:///Users/a/%E4%B8%AD%E6%96%87": "/Users/a/中文",
	} {
		got, ok := fileURLPath(in)
		if got != want || ok != (want != "") {
			t.Errorf("fileURLPath(%q) = %q, %v", in, got, ok)
		}
	}
}
