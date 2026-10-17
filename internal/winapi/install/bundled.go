package install

import "runtime"

// Bundled is set where golauncher is an app bundle (macOS): the
// installer and the updates handle golauncher.app as a whole
// (install.Installer.Bundle).
const Bundled = runtime.GOOS == "darwin"
