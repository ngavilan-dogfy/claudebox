package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) > 0 && strings.HasPrefix(args[0], "@") {
		os.Setenv("CBOX_ENV", strings.TrimPrefix(args[0], "@"))
		args = args[1:]
	}
	cfg := loadConfig()

	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}

	var err error
	switch cmd {
	case "build":
		err = buildImage(cfg, false, false)
	case "update":
		if err = buildImage(cfg, true, true); err == nil {
			err = cleanup(cfg)
		}
	case "cleanup":
		err = cleanup(cfg)
	case "new", "n":
		name := ""
		var extra []string
		if len(args) > 1 {
			if !strings.HasPrefix(args[1], "-") {
				name = args[1]
				extra = args[2:]
			} else {
				extra = args[1:]
			}
		}
		err = newSession(cfg, name, extra)
	case "ls":
		err = lsSessions(cfg)
	case "attach", "a":
		arg := ""
		if len(args) > 1 {
			arg = args[1]
		}
		err = attachCmd(cfg, arg)
	case "kill":
		arg := ""
		if len(args) > 1 {
			arg = args[1]
		}
		err = killCmd(cfg, arg)
	case "ps":
		err = passthrough(cfg, "ps", "--filter", "label=cbox",
			"--format", `table {{.Names}}\t{{.Label "cbox.env"}}\t{{.Label "cbox.session"}}\t{{.RunningFor}}\t{{.Status}}`)
	case "envs":
		err = listEnvs(cfg)
	case "doctor":
		os.Exit(doctor(cfg))
	case "version", "--version", "-V":
		fmt.Printf("cbox %s %s\n", version, dim("(image "+cfg.ImageTag()+")"))
	case "help", "-h", "--help":
		usage()
	case "ui", "tui":
		err = dashboard(cfg)
	case "_cycle":
		if len(args) >= 4 {
			err = cycleSession(args[1], args[2], args[3])
		}
	case "yolo":
		err = session(cfg, "yolo", args[1:])
	case "shell":
		err = session(cfg, "shell", args[1:])
	case "login":
		err = session(cfg, "login", nil)
	default:
		err = session(cfg, "run", args)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %v\n", red("cbox:"), err)
		os.Exit(1)
	}
}

// dashboard runs the interactive TUI and executes whatever the user picked.
func dashboard(cfg *Config) error {
	if !imageExists(cfg) {
		if err := buildImage(cfg, false, false); err != nil {
			return err
		}
	}
	action, err := runTUI(cfg)
	if err != nil || action == nil {
		return err
	}
	switch action.kind {
	case "attach":
		return attachSession(action.arg)
	case "shell":
		// New shell inside the live session container, as node (the
		// container itself was started --user root for the firewall).
		return execRuntime(cfg, []string{"exec", "-it",
			"-u", "node", "-e", "HOME=/home/node", "-w", "/work",
			action.arg, "bash"})
	case "project":
		if err := os.Chdir(action.arg); err != nil {
			return err
		}
		return startPreferManaged(loadConfig()) // reload: project .cbox.conf applies
	case "env-session":
		if action.arg != "default" {
			os.Setenv("CBOX_ENV", action.arg)
		}
		return startPreferManaged(loadConfig())
	}
	return nil
}

// Managed (tmux) session when possible, plain direct run otherwise — the
// dashboard must stay usable on machines without tmux.
func startPreferManaged(cfg *Config) error {
	if tmuxNeeded() == nil {
		return newSession(cfg, "", nil)
	}
	return session(cfg, "run", nil)
}

func session(cfg *Config, profile string, extra []string) error {
	if err := guardHome(cfg); err != nil {
		return err
	}
	pwd, _ := os.Getwd()
	recordProject(pwd)
	if !imageExists(cfg) {
		fmt.Println(dim("first run for this image version — building the sandbox (cached afterwards)"))
		if err := buildImage(cfg, false, false); err != nil {
			return err
		}
	}
	ensureGitconfigTemplate(cfg)
	maybeSeedEnv(cfg)

	printHeader(cfg, profile)
	return execRuntime(cfg, sessionArgs(cfg, innerCommand(profile, extra)))
}

