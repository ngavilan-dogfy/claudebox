#!/usr/bin/env bash
# claudebox installer — macOS, Linux, Windows via WSL2.
#
#   curl -fsSL https://raw.githubusercontent.com/ngavilan-dogfy/claudebox/main/install.sh | bash
#
# Downloads the prebuilt cbox binary for your platform (falls back to
# building from source if Go is available). Idempotent: re-run to upgrade.
set -euo pipefail

REPO="${CBOX_REPO:-ngavilan-dogfy/claudebox}"
BIN_DIR="${CBOX_BIN_DIR:-$HOME/.local/bin}"

ok()   { printf '  \033[32mOK\033[0m  %s\n' "$1"; }
warn() { printf '  \033[33m!!\033[0m  %s\n' "$1"; }
die()  { printf '  \033[31mXX\033[0m  %s\n' "$1"; exit 1; }

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) die "unsupported architecture: $ARCH" ;;
esac
case "$OS" in
  darwin|linux) ok "platform: $OS/$ARCH" ;;
  *) die "unsupported platform '$OS' — on Windows, run inside WSL2" ;;
esac

mkdir -p "$BIN_DIR"
URL="https://github.com/$REPO/releases/latest/download/cbox-$OS-$ARCH"

if curl -fsSL "$URL" -o "$BIN_DIR/.cbox.tmp"; then
  mv "$BIN_DIR/.cbox.tmp" "$BIN_DIR/cbox"
  chmod +x "$BIN_DIR/cbox"
  ok "installed prebuilt binary → $BIN_DIR/cbox"
elif command -v go >/dev/null; then
  warn "no release binary for $OS/$ARCH — building from source"
  TMP="$(mktemp -d)"
  git clone -q --depth 1 "https://github.com/$REPO" "$TMP"
  (cd "$TMP" && go build -trimpath -o "$BIN_DIR/cbox" .)
  rm -rf "$TMP"
  ok "built from source → $BIN_DIR/cbox"
else
  die "could not download $URL and Go is not installed"
fi

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) warn "$BIN_DIR is not in PATH — add to your shell rc: export PATH=\"$BIN_DIR:\$PATH\"" ;;
esac

ok "$("$BIN_DIR/cbox" version)"

RUNTIME="${CBOX_RUNTIME:-docker}"
if command -v "$RUNTIME" >/dev/null && "$RUNTIME" info >/dev/null 2>&1; then
  "$BIN_DIR/cbox" build
else
  warn "container runtime not running — the image will build on first cbox run"
fi

echo
echo "Done. Next steps:"
echo "  cd <your-project> && cbox      # first run asks for login (paste-code flow)"
echo "  cbox doctor                    # verify everything"
