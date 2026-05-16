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
# Use GitHub's /releases/latest/download/ redirect — it resolves to the
# newest non-prerelease asset WITHOUT calling the rate-limited GitHub API
# (anonymous api.github.com is 60 req/hr/IP; this path has no such limit).
URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"

echo "Downloading latest ${BINARY} for ${OS}/${ARCH}..."
tmpdir=$(mktemp -d)
if ! curl -fsSL "$URL" | tar xz -C "$tmpdir" 2>/dev/null; then
  echo "Error: could not download ${ASSET} from" >&2
  echo "  ${URL}" >&2
  echo "Check that a release exists: https://github.com/${REPO}/releases" >&2
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
