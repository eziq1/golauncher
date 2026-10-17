package everything

import (
	"cmp"
	"path"
	"slices"
	"strings"
)

// The macOS backend (everything_darwin.go) answers the same Query with
// Spotlight's mdfind instead of Everything. The plugin speaks Everything's
// search syntax, so this file translates it: the text is parsed into
// clauses, the clauses Spotlight can express become an mdfind query that
// narrows the candidates, and every clause is then checked exactly on each
// candidate (filter), so the results do not depend on how Spotlight reads
// the query. It is plain Go and builds everywhere, so it is tested on every
// platform.
//
// Supported: space-separated terms that must all match the name; "quoted
// phrases"; !negation; <a|b> and a|b alternatives; terms with a slash or a
// backslash, matched against the full path (a leading / makes it a prefix,
// used as mdfind -onlyin when positive; a leading backslash, as in the
// plugin's `\node_modules\`, matches anywhere); the wildcards * and ? matching the
// whole name; ext:a;b; file: and folder:; MatchCase and MatchPath.
// Ignored: WholeWord, Regex and the other Everything functions (size:,
// dm:, …), which are searched for as plain text.

// termKind is what a term of the search text matches.
type termKind int

const (
	termName   termKind = iota // a substring of the name
	termWild                   // a wildcard pattern of the whole name
	termPath                   // a substring (or, with a leading /, a prefix) of the path
	termExt                    // one of the extensions in exts
	termFolder                 // folders only
	termFile                   // files only
)

type term struct {
	kind termKind
	text string   // termName, termWild, termPath (with slashes)
	abs  bool     // termPath starting with a slash: a prefix of the path
	exts []string // termExt, lower case without dots
}

// clause is one AND-ed part of the search: any of alts matches, or with
// neg none does.
type clause struct {
	neg  bool
	alts []term
}

// spotlightQuery is a translated Query.
type spotlightQuery struct {
	clauses   []clause
	matchCase bool
	matchPath bool
}

// parseSpotlight parses q.Text.
func parseSpotlight(q Query) spotlightQuery {
	sq := spotlightQuery{matchCase: q.MatchCase, matchPath: q.MatchPath}
	text := q.Text
	if strings.Contains(text, "|") && !strings.ContainsAny(text, "<>") {
		text = "<" + text + ">"
	}
	for _, f := range splitFields(text, ' ') {
		c := clause{}
		if strings.HasPrefix(f, "!") {
			c.neg, f = true, f[1:]
		}
		parts := []string{f}
		if strings.HasPrefix(f, "<") && strings.HasSuffix(f, ">") && len(f) >= 2 {
			parts = splitFields(f[1:len(f)-1], '|')
		}
		var extra []clause // folder:name is two clauses
		for _, p := range parts {
			// A group may hold several space-separated words: <a b|c> is
			// "a b" or "c"; such an alternative is kept as one name term.
			t, rest, ok := parseTerm(p)
			if !ok {
				continue
			}
			c.alts = append(c.alts, t)
			if rest != "" && len(parts) == 1 && !c.neg {
				if rt, _, ok := parseTerm(rest); ok {
					extra = append(extra, clause{alts: []term{rt}})
				}
			}
		}
		if len(c.alts) > 0 {
			sq.clauses = append(sq.clauses, c)
		}
		sq.clauses = append(sq.clauses, extra...)
	}
	return sq
}

