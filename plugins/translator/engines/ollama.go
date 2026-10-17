package engines

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// Ollama translates with a local model through Ollama's OpenAI-compatible
// endpoint (docs/translator.md §8, phase 3): offline, no key, nothing
// leaves the computer. Before translating it checks that the server runs
// and has the model (GET /api/tags, cached for a while).
type Ollama struct {
	Env
	// BaseURL is the server (default http://localhost:11434).
	BaseURL string
	Model   string

	mu      sync.Mutex
	checked time.Time
	models  []string
	err     error
}

const (
	// OllamaBase is Ollama's default address.
	OllamaBase = "http://localhost:11434"
	// ollamaMax is the most text of one request (bytes).
	ollamaMax = 8000
	// ollamaCheckTTL is how long the server check is trusted.
	ollamaCheckTTL = 30 * time.Second
	// ollamaTimeout is how long a local model may take (it may have to load).
	ollamaTimeout = 60 * time.Second
)

// ollamaPrompt is the system prompt (English: models follow it best).
const ollamaPrompt = "You are a translation engine. Translate the user's message from %s into %s. Output only the translation, without quotes, notes or explanations."

// ID implements Engine.
func (o *Ollama) ID() string { return "ollama" }

// Timeout implements Timeouter: a local model may first have to load.
func (o *Ollama) Timeout() time.Duration { return ollamaTimeout }

// Timeouter is implemented by engines that need longer than the plugin's
// usual timeout.
type Timeouter interface{ Timeout() time.Duration }

func (o *Ollama) base() string {
	if b := strings.TrimRight(strings.TrimSpace(o.BaseURL), "/"); b != "" {
		return b
	}
	return OllamaBase
}

// Models lists the models the server has (GET /api/tags); an error wraps
// ErrUnavailable when the server cannot be reached.
func (o *Ollama) Models(ctx context.Context) ([]string, error) {
	req, err := newRequest(ctx, http.MethodGet, o.base()+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := doJSON(o.Client, req, &resp, nil); err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w (Ollama: %v)", ErrUnavailable, err)
	}
	var out []string
	for _, m := range resp.Models {
		out = append(out, m.Name)
	}
	return out, nil
}

// check makes sure the server runs and has the model.
func (o *Ollama) check(ctx context.Context) error {
	o.mu.Lock()
	if !o.checked.IsZero() && time.Since(o.checked) < ollamaCheckTTL {
		models, err := o.models, o.err
		o.mu.Unlock()
		if err != nil {
			return err
		}
		return o.hasModel(models)
	}
	o.mu.Unlock()
	models, err := o.Models(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	o.mu.Lock()
	o.checked, o.models, o.err = time.Now(), models, err
	o.mu.Unlock()
	if err != nil {
		return err
	}
	return o.hasModel(models)
}

func (o *Ollama) hasModel(models []string) error {
	m := strings.TrimSpace(o.Model)
	if slices.Contains(models, m) || !strings.Contains(m, ":") && slices.Contains(models, m+":latest") {
		return nil
	}
	return fmt.Errorf("%w (Ollama has no model %q)", ErrUnavailable, m)
}

// Translate implements Engine.
func (o *Ollama) Translate(ctx context.Context, r Request) (Result, error) {
	if strings.TrimSpace(o.Model) == "" {
		return Result{}, ErrNoKey
	}
	if err := o.check(ctx); err != nil {
		return Result{}, err
	}
	if r.From == "" {
		r.From = Guess(r.Text)
	}
	return translatePieces(o.Env, ctx, r, ollamaMax, o.one)
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (o *Ollama) one(ctx context.Context, r Request) (Result, error) {
	from := "the language it is written in"
	if r.From != "" {
		from = LanguageName(r.From)
	}
	body, err := json.Marshal(map[string]any{
		"model":       strings.TrimSpace(o.Model),
		"stream":      false,
		"temperature": 0,
		"messages": []chatMessage{
			{Role: "system", Content: fmt.Sprintf(ollamaPrompt, from, LanguageName(r.To))},
			{Role: "user", Content: r.Text},
		},
	})
	if err != nil {
		return Result{}, err
	}
	req, err := newRequest(ctx, http.MethodPost, o.base()+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var resp struct {
		Choices []struct {
			Message chatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := doJSON(o.Client, req, &resp, nil); err != nil {
		return Result{}, err
	}
	if len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
		return Result{}, errors.New("ollama: no translation in the response")
	}
	return Result{Text: strings.TrimSpace(resp.Choices[0].Message.Content), From: r.From}, nil
}

// languageNames are English names for prompts.
var languageNames = map[string]string{
	English: "English", Simplified: "Simplified Chinese", Traditional: "Traditional Chinese", Japanese: "Japanese",
	Korean: "Korean", Russian: "Russian", "fr": "French", "de": "German", "es": "Spanish", "pt": "Portuguese",
	"it": "Italian", "ar": "Arabic", "th": "Thai", "vi": "Vietnamese", "id": "Indonesian", "nl": "Dutch",
	"pl": "Polish", "tr": "Turkish", "uk": "Ukrainian", "sv": "Swedish", "hi": "Hindi",
}

// LanguageName is the English name of a language (its code when unknown).
func LanguageName(code string) string {
	if n, ok := languageNames[code]; ok {
		return n
	}
	return code
}
