package engines

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Phase 3: custom HTTP engines and Ollama, against fake servers.

func TestParseCustom(t *testing.T) {
	c, err := ParseCustom(" My API | post | https://example.com/t | data.0.text | {\"q\":\"{text}\"} | Authorization: Bearer k; X-A: 1 ")
	if err != nil || c.Name != "My API" || c.Method != "POST" || c.URL != "https://example.com/t" || c.Path != "data.0.text" ||
		c.Body != `{"q":"{text}"}` || c.Headers["Authorization"] != "Bearer k" || c.Headers["X-A"] != "1" || c.ID() != "custom:My API" {
		t.Fatalf("%+v %v", c, err)
	}
	if c, err := ParseCustom("G | GET | https://x.test/?q={text}&tl={to} | 0.0"); err != nil || c.Body != "" || c.Headers != nil {
		t.Errorf("GET %+v %v", c, err)
	}
	for _, bad := range []string{"", "a | GET | https://x/?q={text}", "a | PUT | https://x/?q={text} | p", "a | GET | ftp://x/{text} | p",
		"a | GET | https://x/ | p", "| GET | https://x/?q={text} | p", "a | POST | https://x/ | p | {} | NoColon"} {
		if _, err := ParseCustom(bad); err == nil {
			t.Errorf("ParseCustom(%q) accepted", bad)
		}
	}
}

func TestCustomEngine(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, r recorded) {
		write(w, 200, `{"data":{"translations":[{"translatedText":"T"}]}}`)
	})
	get, _ := ParseCustom("G | GET | " + s.srv.URL + "/t?q={text}&sl={from}&tl={to} | data.translations[0].translatedText")
	get.Env = s.env()
	res, err := get.Translate(t.Context(), Request{Text: "a&b c", To: Japanese})
	if err != nil || res.Text != "T" {
		t.Fatalf("%+v %v", res, err)
	}
	r := s.requests()[0]
	if r.Method != http.MethodGet || r.Query.Get("q") != "a&b c" || r.Query.Get("sl") != "auto" || r.Query.Get("tl") != "ja" {
		t.Errorf("GET %+v", r)
	}
	post, _ := ParseCustom("P | POST | " + s.srv.URL + "/t | data.translations.0.translatedText | {\"q\":\"{text}\",\"to\":\"{to}\"} | X-Key: k")
	post.Env = s.env()
	post.Translate(t.Context(), Request{Text: `say "hi"` + "\n", From: English, To: Simplified})
	r = s.requests()[1]
	var body map[string]string
	if json.Unmarshal([]byte(r.Body), &body) != nil || body["q"] != "say \"hi\"\n" || body["to"] != "zh-CN" ||
		r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Key") != "k" {
		t.Errorf("POST JSON %+v", r)
	}
	form, _ := ParseCustom("F | POST | " + s.srv.URL + "/t | data.translations.0.translatedText | q={text}&target={to}")
	form.Env = s.env()
	form.Translate(t.Context(), Request{Text: "a&b", To: "fr"})
	r = s.requests()[2]
	if v, _ := url.ParseQuery(r.Body); v.Get("q") != "a&b" || v.Get("target") != "fr" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Errorf("POST form %+v", r)
	}
	bad, _ := ParseCustom("B | GET | " + s.srv.URL + "/t?q={text} | data.nothing")
	bad.Env = s.env()
	if _, err := bad.Translate(t.Context(), Request{Text: "x", To: English}); err == nil {
		t.Error("a missing path is a translation")
	}
}

func TestJSONPath(t *testing.T) {
	doc := jsontext.Value(`{"a":{"b":[{"c":"x"},{"c":5}]},"list":["y"]}`)
	for path, want := range map[string]string{"a.b.0.c": "x", "a.b[0].c": "x", "list.0": "y", "list[0]": "y"} {
		if got, err := JSONPath(doc, path); err != nil || got != want {
			t.Errorf("%s = %q %v", path, got, err)
		}
	}
	for _, path := range []string{"a.b.1.c", "a.b.2.c", "a.x", "list.1", "a", "a.b.c"} {
		if _, err := JSONPath(doc, path); err == nil {
			t.Errorf("%s found", path)
		}
	}
	if got, err := JSONPath(jsontext.Value(`[{"translations":[{"text":"z"}]}]`), "[0].translations[0].text"); err != nil || got != "z" {
		t.Errorf("array root %q %v", got, err)
	}
}

func TestOllama(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, r recorded) {
		switch r.Path {
		case "/api/tags":
			write(w, 200, `{"models":[{"name":"qwen2.5:latest"},{"name":"llama3.1:8b"}]}`)
		case "/v1/chat/completions":
			write(w, 200, `{"choices":[{"message":{"role":"assistant","content":"  你好  "}}]}`)
		default:
			write(w, 404, `{}`)
		}
	})
	o := &Ollama{Env: s.env(), BaseURL: s.srv.URL + "/", Model: "qwen2.5"}
	res, err := o.Translate(t.Context(), Request{Text: "hello", From: English, To: Simplified})
	if err != nil || res.Text != "你好" || res.From != English {
		t.Fatalf("%+v %v", res, err)
	}
	reqs := s.requests()
	if len(reqs) != 2 || reqs[0].Path != "/api/tags" || reqs[1].Method != http.MethodPost {
		t.Fatalf("requests %+v", reqs)
	}
	var body struct {
		Model    string        `json:"model"`
		Stream   bool          `json:"stream"`
		Messages []chatMessage `json:"messages"`
	}
	if json.Unmarshal([]byte(reqs[1].Body), &body) != nil || body.Model != "qwen2.5" || body.Stream || len(body.Messages) != 2 ||
		!strings.Contains(body.Messages[0].Content, "from English into Simplified Chinese") || body.Messages[1].Content != "hello" {
		t.Errorf("body %s", reqs[1].Body)
	}
	// The server check is cached.
	o.Translate(t.Context(), Request{Text: "x", From: English, To: "fr"})
	if n := len(s.requests()); n != 3 {
		t.Errorf("%d requests", n)
	}
	// A model the server does not have; no model chosen.
	missing := &Ollama{Env: s.env(), BaseURL: s.srv.URL, Model: "mistral"}
	if _, err := missing.Translate(t.Context(), Request{Text: "x", To: English}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("missing model: %v", err)
	}
	if _, err := (&Ollama{Env: s.env(), BaseURL: s.srv.URL}).Translate(t.Context(), Request{Text: "x", To: English}); !errors.Is(err, ErrNoKey) {
		t.Errorf("no model: %v", err)
	}
	if m, err := o.Models(t.Context()); err != nil || len(m) != 2 {
		t.Errorf("models %v %v", m, err)
	}
}

func TestOllamaNotRunning(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, r recorded) {})
	base := s.srv.URL
	s.srv.Close()
	o := &Ollama{Env: Env{Client: http.DefaultClient}, BaseURL: base, Model: "m"}
	if _, err := o.Translate(t.Context(), Request{Text: "x", To: English}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("not running: %v", err)
	}
}
