package cli

import (
	"crypto/rand"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	upd "github.com/eziq1/golauncher/internal/update"
)

// TestUpdateGolden covers glctl update against the fake GitHub; nothing
// goes online.
func TestUpdateGolden(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		code int
	}{
		{"update_check", []string{"update", "check", "--current", "0.4.0", "--arch", "amd64"}, 0},
		{"update_check_uptodate", []string{"update", "check", "--current", "1.0.0", "--arch", "arm64", "--format", "text"}, 0},
		{"update_check_no_arch", []string{"update", "check", "--current", "0.4.0", "--arch", "386"}, 0},
		{"update_check_dev", []string{"update", "check", "--current", "0.4.0-3-gabc1234", "--latest", "0.5.0", "--arch", "amd64", "--format", "text"}, 0},
		{"update_download_dry_run", []string{"update", "download", "--dry-run", "--current", "0.4.0", "--arch", "amd64"}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, code := glctl(t, tt.args...)
			if code != tt.code {
				t.Fatalf("exit code %d, want %d; output:\n%s", code, tt.code, out)
			}
			golden(t, tt.name, out)
		})
	}
	if _, code := glctl(t, "update", "download", "--current", "0.4.0"); code != 2 {
		t.Errorf("download without --dry-run: exit %d", code)
	}
	if _, code := glctl(t, "update"); code != 2 {
		t.Errorf("no subcommand: exit %d", code)
	}
}

func TestUpdateSignVerify(t *testing.T) {
	seed, pub, err := upd.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "checksums.txt")
	os.WriteFile(file, []byte(strings.Repeat("ab", 32)+"  golauncher_1.0.0_windows_amd64.zip\n"), 0o644)
	t.Setenv(EnvSigningKey, "")
	if _, code := glctl(t, "update", "sign", file); code != 1 {
		t.Fatalf("sign without a key: exit %d", code)
	}
	t.Setenv(EnvSigningKey, seed)
	out, code := glctl(t, "update", "sign", file)
	if code != 0 || !strings.Contains(out, pub) {
		t.Fatalf("sign: %d\n%s", code, out)
	}
	if out, code := glctl(t, "update", "verify", file, "--key", pub); code != 0 {
		t.Fatalf("verify: %d\n%s", code, out)
	}
	os.WriteFile(file, []byte("changed\n"), 0o644)
	if _, code := glctl(t, "update", "verify", file, "--key", pub); code != 1 {
		t.Fatalf("verify of a changed file: exit %d", code)
	}
	out, code = glctl(t, "update", "keygen")
	var keys struct {
		PrivateKey string `json:"privateKey"`
		PublicKey  string `json:"publicKey"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &keys) != nil || keys.PrivateKey == "" {
		t.Fatalf("keygen: %d\n%s", code, out)
	}
	if got, _ := upd.PublicKeyOf(keys.PrivateKey); got != keys.PublicKey {
		t.Fatal("keygen printed keys that do not match")
	}
}

// TestUpdateRealGitHub checks the real GitHub, read only: it never
// downloads or installs anything. Opt in with GOLAUNCHER_UPDATE_REAL_TESTS=1.
func TestUpdateRealGitHub(t *testing.T) {
	if os.Getenv("GOLAUNCHER_UPDATE_REAL_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_UPDATE_REAL_TESTS=1 to query GitHub")
	}
	out, code := glctl(t, "update", "check", "--real", "--current", "0.0.1", "--arch", "amd64")
	var res updateCheckResult
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if res.Source != "github" || !res.Newer || !strings.HasPrefix(res.Tag, "v") || !res.Checksums || res.Archive == "" {
		t.Fatalf("result = %+v", res)
	}
}
