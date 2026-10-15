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
	// tokCur is a currency sign written before an amount ("$100", "€5").
	tokCur
	// tokConv is a conversion keyword other than the words in, to and as:
	// "=>", "→", "换成", "转换为".
	tokConv
)

// token is a lexical token. text is its normalized spelling: operators use
// their canonical form and identifiers are lower-cased; raw is the spelling
// as typed (unit symbols are case-sensitive: "Mb" is not "MB").
type token struct {
	kind tokKind
	text string
	raw  string
	num  *big.Float // set for tokNum
}

// opAliases maps operator characters to their canonical spelling.
var opAliases = map[rune]string{
	'+': "+", '-': "-", '−': "-",
	'*': "*", '×': "*", '·': "*", '⋅': "*",
	'/': "/", '÷': "/",
	'^': "^", '%': "%", '!': "!",
}

// currencySigns are lexed as tokCur.
var currencySigns = map[rune]bool{'$': true, '€': true, '£': true, '¥': true, '￥': true, '₩': true, '₹': true, '₽': true, '฿': true, '₫': true, '₱': true, '₺': true}

// unitSigns are symbol characters that are units on their own.
var unitSigns = map[rune]bool{'℃': true, '℉': true, '°': true, '′': true, '″': true}

// convWords are the Chinese conversion keywords; they may be written
// without spaces ("3斤换成kg"), so identifiers stop in front of them.
var convWords = []string{"转换为", "转换成", "换算成", "换算为", "换成", "转成", "转为", "=>", "→"}

// specialIdents are unit spellings that contain operator characters,
// matched case-insensitively in front of the general rules.
var specialIdents = []struct{ spelling, unit string }{
	{"l/100km", "L/100km"}, {"l/100 km", "L/100km"},
	{"lbf-ft", "lbf·ft"}, {"ft-lbf", "lbf·ft"}, {"lb-ft", "lbf·ft"}, {"ft-lb", "lbf·ft"},
	{"kgf-m", "kgf·m"}, {"n-m", "N·m"},
}

// maxExpDigits bounds the exponent of a decimal literal so that parsing
// stays cheap.
const maxExpDigits = 6

// convWordAt returns the conversion keyword at the start of s, if any.
func convWordAt(s string) string {
	for _, w := range convWords {
		if strings.HasPrefix(s, w) {
			return w
		}
	}
	return ""
}

// specialAt returns the special identifier at the start of s and its
// length, if it is not followed by a letter.
func specialAt(s string) (string, int) {
	for _, sp := range specialIdents {
		n := len(sp.spelling)
		if len(s) >= n && strings.EqualFold(s[:n], sp.spelling) {
			if r, _ := utf8.DecodeRuneInString(s[n:]); n == len(s) || !unicode.IsLetter(r) {
				return sp.unit, n
			}
		}
	}
	return "", 0
}

func isIdentRune(r rune) bool { return unicode.IsLetter(r) || isDigit(r) || r == '_' }

// lex splits s into tokens, ending with a tokEOF token.
func lex(s string) ([]token, error) {
	var toks []token
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if w := convWordAt(s[i:]); w != "" {
			toks = append(toks, token{kind: tokConv, text: w, raw: w})
			i += len(w)
			continue
		}
		if unicode.IsLetter(r) {
			if u, n := specialAt(s[i:]); n > 0 {
				toks = append(toks, token{kind: tokIdent, text: strings.ToLower(u), raw: u})
				i += n
				continue
			}
		}
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
		case r == 'Δ':
			toks = append(toks, token{kind: tokOp, text: "Δ", raw: "Δ"})
			i += size
		case unicode.IsLetter(r) || r == '_':
			j := i + size
			for j < len(s) {
				r2, sz := utf8.DecodeRuneInString(s[j:])
				if !isIdentRune(r2) || convWordAt(s[j:]) != "" {
					break
				}
				j += sz
			}
			toks = append(toks, token{kind: tokIdent, text: strings.ToLower(s[i:j]), raw: s[i:j]})
			i = j
		case unitSigns[r]:
			j := i + size
			if r == '°' && j < len(s) && strings.ContainsRune("CFcf", rune(s[j])) &&
				(j+1 == len(s) || !isIdentRune(rune(s[j+1]))) {
				j++
			}
			toks = append(toks, token{kind: tokIdent, text: strings.ToLower(s[i:j]), raw: s[i:j]})
			i = j
		case currencySigns[r]:
			toks = append(toks, token{kind: tokCur, text: string(r), raw: string(r)})
			i += size
		case r == '²' || r == '³':
			n := int64(2)
			if r == '³' {
				n = 3
			}
			toks = append(toks, token{kind: tokOp, text: "^", raw: string(r)},
				token{kind: tokNum, text: fmt.Sprint(n), raw: string(r), num: newF().SetInt64(n)})
			i += size
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
			toks = append(toks, token{kind: tokOp, text: op, raw: string(r)})
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
				return token{kind: tokNum, text: s[:j], raw: s[:j], num: newF().SetInt(n)}, j, nil
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
	return token{kind: tokNum, text: s[:j], raw: s[:j], num: f}, j, nil
}
