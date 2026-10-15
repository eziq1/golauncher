//go:build windows

package terminal

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// lxssKey is where WSL registers the distributions of the user.
const lxssKey = `Software\Microsoft\Windows\CurrentVersion\Lxss`

// readRegistry reads the WSL distributions of the user from the registry.
// A missing key means no distribution (WSL is not installed or unused).
func readRegistry() (Registry, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, lxssKey, registry.READ)
	if errors.Is(err, registry.ErrNotExist) {
		return Registry{}, nil
	}
	if err != nil {
		return Registry{}, err
	}
	defer k.Close()
	var r Registry
	r.Default, _, _ = k.GetStringValue("DefaultDistribution")
	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return r, err
	}
	for _, guid := range names {
		sk, err := registry.OpenKey(k, guid, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		d := Distro{GUID: guid}
		d.Name, _, _ = sk.GetStringValue("DistributionName")
		d.BasePath, _, _ = sk.GetStringValue("BasePath")
		d.BasePath = strings.TrimPrefix(d.BasePath, `\\?\`)
		v, _, err := sk.GetIntegerValue("Version")
		if err != nil {
			v = 1 // WSL 1 distributions installed before WSL 2 have no Version
		}
		d.Version = int(v)
		state, _, err := sk.GetIntegerValue("State")
		sk.Close()
		// State 1 is "installed"; others are being installed or removed.
		if d.Name == "" || (err == nil && state != 1) {
			continue
		}
		r.Distros = append(r.Distros, d)
	}
	return r, nil
}

// runHidden runs a console program without a window and returns its
// output (stdout and stderr). A non-zero exit code is an error that comes
// with the output.
func runHidden(ctx context.Context, argv ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	return cmd.CombinedOutput()
}
