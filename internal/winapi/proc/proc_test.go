package proc

import (
	"slices"
	"strings"
	"testing"
)

func TestCleanLine(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain\n", "plain"},
		{"crlf\r\n", "crlf"},
		{"\x1b[32mgreen\x1b[0m text", "green text"},
		{"progress 10%\rprogress 100%\r\n", "progress 100%"},
		{"\x1b]0;title\x07after", "after"},
		{"中文 ünïcödé", "中文 ünïcödé"},
	}
	for _, tt := range tests {
		if got := CleanLine([]byte(tt.in)); got != tt.want {
			t.Errorf("CleanLine(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	// Not UTF-8: decoded from the OEM code page, never invalid UTF-8.
	if got := CleanLine([]byte{'a', 0x82, 0xa0, 'b'}); !strings.HasPrefix(got, "a") || !strings.HasSuffix(got, "b") || !strings.ContainsFunc(got, func(r rune) bool { return r > 127 }) {
		t.Errorf("legacy line = %q", got)
	}
}

func TestLineWriter(t *testing.T) {
	var lines []string
	w := &LineWriter{OnLine: func(s string) { lines = append(lines, s) }}
	w.Write([]byte("one\ntw"))
	w.Write([]byte("o\nthr"))
	if !slices.Equal(lines, []string{"one", "two"}) || w.Partial() != "thr" {
		t.Fatalf("lines %q, partial %q", lines, w.Partial())
	}
	w.Flush()
	w.Flush()
	if !slices.Equal(lines, []string{"one", "two", "thr"}) || w.Partial() != "" {
		t.Errorf("after Flush: %q", lines)
	}
	big := strings.Repeat("x", maxPending+10)
	w.Write([]byte(big))
	if len(w.Partial()) != maxPending {
		t.Errorf("pending %d bytes", len(w.Partial()))
	}
}

func TestMergeEnv(t *testing.T) {
	got := MergeEnv([]string{"Path=C:\\a", "HOME=x", "=C:=C:\\"}, []string{"PATH=C:\\b", "NEW=1"})
	if !slices.Equal(got, []string{"HOME=x", "=C:=C:\\", "PATH=C:\\b", "NEW=1"}) {
		t.Errorf("MergeEnv = %q", got)
	}
	if Getenv(got, "path") != `C:\b` || Getenv(got, "nope") != "" {
		t.Error("Getenv")
	}
	if !IsBatch(`C:\x\run.CMD`) || IsBatch("run.ps1") {
		t.Error("IsBatch")
	}
}
