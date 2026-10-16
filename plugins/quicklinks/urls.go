package quicklinks

import (
	"net/netip"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// knownTLDs lists the top-level domains accepted for bare domains such as
// "github.com". It is deliberately short: anything not listed is treated as
// a file name or other non-URL text unless prefixed with "www." or a scheme.
var knownTLDs = map[string]bool{}

// ambiguousTLDs are real TLDs that are also common file extensions
// ("setup.py", "readme.md"). They are accepted only with an explicit scheme,
// a "www." prefix, or a following path.
var ambiguousTLDs = map[string]bool{}

func init() {
	for t := range strings.FieldsSeq(`
		com org net edu gov mil int info biz name pro io dev app ai co me tv
		xyz top site online tech cloud page blog shop store art club live news
		wiki link one fun space website icu vip ink design
		cn uk de jp fr ru us ca au in br kr tw hk sg nl it es se ch eu nz ie
		at be dk fi no ly gl fm im gg mx ar za th vn my ph tr ua cz il gr pt
		hu ro lt lv ee lu
		中国 中國 公司 网络 xn--fiqs8s xn--fiqz9s xn--55qx5d xn--io0a7i`) {
		knownTLDs[t] = true
	}
	for t := range strings.FieldsSeq("py md sh rs pl zip mov ps so mk cc") {
		knownTLDs[t] = true
		ambiguousTLDs[t] = true
	}
}

// DetectURL reports whether text (already trimmed by the caller; trim again anyway) is, as a whole,
// a web address the user wants to open, and returns the URL to open.
//
// Text with an explicit http:// or https:// scheme is accepted when it has a
// valid host. Without a scheme, "www." names and bare domains with a known
// TLD get "https://" prepended, while localhost, IPv4 and bracketed IPv6
// addresses get "http://". Numbers, versions, file names, e-mail addresses,
// Windows paths and other schemes are rejected.
func DetectURL(text string) (string, bool) {
	s := strings.TrimSpace(text)
	if s == "" || strings.IndexFunc(s, unicode.IsSpace) >= 0 || !utf8.ValidString(s) {
		return "", false
	}
	if out, ok := detectExplicit(s); ok || strings.Contains(s, "://") {
		return out, ok
	}
	if strings.ContainsAny(s, `\@`) {
		return "", false
	}
	hostport, rest := s, ""
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		hostport, rest = s[:i], s[i:]
	}
	host, ok := splitHostPort(hostport)
	if !ok {
		return "", false
	}
	var out string
	switch classifyHost(host, false) {
	case hostLocal:
		out = "http://" + s
	case hostDomain:
		labels := strings.Split(strings.ToLower(host), ".")
		if labels[0] != "www" && ambiguousTLDs[labels[len(labels)-1]] && !strings.HasPrefix(rest, "/") {
			return "", false
		}
		out = "https://" + s
	default:
		return "", false
	}
	if u, err := url.Parse(out); err != nil || u.Host == "" {
		return "", false
	}
	return out, true
}

// detectExplicit handles text starting with an http:// or https:// scheme.
func detectExplicit(s string) (string, bool) {
	lower := strings.ToLower(s)
	var scheme string
	switch {
	case strings.HasPrefix(lower, "http://"):
		scheme = "http://"
	case strings.HasPrefix(lower, "https://"):
		scheme = "https://"
	default:
		return "", false
	}
	out := scheme + s[len(scheme):]
	u, err := url.Parse(out)
	if err != nil || u.Host == "" || u.User != nil || strings.HasSuffix(u.Host, ":") {
		return "", false
	}
	if p := u.Port(); p != "" && !validPort(p) {
		return "", false
	}
	host := u.Hostname()
	if strings.HasPrefix(u.Host, "[") {
		host = "[" + host + "]"
	}
	if classifyHost(host, true) == hostInvalid {
		return "", false
	}
	return out, true
}

// splitHostPort splits "host[:port]" or "[ipv6][:port]" and validates the port.
func splitHostPort(hp string) (string, bool) {
	host, port, hasPort := hp, "", false
	if strings.HasPrefix(hp, "[") {
		end := strings.IndexByte(hp, ']')
		if end < 0 {
			return "", false
		}
		host = hp[:end+1]
		after := hp[end+1:]
		if after != "" {
			if !strings.HasPrefix(after, ":") {
				return "", false
			}
			port, hasPort = after[1:], true
		}
	} else {
		host, port, hasPort = strings.Cut(hp, ":")
	}
	if hasPort && !validPort(port) {
		return "", false
	}
	return host, host != ""
}

// validPort reports whether p is a decimal port number in 1..65535.
func validPort(p string) bool {
	if p == "" || len(p) > 5 {
		return false
	}
	n := 0
	for _, c := range p {
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + int(c-'0')
	}
	return n >= 1 && n <= 65535
}

type hostKind int

const (
	hostInvalid hostKind = iota
	hostLocal            // localhost or an IP address: opened over http
	hostDomain           // a domain name: opened over https
)

// classifyHost validates host (an IP literal in brackets, localhost, IPv4 or a
// domain name). With explicit set (the user typed a scheme) single-label hosts
// and any alphabetic TLD are allowed; otherwise the TLD must be known, except
// for "www." names whose TLD only has to look like one.
func classifyHost(host string, explicit bool) hostKind {
	h := strings.ToLower(host)
	if strings.HasPrefix(h, "[") {
		if !strings.HasSuffix(h, "]") {
			return hostInvalid
		}
		a, err := netip.ParseAddr(h[1 : len(h)-1])
		if err != nil || !a.Is6() || a.Zone() != "" {
			return hostInvalid
		}
		return hostLocal
	}
	if h == "localhost" {
		return hostLocal
	}
	if strings.Trim(h, "0123456789.") == "" {
		if a, err := netip.ParseAddr(h); err == nil && a.Is4() {
			return hostLocal
		}
		return hostInvalid
	}
	labels := strings.Split(h, ".")
	for _, l := range labels {
		if !validLabel(l) {
			return hostInvalid
		}
	}
	tld := labels[len(labels)-1]
	switch {
	case explicit:
		if len(labels) > 1 && strings.Trim(tld, "0123456789") == "" {
			return hostInvalid
		}
	case labels[0] == "www" && len(labels) >= 3:
		if !alphaTLD(tld) {
			return hostInvalid
		}
	case len(labels) < 2 || !knownTLDs[tld]:
		return hostInvalid
	}
	return hostDomain
}

// validLabel reports whether l is a DNS label of 1..63 characters made of
// ASCII letters, digits and hyphens or non-ASCII letters (IDN), not starting
// or ending with a hyphen.
func validLabel(l string) bool {
	if l == "" || utf8.RuneCountInString(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
		return false
	}
	for _, r := range l {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		case r >= utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r)):
		default:
			return false
		}
	}
	return true
}

// alphaTLD reports whether tld is at least two letters (ASCII or not), or a
// punycode "xn--" label.
func alphaTLD(tld string) bool {
	if strings.HasPrefix(tld, "xn--") {
		return true
	}
	if utf8.RuneCountInString(tld) < 2 {
		return false
	}
	for _, r := range tld {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}
