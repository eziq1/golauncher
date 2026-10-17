package translator

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/translator/engines"
)

// use runs a recorded copy or paste as the host would: the callback, then
// its follow-up action.
func use(t *testing.T, e *env, a plugin.Action) plugin.Result {
	t.Helper()
	if a.Do.Kind != plugin.ActionCallback || a.Do.Target != cbUse {
		t.Fatalf("not recorded: %+v", a)
	}
	r, err := e.p.HandleAction(t.Context(), a.Do)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func historyItems(t *testing.T, e *env) plugin.View {
	t.Helper()
	v, err := e.p.View(t.Context(), plugin.ViewRequest{ID: viewHistory})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestHistoryOffByDefault(t *testing.T) {
	e := newEnv(t, Options{Sync: true})
	it := e.root("tr hello")[0]
	if a := it.Actions[0]; a.Do.Kind != plugin.ActionCopyText {
		t.Fatalf("copy with the history off: %+v", a)
	}
	if v := historyItems(t, e); len(v.Sections) != 0 || !strings.Contains(v.EmptyText, "off") {
		t.Errorf("history view %+v", v)
	}
	if _, ok, _ := e.h.KV.Get(keyHistory); ok {
		t.Error("something stored")
	}
}

func TestHistory(t *testing.T) {
	e := newEnv(t, Options{Sync: true})
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	e.h.Clock = func() time.Time { return now }
	e.set(prefHistory, true)
	// Typing records nothing; copying does.
	it := e.root("tr hello")[0]
	if _, ok, _ := e.h.KV.Get(keyHistory); ok {
		t.Fatal("recorded while typing")
	}
	r := use(t, e, it.Actions[0])
	if r.Do == nil || r.Do.Kind != plugin.ActionCopyText || r.Do.Target != "[zh-CN] hello" {
		t.Fatalf("follow-up %+v", r)
	}
	now = now.Add(time.Hour)
	use(t, e, e.root("tr >ja good night")[0].Actions[1]) // paste
	v := historyItems(t, e)
	items := v.Sections[0].Items
	if len(items) != 2 || items[0].Title != "[ja] good night" || items[1].Title != "[zh-CN] hello" || items[0].Subtitle != "good night" {
		t.Fatalf("history %+v", items)
	}
	if a := action(t, items[1], "again"); a.Do.Kind != plugin.ActionPush || !strings.Contains(string(a.Do.Payload), `"text":"hello"`) {
		t.Errorf("again %+v", a)
	}
	// The same translation again moves to the top.
	now = now.Add(time.Hour)
	use(t, e, e.root("tr hello")[0].Actions[0])
	if items := historyItems(t, e).Sections[0].Items; len(items) != 2 || items[0].Title != "[zh-CN] hello" {
		t.Errorf("after again %v", ids(items))
	}
	// Forget one.
	forget := action(t, historyItems(t, e).Sections[0].Items[1], "forget")
	if forget.Style != plugin.StyleDestructive {
		t.Error("forget is not destructive")
	}
	e.p.HandleAction(t.Context(), forget.Do)
	if items := historyItems(t, e).Sections[0].Items; len(items) != 1 {
		t.Errorf("after forget %v", ids(items))
	}
	// Retention: 7 days.
	e.set(prefHistoryDays, "7")
	now = now.Add(8 * 24 * time.Hour)
	if v := historyItems(t, e); len(v.Sections) != 0 {
		t.Errorf("kept after the retention %+v", v.Sections)
	}
	e.p.pruneHistory()
	if _, ok, _ := e.h.KV.Get(keyHistory); ok {
		t.Error("old entries stored")
	}
	// Clear, and turning it off deletes it.
	use(t, e, e.root("tr a")[0].Actions[0])
	r, _ = e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: cbClearHistory})
	if r.Toast == nil || r.Toast.Title != "History cleared" {
		t.Errorf("clear %+v", r)
	}
	if _, ok, _ := e.h.KV.Get(keyHistory); ok {
		t.Error("cleared history stored")
	}
	use(t, e, e.root("tr b")[0].Actions[0])
	e.set(prefHistory, false)
	if _, ok, _ := e.h.KV.Get(keyHistory); ok {
		t.Error("history kept after turning it off")
	}
	if e.root("tr b")[0].Actions[0].Do.Kind != plugin.ActionCopyText {
		t.Error("still recording")
	}
	// The stored entry has no more than the history needs.
	e.set(prefHistory, true)
	use(t, e, e.root("tr c")[0].Actions[0])
	b, _, _ := e.h.KV.Get(keyHistory)
	if s := string(b); !strings.Contains(s, `"text":"c"`) || strings.Contains(s, "secret") {
		t.Errorf("stored %s", s)
	}
}

