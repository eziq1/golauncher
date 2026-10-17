package clip

import "github.com/eziq1/golauncher/internal/darwin/ns"

// HasFilesOrImage reports whether ReadPaste would return files or an image:
// the launcher asks it on ⌘V to decide whether the key pastes files into a
// view or text into its search field. Files, or an image without text, are
// told from the pasteboard's types alone. With both an image and text, the
// text is read and decides: the image is taken when the text only describes
// it (DescribesImage, R96). Content marked concealed, transient or
// auto-generated (Read returns none of it) reports false.
func HasFilesOrImage() bool {
	if ns.Load() != nil {
		return false
	}
	var ok bool
	ns.Pool(func() {
		pb := pasteboard()
		var askText bool
		ok, askText = probeTypes(ns.Strings(ns.Send(pb, "types")))
		if askText {
			ok = DescribesImage(ns.GoString(ns.Send(pb, "stringForType:", ns.String(typeText))))
		}
	})
	return ok
}
