package agentrt

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// SessionMap maps chat IDs to the session IDs a CLI assigned
// (Info.AssignsSessionID), kept in a JSON file so that a chat resumes its
// session after a restart of the launcher. It is safe for concurrent use.
type SessionMap struct {
	path string

	mu     sync.Mutex
	loaded bool
	m      map[string]string
}

// NewSessionMap returns the map kept in the file path.
func NewSessionMap(path string) *SessionMap { return &SessionMap{path: path} }

// load reads the file once. s.mu must be held.
func (s *SessionMap) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	s.m = map[string]string{}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &s.m)
	if s.m == nil {
		s.m = map[string]string{}
	}
}

// Get returns the CLI's session ID of a chat, "" when none is known.
func (s *SessionMap) Get(chat string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	return s.m[chat]
}

// Set records the CLI's session ID of a chat.
func (s *SessionMap) Set(chat, session string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	if s.m[chat] == session {
		return nil
	}
	s.m[chat] = session
	return s.save()
}

// Delete forgets a chat.
func (s *SessionMap) Delete(chat string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	if _, ok := s.m[chat]; !ok {
		return nil
	}
	delete(s.m, chat)
	return s.save()
}

// save writes the file atomically. s.mu must be held.
func (s *SessionMap) save() error {
	b, err := json.MarshalIndent(s.m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return nil
}
