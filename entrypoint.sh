#!/usr/bin/env bash
set -e

# sudo strips the environment, so pass config as positional args
if [ "${CBOX_FIREWALL:-0}" = "1" ]; then
  sudo /usr/local/bin/init-firewall.sh "${CBOX_NET:-open}" "${CBOX_ALLOWED_DOMAINS:-}"
fi

exec "$@"
