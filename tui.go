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
	accent      = lipgloss.AdaptiveColor{Light: "#7D56F4", Dark: "#A78BFA"}
	subtle      = lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"}
	greenC      = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#3FB950"}
	yellowC     = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#D29922"}
	redC        = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F85149"}
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	barStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(accent)
	tabActive   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(accent).Padding(0, 2)
	tabInactive = lipgloss.NewStyle().Foreground(subtle).Padding(0, 2)
	keyStyle    = lipgloss.NewStyle().Foreground(accent).Bold(true)
	descStyle   = lipgloss.NewStyle().Foreground(subtle)
	statusStyle = lipgloss.NewStyle().Foreground(yellowC)
	okStyle     = lipgloss.NewStyle().Foreground(greenC)
	warnStyle   = lipgloss.NewStyle().Foreground(yellowC)
	dangerStyle = lipgloss.NewStyle().Foreground(redC).Bold(true)
	frameStyle  = lipgloss.NewStyle().Padding(0, 1)
	modalStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(1, 3)
	paneStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(subtle).Padding(0, 1)
)

func hintBar(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, keyStyle.Render(pairs[i])+" "+descStyle.Render(pairs[i+1]))
	}
	return strings.Join(parts, descStyle.Render("  ·  "))
}

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

type projectItem struct{ path string }

func (i projectItem) Title() string       { return filepath.Base(i.path) }
func (i projectItem) Description() string { return collapseHome(i.path) }
func (i projectItem) FilterValue() string { return i.path }

type envItem struct{ name, vol, email string }

func (i envItem) Title() string       { return i.name }
func (i envItem) Description() string { return i.vol + " · " + i.email }
func (i envItem) FilterValue() string { return i.name }

type dirEntry struct {
	label  string
	target string
	kind   string // "use" | "up" | "dir"
}

func (d dirEntry) Title() string       { return d.label }
func (d dirEntry) Description() string { return "" }
func (d dirEntry) FilterValue() string { return d.label }

func collapseHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// ---- async messages -------------------------------------------------------------

type accountMsg string
type envEmailMsg struct{ vol, email string }
type sessionsReloadedMsg []list.Item
type tickMsg time.Time
type statsTickMsg time.Time
type previewMsg struct{ key, content, info, state string }
type statsMsg struct{ key, line string }
type pickPrevMsg struct{ target, content string }

func tickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func statsTickCmd() tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg { return statsTickMsg(t) })
}

// ---- model -----------------------------------------------------------------------

type uiModel struct {
	cfg     *Config
	lists   [3]list.Model
	active  int
	account string
	width   int
	height  int
	status  string
	action  *uiAction

	preview     string
	previewInfo string // git line for the context card
	agentState  string // "working" | "ready" | ""
	stats       string // cpu/mem line

	mode         string // "" | "name" | "pickdir" | "confirm"
	input        textinput.Model
	namingDir    string
	picker       list.Model
	pickerPath   string
	pickPreview  string
	confirmKind  string
	confirmID    string
	confirmLabel string
}

// selectedPickTarget is the directory the picker is highlighting.
func (m uiModel) selectedPickTarget() string {
	if e, ok := m.picker.SelectedItem().(dirEntry); ok {
		return e.target
	}
	return m.pickerPath
}

func (m uiModel) loadPickPreviewCmd() tea.Cmd {
	target := m.selectedPickTarget()
	max := m.height - 16
	return func() tea.Msg {
		return pickPrevMsg{target: target, content: dirPreview(target, max)}
	}
}

// enterDir moves the picker into a directory.
func (m *uiModel) enterDir(path string) {
	m.pickerPath = path
	m.picker.SetItems(loadDirItems(path))
	m.picker.ResetSelected()
	m.pickPreview = ""
}

