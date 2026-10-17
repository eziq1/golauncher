# scripts

Build, release and code-generation scripts, grouped by platform. Run them
from the repository root; output goes to `dist/` (ignored by git).

| Folder | Script | What it does |
|--------|--------|--------------|
| `windows/` | `build.ps1` | Builds `golauncher.exe` and `glctl.exe` (`-Arch amd64\|arm64`, `-Version`, `-Zip` for the release zip `golauncher_<version>_windows_<arch>.zip`) |
| | `winres.ps1` | Regenerates the icon, manifest and version resources embedded in the exes (`cmd/*/rsrc_windows_*.syso`, committed); `build.ps1` runs it |
| `macos/` | `build.sh` | Builds `golauncher` and `glctl` (`--arch amd64\|arm64\|universal`, `--version`, `--debug`), `--app` for `golauncher.app` (ad hoc signed, or `--sign IDENTITY`), `--zip` for the release zip `golauncher_<version>_darwin_<arch>.zip`. `--help` lists everything |
| | `macbundle/` | Writes the bundle's `Info.plist`, `PkgInfo` and `InfoPlist.strings` (from `internal/install`); `build.sh --app` runs it |
| | `genmenubaricon/` | Regenerates `assets/icon-menubar.png`, the menu bar template icon |
| `release/` | `changelog.ps1` | Prints a version's section of `CHANGELOG.md`: the GitHub release notes |
| `data/` | `gencities/` | Regenerates the world clock's city data (`plugins/clock/geonames.tsv.gz`) from GeoNames ([world-clock.md](../docs/world-clock.md)) |

```sh
pwsh scripts/windows/build.ps1 -Arch arm64 -Zip       # Windows
scripts/macos/build.sh --arch arm64 --app --zip       # macOS, Apple silicon
scripts/macos/build.sh --arch amd64 --app --zip       # macOS, Intel
```

Releases are built by `.github/workflows/release.yml` with the same scripts:
Windows amd64 and arm64, macOS arm64 and amd64 ([docs/release.md](../docs/release.md)).
