package engines

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// server is a fake engine API: it records the requests and answers with
// handle.
type server struct {
	srv    *httptest.Server
	mu     sync.Mutex
	reqs   []recorded
	handle func(w http.ResponseWriter, r recorded)
}

type recorded struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   string
}

func newServer(t *testing.T, handle func(w http.ResponseWriter, r recorded)) *server {
	t.Helper()
	s := &server{handle: handle}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec := recorded{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Header: r.Header.Clone(), Body: string(body)}
		s.mu.Lock()
		s.reqs = append(s.reqs, rec)
		h := s.handle
		s.mu.Unlock()
		h(w, rec)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *server) requests() []recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reqs)
}

func (s *server) env() Env { return Env{Client: s.srv.Client()} }

func write(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	io.WriteString(w, body)
}

func TestGoogle(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, r recorded) {
		write(w, 200, `{"sentences":[{"trans":"你好，","orig":"Hello, "},{"trans":"世界","orig":"world"}],"src":"en","confidence":1}`)
	})
	g := &Google{Env: s.env(), BaseURL: s.srv.URL}
	res, err := g.Translate(t.Context(), Request{Text: "Hello, world", To: Simplified})
	if err != nil || res.Text != "你好，世界" || res.From != English {
		t.Fatalf("%+v %v", res, err)
	}
	r := s.requests()[0]
	if r.Method != http.MethodGet || r.Path != "/translate_a/single" || r.Query.Get("client") != "gtx" || r.Query.Get("sl") != "auto" ||
		r.Query.Get("tl") != "zh-CN" || r.Query.Get("q") != "Hello, world" || r.Query.Get("dj") != "1" || !slices.Contains(r.Query["dt"], "t") {
		t.Errorf("request %+v", r)
	}
	// A given source; long text is posted as a form.
	long := strings.Repeat("word ", 400)
	if _, err := g.Translate(t.Context(), Request{Text: long, From: Japanese, To: English}); err != nil {
		t.Fatal(err)
	}
	r = s.requests()[1]
	form, _ := url.ParseQuery(r.Body)
	if r.Method != http.MethodPost || r.Query.Get("sl") != "ja" || form.Get("q") != long || r.Query.Has("q") {
		t.Errorf("long request %s %v %d", r.Method, r.Query, len(form.Get("q")))
	}
}

func TestGoogleErrors(t *testing.T) {
	for status, want := range map[int]error{429: ErrQuota, 403: ErrAuth, 500: nil} {
		s := newServer(t, func(w http.ResponseWriter, r recorded) { write(w, status, `<html>no</html>`) })
		_, err := (&Google{Env: s.env(), BaseURL: s.srv.URL}).Translate(t.Context(), Request{Text: "x", To: English})
		if err == nil || want != nil && !errors.Is(err, want) {
			t.Errorf("status %d: %v", status, err)
		}
	}
	s := newServer(t, func(w http.ResponseWriter, r recorded) { write(w, 200, `{"sentences":[]}`) })
	if _, err := (&Google{Env: s.env(), BaseURL: s.srv.URL}).Translate(t.Context(), Request{Text: "x", To: English}); err == nil {
		t.Error("an empty answer is a translation")
	}
	s = newServer(t, func(w http.ResponseWriter, r recorded) { write(w, 200, `not json`) })
	if _, err := (&Google{Env: s.env(), BaseURL: s.srv.URL}).Translate(t.Context(), Request{Text: "x", To: English}); err == nil {
		t.Error("bad JSON is a translation")
	}
}

func TestMyMemory(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, r recorded) {
		q := r.Query.Get("q")
		write(w, 200, `{"responseData":{"translatedText":"<`+strings.ReplaceAll(q, "'", "&#39;")+`>","match":1},"quotaFinished":false,"responseDetails":"","responseStatus":200}`)
	})
	m := &MyMemory{Env: s.env(), Email: "someone@example.com", BaseURL: s.srv.URL}
	res, err := m.Translate(t.Context(), Request{Text: "it's", From: English, To: Simplified})
	if err != nil || res.Text != "<it's>" || res.From != English {
		t.Fatalf("%+v %v", res, err)
	}
	r := s.requests()[0]
	if r.Path != "/get" || r.Query.Get("langpair") != "en|zh-CN" || r.Query.Get("de") != "someone@example.com" {
		t.Errorf("request %+v", r)
	}
	// No source: the script tells Chinese; Latin text is detected.
	m.Email = ""
	m.Translate(t.Context(), Request{Text: "你好", To: English})
	m.Translate(t.Context(), Request{Text: "bonjour", To: English})
	reqs := s.requests()
	if reqs[1].Query.Get("langpair") != "zh-CN|en" || reqs[2].Query.Get("langpair") != "Autodetect|en" || reqs[1].Query.Has("de") {
		t.Errorf("langpairs %q %q", reqs[1].Query.Get("langpair"), reqs[2].Query.Get("langpair"))
	}
}

