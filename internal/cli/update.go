package cli

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/eziq1/golauncher/internal/install"
	upd "github.com/eziq1/golauncher/internal/update"
	"github.com/eziq1/golauncher/internal/update/fakegithub"
)

// EnvSigningKey holds the private key (a base64 ed25519 seed) for
// glctl update sign.
const EnvSigningKey = "GOLAUNCHER_UPDATE_SIGNING_KEY"

// updateFlags are the flags of glctl update check and download.
type updateFlags struct {
	real    bool
	current string
	arch    string
	latest  string // the fake's latest version
	dryRun  bool
	// system is the archive's system: "windows" (the default, whatever
	// glctl runs on, so that the output is the same everywhere) or
	// "darwin" (the universal golauncher.app archive).
	system string
}

// bundle reports whether the macOS archive is chosen.
func (f updateFlags) bundle() bool { return f.system == "darwin" }

// updateCheckResult is the output of glctl update check.
type updateCheckResult struct {
	Source      string `json:"source"` // "fake" or "github"
	Current     string `json:"current"`
	Updatable   bool   `json:"updatable"` // a release build that updates itself when installed
	Latest      string `json:"latest"`
	Tag         string `json:"tag"`
	Newer       bool   `json:"newer"`
	Page        string `json:"page"`
	PublishedAt string `json:"publishedAt,omitempty"`
	Archive     string `json:"archive,omitempty"`
	Size        int64  `json:"size,omitzero"`
	Checksums   bool   `json:"checksums"`
	Signature   bool   `json:"signature"`
	Error       string `json:"error,omitempty"`
}

// updateDownloadResult is the output of glctl update download --dry-run.
type updateDownloadResult struct {
	updateCheckResult
	SHA256   string   `json:"sha256,omitempty"`
	Verified bool     `json:"verified"`
	Signed   bool     `json:"signed"`
	Files    []string `json:"files,omitempty"`
}

func (c *cli) update(ctx context.Context, args []string) error {
	const want = "update: want check, download --dry-run, sign <file>, verify <file> or keygen"
	if len(args) == 0 {
		return usagef(want)
	}
	sub, rest := args[0], args[1:]
	var f updateFlags
	fs := c.flagSet("update "+sub, nil)
	fs.BoolVar(&f.real, "real", false, "query the real GitHub (read only)")
	fs.StringVar(&f.current, "current", "", "the running version to compare with (default: glctl's)")
	fs.StringVar(&f.arch, "arch", runtime.GOARCH, "the architecture whose archive is chosen")
	fs.StringVar(&f.latest, "latest", "1.0.0", "the fake GitHub's latest version (fake mode)")
	fs.BoolVar(&f.dryRun, "dry-run", false, "download and verify without installing")
	fs.StringVar(&f.system, "os", "windows", "the system whose archive is chosen: windows or darwin")
	key := fs.String("key", "", "the base64 public key for verify (default: the program's)")
	_ = fs.Bool("json", false, "print JSON (the default)")
	pos, err := parse(fs, rest)
	if err != nil {
		return err
	}
	if f.system != "windows" && f.system != "darwin" {
		return usagef("update: --os must be windows or darwin")
	}
	switch sub {
	case "check":
		if len(pos) != 0 {
			return usagef("update check: takes no arguments")
		}
		return c.updateCheck(ctx, f)
	case "download":
		if !f.dryRun {
			return usagef("update download: only --dry-run is supported; golauncher installs updates itself")
		}
		return c.updateDownload(ctx, f)
	case "sign":
		if len(pos) != 1 {
			return usagef("update sign: want <file>")
		}
		return c.updateSign(pos[0])
	case "verify":
		if len(pos) != 1 {
			return usagef("update verify: want <file>")
		}
		return c.updateVerify(pos[0], *key)
	case "keygen":
		return c.updateKeygen()
	}
	return usagef(want)
}

// updateClient returns the GitHub client: the fake (started here, closed
// by the returned function) or, with --real, GitHub.
func (f updateFlags) client() (*upd.Client, string, func()) {
	if f.real {
		c := &upd.Client{HTTP: upd.NewHTTPClient(nil), UserAgent: "glctl/" + Version + " (+" + install.URL + ")"}
		if api := os.Getenv(upd.EnvAPI); api != "" {
			c.API = api
		}
		return c, "github", func() {}
	}
	fake := fakegithub.New(fakegithub.Options{Version: f.latest, Darwin: f.bundle()})
	return &upd.Client{HTTP: fake.Client(), API: fake.API(), UserAgent: "glctl/" + Version}, "fake", fake.Close
}

func (f updateFlags) currentVersion() string {
	if f.current != "" {
		return f.current
	}
	return Version
}

