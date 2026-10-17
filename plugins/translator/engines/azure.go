package engines

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Azure is Azure AI Translator (Translator Text API v3): a key and, for a
// regional or multi-service resource, its region. The free tier (F0) has a
// monthly quota (docs/translator.md §3); it is reachable from mainland
// China.
type Azure struct {
	Env
	Key, Region string
	// BaseURL replaces https://api.cognitive.microsofttranslator.com (tests).
	BaseURL string
}

const (
	azureBase = "https://api.cognitive.microsofttranslator.com"
	// azureMax is the most text of one request (bytes; the API takes 50,000
	// characters).
	azureMax = 40 << 10
)

// ID implements Engine.
func (a *Azure) ID() string { return "azure" }

type azureResponse []struct {
	DetectedLanguage struct {
		Language string `json:"language"`
	} `json:"detectedLanguage"`
	Translations []struct {
		Text string `json:"text"`
	} `json:"translations"`
}

type azureError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Translate implements Engine.
func (a *Azure) Translate(ctx context.Context, r Request) (Result, error) {
	if strings.TrimSpace(a.Key) == "" {
		return Result{}, ErrNoKey
	}
	return translatePieces(a.Env, ctx, r, azureMax, a.one)
}

func (a *Azure) one(ctx context.Context, r Request) (Result, error) {
	base := a.BaseURL
	if base == "" {
		base = azureBase
	}
	q := url.Values{}
	q.Set("api-version", "3.0")
	q.Set("to", azureCode(r.To))
	if r.From != "" {
		q.Set("from", azureCode(r.From))
	}
	body, err := json.Marshal([]map[string]string{{"Text": r.Text}})
	if err != nil {
		return Result{}, err
	}
	req, err := newRequest(ctx, http.MethodPost, base+"/translate?"+q.Encode(), bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", strings.TrimSpace(a.Key))
	if region := strings.TrimSpace(a.Region); region != "" {
		req.Header.Set("Ocp-Apim-Subscription-Region", region)
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	var resp azureResponse
	if err := doJSON(a.Client, req, &resp, azureCheck); err != nil {
		return Result{}, err
	}
	if len(resp) == 0 || len(resp[0].Translations) == 0 {
		return Result{}, errors.New("azure: no translation in the response")
	}
	return Result{Text: resp[0].Translations[0].Text, From: firstNonEmpty(Canon(resp[0].DetectedLanguage.Language), r.From)}, nil
}

// azureCheck reads Azure's error codes: 401xxx a bad key or region,
// 403001 the free quota used up, 429xxx too many requests, 400035/400036
// a language it does not have.
func azureCheck(status int, body []byte) error {
	if status == http.StatusOK {
		return nil
	}
	var e azureError
	_ = json.Unmarshal(body, &e)
	code := e.Error.Code
	switch {
	case code == 403001 || status == http.StatusTooManyRequests || code/1000 == 429:
		return fmt.Errorf("%w (Azure %d)", ErrQuota, code)
	case status == http.StatusUnauthorized || code/1000 == 401:
		return fmt.Errorf("%w (Azure %d)", ErrAuth, code)
	case code == 400035 || code == 400036:
		return fmt.Errorf("%w (Azure %d)", ErrLanguage, code)
	}
	return nil // statusError
}

// azureCode is Azure's language code: Chinese by script.
func azureCode(code string) string {
	switch code {
	case Simplified:
		return "zh-Hans"
	case Traditional:
		return "zh-Hant"
	}
	return code
}
