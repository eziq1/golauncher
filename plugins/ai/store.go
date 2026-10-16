package ai

import (
	"crypto/rand"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Role is who wrote a message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one message of a chat, as the launcher shows it again. This is
// golauncher's own record (docs/ai.md §7): the CLI's session files are an
// internal format that is never read; --resume restores the model's side.
type Message struct {
	Role Role      `json:"role"`
	Text string    `json:"text"`
	Time time.Time `json:"time"`
	// Tools are the names of the tools the model used for an answer (e.g.
	// "WebSearch"), as older records have them; newer ones have Steps.
	Tools []string `json:"tools,omitempty"`
	// Steps are the tool calls of an answer, in order.
	Steps []ToolStep `json:"steps,omitempty"`
	// Error is the error that ended an answer, as shown (ErrorKind:message).
	Error string `json:"error,omitempty"`
	// Stopped marks an answer the user stopped.
	Stopped bool `json:"stopped,omitzero"`
}

// ToolStep is a tool call of an answer, shown as one line: e.g. a search
// with golauncher's tools ("mcp__golauncher__search") or a web search.
type ToolStep struct {
	// ID is the backend's ID of the call ("" when it has none).
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	// Input is the JSON text of the arguments and Result the text of the
	// result, both capped (maxStepInput, maxStepResult).
	Input  string `json:"input,omitempty"`
	Result string `json:"result,omitempty"`
	// Error marks a call that failed; Done one whose result came.
	Error bool `json:"error,omitzero"`
	Done  bool `json:"done,omitzero"`
}

// Session is the summary of a chat in the history.
type Session struct {
	ID      string    `json:"id"` // also the CLI's --session-id
	Backend string    `json:"backend"`
	Title   string    `json:"title"` // the first message, shortened
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	// Messages is the number of messages.
	Messages int `json:"messages"`
	// Started is set once the backend has the session (an answer came), so
	// that it is resumed rather than started again.
	Started bool `json:"started,omitzero"`
}

const (
	indexKey      = "sessions"
	sessionPrefix = "session/"
	titleRunes    = 80
)

// store keeps the chats in the plugin's key/value storage: the list of
// sessions under indexKey and each chat's messages under its own key.
type store struct {
	kv plugin.KV

	mu sync.Mutex
}

// newSessionID returns a random (version 4) UUID.
func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// titleOf is the history title of a chat starting with text.
func titleOf(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= titleRunes {
		return text
	}
	r := []rune(text)
	return strings.TrimSpace(string(r[:titleRunes-1])) + "…"
}

func (s *store) index() ([]Session, error) {
	b, ok, err := s.kv.Get(indexKey)
	if err != nil || !ok {
		return nil, err
	}
	var out []Session
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *store) setIndex(list []Session) error {
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return s.kv.Set(indexKey, b)
}

// Sessions lists the chats, the most recently used first.
func (s *store) Sessions() ([]Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.index()
	slices.SortStableFunc(list, func(a, b Session) int { return b.Updated.Compare(a.Updated) })
	return list, err
}

// Get returns a chat and its messages.
func (s *store) Get(id string) (Session, []Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.index()
	if err != nil {
		return Session{}, nil, err
	}
	i := slices.IndexFunc(list, func(x Session) bool { return x.ID == id })
	if i < 0 {
		return Session{}, nil, fmt.Errorf("ai: no chat %q", id)
	}
	b, ok, err := s.kv.Get(sessionPrefix + id)
	if err != nil || !ok {
		return list[i], nil, err
	}
	var msgs []Message
	err = json.Unmarshal(b, &msgs)
	return list[i], msgs, err
}

// Save stores a chat and its messages.
func (s *store) Save(sess Session, msgs []Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(msgs)
	if err != nil {
		return err
	}
	if err := s.kv.Set(sessionPrefix+sess.ID, b); err != nil {
		return err
	}
	list, err := s.index()
	if err != nil {
		return err
	}
	sess.Messages = len(msgs)
	if i := slices.IndexFunc(list, func(x Session) bool { return x.ID == sess.ID }); i >= 0 {
		list[i] = sess
	} else {
		list = append(list, sess)
	}
	return s.setIndex(list)
}

// Delete removes a chat.
func (s *store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.index()
	if err != nil {
		return err
	}
	list = slices.DeleteFunc(list, func(x Session) bool { return x.ID == id })
	if err := s.kv.Delete(sessionPrefix + id); err != nil {
		return err
	}
	return s.setIndex(list)
}

// Clear removes every chat and returns how many there were.
func (s *store) Clear() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.index()
	if err != nil {
		return 0, err
	}
	for _, x := range list {
		if err := s.kv.Delete(sessionPrefix + x.ID); err != nil {
			return 0, err
		}
	}
	return len(list), s.setIndex(nil)
}

// Prune removes the chats not used since before; it returns how many. keep
// lists chats that stay anyway (open ones).
func (s *store) Prune(before time.Time, keep func(id string) bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.index()
	if err != nil {
		return 0, err
	}
	var kept []Session
	n := 0
	for _, x := range list {
		if !x.Updated.Before(before) || keep != nil && keep(x.ID) {
			kept = append(kept, x)
			continue
		}
		if err := s.kv.Delete(sessionPrefix + x.ID); err != nil {
			return n, err
		}
		n++
	}
	if n == 0 {
		return 0, nil
	}
	return n, s.setIndex(kept)
}
