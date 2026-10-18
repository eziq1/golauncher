# Created with scripts/release/brewcask (https://github.com/eziq1/golauncher).
# The tap is eziq1/tap, github.com/eziq1/homebrew-tap.
cask "golauncher" do
  version "0.8.0"

  on_arm do
    sha256 "d1414c62f13c7085806d589f8675c8f8639547ee7cfd47cb96908fe5cc8be0f0"

    url "https://github.com/eziq1/golauncher/releases/download/v#{version}/golauncher_#{version}_darwin_arm64.zip"
  end
  on_intel do
    sha256 "d8738ede7bdfdc6f206ed2c461a04b51adcd87da417983343c3b624de798da7a"

    url "https://github.com/eziq1/golauncher/releases/download/v#{version}/golauncher_#{version}_darwin_amd64.zip"
  end

  name "golauncher"
  desc "Raycast-style keyboard launcher with plugins, written in Go"
  homepage "https://github.com/eziq1/golauncher"

  livecheck do
    url :url
    strategy :github_latest
  end

  depends_on macos: :ventura

  app "golauncher.app"
  binary "#{appdir}/golauncher.app/Contents/MacOS/glctl"

  zap trash: [
    "~/Library/Application Support/golauncher",
    "~/Library/Caches/golauncher",
    "~/Library/LaunchAgents/io.github.eziq1.golauncher.login.plist",
    "~/Library/Preferences/io.github.eziq1.golauncher.plist",
    "~/Library/Saved Application State/io.github.eziq1.golauncher.savedState",
  ]

  caveats <<~EOS
    golauncher is not notarized by Apple (it is ad hoc signed), so macOS
    blocks it the first time it opens and may again after an upgrade. Open it
    once, then choose System Settings > Privacy & Security > Open Anyway.

    It does not update itself: Homebrew does. Upgrade it with
      brew upgrade --cask golauncher
    and then quit and reopen it.
  EOS
end
