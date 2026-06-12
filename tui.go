package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// uiAction is what the dashboard hands back to main() for execution after
// the TUI exits (interactive things must replace the process).
type uiAction struct {
	kind string // "attach" | "shell" | "project" | "env-session" | "new"
	arg  string
	name string // session name for "new"
}

const (
	tabSessions = iota
	tabProjects
	tabEnvs
)

var (
	accent       = lipgloss.AdaptiveColor{Light: "#7D56F4", Dark: "#A78BFA"}
	subtle       = lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"}
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(accent)
	tabActive    = lipgloss.NewStyle().Bold(true).Foreground(accent).Underline(true).Padding(0, 2)
	tabInactive  = lipgloss.NewStyle().Foreground(subtle).Padding(0, 2)
	footerStyle  = lipgloss.NewStyle().Foreground(subtle)
	statusStyle  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#B58900", Dark: "#FFCC66"})
	accountStyle = lipgloss.NewStyle().Foreground(subtle)
	frameStyle   = lipgloss.NewStyle().Padding(0, 1)
)

// ---- items --------------------------------------------------------------------

type sessionItem struct {
	kind     string // "managed" (tmux) | "container" (unmanaged direct run)
	id       string // tmux session name or container name
	path     string
	attached bool
	cont     containerInfo
	t, d     string
}

func (i sessionItem) Title() string       { return i.t }
func (i sessionItem) Description() string { return i.d }
func (i sessionItem) FilterValue() string { return i.t + " " + i.d + " " + i.path }

func collapseHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

type projectItem struct{ path string }

func (i projectItem) Title() string       { return filepath.Base(i.path) }
func (i projectItem) Description() string { return i.path }
func (i projectItem) FilterValue() string { return i.path }

type envItem struct{ name, vol, email string }

func (i envItem) Title() string       { return i.name }
func (i envItem) Description() string { return i.vol + " · " + i.email }
func (i envItem) FilterValue() string { return i.name }

// ---- async messages -------------------------------------------------------------

type accountMsg string
type envEmailMsg struct{ vol, email string }
type sessionsReloadedMsg []list.Item
type tickMsg time.Time
type previewMsg struct{ key, content, info string }

func tickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// ---- model -----------------------------------------------------------------------

type uiModel struct {
	cfg         *Config
	lists       [3]list.Model
	active      int
	account     string
	width       int
	height      int
	pending     string // container name awaiting stop confirmation
	status      string
	action      *uiAction
	preview     string
	previewInfo string // git line for the context card
	naming      bool   // name-input modal open
	namingDir   string
	input       textinput.Model
}

// previewKey identifies the current selection, so stale async previews can
// be discarded.
func (m uiModel) previewKey() string {
	switch m.active {
	case tabSessions:
		if it, ok := m.lists[tabSessions].SelectedItem().(sessionItem); ok {
			return "s:" + it.kind + ":" + it.id
		}
	case tabProjects:
		if it, ok := m.lists[tabProjects].SelectedItem().(projectItem); ok {
			return "p:" + it.path
		}
	}
	return ""
}

func (m uiModel) loadPreviewCmd() tea.Cmd {
	key := m.previewKey()
	if key == "" {
		return nil
	}
	height := m.height - 11
	var itemPath string
	if m.active == tabSessions {
		if it, ok := m.lists[tabSessions].SelectedItem().(sessionItem); ok {
			itemPath = it.path
		}
	}
	return func() tea.Msg {
		var content, info string
		switch {
		case strings.HasPrefix(key, "s:managed:"):
			name := strings.TrimPrefix(key, "s:managed:")
			// capture-pane takes pane targets and rejects the "=" exact-match
			// session prefix; full names are unique enough here.
			out, err := exec.Command("tmux", "capture-pane", "-p", "-t", name).Output()
			if err != nil {
				content = "(no screen yet)"
			} else {
				content = lastNonEmptyLines(string(out), height)
			}
			info = gitLine(itemPath)
		case strings.HasPrefix(key, "s:container:"):
			content = "unmanaged direct run — it lives in the terminal that started it."
			info = "—"
		case strings.HasPrefix(key, "p:"):
			path := strings.TrimPrefix(key, "p:")
			content = gitSummary(path, height)
		}
		return previewMsg{key: key, content: content, info: info}
	}
}