// pickerDims returns left width, preview width and pane height.
func (m uiModel) pickerDims() (int, int, int) {
	lw := m.width * 2 / 5
	if lw < 30 {
		lw = 30
	}
	pw := m.width - lw - 9
	ph := m.height - 13
	return lw, pw, ph
}

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
	height := m.height - 14
	var itemPath string
	if m.active == tabSessions {
		if it, ok := m.lists[tabSessions].SelectedItem().(sessionItem); ok {
			itemPath = it.path
		}
	}
	return func() tea.Msg {
		var content, info, state string
		switch {
		case strings.HasPrefix(key, "s:managed:"):
			name := strings.TrimPrefix(key, "s:managed:")
			// capture-pane takes pane targets and rejects the "=" exact-match
			// session prefix; full names are unique enough here.
			out, err := exec.Command("tmux", "capture-pane", "-p", "-t", name).Output()
			if err != nil {
				content = "(no screen yet)"
			} else {
				full := string(out)
				content = lastNonEmptyLines(full, height)
				if strings.Contains(full, "esc to interrupt") {
					state = "working"
				} else if strings.Contains(full, "Claude Code") || strings.Contains(full, "❯") {
					state = "ready"
				}
			}
			info = gitLine(itemPath)
		case strings.HasPrefix(key, "s:container:"):
			content = "unmanaged direct run — it lives in the terminal that started it."
			info = "—"
		case strings.HasPrefix(key, "p:"):
			path := strings.TrimPrefix(key, "p:")
			content = gitSummary(path, height)
		}
		return previewMsg{key: key, content: content, info: info, state: state}
	}
}

func (m uiModel) loadStatsCmd() tea.Cmd {
	key := m.previewKey()
	if m.active != tabSessions || key == "" {
		return nil
	}
	it, ok := m.lists[tabSessions].SelectedItem().(sessionItem)
	if !ok || it.cont.Name == "" {
		return nil
	}
	cfg, name := m.cfg, it.cont.Name
	return func() tea.Msg {
		out, err := run(cfg, "stats", "--no-stream", "--format", "cpu {{.CPUPerc}} · mem {{.MemUsage}}", name)
		if err != nil || out == "" {
			return statsMsg{key: key, line: "—"}
		}
		return statsMsg{key: key, line: out}
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
	return strings.TrimSpace(string(branch)) + "\n\nrecent commits:\n" + strings.TrimSpace(string(log))
}

// ---- construction ------------------------------------------------------------------

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

func newPicker(path string) list.Model {
	d := newDelegate()
	d.ShowDescription = false
	d.SetSpacing(0)
	l := list.New(loadDirItems(path), d, 0, 0)
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.DisableQuitKeybindings()
	return l
}

func loadDirItems(path string) []list.Item {
	recents := map[string]bool{}
	for _, p := range readProjectHistory() {
		recents[p] = true
	}
	items := []list.Item{
		dirEntry{label: "✓ start session in this folder", target: path, kind: "use"},
	}
	if parent := filepath.Dir(path); parent != path {
		items = append(items, dirEntry{label: "⬑ ..", target: parent, kind: "up"})
	}
	entries, _ := os.ReadDir(path)
	var starred, gits, plain []dirEntry
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := filepath.Join(path, e.Name())
		isGit := false
		if st, err := os.Stat(filepath.Join(full, ".git")); err == nil && st.IsDir() {
			isGit = true
		}
		d := dirEntry{target: full, kind: "dir"}
		switch {
		case recents[full]:
			d.label = "★ " + e.Name() + " · recent"
			if isGit {
				d.label += " · git"
			}
			starred = append(starred, d)
		case isGit:
			d.label = "▸ " + e.Name() + " · git"
			gits = append(gits, d)
		default:
			d.label = "▸ " + e.Name()
			plain = append(plain, d)
		}
	}
	for _, group := range [][]dirEntry{starred, gits, plain} {
		for _, d := range group {
			items = append(items, d)
		}
	}
	return items
}

// dirPreview renders the contents of a directory for the right pane.
func dirPreview(path string, max int) string {
	head := titleStyle.Render(filepath.Base(path)) + "  " + descStyle.Render(gitLine(path))
	entries, err := os.ReadDir(path)
	if err != nil {
		return head + "\n" + descStyle.Render("(unreadable)")
	}
	var dirs, files []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, keyStyle.Render("▸ ")+e.Name())
		} else {
			files = append(files, descStyle.Render("  "+e.Name()))
		}
	}
	all := append(dirs, files...)
	if len(all) == 0 {
		all = []string{descStyle.Render("(empty)")}
	}
	more := ""
	if len(all) > max {
		more = descStyle.Render(fmt.Sprintf("… +%d more", len(all)-max))
		all = all[:max]
	}
	out := head + "\n\n" + strings.Join(all, "\n")
	if more != "" {
		out += "\n" + more
	}
	return out
}

