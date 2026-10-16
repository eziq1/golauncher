package quicklinks

import (
	"encoding/json/v2"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

var fixedNow = time.Date(2026, 3, 5, 9, 7, 3, 0, time.UTC)

func TestEncodeComponent(t *testing.T) {
	for in, want := range map[string]string{
		"golang generics": "golang%20generics",
		"a&b=c":           "a%26b%3Dc",
		"c# tips":         "c%23%20tips",
		"1+1":             "1%2B1",
		"100%":            "100%25",
		"a/b?c":           "a%2Fb%3Fc",
		"天气":              "%E5%A4%A9%E6%B0%94",
		"🎉 party":         "%F0%9F%8E%89%20party",
		"-_.~":            "-_.~",
		"":                "",
	} {
		got := encodeComponent(in)
		if got != want {
			t.Errorf("encodeComponent(%q) = %q, want %q", in, got, want)
		}
		if back, err := url.QueryUnescape(got); err != nil || back != in {
			t.Errorf("round trip of %q: %q, %v", in, back, err)
		}
	}
}

func TestExpand(t *testing.T) {
	v := values{query: "a b&c", now: fixedNow, clipboard: func() string { return "x#y 中" }}
	for _, tt := range []struct {
		tmpl   string
		encode bool
		want   string
	}{
		{"https://e.com/?q={query}", true, "https://e.com/?q=a%20b%26c"},
		{"https://e.com/?q={argument}", true, "https://e.com/?q=a%20b%26c"},
		{"https://e.com/?q={QUERY}", true, "https://e.com/?q=a%20b%26c"},
		{"https://e.com/{query:raw}", true, "https://e.com/a b&c"},
		{"https://e.com/?t={clipboard}", true, "https://e.com/?t=x%23y%20%E4%B8%AD"},
		{"https://e.com/?t={clipboard:raw}", true, "https://e.com/?t=x#y 中"},
		{"https://e.com/{date}", true, "https://e.com/2026-03-05"},
		{"https://e.com/{datetime}", true, "https://e.com/2026-03-05%2009%3A07"},
		{"https://e.com/{date:yyyyMMdd}/{time:HH-mm-ss}", true, "https://e.com/20260305/09-07-03"},
		{`C:\notes\{date:yyyy}\{date:M}-{date:d} {time:H}h.md`, false, `C:\notes\2026\3-5 9h.md`},
		{"{date:yy/MM/dd}", false, "26/03/05"},
		{"{query} and {query}", false, "a b&c and a b&c"},
		{"https://e.com/{unknown}/{query", true, "https://e.com/{unknown}/{query"},
		{"no placeholders", true, "no placeholders"},
	} {
		if got := expand(tt.tmpl, tt.encode, v); got != tt.want {
			t.Errorf("expand(%q, %v) = %q, want %q", tt.tmpl, tt.encode, got, tt.want)
		}
	}
	// The clipboard is read only when the link uses it.
	read := 0
	expand("https://e.com/{query}", true, values{clipboard: func() string { read++; return "" }})
	if read != 0 {
		t.Error("clipboard read without {clipboard}")
	}
}

func TestPlaceholders(t *testing.T) {
	for tmpl, want := range map[string][2]bool{
		"https://e.com":              {false, false},
		"https://e.com/{query}":      {true, false},
		"https://e.com/{argument}":   {true, false},
		"https://e.com/{clipboard}":  {false, true},
		"x {date:yyyy} {query:raw}":  {true, true},
		"https://e.com/{other}":      {false, false},
		"https://e.com/{time}?{tim}": {false, true},
	} {
		q, d := placeholders(tmpl)
		if q != want[0] || d != want[1] {
			t.Errorf("placeholders(%q) = %v, %v; want %v", tmpl, q, d, want)
		}
	}
}

func TestParseLink(t *testing.T) {
	for _, tt := range []struct {
		link string
		want target
	}{
		{"https://pkg.go.dev/std", target{kind: kindURL, link: "https://pkg.go.dev/std"}},
		{"github.com/{query}", target{kind: kindURL, link: "https://github.com/{query}"}},
		{"mailto:someone@example.com", target{kind: kindURL, link: "mailto:someone@example.com"}},
		{"ms-settings:display", target{kind: kindURL, link: "ms-settings:display"}},
		{`C:\Users\Public\Documents`, target{kind: kindPath, link: `C:\Users\Public\Documents`}},
		{`C:\My Folder\notes.txt`, target{kind: kindPath, link: `C:\My Folder\notes.txt`}},
		{`%USERPROFILE%\Downloads`, target{kind: kindPath, link: `%USERPROFILE%\Downloads`}},
		{`\\server\share`, target{kind: kindPath, link: `\\server\share`}},
		{`"C:\Program Files\App\app.exe" --open {query}`, target{kind: kindProgram, link: `C:\Program Files\App\app.exe`, args: "--open {query}"}},
		{`"C:\Tools\x.exe"`, target{kind: kindProgram, link: `C:\Tools\x.exe`}},
		{`notepad.exe C:\notes.txt`, target{kind: kindProgram, link: "notepad.exe", args: `C:\notes.txt`}},
		{`build.cmd release`, target{kind: kindProgram, link: "build.cmd", args: "release"}},
		{"notepad.exe", target{kind: kindPath, link: "notepad.exe"}},
	} {
		if got := parseLink(tt.link); got != tt.want {
			t.Errorf("parseLink(%q) = %+v, want %+v", tt.link, got, tt.want)
		}
	}
}

func TestResolveAndOpen(t *testing.T) {
	t.Setenv("QL_TEST_DIR", `D:\data`)
	v := values{query: "a b", now: fixedNow}
	open := func(link, openWith string) plugin.ActionDo { return parseLink(link).resolve(v).openDo(openWith) }
	opts := func(do plugin.ActionDo) plugin.OpenOptions {
		var o plugin.OpenOptions
		if len(do.Payload) > 0 {
			if err := json.Unmarshal(do.Payload, &o); err != nil {
				t.Fatal(err)
			}
		}
		return o
	}
	do := open("https://e.com/?q={query}", "")
	if do.Kind != plugin.ActionOpen || do.Target != "https://e.com/?q=a%20b" || len(do.Payload) != 0 {
		t.Errorf("url: %+v", do)
	}
	do = open("https://e.com/?q={query}", "firefox.exe")
	if do.Target != "firefox.exe" || opts(do).Args != `"https://e.com/?q=a%20b"` {
		t.Errorf("open with: %+v %+v", do, opts(do))
	}
	do = open(`%QL_TEST_DIR%\{date}.md`, "")
	if do.Target != `D:\data\2026-03-05.md` {
		t.Errorf("path: %+v", do)
	}
	do = open(`%QL_UNKNOWN%\x`, "")
	if do.Target != `%QL_UNKNOWN%\x` {
		t.Errorf("unknown variable: %+v", do)
	}
	do = open(`"C:\Tools\app.exe" --q "{query}"`, "ignored.exe")
	if do.Target != `C:\Tools\app.exe` || opts(do).Args != `--q "a b"` {
		t.Errorf("program: %+v %+v", do, opts(do))
	}
}

func TestHomepage(t *testing.T) {
	for tmpl, want := range map[string]string{
		"https://www.google.com/search?q={query}":    "https://www.google.com/",
		"https://www.google.com/maps/search/{query}": "https://www.google.com/",
		"not a url": "not a url",
	} {
		if got := homepage(tmpl); got != want {
			t.Errorf("homepage(%q) = %q, want %q", tmpl, got, want)
		}
	}
}

func TestKeywords(t *testing.T) {
	for _, tt := range []struct {
		text, kw, rest string
		ok             bool
	}{
		{"g golang generics", "g", "golang generics", true},
		{"G  golang", "g", "golang", true},
		{"g", "g", "", true},
		{"gh foo", "g", "", false},
		{"good", "go", "", false},
		{"百度 天气", "百度", "天气", true},
		{"百度网盘", "百度", "", false},
		{"百度　天气", "百度", "天气", true}, // full-width space
		{"b站 猫", "b站", "猫", true},
		{"", "g", "", false},
	} {
		rest, ok := cutKeyword(tt.text, tt.kw)
		if rest != tt.rest || ok != tt.ok {
			t.Errorf("cutKeyword(%q, %q) = %q, %v", tt.text, tt.kw, rest, ok)
		}
	}
	if got := parseKeywords(" g,GG， 谷歌、g  "); !slices.Equal(got, []string{"g", "gg", "谷歌"}) {
		t.Errorf("parseKeywords = %q", got)
	}
}

func TestFindKeyword(t *testing.T) {
	engines := []Engine{
		{ID: "google", Keywords: []string{"g"}},
		{ID: "gopkg", Keywords: []string{"go"}},
		{ID: "off", Keywords: []string{"x"}, Disabled: true},
		{ID: "custom1", Keywords: []string{"gx"}, Custom: true},
		{ID: "github", Keywords: []string{"gx"}},
	}
	links := []Quicklink{{ID: "1", Keyword: "go"}}
	m, ok := findKeyword("go generics", links, engines)
	if !ok || m.link == nil || m.rest != "generics" {
		t.Errorf("quicklink keyword: %+v %v", m, ok)
	}
	m, _ = findKeyword("go generics", nil, engines)
	if m.engine == nil || m.engine.ID != "gopkg" {
		t.Errorf("longest keyword: %+v", m)
	}
	m, _ = findKeyword("gx y", nil, engines)
	if m.engine == nil || m.engine.ID != "custom1" {
		t.Errorf("custom before built-in: %+v", m)
	}
	if _, ok := findKeyword("x y", nil, engines); ok {
		t.Error("disabled engine matched")
	}
}

// TestBuiltinKeywords: the built-in keywords are unique, lowercase, without
// spaces, and do not take the other plugins' prefixes.
func TestBuiltinKeywords(t *testing.T) {
	seen := map[string]string{}
	for _, b := range builtins {
		if b.name != "%engine."+b.id+"%" {
			t.Errorf("%s: name key %q", b.id, b.name)
		}
		if hasQuery, _ := placeholders(b.url); !hasQuery || parseLink(b.url).kind != kindURL {
			t.Errorf("%s: bad URL %q", b.id, b.url)
		}
		for _, kw := range b.keywords {
			if kw != normKeyword(kw) || len(parseKeywords(kw)) != 1 {
				t.Errorf("%s: keyword %q is not normalized", b.id, kw)
			}
			if other, ok := seen[kw]; ok {
				t.Errorf("keyword %q of %s is also %s's", kw, b.id, other)
			}
			seen[kw] = b.id
			if slices.Contains(reservedPrefixes, kw) {
				t.Errorf("keyword %q of %s is another plugin's prefix", kw, b.id)
			}
		}
	}
}
