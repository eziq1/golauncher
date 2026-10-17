package engines

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Baidu is Baidu Translate's general text API (fanyi-api.baidu.com): an
// APP ID and a secret key, requests signed with MD5.
type Baidu struct {
	Env
	AppID, Secret string
	// BaseURL replaces https://fanyi-api.baidu.com (tests).
	BaseURL string
	// Salt makes the signature reproducible (tests); nil for random salts.
	Salt func() string
}

const (
	baiduBase = "https://fanyi-api.baidu.com"
	// baiduMax is the most text of one request (bytes; the API takes 6,000).
	baiduMax = 5000
)

// ID implements Engine.
func (b *Baidu) ID() string { return "baidu" }

type baiduResponse struct {
	From        string `json:"from"`
	TransResult []struct {
		Dst string `json:"dst"`
	} `json:"trans_result"`
	// ErrorCode is a string, or sometimes a number.
	ErrorCode jsontext.Value `json:"error_code"`
}

// Translate implements Engine.
func (b *Baidu) Translate(ctx context.Context, r Request) (Result, error) {
	if strings.TrimSpace(b.AppID) == "" || strings.TrimSpace(b.Secret) == "" {
		return Result{}, ErrNoKey
	}
	return translatePieces(b.Env, ctx, r, baiduMax, b.one)
}

func (b *Baidu) one(ctx context.Context, r Request) (Result, error) {
	base := b.BaseURL
	if base == "" {
		base = baiduBase
	}
	appID, secret := strings.TrimSpace(b.AppID), strings.TrimSpace(b.Secret)
	salt := randomSalt()
	if b.Salt != nil {
		salt = b.Salt()
	}
	from := baiduCode(r.From)
	if from == "" {
		from = "auto"
	}
	form := url.Values{"q": {r.Text}, "from": {from}, "to": {baiduCode(r.To)}, "appid": {appID}, "salt": {salt},
		"sign": {BaiduSign(appID, r.Text, salt, secret)}}
	req, err := newRequest(ctx, http.MethodPost, base+"/api/trans/vip/translate", strings.NewReader(form.Encode()))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var resp baiduResponse
	if err := doJSON(b.Client, req, &resp, nil); err != nil {
		return Result{}, err
	}
	if err := baiduError(jsonInt(resp.ErrorCode)); err != nil {
		return Result{}, err
	}
	if len(resp.TransResult) == 0 {
		return Result{}, errors.New("baidu: no translation in the response")
	}
	// One result per line of the text.
	lines := make([]string, len(resp.TransResult))
	for i, t := range resp.TransResult {
		lines[i] = t.Dst
	}
	return Result{Text: strings.Join(lines, "\n"), From: firstNonEmpty(Canon(fromBaidu(resp.From)), r.From)}, nil
}

// BaiduSign is the signature: MD5 of the APP ID, the text, salt and the
// secret key, in lower-case hexadecimal.
func BaiduSign(appID, q, salt, secret string) string {
	sum := md5.Sum([]byte(appID + q + salt + secret))
	return hex.EncodeToString(sum[:])
}

// baiduError maps Baidu's error codes (52000 is success).
func baiduError(code int) error {
	switch code {
	case 0, 52000:
		return nil
	case 52003, 54001, 58002, 90107:
		return fmt.Errorf("%w (Baidu %d)", ErrAuth, code)
	case 54003, 54004, 54005:
		return fmt.Errorf("%w (Baidu %d)", ErrQuota, code)
	case 58001:
		return fmt.Errorf("%w (Baidu %d)", ErrLanguage, code)
	}
	return fmt.Errorf("baidu error %d", code)
}

// baiduCodes are Baidu's own language codes where they differ.
var baiduCodes = map[string]string{
	Simplified: "zh", Traditional: "cht", Japanese: "jp", Korean: "kor", "fr": "fra", "es": "spa", "ar": "ara",
	"vi": "vie", "sv": "swe", "uk": "ukr",
}

// baiduCode is Baidu's language code.
func baiduCode(code string) string {
	if c, ok := baiduCodes[code]; ok {
		return c
	}
	return code
}

// fromBaidu turns a code Baidu returned into a canonical one.
func fromBaidu(code string) string {
	for canon, c := range baiduCodes {
		if c == code {
			return canon
		}
	}
	return code
}
