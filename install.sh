#!/usr/bin/env bash
# claudebox installer — macOS, Linux, Windows via WSL2.
#
#   curl -fsSL https://raw.githubusercontent.com/ngavilan-dogfy/claudebox/main/install.sh | bash
#   # or: git clone https://github.com/ngavilan-dogfy/claudebox ~/claudebox && ~/claudebox/install.sh
#
# Idempotent: safe to re-run after a git pull.
set -euo pipefail

REPO_URL="${CBOX_REPO:-https://github.com/ngavilan-dogfy/claudebox}"

ok()   { printf '  \033[32mOK\033[0m  %s\n' "$1"; }
warn() { printf '  \033[33m!!\033[0m  %s\n' "$1"; }
die()  { printf '  \033[31mXX\033[0m  %s\n' "$1"; exit 1; }

SRC_DIR="$(cd "$(dirname "$0")" && pwd)"
OS="$(uname -s)"

# Piped via curl? Bootstrap: clone (or update) the repo and re-exec from there.
if [[ ! -f $SRC_DIR/Dockerfile || ! -f $SRC_DIR/cbox ]]; then
  TARGET="${CBOX_HOME:-$HOME/claudebox}"
  if [[ -d $TARGET/.git ]]; then
    ok "updating existing clone at $TARGET"
    git -C "$TARGET" pull --ff-only -q
  else
    ok "cloning $REPO_URL → $TARGET"
    git clone -q "$REPO_URL" "$TARGET"
  fi
  exec bash "$TARGET/install.sh"
fi

case "$OS" in
  Darwin|Linux) ok "platform: $OS" ;;
  *) die "unsupported platform '$OS' — on Windows, run inside WSL2" ;;
esac
if [[ $OS == Linux ]] && grep -qi microsoft /proc/version 2>/dev/null; then
  ok "WSL2 detected"
fi

# ---- container runtime --------------------------------------------------------
RUNTIME="${CBOX_RUNTIME:-}"
if [[ -z $RUNTIME ]]; then
  for c in docker podman; do command -v "$c" >/dev/null && { RUNTIME=$c; break; }; done
fi
[[ -n $RUNTIME ]] || die "no container runtime found — install OrbStack (macOS, recommended), Docker or Podman"
"$RUNTIME" info >/dev/null 2>&1 || die "$RUNTIME daemon not running — start it and re-run"
ok "runtime: $RUNTIME"

# ---- cbox on PATH ---------------------------------------------------------------
BIN_DIR="${CBOX_BIN_DIR:-$HOME/.local/bin}"
mkdir -p "$BIN_DIR"
ln -sf "$SRC_DIR/cbox" "$BIN_DIR/cbox"
chmod +x "$SRC_DIR/cbox" "$SRC_DIR/entrypoint.sh" "$SRC_DIR/init-firewall.sh"
ok "linked $BIN_DIR/cbox"
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) warn "$BIN_DIR is not in PATH — add to your shell rc: export PATH=\"$BIN_DIR:\$PATH\"" ;;
esac

# ---- image -----------------------------------------------------------------------
build_args=()
if [[ $OS == Linux ]]; then
  build_args+=( --build-arg USER_UID="$(id -u)" --build-arg USER_GID="$(id -g)" )
fi
"$RUNTIME" build -q ${build_args[@]+"${build_args[@]}"} -t "${CBOX_IMAGE:-claude-box}" "$SRC_DIR" >/dev/null
ok "image built"

# ---- agent identity (optional but recommended) -------------------------------------
KEY="$HOME/.ssh/claude_agent"
if [[ -f $KEY ]]; then
  ok "dedicated SSH key exists: $KEY"
else
  warn "no dedicated SSH key — create one with: ssh-keygen -t ed25519 -f $KEY -N ''"
fi
GITCFG="$HOME/.config/cbox/gitconfig"
if [[ ! -f $GITCFG ]]; then
  mkdir -p "$(dirname "$GITCFG")"
  printf '[user]\n\tname = %s (agent)\n\temail = %s\n' \
    "$(git config user.name 2>/dev/null || echo claude-agent)" \
    "$(git config user.email 2>/dev/null || echo agent@example.invalid)" > "$GITCFG"
  ok "created agent gitconfig template: $GITCFG (edit it!)"
else
  ok "agent gitconfig exists: $GITCFG"
fi

echo
echo "Done. Next steps:"
echo "  cd <your-project> && cbox      # first run asks for login (paste-code flow)"
echo "  cbox doctor                    # verify everything"
