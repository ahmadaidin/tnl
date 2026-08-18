#!/usr/bin/env bash
#
# Install tnl from the latest GitHub release.
#
# Usage:
#   ./install.sh                 # install into ./bin
#   ./install.sh /usr/local/bin  # install into an explicit directory
#   TNL_VERSION=v0.1.0 ./install.sh  # pin a release version
#
# Downloads https://github.com/ahmadaidin/tnl/releases/latest/download/tnl-<os>-<arch>
# (or .../download/<TNL_VERSION>/tnl-<os>-<arch> when TNL_VERSION is set).
#
# Platform notes:
#   - The release does not ship binaries for FreeBSD or 32-bit platforms;
#     the script exits with an error there.
#   - On Windows there is no system ssh to supervise, so installation is
#     unsupported (exit code 2).

set -euo pipefail

# shellcheck disable=SC2155
readonly REPO="ahmadaidin/tnl"

# Resolve the destination directory (default: ./bin relative to CWD).
readonly DEST="${1:-bin}"

usage() {
  sed -n '2,13p' "$0"
  exit "${1:-0}"
}

# --help / --version are the only flags we honor; everything else is a path.
case "${1:-}" in
  -h | --help) usage ;;
esac

# Map the runtime to the release asset suffix.
case "$(uname -s)" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  FreeBSD | OpenBSD | NetBSD | DragonFly) os="bsd" ;;
  CYGWIN* | MINGW* | MSYS*) os="windows" ;;
  *)
    echo "error: unsupported operating system: $(uname -s)" >&2
    exit 1
    ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch="amd64" ;;
  arm64 | aarch64) arch="arm64" ;;
  i386 | i686 | x86) arch="386" ;;
  *)
    echo "error: unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

# The SLSA release workflow only builds linux/darwin for amd64/arm64.
if [[ "$os" == "bsd" ]]; then
  echo "error: tnl does not publish ${os} binaries; build from source (see README)" >&2
  exit 1
fi
if [[ "$os" == "windows" ]]; then
  echo "error: tnl requires the system ssh and is not supported on Windows" >&2
  exit 1
fi
if [[ "$arch" != "amd64" && "$arch" != "arm64" ]]; then
  echo "error: tnl does not publish ${os}/${arch} binaries; build from source (see README)" >&2
  exit 1
fi

asset="tnl-${os}-${arch}"

if [[ -n "${TNL_VERSION:-}" ]]; then
  base_url="https://github.com/${REPO}/releases/download/${TNL_VERSION}"
else
  base_url="https://github.com/${REPO}/releases/latest/download"
fi
url="${base_url}/${asset}"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

echo "Downloading ${url}"
if command -v curl >/dev/null 2>&1; then
  curl -fsSL --proto '=https' --tlsv1.2 -o "${tmpdir}/${asset}" "$url"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "${tmpdir}/${asset}" "$url"
else
  echo "error: neither curl nor wget is available" >&2
  exit 1
fi

mkdir -p "$DEST"
install -m 0755 "${tmpdir}/${asset}" "$DEST/tnl"

echo "Installed tnl to ${DEST}/tnl"
echo "Run '${DEST}/tnl version' to confirm, or add ${DEST} to your PATH."
