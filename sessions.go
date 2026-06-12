package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Managed sessions: each one is a named tmux session running a cbox claude
// session. They survive closing the terminal, you can attach from anywhere,
// detach with Ctrl+b d, and navigate them via `cbox ls/attach/kill` or the
// dashboard. The container is tied back via the cbox.session label.

const tmuxPrefix = "cbox-"

type managedSession struct {
	Name     string // full tmux name (cbox-...)
	Attached bool
	Path     string
	Created  string
}

func (s managedSession) Short() string { return strings.TrimPrefix(s.Name, tmuxPrefix) }

func tmuxNeeded() error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux is required for managed sessions — macOS: brew install tmux · debian/ubuntu: sudo apt install tmux")
	}
	return nil
}

func insideTmux() bool { return os.Getenv("TMUX") != "" }

func tmuxSessions() []managedSession {
	out, err := exec.Command("tmux", "list-sessions", "-F",
		"#{session_name}\t#{session_attached}\t#{session_path}\t#{t:session_created}").Output()
	if err != nil {
		return nil // no tmux server running
	}
	var ss []managedSession
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 4 || !strings.HasPrefix(f[0], tmuxPrefix) {
			continue
		}
		ss = append(ss, managedSession{Name: f[0], Attached: f[1] != "0", Path: f[2], Created: f[3]})
	}
	return ss
}

func tmuxHas(name string) bool {
	return exec.Command("tmux", "has-session", "-t", "="+name).Run() == nil
}

// ensureTmuxBindings installs cbox's navigation keys on the tmux server
// (idempotent; lives for the server's lifetime):
//
//	prefix+Tab     dashboard as a floating popup over the current session
//	prefix+Ctrl-n  next cbox session
//	prefix+Ctrl-p  previous cbox session
func ensureTmuxBindings() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	exec.Command("tmux", "bind-key", "Tab",
		"display-popup", "-E", "-w", "85%", "-h", "75%", exe+" ui").Run()
	exec.Command("tmux", "bind-key", "C-n", "run-shell",
		fmt.Sprintf("%q _cycle next '#{client_tty}' '#{session_name}'", exe)).Run()
	exec.Command("tmux", "bind-key", "C-p", "run-shell",
		fmt.Sprintf("%q _cycle prev '#{client_tty}' '#{session_name}'", exe)).Run()
}

// cycleSession jumps the given client to the next/prev cbox session.
func cycleSession(dir, clientTTY, current string) error {
	ss := tmuxSessions()
	if len(ss) == 0 {
		return nil
	}
	idx := -1
	for i, s := range ss {
		if s.Name == current {
			idx = i
		}
	}
	target := ss[0].Name
	switch {
	case idx >= 0 && dir == "next":
		target = ss[(idx+1)%len(ss)].Name
	case idx >= 0 && dir == "prev":
		target = ss[(idx+len(ss)-1)%len(ss)].Name
	}
	args := []string{"switch-client", "-t", "=" + target}
	if clientTTY != "" {
		args = append(args, "-c", clientTTY)
	}
	return exec.Command("tmux", args...).Run()
}

// attachSession hands the terminal over to the tmux session — switch-client
// when already inside tmux, otherwise exec replaces cbox with tmux.
func attachSession(name string) error {
	ensureTmuxBindings()
	path, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	if insideTmux() {
		if exec.Command("tmux", "switch-client", "-t", "="+name).Run() == nil {
			return nil
		}
		// No switchable client (scripted pane, detached context): fall back
		// to a nested attach with TMUX cleared.
		env := []string{}
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "TMUX=") {
				env = append(env, e)
			}
		}
		return syscall.Exec(path, []string{"tmux", "attach-session", "-t", "=" + name}, env)
	}
	return syscall.Exec(path, []string{"tmux", "attach-session", "-t", "=" + name}, os.Environ())
}

func sessionNameFor(pwd, custom string) string {
	name := tmuxPrefix + unsafeChars.ReplaceAllString(filepath.Base(pwd), "")
	if custom != "" {
		name += "-" + unsafeChars.ReplaceAllString(custom, "")
	}
	return strings.ReplaceAll(name, ".", "_")
}

// autoSessionName derives a meaningful default: the git branch when it's
// not main/master (so "sprout-game › fix-login"), nothing otherwise.
func autoSessionName(pwd string) string {
	out, _ := exec.Command("git", "-C", pwd, "branch", "--show-current").Output()
	br := strings.TrimSpace(string(out))
	if br == "main" || br == "master" {
		return ""
	}
	return br
}

