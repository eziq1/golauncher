// Package engines holds the translation engines of builtin.translator
// (docs/translator.md §3): one file per engine, behind the Engine interface.
//
// Language codes are canonical: Google's codes ("en", "zh-CN", "zh-TW",
// "ja", …), which each engine maps to its own. An empty Request.From asks
// the engine to detect the source language; Result.From is what it found
// (canonical, "" when it did not say).
//
// Engines never log or keep the text: the plugin logs lengths and timings
// only.
package engines

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Request is a text to translate.
type Request struct {
	Text string
	// From is the source language, "" to detect it.
	From string
	// To is the target language.
	To string
}

// Result is a translation.
type Result struct {
	Text string
	// From is the source language: the one detected by the engine, or the
	// one asked for. "" when unknown.
	From string
	// Dict is dictionary information for a single word, when the engine
	// gives some (phonetics, parts of speech, a few meanings).
	Dict *Dict
}

// Dict is a dictionary entry for a single word.
type Dict struct {
	// Phonetic is the word's pronunciation, e.g. "ˌserənˈdipədē".
	Phonetic string `json:"phonetic,omitempty"`
	// Entries are the word's translations by part of speech.
	Entries []DictEntry `json:"entries,omitempty"`
	// Definitions explain the word in its own language, by part of speech.
	Definitions []DictEntry `json:"definitions,omitempty"`
}

// DictEntry is one part of speech of a word with its meanings.
type DictEntry struct {
	// POS is the part of speech as the engine names it ("noun", "adj.").
	POS      string   `json:"pos,omitempty"`
	Meanings []string `json:"meanings"`
}

// Dictionary limits: a few meanings are shown, not a dictionary page.
const (
	maxDictEntries  = 4
	maxDictMeanings = 5
	// maxWordRunes is the longest text looked up as a word.
	maxWordRunes = 40
	// maxCJKWordRunes is the longest Chinese, Japanese or Korean text looked
	// up as a word.
	maxCJKWordRunes = 4
)

// Empty reports whether d holds nothing to show.
func (d *Dict) Empty() bool {
	return d == nil || d.Phonetic == "" && len(d.Entries) == 0 && len(d.Definitions) == 0
}

// IsWord reports whether text is a single word worth a dictionary lookup:
// no spaces, at most a few dozen characters, some letters. Chinese and
// Japanese have no spaces between words: there it is a short text.
func IsWord(text string) bool {
	t := strings.TrimSpace(text)
	n := utf8.RuneCountInString(t)
	if t == "" || n > maxWordRunes || strings.ContainsFunc(t, unicode.IsSpace) || !strings.ContainsFunc(t, unicode.IsLetter) {
		return false
	}
	if strings.ContainsFunc(t, func(r rune) bool {
		return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
	}) {
		return n <= maxCJKWordRunes
	}
	return !strings.ContainsFunc(t, unicode.IsPunct) || strings.ContainsAny(t, "-'")
}

// addEntry appends a part of speech with its first meanings, up to the
// limits; empty meanings are dropped.
func addEntry(list []DictEntry, pos string, meanings []string) []DictEntry {
	if len(list) >= maxDictEntries {
		return list
	}
	var m []string
	for _, s := range meanings {
		if s = strings.TrimSpace(s); s != "" && len(m) < maxDictMeanings {
			m = append(m, s)
		}
	}
	if len(m) == 0 {
		return list
	}
	return append(list, DictEntry{POS: strings.TrimSpace(pos), Meanings: m})
}

// Engine translates text.
type Engine interface {
	// ID is the engine's stable name ("google", "deepl", …).
	ID() string
	Translate(ctx context.Context, r Request) (Result, error)
}

// Env is what engines need from the plugin.
type Env struct {
	// Client makes the requests (system proxy settings, a timeout).
	Client *http.Client
	// Go runs a function on a new goroutine (plugin.Host.Go), for requests
	// made in parallel (long text in pieces). Nil runs them one by one.
	Go func(func())
}

// Errors engines return, wrapped with details. The plugin shows a message
// for each.
var (
	// ErrQuota: the free quota or the rate limit is exhausted.
	ErrQuota = errors.New("quota exceeded")
	// ErrAuth: the key (or app ID and secret) was rejected.
	ErrAuth = errors.New("key rejected")
	// ErrLanguage: the engine does not support the language (pair).
	ErrLanguage = errors.New("language not supported")
	// ErrNoKey: the engine needs a key that is not set.
	ErrNoKey = errors.New("no key")
	// ErrUnavailable: the engine cannot be used (a local server is not
	// running).
	ErrUnavailable = errors.New("engine unavailable")
)

