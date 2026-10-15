//go:build !windows

package proc

import (
	"os/exec"
	"strings"
)

func command(argv []string) *exec.Cmd { return exec.Command(argv[0], argv[1:]...) }

type group struct{}

func newGroup(int) *group { return &group{} }
func (g *group) kill()    {}
func (g *group) close()   {}

// DecodeLegacy replaces invalid UTF-8.
func DecodeLegacy(b []byte) string { return strings.ToValidUTF8(string(b), "�") }
