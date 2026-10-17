package translator

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/translator/engines"
)

// fakes makes engines.Fake engines and remembers them by ID.
type fakes struct {
	mu   sync.Mutex
	errs map[string]error
	from map[string]string // source language to report, by engine
	made map[string][]*engines.Fake
}

func newFakes() *fakes {
	return &fakes{errs: map[string]error{}, from: map[string]string{}, made: map[string][]*engines.Fake{}}
}

func (f *fakes) engine(id string) engines.Engine {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := &engines.Fake{EngineID: id, Err: f.errs[id]}
	f.made[id] = append(f.made[id], e)
	if from := f.from[id]; from != "" {
		return fromEngine{e, from}
	}
	return e
}

// calls are all requests made to the engines with id.
func (f *fakes) calls(id string) []engines.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []engines.Request
	for _, e := range f.made[id] {
		out = append(out, e.Calls()...)
	}
	return out
}

func (f *fakes) total() int {
	n := 0
	for _, id := range []string{engGoogle, engMyMemory, engDeepL} {
		n += len(f.calls(id))
	}
	return n
}

// fromEngine reports a fixed source language ("bonjour" is French).
type fromEngine struct {
	*engines.Fake
	from string
}

func (e fromEngine) Translate(ctx context.Context, r engines.Request) (engines.Result, error) {
	res, err := e.Fake.Translate(ctx, r)
	res.From = e.from
	return res, err
}

// fakeAfter hands out debounce timers that fire only when released.
type fakeAfter struct {
	mu    sync.Mutex
	chans []chan time.Time
}

func (f *fakeAfter) after(time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := make(chan time.Time, 1)
	f.chans = append(f.chans, c)
	return c
}

func (f *fakeAfter) fireAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.chans {
		c <- time.Time{}
	}
	f.chans = nil
}

type env struct {
	t *testing.T
	p *Plugin
	h *hostfake.Host
	f *fakes
}