// Mounting your home (or an ancestor of it) hands the agent your real SSH
// keys, tokens and config — the exact thing this sandbox exists to prevent.
func guardHome(cfg *Config) error {
	if cfg.AllowHome {
		return nil
	}
	pwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	rel, err := filepath.Rel(pwd, home)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("refusing to run from %s — it contains your home directory.\n      cd into a project directory (or CBOX_ALLOW_HOME=1 if you really mean it)", pwd)
	}
	return nil
}

func printHeader(cfg *Config, profile string) {
	if !isTTY(os.Stdout) {
		return
	}
	netDesc := map[string]string{
		"open":      green("internet ✓") + dim(" · ") + green("host/LAN ✗"),
		"allowlist": yellow("allowlist only") + dim(" · ") + green("host/LAN ✗"),
		"full":      red("unrestricted — host reachable"),
	}[cfg.Net]
	sshDesc := map[string]string{
		"key":   "key " + dim(cfg.Key),
		"agent": "agent socket " + dim("(key never enters the container)"),
		"none":  dim("none"),
	}[cfg.SSH]
	pwd, _ := os.Getwd()
	rows := [][2]string{
		{"project", bold(filepath.Base(pwd))},
		{"env", cfg.EnvName()},
		{"net", cfg.Net + "  " + netDesc},
		{"ssh", sshDesc},
		{"image", dim(cfg.ImageTag())},
	}
	if profile == "yolo" {
		rows = append(rows, [2]string{"mode", yellow("yolo — no permission prompts")})
	}
	fmt.Print(panel("claudebox", rows))
}

func passthrough(cfg *Config, args ...string) error {
	out, err := run(cfg, args...)
	if out != "" {
		fmt.Println(out)
	}
	return err
}

func listEnvs(cfg *Config) error {
	out, err := run(cfg, "volume", "ls", "--filter", "name=claude-box-config", "--format", "{{.Name}}")
	if err != nil {
		return err
	}
	if out == "" {
		fmt.Println(dim("no environments yet — they are created on first use"))
		return nil
	}
	haveImage := imageExists(cfg)
	for _, vol := range strings.Split(out, "\n") {
		name := strings.TrimPrefix(vol, "claude-box-config")
		name = strings.TrimPrefix(name, "-")
		if name == "" {
			name = "default"
		}
		status := dim("(state unknown — image missing)")
		if haveImage {
			out, err := run(cfg, "run", "--rm", "-v", vol+":/v", cfg.ImageTag(),
				"bash", "-c", "jq -r '.oauthAccount.emailAddress // empty' /v/.claude.json 2>/dev/null")
			if err == nil && out != "" {
				status = green(out)
			} else {
				status = yellow("no app state yet (created on first session)")
			}
		}
		marker := "  "
		if name == cfg.EnvName() {
			marker = cyan("▸ ")
		}
		fmt.Printf("%s%s  %s  %s\n", marker, bold(pad(name, 12)), dim(pad(vol, 28)), status)
	}
	return nil
}

// cleanup removes sandbox images whose tag no longer matches the embedded
// assets, plus any stopped cbox session containers, so nothing hangs around.
func cleanup(cfg *Config) error {
	current := cfg.ImageTag()
	out, _ := run(cfg, "images", cfg.ImageBase, "--format", "{{.Repository}}:{{.Tag}}")
	cleaned := false
	for _, img := range strings.Split(out, "\n") {
		if img == "" || img == current {
			continue
		}
		if _, err := run(cfg, "rmi", img); err == nil {
			okLine("removed stale image %s", img)
		} else {
			warnLine("%s still in use by a running session — exit it and re-run cbox cleanup", img)
		}
		cleaned = true
	}
	if out, _ := run(cfg, "ps", "-aq", "--filter", "label=cbox", "--filter", "status=exited"); out != "" {
		ids := strings.Fields(out)
		if _, err := run(cfg, append([]string{"rm"}, ids...)...); err == nil {
			okLine("removed %d stopped session container(s)", len(ids))
			cleaned = true
		}
	}
	if !cleaned {
		fmt.Println(dim("nothing to clean"))
	}
	return nil
}