func TestMyMemorySplits(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, r recorded) {
		if len(r.Query.Get("q")) > 500 {
			write(w, 200, `{"responseData":{"translatedText":"QUERY LENGTH LIMIT EXCEEDED"},"responseStatus":403,"responseDetails":"QUERY LENGTH LIMIT EXCEEDED. MAX ALLOWED QUERY : 500 CHARS"}`)
			return
		}
		write(w, 200, `{"responseData":{"translatedText":"T(`+r.Query.Get("q")+`)"},"responseStatus":200}`)
	})
	var mu sync.Mutex
	goes := 0
	env := s.env()
	env.Go = func(fn func()) {
		mu.Lock()
		goes++
		mu.Unlock()
		go fn()
	}
	sentence := strings.Repeat("a", 90) + ". "
	text := strings.Repeat(sentence, 12) // 1104 bytes
	res, err := (&MyMemory{Env: env, BaseURL: s.srv.URL}).Translate(t.Context(), Request{Text: text, From: English, To: "fr"})
	if err != nil {
		t.Fatal(err)
	}
	reqs := s.requests()
	if len(reqs) != 3 || goes != 3 {
		t.Fatalf("%d requests, %d goroutines", len(reqs), goes)
	}
	for _, r := range reqs {
		if q := r.Query.Get("q"); len(q) > 500 || !strings.HasSuffix(q, ".") {
			t.Errorf("piece of %d bytes: %q", len(q), q)
		}
	}
	if strings.Count(res.Text, "T(") != 3 || strings.Count(res.Text, sentence[:90]) != 12 {
		t.Errorf("joined %q", res.Text)
	}
}

func TestMyMemoryErrors(t *testing.T) {
	for _, tc := range []struct {
		body string
		want error
	}{
		{`{"responseData":{"translatedText":"MYMEMORY WARNING: YOU USED ALL AVAILABLE FREE TRANSLATIONS FOR TODAY. NEXT AVAILABLE IN  10 HOURS"},"quotaFinished":true,"responseStatus":"429"}`, ErrQuota},
		{`{"responseData":{"translatedText":"x"},"responseStatus":"403","responseDetails":"'XX' IS AN INVALID TARGET LANGUAGE . EXAMPLE: LANGPAIR=EN|IT USING 2 LETTER ISO OR RFC3066 LIKE ZH-CN. ALMOST ALL LANGUAGES SUPPORTED BUT SOME MAY HAVE NO CONTENT"}`, ErrLanguage},
		{`{"responseData":{"translatedText":""},"responseStatus":200}`, nil},
		{`{"responseData":{"translatedText":"x"},"responseStatus":500,"responseDetails":"oops"}`, nil},
	} {
		s := newServer(t, func(w http.ResponseWriter, r recorded) { write(w, 200, tc.body) })
		_, err := (&MyMemory{Env: s.env(), BaseURL: s.srv.URL}).Translate(t.Context(), Request{Text: "x", From: English, To: "xx"})
		if err == nil || tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: %v", tc.body, err)
		}
	}
}

func TestDeepL(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, r recorded) {
		write(w, 200, `{"translations":[{"detected_source_language":"EN","text":"苹果"}]}`)
	})
	d := &DeepL{Env: s.env(), Key: "abc:fx", BaseURL: s.srv.URL}
	res, err := d.Translate(t.Context(), Request{Text: "apple", To: Simplified})
	if err != nil || res.Text != "苹果" || res.From != English {
		t.Fatalf("%+v %v", res, err)
	}
	r := s.requests()[0]
	if r.Method != http.MethodPost || r.Path != "/v2/translate" || r.Header.Get("Authorization") != "DeepL-Auth-Key abc:fx" ||
		r.Header.Get("Content-Type") != "application/json" || r.Body != `{"text":["apple"],"target_lang":"ZH-HANS"}` {
		t.Errorf("request %+v", r)
	}
	d.Translate(t.Context(), Request{Text: "Olá", From: "pt", To: English})
	if b := s.requests()[1].Body; b != `{"text":["Olá"],"target_lang":"EN-US","source_lang":"PT"}` {
		t.Errorf("body %s", b)
	}
	// No key: nothing is sent.
	if _, err := (&DeepL{Env: s.env(), BaseURL: s.srv.URL}).Translate(t.Context(), Request{Text: "x", To: English}); !errors.Is(err, ErrNoKey) || len(s.requests()) != 2 {
		t.Errorf("without a key: %v, %d requests", err, len(s.requests()))
	}
}

