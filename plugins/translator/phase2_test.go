package translator

import (
	"context"
	"encoding/json/v2"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/winapi/tts"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/translator/engines"
)

// fakeSpeaker records what it is asked to read; it never makes a sound.
type fakeSpeaker struct {
	mu    sync.Mutex
	said  []speakPayload
	err   error
	block chan struct{} // when set, Speak waits for it or the cancel
	stops int
}

func (s *fakeSpeaker) Speak(ctx context.Context, text, lang string) error {
	s.mu.Lock()
	s.said = append(s.said, speakPayload{Text: text, Lang: lang})
	block, err := s.block, s.err
	s.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			s.mu.Lock()
			s.stops++
			s.mu.Unlock()
			return ctx.Err()
		}
	}
	return err
}

func (s *fakeSpeaker) stopped() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stops
}

func (s *fakeSpeaker) calls() []speakPayload {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.said)
}

func TestKeyedEngines(t *testing.T) {
	e := newEnv(t, Options{Sync: true})
	// An app ID without its secret is not enough.
	e.set(prefYoudaoApp, "app")
	e.set(prefBaiduApp, "id")
	if got := ids(e.root("tr hello")); !slices.Equal(got, []string{"result:google"}) {
		t.Fatalf("half keys: %v", got)
	}
	e.set(prefYoudaoKey, "secret")
	e.set(prefBaiduKey, "secret")
	e.set(prefAzureKey, "key")
	if got := ids(e.root("tr hello")); !slices.Equal(got, []string{"result:azure", "result:youdao", "result:baidu", "result:google"}) {
		t.Fatalf("with keys: %v", got)
	}
	items := e.root("tr hello")
	if items[0].Subtitle != "Azure Translator · English → Chinese (Simplified)" || !strings.HasPrefix(items[2].Subtitle, "Baidu Translate") {
		t.Errorf("names %q %q", items[0].Subtitle, items[2].Subtitle)
	}
}

// TestRealEngineSettings: the preferences reach the real engines.
func TestRealEngineSettings(t *testing.T) {
	e := newEnv(t, Options{BaseURLs: map[string]string{}})
	e.set(prefAzureKey, "k")
	e.set(prefAzureRgn, "eastasia")
	e.set(prefYoudaoApp, "a")
	e.set(prefYoudaoKey, "s")
	e.set(prefBaiduApp, "b")
	e.set(prefBaiduKey, "t")
	var got []string
	for _, eng := range e.p.settings().engines {
		switch x := eng.(type) {
		case *engines.Azure:
			got = append(got, "azure:"+x.Key+":"+x.Region)
		case *engines.Youdao:
			got = append(got, "youdao:"+x.AppKey+":"+x.Secret)
		case *engines.Baidu:
			got = append(got, "baidu:"+x.AppID+":"+x.Secret)
		case *engines.Google:
			got = append(got, "google")
		}
	}
	if !slices.Equal(got, []string{"azure:k:eastasia", "youdao:a:s", "baidu:b:t", "google"}) {
		t.Errorf("engines %v", got)
	}
}

func TestDictionary(t *testing.T) {
	e := newEnv(t, Options{Sync: true, Engine: func(id string) engines.Engine {
		return &engines.Fake{EngineID: id, Dict: &engines.Dict{Phonetic: "ˌserənˈdipədē",
			Entries:     []engines.DictEntry{{POS: "noun", Meanings: []string{"意外发现", "机缘巧合"}}},
			Definitions: []engines.DictEntry{{POS: "noun", Meanings: []string{"the occurrence of events by chance in a happy way."}}}}}
	}})
	items := e.root("tr serendipity")
	if got := ids(items); !slices.Equal(got, []string{"result:google", "dict"}) {
		t.Fatalf("items %v", got)
	}
	d := items[1]
	if d.Title != "serendipity  /ˌserənˈdipədē/" || d.Subtitle != "Dictionary · Google Translate · noun 意外发现; 机缘巧合" || d.Score >= items[0].Score ||
		!d.Transient || !strings.Contains(d.Detail.Markdown, "*noun* 意外发现; 机缘巧合") || !strings.Contains(d.Detail.Markdown, "1. the occurrence") {
		t.Errorf("dict %+v %s", d, d.Detail.Markdown)
	}
	if a := action(t, d, "speak-original"); a.Do.Kind != plugin.ActionCallback || !a.ReadOnly {
		t.Errorf("speak %+v", a)
	}
	// Sentences have none.
	if got := ids(e.root("tr good morning")); !slices.Equal(got, []string{"result:google"}) {
		t.Errorf("sentence %v", got)
	}
}

func TestSpeak(t *testing.T) {
	sp := &fakeSpeaker{}
	e := newEnv(t, Options{Sync: true, Speaker: sp})
	it := e.root("tr >ja hello")[0]
	a := action(t, it, "speak")
	if a.Do.Kind != plugin.ActionCallback || a.Do.Target != cbSpeak || !a.ReadOnly {
		t.Fatalf("speak %+v", a)
	}
	if len(sp.calls()) != 0 {
		t.Fatal("spoke before the action")
	}
	if _, err := e.p.HandleAction(t.Context(), a.Do); err != nil {
		t.Fatal(err)
	}
	e.p.speech.Wait()
	e.p.HandleAction(t.Context(), action(t, it, "speak-original").Do)
	e.p.speech.Wait()
	if got := sp.calls(); !slices.Equal(got, []speakPayload{{Text: "[ja] hello", Lang: "ja"}, {Text: "hello", Lang: "en"}}) {
		t.Errorf("said %+v", got)
	}
	// A new speech stops the one going on; Close stops it too.
	sp.mu.Lock()
	sp.block = make(chan struct{})
	sp.mu.Unlock()
	e.p.HandleAction(t.Context(), a.Do)
	e.p.HandleAction(t.Context(), a.Do)
	deadline := time.Now().Add(5 * time.Second)
	for sp.stopped() < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	e.p.Close()
	if n := sp.stopped(); n != 2 {
		t.Errorf("%d speeches stopped", n)
	}
}

func TestSpeakUnsupported(t *testing.T) {
	sp := &fakeSpeaker{err: tts.ErrUnsupported}
	e := newEnv(t, Options{Sync: true, Speaker: sp})
	e.p.HandleAction(t.Context(), action(t, e.root("tr hello")[0], "speak").Do)
	e.p.speech.Wait()
	if ts := e.h.Toasts(); len(ts) != 1 || ts[0].Title != "Reading aloud is not available on this system" {
		t.Errorf("toasts %+v", ts)
	}
}

func TestTranslateWithAI(t *testing.T) {
	e := newEnv(t, Options{Sync: true})
	a := action(t, e.root("tr >ja good morning")[0], "ai")
	var q plugin.AskAI
	if a.Do.Kind != plugin.ActionAskAI || json.Unmarshal(a.Do.Payload, &q) != nil {
		t.Fatalf("ai %+v", a)
	}
	if !reflect.DeepEqual(q, plugin.AskAI{Instruction: "Translate the quoted text into Japanese. Output only the translation.", Text: "good morning",
		Title: "Translate: good morning"}) {
		t.Errorf("payload %+v", q)
	}
}
