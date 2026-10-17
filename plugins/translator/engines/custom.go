package engines

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Custom is an engine defined by the user as an HTTP template
// (docs/translator.md §8, phase 3): a request with {text}, {from} and {to}
// in its URL (query-escaped) or body (JSON-escaped in a JSON body, else
// form-escaped), and the JSON path of the translation in the answer, e.g.
// "data.translations.0.translatedText" or "[0].translations[0].text".
type Custom struct {
	Env
	Name   string
	Method string // GET or POST
	URL    string
	Path   string
	Body   string
	// Headers are sent with the request ("Authorization: Bearer …").
	Headers map[string]string
}

// ID implements Engine: "custom:" and the name.
func (c *Custom) ID() string { return "custom:" + c.Name }

// customMax is the most text of one request (bytes).
const customMax = 4500

// ParseCustom reads an engine definition from one line of the preference:
//
//	Name | GET | https://example.com/t?q={text}&to={to} | result.text
//	Name | POST | https://example.com/t | data.0.text | {"q":"{text}","target":"{to}"} | Authorization: Bearer xyz; X-Other: 1
//
// The body and the headers are optional.
func ParseCustom(line string) (Custom, error) {
	f := strings.Split(line, "|")
	for i := range f {
		f[i] = strings.TrimSpace(f[i])
	}
	if len(f) < 4 || f[0] == "" || f[2] == "" || f[3] == "" {
		return Custom{}, errors.New("want: name | GET or POST | URL | JSON path [| body [| headers]]")
	}
	c := Custom{Name: f[0], Method: strings.ToUpper(f[1]), URL: f[2], Path: f[3]}
	if c.Method != http.MethodGet && c.Method != http.MethodPost {
		return Custom{}, fmt.Errorf("method %q: want GET or POST", f[1])
	}
	if u, err := url.Parse(strings.NewReplacer("{text}", "x", "{from}", "x", "{to}", "x").Replace(c.URL)); err != nil ||
		u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return Custom{}, fmt.Errorf("URL %q: want http(s)://…", c.URL)
	}
	if !strings.Contains(c.URL, "{text}") && (len(f) < 5 || !strings.Contains(f[4], "{text}")) {
		return Custom{}, errors.New("{text} is in neither the URL nor the body")
	}
	if len(f) > 4 {
		c.Body = f[4]
	}
	if len(f) > 5 && f[5] != "" {
		c.Headers = map[string]string{}
		for h := range strings.SplitSeq(f[5], ";") {
			k, v, ok := strings.Cut(h, ":")
			if !ok || strings.TrimSpace(k) == "" {
				return Custom{}, fmt.Errorf("header %q: want Name: value", strings.TrimSpace(h))
			}
			c.Headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return c, nil
}

// Translate implements Engine.
func (c *Custom) Translate(ctx context.Context, r Request) (Result, error) {
	return translatePieces(c.Env, ctx, r, customMax, c.one)
}

func (c *Custom) one(ctx context.Context, r Request) (Result, error) {
	from := r.From
	if from == "" {
		from = "auto"
	}
	u := Expand(c.URL, url.QueryEscape, r.Text, from, r.To)
	var body *strings.Reader
	contentType := ""
	if c.Body != "" {
		esc := url.QueryEscape
		contentType = "application/x-www-form-urlencoded"
		if t := strings.TrimSpace(c.Body); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
			esc, contentType = jsonEscape, "application/json"
		}
		body = strings.NewReader(Expand(c.Body, esc, r.Text, from, r.To))
	}
	var req *http.Request
	var err error
	if body != nil {
		req, err = newRequest(ctx, c.Method, u, body)
	} else {
		req, err = newRequest(ctx, c.Method, u, nil)
	}
	if err != nil {
		return Result{}, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	var resp jsontext.Value
	if err := doJSON(c.Client, req, &resp, nil); err != nil {
		return Result{}, err
	}
	text, err := JSONPath(resp, c.Path)
	if err != nil {
		return Result{}, err
	}
	return Result{Text: text, From: r.From}, nil
}

// Expand fills in {text}, {from} and {to}, escaped with esc.
func Expand(tmpl string, esc func(string) string, text, from, to string) string {
	return strings.NewReplacer("{text}", esc(text), "{from}", esc(from), "{to}", esc(to)).Replace(tmpl)
}

// jsonEscape escapes s for the inside of a JSON string.
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

// JSONPath reads the string at path in a JSON document: names and indexes
// separated by dots, indexes also in brackets ("a.b[0].c", "[0].text").
func JSONPath(doc jsontext.Value, path string) (string, error) {
	cur := doc
	steps := strings.FieldsFunc(strings.NewReplacer("[", ".", "]", "").Replace(path), func(r rune) bool { return r == '.' })
	for _, step := range steps {
		if i, err := strconv.Atoi(step); err == nil {
			var arr []jsontext.Value
			if json.Unmarshal(cur, &arr) != nil || i < 0 || i >= len(arr) {
				return "", fmt.Errorf("custom engine: no element %d at %q", i, path)
			}
			cur = arr[i]
			continue
		}
		var obj map[string]jsontext.Value
		if json.Unmarshal(cur, &obj) != nil {
			return "", fmt.Errorf("custom engine: no object for %q at %q", step, path)
		}
		v, ok := obj[step]
		if !ok {
			return "", fmt.Errorf("custom engine: no %q at %q", step, path)
		}
		cur = v
	}
	var s string
	if err := json.Unmarshal(cur, &s); err != nil {
		return "", fmt.Errorf("custom engine: %q is not a string", path)
	}
	if s == "" {
		return "", errors.New("custom engine: empty translation")
	}
	return s, nil
}
