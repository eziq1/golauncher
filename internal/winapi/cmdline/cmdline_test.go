package cmdline

import "testing"

func TestQuote(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{`C:\tmp`, `C:\tmp`},
		{``, `""`},
		{`C:\My Files`, `"C:\My Files"`},
		{`C:\My Files\`, `"C:\My Files\\"`},
		{`C:\`, `C:\`},
		{`say "hi"`, `"say \"hi\""`},
		{`a\"b`, `"a\\\"b"`},
		{"tab\there", "\"tab\there\""},
	} {
		if got := Quote(tt.in); got != tt.want {
			t.Errorf("Quote(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
	if got := Join("-d", `C:\a b`, "x"); got != `-d "C:\a b" x` {
		t.Errorf("Join = %s", got)
	}
}

func TestWT(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"Ubuntu", "Ubuntu"},
		{"PowerShell 7", `"PowerShell 7"`},
		{"a;b", `a\;b`},
		{`C:\x; y\`, `"C:\x\; y\\"`},
	} {
		if got := WT(tt.in); got != tt.want {
			t.Errorf("WT(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
	if got := JoinWT("-p", "My;Profile"); got != `-p My\;Profile` {
		t.Errorf("JoinWT = %s", got)
	}
}
