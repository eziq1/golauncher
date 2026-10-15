package scoop

import (
	"encoding/base64"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// CommandLine is how a Scoop command is shown: "scoop install main/git".
func CommandLine(args []string) string {
	return strings.Join(append([]string{"scoop"}, args...), " ")
}

// psQuote quotes s as a single-quoted PowerShell string. PowerShell also
// accepts the typographic single quotes as delimiters, so they are doubled
// as well.
func psQuote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\'', '‘', '’', '‚', '‛':
			b.WriteRune(r)
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}

// Script is the PowerShell script that runs the Scoop commands steps one
// after the other with the Scoop at root: root's shims come first on PATH
// (so scoop is found even if the launcher started before Scoop was
// installed) and $SCOOP is root. Every argument is a quoted literal, output
// is UTF-8, each command is echoed as "> scoop ...", and the script stops
// at the first command that fails, with its exit code.
func Script(root string, steps [][]string) string {
	var b strings.Builder
	b.WriteString("$ProgressPreference = 'SilentlyContinue'\n")
	b.WriteString("try { [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false) } catch {}\n")
	if root != "" {
		b.WriteString("$env:SCOOP = " + psQuote(root) + "\n")
		b.WriteString("$env:PATH = " + psQuote(filepath.Join(root, "shims")+";") + " + $env:PATH\n")
	}
	for _, args := range steps {
		b.WriteString("Write-Output " + psQuote("> "+CommandLine(args)) + "\n")
		b.WriteString("$global:LASTEXITCODE = 0\n")
		b.WriteString("try { & scoop")
		for _, a := range args {
			b.WriteByte(' ')
			b.WriteString(psQuote(a))
		}
		b.WriteString(" } catch { Write-Output $_; exit 1 }\n")
		b.WriteString("if ($global:LASTEXITCODE -ne 0) { exit $global:LASTEXITCODE }\n")
	}
	b.WriteString("exit 0\n")
	return b.String()
}

// encodeCommand encodes a script for powershell -EncodedCommand (base64 of
// UTF-16LE), which needs no command-line quoting at all.
func encodeCommand(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// shell is the PowerShell that runs Scoop: PowerShell 7 (pwsh) when
// installed, else Windows PowerShell (Options.Shell overrides it).
func (p *Plugin) shell() string {
	p.shellOnce.Do(func() {
		p.shellPath = p.opts.Shell
		if p.shellPath != "" {
			return
		}
		p.shellPath = "powershell.exe"
		if path, err := exec.LookPath("pwsh.exe"); err == nil {
			p.shellPath = path
		} else if path, err := exec.LookPath("powershell.exe"); err == nil {
			p.shellPath = path
		}
	})
	return p.shellPath
}

// command is the process the host runs for the Scoop commands steps.
func (p *Plugin) command(steps [][]string) plugin.ProcessSpec {
	return plugin.ProcessSpec{
		Argv: []string{p.shell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
			"-EncodedCommand", encodeCommand(Script(p.root, steps))},
		CommandLine: commandLines(steps),
	}
}
