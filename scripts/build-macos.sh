#!/bin/bash
#
# Builds golauncher and glctl for macOS into dist/, optionally zipped as in a
# release. The macOS counterpart of scripts/build.ps1.
#
# The version comes from "git describe --tags --always --dirty" (without the
# leading "v"), unless --version is given. It is linked into both binaries
# (-X main.version).
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
# --zip also writes dist/golauncher_<version>_darwin_<arch>.zip with
# golauncher, glctl, LICENSE, README.md, README.zh-CN.md (if present) and
# CHANGELOG.md, the layout of the Windows release archives. No .app bundle,
# signing or notarization yet (docs/macos-port.md).
#
# Examples:
#   scripts/build-macos.sh
#   scripts/build-macos.sh --debug
#   scripts/build-macos.sh --arch universal --zip
#   scripts/build-macos.sh --version 0.1.0 --arch arm64 --zip

set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
arch=""
version=""
out_dir=""
zip=0
debug=0
update_public_key="${GOLAUNCHER_UPDATE_PUBLIC_KEY:-}"

usage() {
    sed -n '3,28p' "$0" | sed 's/^# \{0,1\}//'
    echo
    echo "Options: --arch amd64|arm64|universal  --version V  --out-dir DIR"
    echo "         --zip  --debug  --update-public-key KEY  -h|--help"
}

die() { echo "build-macos: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
    case "$1" in
        --arch) [ $# -ge 2 ] || die "$1 needs a value"; arch="$2"; shift 2 ;;
        --version) [ $# -ge 2 ] || die "$1 needs a value"; version="$2"; shift 2 ;;
        --out-dir) [ $# -ge 2 ] || die "$1 needs a value"; out_dir="$2"; shift 2 ;;
        --update-public-key) [ $# -ge 2 ] || die "$1 needs a value"; update_public_key="$2"; shift 2 ;;
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
    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT
    build_one amd64 "$tmp/amd64"
    build_one arm64 "$tmp/arm64"
    for bin in golauncher glctl; do
        lipo -create -output "$out_dir/$bin" "$tmp/amd64/$bin" "$tmp/arm64/$bin"
    done
else
    build_one "$arch" "$out_dir"
fi

if [ "$zip" -eq 1 ]; then
    name="golauncher_${version}_darwin_$arch"
    stage="$out_dir/$name"
    rm -rf "$stage"
    mkdir -p "$stage"
    cp "$out_dir/golauncher" "$out_dir/glctl" "$stage/"
    for f in LICENSE README.md README.zh-CN.md CHANGELOG.md; do
        [ ! -f "$root/$f" ] || cp "$root/$f" "$stage/"
    done
    zip_path="$out_dir/$name.zip"
    rm -f "$zip_path"
    # ditto keeps the executable bits and is what Finder's Compress uses.
    (cd "$stage" && ditto -c -k --sequesterRsrc . "$zip_path")
    rm -rf "$stage"
    echo "wrote $zip_path"
fi
