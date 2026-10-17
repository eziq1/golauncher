#!/bin/bash
#
# Builds golauncher and glctl for macOS into dist/, optionally as the app
# bundle golauncher.app and zipped as in a release. The macOS counterpart
# of scripts/windows/build.ps1.
#
# The version comes from "git describe --tags --always --dirty" (without the
# leading "v"), unless --version is given. It is linked into both binaries
# (-X main.version) and written into the bundle's Info.plist.
#
# --update-public-key (default: $GOLAUNCHER_UPDATE_PUBLIC_KEY) builds in the
# base64 ed25519 public key that release checksums must be signed with
# (docs/auto-update.md §9); without it, updates check only SHA-256.
#
# --arch universal builds both architectures and merges them with lipo.
#
# --debug builds without optimizations and inlining, keeps symbols and
# MyGo's developer tools, and appends "-debug" to the version; for Delve.
#
# --app also assembles dist/golauncher.app (docs/self-install.md §9):
# Contents/MacOS/golauncher and glctl, Info.plist (bundle identifier
# io.github.eziq1.golauncher, no Dock icon, macOS 13 or later, the purpose
# strings of the permission prompts, in English and Chinese),
# golauncher.icns made from assets/icon-*.png with iconutil, and a code
# signature: ad hoc by default, or with --sign IDENTITY (default:
# $GOLAUNCHER_CODESIGN_IDENTITY), e.g. a self-signed certificate of your
# own, which keeps privacy permissions across rebuilds. No Developer ID
# signing or notarization.
#
# --zip also writes dist/golauncher_<version>_darwin_<arch>.zip. With --app
# it holds golauncher.app (the release archive that auto-update installs),
# else the bare golauncher and glctl; LICENSE, README.md, README.zh-CN.md
# and CHANGELOG.md go beside them.
#
# Examples:
#   scripts/macos/build.sh
#   scripts/macos/build.sh --app
#   scripts/macos/build.sh --debug
#   scripts/macos/build.sh --arch arm64 --app --zip    # a release zip (amd64: Intel)
#   scripts/macos/build.sh --arch universal --app --zip
#   scripts/macos/build.sh --version 0.1.0 --arch arm64 --zip

set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
arch=""
version=""
out_dir=""
zip=0
app=0
debug=0
update_public_key="${GOLAUNCHER_UPDATE_PUBLIC_KEY:-}"
sign_identity="${GOLAUNCHER_CODESIGN_IDENTITY:--}"
bundle_id="io.github.eziq1.golauncher"

usage() {
    # The comment at the top of this file.
    awk 'NR > 2 && !/^#/ { exit } NR > 2' "$0" | sed 's/^# \{0,1\}//'
    echo
    echo "Options: --arch amd64|arm64|universal  --version V  --out-dir DIR"
    echo "         --app  --sign IDENTITY  --zip  --debug  --update-public-key KEY  -h|--help"
}

die() { echo "build-macos: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
    case "$1" in
        --arch) [ $# -ge 2 ] || die "$1 needs a value"; arch="$2"; shift 2 ;;
        --version) [ $# -ge 2 ] || die "$1 needs a value"; version="$2"; shift 2 ;;
        --out-dir) [ $# -ge 2 ] || die "$1 needs a value"; out_dir="$2"; shift 2 ;;
        --update-public-key) [ $# -ge 2 ] || die "$1 needs a value"; update_public_key="$2"; shift 2 ;;
        --sign) [ $# -ge 2 ] || die "$1 needs a value"; sign_identity="$2"; shift 2 ;;
        --app) app=1; shift ;;
        --zip) zip=1; shift ;;
        --debug) debug=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) usage >&2; die "unknown option $1" ;;
    esac
done

[ -n "$out_dir" ] || out_dir="$root/dist"
[ -n "$arch" ] || arch="$(go env GOHOSTARCH)"
case "$arch" in
    amd64|arm64|universal) ;;
    *) die "unsupported architecture $arch (want amd64, arm64 or universal)" ;;
esac
[ -n "$sign_identity" ] || sign_identity="-"

if [ -z "$version" ]; then
    described="$(git -C "$root" describe --tags --always --dirty 2>/dev/null || true)"
    [ -n "$described" ] || described="dev"
    if [[ "$described" =~ ^v?[0-9]+\.[0-9]+ ]]; then
        version="${described#v}"
    else
        # No tag yet: a commit hash, perhaps with -dirty.
        version="0.0.0-$described"
    fi
fi
[ "$debug" -eq 0 ] || version="$version-debug"

ldflags="-X main.version=$version"
if [ -n "$update_public_key" ]; then
    [[ "$update_public_key" =~ ^[A-Za-z0-9+/]{43}=$ ]] || die "update public key is not a base64 ed25519 public key"
    ldflags="$ldflags -X github.com/eziq1/golauncher/internal/update.PublicKey=$update_public_key"
    echo "update signatures: required (public key built in)"
fi