// gitLine is the one-line git summary for the context card.
func gitLine(path string) string {
	if path == "" {
		return "—"
	}
	br, err := exec.Command("git", "-C", path, "branch", "--show-current").Output()
	if err != nil {
		return "not a git repository"
	}
	branch := strings.TrimSpace(string(br))
	if branch == "" {
		branch = "(detached HEAD)"
	}
	st, _ := exec.Command("git", "-C", path, "status", "--porcelain").Output()
	n := 0
	for _, l := range strings.Split(strings.TrimSpace(string(st)), "\n") {
		if l != "" {
			n++
		}
	}
	if n == 0 {
		return branch + " · clean"
	}
	return fmt.Sprintf("%s · %d file(s) modified", branch, n)
}

func lastNonEmptyLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func gitSummary(path string, height int) string {
	branch, err := exec.Command("git", "-C", path, "status", "--short", "--branch").Output()
	if err != nil {
		return "not a git repository"
	}
	log, _ := exec.Command("git", "-C", path, "log", "--oneline", "-8").Output()
	out := strings.TrimSpace(string(branch)) + "\n\nrecent commits:\n" + strings.TrimSpace(string(log))
	return lastNonEmptyLines(out, 0)
}

func newDelegate() list.DefaultDelegate {
	d := list.NewDefaultDelegate()
	d.Styles.SelectedTitle = d.Styles.SelectedTitle.Foreground(accent).BorderLeftForeground(accent)
	d.Styles.SelectedDesc = d.Styles.SelectedDesc.Foreground(subtle).BorderLeftForeground(accent)
	return d
}

func newList(items []list.Item) list.Model {
	l := list.New(items, newDelegate(), 0, 0)
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.DisableQuitKeybindings()
	return l
}

func runTUI(cfg *Config) (*uiAction, error) {
	m := uiModel{cfg: cfg, account: "…"}
	m.lists[tabSessions] = newList(loadSessionItems(cfg))
	m.lists[tabProjects] = newList(loadProjectItems())
	m.lists[tabEnvs] = newList(loadEnvItems(cfg))

	p := tea.NewProgram(m, tea.WithAltScreen())
	out, err := p.Run()
	if err != nil {
		return nil, err
	}
	if fm, ok := out.(uiModel); ok {
		return fm.action, nil
	}
	return nil, nil
}

func (m uiModel) Init() tea.Cmd {
	cmds := []tea.Cmd{loadAccountCmd(m.cfg), tickCmd()}
	for _, it := range m.lists[tabEnvs].Items() {
		if e, ok := it.(envItem); ok {
			cmds = append(cmds, loadEnvEmailCmd(m.cfg, e.vol))
		}
	}
	return tea.Batch(cmds...)
}

func (m *uiModel) resize() {
	listW := m.width - 4
	if m.active == tabSessions || m.active == tabProjects {
		listW = m.width * 2 / 5 // left column; preview pane takes the rest
	}
	for i := range m.lists {
		m.lists[i].SetSize(listW, m.height-7)
	}
}