func TestDeepLEndpoint(t *testing.T) {
	var got []string
	c := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		got = append(got, r.URL.Scheme+"://"+r.URL.Host)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"translations":[{"text":"x"}]}`)), Header: http.Header{}}, nil
	})}
	(&DeepL{Env: Env{Client: c}, Key: "k:fx"}).Translate(t.Context(), Request{Text: "x", To: English})
	(&DeepL{Env: Env{Client: c}, Key: "k"}).Translate(t.Context(), Request{Text: "x", To: English})
	if !slices.Equal(got, []string{deeplFree, deeplPro}) {
		t.Errorf("endpoints %v", got)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDeepLErrors(t *testing.T) {
	for status, want := range map[int]error{403: ErrAuth, 456: ErrQuota, 429: ErrQuota, 400: ErrLanguage} {
		s := newServer(t, func(w http.ResponseWriter, r recorded) {
			write(w, status, `{"message":"Value for 'target_lang' not supported."}`)
		})
		_, err := (&DeepL{Env: s.env(), Key: "k", BaseURL: s.srv.URL}).Translate(t.Context(), Request{Text: "x", To: "xx"})
		if !errors.Is(err, want) {
			t.Errorf("status %d: %v", status, err)
		}
	}
}

func TestTimeoutAndCancel(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, r recorded) {
		time.Sleep(2 * time.Second)
		write(w, 200, `{}`)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := (&Google{Env: s.env(), BaseURL: s.srv.URL}).Translate(ctx, Request{Text: "x", To: English})
	if err == nil || time.Since(start) > 1500*time.Millisecond {
		t.Errorf("%v after %v", err, time.Since(start))
	}
}

func TestSplit(t *testing.T) {
	for _, tc := range []struct {
		text string
		max  int
		want []string
	}{
		{"short", 10, []string{"short"}},
		{"One. Two. Three.", 10, []string{"One. Two.", " Three."}},
		{"aaaa bbbb cccc dddd", 9, []string{"aaaa ", "bbbb ", "cccc dddd"}},
		{"你好。世界。", 10, []string{"你好。", "世界。"}},
		{"abcdefghij", 4, []string{"abcd", "efgh", "ij"}},
		{"你好世界", 4, []string{"你", "好", "世", "界"}},
	} {
		got := Split(tc.text, tc.max)
		if !slices.Equal(got, tc.want) {
			t.Errorf("Split(%q, %d) = %q, want %q", tc.text, tc.max, got, tc.want)
		}
		if strings.Join(got, "") != tc.text {
			t.Errorf("Split(%q) loses text", tc.text)
		}
		for _, p := range got {
			if len(p) > tc.max && utf8.RuneCountInString(p) > 1 || !utf8.ValidString(p) {
				t.Errorf("piece %q of %q", p, tc.text)
			}
		}
	}
}

func TestCanonAndGuess(t *testing.T) {
	for in, want := range map[string]string{"ZH": Simplified, "zh-Hans": Simplified, "zh_TW": Traditional, "EN-US": English,
		"jp": Japanese, "auto": "", "": "", "FR": "fr", "zh-CHS": Simplified, "kor": Korean} {
		if got := Canon(in); got != want {
			t.Errorf("Canon(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"你好世界": Simplified, "こんにちは": Japanese, "日本語のテキスト": Japanese, "안녕하세요": Korean,
		"Привет": Russian, "hello": "", "bonjour": "", "": "", "你好 hi": Simplified, "Hello 你": ""} {
		if got := Guess(in); got != want {
			t.Errorf("Guess(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRealNoKeyEngines asks Google's web endpoint and MyMemory for real; it
// runs only with GOLAUNCHER_NET_TESTS=1 (engines needing a key are never
// asked).
func TestRealNoKeyEngines(t *testing.T) {
	if os.Getenv("GOLAUNCHER_NET_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_NET_TESTS=1 to go online")
	}
	env := Env{Client: &http.Client{Timeout: 10 * time.Second}}
	for _, e := range []Engine{&Google{Env: env}, &MyMemory{Env: env}} {
		res, err := e.Translate(t.Context(), Request{Text: "good morning", To: Simplified})
		if err != nil || res.Text == "" {
			t.Errorf("%s: %+v %v", e.ID(), res, err)
		}
		t.Logf("%s: %d characters, from %q", e.ID(), utf8.RuneCountInString(res.Text), res.From)
	}
}
