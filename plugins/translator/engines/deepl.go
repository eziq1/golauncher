package engines

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// DeepL is the DeepL API: the Free plan (keys ending in ":fx",
// api-free.deepl.com; 500,000 characters a month) or Pro (api.deepl.com).
type DeepL struct {
	Env
	Key string
	// BaseURL replaces the API's address (tests).
	BaseURL string
}

const (
	deeplFree = "https://api-free.deepl.com"
	deeplPro  = "https://api.deepl.com"
	// deeplMax is the most text of one request (bytes; DeepL takes 128 KiB).
	deeplMax = 100 << 10
)

// ID implements Engine.
func (d *DeepL) ID() string { return "deepl" }

type deeplRequest struct {
	Text       []string `json:"text"`
	TargetLang string   `json:"target_lang"`
	SourceLang string   `json:"source_lang,omitempty"`
}

type deeplResponse struct {
	Translations []struct {
		DetectedSourceLanguage string `json:"detected_source_language"`
		Text                   string `json:"text"`
	} `json:"translations"`
}

// Translate implements Engine.
func (d *DeepL) Translate(ctx context.Context, r Request) (Result, error) {
	if strings.TrimSpace(d.Key) == "" {
		return Result{}, ErrNoKey
	}
	return translatePieces(d.Env, ctx, r, deeplMax, d.one)
}

func (d *DeepL) one(ctx context.Context, r Request) (Result, error) {
	key := strings.TrimSpace(d.Key)
	base := d.BaseURL
	if base == "" {
		base = deeplPro
		if strings.HasSuffix(key, ":fx") {
			base = deeplFree
		}
	}
	body, err := json.Marshal(deeplRequest{Text: []string{r.Text}, TargetLang: deeplTarget(r.To), SourceLang: deeplSource(r.From)})
	if err != nil {
		return Result{}, err
	}
	req, err := newRequest(ctx, http.MethodPost, base+"/v2/translate", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "DeepL-Auth-Key "+key)
	req.Header.Set("Content-Type", "application/json")
	var resp deeplResponse
	check := func(status int, body []byte) error {
		if status == http.StatusBadRequest && bytes.Contains(bytes.ToLower(body), []byte("lang")) {
			return fmt.Errorf("%w (DeepL)", ErrLanguage)
		}
		return nil
	}
	if err := doJSON(d.Client, req, &resp, check); err != nil {
		return Result{}, err
	}
	if len(resp.Translations) == 0 {
		return Result{}, errors.New("deepl: no translation in the response")
	}
	t := resp.Translations[0]
	return Result{Text: t.Text, From: firstNonEmpty(Canon(t.DetectedSourceLanguage), r.From)}, nil
}

// deeplTarget is DeepL's target code: English and Portuguese need a
// variant, Chinese a script.
func deeplTarget(code string) string {
	switch code {
	case English:
		return "EN-US"
	case "pt":
		return "PT-BR"
	case Simplified:
		return "ZH-HANS"
	case Traditional:
		return "ZH-HANT"
	}
	return strings.ToUpper(code)
}

// deeplSource is DeepL's source code ("" to detect).
func deeplSource(code string) string {
	switch code {
	case "":
		return ""
	case Simplified, Traditional:
		return "ZH"
	}
	return strings.ToUpper(code)
}
