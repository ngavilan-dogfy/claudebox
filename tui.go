package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// uiAction is what the dashboard hands back to main() for execution after
// the TUI exits (interactive things must replace the process).
type uiAction struct {
	kind string // "shell" | "project" | "env-session"
	arg  string
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

type sessionItem struct{ name, env, uptime, status, image string }

func (i sessionItem) Title() string { return i.name }
func (i sessionItem) Description() string {
	return fmt.Sprintf("env %s · up %s · %s", i.env, i.uptime, i.image)
}
func (i sessionItem) FilterValue() string { return i.name + " " + i.env }

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

// ---- model -----------------------------------------------------------------------

type uiModel struct {
	cfg     *Config
	lists   [3]list.Model
	active  int
	account string
	width   int
	height  int
	pending string // container name awaiting stop confirmation
	status  string
	action  *uiAction
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
	cmds := []tea.Cmd{loadAccountCmd(m.cfg)}
	for _, it := range m.lists[tabEnvs].Items() {
		if e, ok := it.(envItem); ok {
			cmds = append(cmds, loadEnvEmailCmd(m.cfg, e.vol))
		}
	}
	return tea.Batch(cmds...)
}

func (m uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		for i := range m.lists {
			m.lists[i].SetSize(msg.Width-4, msg.Height-7)
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
		// While the fuzzy filter is open, every key belongs to the list.
		if m.lists[m.active].FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "tab", "right":
			m.active = (m.active + 1) % 3
			m.pending, m.status = "", ""
			return m, nil
		case "shift+tab", "left":
			m.active = (m.active + 2) % 3
			m.pending, m.status = "", ""
			return m, nil
		case "1", "2", "3":
			m.active = int(msg.String()[0] - '1')
			m.pending, m.status = "", ""
			return m, nil

		case "r":
			if m.active == tabSessions {
				m.status = ""
				return m, reloadSessionsCmd(m.cfg)
			}

		case "y":
			if m.pending != "" {
				name := m.pending
				m.pending = ""
				m.status = "stopping " + name + "…"
				return m, stopSessionCmd(m.cfg, name)
			}

		case "x":
			if m.active == tabSessions {
				if it, ok := m.lists[tabSessions].SelectedItem().(sessionItem); ok {
					m.pending = it.name
					m.status = "stop " + it.name + "? press y to confirm, any key to cancel"
					return m, nil
				}
			}

		case "enter":
			switch m.active {
			case tabSessions:
				if it, ok := m.lists[tabSessions].SelectedItem().(sessionItem); ok {
					m.action = &uiAction{kind: "shell", arg: it.name}
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
		tabSessions: "enter shell into session · x stop · r refresh · / filter · tab switch · q quit",
		tabProjects: "enter start claude there · / filter · tab switch · q quit",
		tabEnvs:     "enter start session here with env · / filter · tab switch · q quit",
	}[m.active]
	footer := footerStyle.Render(hints)
	if m.status != "" {
		footer = statusStyle.Render(m.status)
	}

	return frameStyle.Render(
		header + "\n" + tabBar + "\n\n" + m.lists[m.active].View() + "\n" + footer,
	)
}

// ---- data loading -----------------------------------------------------------------

func loadSessionItems(cfg *Config) []list.Item {
	out, err := run(cfg, "ps", "--filter", "label=cbox",
		"--format", `{{.Names}}\t{{.Label "cbox.env"}}\t{{.RunningFor}}\t{{.Status}}\t{{.Image}}`)
	items := []list.Item{}
	if err != nil || out == "" {
		return items
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 5 {
			continue
		}
		items = append(items, sessionItem{
			name: f[0], env: f[1],
			uptime: strings.TrimSuffix(f[2], " ago"),
			status: f[3], image: f[4],
		})
	}
	return items
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

func stopSessionCmd(cfg *Config, name string) tea.Cmd {
	return func() tea.Msg {
		run(cfg, "stop", "-t", "3", name)
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