func breadcrumb(path string) string {
	parts := strings.Split(strings.Trim(collapseHome(path), "/"), "/")
	if parts[0] == "" {
		parts[0] = "/"
	}
	styled := make([]string, len(parts))
	for i, p := range parts {
		styled[i] = descStyle.Render(p)
	}
	return strings.Join(styled, keyStyle.Render(" ❯ "))
}

func runTUI(cfg *Config) (*uiAction, error) {
	m := uiModel{cfg: cfg, account: "…"}
	m.lists[tabSessions] = newList(loadSessionItems(cfg))
	m.lists[tabProjects] = newList(loadProjectItems())
	m.lists[tabEnvs] = newList(loadEnvItems(cfg))
	home, _ := os.UserHomeDir()
	m.pickerPath = home
	m.picker = newPicker(home)

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
	cmds := []tea.Cmd{loadAccountCmd(m.cfg), tickCmd(), statsTickCmd()}
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
		listW = m.width * 2 / 5
	}
	for i := range m.lists {
		m.lists[i].SetSize(listW, m.height-8)
	}
	lw, _, ph := m.pickerDims()
	m.picker.SetSize(lw-2, ph)
}

func (m *uiModel) clearTransient() {
	m.mode, m.status, m.preview, m.previewInfo, m.agentState, m.stats = "", "", "", "", "", ""
}

// ---- update -----------------------------------------------------------------------

