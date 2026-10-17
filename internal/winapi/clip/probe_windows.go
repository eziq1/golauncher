package clip

// HasFilesOrImage reports whether ReadPaste would return files or an image:
// the launcher asks it on Ctrl+V to decide whether the key pastes files
// into a view or text into its search field. Files, or an image without
// text, are told from the formats alone (IsClipboardFormatAvailable),
// without opening the clipboard. With both an image and text, the text is
// read (only CF_UNICODETEXT) and decides: the image is taken when the text
// only describes it (DescribesImage), as browsers' and chat apps' "Copy
// Image" put its address or "[图片]" next to it (R96). Content that asks not
// to be recorded in clipboard history (Read returns none of it) reports
// false.
func HasFilesOrImage() bool {
	f := formats()
	switch {
	case available(f.viewerIgnore):
		return false
	case available(f.exclude):
		// Ours only when CopySelection put the user's copy back.
		if !ownRestore(sequenceNumber()) {
			return false
		}
	}
	if available(cfHDROP) {
		return true
	}
	if !hasImageFormat() {
		return false
	}
	if !available(cfUnicodeText) && !available(cfText) {
		return true
	}
	var (
		text    []byte
		exclude bool
	)
	err := withClipboard(func() error {
		if exclude = pasteExcluded(); !exclude && available(cfUnicodeText) {
			text, _ = getData(cfUnicodeText)
		}
		return nil
	})
	return err == nil && !exclude && DescribesImage(utf16String(text))
}

// hasImageFormat reports whether the clipboard offers an image Read can
// take (CF_BITMAP is converted to a DIB by Windows).
func hasImageFormat() bool {
	return available(formats().png) || available(cfDIBV5) || available(cfDIB) || available(cfBitmap)
}
