#!/bin/sh
# Install the latest byre release binary:
#
#   curl -fsSL https://raw.githubusercontent.com/pjlsergeant/byre/main/install.sh | sh
#
# Environment:
#   BYRE_VERSION      release tag to install (default: latest release)
#   BYRE_INSTALL_DIR  target directory (default: /usr/local/bin if writable,
#                     otherwise ~/.local/bin)
set -eu

repo="pjlsergeant/byre"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
linux | darwin) ;;
*)
	echo "byre install: unsupported OS: $os (use: go install github.com/$repo/cmd/byre@latest)" >&2
	exit 1
	;;
esac

arch=$(uname -m)
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*)
	echo "byre install: unsupported architecture: $arch (use: go install github.com/$repo/cmd/byre@latest)" >&2
	exit 1
	;;
esac

version="${BYRE_VERSION:-}"
if [ -z "$version" ]; then
	# Resolve the latest tag from the release redirect: no API, no rate limit.
	version=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest")
	version="${version##*/}"
fi
case "$version" in
v[0-9]*) ;;
*)
	echo "byre install: could not resolve a release tag (got \"$version\")" >&2
	exit 1
	;;
esac

asset="byre_${version#v}_${os}_${arch}.tar.gz"
base="https://github.com/$repo/releases/download/$version"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "byre install: fetching $asset ($version)"
curl -fsSL -o "$tmp/$asset" "$base/$asset" -o "$tmp/checksums.txt" "$base/checksums.txt"

# grep finding nothing still fails the check: the verifier errors on empty
# input, and set -e stops the install.
sum="sha256sum"
command -v sha256sum >/dev/null 2>&1 || sum="shasum -a 256"
(cd "$tmp" && grep " $asset\$" checksums.txt | $sum -c - >/dev/null)

tar -xzf "$tmp/$asset" -C "$tmp" byre

dir="${BYRE_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
	if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
		dir=/usr/local/bin
	else
		dir="$HOME/.local/bin"
	fi
fi
mkdir -p "$dir"
install -m 0755 "$tmp/byre" "$dir/byre"

echo "byre install: installed $("$dir/byre" version | sed 's/^byre //') to $dir/byre"
case ":$PATH:" in
*":$dir:"*) ;;
*) echo "byre install: note: $dir is not on your PATH" >&2 ;;
esac
