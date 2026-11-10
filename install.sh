#!/bin/sh
# lore installer for macOS / Linux.
#
# Usage:
#   curl -sL https://raw.githubusercontent.com/thesatellite-ai/lore/main/install.sh | sh
#
# Detects OS + arch, downloads the latest released binary from GitHub
# Releases, installs it to /usr/local/bin, and (optionally) installs the
# Claude skill bundle to ~/.claude/skills/lore.

set -e

REPO="thesatellite-ai/lore"
BINARY="lore"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
SKILL_DEST="${HOME}/.claude/skills/lore"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" && exit 1 ;;
esac

case "$OS" in
  linux|darwin) ;;
  *) echo "Unsupported OS: $OS" && exit 1 ;;
esac

ASSET="${BINARY}_${OS}_${ARCH}.tar.gz"
# Resolve the newest tag from GitHub's /releases/latest redirect — it
# names the newest non-prerelease WITHOUT calling the rate-limited GitHub
# API (anonymous api.github.com is 60 req/hr/IP; this path has no limit).
# The archive and checksums.txt then both come from that exact tag, so a
# release published between two requests cannot mix files.
LATEST=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")
TAG=${LATEST##*/tag/}
case "$TAG" in
  v*) ;;
  *) echo "Error: could not find the latest release (${LATEST})" >&2; exit 1 ;;
esac
BASE="https://github.com/${REPO}/releases/download/${TAG}"

echo "Downloading ${BINARY} ${TAG} for ${OS}/${ARCH}..."
tmpdir=$(mktemp -d)
if ! curl -fsSL -o "$tmpdir/$ASSET" "$BASE/$ASSET" || ! curl -fsSL -o "$tmpdir/checksums.txt" "$BASE/checksums.txt"; then
  echo "Error: could not download ${ASSET} or checksums.txt from" >&2
  echo "  ${BASE}" >&2
  echo "Check that a release exists: https://github.com/${REPO}/releases" >&2
  exit 1
fi

# Check the archive's sha256 against its line in checksums.txt (catches a
# corrupt or truncated download; signed releases are a later step).
WANT=$(awk -v a="$ASSET" '$2 == a || $2 == "*" a { print $1 }' "$tmpdir/checksums.txt")
if command -v sha256sum >/dev/null 2>&1; then
  GOT=$(sha256sum "$tmpdir/$ASSET" | awk '{ print $1 }')
else
  GOT=$(shasum -a 256 "$tmpdir/$ASSET" | awk '{ print $1 }')
fi
if [ -z "$WANT" ] || [ "$WANT" != "$GOT" ]; then
  echo "Error: sha256 of ${ASSET} does not match checksums.txt; nothing installed." >&2
  rm -rf "$tmpdir"
  exit 1
fi
echo "sha256 ok"

if ! tar xzf "$tmpdir/$ASSET" -C "$tmpdir" 2>/dev/null; then
  echo "Error: could not unpack ${ASSET}" >&2
  exit 1
fi

if [ ! -f "$tmpdir/$BINARY" ]; then
  echo "Error: ${BINARY} binary not found in archive." && exit 1
fi

echo "Installing to ${INSTALL_DIR}/${BINARY}..."
if [ -w "$INSTALL_DIR" ]; then
  mv "$tmpdir/$BINARY" "$INSTALL_DIR/$BINARY"
else
  sudo mv "$tmpdir/$BINARY" "$INSTALL_DIR/$BINARY"
fi
chmod +x "$INSTALL_DIR/$BINARY"

# Install the Claude skill bundle shipped inside the archive.
if [ -d "$tmpdir/skills" ]; then
  echo "Installing Claude skill to ${SKILL_DEST}..."
  mkdir -p "$SKILL_DEST"
  cp -R "$tmpdir/skills/." "$SKILL_DEST/"
fi

rm -rf "$tmpdir"

echo ""
echo "Installed. Next steps:"
echo "  cd <your-project>"
echo "  ${BINARY} init                 # create .lore/ + sqlite db"
echo "  ${BINARY} setup                # build FTS5 search index"
echo "  ${BINARY} directive install    # add the agent-directive block to CLAUDE.md / AGENTS.md"
echo ""
echo "The Claude skill is at ${SKILL_DEST} (restart Claude Code to load it)."
echo "Verify: ${BINARY} version"