func (m uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil

	case tickMsg:
		switch m.mode {
		case "":
			return m, tea.Batch(tickCmd(), m.loadPreviewCmd())
		case "pickdir":
			return m, tea.Batch(tickCmd(), m.loadPickPreviewCmd())
		}
		return m, tickCmd()

	case pickPrevMsg:
		if msg.target == m.selectedPickTarget() {
			m.pickPreview = msg.content
		}
		return m, nil

	case statsTickMsg:
		if m.mode != "" {
			return m, statsTickCmd()
		}
		return m, tea.Batch(statsTickCmd(), m.loadStatsCmd())

	case previewMsg:
		if msg.key == m.previewKey() {
			m.preview = msg.content
			m.previewInfo = msg.info
			m.agentState = msg.state
		}
		return m, nil

	case statsMsg:
		if msg.key == m.previewKey() {
			m.stats = msg.line
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
		switch m.mode {
		case "confirm":
			switch msg.String() {
			case "y", "Y":
				kind, id := m.confirmKind, m.confirmID
				m.mode = ""
				m.status = "closing " + m.confirmLabel + "…"
				return m, stopSessionCmd(m.cfg, kind, id)
			default:
				m.mode, m.status = "", ""
				return m, nil
			}

		case "name":
			switch msg.String() {
			case "esc":
				m.mode = ""
				return m, nil
			case "enter":
				m.action = &uiAction{kind: "new", arg: m.namingDir,
					name: strings.TrimSpace(m.input.Value())}
				return m, tea.Quit
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd

		case "pickdir":
			if m.picker.FilterState() == list.Filtering {
				break
			}
			switch msg.String() {
			case "esc":
				m.mode = ""
				return m, nil
			case ".":
				return m.toNaming(m.pickerPath), textinput.Blink
			case "~":
				if home, err := os.UserHomeDir(); err == nil {
					m.enterDir(home)
					return m, m.loadPickPreviewCmd()
				}
			case "h", "left":
				if parent := filepath.Dir(m.pickerPath); parent != m.pickerPath {
					m.enterDir(parent)
					return m, m.loadPickPreviewCmd()
				}
			case "l", "right":
				if e, ok := m.picker.SelectedItem().(dirEntry); ok && e.kind != "use" {
					m.enterDir(e.target)
					return m, m.loadPickPreviewCmd()
				}
			case "enter":
				if e, ok := m.picker.SelectedItem().(dirEntry); ok {
					switch e.kind {
					case "use":
						return m.toNaming(e.target), textinput.Blink
					default:
						m.enterDir(e.target)
						return m, m.loadPickPreviewCmd()
					}
				}
			}
			break
		}
		if m.mode == "name" || m.mode == "pickdir" {
			break
		}

		// Plain browsing mode below. While the fuzzy filter is open, every
		// key belongs to the list.
		if m.lists[m.active].FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "tab", "right":
			m.active = (m.active + 1) % 3
			m.clearTransient()
			m.resize()
			return m, m.loadPreviewCmd()
		case "shift+tab", "left":
			m.active = (m.active + 2) % 3
			m.clearTransient()
			m.resize()
			return m, m.loadPreviewCmd()
		case "1", "2", "3":
			m.active = int(msg.String()[0] - '1')
			m.clearTransient()
			m.resize()
			return m, m.loadPreviewCmd()

		case "r":
			if m.active == tabSessions {
				m.status = ""
				return m, reloadSessionsCmd(m.cfg)
			}

		case "n":
			start := ""
			switch m.active {
			case tabSessions:
				if it, ok := m.lists[tabSessions].SelectedItem().(sessionItem); ok && it.path != "" {
					start = it.path
				}
			case tabProjects:
				if it, ok := m.lists[tabProjects].SelectedItem().(projectItem); ok {
					start = it.path
				}
			}
			if start == "" {
				if pwd, err := os.Getwd(); err == nil {
					start = pwd
				} else if home, err := os.UserHomeDir(); err == nil {
					start = home
				}
			}
			m.mode = "pickdir"
			m.pickerPath = start
			m.picker = newPicker(start)
			m.resize()
			return m, nil

		case "s":
			if m.active == tabSessions {
				if it, ok := m.lists[tabSessions].SelectedItem().(sessionItem); ok && it.cont.Name != "" {
					m.action = &uiAction{kind: "shell", arg: it.cont.Name}
					return m, tea.Quit
				}
				m.status = "no container yet for this session"
				return m, nil
			}

		case "x":
			if m.active == tabSessions {
				if it, ok := m.lists[tabSessions].SelectedItem().(sessionItem); ok {
					m.mode = "confirm"
					m.confirmKind, m.confirmID, m.confirmLabel = it.kind, it.id, it.t
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
	}

	var cmd tea.Cmd
	if m.mode == "pickdir" {
		m.picker, cmd = m.picker.Update(msg)
	} else if m.mode == "" {
		m.lists[m.active], cmd = m.lists[m.active].Update(msg)
	}
	return m, cmd
}

func (m uiModel) toNaming(dir string) uiModel {
	m.mode = "name"
	m.namingDir = dir
	m.input = textinput.New()
	m.input.Placeholder = "empty = auto (branch name)"
	m.input.Focus()
	m.input.CharLimit = 40
	m.input.Width = 36
	return m
}

// ---- view -------------------------------------------------------------------------

func (m uiModel) View() string {
	left := " ◆ claudebox "
	right := " " + m.account + " · shared login "
	gap := m.width - 2 - utf8.RuneCountInString(left) - utf8.RuneCountInString(right)
	if gap < 1 {
		gap = 1
	}
	header := barStyle.Render(left + strings.Repeat(" ", gap) + right)

	labels := []string{"1 Running claudes", "2 Folders", "3 Profiles"}
	var tabs []string
	for i, l := range labels {
		if i == m.active {
			tabs = append(tabs, tabActive.Render(l))
		} else {
			tabs = append(tabs, tabInactive.Render(l))
		}
	}
	tabBar := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)

	explain := map[int]string{
		tabSessions: "claudes alive in sandboxes right now — ● you're inside it · ○ detached: keeps running in the background",
		tabProjects: "recent folders, nothing running here — enter starts a fresh claude in one",
		tabEnvs:     "separate profiles: own history & settings per profile (the login is shared)",
	}[m.active]

	body := m.bodyView()
	footer := m.footerView()

	return frameStyle.Render(header + "\n" + tabBar + "\n" +
		descStyle.Render(" "+explain) + "\n\n" + body + "\n" + footer)
}

func (m uiModel) bodyView() string {
	bodyH := m.height - 8

	switch m.mode {
	case "pickdir":
		lw, pw, ph := m.pickerDims()
		title := titleStyle.Render("new session — pick a directory") + "   " + breadcrumb(m.pickerPath)
		left := paneStyle.Width(lw).Height(ph).Render(m.picker.View())
		right := paneStyle.Width(pw).Height(ph).Render(
			truncateLines(m.pickPreview, pw-2, ph))
		panes := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
		return lipgloss.Place(m.width-2, bodyH, lipgloss.Center, lipgloss.Center,
			title+"\n"+panes)

	case "name":
		box := modalStyle.Render(
			titleStyle.Render("new session") + "\n" +
				descStyle.Render("in "+collapseHome(m.namingDir)) + "\n\n" +
				"name: " + m.input.View())
		return lipgloss.Place(m.width-2, bodyH, lipgloss.Center, lipgloss.Center, box)

	case "confirm":
		box := modalStyle.BorderForeground(redC).Render(
			dangerStyle.Render("close "+m.confirmLabel+"?") + "\n\n" +
				descStyle.Render("session and container will be stopped") + "\n\n" +
				hintBar("y", "confirm", "esc", "cancel"))
		return lipgloss.Place(m.width-2, bodyH, lipgloss.Center, lipgloss.Center, box)
	}

	body := m.lists[m.active].View()
	if m.active == tabSessions || m.active == tabProjects {
		pw := m.width - m.width*2/5 - 7
		ph := m.height - 10
		if pw > 10 && ph > 3 {
			var inner string
			if m.active == tabSessions {
				inner = m.sessionPane(pw-2, ph)
			} else {
				inner = titleStyle.Render("git") + "\n" + truncateLines(m.preview, pw-2, ph-1)
			}
			pane := paneStyle.Width(pw).Height(ph).Render(inner)
			body = lipgloss.JoinHorizontal(lipgloss.Top, body, " ", pane)
		}
	}
	return body
}

func (m uiModel) footerView() string {
	if m.status != "" {
		return statusStyle.Render(m.status)
	}
	switch m.mode {
	case "pickdir":
		return hintBar("←/h", "up", "→/l/enter", "open", ".", "start session here", "~", "home", "/", "filter", "esc", "cancel")
	case "name":
		return hintBar("enter", "create", "esc", "back")
	case "confirm":
		return hintBar("y", "confirm", "esc", "cancel")
	}
	switch m.active {
	case tabSessions:
		return hintBar("enter", "attach", "s", "shell", "n", "new", "x", "close", "r", "refresh", "/", "filter", "tab", "switch", "q", "quit")
	case tabProjects:
		return hintBar("enter", "start session", "n", "new anywhere", "/", "filter", "tab", "switch", "q", "quit")
	default:
		return hintBar("enter", "session with env", "/", "filter", "tab", "switch", "q", "quit")
	}
}

// sessionPane renders the context card + live screen for the selection.
func (m uiModel) sessionPane(w, h int) string {
	it, ok := m.lists[tabSessions].SelectedItem().(sessionItem)
	if !ok {
		return descStyle.Render("no sessions — press ") + keyStyle.Render("n") +
			descStyle.Render(" to start one anywhere")
	}
	state := descStyle.Render("○ detached")
	switch {
	case it.kind == "container":
		state = warnStyle.Render("unmanaged")
	case it.attached:
		state = okStyle.Render("● attached")
	}
	agent := ""
	switch m.agentState {
	case "working":
		agent = "  " + warnStyle.Render("⚙ working")
	case "ready":
		agent = "  " + okStyle.Render("✓ ready for you")
	}
	box := "starting…"
	if it.cont.Name != "" {
		box = it.cont.Name + descStyle.Render(" · up "+it.cont.Up+" · env "+it.cont.Env)
	}
	info := m.previewInfo
	if info == "" {
		info = "…"
	}
	stats := m.stats
	if stats == "" {
		stats = "…"
	}
	actions := hintBar("enter", "attach", "s", "shell", "x", "close", "n", "new")
	if it.kind == "container" {
		actions = hintBar("enter", "shell", "x", "stop")
	}
	card := titleStyle.Render(it.t) + "  " + state + agent + "\n" +
		descStyle.Render("dir  ") + collapseHome(it.path) + "\n" +
		descStyle.Render("git  ") + info + "\n" +
		descStyle.Render("box  ") + box + "\n" +
		descStyle.Render("res  ") + stats + "\n" +
		actions
	sep := descStyle.Render(strings.Repeat("─", w))
	screen := truncateLines(m.preview, w, h-9)
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
