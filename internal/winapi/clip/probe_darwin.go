package clip

import "github.com/eziq1/golauncher/internal/darwin/ns"

// HasFilesOrImage reports whether Read would return files, or an image (and
// no text), from the pasteboard's types alone, without reading their data:
// the launcher asks it on ⌘V to decide whether the key pastes files into a
// view or text into its search field. Content marked concealed, transient
// or auto-generated (Read returns none of it) reports false.
func HasFilesOrImage() bool {
	if ns.Load() != nil {
		return false
	}
	var ok bool
	ns.Pool(func() {
		types := ns.Strings(ns.Send(pasteboard(), "types"))
		ok = probeTypes(types)
	})
	return ok
}
