//go:build !windows

package scripts

import (
	"os/exec"
	"strings"
)

func prepare(cmd *exec.Cmd, it Interpreter, script string, args []string) {}

type procGroup struct{}

func newProcGroup(pid int) *procGroup { return &procGroup{} }
func (g *procGroup) kill()            {}
func (g *procGroup) close()           {}

func decodeLegacy(b []byte) string { return strings.ToValidUTF8(string(b), "\ufffd") }
