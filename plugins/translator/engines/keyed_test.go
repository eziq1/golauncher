package engines

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"
)

// Engines that need a key: Azure, Youdao, Baidu (phase 2), each against a
// fake server. Their real APIs are never asked by tests.

func TestAzure(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, r recorded) {
		write(w, 200, `[{"detectedLanguage":{"language":"en","score":1.0},"translations":[{"text":"你好","to":"zh-Hans"}]}]`)
	})
	a := &Azure{Env: s.env(), Key: "k1", Region: "eastasia", BaseURL: s.srv.URL}
	res, err := a.Translate(t.Context(), Request{Text: "hello", To: Simplified})
	if err != nil || res.Text != "你好" || res.From != English {
		t.Fatalf("%+v %v", res, err)
	}
	r := s.requests()[0]
	if r.Method != http.MethodPost || r.Path != "/translate" || r.Query.Get("api-version") != "3.0" || r.Query.Get("to") != "zh-Hans" ||
		r.Query.Has("from") || r.Header.Get("Ocp-Apim-Subscription-Key") != "k1" || r.Header.Get("Ocp-Apim-Subscription-Region") != "eastasia" ||
		r.Body != `[{"Text":"hello"}]` {
		t.Errorf("request %+v", r)
	}
	a.Region = ""
	a.Translate(t.Context(), Request{Text: "你好", From: Traditional, To: English})
	if r := s.requests()[1]; r.Query.Get("from") != "zh-Hant" || r.Header.Get("Ocp-Apim-Subscription-Region") != "" {
		t.Errorf("request %+v", r)
	}
	if _, err := (&Azure{Env: s.env(), BaseURL: s.srv.URL}).Translate(t.Context(), Request{Text: "x", To: English}); !errors.Is(err, ErrNoKey) {
		t.Errorf("without a key: %v", err)
	}
}

func TestAzureErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   int
		want   error
	}{
		{401, 401000, ErrAuth}, {403, 403001, ErrQuota}, {429, 429001, ErrQuota}, {400, 400036, ErrLanguage}, {500, 500000, nil},
	} {
		s := newServer(t, func(w http.ResponseWriter, r recorded) {
			write(w, tc.status, `{"error":{"code":`+strconv.Itoa(tc.code)+`,"message":"m"}}`)
		})
		_, err := (&Azure{Env: s.env(), Key: "k", BaseURL: s.srv.URL}).Translate(t.Context(), Request{Text: "x", To: English})
		if err == nil || tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%d/%d: %v", tc.status, tc.code, err)
		}
	}
}

func TestYoudao(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, r recorded) {
		write(w, 200, `{"errorCode":"0","query":"good","translation":["好"],"l":"en2zh-CHS",
			"basic":{"us-phonetic":"ɡʊd","phonetic":"ɡʊd","uk-phonetic":"ɡʊd","explains":["adj. 好的；优秀的；有益的","n. 好处；善行","int. 好"]}}`)
	})
	y := &Youdao{Env: s.env(), AppKey: "app", Secret: "sec", BaseURL: s.srv.URL,
		Now: func() time.Time { return time.Unix(1700000000, 0) }, Salt: func() string { return "salt1" }}
	res, err := y.Translate(t.Context(), Request{Text: "good", To: Simplified})
	if err != nil || res.Text != "好" || res.From != English {
		t.Fatalf("%+v %v", res, err)
	}
	if d := res.Dict; d == nil || d.Phonetic != "ɡʊd" || len(d.Entries) != 3 || d.Entries[0].POS != "adj." ||
		len(d.Entries[0].Meanings) != 3 || d.Entries[0].Meanings[1] != "优秀的" || d.Entries[2].POS != "int." {
		t.Errorf("dict %+v", res.Dict)
	}
	r := s.requests()[0]
	form, _ := url.ParseQuery(r.Body)
	want := sha256.Sum256([]byte("app" + "good" + "salt1" + "1700000000" + "sec"))
	if r.Path != "/api" || form.Get("q") != "good" || form.Get("from") != "auto" || form.Get("to") != "zh-CHS" || form.Get("appKey") != "app" ||
		form.Get("salt") != "salt1" || form.Get("curtime") != "1700000000" || form.Get("signType") != "v3" || form.Get("sign") != hex.EncodeToString(want[:]) {
		t.Errorf("request %v", form)
	}
	if form.Has("secret") || form.Get("sign") == "" {
		t.Error("the secret is sent")
	}
}

func TestYoudaoSign(t *testing.T) {
	// Long input: its first 10 characters, its length and its last 10.
	q := "一二三四五六七八九十abcdefghijklmnopqrstuvwxyz"
	input := "一二三四五六七八九十" + "36" + "qrstuvwxyz"
	want := sha256.Sum256([]byte("k" + input + "s" + "1" + "x"))
	if got := YoudaoSign("k", q, "s", "1", "x"); got != hex.EncodeToString(want[:]) {
		t.Errorf("sign %s", got)
	}
	short := sha256.Sum256([]byte("k" + "exactly twenty chars" + "s" + "1" + "x"))
	if got := YoudaoSign("k", "exactly twenty chars", "s", "1", "x"); got != hex.EncodeToString(short[:]) {
		t.Errorf("sign of 20 characters %s", got)
	}
}

