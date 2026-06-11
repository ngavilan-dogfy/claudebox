FROM node:22-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git openssh-client ripgrep curl ca-certificates jq procps less \
    iptables ipset dnsutils util-linux \
 && rm -rf /var/lib/apt/lists/*

RUN npm install -g @anthropic-ai/claude-code

# On native Linux the container user must match the host UID/GID or
# bind-mounted repos end up with broken ownership (macOS VMs map this for you).
ARG USER_UID=1000
ARG USER_GID=1000
RUN if [ "$USER_UID" != "1000" ] || [ "$USER_GID" != "1000" ]; then \
      groupmod -g "$USER_GID" node \
      && usermod -u "$USER_UID" -g "$USER_GID" node \
      && chown -R "$USER_UID:$USER_GID" /home/node; \
    fi

COPY init-firewall.sh /usr/local/bin/init-firewall.sh
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/init-firewall.sh /usr/local/bin/entrypoint.sh \
 && mkdir -p /home/node/.claude /home/node/.ssh \
 && chown -R node:node /home/node/.claude /home/node/.ssh \
 && chmod 700 /home/node/.ssh

ENV DISABLE_AUTOUPDATER=1 \
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
    LANG=C.UTF-8 \
    LC_ALL=C.UTF-8

USER node
WORKDIR /work
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["claude"]
