package main

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const rootGuard = `if [ "$(id -u)" = 0 ]; then
  echo "cbox: still root after entrypoint — stale image. Run: cbox update" >&2
  exit 1
fi
exec "$@"`

func run(cfg *Config, args ...string) (string, error) {
	out, err := exec.Command(cfg.Runtime, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func imageExists(cfg *Config) bool {
	_, err := run(cfg, "image", "inspect", cfg.ImageTag())
	return err == nil
}

func buildImage(cfg *Config, pull, bust bool) error {
	dir, err := os.MkdirTemp("", "cbox-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	for name, data := range map[string][]byte{
		"Dockerfile":       assetDockerfile,
		"entrypoint.sh":    assetEntrypoint,
		"init-firewall.sh": assetFirewall,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o755); err != nil {
			return err
		}
	}

	args := []string{"build", "--progress=plain", "-t", cfg.ImageTag()}
	if pull {
		args = append(args, "--pull")
	}
	if bust {
		args = append(args, "--build-arg", "CACHE_BUST="+strconv.FormatInt(time.Now().Unix(), 10))
	}
	if runtime.GOOS == "linux" {
		args = append(args,
			"--build-arg", "USER_UID="+strconv.Itoa(os.Getuid()),
			"--build-arg", "USER_GID="+strconv.Itoa(os.Getgid()))
	}
	args = append(args, dir)

	fmt.Printf("%s %s\n", cyan("◆"), bold("building sandbox image ")+dim(cfg.ImageTag()))
	start := time.Now()
	cmd := exec.Command(cfg.Runtime, args...)
	w := &lineWriter{prefix: "  │ "}
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("image build failed: %w", err)
	}
	okLine("image ready in %s", time.Since(start).Round(time.Second))
	return nil
}

func agentSocket() string {
	if runtime.GOOS == "darwin" {
		// Docker Desktop & OrbStack forward the host agent at this VM path.
		return "/run/host-services/ssh-auth.sock"
	}
	return os.Getenv("SSH_AUTH_SOCK")
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

func innerCommand(profile string, extra []string) []string {
	switch profile {
	case "yolo":
		return append([]string{"claude", "--dangerously-skip-permissions"}, extra...)
	case "shell":
		return append([]string{"bash"}, extra...)
	case "login":
		return []string{"claude", "/login"}
	default:
		return append([]string{"claude"}, extra...)
	}
}

func sessionArgs(cfg *Config, inner []string) []string {
	home, _ := os.UserHomeDir()
	pwd, _ := os.Getwd()
	project := unsafeChars.ReplaceAllString(filepath.Base(pwd), "")

	a := []string{"run", "--rm", "-i", "--init",
		"--name", fmt.Sprintf("cbox-%s-%d-%04d", project, os.Getpid(), rand.Intn(10000)),
		"--hostname", "cbox-" + project,
		"--label", "cbox", "--label", "cbox.env=" + cfg.EnvName(),
		"--security-opt", "no-new-privileges",
		"--memory", cfg.Memory, "--memory-swap", cfg.Memory,
		"--pids-limit", cfg.Pids,
		"-e", "TERM=" + envOr("TERM", "xterm-256color"),
		"-e", "COLORTERM=" + envOr("COLORTERM", "truecolor"),
		// Keep ALL claude state (including .claude.json, which normally sits
		// at ~/.claude.json, outside ~/.claude) inside the mounted volume —
		// otherwise OAuth/app state dies with the container and every
		// session asks for login again.
		"-e", "CLAUDE_CONFIG_DIR=/home/node/.claude",
		"-v", pwd + ":/work",
		"-v", cfg.Volume() + ":/home/node/.claude",
	}
	if isTTY(os.Stdin) && isTTY(os.Stdout) {
		a = append(a, "-t")
	}
	if cfg.CPUs != "" {
		a = append(a, "--cpus", cfg.CPUs)
	}

	if cfg.Net != "full" {
		// Starts as root with only the caps for iptables plus the SETUID/SETGID
		// needed by setpriv to drop to node — compatible with no-new-privileges,
		// which blocks privilege *raising*, not lowering.
		a = append(a, "--user", "root",
			"--cap-drop=ALL", "--cap-add=NET_ADMIN", "--cap-add=NET_RAW",
			"--cap-add=SETUID", "--cap-add=SETGID",
			"-e", "CBOX_FIREWALL=1", "-e", "CBOX_NET="+cfg.Net)
		if cfg.AllowedDomains != "" {
			a = append(a, "-e", "CBOX_ALLOWED_DOMAINS="+cfg.AllowedDomains)
		}
	} else {
		a = append(a, "--cap-drop=ALL")
	}

	switch cfg.SSH {
	case "key":
		if fileExists(cfg.Key) {
			a = append(a, "-v", cfg.Key+":/home/node/.ssh/id_ed25519:ro")
			if fileExists(cfg.Key + ".pub") {
				a = append(a, "-v", cfg.Key+".pub:/home/node/.ssh/id_ed25519.pub:ro")
			}
		}
	case "agent":
		if sock := agentSocket(); sock != "" {
			a = append(a, "-v", sock+":/ssh-agent.sock", "-e", "SSH_AUTH_SOCK=/ssh-agent.sock")
		} else {
			warnLine("CBOX_SSH=agent but no agent socket available — continuing without ssh")
		}
	}
	if kh := filepath.Join(home, ".ssh", "known_hosts"); fileExists(kh) {
		a = append(a, "-v", kh+":/home/node/.ssh/known_hosts:ro")
	}
	if fileExists(cfg.GitConfig) {
		a = append(a, "-v", cfg.GitConfig+":/home/node/.gitconfig:ro")
	}
	if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != "" {
		a = append(a, "-e", "CLAUDE_CODE_OAUTH_TOKEN")
	}
	for _, p := range cfg.Ports {
		a = append(a, "-p", fmt.Sprintf("127.0.0.1:%s:%s", p, p))
	}
	for _, m := range cfg.Mounts {
		a = append(a, "-v", m)
	}

	a = append(a, cfg.ImageTag(), "bash", "-c", rootGuard, "cbox-guard")
	return append(a, inner...)
}

// execRuntime replaces the cbox process with the container runtime so the
// session owns the TTY directly — zero wrapper overhead while claude runs.
func execRuntime(cfg *Config, argv []string) error {
	path, err := exec.LookPath(cfg.Runtime)
	if err != nil {
		return fmt.Errorf("container runtime %q not found in PATH", cfg.Runtime)
	}
	return syscall.Exec(path, append([]string{cfg.Runtime}, argv...), os.Environ())
}
