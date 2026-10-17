package engines

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Google is the web endpoint of Google Translate that its browser
// extensions use (translate.googleapis.com/translate_a/single, client=gtx).
// It needs no key but is not an official API: it may change or limit
// requests at any time, and it is usually unreachable in mainland China.
type Google struct {
	Env
	// BaseURL replaces https://translate.googleapis.com (tests).
	BaseURL string
}

const (
	googleBase = "https://translate.googleapis.com"
	// googleMax is the most text sent in one request (bytes).
	googleMax = 4500
	// googleGetMax is the longest text sent in the URL; longer text is
	// posted as a form.
	googleGetMax = 1500
)

// ID implements Engine.
func (g *Google) ID() string { return "google" }

// googleResponse is the answer with dj=1.
type googleResponse struct {
	Sentences []struct {
		Trans       string `json:"trans"`
		Orig        string `json:"orig"`
		SrcTranslit string `json:"src_translit"`
	} `json:"sentences"`
	Src string `json:"src"`
}

// Translate implements Engine.
func (g *Google) Translate(ctx context.Context, r Request) (Result, error) {
	return translatePieces(g.Env, ctx, r, googleMax, g.one)
}

func (g *Google) one(ctx context.Context, r Request) (Result, error) {
	base := g.BaseURL
	if base == "" {
		base = googleBase
	}
	q := url.Values{}
	q.Set("client", "gtx")
	q.Set("sl", googleCode(r.From))
	q.Set("tl", googleCode(r.To))
	q.Set("dj", "1")
	q.Set("ie", "UTF-8")
	q.Set("oe", "UTF-8")
	q.Add("dt", "t")
	u := base + "/translate_a/single"
	var req *http.Request
	var err error
	if len(r.Text) <= googleGetMax {
		q.Set("q", r.Text)
		req, err = newRequest(ctx, http.MethodGet, u+"?"+q.Encode(), nil)
	} else {
		form := url.Values{"q": {r.Text}}
		req, err = newRequest(ctx, http.MethodPost, u+"?"+q.Encode(), strings.NewReader(form.Encode()))
		if req != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
		}
	}
	if err != nil {
		return Result{}, err
	}
	var resp googleResponse
	if err := doJSON(g.Client, req, &resp, nil); err != nil {
		return Result{}, err
	}
	var b strings.Builder
	for _, s := range resp.Sentences {
		b.WriteString(s.Trans)
	}
	if b.Len() == 0 {
		return Result{}, errors.New("google: no translation in the response")
	}
	return Result{Text: b.String(), From: firstNonEmpty(Canon(resp.Src), r.From)}, nil
}

// googleCode is the code Google takes: canonical codes are Google's, and
// "" is "auto".
func googleCode(code string) string {
	if code == "" {
		return "auto"
	}
	return code
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}
