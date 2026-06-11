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
		err = buildImage(cfg, true, true)
	case "ps":
		err = passthrough(cfg, "ps", "--filter", "label=cbox",
			"--format", `table {{.Names}}\t{{.Label "cbox.env"}}\t{{.RunningFor}}\t{{.Status}}`)
	case "envs":
		err = listEnvs(cfg)
	case "doctor":
		os.Exit(doctor(cfg))
	case "version", "--version", "-V":
		fmt.Printf("cbox %s %s\n", version, dim("(image "+cfg.ImageTag()+")"))
	case "help", "-h", "--help":
		usage()
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

func session(cfg *Config, profile string, extra []string) error {
	if err := guardHome(cfg); err != nil {
		return err
	}
	if !imageExists(cfg) {
		fmt.Println(dim("first run for this image version — building the sandbox (cached afterwards)"))
		if err := buildImage(cfg, false, false); err != nil {
			return err
		}
	}
	ensureGitconfigTemplate(cfg)

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
		status := dim("(login unknown — image missing)")
		if haveImage {
			if _, err := run(cfg, "run", "--rm", "-v", vol+":/home/node/.claude",
				cfg.ImageTag(), "bash", "-c", "test -s /home/node/.claude/.credentials.json"); err == nil {
				status = green("logged in")
			} else {
				status = yellow("not logged in")
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
		bold("usage\n") +
		"  cbox [@env] [args...]        interactive claude in the current dir\n" +
		"  cbox [@env] yolo [args...]   --dangerously-skip-permissions (net still filtered)\n" +
		"  cbox [@env] shell [args...]  bash inside the sandbox\n" +
		"  cbox [@env] login            login flow for that env\n" +
		"  cbox ps                      running sessions\n" +
		"  cbox envs                    environments and their login state\n" +
		"  cbox doctor                  check the whole setup\n" +
		"  cbox build | update          (re)build the image (update pulls latest)\n" +
		"  cbox version | help\n\n" +
		bold("config") + dim("  (env vars, or a sourced-style .cbox.conf in the project root)\n") +
		"  CBOX_NET=open|allowlist|full   CBOX_SSH=key|agent|none   CBOX_ENV=<name>\n" +
		"  CBOX_PORTS=\"3000 5173\"         CBOX_MOUNTS=\"src:dst:ro\"  CBOX_MEMORY=8g\n" +
		"  CBOX_ALLOWED_DOMAINS=a,b       CBOX_RUNTIME=docker|podman\n")
}