func newEnv(t *testing.T, o Options, setup ...func(*hostfake.Host)) *env {
	t.Helper()
	f := newFakes()
	if o.Engine == nil && o.BaseURLs == nil {
		o.Engine = f.engine
	}
	if o.Speaker == nil {
		o.Speaker = &fakeSpeaker{} // tests never speak
	}
	p := NewWithOptions(o)
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range setup {
		s(h)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return &env{t: t, p: p, h: h, f: f}
}

func (e *env) set(name string, v any) {
	e.h.Prefs.Set(name, plugin.MustPayload(v))
	e.p.configChanged() // what the host's config-changed event runs, synchronously
}

func (e *env) root(text string) []plugin.Item {
	e.t.Helper()
	items, err := e.p.RootItems(e.t.Context(), plugin.Query{Text: text})
	if err != nil {
		e.t.Fatal(err)
	}
	return items
}

// wait waits for the requests being made.
func (e *env) wait() {
	e.p.typing.wg.Wait()
	e.p.sel.wg.Wait()
}

func ids(items []plugin.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func action(t *testing.T, it plugin.Item, id string) plugin.Action {
	t.Helper()
	for _, a := range it.Actions {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("%s has no action %q: %+v", it.ID, id, it.Actions)
	return plugin.Action{}
}

func TestParseQuery(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want query
	}{
		{"hello world", query{Text: "hello world"}},
		{"ja 你好", query{To: "ja", Text: "你好"}},
		{"JA 你好", query{To: "ja", Text: "你好"}},
		{">ja 你好", query{To: "ja", Text: "你好"}},
		{"→ja 你好", query{To: "ja", Text: "你好"}},
		{"日 你好", query{To: "ja", Text: "你好"}},
		{"日语 你好", query{To: "ja", Text: "你好"}},
		{"en>zh apple", query{From: "en", To: "zh-CN", Text: "apple"}},
		{"英>中 apple pie", query{From: "en", To: "zh-CN", Text: "apple pie"}},
		{"auto>fr hello", query{To: "fr", Text: "hello"}},
		{"en->de hello", query{From: "en", To: "de", Text: "hello"}},
		{"zh-tw 你好", query{To: "zh-TW", Text: "你好"}},
		{"繁 你好", query{To: "zh-TW", Text: "你好"}},
		{"jp konnichiwa", query{To: "ja", Text: "konnichiwa"}},
		// English words that are codes need ">": "it is" is text.
		{"it is raining", query{Text: "it is raining"}},
		{">it it is raining", query{To: "it", Text: "it is raining"}},
		{"en>it hello", query{From: "en", To: "it", Text: "hello"}},
		{"hi there", query{Text: "hi there"}},
		// A code alone waits for the text; a Chinese name alone is a word.
		{"ja", query{To: "ja"}},
		{"日语", query{Text: "日语"}},
		{"hi", query{Text: "hi"}},
		{">ja", query{To: "ja"}},
		{"xx>yy hello", query{Text: "xx>yy hello"}},
		{"a>b", query{Text: "a>b"}},
		{"", query{}},
	} {
		if got := parseQuery(tc.in); got != tc.want {
			t.Errorf("parseQuery(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestCutPrefix(t *testing.T) {
	for _, tc := range []struct {
		in, rest string
		ok       bool
	}{
		{"tr hello", "hello", true},
		{"TR hello", "hello", true},
		{"fy 你好", "你好", true},
		{"翻译 hello", "hello", true},
		{"翻译　hello", "hello", true}, // full-width space
		{"tr", "", true},
		{"translate hello", "", false},
		{"trello", "", false},
		{"翻译器", "", false},
		{"hello", "", false},
	} {
		rest, ok := cutPrefix(tc.in)
		if rest != tc.rest || ok != tc.ok {
			t.Errorf("cutPrefix(%q) = %q, %v", tc.in, rest, ok)
		}
	}
}

func TestAutoTarget(t *testing.T) {
	for _, tc := range []struct {
		base, src, text, want string
	}{
		{"en", "", "你好", "en"},
		{"en", "", "hello", "zh-CN"}, // English text in the English UI (R65)
		{"zh-CN", "", "hello", "zh-CN"},
		{"zh-CN", "", "你好", "en"},
		{"zh-CN", "", "こんにちは", "zh-CN"},
		{"en", "fr", "bonjour", "en"}, // detected: not English after all
		{"en", "en", "bonjour", "zh-CN"},
		{"zh-CN", "zh-TW", "妳好", "zh-CN"},
		{"ja", "", "こんにちは", "en"},
	} {
		if got := autoTarget(tc.base, tc.src, tc.text); got != tc.want {
			t.Errorf("autoTarget(%q, %q, %q) = %q, want %q", tc.base, tc.src, tc.text, got, tc.want)
		}
	}
	if baseTarget("", "zh-CN") != "zh-CN" || baseTarget("", "en") != "en" || baseTarget("ja", "zh-CN") != "ja" || baseTarget("xx", "en") != "en" {
		t.Error("baseTarget")
	}
}

// TestNoPrefixNoRequest: nothing is sent without a prefix, nor for the
// selected text with the preference off (the default).
func TestNoPrefixNoRequest(t *testing.T) {
	e := newEnv(t, Options{Sync: true})
	for _, q := range []string{"hello", "translate hello", "trello", "trhello", "g hello", "tr", "tr ", "tr ja", "翻译"} {
		e.root(q)
	}
	items, err := e.p.SelectionItems(t.Context(), plugin.SelectionQuery{Selection: plugin.Selection{Text: "hello world"}})
	if err != nil || items != nil {
		t.Fatalf("selection items with the preference off: %v %v", items, err)
	}
	e.wait()
	if n := e.f.total(); n != 0 {
		t.Fatalf("%d requests without a prefix: %+v", n, e.f.calls(engGoogle))
	}
	// The prefix with languages but no text: a hint.
	if got := ids(e.root("tr ja")); !slices.Equal(got, []string{"hint"}) {
		t.Errorf("tr ja: %v", got)
	}
}

func TestRootTranslates(t *testing.T) {
	fa := &fakeAfter{}
	e := newEnv(t, Options{After: fa.after})
	items := e.root("tr hello world")
	if got := ids(items); !slices.Equal(got, []string{"pending:google"}) {
		t.Fatalf("before the answer: %v", got)
	}
	if !items[0].Transient || items[0].Title != "Translating…" {
		t.Errorf("pending %+v", items[0])
	}
	fa.fireAll()
	e.wait()
	if !slices.Contains(e.h.Invalidated(), "") {
		t.Error("no Invalidate after the answer")
	}
	items = e.root("tr hello world")
	if got := ids(items); !slices.Equal(got, []string{"result:google"}) {
		t.Fatalf("after the answer: %v", got)
	}
	it := items[0]
	if it.Title != "[zh-CN] hello world" || it.Subtitle != "Google Translate · English → Chinese (Simplified)" || !it.Transient ||
		it.Score != 1 || it.Group != "Translator" {
		t.Errorf("row %+v", it)
	}
	if a := action(t, it, "copy"); a.Do.Kind != plugin.ActionCopyText || a.Do.Target != "[zh-CN] hello world" || it.Actions[0].ID != "copy" {
		t.Errorf("copy %+v", a)
	}
	if a := it.Actions[1]; a.ID != "paste" || a.Do.Kind != plugin.ActionPaste || a.Do.Target != "[zh-CN] hello world" {
		t.Errorf("paste (Ctrl+Enter) %+v", a)
	}
	if a := action(t, it, "web-google"); a.Do.Kind != plugin.ActionOpen ||
		a.Do.Target != "https://translate.google.com/?sl=en&tl=zh-CN&text=hello+world&op=translate" {
		t.Errorf("google web %+v", a)
	}
	if a := action(t, it, "web-deepl"); a.Do.Target != "https://www.deepl.com/translator#en/zh/hello%20world" {
		t.Errorf("deepl web %+v", a)
	}
	if a := action(t, it, "web-youdao"); a.Do.Target != "https://www.youdao.com/result?word=hello+world&lang=en" {
		t.Errorf("youdao web %+v", a)
	}
	swap := action(t, it, "swap")
	var pl viewPayload
	json.Unmarshal(swap.Do.Payload, &pl)
	if swap.Do.Kind != plugin.ActionPush || swap.Do.Target != viewTranslate || pl != (viewPayload{Text: "[zh-CN] hello world", From: "zh-CN", To: "en"}) {
		t.Errorf("swap %+v %+v", swap, pl)
	}
	// The answer is cached: asked once.
	e.root("tr hello world")
	e.wait()
	if n := len(e.f.calls(engGoogle)); n != 1 {
		t.Errorf("%d requests for one text", n)
	}
	// The given target, from the syntax.
	e.root("tr >ja hello")
	fa.fireAll()
	e.wait()
	if items := e.root("tr >ja hello"); items[0].Title != "[ja] hello" {
		t.Errorf("tr >ja: %+v", items[0])
	}
}

func TestDebounceAndCancel(t *testing.T) {
	fa := &fakeAfter{}
	e := newEnv(t, Options{After: fa.after})
	// Typing quickly: only the last text is asked for.
	for _, q := range []string{"tr h", "tr he", "tr hel", "tr hell"} {
		e.root(q)
	}
	fa.fireAll()
	e.wait()
	calls := e.f.calls(engGoogle)
	if len(calls) != 1 || calls[0].Text != "hell" {
		t.Fatalf("requests %+v", calls)
	}
	// Leaving the prefix cancels the pending request.
	e.root("tr hello")
	e.root("hello")
	fa.fireAll()
	e.wait()
	if n := len(e.f.calls(engGoogle)); n != 1 {
		t.Errorf("%d requests after leaving the prefix", n)
	}
	// Hiding the window too.
	e.root("tr hello there")
	e.p.windowHidden()
	e.wait()
	fa.fireAll()
	e.wait()
	if n := len(e.f.calls(engGoogle)); n != 1 {
		t.Errorf("%d requests after hiding", n)
	}
	// A cancelled text is asked again when typed again.
	e.root("tr hello")
	fa.fireAll()
	e.wait()
	if n := len(e.f.calls(engGoogle)); n != 2 {
		t.Errorf("%d requests", n)
	}
}

func TestDebounceDelay(t *testing.T) {
	var asked atomic.Int64
	e := newEnv(t, Options{After: func(d time.Duration) <-chan time.Time {
		asked.Store(int64(d))
		return time.After(time.Millisecond)
	}})
	e.root("tr hello")
	e.wait()
	if time.Duration(asked.Load()) != 300*time.Millisecond {
		t.Errorf("debounce %v", time.Duration(asked.Load()))
	}
}

func TestEnginesAndKeys(t *testing.T) {
	e := newEnv(t, Options{Sync: true})
	if got := ids(e.root("tr hello")); !slices.Equal(got, []string{"result:google"}) {
		t.Fatalf("default engines: %v", got)
	}
	// A DeepL key: DeepL comes first.
	e.set(prefDeepLKey, "secret:fx")
	if got := ids(e.root("tr hello")); !slices.Equal(got, []string{"result:deepl", "result:google"}) {
		t.Fatalf("with a DeepL key: %v", got)
	}
	// The user's order; MyMemory listed.
	e.set(prefEngines, []string{"google", "mymemory", "unknown", "google"})
	if got := ids(e.root("tr hello")); !slices.Equal(got, []string{"result:google", "result:mymemory"}) {
		t.Fatalf("listed: %v", got)
	}
}

func TestFallback(t *testing.T) {
	e := newEnv(t, Options{Sync: true}, func(*hostfake.Host) {})
	e.f.errs[engGoogle] = fmt.Errorf("dial: %w", errOffline)
	e.set(prefEngines, []string{"google"}) // rebuild the engines with the error
	items := e.root("tr hello")
	if got := ids(items); !slices.Equal(got, []string{"result:mymemory"}) {
		t.Fatalf("fallback: %v", got)
	}
	if !strings.Contains(items[0].Subtitle, "MyMemory") || !strings.Contains(items[0].Subtitle, "Google Translate unavailable") {
		t.Errorf("subtitle %q", items[0].Subtitle)
	}
	// Without the fallback, the error shows.
	e.set(prefFallback, false)
	items = e.root("tr hello again")
	if got := ids(items); !slices.Equal(got, []string{"error:google"}) || items[0].Title != "Google Translate: no connection" {
		t.Fatalf("error row: %v %+v", got, items)
	}
	// Errors by kind.
	for err, want := range map[error]string{
		engines.ErrQuota: "Google Translate: quota or rate limit exhausted", engines.ErrAuth: "Google Translate: the key was rejected",
		context.DeadlineExceeded: "Google Translate: no answer in time", fmt.Errorf("x"): "Google Translate: translation failed",
	} {
		e.f.errs[engGoogle] = err
		e.set(prefEngines, []string{"google"})
		if items := e.root("tr " + want); items[0].Title != want {
			t.Errorf("%v: %q", err, items[0].Title)
		}
	}
}

// TestRetarget: with an automatic target, a detected source language that
// is not what the script suggested changes the target ("bonjour" is
// French: into English, not Chinese).
func TestRetarget(t *testing.T) {
	e := newEnv(t, Options{Sync: true})
	e.f.from[engGoogle] = "fr"
	e.set(prefEngines, []string{"google"})
	items := e.root("tr bonjour")
	if items[0].Title != "[en] bonjour" || items[0].Subtitle != "Google Translate · French → English" {
		t.Fatalf("%+v", items[0])
	}
	calls := e.f.calls(engGoogle)
	if len(calls) != 2 || calls[0].To != "zh-CN" || calls[1].To != "en" || calls[1].From != "fr" {
		t.Errorf("calls %+v", calls)
	}
	// A given target is kept.
	e.root("tr >zh bonjour le monde")
	if n := len(e.f.calls(engGoogle)); n != 3 {
		t.Errorf("%d calls", n)
	}
}

func TestChineseUI(t *testing.T) {
	e := newEnv(t, Options{Sync: true}, func(h *hostfake.Host) { h.I18N.SetLocale("zh-CN") })
	items := e.root("翻译 hello")
	if items[0].Title != "[zh-CN] hello" || items[0].Subtitle != "Google 翻译 · 英语 → 简体中文" || items[0].Group != "翻译" {
		t.Fatalf("%+v", items[0])
	}
	if items := e.root("fy 你好"); items[0].Title != "[en] 你好" {
		t.Errorf("Chinese text: %+v", items[0])
	}
	if items := e.root("tr 日 你好"); items[0].Title != "[ja] 你好" {
		t.Errorf("tr 日: %+v", items[0])
	}
	e.set(prefTarget, "ko")
	if items := e.root("tr hello"); items[0].Title != "[ko] hello" {
		t.Errorf("preference: %+v", items[0])
	}
}

func TestSelection(t *testing.T) {
	e := newEnv(t, Options{Sync: true})
	sel := plugin.SelectionQuery{Selection: plugin.Selection{Text: "  good morning  ", Editable: true}}
	items, _ := e.p.SelectionItems(t.Context(), sel)
	if items != nil || e.f.total() != 0 {
		t.Fatal("off by default")
	}
	e.set(prefSelection, true)
	items, _ = e.p.SelectionItems(t.Context(), sel)
	if len(items) != 1 {
		t.Fatalf("items %v", ids(items))
	}
	it := items[0]
	if it.ID != "selection" || it.Title != "[zh-CN] good morning" || it.Score != selectionScore || !it.Transient ||
		it.Subtitle != "Translation · Google Translate · English → Chinese (Simplified)" {
		t.Errorf("row %+v", it)
	}
	if a := it.Actions[1]; a.ID != "replace" || a.Title != "Replace Selected Text" || a.Do.Kind != plugin.ActionPaste {
		t.Errorf("replace %+v", a)
	}
	all := action(t, it, "all")
	var pl viewPayload
	json.Unmarshal(all.Do.Payload, &pl)
	if all.Do.Kind != plugin.ActionPush || pl.Text != "good morning" {
		t.Errorf("all engines %+v", all)
	}
	// Typed text: no Score; not editable: paste.
	sel.Text, sel.Selection.Editable = "x", false
	items, _ = e.p.SelectionItems(t.Context(), sel)
	if items[0].Score != 0 || items[0].Actions[1].ID != "paste" {
		t.Errorf("typed %+v", items[0])
	}
	// Too long: nothing.
	sel.Selection.Text = strings.Repeat("a ", maxSelectionRunes)
	if items, _ := e.p.SelectionItems(t.Context(), sel); items != nil {
		t.Error("long selection translated")
	}
	if n := len(e.f.calls(engGoogle)); n != 1 {
		t.Errorf("%d requests (the selection is cached)", n)
	}
	// Hiding forgets it.
	e.p.windowHidden()
	sel.Selection.Text = "good morning"
	e.p.SelectionItems(t.Context(), sel)
	if n := len(e.f.calls(engGoogle)); n != 2 {
		t.Errorf("%d requests after hiding", n)
	}
}

func TestView(t *testing.T) {
	fa := &fakeAfter{}
	e := newEnv(t, Options{After: fa.after})
	view := func(inst, text string, pl *viewPayload) plugin.View {
		req := plugin.ViewRequest{ID: viewTranslate, Instance: inst, Query: plugin.Query{Text: text}}
		if pl != nil {
			req.Payload = plugin.MustPayload(pl)
		}
		v, err := e.p.View(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	// Opened with a query (an AI's run_command): answered at once.
	v := view("v1", "ja hello", nil)
	if len(v.Sections) != 1 || v.Sections[0].Items[0].Title != "[ja] hello" || v.Sections[0].Items[0].Group != "" {
		t.Fatalf("first rendering %+v", v)
	}
	// Typing afterwards is debounced.
	v = view("v1", "ja hello there", nil)
	if v.Sections[0].Items[0].ID != "pending:google" {
		t.Errorf("typing: %+v", v.Sections[0].Items[0])
	}
	fa.fireAll()
	e.wait()
	// Swap: the payload's text and languages.
	v = view("v2", "", &viewPayload{Text: "[ja] hello", From: "ja", To: "en"})
	if it := v.Sections[0].Items[0]; it.Title != "[en] [ja] hello" || !strings.Contains(it.Subtitle, "Japanese → English") {
		t.Errorf("swap view %+v", it)
	}
	e.p.ViewClosed("v2")
	// Empty: a hint, nothing sent.
	n := e.f.total()
	if v := view("v3", "", nil); len(v.Sections) != 0 || v.EmptyText != "Type the text to translate" || e.f.total() != n {
		t.Errorf("empty view %+v", v)
	}
}

// TestHTTPEngine goes through a real engine and HTTP (a fake Google).
func TestHTTPEngine(t *testing.T) {
	var reqs atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs.Add(1)
		fmt.Fprintf(w, `{"sentences":[{"trans":"T:%s"}],"src":"en"}`, r.URL.Query().Get("q"))
	}))
	t.Cleanup(srv.Close)
	e := newEnv(t, Options{Sync: true, BaseURLs: map[string]string{engGoogle: srv.URL}})
	if items := e.root("tr hello"); items[0].Title != "T:hello" {
		t.Fatalf("%+v", items[0])
	}
	e.root("hello")
	if reqs.Load() != 1 {
		t.Errorf("%d requests", reqs.Load())
	}
}

func TestOffline(t *testing.T) {
	e := newEnv(t, Options{Sync: true, Offline: true, BaseURLs: map[string]string{}})
	items := e.root("tr hello")
	if got := ids(items); !slices.Equal(got, []string{"error:google", "error:mymemory"}) || items[0].Title != "Google Translate: no connection" {
		t.Errorf("%v %+v", got, items)
	}
}

func TestStartAgain(t *testing.T) {
	e := newEnv(t, Options{Sync: true})
	e.root("tr hello")
	if err := e.p.Close(); err != nil {
		t.Fatal(err)
	}
	h, _ := hostfake.New(e.p, t.TempDir())
	if err := e.p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	e.h = h
	if items := e.root("tr hello"); items[0].ID != "result:google" {
		t.Errorf("after Init again: %v", ids(items))
	}
}
