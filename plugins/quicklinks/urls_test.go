package quicklinks

import (
	"net/url"
	"strings"
	"testing"
)

func TestDetectURLPositive(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://example.com", "https://example.com"},
		{"http://example.com", "http://example.com"},
		{"HTTPS://EXAMPLE.COM", "https://EXAMPLE.COM"},
		{"  https://example.com/a?b=c#d  ", "https://example.com/a?b=c#d"},
		{"http://intranet", "http://intranet"},
		{"http://intranet:8080/x", "http://intranet:8080/x"},
		{"https://setup.py", "https://setup.py"},
		{"https://例子.中国", "https://例子.中国"},
		{"http://[::1]:8080/", "http://[::1]:8080/"},
		{"http://192.168.1.1", "http://192.168.1.1"},
		{"https://foo.unknowntld", "https://foo.unknowntld"},
		{"github.com/foo", "https://github.com/foo"},
		{"github.com/foo/bar?tab=readme#top", "https://github.com/foo/bar?tab=readme#top"},
		{"GitHub.com", "https://GitHub.com"},
		{"golang.org/x/text", "https://golang.org/x/text"},
		{"example.com:8080", "https://example.com:8080"},
		{"example.com?q=1", "https://example.com?q=1"},
		{"example.com#top", "https://example.com#top"},
		{"sub.domain.example.co.uk", "https://sub.domain.example.co.uk"},
		{"asp.net", "https://asp.net"},
		{"example.cn", "https://example.cn"},
		{"bilibili.com/video/BV1xx", "https://bilibili.com/video/BV1xx"},
		{"t.co", "https://t.co"},
		{"x.ai", "https://x.ai"},
		{"my-site.dev", "https://my-site.dev"},
		{"例子.中国", "https://例子.中国"},
		{"münchen.de", "https://münchen.de"},
		{"example.xn--fiqs8s", "https://example.xn--fiqs8s"},
		{"www.example.md", "https://www.example.md"},
		{"www.example.whatever", "https://www.example.whatever"},
		{"example.md/page", "https://example.md/page"},
		{"setup.py/", "https://setup.py/"},
		{"localhost", "http://localhost"},
		{"localhost:3000", "http://localhost:3000"},
		{"LOCALHOST:3000", "http://LOCALHOST:3000"},
		{"localhost:3000/api?x=1", "http://localhost:3000/api?x=1"},
		{"localhost/x", "http://localhost/x"},
		{"192.168.1.1", "http://192.168.1.1"},
		{"192.168.1.1:8080/admin", "http://192.168.1.1:8080/admin"},
		{"127.0.0.1", "http://127.0.0.1"},
		{"0.0.0.0:65535", "http://0.0.0.0:65535"},
		{"[::1]:8080", "http://[::1]:8080"},
		{"[2001:db8::1]/x", "http://[2001:db8::1]/x"},
		{"[::1]", "http://[::1]"},
	}
	for _, tt := range tests {
		got, ok := DetectURL(tt.in)
		if !ok || got != tt.want {
			t.Errorf("DetectURL(%q) = %q, %v; want %q, true", tt.in, got, ok, tt.want)
		}
	}
}

func TestDetectURLNegative(t *testing.T) {
	tests := []string{
		// empty and whitespace
		"", "   ", "https://example.com/search?q=a b", "example .com", "a\tb.com",
		// explicit scheme without a valid host
		"https://", "http://", "http:///path", "https://:80", "http://example.com:",
		"http://example.com:0", "http://example.com:70000", "http://example.com:abc",
		"http://-foo.com", "http://foo..com", "http://1.2.3", "http://1.2.3.4.5",
		"http://999.1.1.1", "http://[zz]", "http://[fe80::1%25eth0]", "http://user:pass@example.com",
		"http://foo.123",
		// numbers and versions
		"1.5", "3.14", "v1.2.3", "1.2.3", "1.2.3.4.5", "256.1.1.1", "01.2.3.4",
		"1.2.3.4:0", "1.2.3.4:70000", "42",
		// file names
		"file.txt", "main.go", "config.json", "node.js", "index.html", "readme.md",
		"setup.py", "archive.zip", "go.mod", "chrome.exe", "a.b", "main.cc", "run.sh",
		"lib.rs", "clip.mov", "libfoo.so", "rules.mk", "script.ps", "app.pl",
		// e-mail and credentials
		"a@b.com", "user:pass@host.com", "mailto:a@b.com",
		// paths
		`C:\foo`, "C:/foo", `\\server\share`, "./foo.com", "../foo.com", "/foo.com",
		"~/foo.com", "foo\\bar.com",
		// malformed domains
		"foo..com", "-foo.com", "foo-.com", ".com", "com", "hello", "localhost.",
		"example.com.", "www.example", "www.example.c", "www.example.123",
		strings.Repeat("a", 64) + ".com",
		// ports
		"example.com:", "example.com:abc", "example.com:0", "example.com:65536",
		"example.com:80:80", "[::1]:", "[::1]x", "[::1", "[1.2.3.4]", "[::1]:99999",
		// other schemes and malformed schemes
		"ftp://example.com", "file:///C:/x", "file:foo", "javascript:alert(1)",
		"http//example.com", "http:/example.com", "://example.com",
	}
	for _, in := range tests {
		if got, ok := DetectURL(in); ok {
			t.Errorf("DetectURL(%q) = %q, true; want false", in, got)
		}
	}
}

func FuzzDetectURL(f *testing.F) {
	for _, s := range []string{
		"github.com/foo", "https://例子.中国", "localhost:3000", "[::1]:8080",
		"1.2.3.4", "setup.py", "www.example.md", "http://intranet", "a@b.com",
		`C:\foo`, "example.com:65535/x?y#z", "HTTP://[2001:db8::1]:1/",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got, ok := DetectURL(s)
		if !ok {
			if got != "" {
				t.Fatalf("DetectURL(%q) = %q, false; want empty string", s, got)
			}
			return
		}
		if !strings.HasPrefix(got, "http://") && !strings.HasPrefix(got, "https://") {
			t.Fatalf("DetectURL(%q) = %q: missing http(s) scheme", s, got)
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Fatalf("DetectURL(%q) = %q: url.Parse: %v", s, got, err)
		}
		if u.Host == "" {
			t.Fatalf("DetectURL(%q) = %q: empty host", s, got)
		}
	})
}