build_flags=()
launcher_flags=()
if [ "$debug" -eq 1 ]; then
    build_flags+=(-gcflags "all=-N -l")
else
    ldflags="-s -w $ldflags"
    build_flags+=(-trimpath)
    # mygo_noinspector leaves MyGo's developer tools out of release binaries.
    launcher_flags+=(-tags mygo_noinspector)
fi

echo "golauncher $version darwin/$arch -> $out_dir"
mkdir -p "$out_dir"
out_dir="$(cd "$out_dir" && pwd)"
cd "$root"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# build_one <goarch> <output dir>
build_one() {
    mkdir -p "$2"
    # purego needs no cgo, so both architectures cross-compile from either Mac.
    GOOS=darwin GOARCH="$1" CGO_ENABLED=0 \
        go build "${build_flags[@]}" ${launcher_flags[@]+"${launcher_flags[@]}"} -ldflags "$ldflags" -o "$2/golauncher" ./cmd/golauncher
    GOOS=darwin GOARCH="$1" CGO_ENABLED=0 \
        go build "${build_flags[@]}" -ldflags "$ldflags" -o "$2/glctl" ./cmd/glctl
}

if [ "$arch" = "universal" ]; then
    build_one amd64 "$tmp/amd64"
    build_one arm64 "$tmp/arm64"
    for bin in golauncher glctl; do
        lipo -create -output "$out_dir/$bin" "$tmp/amd64/$bin" "$tmp/arm64/$bin"
    done
else
    build_one "$arch" "$out_dir"
fi

# make_icns <output .icns>: the app icon from assets/icon-<size>.png. The
# 1024-pixel image (512 points at 2x) is scaled up from 512 with sips.
make_icns() {
    local set="$tmp/golauncher.iconset" a="$root/assets"
    mkdir -p "$set"
    cp "$a/icon-16.png" "$set/icon_16x16.png"
    cp "$a/icon-32.png" "$set/icon_16x16@2x.png"
    cp "$a/icon-32.png" "$set/icon_32x32.png"
    cp "$a/icon-64.png" "$set/icon_32x32@2x.png"
    cp "$a/icon-128.png" "$set/icon_128x128.png"
    cp "$a/icon-256.png" "$set/icon_128x128@2x.png"
    cp "$a/icon-256.png" "$set/icon_256x256.png"
    cp "$a/icon-512.png" "$set/icon_256x256@2x.png"
    cp "$a/icon-512.png" "$set/icon_512x512.png"
    sips -z 1024 1024 "$a/icon-512.png" --out "$set/icon_512x512@2x.png" >/dev/null
    iconutil -c icns -o "$1" "$set"
}

if [ "$app" -eq 1 ]; then
    bundle="$out_dir/golauncher.app"
    rm -rf "$bundle"
    mkdir -p "$bundle/Contents/MacOS" "$bundle/Contents/Resources"
    cp "$out_dir/golauncher" "$out_dir/glctl" "$bundle/Contents/MacOS/"
    # Info.plist, PkgInfo and the localized purpose strings (internal/install).
    GOOS= GOARCH= go run ./scripts/macos/macbundle -version "$version" -app "$bundle"
    make_icns "$bundle/Contents/Resources/golauncher.icns"
    # Nested code first, then the bundle, which seals everything in it. The
    # identifiers stay the same from build to build; an ad hoc signature
    # still changes with every build (docs/self-install.md §9.4).
    codesign --force --sign "$sign_identity" --timestamp=none \
        --identifier "$bundle_id.glctl" "$bundle/Contents/MacOS/glctl"
    codesign --force --sign "$sign_identity" --timestamp=none \
        --identifier "$bundle_id" "$bundle"
    codesign --verify --strict --deep "$bundle"
    plutil -lint -s "$bundle/Contents/Info.plist"
    echo "wrote $bundle (signed: $([ "$sign_identity" = "-" ] && echo "ad hoc" || echo "$sign_identity"))"
fi

if [ "$zip" -eq 1 ]; then
    name="golauncher_${version}_darwin_$arch"
    stage="$tmp/$name"
    rm -rf "$stage"
    mkdir -p "$stage"
    if [ "$app" -eq 1 ]; then
        # ditto copies the bundle as it is, signature included.
        ditto "$out_dir/golauncher.app" "$stage/golauncher.app"
    else
        cp "$out_dir/golauncher" "$out_dir/glctl" "$stage/"
    fi
    for f in LICENSE README.md README.zh-CN.md CHANGELOG.md; do
        [ ! -f "$root/$f" ] || cp "$root/$f" "$stage/"
    done
    zip_path="$out_dir/$name.zip"
    rm -f "$zip_path"
    # ditto keeps the executable bits and is what Finder's Compress uses;
    # no resource forks, extended attributes or ACLs (nothing to quarantine
    # or sequester: the signature is in the files).
    (cd "$stage" && ditto -c -k --norsrc --noextattr --noacl . "$zip_path")
    echo "wrote $zip_path"
fi