// maxBody is the most read from a response.
const maxBody = 1 << 20

// doJSON sends req and decodes a 200 response's JSON body into v. Other
// statuses become errors through statusError (or check, when not nil, which
// sees the status and body first).
func doJSON(c *http.Client, req *http.Request, v any, check func(status int, body []byte) error) error {
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return err
	}
	if check != nil {
		if err := check(resp.StatusCode, body); err != nil {
			return err
		}
	}
	if resp.StatusCode != http.StatusOK {
		return statusError(resp.StatusCode)
	}
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("bad response: %w", err)
	}
	return nil
}

// statusError is the error for an HTTP status other than 200.
func statusError(status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w (HTTP %d)", ErrAuth, status)
	case http.StatusTooManyRequests, 456: // 456: DeepL's quota exceeded
		return fmt.Errorf("%w (HTTP %d)", ErrQuota, status)
	}
	return fmt.Errorf("HTTP %d", status)
}

// newRequest makes a request with a JSON Accept header.
func newRequest(ctx context.Context, method, u string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// Split cuts text into pieces of at most max bytes, at the end of a
// sentence where possible, else at a space, else at a character boundary.
// Joining the pieces gives the text back.
func Split(text string, max int) []string {
	if max <= 0 || len(text) <= max {
		return []string{text}
	}
	var out []string
	for len(text) > max {
		cut := lastBreak(text[:max+1], isSentenceEnd)
		if cut <= 0 {
			cut = lastBreak(text[:max+1], unicode.IsSpace)
		}
		if cut <= 0 {
			cut = max
			for cut > 0 && !utf8.RuneStart(text[cut]) {
				cut--
			}
			if cut == 0 { // a single rune longer than max: keep it whole
				_, cut = utf8.DecodeRuneInString(text)
			}
		}
		out = append(out, text[:cut])
		text = text[cut:]
	}
	if text != "" {
		out = append(out, text)
	}
	return out
}

// lastBreak is the index just after the last rune of s for which is
// reports true, short of s's last byte (s is one byte longer than the
// limit); 0 when none.
func lastBreak(s string, is func(rune) bool) int {
	best := 0
	for i, r := range s {
		if end := i + utf8.RuneLen(r); is(r) && end < len(s) {
			best = end
		}
	}
	return best
}

func isSentenceEnd(r rune) bool {
	switch r {
	case '.', '!', '?', ';', '\n', '。', '！', '？', '；', '…':
		return true
	}
	return false
}

// translatePieces translates the pieces of text (Split with max) with one,
// in parallel when env.Go is set, and joins the results. The first error
// wins; the source language is the first piece's.
func translatePieces(env Env, ctx context.Context, r Request, max int, one func(context.Context, Request) (Result, error)) (Result, error) {
	pieces := Split(r.Text, max)
	if len(pieces) == 1 {
		return one(ctx, r)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]Result, len(pieces))
	errs := make([]error, len(pieces))
	var wg sync.WaitGroup
	for i, piece := range pieces {
		run := func() {
			defer wg.Done()
			// Leading and trailing spaces are kept around the translation.
			trimmed := strings.TrimSpace(piece)
			if trimmed == "" {
				results[i].Text = piece
				return
			}
			pr := r
			pr.Text = trimmed
			res, err := one(ctx, pr)
			if err != nil {
				errs[i] = err
				cancel()
				return
			}
			lead := piece[:strings.Index(piece, trimmed)]
			trail := piece[len(lead)+len(trimmed):]
			res.Text = lead + res.Text + trail
			results[i] = res
		}
		wg.Add(1)
		if env.Go != nil {
			env.Go(run)
		} else {
			run()
		}
	}
	wg.Wait()
	out := Result{From: r.From}
	var b strings.Builder
	for i := range pieces {
		if errs[i] != nil && !errors.Is(errs[i], context.Canceled) {
			return Result{}, errs[i]
		}
		b.WriteString(results[i].Text)
		if out.From == "" {
			out.From = results[i].From
		}
	}
	for _, err := range errs {
		if err != nil {
			return Result{}, err
		}
	}
	out.Text = b.String()
	return out, nil
}
