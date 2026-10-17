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

// googleResponse is the answer with dj=1: the translation by sentence
// (a last "sentence" holds the transliterations), with dt=bd the
// translations of a word by part of speech, with dt=md its definitions.
type googleResponse struct {
	Sentences []struct {
		Trans       string `json:"trans"`
		Orig        string `json:"orig"`
		SrcTranslit string `json:"src_translit"`
	} `json:"sentences"`
	Src  string `json:"src"`
	Dict []struct {
		Pos   string   `json:"pos"`
		Terms []string `json:"terms"`
	} `json:"dict"`
	Definitions []struct {
		Pos   string `json:"pos"`
		Entry []struct {
			Gloss string `json:"gloss"`
		} `json:"entry"`
	} `json:"definitions"`
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
	word := IsWord(r.Text)
	if word {
		// Dictionary data for a single word: translations by part of speech,
		// definitions, transliteration (the phonetics).
		q.Add("dt", "bd")
		q.Add("dt", "md")
		q.Add("dt", "rm")
	}
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
	phonetic := ""
	for _, s := range resp.Sentences {
		b.WriteString(s.Trans)
		phonetic = firstNonEmpty(phonetic, s.SrcTranslit)
	}
	if b.Len() == 0 {
		return Result{}, errors.New("google: no translation in the response")
	}
	res := Result{Text: b.String(), From: firstNonEmpty(Canon(resp.Src), r.From)}
	if word {
		d := &Dict{Phonetic: phonetic}
		for _, e := range resp.Dict {
			d.Entries = addEntry(d.Entries, e.Pos, e.Terms)
		}
		for _, e := range resp.Definitions {
			var glosses []string
			for _, x := range e.Entry {
				glosses = append(glosses, x.Gloss)
			}
			d.Definitions = addEntry(d.Definitions, e.Pos, glosses)
		}
		if !d.Empty() {
			res.Dict = d
		}
	}
	return res, nil
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
