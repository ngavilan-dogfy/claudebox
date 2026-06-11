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

# ---- PATH: configure the user's shell rc, idempotently -------------------------
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *)
    RC=""
    case "$(basename "${SHELL:-}")" in
      zsh)  RC="$HOME/.zshrc" ;;
      bash) RC="$HOME/.bashrc" ;;
    esac
    if [[ -n $RC ]] && ! grep -qs 'added by cbox installer' "$RC"; then
      printf '\nexport PATH="%s:$PATH" # added by cbox installer\n' "$BIN_DIR" >> "$RC"
      ok "added $BIN_DIR to PATH in $RC — restart your shell or: source $RC"
    else
      warn "$BIN_DIR is not in PATH — add: export PATH=\"$BIN_DIR:\$PATH\""
    fi ;;
esac

ok "$("$BIN_DIR/cbox" version)"

# ---- dedicated agent SSH key (never your personal one) --------------------------
KEY="$HOME/.ssh/claude_agent"
if [[ ! -f $KEY ]]; then
  mkdir -p "$HOME/.ssh" && chmod 700 "$HOME/.ssh"
  ssh-keygen -q -t ed25519 -N '' -C 'claude-agent' -f "$KEY"
  ok "created dedicated agent SSH key: $KEY"
  echo "        public key (add it as a deploy key wherever the agent needs access):"
  sed 's/^/          /' "$KEY.pub"
else
  ok "dedicated agent SSH key exists: $KEY"
fi

# ---- global config: one place to tune everything --------------------------------
CFG_FILE="$HOME/.config/cbox/config"
if [[ ! -f $CFG_FILE ]]; then
  mkdir -p "$(dirname "$CFG_FILE")"
  cat > "$CFG_FILE" <<'EOF'
# cbox global config — applies to every project; a project's .cbox.conf
# overrides anything here. Uncomment what you want to change.

# CBOX_NET="open"            # open (internet sí, host/LAN no) | allowlist | full
# CBOX_SSH="key"             # key | agent | none
# CBOX_MEMORY="8g"           # techo de RAM por sesión (no reserva)
# CBOX_CPUS="4"              # techo de CPU
# CBOX_RUNTIME="docker"      # docker | podman
# CBOX_ALLOWED_DOMAINS="api.anthropic.com,github.com"   # para CBOX_NET=allowlist
EOF
  ok "created global config: $CFG_FILE — one place to tune everything"
else
  ok "global config exists: $CFG_FILE"
fi

# ---- image: build current, sweep stale ------------------------------------------
RUNTIME="${CBOX_RUNTIME:-docker}"
if command -v "$RUNTIME" >/dev/null && "$RUNTIME" info >/dev/null 2>&1; then
  "$BIN_DIR/cbox" build
  "$BIN_DIR/cbox" cleanup
  echo
  "$BIN_DIR/cbox" doctor || true
else
  warn "container runtime not running — the image will build on first cbox run"
fi

echo
echo "Done. Next step:  cd <your-project> && cbox"
echo "(first run asks for login once — paste-code flow — then it persists)"
