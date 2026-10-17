package everything

import (
	"bufio"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMdfindQuery(t *testing.T) {
	tests := []struct {
		name   string
		q      Query
		query  string
		onlyIn string
	}{
		{"one word", Query{Text: "report"}, `kMDItemFSName == "*report*"cd`, ""},
		{"two words", Query{Text: "annual report"},
			`kMDItemFSName == "*annual*"cd && kMDItemFSName == "*report*"cd`, ""},
		{"phrase", Query{Text: `"annual report"`}, `kMDItemFSName == "*annual report*"cd`, ""},
		{"quote", Query{Text: `it's`}, `kMDItemFSName == "*it\'s*"cd`, ""},
		{"quoted wildcard ext", Query{Text: `ext:a"b`}, `kMDItemFSName == "*.ab"c`, ""},
		{"wildcard", Query{Text: "*.pd?"}, `kMDItemFSName == "*.pd*"cd`, ""},
		{"match case", Query{Text: "Report", MatchCase: true}, `kMDItemFSName == "*Report*"d`, ""},
		{"negation filters only", Query{Text: `report !draft !"\node_modules\"`}, `kMDItemFSName == "*report*"cd`, ""},
		{"alternatives", Query{Text: "cat|dog"},
			`(kMDItemFSName == "*cat*"cd || kMDItemFSName == "*dog*"cd)`, ""},
		{"grouped alternatives", Query{Text: `<cat|dog> !<"/system/"|"/library/">`},
			`(kMDItemFSName == "*cat*"cd || kMDItemFSName == "*dog*"cd)`, ""},
		{"ext", Query{Text: "ext:pdf;.Docx report"},
			`(kMDItemFSName == "*.pdf"c || kMDItemFSName == "*.docx"c) && kMDItemFSName == "*report*"cd`, ""},
		{"folder", Query{Text: "folder:src"},
			`kMDItemContentType == "public.folder" && kMDItemFSName == "*src*"cd`, ""},
		{"file", Query{Text: "file: notes"},
			`kMDItemContentType != "public.folder" && kMDItemFSName == "*notes*"cd`, ""},
		{"scope", Query{Text: `"/Users/demo/My Reports\" quarterly`},
			`kMDItemFSName == "*quarterly*"cd`, "/Users/demo/My Reports"},
		{"scope only", Query{Text: `"/Users/demo\"`}, `kMDItemFSName == "*"`, "/Users/demo"},
		{"relative path", Query{Text: `docs\report`}, `kMDItemFSName == "*report*"cd`, ""},
		{"match path", Query{Text: "project report", MatchPath: true},
			`(kMDItemFSName == "*project*"cd || kMDItemFSName == "*report*"cd)`, ""},
		{"empty", Query{Text: "  "}, `kMDItemFSName == "*"`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, onlyIn := parseSpotlight(tt.q).mdfindQuery()
			if query != tt.query || onlyIn != tt.onlyIn {
				t.Errorf("mdfindQuery(%q) =\n %s, %q\nwant\n %s, %q", tt.q.Text, query, onlyIn, tt.query, tt.onlyIn)
			}
		})
	}
}

func TestEscapeSpotlight(t *testing.T) {
	for _, tt := range []struct {
		in   string
		wild bool
		want string
	}{
		{`plain`, false, `plain`},
		{`a"b\c*d'e`, false, `a\"b\\c\*d\'e`},
		{`*.pd?`, true, `*.pd*`},
		{`a"*`, true, `a\"*`},
	} {
		if got := escapeSpotlight(tt.in, tt.wild); got != tt.want {
			t.Errorf("escapeSpotlight(%q, %v) = %s, want %s", tt.in, tt.wild, got, tt.want)
		}
	}
}

func TestSpotlightFilter(t *testing.T) {
	type file struct {
		path string
		dir  bool
	}
	files := []file{
		{"/Users/demo/Documents/Annual Report.pdf", false},
		{"/Users/demo/Documents/report draft.docx", false},
		{"/Users/demo/project/node_modules/report/index.js", false},
		{"/Users/demo/project/node_modules", true},
		{"/System/Library/report.plist", false},
		{"/Users/demo/project/src", true},
		{"/Users/demo/projects-old/src/main.go", false},
	}
	tests := []struct {
		q    Query
		want []string
	}{
		{Query{Text: "report"}, []string{"Annual Report.pdf", "report draft.docx", "report.plist"}},
		{Query{Text: "Report", MatchCase: true}, []string{"Annual Report.pdf"}},
		{Query{Text: `report !draft !"\node_modules\" !"/system/"`}, []string{"Annual Report.pdf"}},
		{Query{Text: `<"/system/"|"\node_modules\">`}, []string{"index.js", "node_modules", "report.plist"}},
		{Query{Text: "ext:pdf;docx"}, []string{"Annual Report.pdf", "report draft.docx"}},
		{Query{Text: "folder:"}, []string{"node_modules", "src"}},
		{Query{Text: "file: src"}, nil},
		{Query{Text: "*.go"}, []string{"main.go"}},
		{Query{Text: "re?ort.*"}, []string{"report.plist"}},
		{Query{Text: `"/users/demo/project\"`}, []string{"index.js", "node_modules", "src"}},
		{Query{Text: `"/Users/demo/project" src`}, []string{"src"}},
		{Query{Text: `project\src`}, []string{"src"}},
		{Query{Text: "demo index", MatchPath: true}, []string{"index.js"}},
		{Query{Text: "pdf|plist"}, []string{"Annual Report.pdf", "report.plist"}},
	}
	for _, tt := range tests {
		sq := parseSpotlight(tt.q)
		var got []string
		for _, f := range files {
			if sq.filter(f.path, f.dir) {
				got = append(got, f.path[strings.LastIndex(f.path, "/")+1:])
			}
		}
		slices.Sort(got)
		if !slices.Equal(got, tt.want) {
			t.Errorf("filter %q (case %v, path %v) = %q, want %q", tt.q.Text, tt.q.MatchCase, tt.q.MatchPath, got, tt.want)
		}
	}
}

func TestSortResults(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	res := []Result{
		{Name: "b", Path: "/z/b", Size: 1, Modified: t0.Add(2 * time.Hour)},
		{Name: "A", Path: "/y/A", Size: 3, Modified: t0},
		{Name: "c", Path: "/x/c", Size: 2, Modified: t0.Add(time.Hour)},
	}
	names := func(o SortOrder) string {
		r := slices.Clone(res)
		sortResults(r, o)
		var s []string
		for _, x := range r {
			s = append(s, x.Name)
		}
		return strings.Join(s, "")
	}
	for o, want := range map[SortOrder]string{
		0: "Abc", SortNameAscending: "Abc", SortNameDescending: "cbA",
		SortPathAscending: "cAb", SortPathDescending: "bAc",
		SortSizeAscending: "bcA", SortSizeDescending: "Acb",
		SortDateModifiedAscending: "Acb", SortDateModifiedDescending: "bcA",
		SortRunCountDescending: "bAc",
	} {
		if got := names(o); got != want {
			t.Errorf("sort %d = %s, want %s", o, got, want)
		}
	}
}

func TestSplitNUL(t *testing.T) {
	sc := bufio.NewScanner(strings.NewReader("/a b\x00/c\nd\x00/tail"))
	sc.Split(splitNUL)
	var got []string
	for sc.Scan() {
		got = append(got, sc.Text())
	}
	if want := []string{"/a b", "/c\nd", "/tail"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