func TestHistoryButton(t *testing.T) {
	m := New().Manifest()
	i := slices.IndexFunc(m.Preferences, func(p plugin.Preference) bool { return p.Name == prefClearHistory })
	if i < 0 || m.Preferences[i].Type != plugin.PrefButton || m.Preferences[i].Action.Target != cbClearHistory {
		t.Fatalf("clear button %+v", m.Preferences)
	}
	j := slices.IndexFunc(m.Preferences, func(p plugin.Preference) bool { return p.Name == prefHistory })
	if string(m.Preferences[j].Default) != "false" {
		t.Error("history on by default")
	}
}

func TestCustomEngines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"result":{"text":"C:%s>%s"}}`, r.URL.Query().Get("q"), r.URL.Query().Get("to"))
	}))
	t.Cleanup(srv.Close)
	e := newEnv(t, Options{Sync: true, BaseURLs: map[string]string{engGoogle: srv.URL}})
	e.set(prefEngines, []string{"google"})
	e.set(prefCustom, []string{"Mine | GET | " + srv.URL + "/?q={text}&to={to} | result.text", "broken line", ""})
	items := e.root("tr >de hello")
	if got := ids(items); !slices.Equal(got, []string{"error:google", "result:custom:Mine"}) {
		t.Fatalf("custom after the list: %v", got)
	}
	if items[1].Title != "C:hello>de" || !strings.HasPrefix(items[1].Subtitle, "Mine · ") {
		t.Errorf("custom row %+v", items[1])
	}
	// Named in the list: placed there.
	e.set(prefEngines, []string{"custom:mine", "google"})
	if got := ids(e.root("tr >fr hello")); got[0] != "result:custom:Mine" {
		t.Errorf("listed custom: %v", got)
	}
}

func TestOllamaEngine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			fmt.Fprint(w, `{"models":[{"name":"qwen2.5:latest"}]}`)
		case "/v1/chat/completions":
			fmt.Fprint(w, `{"choices":[{"message":{"content":"Hallo"}}]}`)
		}
	}))
	t.Cleanup(srv.Close)
	e := newEnv(t, Options{Sync: true, BaseURLs: map[string]string{engOllama: srv.URL}})
	// Unused until a model is chosen.
	if got := ids(e.root("tr >de hello")); slices.Contains(got, "result:ollama") {
		t.Fatalf("ollama without a model: %v", got)
	}
	opts, err := e.p.PreferenceOptions(t.Context(), prefOllamaModel)
	if err != nil || len(opts) != 2 || opts[1].Value != "qwen2.5:latest" {
		t.Fatalf("models %+v %v", opts, err)
	}
	e.set(prefEngines, []string{"ollama"})
	e.set(prefOllamaModel, "qwen2.5")
	items := e.root("tr >de hello")
	if len(items) == 0 || items[0].ID != "result:ollama" || items[0].Title != "Hallo" || !strings.HasPrefix(items[0].Subtitle, "Ollama · ") {
		t.Fatalf("ollama %+v", items)
	}
	if _, ok := e.p.settings().engines[0].(*engines.Ollama); !ok {
		t.Error("not the Ollama engine")
	}
}

func TestOllamaOptionsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	e := newEnv(t, Options{BaseURLs: map[string]string{engOllama: url}})
	opts, err := e.p.PreferenceOptions(t.Context(), prefOllamaModel)
	if err == nil || len(opts) != 1 || !strings.Contains(err.Error(), "Ollama is not running") {
		t.Errorf("%+v %v", opts, err)
	}
}
