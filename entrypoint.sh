#!/usr/bin/env bash
# When the firewall is on, cbox starts the container as root (with only the
# caps for iptables + setpriv): apply rules, then DROP to node. Lowering
# privileges is allowed under no-new-privileges; sudo (raising) is not.
set -e

CFG=/home/node/.claude
AUTH=/claude-auth

# One cbox login shared across every env and session: the live credential
# lives in the shared auth volume and each env's config dir holds a symlink
# to it. A /login or token refresh inside a session atomically replaces the
# symlink with a regular file — we publish that back here on the next start,
# so the link is self-healing and the freshest token always wins.
sync_auth() {
  [ -d "$AUTH" ] || return 0
  if [ -f "$CFG/.credentials.json" ] && [ ! -L "$CFG/.credentials.json" ]; then
    if [ ! -f "$AUTH/.credentials.json" ] || [ "$CFG/.credentials.json" -nt "$AUTH/.credentials.json" ]; then
      cp "$CFG/.credentials.json" "$AUTH/.credentials.json"
      chmod 600 "$AUTH/.credentials.json"
    fi
    rm -f "$CFG/.credentials.json"
  fi
  if [ -f "$AUTH/.credentials.json" ]; then
    ln -sfn "$AUTH/.credentials.json" "$CFG/.credentials.json"
  fi
}

# Root does the minimum (firewall + handing the auth volume to node: fresh
# volumes mount root-owned) and re-enters this script as node via setpriv —
# the caps we keep don't include DAC_OVERRIDE, so root here can't even read
# node's 600 files; the auth sync must run as node.
if [ "$(id -u)" = "0" ]; then
  if [ "${CBOX_FIREWALL:-0}" = "1" ]; then
    /usr/local/bin/init-firewall.sh "${CBOX_NET:-open}" "${CBOX_ALLOWED_DOMAINS:-}"
  fi
  chown node:node "$AUTH" 2>/dev/null || true
  export HOME=/home/node USER=node LOGNAME=node
  exec setpriv --reuid node --regid node --init-groups /usr/local/bin/entrypoint.sh "$@"
fi

# As node (CBOX_NET=full lands here directly; skip sync if volume unowned).
sync_auth 2>/dev/null || true
exec "$@"
