package terminal

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Distro is a registered WSL distribution, read from the registry key
// HKCU\Software\Microsoft\Windows\CurrentVersion\Lxss\{guid}.
type Distro struct {
	GUID     string `json:"guid"`
	Name     string `json:"name"`     // DistributionName
	BasePath string `json:"basePath"` // where its virtual disk lives
	Version  int    `json:"version"`  // 1 or 2
}

// Registry is what the Lxss registry key says.
type Registry struct {
	// Default is the GUID of DefaultDistribution.
	Default string   `json:"default"`
	Distros []Distro `json:"distros"`
}

// DefaultName returns the name of the default distribution, "" if unknown.
func (r Registry) DefaultName() string {
	for _, d := range r.Distros {
		if normGUID(d.GUID) == normGUID(r.Default) && r.Default != "" {
			return d.Name
		}
	}
	return ""
}

// State is the state of a distribution in `wsl --list --verbose`.
type State struct {
	// Text is the state as wsl.exe printed it (localized by Windows).
	Text    string
	Version int
	Default bool
}

// stateKind classifies State.Text.
type stateKind int

const (
	stateUnknown stateKind = iota
	stateRunning
	stateStopped
)

// runningWords and stoppedWords are the states wsl.exe prints, in the
// languages seen so far (compared case-insensitively). Other texts are shown
// as printed.
var (
	runningWords = []string{"running", "正在运行", "运行中", "執行中", "正在執行", "wird ausgeführt", "en cours d'exécution", "実行中", "실행 중"}
	stoppedWords = []string{"stopped", "已停止", "已停止运行", "已停止執行", "beendet", "arrêté", "停止", "중지됨"}
)

func (s State) kind() stateKind {
	t := strings.ToLower(strings.TrimSpace(s.Text))
	for _, w := range runningWords {
		if t == w {
			return stateRunning
		}
	}
	for _, w := range stoppedWords {
		if t == w {
			return stateStopped
		}
	}
	return stateUnknown
}

// DecodeOutput decodes the output of wsl.exe: UTF-16LE (with or without a
// byte order mark) unless WSL_UTF8=1 made it UTF-8.
func DecodeOutput(b []byte) string {
	if bytes.HasPrefix(b, []byte{0xff, 0xfe}) {
		return decodeUTF16LE(b[2:])
	}
	if bytes.HasPrefix(b, []byte("\xef\xbb\xbf")) {
		return string(b[3:])
	}
	// ASCII text in UTF-16LE has a zero in every odd byte.
	zeros := 0
	for i := 1; i < len(b); i += 2 {
		if b[i] == 0 {
			zeros++
		}
	}
	if len(b) >= 2 && zeros*4 >= len(b) {
		return decodeUTF16LE(b)
	}
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), "�")
}

func decodeUTF16LE(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

// ParseListVerbose reads the output of `wsl --list --verbose` (already
// decoded) into the states by distribution name:
//
//	  NAME            STATE           VERSION
//	* Ubuntu-22.04    Running         2
//	  docker-desktop  Stopped         2
//
// The header and the states are localized (名称 状态 版本, 正在运行), so the
// header is recognized as the line whose last column is not a number. Names
// of distributions have no spaces; states may.
func ParseListVerbose(out string) map[string]State {
	states := map[string]State{}
	for line := range strings.Lines(out) {
		line = strings.TrimSpace(strings.ReplaceAll(line, "\x00", ""))
		def := false
		if rest, ok := strings.CutPrefix(line, "*"); ok {
			def, line = true, strings.TrimSpace(rest)
		}
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		v, err := strconv.Atoi(f[len(f)-1])
		if err != nil {
			continue // the header, or a message
		}
		states[f[0]] = State{Text: strings.Join(f[1:len(f)-1], " "), Version: v, Default: def}
	}
	return states
}
