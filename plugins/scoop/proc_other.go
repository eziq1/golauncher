//go:build !windows

package scoop

import (
	"os/exec"
	"strings"
)

func prepare(cmd *exec.Cmd) {}

type procGroup struct{}

func newProcGroup(pid int) *procGroup { return &procGroup{} }
func (g *procGroup) kill()            {}
func (g *procGroup) close()           {}

func decodeLegacy(b []byte) string { return strings.ToValidUTF8(string(b), "�") }
