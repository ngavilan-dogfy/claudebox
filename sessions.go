package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ---- per-session git worktrees ------------------------------------------------

func worktreeBase() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "cbox", "worktrees")
}

// createWorktree adds a worktree for sess on a fresh cbox/<branch> branch.
func createWorktree(repo, sess, branch string) (string, error) {
	wtdir := filepath.Join(worktreeBase(), sess)
	os.MkdirAll(worktreeBase(), 0o755)
	br := "cbox/" + branch
	var lastErr []byte
	for i := 0; i < 5; i++ {
		candidate := br
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", br, i+1)
		}
		out, err := exec.Command("git", "-C", repo, "worktree", "add", wtdir, "-b", candidate).CombinedOutput()
		if err == nil {
			os.WriteFile(filepath.Join(worktreeBase(), sess+".meta"), []byte(repo), 0o644)
			return wtdir, nil
		}
		lastErr = out
	}
	return "", fmt.Errorf("git worktree: %s", strings.TrimSpace(string(lastErr)))
}

// removeWorktree cleans up a session's worktree if it exists and is clean;
// dirty worktrees are kept (the work matters more than tidiness).
func removeWorktree(sess string) {
	meta := filepath.Join(worktreeBase(), sess+".meta")
	repoB, err := os.ReadFile(meta)
	if err != nil {
		return
	}
	wtdir := filepath.Join(worktreeBase(), sess)
	if exec.Command("git", "-C", strings.TrimSpace(string(repoB)),
		"worktree", "remove", wtdir).Run() == nil {
		os.Remove(meta)
	}
}

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
	if os.Getenv("CBOX_NO_BINDINGS") == "1" {
		return
	}
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
// With worktree, the session runs in a fresh git worktree on branch
// cbox/<name> — parallel claudes on one repo without stepping on each other.
func newSession(cfg *Config, custom string, extra []string, worktree bool) error {
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
		// Attaching is only right if it's genuinely the same project —
		// two different folders sharing a basename must not collide.
		samePath := false
		for _, s := range tmuxSessions() {
			if s.Name == sess && s.Path == pwd {
				samePath = true
			}
		}
		if explicit && samePath {
			warnLine("session %s already exists — attaching", sess)
			return attachSession(sess)
		}
		base := sess
		for i := 2; i < 100 && tmuxHas(sess); i++ {
			sess = fmt.Sprintf("%s-%d", base, i)
		}
	}
	workdir := pwd
	if worktree {
		if _, err := os.Stat(filepath.Join(pwd, ".git")); err != nil {
			return fmt.Errorf("worktree mode needs a git repository at %s", pwd)
		}
		branch := custom
		if branch == "" {
			branch = "task"
		}
		wtdir, err := createWorktree(pwd, sess, unsafeChars.ReplaceAllString(branch, "-"))
		if err != nil {
			return err
		}
		workdir = wtdir
		okLine("worktree ready: %s", collapseHome(wtdir))
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Env via command prefix, not tmux's -e flag: -e needs tmux >= 3.2
	// (Ubuntu 20.04 / Debian 11 ship older). _sessionrun wraps claude so
	// exiting it offers restart/dashboard/close instead of dying abruptly.
	inner := ""
	if cfg.Env != "" {
		inner = fmt.Sprintf("CBOX_ENV=%q ", cfg.Env)
	}
	inner += fmt.Sprintf("CBOX_SESSION=%q %q _sessionrun", sess, exe)
	for _, a := range extra {
		inner += fmt.Sprintf(" %q", a)
	}
	args := []string{"new-session", "-d", "-s", sess, "-c", workdir, inner}
	if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux: %s", strings.TrimSpace(string(out)))
	}
	ensureTmuxBindings()
	okLine("session %s started in %s", bold(sess), collapseHome(workdir))
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
		badge := dim(pad("", 12))
		switch readAgentState(s.Name) {
		case "working":
			badge = yellow(pad("⚙ working", 12))
		case "ready":
			badge = green(pad("✓ ready", 12))
		case "attention":
			badge = red(pad("● needs you", 12))
		}
		fmt.Printf("  %s  %s %s %s · %s\n", dot, badge, bold(pad(s.Short(), 24)), dim(pad(s.Path, 36)), cont)
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
	os.RemoveAll(agentStateDir(name))
	removeWorktree(name)
}

// notifyHost sends a native desktop notification (best effort).
func notifyHost(title, msg string) {
	if _, err := exec.LookPath("osascript"); err == nil {
		exec.Command("osascript", "-e",
			fmt.Sprintf("display notification %q with title %q sound name \"Glass\"", msg, title)).Run()
		return
	}
	if _, err := exec.LookPath("notify-send"); err == nil {
		exec.Command("notify-send", title, msg).Run()
	}
}

func sessionAttachedNow(name string) bool {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", "="+name, "#{session_attached}").Output()
	return err == nil && strings.TrimSpace(string(out)) != "0"
}

// watchAndNotify pings the host when a DETACHED session's agent finishes or
// needs input — so backgrounded claudes never wait silently.
func watchAndNotify(sess string, stop <-chan struct{}) {
	short := strings.TrimPrefix(sess, tmuxPrefix)
	last := ""
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			st := readAgentState(sess)
			if st == last {
				continue
			}
			prev := last
			last = st
			if prev == "" || sessionAttachedNow(sess) {
				continue
			}
			switch st {
			case "ready":
				notifyHost("cbox · "+short, "claude finished — ready for you")
			case "attention":
				notifyHost("cbox · "+short, "claude needs your input")
			}
		}
	}
}

// exitMenu shows a tiny key menu after claude exits inside a managed
// session, instead of slamming the user back to a dead terminal.
type exitModel struct{ choice string }

func (e exitModel) Init() tea.Cmd { return nil }
func (e exitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "r":
			e.choice = "r"
		case "u", "d":
			e.choice = "u"
		default:
			e.choice = "q"
		}
		return e, tea.Quit
	}
	return e, nil
}
func (e exitModel) View() string {
	return "\n  " + titleStyle.Render("claude exited") + "  " +
		descStyle.Render("— this sandbox session is still yours") + "\n\n  " +
		hintBar("r", "restart claude here", "u", "dashboard", "q", "close session") + "\n"
}

func exitMenu() string {
	out, err := tea.NewProgram(exitModel{}).Run()
	if err != nil {
		return "q"
	}
	if em, ok := out.(exitModel); ok {
		return em.choice
	}
	return "q"
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
