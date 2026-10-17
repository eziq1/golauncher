package ai

import (
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// The programs of the backends (R84): the reports, the "program.<id>"
// preference, the notices and the hint at the wrong installation. The
// paths are made up.

const (
	newCodex = `C:\Users\someone\AppData\Local\OpenAI\Codex\bin\codex.exe`
	oldCodex = `C:\Users\someone\AppData\Roaming\npm\node_modules\@openai\codex\vendor\x86_64-pc-windows-msvc\codex\codex.exe`
)

func fakeCodex(fake *backend.Fake) *backend.FakeInstalls {
	return &backend.FakeInstalls{Fake: fake, BackendName: "Codex (ChatGPT)", Found: backend.Discovery{
		MinVersion: "0.130.0", Auto: 0, AutoReason: backend.ReasonOnly, Status: backend.StatusAvailable,
		Installs: []backend.Install{
			{Path: newCodex, Source: backend.SourceApp, Version: "0.162.0"},
			{Path: oldCodex, Source: backend.SourceNpmVendor, Version: "0.46.0", TooOld: true},
		}}}
}

// TestBackendReports: only backends that can list their installations are
// reported; the program preference reaches the backend, which is checked
// again after a change.
func TestBackendReports(t *testing.T) {
	claude := &backend.Fake{}
	codexB := fakeCodex(&backend.Fake{})
	p := NewWithOptions(Options{Sync: true, HasProtocol: func(string) bool { return false }, Backends: []BackendOption{
		{ID: DefaultBackendID, Backend: claude}, {ID: "codex", Backend: codexB},
	}})
	h := newHost(t, p)

	reps := p.BackendReports(t.Context(), false)
	if len(reps) != 1 || reps[0].ID != "codex" || reps[0].Name != "Codex (ChatGPT)" || reps[0].InUse != 0 || len(reps[0].Installs) != 2 {
		t.Fatalf("reports %+v", reps)
	}
	if _, refreshed := codexB.Searches(); refreshed != 0 {
		t.Fatal("refreshed without being asked")
	}
	p.BackendReports(t.Context(), true)
	if _, refreshed := codexB.Searches(); refreshed != 1 {
		t.Fatal("not refreshed")
	}

	// A chat checks the backend once; the program is chosen in the
	// preferences: it is used, and the backend is checked again.
	setPrefs(h, prefBackend, `"codex"`)
	openChat(t, p, "v1", chatPayload{Text: "one"})
	if _, ok := p.checks["codex"]; !ok {
		t.Fatal("not checked")
	}
	setPrefs(h, prefBackend, `"codex"`, backend.ProgramPref("codex"), `"`+strings.ReplaceAll(oldCodex, `\`, `\\`)+`"`)
	p.configChanged()
	if _, ok := p.checks["codex"]; ok {
		t.Fatal("the check was kept after the program changed")
	}
	reps = p.BackendReports(t.Context(), false)
	if r := reps[0]; r.Configured != oldCodex || r.InUse != 1 || r.Status != backend.StatusTooOld || codexB.Program() != oldCodex {
		t.Fatalf("configured: %+v", r)
	}
	// A program that is gone: the automatic choice, with a warning.
	setPrefs(h, prefBackend, `"codex"`, backend.ProgramPref("codex"), `"C:\\gone\\codex.exe"`)
	if r := p.BackendReports(t.Context(), false)[0]; !r.ConfiguredMissing || r.InUse != 0 || r.Status != backend.StatusAvailable {
		t.Fatalf("missing: %+v", r)
	}

	if in := p.ProbeProgram(t.Context(), "codex", newCodex); !in.Usable() || in.Version != "0.162.0" {
		t.Fatalf("probe %+v", in)
	}
	if in := p.ProbeProgram(t.Context(), "codex", `C:\nowhere\codex.exe`); in.VersionError != backend.ProbeNotFound {
		t.Fatalf("probe %+v", in)
	}
	if in := p.ProbeProgram(t.Context(), DefaultBackendID, newCodex); in.VersionError == "" {
		t.Fatalf("probe of a backend without installations: %+v", in)
	}
}

// TestWrongInstallationHint: an error that another installation may avoid
// names the program that ran and points to the settings; others do not.
func TestWrongInstallationHint(t *testing.T) {
	fake := &backend.Fake{SendErr: &backend.Error{Kind: backend.ErrVersion, Version: "0.46.0"}}
	codexB := fakeCodex(fake)
	codexB.SetProgram(oldCodex)
	p := NewWithOptions(Options{Sync: true, HasProtocol: func(string) bool { return false }, Backends: []BackendOption{{ID: "codex", Backend: codexB}}})
	h := newHost(t, p)
	setPrefs(h, backend.ProgramPref("codex"), `"`+strings.ReplaceAll(oldCodex, `\`, `\\`)+`"`)
	p.configChanged()
	v := openChat(t, p, "v1", chatPayload{Text: "hi"})
	md := v.Detail.Markdown
	if !strings.Contains(md, "golauncher ran `"+oldCodex+"`") || !strings.Contains(md, "Settings › Plugins › AI") {
		t.Fatalf("no hint:\n%s", md)
	}
	msgs := p.chats[p.views["v1"]].msgs
	if msgs[len(msgs)-1].Program != oldCodex {
		t.Fatalf("not recorded: %+v", msgs[len(msgs)-1])
	}

	// "No longer supported" from the service is one too; a quota is not.
	for _, tt := range []struct {
		kind, msg string
		want      bool
	}{
		{string(backend.ErrFailed), "This client is no longer supported for Gemini Code Assist for individuals.", true},
		{string(backend.ErrFailed), "requires a newer version of Codex", true},
		{string(backend.ErrFailed), "connection reset", false},
		{string(backend.ErrQuota), "no longer supported", false},
		{string(backend.ErrVersion), "", true},
	} {
		if got := wrongInstallation(tt.kind, tt.msg); got != tt.want {
			t.Errorf("wrongInstallation(%s, %q) = %v", tt.kind, tt.msg, got)
		}
	}
}

// TestProgramMissingNotice: the backend's notices are all shown, the
// missing program's among them.
func TestProgramMissingNotice(t *testing.T) {
	fake := &backend.Fake{Steps: func(string) []backend.Event {
		return []backend.Event{{Kind: backend.EventNotice, Text: backend.NoticeProgramMissing}, {Kind: backend.EventNotice, Text: backend.NoticeNewSession},
			{Kind: backend.EventNotice, Text: backend.NoticeNewSession}}
	}}
	p, _ := newTestPlugin(t, fake, true)
	v := openChat(t, p, "v1", chatPayload{Text: "hi"})
	md := v.Detail.Markdown
	if !strings.Contains(md, "no longer exists, so golauncher uses the one it finds automatically") ||
		strings.Count(md, "could not continue the earlier conversation") != 1 {
		t.Fatalf("notices:\n%s", md)
	}
}

// newHost initializes p on a fake host, with AI on.
func newHost(t *testing.T, p *Plugin) *hostfake.Host {
	t.Helper()
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setPrefs(h)
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return h
}
