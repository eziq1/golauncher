package quicklinks

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// placeholderRe matches the placeholders of a link (docs/quicklinks.md
// §5.2): {query} (or {argument}), {clipboard}, {date}, {time}, {datetime},
// each optionally with ":modifier" (":raw", or a date format).
var placeholderRe = regexp.MustCompile(`(?i)\{(query|argument|clipboard|datetime|date|time)(?::([^{}]*))?\}`)

// values are what the placeholders stand for when a link is opened.
type values struct {
	query string
	// clipboard returns the clipboard text; it is called only when the link
	// has {clipboard}.
	clipboard func() string
	now       time.Time
}

// placeholders reports which placeholders tmpl has.
func placeholders(tmpl string) (query, dynamic bool) {
	for _, m := range placeholderRe.FindAllStringSubmatch(tmpl, -1) {
		switch strings.ToLower(m[1]) {
		case "query", "argument":
			query = true
		default:
			dynamic = true
		}
	}
	return query, dynamic
}

// encodeComponent percent-encodes s as UTF-8 for any part of a URL: every
// byte except letters, digits and -_.~ is encoded, and a space is %20 (so
// it is right in a path as well as in a query).
func encodeComponent(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// expand replaces the placeholders of tmpl. With encode (a URL), the query
// and the clipboard are percent-encoded unless written with ":raw";
// dates are encoded too (a format may contain spaces). Unknown {…} are
// kept as they are.
func expand(tmpl string, encode bool, v values) string {
	return placeholderRe.ReplaceAllStringFunc(tmpl, func(s string) string {
		m := placeholderRe.FindStringSubmatch(s)
		name, mod := strings.ToLower(m[1]), m[2]
		raw := strings.EqualFold(strings.TrimSpace(mod), "raw")
		var out string
		switch name {
		case "query", "argument":
			out = v.query
		case "clipboard":
			if v.clipboard != nil {
				out = v.clipboard()
			}
		default:
			raw = false
			out = formatTime(v.now, name, mod)
		}
		if encode && !raw {
			out = encodeComponent(out)
		}
		return out
	})
}

// formatTime formats t for {date}, {time} and {datetime}, with an optional
// format of yyyy yy MM M dd d HH H mm ss (other characters are copied).
func formatTime(t time.Time, name, format string) string {
	format = strings.TrimSpace(format)
	if format == "" {
		switch name {
		case "date":
			format = "yyyy-MM-dd"
		case "time":
			format = "HH:mm"
		default:
			format = "yyyy-MM-dd HH:mm"
		}
	}
	var b strings.Builder
	tokens := []struct {
		tok string
		val func() string
	}{
		{"yyyy", func() string { return fmt.Sprintf("%04d", t.Year()) }},
		{"yy", func() string { return fmt.Sprintf("%02d", t.Year()%100) }},
		{"MM", func() string { return fmt.Sprintf("%02d", int(t.Month())) }},
		{"M", func() string { return fmt.Sprint(int(t.Month())) }},
		{"dd", func() string { return fmt.Sprintf("%02d", t.Day()) }},
		{"d", func() string { return fmt.Sprint(t.Day()) }},
		{"HH", func() string { return fmt.Sprintf("%02d", t.Hour()) }},
		{"H", func() string { return fmt.Sprint(t.Hour()) }},
		{"mm", func() string { return fmt.Sprintf("%02d", t.Minute()) }},
		{"ss", func() string { return fmt.Sprintf("%02d", t.Second()) }},
	}
outer:
	for i := 0; i < len(format); {
		for _, tk := range tokens {
			if strings.HasPrefix(format[i:], tk.tok) {
				b.WriteString(tk.val())
				i += len(tk.tok)
				continue outer
			}
		}
		b.WriteByte(format[i])
		i++
	}
	return b.String()
}

// linkKind is what a link opens.
type linkKind int

const (
	kindURL     linkKind = iota // a URL of any scheme
	kindPath                    // a file or folder
	kindProgram                 // a program with arguments
)

// target is a parsed link.
type target struct {
	kind linkKind
	// link is the URL or path, or the program (kindProgram).
	link string
	args string // kindProgram
}

var schemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]+:`)

// parseLink tells what a link (with its placeholders) opens
// (docs/quicklinks.md §5.1): a URL ("https:", "mailto:", "ms-settings:"…,
// or a bare domain such as "github.com/{query}", made https), a program
// with arguments (a quoted program, or a first word ending in .exe, .com,
// .bat or .cmd followed by arguments), or else a path.
func parseLink(link string) target {
	link = strings.TrimSpace(link)
	if rest, ok := strings.CutPrefix(link, `"`); ok {
		prog, args, _ := strings.Cut(rest, `"`)
		return target{kind: kindProgram, link: prog, args: strings.TrimSpace(args)}
	}
	if schemeRe.MatchString(link) {
		return target{kind: kindURL, link: link}
	}
	if u, ok := DetectURL(link); ok {
		return target{kind: kindURL, link: u}
	}
	if first, args, ok := strings.Cut(link, " "); ok {
		low := strings.ToLower(first)
		for _, ext := range []string{".exe", ".com", ".bat", ".cmd"} {
			if strings.HasSuffix(low, ext) {
				return target{kind: kindProgram, link: first, args: strings.TrimSpace(args)}
			}
		}
	}
	return target{kind: kindPath, link: link}
}

// resolve expands the placeholders of a parsed link: encoded in a URL, as
// they are in paths and command lines, whose %VARIABLES% are expanded.
func (t target) resolve(v values) target {
	if t.kind == kindURL {
		t.link = expand(t.link, true, v)
		return t
	}
	t.link = expandEnv(expand(t.link, false, v))
	t.args = expand(t.args, false, v)
	return t
}

// openDo is the action that opens a resolved link, with the program
// openWith when set (a URL or path is then its argument).
func (t target) openDo(openWith string) plugin.ActionDo {
	openWith = strings.TrimSpace(openWith)
	switch {
	case t.kind == kindProgram:
		return openAction(t.link, t.args)
	case openWith != "":
		return openAction(expandEnv(openWith), quoteArg(t.link))
	}
	return openAction(t.link, "")
}

// openAction is ActionOpen of target with arguments args.
func openAction(target, args string) plugin.ActionDo {
	do := plugin.ActionDo{Kind: plugin.ActionOpen, Target: target}
	if args != "" {
		do.Payload = plugin.MustPayload(plugin.OpenOptions{Args: args})
	}
	return do
}

// quoteArg quotes s as one command-line argument.
func quoteArg(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

var envRe = regexp.MustCompile(`%([A-Za-z_][A-Za-z0-9_()]*)%`)

// expandEnv expands %VARIABLE% as Windows does; unknown ones are kept.
func expandEnv(s string) string {
	return envRe.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := os.LookupEnv(m[1 : len(m)-1]); ok {
			return v
		}
		return m
	})
}

// homepage is the start page of a search URL template: its scheme and host.
func homepage(tmpl string) string {
	u, err := url.Parse(strings.ReplaceAll(tmpl, "{query}", ""))
	if err != nil || u.Host == "" {
		return tmpl
	}
	return u.Scheme + "://" + u.Host + "/"
}