func TestYoudaoErrors(t *testing.T) {
	for code, want := range map[string]error{"108": ErrAuth, "202": ErrAuth, "401": ErrQuota, "411": ErrQuota, "102": ErrLanguage, "999": nil} {
		s := newServer(t, func(w http.ResponseWriter, r recorded) { write(w, 200, `{"errorCode":"`+code+`"}`) })
		_, err := (&Youdao{Env: s.env(), AppKey: "a", Secret: "b", BaseURL: s.srv.URL}).Translate(t.Context(), Request{Text: "x", To: English})
		if err == nil || want != nil && !errors.Is(err, want) {
			t.Errorf("%s: %v", code, err)
		}
	}
	if _, err := (&Youdao{Env: Env{Client: http.DefaultClient}, AppKey: "a"}).Translate(t.Context(), Request{Text: "x", To: English}); !errors.Is(err, ErrNoKey) {
		t.Errorf("without a secret: %v", err)
	}
}

func TestBaidu(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, r recorded) {
		write(w, 200, `{"from":"en","to":"jp","trans_result":[{"src":"apple","dst":"りんご"},{"src":"pie","dst":"パイ"}]}`)
	})
	b := &Baidu{Env: s.env(), AppID: "2015063000000001", Secret: "12345678", BaseURL: s.srv.URL, Salt: func() string { return "1435660288" }}
	res, err := b.Translate(t.Context(), Request{Text: "apple\npie", To: Japanese})
	if err != nil || res.Text != "りんご\nパイ" || res.From != English {
		t.Fatalf("%+v %v", res, err)
	}
	r := s.requests()[0]
	form, _ := url.ParseQuery(r.Body)
	if r.Path != "/api/trans/vip/translate" || form.Get("from") != "auto" || form.Get("to") != "jp" || form.Get("appid") != "2015063000000001" ||
		form.Get("salt") != "1435660288" || form.Get("sign") != BaiduSign("2015063000000001", "apple\npie", "1435660288", "12345678") {
		t.Errorf("request %v", form)
	}
	// The example of Baidu's documentation.
	if got := BaiduSign("2015063000000001", "apple", "1435660288", "12345678"); got != "f89f9594663708c1605f3d736d01d2d4" {
		t.Errorf("sign %s", got)
	}
	b.Translate(t.Context(), Request{Text: "x", From: Korean, To: Traditional})
	if form, _ := url.ParseQuery(s.requests()[1].Body); form.Get("from") != "kor" || form.Get("to") != "cht" {
		t.Errorf("codes %v", form)
	}
}

func TestBaiduErrors(t *testing.T) {
	for body, want := range map[string]error{
		`{"error_code":"54001","error_msg":"Invalid Sign"}`: ErrAuth, `{"error_code":"52003"}`: ErrAuth,
		`{"error_code":54003}`: ErrQuota, `{"error_code":"54004"}`: ErrQuota, `{"error_code":"58001"}`: ErrLanguage,
		`{"error_code":"52002"}`: nil, `{"from":"en"}`: nil,
	} {
		s := newServer(t, func(w http.ResponseWriter, r recorded) { write(w, 200, body) })
		_, err := (&Baidu{Env: s.env(), AppID: "a", Secret: "b", BaseURL: s.srv.URL}).Translate(t.Context(), Request{Text: "x", To: English})
		if err == nil || want != nil && !errors.Is(err, want) {
			t.Errorf("%s: %v", body, err)
		}
	}
}

func TestGoogleDictionary(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, r recorded) {
		write(w, 200, `{"sentences":[{"trans":"好","orig":"good"},{"translit":"Hǎo","src_translit":"ɡo͝od"}],"src":"en",
			"dict":[{"pos":"adjective","terms":["好","良好","善","佳","美好","优良","良"]},{"pos":"noun","terms":["好处"]}],
			"definitions":[{"pos":"adjective","entry":[{"gloss":"to be desired or approved of."},{"gloss":"having the required qualities."}]}]}`)
	})
	g := &Google{Env: s.env(), BaseURL: s.srv.URL}
	res, err := g.Translate(t.Context(), Request{Text: "good", To: Simplified})
	if err != nil {
		t.Fatal(err)
	}
	d := res.Dict
	if d == nil || d.Phonetic != "ɡo͝od" || len(d.Entries) != 2 || len(d.Entries[0].Meanings) != maxDictMeanings ||
		d.Entries[1].POS != "noun" || len(d.Definitions) != 1 || len(d.Definitions[0].Meanings) != 2 {
		t.Fatalf("dict %+v", d)
	}
	if dt := s.requests()[0].Query["dt"]; len(dt) != 4 {
		t.Errorf("dt %v", dt)
	}
	// A sentence: no dictionary asked for.
	g.Translate(t.Context(), Request{Text: "good morning", To: Simplified})
	if dt := s.requests()[1].Query["dt"]; len(dt) != 1 {
		t.Errorf("dt for a sentence %v", dt)
	}
}

func TestIsWord(t *testing.T) {
	for in, want := range map[string]bool{"good": true, "serendipity": true, "don't": true, "well-known": true, "你好": true,
		"good morning": false, "今天天气很好": false, "": false, "42": false, "hello!": false, "日本語": true} {
		if got := IsWord(in); got != want {
			t.Errorf("IsWord(%q) = %v", in, got)
		}
	}
}
