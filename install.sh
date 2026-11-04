#!/bin/sh
# Install the latest dbbie release (or $DBBIE_VERSION, e.g. v0.1.0).
#   curl -fsSL https://raw.githubusercontent.com/Yeshwanthyk/dbbie/main/install.sh | sh
# Installs to $DBBIE_INSTALL_DIR, default ~/.local/bin.
set -eu

repo="Yeshwanthyk/dbbie"
dir="${DBBIE_INSTALL_DIR:-$HOME/.local/bin}"

fail() { echo "dbbie install: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || fail "needs $1"; }
need curl
need tar

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "unsupported OS $(uname -s); download a release from https://github.com/$repo/releases" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported CPU $(uname -m)" ;;
esac

tag="${DBBIE_VERSION:-}"
if [ -z "$tag" ]; then
  # The latest-release redirect names the tag without needing the API or jq.
  tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest" | sed 's#.*/tag/##')
  case "$tag" in v*) ;; *) fail "could not find the latest release" ;; esac
fi
version="${tag#v}"
archive="dbbie_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$repo/releases/download/$tag"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "downloading dbbie $tag ($os/$arch)"
curl -fsSL -o "$tmp/$archive" "$base/$archive" || fail "no release archive $archive"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || fail "no checksums.txt in $tag"

want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "$archive missing from checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$archive" | awk '{ print $1 }')
else
  got=$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')
fi
[ "$got" = "$want" ] || fail "checksum mismatch for $archive"

tar -xzf "$tmp/$archive" -C "$tmp" dbbie
mkdir -p "$dir"
install -m 0755 "$tmp/dbbie" "$dir/dbbie"
echo "installed $("$dir/dbbie" version) to $dir/dbbie"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "add $dir to your PATH to run dbbie" ;;
esac
