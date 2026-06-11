#!/usr/bin/env bash
# When the firewall is on, cbox starts the container as root (with only the
# caps for iptables + setpriv): apply rules, then DROP to node. Lowering
# privileges is allowed under no-new-privileges; sudo (raising) is not.
set -e

if [ "$(id -u)" = "0" ]; then
  if [ "${CBOX_FIREWALL:-0}" = "1" ]; then
    /usr/local/bin/init-firewall.sh "${CBOX_NET:-open}" "${CBOX_ALLOWED_DOMAINS:-}"
  fi
  export HOME=/home/node USER=node LOGNAME=node
  exec setpriv --reuid node --regid node --init-groups "$@"
fi

exec "$@"
