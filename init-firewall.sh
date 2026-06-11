#!/bin/bash
# Egress firewall, run as root at container start.
#
# CBOX_NET=open       (default) full internet access, but the host machine,
#                     the LAN and any private/link-local range are unreachable.
# CBOX_NET=allowlist  default-deny; only CBOX_ALLOWED_DOMAINS (or built-in list).
# CBOX_NET=full       no rules at all (also reaches host/LAN — opt-in only).
#
# DNS (port 53) stays open in both filtered modes: resolution is needed and on
# some setups the resolver lives on the gateway. This stops casual
# exfiltration and host access, not a determined DNS tunnel.
set -euo pipefail

# Config arrives as positional args (sudo strips env vars)
MODE="${1:-${CBOX_NET:-open}}"
CBOX_ALLOWED_DOMAINS="${2:-${CBOX_ALLOWED_DOMAINS:-}}"

[[ $MODE == full ]] && exit 0

iptables -F OUTPUT
iptables -A OUTPUT -o lo -j ACCEPT
iptables -A OUTPUT -m state --state ESTABLISHED,RELATED -j ACCEPT
iptables -A OUTPUT -p udp --dport 53 -j ACCEPT
iptables -A OUTPUT -p tcp --dport 53 -j ACCEPT

if [[ $MODE == open ]]; then
  # Host aliases first (covers runtimes that map the host outside RFC1918)
  for ip in $(getent ahostsv4 host.docker.internal 2>/dev/null | awk '{print $1}' | sort -u); do
    iptables -A OUTPUT -d "$ip" -j REJECT
  done
  # Private, link-local, CGN and benchmark (OrbStack) ranges = host + LAN
  for net in 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 169.254.0.0/16 \
             100.64.0.0/10 198.18.0.0/15; do
    iptables -A OUTPUT -d "$net" -j REJECT
  done
  iptables -A OUTPUT -j ACCEPT
  echo "[cbox] net=open: internet OK, host/LAN blocked"
  exit 0
fi

# ---- allowlist mode ---------------------------------------------------------
DEFAULT_DOMAINS="api.anthropic.com,statsig.anthropic.com,sentry.io,registry.npmjs.org,github.com,api.github.com,codeload.github.com,objects.githubusercontent.com,raw.githubusercontent.com"
DOMAINS="${CBOX_ALLOWED_DOMAINS:-$DEFAULT_DOMAINS}"

ipset destroy cbox-allow 2>/dev/null || true
ipset create cbox-allow hash:net

for d in ${DOMAINS//,/ }; do
  for ip in $(dig +short A "$d" | grep -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$'); do
    ipset add cbox-allow "$ip" 2>/dev/null || true
  done
done

if [[ ",$DOMAINS," == *",github.com,"* ]]; then
  meta=$(curl -fsSL --max-time 10 https://api.github.com/meta || true)
  if [[ -n "$meta" ]]; then
    for cidr in $(echo "$meta" | jq -r '(.git + .api + .web)[]' | grep -v ':'); do
      ipset add cbox-allow "$cidr" 2>/dev/null || true
    done
  fi
fi

iptables -A OUTPUT -m set --match-set cbox-allow dst -j ACCEPT
iptables -A OUTPUT -j REJECT --reject-with icmp-port-unreachable
echo "[cbox] net=allowlist: egress restricted to: $DOMAINS"