func (m uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil

	case tickMsg:
		return m, tea.Batch(tickCmd(), m.loadPreviewCmd())

	case previewMsg:
		if msg.key == m.previewKey() {
			m.preview = msg.content
			m.previewInfo = msg.info
		}
		return m, nil

	case accountMsg:
		m.account = string(msg)
		return m, nil

	case envEmailMsg:
		items := m.lists[tabEnvs].Items()
		for idx, it := range items {
			if e, ok := it.(envItem); ok && e.vol == msg.vol {
				e.email = msg.email
				items[idx] = e
			}
		}
		m.lists[tabEnvs].SetItems(items)
		return m, nil

	case sessionsReloadedMsg:
		m.lists[tabSessions].SetItems([]list.Item(msg))
		return m, nil

	case tea.KeyMsg:
		// Name-input modal captures everything until enter/esc.
		if m.naming {
			switch msg.String() {
			case "esc":
				m.naming = false
				m.status = ""
				return m, nil
			case "enter":
				m.naming = false
				m.action = &uiAction{kind: "new", arg: m.namingDir,
					name: strings.TrimSpace(m.input.Value())}
				return m, tea.Quit
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		// While the fuzzy filter is open, every key belongs to the list.
		if m.lists[m.active].FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "tab", "right":
			m.active = (m.active + 1) % 3
			m.pending, m.status, m.preview = "", "", ""
			m.resize()
			return m, m.loadPreviewCmd()
		case "shift+tab", "left":
			m.active = (m.active + 2) % 3
			m.pending, m.status, m.preview = "", "", ""
			m.resize()
			return m, m.loadPreviewCmd()
		case "1", "2", "3":
			m.active = int(msg.String()[0] - '1')
			m.pending, m.status, m.preview = "", "", ""
			m.resize()
			return m, m.loadPreviewCmd()

		case "r":
			if m.active == tabSessions {
				m.status = ""
				return m, reloadSessionsCmd(m.cfg)
			}

		case "n":
			dir := ""
			switch m.active {
			case tabSessions:
				if it, ok := m.lists[tabSessions].SelectedItem().(sessionItem); ok && it.path != "" {
					dir = it.path
				}
			case tabProjects:
				if it, ok := m.lists[tabProjects].SelectedItem().(projectItem); ok {
					dir = it.path
				}
			}
			if dir == "" {
				if pwd, err := os.Getwd(); err == nil {
					dir = pwd
				}
			}
			m.naming = true
			m.namingDir = dir
			m.input = textinput.New()
			m.input.Placeholder = "empty = auto (branch name)"
			m.input.Focus()
			m.input.CharLimit = 40
			m.input.Width = 36
			return m, textinput.Blink

		case "s":
			if m.active == tabSessions {
				if it, ok := m.lists[tabSessions].SelectedItem().(sessionItem); ok && it.cont.Name != "" {
					m.action = &uiAction{kind: "shell", arg: it.cont.Name}
					return m, tea.Quit
				}
				m.status = "no container yet for this session"
				return m, nil
			}

		case "y":
			if m.pending != "" {
				parts := strings.SplitN(m.pending, "\x00", 2)
				m.pending = ""
				m.status = "closing " + parts[1] + "…"
				return m, stopSessionCmd(m.cfg, parts[0], parts[1])
			}

		case "x":
			if m.active == tabSessions {
				if it, ok := m.lists[tabSessions].SelectedItem().(sessionItem); ok {
					m.pending = it.kind + "\x00" + it.id
					m.status = "close " + it.t + "? press y to confirm, any key to cancel"
					return m, nil
				}
			}

		case "enter":
			switch m.active {
			case tabSessions:
				if it, ok := m.lists[tabSessions].SelectedItem().(sessionItem); ok {
					kind := "shell"
					if it.kind == "managed" {
						kind = "attach"
					}
					m.action = &uiAction{kind: kind, arg: it.id}
					return m, tea.Quit
				}
			case tabProjects:
				if it, ok := m.lists[tabProjects].SelectedItem().(projectItem); ok {
					m.action = &uiAction{kind: "project", arg: it.path}
					return m, tea.Quit
				}
			case tabEnvs:
				if it, ok := m.lists[tabEnvs].SelectedItem().(envItem); ok {
					m.action = &uiAction{kind: "env-session", arg: it.name}
					return m, tea.Quit
				}
			}
		}
		if m.pending != "" {
			m.pending, m.status = "", ""
		}
	}

	var cmd tea.Cmd
	m.lists[m.active], cmd = m.lists[m.active].Update(msg)
	return m, cmd
}

