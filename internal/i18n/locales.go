package i18n

import (
	"embed"
	"io/fs"
)

//go:embed locales/*.json
var coreLocales embed.FS

// CoreLocales returns the catalogs of the core namespace.
func CoreLocales() fs.FS {
	sub, err := fs.Sub(coreLocales, "locales")
	if err != nil {
		panic(err)
	}
	return sub
}
