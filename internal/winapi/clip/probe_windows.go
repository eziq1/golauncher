package clip

// HasFilesOrImage reports whether Read would return files, or an image (and
// no text), without opening the clipboard or reading its content: the
// launcher asks it on Ctrl+V to decide whether the key pastes files into a
// view or text into its search field. Content that asks not to be recorded
// in clipboard history (Read returns none of it) reports false.
func HasFilesOrImage() bool {
	f := formats()
	switch {
	case available(f.exclude) || available(f.viewerIgnore):
		return false
	case available(cfHDROP):
		return true
	case available(cfUnicodeText) || available(cfText):
		return false
	}
	return available(f.png) || available(cfDIBV5) || available(cfDIB) || available(cfBitmap)
}
