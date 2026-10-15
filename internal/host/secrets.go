package host

import (
	"sync"

	"github.com/eziq1/golauncher/internal/winapi/cred"
)

// Secrets stores the values of password preferences outside the
// configuration file (design §6). Implementations are safe for concurrent
// use.
type Secrets interface {
	// Get returns the secret of preference pref of plugin pluginID; ok is
	// false when there is none.
	Get(pluginID, pref string) (secret string, ok bool, err error)
	// Set stores the secret of a preference, replacing what was there.
	Set(pluginID, pref, secret string) error
	// Delete removes the secret of a preference; a missing one is not an
	// error.
	Delete(pluginID, pref string) error
}

// CredentialSecrets keeps secrets in the Windows Credential Manager as
// generic credentials named golauncher/<pluginID>/<pref>.
type CredentialSecrets struct{}

var _ Secrets = CredentialSecrets{}

// SecretTarget returns the Credential Manager target name of a preference.
func SecretTarget(pluginID, pref string) string {
	return "golauncher/" + pluginID + "/" + pref
}

func (CredentialSecrets) Get(pluginID, pref string) (string, bool, error) {
	return cred.Get(SecretTarget(pluginID, pref))
}

func (CredentialSecrets) Set(pluginID, pref, secret string) error {
	return cred.Set(SecretTarget(pluginID, pref), secret)
}

func (CredentialSecrets) Delete(pluginID, pref string) error {
	return cred.Delete(SecretTarget(pluginID, pref))
}

// MemorySecrets keeps secrets in memory, for fake environments and tests.
type MemorySecrets struct {
	mu sync.Mutex
	m  map[string]string
}

var _ Secrets = (*MemorySecrets)(nil)

// NewMemorySecrets returns an empty MemorySecrets.
func NewMemorySecrets() *MemorySecrets { return &MemorySecrets{m: map[string]string{}} }

func (s *MemorySecrets) Get(pluginID, pref string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[SecretTarget(pluginID, pref)]
	return v, ok, nil
}

func (s *MemorySecrets) Set(pluginID, pref, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[SecretTarget(pluginID, pref)] = secret
	return nil
}

func (s *MemorySecrets) Delete(pluginID, pref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, SecretTarget(pluginID, pref))
	return nil
}
