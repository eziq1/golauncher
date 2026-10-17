// Package install implements internal/install.System on Windows: the
// uninstall entry in the registry, .lnk shortcuts through IShellLinkW,
// version resources, waiting for running instances, starting programs and
// deleting the program files after the uninstaller exits.
//
// It is Windows-only; elsewhere every operation fails with
// errors.ErrUnsupported and the shell skips self-installation.
package install
