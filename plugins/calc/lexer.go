package calc

import (
	"fmt"
	"math/big"
	"strings"
	"unicode"
	"unicode/utf8"
)

// tokKind is the kind of a lexical token.
type tokKind uint8

const (
	tokEOF tokKind = iota
	tokNum
	tokIdent
	tokOp
	tokLParen
	tokRParen
	tokComma
)

// token is a lexical token. text is its normalized spelling: operators use
// their canonical form and identifiers are lower-cased.
type token struct {
	kind tokKind
	text string
	num  *big.Float // set for tokNum
}

// opAliases maps operator characters to their canonical spelling.
var opAliases = map[rune]string{
	'+': "+", '-': "-", '−': "-",
	'*': "*", '×': "*",
	'/': "/", '÷': "/",
	'^': "^", '%': "%", '!': "!",
}

// maxExpDigits bounds the exponent of a decimal literal so that parsing
// stays cheap.
const maxExpDigits = 6

// lex splits s into tokens, ending with a tokEOF token.
func lex(s string) ([]token, error) {
	var toks []token
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case unicode.IsSpace(r):
			i += size
		case isDigit(r) || r == '.' && i+1 < len(s) && isDigit(rune(s[i+1])):
			t, n, err := lexNumber(s[i:])
			if err != nil {
				return nil, err
			}
			toks = append(toks, t)
			i += n
		case unicode.IsLetter(r) || r == '_':
			j := i + size
			for j < len(s) {
				r2, sz := utf8.DecodeRuneInString(s[j:])
				if !unicode.IsLetter(r2) && !isDigit(r2) && r2 != '_' {
					break
				}
				j += sz
			}
			toks = append(toks, token{kind: tokIdent, text: strings.ToLower(s[i:j])})
			i = j
		case r == '(':
			toks = append(toks, token{kind: tokLParen, text: "("})
			i += size
		case r == ')':
			toks = append(toks, token{kind: tokRParen, text: ")"})
			i += size
		case r == ',':
			toks = append(toks, token{kind: tokComma, text: ","})
			i += size
		case strings.HasPrefix(s[i:], "**"):
			toks = append(toks, token{kind: tokOp, text: "^"})
			i += 2
		default:
			op, ok := opAliases[r]
			if !ok {
				return nil, fmt.Errorf("%w: unexpected %q", errSyntax, r)
			}
			toks = append(toks, token{kind: tokOp, text: op})
			i += size
		}
	}
	return append(toks, token{kind: tokEOF}), nil
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// digitVal returns the value of hex digit c, or 99 if c is not one.
func digitVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return 99
}

// prefixBase returns the base selected by the letter after a leading "0".
func prefixBase(c byte) int {
	switch c {
	case 'x', 'X':
		return 16
	case 'b', 'B':
		return 2
	case 'o', 'O':
		return 8
	}
	return 0
}

// lexNumber scans the number literal at the start of s and returns it with
// its length in bytes.
func lexNumber(s string) (token, int, error) {
	if len(s) > 2 && s[0] == '0' {
		if base := prefixBase(s[1]); base != 0 {
			j := 2
			for j < len(s) && digitVal(s[j]) < base {
				j++
			}
			if j > 2 {
				n, _ := new(big.Int).SetString(s[2:j], base)
				return token{kind: tokNum, text: s[:j], num: newF().SetInt(n)}, j, nil
			}
		}
	}
	j := 0
	digits := func() {
		for j < len(s) && isDigit(rune(s[j])) {
			j++
		}
	}
	digits()
	if j < len(s) && s[j] == '.' {
		j++
		digits()
	}
	if j < len(s) && (s[j] == 'e' || s[j] == 'E') {
		k := j + 1
		if k < len(s) && (s[k] == '+' || s[k] == '-') {
			k++
		}
		if k < len(s) && isDigit(rune(s[k])) {
			start := k
			for k < len(s) && isDigit(rune(s[k])) {
				k++
			}
			if k-start > maxExpDigits {
				return token{}, 0, fmt.Errorf("%w: exponent too large", errRange)
			}
			j = k
		}
	}
	// The scanned text is always a valid decimal literal.
	f, _ := newF().SetString(s[:j])
	return token{kind: tokNum, text: s[:j], num: f}, j, nil
}
