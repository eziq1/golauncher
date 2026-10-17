package cred

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRoundTrip writes, reads and deletes a secret of the login keychain.
// It runs only with GOLAUNCHER_KEYCHAIN_TESTS=1: the keychain may be locked
// or ask for permission (CI, a test binary with another signature).
func TestRoundTrip(t *testing.T) {
	if os.Getenv("GOLAUNCHER_KEYCHAIN_TESTS") != "1" {
		t.Skip("touches the login keychain; set GOLAUNCHER_KEYCHAIN_TESTS=1")
	}
	target := fmt.Sprintf("golauncher-test/%d-%d", os.Getpid(), time.Now().UnixNano())
	t.Cleanup(func() { Delete(target) })

	if _, ok, err := Get(target); err != nil || ok {
		t.Fatalf("Get before Set = %v, %v", ok, err)
	}
	for _, secret := range []string{"s3cret", "密码 🔑 with spaces", "", strings.Repeat("x", MaxSecretLen)} {
		if err := Set(target, secret); err != nil {
			t.Fatal(err)
		}
		got, ok, err := Get(target)
		if err != nil || !ok || got != secret {
			t.Fatalf("Get = %q, %v, %v; want %q", got, ok, err, secret)
		}
	}
	if err := Delete(target); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Get(target); err != nil || ok {
		t.Fatalf("Get after Delete = %v, %v", ok, err)
	}
	if err := Delete(target); err != nil {
		t.Fatalf("deleting a missing secret: %v", err)
	}
}

func TestTooLong(t *testing.T) {
	if err := Set("golauncher-test/never-written", strings.Repeat("x", MaxSecretLen+1)); err == nil {
		t.Fatal("want an error for a secret that is too long")
	}
}