// parseTerm parses one term; rest is the text after a file: or folder:
// prefix ("folder:src" is folders named like src).
func parseTerm(s string) (t term, rest string, ok bool) {
	s = strings.TrimSpace(s)
	unq := strings.ReplaceAll(s, `"`, "")
	lower := strings.ToLower(unq)
	switch {
	case unq == "":
		return term{}, "", false
	case strings.HasPrefix(lower, "ext:"):
		var exts []string
		for e := range strings.SplitSeq(lower[len("ext:"):], ";") {
			if e = strings.TrimPrefix(strings.TrimSpace(e), "."); e != "" {
				exts = append(exts, e)
			}
		}
		if len(exts) == 0 {
			return term{}, "", false
		}
		return term{kind: termExt, exts: exts}, "", true
	case strings.HasPrefix(lower, "folder:"):
		return term{kind: termFolder}, unq[len("folder:"):], true
	case strings.HasPrefix(lower, "file:"):
		return term{kind: termFile}, unq[len("file:"):], true
	case strings.ContainsAny(unq, `\/`):
		// Everything's segments (`\node_modules\`) match anywhere; only a
		// POSIX path, starting with a slash, is a prefix.
		return term{kind: termPath, text: strings.ReplaceAll(unq, `\`, "/"), abs: strings.HasPrefix(unq, "/")}, "", true
	case strings.ContainsAny(unq, "*?"):
		return term{kind: termWild, text: unq}, "", true
	}
	return term{kind: termName, text: unq}, "", true
}

// splitFields splits s at sep outside quotes and <> groups, dropping empty
// fields; quotes are kept.
func splitFields(s string, sep rune) []string {
	var out []string
	var cur strings.Builder
	quoted, depth := false, 0
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
		}
		cur.Reset()
	}
	for _, c := range s {
		switch {
		case c == '"':
			quoted = !quoted
		case c == '<' && !quoted:
			depth++
		case c == '>' && !quoted && depth > 0:
			depth--
		case c == sep && !quoted && depth == 0:
			flush()
			continue
		}
		cur.WriteRune(c)
	}
	flush()
	return out
}

// escapeSpotlight escapes s for a quoted string of a Spotlight query:
// quotes, backslashes and, unless wild, asterisks (Spotlight's wildcard).
func escapeSpotlight(s string, wild bool) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c == '"' || c == '\\' || c == '\'':
			b.WriteByte('\\')
		case c == '*' && !wild:
			b.WriteByte('\\')
		case c == '?' && wild:
			c = '*' // Spotlight has no single-character wildcard; filter checks
		}
		b.WriteRune(c)
	}
	return b.String()
}

// mdfindQuery is the Spotlight query narrowing the candidates, and the
// folder to search in (mdfind -onlyin), "" for everywhere. Every result
// still goes through filter.
func (sq spotlightQuery) mdfindQuery() (query, onlyIn string) {
	mod := "cd"
	if sq.matchCase {
		mod = "d"
	}
	name := func(pattern string) string { return `kMDItemFSName == "` + pattern + `"` + mod }
	var and, names []string
	for _, c := range sq.clauses {
		if c.neg {
			continue // negations only filter
		}
		var or []string
		filterOnly := false // the clause cannot narrow the candidates
		for _, t := range c.alts {
			switch t.kind {
			case termName:
				if sq.matchPath {
					// The words may be in a folder's name: any word in the
					// name finds the candidates, filter checks the path.
					names = append(names, name("*"+escapeSpotlight(t.text, false)+"*"))
					filterOnly = true
					continue
				}
				or = append(or, name("*"+escapeSpotlight(t.text, false)+"*"))
			case termWild:
				or = append(or, name(escapeSpotlight(t.text, true)))
			case termExt:
				for _, e := range t.exts {
					or = append(or, `kMDItemFSName == "*.`+escapeSpotlight(e, false)+`"c`)
				}
			case termFolder:
				or = append(or, `kMDItemContentType == "public.folder"`)
			case termFile:
				or = append(or, `kMDItemContentType != "public.folder"`)
			case termPath:
				switch {
				case len(c.alts) > 1:
					filterOnly = true // a path in an alternative
				case t.abs:
					if onlyIn == "" {
						onlyIn = cmp.Or(strings.TrimRight(t.text, "/"), "/")
					}
					filterOnly = true
				default:
					// docs/report: the name contains the last element.
					last := path.Base(strings.TrimRight(t.text, "/"))
					if last == "" || last == "." || last == "/" {
						filterOnly = true
						continue
					}
					or = append(or, name("*"+escapeSpotlight(last, false)+"*"))
				}
			}
		}
		switch {
		case filterOnly || len(or) == 0:
		case len(or) == 1:
			and = append(and, or[0])
		default:
			and = append(and, "("+strings.Join(or, " || ")+")")
		}
	}
	if len(names) > 0 {
		if len(names) == 1 {
			and = append(and, names[0])
		} else {
			and = append(and, "("+strings.Join(names, " || ")+")")
		}
	}
	if len(and) == 0 {
		and = []string{`kMDItemFSName == "*"`}
	}
	return strings.Join(and, " && "), onlyIn
}

// filter reports whether the file at p (a folder with dir) matches every
// clause.
func (sq spotlightQuery) filter(p string, dir bool) bool {
	fold := func(s string) string {
		if sq.matchCase {
			return s
		}
		return strings.ToLower(s)
	}
	name := path.Base(p)
	for _, c := range sq.clauses {
		ok := false
		for _, t := range c.alts {
			if t.matches(p, name, dir, sq.matchPath, fold) {
				ok = true
				break
			}
		}
		if ok == c.neg {
			return false
		}
	}
	return true
}

func (t term) matches(p, name string, dir, matchPath bool, fold func(string) string) bool {
	switch t.kind {
	case termName:
		hay := name
		if matchPath {
			hay = p
		}
		return strings.Contains(fold(hay), fold(t.text))
	case termWild:
		ok, _ := path.Match(fold(t.text), fold(name))
		return ok
	case termPath:
		// Folders match their own rules: "/node_modules/" matches the
		// node_modules folder itself.
		hay := strings.ToLower(p) + "/"
		pat := strings.ToLower(t.text)
		if t.abs {
			return strings.HasPrefix(hay, strings.TrimRight(pat, "/")+"/")
		}
		return strings.Contains(hay, pat)
	case termExt:
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
		return !dir && slices.Contains(t.exts, ext)
	case termFolder:
		return dir
	case termFile:
		return !dir
	}
	return false
}

// sortResults sorts res in the order o; equal keys keep their order.
func sortResults(res []Result, o SortOrder) {
	var key func(a, b Result) int
	switch o {
	case SortNameDescending:
		key = func(a, b Result) int { return -cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) }
	case SortPathAscending:
		key = func(a, b Result) int { return cmp.Compare(strings.ToLower(a.Path), strings.ToLower(b.Path)) }
	case SortPathDescending:
		key = func(a, b Result) int { return -cmp.Compare(strings.ToLower(a.Path), strings.ToLower(b.Path)) }
	case SortSizeAscending:
		key = func(a, b Result) int { return cmp.Compare(a.Size, b.Size) }
	case SortSizeDescending:
		key = func(a, b Result) int { return -cmp.Compare(a.Size, b.Size) }
	case SortDateModifiedAscending:
		key = func(a, b Result) int { return a.Modified.Compare(b.Modified) }
	case SortDateModifiedDescending:
		key = func(a, b Result) int { return -a.Modified.Compare(b.Modified) }
	case SortRunCountDescending:
		return // unknown to Spotlight: keep its order
	default:
		key = func(a, b Result) int { return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) }
	}
	slices.SortStableFunc(res, key)
}

// splitNUL splits the output of mdfind -0 (paths ending in NUL bytes) for a
// bufio.Scanner.
func splitNUL(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := slices.Index(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