func (m uiModel) View() string {
	header := titleStyle.Render("◆ claudebox") + "  " +
		accountStyle.Render("login: "+m.account+" · shared by all envs")

	labels := []string{"1 Sessions", "2 Projects", "3 Envs"}
	var tabs []string
	for i, l := range labels {
		if i == m.active {
			tabs = append(tabs, tabActive.Render(l))
		} else {
			tabs = append(tabs, tabInactive.Render(l))
		}
	}
	tabBar := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)

	hints := map[int]string{
		tabSessions: "enter attach · s shell · n new · x close · r refresh · / filter · tab/1-3 switch · q quit",
		tabProjects: "enter start session there · n new with name · / filter · tab/1-3 switch · q quit",
		tabEnvs:     "enter start session here with env · / filter · tab/1-3 switch · q quit",
	}[m.active]
	footer := footerStyle.Render(hints)
	if m.status != "" {
		footer = statusStyle.Render(m.status)
	}

	body := m.lists[m.active].View()
	if m.active == tabSessions || m.active == tabProjects {
		pw := m.width - m.width*2/5 - 7
		ph := m.height - 9
		if pw > 10 && ph > 3 {
			var inner string
			if m.active == tabSessions {
				inner = m.sessionPane(pw-2, ph)
			} else {
				inner = titleStyle.Render("git") + "\n" + truncateLines(m.preview, pw-2, ph-1)
			}
			pane := lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).BorderForeground(subtle).
				Width(pw).Height(ph).Padding(0, 1).
				Render(inner)
			body = lipgloss.JoinHorizontal(lipgloss.Top, body, " ", pane)
		}
	}

	if m.naming {
		footer = statusStyle.Render("new session in "+collapseHome(m.namingDir)) +
			"\n name: " + m.input.View() + footerStyle.Render("   enter create · esc cancel")
	}

	return frameStyle.Render(
		header + "\n" + tabBar + "\n\n" + body + "\n" + footer,
	)
}

// sessionPane renders the context card + live screen for the selection.
func (m uiModel) sessionPane(w, h int) string {
	it, ok := m.lists[tabSessions].SelectedItem().(sessionItem)
	if !ok {
		return dim("no sessions — press n to start one")
	}
	state := dim("○ detached")
	switch {
	case it.kind == "container":
		state = yellow("unmanaged")
	case it.attached:
		state = green("● attached")
	}
	box := "starting…"
	if it.cont.Name != "" {
		box = it.cont.Name + dim(" · up "+it.cont.Up)
	}
	info := m.previewInfo
	if info == "" {
		info = "…"
	}
	actions := footerStyle.Render("[enter] attach   [s] shell   [x] close   [n] new here")
	if it.kind == "container" {
		actions = footerStyle.Render("[enter] shell   [x] stop container")
	}
	card := lipgloss.NewStyle().Bold(true).Foreground(accent).Render(it.t) + "  " + state + "\n" +
		dim("dir  ") + collapseHome(it.path) + "\n" +
		dim("git  ") + info + "\n" +
		dim("box  ") + box + dim(" · env "+it.cont.Env) + "\n" +
		actions
	sep := dim(strings.Repeat("─", w))
	screen := truncateLines(m.preview, w, h-8)
	return card + "\n" + sep + "\n" + screen
}

// truncateLines hard-trims preview content so it can never overflow the pane.
func truncateLines(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	for i, l := range lines {
		if utf8.RuneCountInString(l) > w {
			r := []rune(l)
			lines[i] = string(r[:w])
		}
	}
	return strings.Join(lines, "\n")
}

// ---- data loading -----------------------------------------------------------------

