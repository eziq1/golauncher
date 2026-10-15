package cred

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("touches the Windows Credential Manager")
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
	if err := Set(target, strings.Repeat("x", MaxSecretLen+1)); err == nil {
		t.Fatal("want an error for a secret that is too long")
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