// newSession creates a detached managed session in the current directory and
// attaches to it (when on a terminal). extra args go straight to claude.
func newSession(cfg *Config, custom string, extra []string) error {
	if err := tmuxNeeded(); err != nil {
		return err
	}
	if err := guardHome(cfg); err != nil {
		return err
	}
	pwd, _ := os.Getwd()
	explicit := custom != ""
	if !explicit {
		custom = autoSessionName(pwd)
	}
	sess := sessionNameFor(pwd, custom)
	if tmuxHas(sess) {
		if explicit {
			warnLine("session %s already exists — attaching", sess)
			return attachSession(sess)
		}
		// Auto-named and taken: spin up a parallel sibling instead.
		base := sess
		for i := 2; i < 100 && tmuxHas(sess); i++ {
			sess = fmt.Sprintf("%s-%d", base, i)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Env via command prefix, not tmux's -e flag: -e needs tmux >= 3.2
	// (Ubuntu 20.04 / Debian 11 ship older).
	inner := fmt.Sprintf("CBOX_SESSION=%q exec %q", sess, exe)
	if cfg.Env != "" {
		inner += fmt.Sprintf(" %q", "@"+cfg.Env)
	}
	for _, a := range extra {
		inner += fmt.Sprintf(" %q", a)
	}
	args := []string{"new-session", "-d", "-s", sess, "-c", pwd, inner}
	if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux: %s", strings.TrimSpace(string(out)))
	}
	ensureTmuxBindings()
	okLine("session %s started in %s", bold(sess), pwd)
	fmt.Println(dim("   attach: cbox attach " + strings.TrimPrefix(sess, tmuxPrefix) +
		" · inside: Ctrl+b d detach · Ctrl+b Tab dashboard · Ctrl+b C-n/C-p next/prev"))
	if isTTY(os.Stdin) && isTTY(os.Stdout) {
		return attachSession(sess)
	}
	return nil
}

type containerInfo struct {
	Name, Env, Up, Image string
}

// containersBySession joins containers to their managed session by label.
func containersBySession(cfg *Config) map[string]containerInfo {
	out, _ := run(cfg, "ps", "--filter", "label=cbox",
		"--format", `{{.Label "cbox.session"}}\t{{.Names}}\t{{.Label "cbox.env"}}\t{{.RunningFor}}\t{{.Image}}`)
	m := map[string]containerInfo{}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Split(line, "\t"); len(f) == 5 && f[0] != "" {
			m[f[0]] = containerInfo{
				Name: f[1], Env: f[2],
				Up: strings.TrimSuffix(f[3], " ago"), Image: f[4],
			}
		}
	}
	return m
}

func lsSessions(cfg *Config) error {
	if err := tmuxNeeded(); err != nil {
		return err
	}
	ss := tmuxSessions()
	if len(ss) == 0 {
		fmt.Println(dim("no managed sessions — start one: cbox new [name]"))
		return nil
	}
	byCont := containersBySession(cfg)
	for _, s := range ss {
		dot := dim("○ detached")
		if s.Attached {
			dot = green("● attached")
		}
		cont := dim("container starting…")
		if c, ok := byCont[s.Name]; ok {
			cont = "up " + c.Up
		}
		fmt.Printf("  %s  %s  %s · %s\n", dot, bold(pad(s.Short(), 24)), dim(pad(s.Path, 36)), cont)
	}
	fmt.Println(dim("\n  attach: cbox attach <name> · close: cbox kill <name> · dashboard: cbox ui"))
	return nil
}

func resolveSession(arg string) (string, error) {
	ss := tmuxSessions()
	if len(ss) == 0 {
		return "", fmt.Errorf("no managed sessions running — start one: cbox new")
	}
	var matches []managedSession
	for _, s := range ss {
		if arg == "" || s.Name == arg || s.Short() == arg || strings.Contains(s.Short(), arg) {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no session matches %q — see: cbox ls", arg)
	case 1:
		return matches[0].Name, nil
	default:
		var names []string
		for _, m := range matches {
			names = append(names, m.Short())
		}
		return "", fmt.Errorf("ambiguous (%s) — be more specific or use: cbox ui", strings.Join(names, ", "))
	}
}

func attachCmd(cfg *Config, arg string) error {
	if err := tmuxNeeded(); err != nil {
		return err
	}
	name, err := resolveSession(arg)
	if err != nil {
		return err
	}
	return attachSession(name)
}

// killSessionResources tears down the tmux session and any container it owns.
func killSessionResources(cfg *Config, name string) {
	exec.Command("tmux", "kill-session", "-t", "="+name).Run()
	if out, _ := run(cfg, "ps", "-q", "--filter", "label=cbox.session="+name); out != "" {
		run(cfg, append([]string{"stop", "-t", "3"}, strings.Fields(out)...)...)
	}
}

func killCmd(cfg *Config, arg string) error {
	if err := tmuxNeeded(); err != nil {
		return err
	}
	if arg == "--all" {
		ss := tmuxSessions()
		if len(ss) == 0 {
			fmt.Println(dim("nothing to kill"))
			return nil
		}
		for _, s := range ss {
			killSessionResources(cfg, s.Name)
			okLine("closed %s", s.Short())
		}
		return nil
	}
	if arg == "" {
		return fmt.Errorf("usage: cbox kill <name|--all> — see: cbox ls")
	}
	name, err := resolveSession(arg)
	if err != nil {
		return err
	}
	killSessionResources(cfg, name)
	okLine("closed %s", strings.TrimPrefix(name, tmuxPrefix))
	return nil
}
