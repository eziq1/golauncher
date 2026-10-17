package main

import (
	"fmt"
	"strings"

	"github.com/eziq1/golauncher/internal/install"
)

// The cask.
const (
	// CaskFile is the cask's file in the tap's Casks folder.
	CaskFile = install.CaskToken + ".rb"
	homepage = "https://github.com/" + Repo
	// Desc follows Homebrew's rules: no article first, no platform, no
	// cask name, at most 80 characters.
	Desc = "Raycast-style keyboard launcher with plugins, written in Go"
)

// zap are the files golauncher leaves in the user's Library
// (brew uninstall --zap): configuration and data, caches and logs, the
// login item's fallback launch agent, and what macOS keeps by bundle ID.
var zap = []string{
	"~/Library/Application Support/" + install.AppName,
	"~/Library/Caches/" + install.AppName,
	"~/Library/LaunchAgents/" + install.LaunchAgentLabel + ".plist",
	"~/Library/Preferences/" + install.BundleID + ".plist",
	"~/Library/Saved Application State/" + install.BundleID + ".savedState",
}

// caveats are shown after brew install: the app is ad hoc signed, not
// notarized, so Gatekeeper blocks its first start.
var caveats = []string{
	install.AppName + " is not notarized by Apple (it is ad hoc signed), so macOS",
	"blocks it the first time it opens and may again after an upgrade. Open it",
	"once, then choose System Settings > Privacy & Security > Open Anyway.",
	"",
	"It does not update itself: Homebrew does. Upgrade it with",
	"  " + install.HomebrewUpgrade,
	"and then quit and reopen it.",
}

// Cask returns the cask of rel (Ruby, as brew style formats it).
func Cask(rel Release) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	p("# Created with scripts/release/brewcask (%s).", homepage)
	p("# The tap is %s, github.com/eziq1/homebrew-tap.", strings.TrimSuffix(install.CaskFullName, "/"+install.CaskToken))
	p("cask %s do", ruby(install.CaskToken))
	p("  version %s", ruby(rel.Version))
	p("")
	for _, a := range rel.Arches {
		p("  on_%s do", a.Brew)
		p("    sha256 %s", ruby(a.SHA256))
		p("")
		// Interpolated, so that a version bump only changes version.
		p(`    url "%s/releases/download/v#{version}/%s"`, homepage, ZipName("#{version}", a.GOARCH))
		p("  end")
	}
	p("")
	p("  name %s", ruby(install.AppName))
	p("  desc %s", ruby(Desc))
	p("  homepage %s", ruby(homepage))
	p("")
	p("  livecheck do")
	p("    url :url")
	p("    strategy :github_latest")
	p("  end")
	p("")
	p(`  depends_on macos: ">= :%s"`, rel.MacOS)
	p("")
	p("  app %s", ruby(install.BundleName))
	p(`  binary "#{appdir}/%s/Contents/MacOS/%s"`, install.BundleName, install.BundleCtlName)
	p("")
	p("  zap trash: [")
	for _, z := range zap {
		p("    %s,", ruby(z))
	}
	p("  ]")
	p("")
	p("  caveats <<~EOS")
	for _, c := range caveats {
		if c == "" {
			p("")
		} else {
			p("    %s", c)
		}
	}
	p("  EOS")
	p("end")
	return b.String()
}

// ruby returns s as a Ruby string literal: double-quoted, as brew style
// wants, with no interpolation.
func ruby(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `#`, `\#`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}
