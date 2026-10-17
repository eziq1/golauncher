// Command macbundle writes the property lists of golauncher.app for
// scripts/macos/build.sh --app: Contents/Info.plist, Contents/PkgInfo and
// the localized purpose strings, Contents/Resources/<lang>.lproj/
// InfoPlist.strings. They come from internal/install, where they are
// tested. The programs, the icon and the signature are the script's.
//
//	go run ./scripts/macos/macbundle -version 0.7.0 -app dist/golauncher.app
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/eziq1/golauncher/internal/install"
)

func main() {
	version := flag.String("version", "", "golauncher's version")
	app := flag.String("app", "", "the app bundle to write into")
	flag.Parse()
	if *version == "" || *app == "" || flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: macbundle -version V -app path/golauncher.app")
		os.Exit(2)
	}
	if err := write(*app, *version); err != nil {
		fmt.Fprintln(os.Stderr, "macbundle:", err)
		os.Exit(1)
	}
}

func write(app, version string) error {
	contents := filepath.Join(app, "Contents")
	files := map[string][]byte{
		filepath.Join(contents, "Info.plist"): install.InfoPlist(install.BundleInfo{Version: version}),
		filepath.Join(contents, "PkgInfo"):    []byte("APPL????"),
	}
	for _, lang := range install.Localizations {
		data, err := install.InfoPlistStrings(lang)
		if err != nil {
			return err
		}
		files[filepath.Join(contents, "Resources", lang+".lproj", "InfoPlist.strings")] = data
	}
	for path, data := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