// maybeSeedEnv copies the default env's app state (.claude.json: onboarding,
// oauth account info) into a brand-new named env so `cbox @foo` works first
// try. Credentials themselves are shared by all envs via the auth volume.
func maybeSeedEnv(cfg *Config) {
	const defVol = "claude-box-config"
	if cfg.Volume() == defVol {
		return
	}
	hasState := func(vol string) bool {
		_, err := run(cfg, "run", "--rm", "-v", vol+":/v", cfg.ImageTag(),
			"bash", "-c", "test -s /v/.claude.json")
		return err == nil
	}
	if hasState(cfg.Volume()) || !hasState(defVol) {
		return
	}
	// Needs real root: fresh volumes mount root-owned, and our own
	// entrypoint always drops root to node — bypass it for the copy.
	if _, err := run(cfg, "run", "--rm", "--user", "root", "--entrypoint", "bash",
		"-v", defVol+":/src:ro", "-v", cfg.Volume()+":/dst", cfg.ImageTag(),
		"-c", "cp /src/.claude.json /dst/ && chown -R node:node /dst"); err == nil {
		okLine("env %q inherited app state from default (login is shared anyway)", cfg.EnvName())
	}
}

func ensureGitconfigTemplate(cfg *Config) {
	if fileExists(cfg.GitConfig) {
		return
	}
	if err := os.MkdirAll(filepath.Dir(cfg.GitConfig), 0o755); err != nil {
		return
	}
	name := gitConfigValue("user.name", "claude-agent")
	email := gitConfigValue("user.email", "agent@example.invalid")
	content := fmt.Sprintf("[user]\n\tname = %s (agent)\n\temail = %s\n", name, email)
	if os.WriteFile(cfg.GitConfig, []byte(content), 0o644) == nil {
		warnLine("created agent gitconfig template: %s — edit it", cfg.GitConfig)
	}
}

func gitConfigValue(key, def string) string {
	out, err := exec.Command("git", "config", "--get", key).Output()
	if v := strings.TrimSpace(string(out)); err == nil && v != "" {
		return v
	}
	return def
}

func usage() {
	fmt.Print(bold("cbox") + dim(" — Claude Code in a throwaway Docker sandbox\n\n") +
		bold("managed sessions") + dim("  (survive closing the terminal; tmux-backed)\n") +
		"  cbox new [name] [args...]    start + attach a named session here\n" +
		"  cbox ls                      list sessions (attached/detached, container state)\n" +
		"  cbox attach [name]           re-attach (fuzzy match; alias: a)\n" +
		"  cbox kill <name|--all>       close session and its container\n\n" +
		bold("direct\n") +
		"  cbox [@env] [args...]        interactive claude in the current dir\n" +
		"  cbox [@env] yolo [args...]   --dangerously-skip-permissions (net still filtered)\n" +
		"  cbox [@env] shell [args...]  bash inside the sandbox\n" +
		"  cbox [@env] login            login flow for that env\n" +
		"  cbox ui                      interactive dashboard: sessions, projects, envs\n" +
		"  cbox ps                      running sessions\n" +
		"  cbox envs                    environments and their login state\n" +
		"  cbox doctor                  check the whole setup\n" +
		"  cbox build | update          (re)build the image (update pulls latest + cleanup)\n" +
		"  cbox cleanup                 remove stale images and dead session containers\n" +
		"  cbox version | help\n\n" +
		bold("config") + dim("  (env vars, or a sourced-style .cbox.conf in the project root)\n") +
		"  CBOX_NET=open|allowlist|full   CBOX_SSH=key|agent|none   CBOX_ENV=<name>\n" +
		"  CBOX_PORTS=\"3000 5173\"         CBOX_MOUNTS=\"src:dst:ro\"  CBOX_MEMORY=8g\n" +
		"  CBOX_ALLOWED_DOMAINS=a,b       CBOX_RUNTIME=docker|podman\n")
}
