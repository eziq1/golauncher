package engines

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Youdao is Youdao's text translation API (ai.youdao.com, openapi.youdao.com/api):
// an application ID and secret, requests signed with SHA-256 (signType
// v3). For a single word it also returns dictionary data (phonetics,
// meanings by part of speech).
type Youdao struct {
	Env
	AppKey, Secret string
	// BaseURL replaces https://openapi.youdao.com (tests).
	BaseURL string
	// Now and Salt make the signature reproducible (tests); nil for the
	// clock and random salts.
	Now  func() time.Time
	Salt func() string
}

const (
	youdaoBase = "https://openapi.youdao.com"
	// youdaoMax is the most text of one request (bytes; the API takes
	// 5,000 characters).
	youdaoMax = 4500
)

// ID implements Engine.
func (y *Youdao) ID() string { return "youdao" }

type youdaoResponse struct {
	ErrorCode   string   `json:"errorCode"`
	Translation []string `json:"translation"`
	// L is the language pair, e.g. "en2zh-CHS".
	L     string `json:"l"`
	Basic *struct {
		Phonetic   string   `json:"phonetic"`
		USPhonetic string   `json:"us-phonetic"`
		Explains   []string `json:"explains"`
	} `json:"basic"`
}

// Translate implements Engine.
func (y *Youdao) Translate(ctx context.Context, r Request) (Result, error) {
	if strings.TrimSpace(y.AppKey) == "" || strings.TrimSpace(y.Secret) == "" {
		return Result{}, ErrNoKey
	}
	return translatePieces(y.Env, ctx, r, youdaoMax, y.one)
}

func (y *Youdao) one(ctx context.Context, r Request) (Result, error) {
	base := y.BaseURL
	if base == "" {
		base = youdaoBase
	}
	key, secret := strings.TrimSpace(y.AppKey), strings.TrimSpace(y.Secret)
	salt := randomSalt()
	if y.Salt != nil {
		salt = y.Salt()
	}
	now := time.Now()
	if y.Now != nil {
		now = y.Now()
	}
	curtime := strconv.FormatInt(now.Unix(), 10)
	from := youdaoCode(r.From)
	if from == "" {
		from = "auto"
	}
	form := url.Values{
		"q": {r.Text}, "from": {from}, "to": {youdaoCode(r.To)}, "appKey": {key}, "salt": {salt},
		"sign": {YoudaoSign(key, r.Text, salt, curtime, secret)}, "signType": {"v3"}, "curtime": {curtime},
	}
	req, err := newRequest(ctx, http.MethodPost, base+"/api", strings.NewReader(form.Encode()))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var resp youdaoResponse
	if err := doJSON(y.Client, req, &resp, nil); err != nil {
		return Result{}, err
	}
	if err := youdaoError(resp.ErrorCode); err != nil {
		return Result{}, err
	}
	if len(resp.Translation) == 0 {
		return Result{}, errors.New("youdao: no translation in the response")
	}
	src, _, _ := strings.Cut(resp.L, "2")
	res := Result{Text: strings.Join(resp.Translation, "\n"), From: firstNonEmpty(r.From, Canon(src))}
	if b := resp.Basic; b != nil {
		d := &Dict{Phonetic: firstNonEmpty(b.USPhonetic, b.Phonetic)}
		for _, e := range b.Explains {
			pos, meanings := splitExplain(e)
			d.Entries = addEntry(d.Entries, pos, meanings)
		}
		if !d.Empty() {
			res.Dict = d
		}
	}
	return res, nil
}

// YoudaoSign is the v3 signature: SHA-256 of appKey, the input, salt,
// curtime and the secret, where the input is the text itself up to 20
// characters, else its first 10, its length and its last 10.
func YoudaoSign(appKey, q, salt, curtime, secret string) string {
	rs := []rune(q)
	input := q
	if len(rs) > 20 {
		input = string(rs[:10]) + strconv.Itoa(len(rs)) + string(rs[len(rs)-10:])
	}
	sum := sha256.Sum256([]byte(appKey + input + salt + curtime + secret))
	return hex.EncodeToString(sum[:])
}

// splitExplain splits a Youdao explanation, "adj. 好的；优秀的", into its
// part of speech and meanings.
func splitExplain(s string) (string, []string) {
	s = strings.TrimSpace(s)
	pos := ""
	if i := strings.Index(s, ". "); i > 0 && i <= 6 && !strings.ContainsAny(s[:i], " ；;") {
		pos, s = s[:i+1], s[i+2:]
	}
	return pos, strings.FieldsFunc(s, func(r rune) bool { return r == '；' || r == ';' })
}

// youdaoError maps Youdao's error codes.
func youdaoError(code string) error {
	switch code {
	case "", "0":
		return nil
	case "108", "111", "202", "110":
		return fmt.Errorf("%w (Youdao %s)", ErrAuth, code)
	case "401", "411", "412":
		return fmt.Errorf("%w (Youdao %s)", ErrQuota, code)
	case "102":
		return fmt.Errorf("%w (Youdao %s)", ErrLanguage, code)
	}
	return fmt.Errorf("youdao error %s", code)
}

// youdaoCode is Youdao's language code.
func youdaoCode(code string) string {
	switch code {
	case Simplified:
		return "zh-CHS"
	case Traditional:
		return "zh-CHT"
	}
	return code
}

// randomSalt is a random hexadecimal string.
func randomSalt() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
