#!/usr/bin/env bash
set -euo pipefail

REPO="nvquanghuy/repoview"
INSTALL_DIR="$HOME/.local/bin"

# Detect OS
case "$(uname -s)" in
  Linux*)  OS="linux" ;;
  Darwin*) OS="darwin" ;;
  *)
    echo "Error: unsupported operating system: $(uname -s)" >&2
    exit 1
    ;;
esac

# Detect architecture
case "$(uname -m)" in
  x86_64)       ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    echo "Error: unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

TARBALL="repoview-${OS}-${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/latest/download/${TARBALL}"

echo "Downloading repoview for ${OS}/${ARCH}..."

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

if ! curl -fSL --progress-bar -o "${TMPDIR}/${TARBALL}" "$URL"; then
  echo "Error: failed to download ${URL}" >&2
  echo "Make sure a release exists at https://github.com/${REPO}/releases/latest" >&2
  exit 1
fi

mkdir -p "$INSTALL_DIR"
tar xzf "${TMPDIR}/${TARBALL}" -C "$INSTALL_DIR"
chmod +x "${INSTALL_DIR}/repoview"

echo "Installed repoview to ${INSTALL_DIR}/repoview"

if ! echo "$PATH" | tr ':' '\n' | grep -qx "$INSTALL_DIR"; then
  echo ""
  echo "Add ~/.local/bin to your PATH if it's not already there:"
  echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
fi