// latest checks for the latest release.
func (f updateFlags) latestRelease(ctx context.Context, cl *upd.Client, source string) (updateCheckResult, upd.Release, error) {
	res := updateCheckResult{Source: source, Current: f.currentVersion()}
	res.Updatable = upd.Updatable(res.Current)
	cctx, cancel := context.WithTimeout(ctx, upd.CheckTimeout)
	defer cancel()
	lr, err := cl.Latest(cctx, "")
	if err != nil {
		return res, upd.Release{}, err
	}
	r := lr.Release
	res.Tag, res.Page = r.Tag, r.Page()
	if !r.PublishedAt.IsZero() {
		res.PublishedAt = r.PublishedAt.UTC().Format(time.RFC3339)
	}
	v, err := r.Version()
	if err != nil {
		return res, r, err
	}
	res.Latest = v
	res.Newer = upd.Newer(v, res.Current)
	if a, err := upd.SelectAssetsFor(r, f.arch, f.bundle()); err == nil {
		res.Archive, res.Size, res.Checksums, res.Signature = a.Archive.Name, a.Archive.Size, true, a.Signature != nil
	} else {
		res.Error = err.Error()
	}
	return res, r, nil
}

func (c *cli) updateCheck(ctx context.Context, f updateFlags) error {
	cl, source, done := f.client()
	defer done()
	res, _, err := f.latestRelease(ctx, cl, source)
	if err != nil {
		res.Error = err.Error()
	}
	if oerr := c.output(res, func(w io.Writer) { writeUpdateCheck(w, res) }); oerr != nil {
		return oerr
	}
	if err != nil {
		return err
	}
	return nil
}

func writeUpdateCheck(w io.Writer, r updateCheckResult) {
	fmt.Fprintf(w, "source: %s\ncurrent: %s (updatable: %v)\n", r.Source, r.Current, r.Updatable)
	if r.Latest != "" {
		state := "up to date"
		if r.Newer {
			state = "newer"
		}
		fmt.Fprintf(w, "latest: %s (%s) %s\n", r.Latest, state, r.Page)
	}
	if r.Archive != "" {
		fmt.Fprintf(w, "archive: %s (%d bytes), checksums: %v, signature: %v\n", r.Archive, r.Size, r.Checksums, r.Signature)
	}
	if r.Error != "" {
		fmt.Fprintf(w, "error: %s\n", r.Error)
	}
}

func (c *cli) updateDownload(ctx context.Context, f updateFlags) error {
	cl, source, done := f.client()
	defer done()
	check, r, err := f.latestRelease(ctx, cl, source)
	res := updateDownloadResult{updateCheckResult: check}
	if err == nil {
		var dir string
		dir, err = os.MkdirTemp("", "glctl-update-")
		if err == nil {
			defer os.RemoveAll(dir)
			var pr upd.PrepareResult
			pr, err = cl.Prepare(ctx, r, upd.PrepareOptions{Arch: f.arch, Dir: dir, PublicKey: upd.PublicKey, Bundle: f.bundle()})
			if err == nil {
				res.SHA256, res.Verified, res.Signed, res.Files = pr.SHA256, true, pr.Signed, pr.Files
			}
		}
	}
	if err != nil {
		res.Error = err.Error()
	}
	if oerr := c.output(res, func(w io.Writer) {
		writeUpdateCheck(w, res.updateCheckResult)
		if res.Verified {
			fmt.Fprintf(w, "sha256: %s (verified, signed: %v)\nfiles: %s\n", res.SHA256, res.Signed, strings.Join(res.Files, ", "))
		}
	}); oerr != nil {
		return oerr
	}
	return err
}

// updateSign writes <file>.sig, the signature of file with the key in
// GOLAUNCHER_UPDATE_SIGNING_KEY (the release workflow signs checksums.txt).
func (c *cli) updateSign(path string) error {
	key := os.Getenv(EnvSigningKey)
	if key == "" {
		return fmt.Errorf("update sign: %s is not set", EnvSigningKey)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sig, err := upd.Sign(key, data)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path+".sig", []byte(sig+"\n"), 0o644); err != nil {
		return err
	}
	pub, _ := upd.PublicKeyOf(key)
	return c.output(struct {
		Signature string `json:"signature"`
		PublicKey string `json:"publicKey"`
	}{path + ".sig", pub}, func(w io.Writer) { fmt.Fprintf(w, "wrote %s.sig (public key %s)\n", path, pub) })
}

// updateVerify checks <file>.sig with key, or the program's public key.
func (c *cli) updateVerify(path, key string) error {
	if key == "" {
		key = upd.PublicKey
	}
	if key == "" {
		return fmt.Errorf("update verify: no --key, and this glctl has no public key built in")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(path + ".sig")
	if err != nil {
		return err
	}
	if err := upd.VerifySignature(key, data, sig); err != nil {
		return err
	}
	return c.output(struct {
		Verified bool `json:"verified"`
	}{true}, func(w io.Writer) { fmt.Fprintf(w, "%s: signature verified\n", path) })
}

// updateKeygen prints a new key pair; nothing is written to disk.
func (c *cli) updateKeygen() error {
	seed, pub, err := upd.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	return c.output(struct {
		PrivateKey string `json:"privateKey"`
		PublicKey  string `json:"publicKey"`
		Note       string `json:"note"`
	}{seed, pub, "Store privateKey as the UPDATE_SIGNING_KEY secret and publicKey as the UPDATE_PUBLIC_KEY variable (docs/auto-update.md §9). Never commit the private key."},
		func(w io.Writer) {
			fmt.Fprintf(w, "private key (secret UPDATE_SIGNING_KEY): %s\npublic key (variable UPDATE_PUBLIC_KEY): %s\nNever commit the private key.\n", seed, pub)
		})
}
