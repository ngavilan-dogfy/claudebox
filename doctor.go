package main

import (
	"fmt"
	"os/exec"
	"strings"
)

func doctor(cfg *Config) int {
	fail := 0
	bad := func(format string, a ...any) { badLine(format, a...); fail = 1 }

	fmt.Println(bold("cbox doctor"))

	if v, err := run(cfg, "version", "--format", "{{.Client.Version}}"); err == nil {
		okLine("runtime: %s %s", cfg.Runtime, v)
	} else {
		bad("container runtime %q not found or not responding", cfg.Runtime)
		return 1
	}
	if _, err := run(cfg, "info"); err == nil {
		okLine("daemon running")
	} else {
		bad("daemon not running — start Docker/OrbStack")
		return 1
	}

	if imageExists(cfg) {
		okLine("image %s built", cfg.ImageTag())
	} else {
		warnLine("image %s missing — built automatically on first run", cfg.ImageTag())
	}

	if p, err := exec.LookPath("cbox"); err == nil {
		okLine("cbox in PATH (%s)", p)
	} else {
		warnLine("cbox not in PATH")
	}

	if fileExists(cfg.Key) {
		okLine("agent SSH key: %s", cfg.Key)
	} else {
		warnLine("no dedicated key — create: ssh-keygen -t ed25519 -f %s", cfg.Key)
	}
	if fileExists(cfg.GitConfig) {
		okLine("agent gitconfig: %s", cfg.GitConfig)
	} else {
		warnLine("no agent gitconfig (%s) — created automatically on first run", cfg.GitConfig)
	}
	if sock := agentSocket(); sock != "" {
		okLine("ssh-agent socket for CBOX_SSH=agent: %s", sock)
	} else {
		warnLine("no ssh-agent socket — CBOX_SSH=agent unavailable")
	}

	if imageExists(cfg) {
		if _, err := run(cfg, "run", "--rm",
			"-v", cfg.Volume()+":/home/node/.claude", cfg.ImageTag(),
			"bash", "-c", "test -s /home/node/.claude/.credentials.json"); err == nil {
			okLine("env %q logged in", cfg.EnvName())
		} else {
			warnLine("env %q not logged in yet — run: cbox%s login",
				cfg.EnvName(), envSuffix(cfg))
		}

		// Mirror the exact flags of a real session, no-new-privileges included.
		out, err := run(cfg, "run", "--rm", "--user", "root",
			"--security-opt", "no-new-privileges",
			"--cap-drop=ALL", "--cap-add=NET_ADMIN", "--cap-add=NET_RAW",
			"--cap-add=SETUID", "--cap-add=SETGID",
			"-e", "CBOX_FIREWALL=1", "-e", "CBOX_NET=open", cfg.ImageTag(),
			"bash", "-c", rootGuard, "cbox-guard",
			"bash", "-c", "id -un; curl -s --max-time 3 -o /dev/null http://host.docker.internal && echo HOST_OPEN || true")
		switch {
		case err != nil:
			bad("firewall: sandbox failed to start: %s", strings.SplitN(out, "\n", 2)[0])
		case strings.Contains(out, "HOST_OPEN"):
			bad("firewall: host reachable from container")
		case strings.Contains(out, "node"):
			okLine("firewall: host unreachable, session runs as node")
		default:
			bad("firewall: container did not drop to node user")
		}
	}

	if fail == 0 {
		fmt.Println(dim("\neverything checks out"))
	}
	return fail
}

func envSuffix(cfg *Config) string {
	if cfg.Env == "" {
		return ""
	}
	return " @" + cfg.Env
}