func loadSessionItems(cfg *Config) []list.Item {
	var items []sessionItem

	byCont := containersBySession(cfg)
	for _, s := range tmuxSessions() {
		project := unsafeChars.ReplaceAllString(filepath.Base(s.Path), "")
		project = strings.ReplaceAll(project, ".", "_")
		title := strings.ReplaceAll(filepath.Base(s.Path), ".", "_")
		if suffix := strings.TrimPrefix(s.Short(), project); suffix != "" && suffix != s.Short() {
			title += " › " + strings.TrimPrefix(suffix, "-")
		}
		state := "○ detached"
		if s.Attached {
			state = "● attached"
		}
		c := byCont[s.Name]
		contState := "container starting…"
		if c.Name != "" {
			contState = "up " + c.Up + " · env " + c.Env
		}
		items = append(items, sessionItem{
			kind: "managed", id: s.Name, path: s.Path,
			attached: s.Attached, cont: c,
			t: title, d: state + " · " + contState,
		})
	}

	// Unmanaged direct runs (cbox launched without `new`) — still reachable.
	out, _ := run(cfg, "ps", "--filter", "label=cbox",
		"--format", `{{.Names}}\t{{.Label "cbox.env"}}\t{{.Label "cbox.session"}}\t{{.RunningFor}}\t{{.Image}}`)
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 5 || f[2] != "" {
			continue
		}
		items = append(items, sessionItem{
			kind: "container", id: f[0], t: f[0],
			cont: containerInfo{Name: f[0], Env: f[1], Up: strings.TrimSuffix(f[3], " ago"), Image: f[4]},
			d:    "unmanaged · env " + f[1] + " · up " + strings.TrimSuffix(f[3], " ago"),
		})
	}

	// Group visually: same project's sessions end up adjacent.
	sort.Slice(items, func(a, b int) bool {
		if items[a].path != items[b].path {
			return items[a].path < items[b].path
		}
		return items[a].t < items[b].t
	})
	out2 := make([]list.Item, len(items))
	for i, it := range items {
		out2[i] = it
	}
	return out2
}

func loadProjectItems() []list.Item {
	items := []list.Item{}
	for _, p := range readProjectHistory() {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			items = append(items, projectItem{path: p})
		}
	}
	return items
}

func loadEnvItems(cfg *Config) []list.Item {
	out, _ := run(cfg, "volume", "ls", "--filter", "name=claude-box-config", "--format", "{{.Name}}")
	items := []list.Item{}
	for _, vol := range strings.Split(out, "\n") {
		if vol == "" || vol == authVolume {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(vol, "claude-box-config"), "-")
		if name == "" {
			name = "default"
		}
		items = append(items, envItem{name: name, vol: vol, email: "…"})
	}
	return items
}

func loadAccountCmd(cfg *Config) tea.Cmd {
	return func() tea.Msg {
		out, err := run(cfg, "run", "--rm", "-v", "claude-box-config:/v", cfg.ImageTag(),
			"bash", "-c", "jq -r '.oauthAccount.emailAddress // empty' /v/.claude.json 2>/dev/null")
		if err != nil || out == "" {
			return accountMsg("not logged in yet")
		}
		return accountMsg(out)
	}
}

func loadEnvEmailCmd(cfg *Config, vol string) tea.Cmd {
	return func() tea.Msg {
		out, err := run(cfg, "run", "--rm", "-v", vol+":/v", cfg.ImageTag(),
			"bash", "-c", "jq -r '.oauthAccount.emailAddress // empty' /v/.claude.json 2>/dev/null")
		if err != nil || out == "" {
			return envEmailMsg{vol: vol, email: "no app state yet"}
		}
		return envEmailMsg{vol: vol, email: out}
	}
}

func reloadSessionsCmd(cfg *Config) tea.Cmd {
	return func() tea.Msg { return sessionsReloadedMsg(loadSessionItems(cfg)) }
}

func stopSessionCmd(cfg *Config, kind, id string) tea.Cmd {
	return func() tea.Msg {
		if kind == "managed" {
			killSessionResources(cfg, id)
		} else {
			run(cfg, "stop", "-t", "3", id)
		}
		return sessionsReloadedMsg(loadSessionItems(cfg))
	}
}

// ---- recent-project history --------------------------------------------------------

func historyPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "cbox", "projects")
}

func readProjectHistory() []string {
	data, err := os.ReadFile(historyPath())
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func recordProject(pwd string) {
	entries := []string{pwd}
	for _, p := range readProjectHistory() {
		if p != pwd && len(entries) < 30 {
			entries = append(entries, p)
		}
	}
	if os.MkdirAll(filepath.Dir(historyPath()), 0o755) != nil {
		return
	}
	_ = os.WriteFile(historyPath(), []byte(strings.Join(entries, "\n")+"\n"), 0o644)
}
