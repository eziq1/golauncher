package engines

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// MyMemory is the free public API of MyMemory (Translated): no key; about
// 5,000 characters a day anonymously, 50,000 with an email address
// (docs/translator.md §3). A request takes at most 500 bytes of text, so
// longer text is sent in pieces.
type MyMemory struct {
	Env
	// Email raises the daily quota (the "de" parameter); optional.
	Email string
	// BaseURL replaces https://api.mymemory.translated.net (tests).
	BaseURL string
}

const (
	myMemoryBase = "https://api.mymemory.translated.net"
	// myMemoryMax is the most text of one request (bytes).
	myMemoryMax = 500
)

// ID implements Engine.
func (m *MyMemory) ID() string { return "mymemory" }

type myMemoryResponse struct {
	ResponseData struct {
		TranslatedText   string         `json:"translatedText"`
		DetectedLanguage jsontext.Value `json:"detectedLanguage"`
	} `json:"responseData"`
	// ResponseStatus is a number, or sometimes a string ("403").
	ResponseStatus  jsontext.Value `json:"responseStatus"`
	ResponseDetails string         `json:"responseDetails"`
	QuotaFinished   jsontext.Value `json:"quotaFinished"`
}

// Translate implements Engine.
func (m *MyMemory) Translate(ctx context.Context, r Request) (Result, error) {
	if r.From == "" {
		// MyMemory needs the source language: the script tells some, else it
		// is detected ("Autodetect").
		r.From = Guess(r.Text)
	}
	return translatePieces(m.Env, ctx, r, myMemoryMax, m.one)
}

func (m *MyMemory) one(ctx context.Context, r Request) (Result, error) {
	base := m.BaseURL
	if base == "" {
		base = myMemoryBase
	}
	from := r.From
	if from == "" {
		from = "Autodetect"
	}
	q := url.Values{}
	q.Set("q", r.Text)
	q.Set("langpair", from+"|"+r.To)
	if e := strings.TrimSpace(m.Email); e != "" {
		q.Set("de", e)
	}
	req, err := newRequest(ctx, http.MethodGet, base+"/get?"+q.Encode(), nil)
	if err != nil {
		return Result{}, err
	}
	var resp myMemoryResponse
	if err := doJSON(m.Client, req, &resp, nil); err != nil {
		return Result{}, err
	}
	status := jsonInt(resp.ResponseStatus)
	text := resp.ResponseData.TranslatedText
	upper := strings.ToUpper(text + " " + resp.ResponseDetails)
	switch {
	case jsonBool(resp.QuotaFinished) || status == http.StatusTooManyRequests ||
		strings.Contains(upper, "YOU USED ALL AVAILABLE FREE TRANSLATIONS"):
		return Result{}, fmt.Errorf("%w (MyMemory)", ErrQuota)
	case status == http.StatusForbidden && strings.Contains(upper, "LANGUAGE"):
		return Result{}, fmt.Errorf("%w (MyMemory: %s)", ErrLanguage, resp.ResponseDetails)
	case status != 0 && status != http.StatusOK:
		return Result{}, fmt.Errorf("MyMemory status %d: %s", status, resp.ResponseDetails)
	case text == "":
		return Result{}, errors.New("mymemory: no translation in the response")
	}
	detected := ""
	var s string
	if json.Unmarshal(resp.ResponseData.DetectedLanguage, &s) == nil {
		detected = Canon(s)
	}
	// MyMemory escapes some characters as HTML entities ("&#39;").
	return Result{Text: html.UnescapeString(text), From: firstNonEmpty(r.From, detected)}, nil
}

// jsonInt reads a number or a string holding one; 0 otherwise.
func jsonInt(v jsontext.Value) int {
	var n float64
	if json.Unmarshal(v, &n) == nil {
		return int(n)
	}
	var s string
	if json.Unmarshal(v, &s) == nil {
		i, _ := strconv.Atoi(strings.TrimSpace(s))
		return i
	}
	return 0
}

// jsonBool reads a boolean or a string "true"; false otherwise.
func jsonBool(v jsontext.Value) bool {
	var b bool
	if json.Unmarshal(v, &b) == nil {
		return b
	}
	var s string
	return json.Unmarshal(v, &s) == nil && strings.EqualFold(s, "true")
}
