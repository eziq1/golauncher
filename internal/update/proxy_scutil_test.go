package update_test

import (
	"net/http"
	"testing"

	"github.com/eziq1/golauncher/internal/update"
)

func TestParseScutilProxy(t *testing.T) {
	out := `<dictionary> {
  ExceptionsList : <array> {
    0 : *.local
    1 : 169.254/16
  }
  ExcludeSimpleHostnames : 1
  FTPPassive : 1
  HTTPEnable : 0
  HTTPPort : 3128
  HTTPProxy : off.example.com
  HTTPSEnable : 1
  HTTPSPort : 8080
  HTTPSProxy : proxy.example.com
}
`
	proxy, bypass := update.ParseScutilProxy(out)
	if proxy != "https=proxy.example.com:8080" || bypass != "*.local;169.254/16;<local>" {
		t.Fatalf("got %q, %q", proxy, bypass)
	}
	for _, v := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(v, "")
	}
	f := update.SystemProxy(proxy, bypass)
	req, _ := http.NewRequest("GET", "https://api.github.com/x", nil)
	if u, err := f(req); err != nil || u == nil || u.Host != "proxy.example.com:8080" {
		t.Fatalf("proxy for GitHub = %v, %v", u, err)
	}
	if p, b := update.ParseScutilProxy("<dictionary> {\n  FTPPassive : 1\n}\n"); p != "" || b != "" {
		t.Fatalf("no proxy: %q, %q", p, b)
	}
}
