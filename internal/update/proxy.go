package update

import (
	"net/http"
	"net/url"
	"path"
	"strings"
)

// SystemProxy returns the proxy function of a Windows proxy setting (as
// WinHttpGetIEProxyConfigForCurrentUser reports the "Use a proxy server"
// setting): proxy is "host:port" for every protocol or
// "http=host:port;https=host:port", bypass the exceptions ("<local>",
// "*.example.com", "10.*"), separated by semicolons or spaces. Environment
// variables (HTTPS_PROXY, NO_PROXY) come first. nil means no proxy.
func SystemProxy(proxy, bypass string) func(*http.Request) (*url.URL, error) {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		return http.ProxyFromEnvironment
	}
	byScheme := map[string]string{}
	all := ""
	for _, part := range strings.FieldsFunc(proxy, func(r rune) bool { return r == ';' || r == ' ' }) {
		if scheme, addr, ok := strings.Cut(part, "="); ok {
			byScheme[strings.ToLower(scheme)] = addr
		} else {
			all = part
		}
	}
	patterns := strings.FieldsFunc(strings.ToLower(bypass), func(r rune) bool { return r == ';' || r == ' ' || r == ',' })
	return func(req *http.Request) (*url.URL, error) {
		if u, err := http.ProxyFromEnvironment(req); u != nil || err != nil {
			return u, err
		}
		host := strings.ToLower(req.URL.Hostname())
		if isLoopback(host) || bypassed(host, patterns) {
			return nil, nil
		}
		addr := byScheme[req.URL.Scheme]
		if addr == "" && len(byScheme) == 0 {
			addr = all
		}
		if addr == "" {
			return nil, nil
		}
		if !strings.Contains(addr, "://") {
			addr = "http://" + addr
		}
		return url.Parse(addr)
	}
}

// ParseScutilProxy turns the output of macOS's "scutil --proxy" (the proxy
// set in System Settings › Network) into SystemProxy's arguments: the
// enabled HTTP and HTTPS proxies as "http=host:port;https=host:port" and
// the exceptions, with "<local>" for "Exclude simple hostnames". Automatic
// configuration (PAC) is not read.
func ParseScutilProxy(out string) (proxy, bypass string) {
	vals := map[string]string{}
	var exceptions []string
	inList := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		key, val, ok := strings.Cut(line, " : ")
		switch {
		case line == "}":
			inList = false
		case ok && key == "ExceptionsList":
			inList = true
		case ok && inList:
			exceptions = append(exceptions, strings.TrimSpace(val))
		case ok:
			vals[key] = strings.TrimSpace(val)
		}
	}
	var parts []string
	for _, p := range []struct{ scheme, prefix string }{{"http", "HTTP"}, {"https", "HTTPS"}} {
		host := vals[p.prefix+"Proxy"]
		if vals[p.prefix+"Enable"] != "1" || host == "" {
			continue
		}
		addr := host
		if port := vals[p.prefix+"Port"]; port != "" {
			addr += ":" + port
		}
		parts = append(parts, p.scheme+"="+addr)
	}
	if vals["ExcludeSimpleHostnames"] == "1" {
		exceptions = append(exceptions, "<local>")
	}
	return strings.Join(parts, ";"), strings.Join(exceptions, ";")
}

// bypassed reports whether host matches an exception.
func bypassed(host string, patterns []string) bool {
	for _, p := range patterns {
		switch {
		case p == "<local>":
			if !strings.Contains(host, ".") {
				return true
			}
		case p == host:
			return true
		default:
			if ok, _ := path.Match(p, host); ok {
				return true
			}
		}
	}
	return false
}
