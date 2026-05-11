package main

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
)

// ── palette (Tokyo Night) ────────────────────────────────────────────────────

var (
	colFg      = lipgloss.Color("#c0caf5")
	colFgDark  = lipgloss.Color("#a9b1d6")
	colMutedC  = lipgloss.Color("#565f89")
	colSubtleC = lipgloss.Color("#9aa5ce")
	colBlue    = lipgloss.Color("#7aa2f7")
	colCyan    = lipgloss.Color("#7dcfff")
	colGreenC  = lipgloss.Color("#9ece6a")
	colBorderC = lipgloss.Color("#3b4261")
	colSelBGC  = lipgloss.Color("#283457")
	colPurple  = lipgloss.Color("#bb9af7")

	styleTitle    = lipgloss.NewStyle().Foreground(colBlue).Bold(true)
	styleText     = lipgloss.NewStyle().Foreground(colFgDark)
	styleMuted    = lipgloss.NewStyle().Foreground(colMutedC)
	styleSubtle   = lipgloss.NewStyle().Foreground(colSubtleC)
	styleSelected = lipgloss.NewStyle().Foreground(colFg).Background(colSelBGC).Bold(true)
	styleGreen    = lipgloss.NewStyle().Foreground(colGreenC)
	styleBold     = lipgloss.NewStyle().Foreground(colFg).Bold(true)
	styleCyan     = lipgloss.NewStyle().Foreground(colCyan)
	styleHeader   = lipgloss.NewStyle().Foreground(colPurple).Bold(true)

	borderNormal = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colBorderC)
	borderFocused = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colBlue)
)

var spinnerFrames = []string{"▱▱▱", "▰▱▱", "▰▰▱", "▱▰▰", "▱▱▰", "▱▱▱"}

// ── item ─────────────────────────────────────────────────────────────────────

type item struct {
	sourceType string // "slack" or "github"
	sourceID   string // channel ID or "owner/repo"
	name       string
	summary    string
	loading    bool
	noActivity bool
}

// ── messages ─────────────────────────────────────────────────────────────────

type refreshStartMsg struct{ sourceType, sourceID string }
type refreshDoneMsg struct {
	sourceType, sourceID, summary string
	err                           error
}
type spinnerTickMsg struct{}

// ── manage mode ──────────────────────────────────────────────────────────────

type manageStep int

const (
	manageMenu manageStep = iota
	manageAddSlackName
	manageAddSlackLookup
	manageAddGHRepo
	manageRemoveSlack
	manageRemoveGH
)

type manageState struct {
	step      manageStep
	inputA    textinput.Model
	statusMsg string
}

func newManageState() manageState {
	a := textinput.New()
	a.Prompt = ""
	a.CharLimit = 200
	return manageState{inputA: a}
}

type channelLookupDoneMsg struct {
	name string
	id   string
	err  error
}

// ── pane ─────────────────────────────────────────────────────────────────────

type pane int

const (
	paneList pane = iota
	paneContent
)

// ── model ────────────────────────────────────────────────────────────────────

type model struct {
	items     []item
	header    string
	selected  int
	listTop   int
	linkIdx   int
	focus     pane
	width     int
	height    int
	viewport  viewport.Model
	ready     bool
	spinFrame int
	cfg       *brfConfig
	managing  bool
	manage    manageState
}

func newModel(header string, items []item, cfg *brfConfig) model {
	return model{
		items:  items,
		header: header,
		focus:  paneList,
		cfg:    cfg,
		manage: newManageState(),
	}
}

// ── init ─────────────────────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	var cmds []tea.Cmd
	for _, it := range m.items {
		it := it
		cmds = append(cmds, func() tea.Msg {
			return refreshStartMsg{sourceType: it.sourceType, sourceID: it.sourceID}
		})
	}
	return tea.Batch(cmds...)
}

// ── update ───────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		if !m.ready {
			m.ready = true
		}
		m.setContent()
		return m, nil

	case spinnerTickMsg:
		m.spinFrame = (m.spinFrame + 1) % len(spinnerFrames)
		if m.anyLoading() {
			return m, spinnerCmd()
		}
		return m, nil

	case refreshStartMsg:
		idx := m.findItem(msg.sourceType, msg.sourceID)
		if idx >= 0 {
			m.items[idx].loading = true
		}
		lookback := m.cfg.Slack.LookbackHours
		sourceType := msg.sourceType
		sourceID := msg.sourceID
		cfg := m.cfg
		var fetchCmd tea.Cmd = func() tea.Msg {
			var summary string
			var err error
			if sourceType == "slack" {
				var ch channelEntry
				for _, c := range cfg.Slack.Channels {
					if c.ID == sourceID {
						ch = c
						break
					}
				}
				prompt := buildChannelPrompt(ch, lookback)
				text, ferr := runClaude(prompt,
					"--allowedTools",
					"mcp__claude_ai_Slack__slack_read_channel,mcp__claude_ai_Slack__slack_search_channels",
				)
				text = strings.ReplaceAll(text, "planningcenter.slack.com", "pco.slack.com")
				if ferr != nil {
					err = ferr
				} else {
					_, secs := parseSections(text)
					if len(secs) > 0 && secs[0].title != "NO_ACTIVITY" {
						summary = secs[0].content
					}
				}
			} else if sourceType == "github" {
				parts := strings.SplitN(sourceID, "/", 2)
				if len(parts) == 2 {
					text, ferr := fetchGithubSummary(parts[0], parts[1], lookback)
					if ferr != nil {
						err = ferr
					} else {
						_, secs := parseSections(text)
						if len(secs) > 0 {
							summary = secs[0].content
						} else {
							summary = text
						}
					}
				}
			}
			return refreshDoneMsg{sourceType: sourceType, sourceID: sourceID, summary: summary, err: err}
		}
		cmds := []tea.Cmd{fetchCmd}
		if !m.anyLoadingExcept(msg.sourceType, msg.sourceID) {
			cmds = append(cmds, spinnerCmd())
		}
		return m, tea.Batch(cmds...)

	case refreshDoneMsg:
		idx := m.findItem(msg.sourceType, msg.sourceID)
		if idx >= 0 {
			m.items[idx].loading = false
			if msg.err != nil {
				m.items[idx].summary = fmt.Sprintf("Error: %v", msg.err)
			} else if msg.summary != "" {
				m.items[idx].summary = msg.summary
				m.items[idx].noActivity = false
			} else {
				m.items[idx].noActivity = true
				m.items[idx].summary = fmt.Sprintf("No activity in the last %s.", formatLookback(m.cfg.Slack.LookbackHours))
			}
			if m.listIndexOf(idx) == m.selected {
				m.setContent()
			}
		}
		return m, nil

	case channelLookupDoneMsg:
		if msg.err != nil {
			m.manage.statusMsg = "Error: " + msg.err.Error()
			m.manage.step = manageMenu
		} else {
			m.cfg.Slack.Channels = append(m.cfg.Slack.Channels, channelEntry{ID: msg.id, Name: msg.name})
			if err := saveConfig(m.cfg); err == nil {
				m.manage.statusMsg = "Added #" + msg.name
				m.items = append(m.items, item{sourceType: "slack", sourceID: msg.id, name: msg.name, loading: true})
				m.manage.step = manageMenu
				return m, func() tea.Msg {
					return refreshStartMsg{sourceType: "slack", sourceID: msg.id}
				}
			} else {
				m.manage.statusMsg = "Error: " + err.Error()
				m.manage.step = manageMenu
			}
		}
		return m, nil

	case tea.KeyMsg:
		if m.managing {
			return m.updateManage(msg)
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab", "shift+tab":
			if m.focus == paneList {
				m.focus = paneContent
			} else {
				m.focus = paneList
			}
			return m, nil
		case "o":
			if it := m.selectedItem(); it != nil && it.summary != "" {
				if urls := extractURLs(it.summary); len(urls) > 0 {
					return m, openURLs([]string{urls[m.linkIdx]})
				}
			}
			return m, nil
		case "m":
			m.managing = true
			m.manage = newManageState()
			return m, nil
		case "r":
			var cmds []tea.Cmd
			for i := range m.items {
				m.items[i].loading = true
				it := m.items[i]
				cmds = append(cmds, func() tea.Msg {
					return refreshStartMsg{sourceType: it.sourceType, sourceID: it.sourceID}
				})
			}
			cmds = append(cmds, spinnerCmd())
			return m, tea.Batch(cmds...)
		}
		switch m.focus {
		case paneList:
			return m.updateList(msg)
		case paneContent:
			if it := m.selectedItem(); it != nil && it.summary != "" {
				urls := extractURLs(it.summary)
				if len(urls) > 0 {
					switch msg.String() {
					case "j", "down":
						if m.linkIdx < len(urls)-1 {
							m.linkIdx++
							m.setContent()
						}
						return m, nil
					case "k", "up":
						if m.linkIdx > 0 {
							m.linkIdx--
							m.setContent()
						}
						return m, nil
					}
				}
			}
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func spinnerCmd() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

func (m model) anyLoading() bool {
	for _, it := range m.items {
		if it.loading {
			return true
		}
	}
	return false
}

func (m model) anyLoadingExcept(sourceType, sourceID string) bool {
	for _, it := range m.items {
		if it.loading && !(it.sourceType == sourceType && it.sourceID == sourceID) {
			return true
		}
	}
	return false
}

func (m model) findItem(sourceType, sourceID string) int {
	for i, it := range m.items {
		if it.sourceType == sourceType && it.sourceID == sourceID {
			return i
		}
	}
	return -1
}

// listIndexOf maps a flat item index to the "visual row" index (skipping headers).
// Here we use selected as a visual index that skips headers, so we need
// to track which visual row corresponds to which item.
func (m model) listIndexOf(itemIdx int) int {
	visual := 0
	slack := 0
	github := 0
	for _, it := range m.items {
		if it.sourceType == "slack" {
			slack++
		} else {
			github++
		}
	}

	row := 0
	if slack > 0 {
		row++ // Slack header
	}
	for i, it := range m.items {
		if it.sourceType == "slack" {
			if i == itemIdx {
				return visual + row - 1
			}
			row++
		}
	}
	if github > 0 {
		row++ // GitHub header
	}
	for i, it := range m.items {
		if it.sourceType == "github" {
			if i == itemIdx {
				return visual + row - 1
			}
			row++
		}
	}
	return -1
}

// listRows returns the ordered rows: headers mixed with item indices (-1 = header).
type listRow struct {
	itemIdx int    // -1 for header
	label   string // header label if itemIdx == -1
}

func (m model) buildListRows() []listRow {
	var rows []listRow
	hasSlack, hasGH := false, false
	for _, it := range m.items {
		switch it.sourceType {
		case "slack":
			hasSlack = true
		case "github":
			hasGH = true
		}
	}
	if hasSlack {
		rows = append(rows, listRow{itemIdx: -1, label: "Slack"})
		for i, it := range m.items {
			if it.sourceType == "slack" {
				rows = append(rows, listRow{itemIdx: i})
			}
		}
	}
	if hasGH {
		rows = append(rows, listRow{itemIdx: -1, label: "GitHub Repos"})
		for i, it := range m.items {
			if it.sourceType == "github" {
				rows = append(rows, listRow{itemIdx: i})
			}
		}
	}
	return rows
}

// selectableRows returns only the row indices (into buildListRows result) that are selectable.
func selectableIndices(rows []listRow) []int {
	var out []int
	for i, r := range rows {
		if r.itemIdx >= 0 {
			out = append(out, i)
		}
	}
	return out
}

func (m model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.buildListRows()
	selectable := selectableIndices(rows)
	if len(selectable) == 0 {
		return m, nil
	}

	// m.selected tracks index into selectable slice
	prev := m.selected
	switch msg.String() {
	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
	case "down", "j":
		if m.selected < len(selectable)-1 {
			m.selected++
		}
	case "home", "g":
		m.selected = 0
	case "end", "G":
		m.selected = len(selectable) - 1
	case "enter", "l", "right":
		m.focus = paneContent
	}
	if m.selected != prev {
		m.linkIdx = 0
		m.setContent()
	}
	return m, nil
}

func (m *model) setContent() {
	rows := m.buildListRows()
	selectable := selectableIndices(rows)
	if m.selected >= len(selectable) {
		return
	}
	rowIdx := selectable[m.selected]
	itemIdx := rows[rowIdx].itemIdx
	if itemIdx < 0 || itemIdx >= len(m.items) {
		return
	}
	s := m.items[itemIdx]
	var rendered string
	if s.summary != "" {
		rendered = renderContent(s.summary, m.viewport.Width, m.linkIdx)
	} else if s.loading {
		rendered = styleMuted.Render("Fetching…")
	} else {
		rendered = styleMuted.Render(fmt.Sprintf("No activity in the last %s.", formatLookback(m.cfg.Slack.LookbackHours)))
	}
	m.viewport.SetContent(rendered)
	if s.summary != "" {
		m.viewport.YOffset = linkLineNum(s.summary, m.linkIdx, m.viewport.Height)
	} else {
		m.viewport.GotoTop()
	}
}

func (m *model) layout() {
	if m.width < 40 || m.height < 10 {
		return
	}
	statusH := 1
	bodyH := m.height - statusH
	listW := m.width / 3
	if listW < 28 {
		listW = 28
	}
	if listW > 50 {
		listW = 50
	}
	rightW := m.width - listW

	vpW := rightW - 2
	vpH := bodyH - 2 - 1
	if vpW < 1 {
		vpW = 1
	}
	if vpH < 1 {
		vpH = 1
	}

	preservedY := 0
	if m.ready {
		preservedY = m.viewport.YOffset
	}
	m.viewport = viewport.New(vpW, vpH)
	m.viewport.Style = lipgloss.NewStyle().Foreground(colFgDark)
	if m.ready {
		m.viewport.YOffset = preservedY
	}
}

// ── view ─────────────────────────────────────────────────────────────────────

func (m model) View() string {
	if !m.ready || m.width < 40 || m.height < 10 {
		return styleMuted.Render("  brf — terminal too small")
	}

	if m.managing {
		return m.viewManage()
	}

	statusH := 1
	bodyH := m.height - statusH
	listW := m.width / 3
	if listW < 28 {
		listW = 28
	}
	if listW > 50 {
		listW = 50
	}
	rightW := m.width - listW

	listBox := m.renderList(listW, bodyH)
	contentBox := m.renderContentPane(rightW, bodyH)

	body := lipgloss.JoinHorizontal(lipgloss.Top, listBox, contentBox)
	status := m.renderStatus()
	return lipgloss.JoinVertical(lipgloss.Left, body, status)
}

func (m model) renderList(outerW, outerH int) string {
	innerW := outerW - 2
	innerH := outerH - 2
	if innerH < 1 {
		innerH = 1
	}

	// titleStr := " Projects "
	// title := styleTitle.Render(titleStr)
	// if m.focus != paneList {
	// 	title = styleTitleDim.Render(titleStr)
	// }

	rows := m.buildListRows()
	selectable := selectableIndices(rows)

	visible := innerH - 1
	if visible < 1 {
		visible = 1
	}

	// Figure out which rows are visible based on selected item's row position
	var selRowIdx int
	if m.selected < len(selectable) {
		selRowIdx = selectable[m.selected]
	}

	if selRowIdx < m.listTop {
		m.listTop = selRowIdx
	}
	if selRowIdx >= m.listTop+visible {
		m.listTop = selRowIdx - visible + 1
	}
	if m.listTop < 0 {
		m.listTop = 0
	}

	var lines []string
	if len(rows) == 0 {
		lines = append(lines, styleMuted.Render("No sources configured."))
		lines = append(lines, "")
		lines = append(lines, styleSubtle.Render("Press ")+styleCyan.Render("m")+styleSubtle.Render(" to add one."))
		for len(lines) < visible {
			lines = append(lines, "")
		}
		body := strings.Join(lines, "\n")
		border := borderNormal
		if m.focus == paneList {
			border = borderFocused
		}
		return border.Width(innerW).Height(innerH).Render(body)
	}
	// lines = append(lines, title)
	for i := 0; i < visible; i++ {
		rowIdx := m.listTop + i
		if rowIdx >= len(rows) {
			lines = append(lines, "")
			continue
		}
		row := rows[rowIdx]
		if row.itemIdx < 0 {
			// section header
			label := "── " + row.label + " ──"
			lines = append(lines, styleHeader.Render(truncate(label, innerW)))
		} else {
			// find which selectable index this corresponds to
			selIdx := -1
			for si, ri := range selectable {
				if ri == rowIdx {
					selIdx = si
					break
				}
			}
			lines = append(lines, m.renderListRow(row.itemIdx, selIdx == m.selected, innerW))
		}
	}

	body := strings.Join(lines, "\n")
	border := borderNormal
	if m.focus == paneList {
		border = borderFocused
	}
	return border.Width(innerW).Height(innerH).Render(body)
}

func (m model) renderListRow(itemIdx int, selected bool, innerW int) string {
	s := m.items[itemIdx]

	var prefix string
	if s.loading {
		prefix = styleCyan.Render(spinnerFrames[m.spinFrame]) + " "
	} else if selected {
		prefix = "▸   "
	} else {
		prefix = "    "
	}

	maxW := innerW - 4
	if maxW < 1 {
		maxW = 1
	}
	name := truncate(s.name, maxW)

	if selected {
		line := prefix + name
		pad := innerW - lipgloss.Width(line)
		if pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		return styleSelected.Render(line)
	}
	if s.noActivity {
		return prefix + styleMuted.Render(name)
	}
	return prefix + styleText.Render(name)
}

func (m model) renderContentPane(outerW, outerH int) string {
	innerW := outerW - 2
	innerH := outerH - 2
	if innerH < 1 {
		innerH = 1
	}

	var body string
	if len(m.items) == 0 {
		body = styleSubtle.Render("Get started:") + "\n\n" +
			styleMuted.Render("  1. Press ") + styleCyan.Render("m") + styleMuted.Render(" to open manage mode") + "\n" +
			styleMuted.Render("  2. Add a Slack channel or GitHub repo") + "\n\n" +
			styleSubtle.Render("Requirements:") + "\n\n" +
			styleMuted.Render("  • Claude Code CLI on PATH with Slack MCP configured") + "\n" +
			styleMuted.Render("  • gh CLI on PATH and authenticated (for GitHub sources)")
	} else {
		body = m.viewport.View()
	}
	border := borderNormal
	if m.focus == paneContent {
		border = borderFocused
	}
	return border.Width(innerW).Height(innerH).Render(body)
}

func (m model) renderStatus() string {
	sep := styleMuted.Render(" · ")
	parts := []string{
		styleTitle.Render("brf"),
		sep,
		styleCyan.Render(m.header),
		sep,
		styleMuted.Render("tab ") + styleSubtle.Render("switch pane"),
		sep,
		styleMuted.Render("↑↓/jk ") + styleSubtle.Render("navigate"),
		sep,
		styleMuted.Render("o ") + styleSubtle.Render("open sources"),
		sep,
		styleMuted.Render("r ") + styleSubtle.Render("refresh"),
		sep,
		styleMuted.Render("m ") + styleSubtle.Render("manage"),
		sep,
		styleMuted.Render("q ") + styleSubtle.Render("quit"),
	}
	return strings.Join(parts, "")
}

// ── manage modal ─────────────────────────────────────────────────────────────

func (m model) viewManage() string {
	var sb strings.Builder

	switch m.manage.step {
	case manageMenu:
		sb.WriteString(styleTitle.Render("Manage sources") + "\n\n")
		sb.WriteString(styleText.Render("  a  ") + styleMuted.Render("add Slack channel") + "\n")
		sb.WriteString(styleText.Render("  s  ") + styleMuted.Render("add GitHub repo") + "\n")
		sb.WriteString(styleText.Render("  z  ") + styleMuted.Render("remove Slack channel") + "\n")
		sb.WriteString(styleText.Render("  x  ") + styleMuted.Render("remove GitHub repo") + "\n")
		sb.WriteString("\n" + styleMuted.Render("  esc  back"))
	case manageAddSlackName:
		sb.WriteString(styleTitle.Render("Add Slack channel") + "\n\n")
		sb.WriteString(styleMuted.Render("Channel name: ") + m.manage.inputA.View())
	case manageAddSlackLookup:
		sb.WriteString(styleTitle.Render("Add Slack channel") + "\n\n")
		sb.WriteString(styleMuted.Render("Looking up channel ID…"))
	case manageAddGHRepo:
		sb.WriteString(styleTitle.Render("Add GitHub repo") + "\n\n")
		sb.WriteString(styleMuted.Render("owner/repo: ") + m.manage.inputA.View())
	case manageRemoveSlack:
		sb.WriteString(styleTitle.Render("Remove Slack channel") + "\n\n")
		sb.WriteString(styleMuted.Render("Channel ID to remove: ") + m.manage.inputA.View())
		sb.WriteString("\n\n" + styleMuted.Render("Current channels:\n"))
		for _, it := range m.items {
			if it.sourceType == "slack" {
				sb.WriteString(styleMuted.Render("  " + it.sourceID + " — " + it.name + "\n"))
			}
		}
	case manageRemoveGH:
		sb.WriteString(styleTitle.Render("Remove GitHub repo") + "\n\n")
		sb.WriteString(styleMuted.Render("owner/repo to remove: ") + m.manage.inputA.View())
		sb.WriteString("\n\n" + styleMuted.Render("Current repos:\n"))
		for _, it := range m.items {
			if it.sourceType == "github" {
				sb.WriteString(styleMuted.Render("  " + it.sourceID + "\n"))
			}
		}
	}

	if m.manage.statusMsg != "" {
		sb.WriteString("\n\n" + styleGreen.Render(m.manage.statusMsg))
	}

	content := sb.String()
	w := m.width - 4
	if w > 60 {
		w = 60
	}
	return borderFocused.Width(w).Render(content)
}

func (m model) updateManage(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.manage.step {
	case manageMenu:
		switch msg.String() {
		case "esc", "q", "m":
			m.managing = false
		case "a":
			m.manage.step = manageAddSlackName
			m.manage.inputA.SetValue("")
			m.manage.inputA.Focus()
		case "s":
			m.manage.step = manageAddGHRepo
			m.manage.inputA.SetValue("")
			m.manage.inputA.Focus()
		case "z":
			m.manage.step = manageRemoveSlack
			m.manage.inputA.SetValue("")
			m.manage.inputA.Focus()
		case "x":
			m.manage.step = manageRemoveGH
			m.manage.inputA.SetValue("")
			m.manage.inputA.Focus()
		}

	case manageAddSlackName:
		switch msg.String() {
		case "esc":
			m.manage.step = manageMenu
		case "enter":
			name := strings.TrimSpace(m.manage.inputA.Value())
			if name != "" {
				m.manage.step = manageAddSlackLookup
				return m, func() tea.Msg {
					id, err := lookupChannelID(name)
					return channelLookupDoneMsg{name: name, id: id, err: err}
				}
			}
		default:
			var cmd tea.Cmd
			m.manage.inputA, cmd = m.manage.inputA.Update(msg)
			return m, cmd
		}

	case manageAddGHRepo:
		switch msg.String() {
		case "esc":
			m.manage.step = manageMenu
		case "enter":
			val := strings.TrimSpace(m.manage.inputA.Value())
			parts := strings.SplitN(val, "/", 2)
			if len(parts) == 2 {
				owner, repo := parts[0], parts[1]
				sourceID := owner + "/" + repo
				m.cfg.GitHub.Repos = append(m.cfg.GitHub.Repos, githubRepo{Owner: owner, Repo: repo})
				if err := saveConfig(m.cfg); err == nil {
					m.manage.statusMsg = "Added " + sourceID
					m.items = append(m.items, item{sourceType: "github", sourceID: sourceID, name: sourceID, loading: true})
					m.manage.step = manageMenu
					return m, func() tea.Msg {
						return refreshStartMsg{sourceType: "github", sourceID: sourceID}
					}
				} else {
					m.manage.statusMsg = "Error: " + err.Error()
					m.manage.step = manageMenu
				}
			} else {
				m.manage.statusMsg = "Format must be owner/repo"
			}
		default:
			var cmd tea.Cmd
			m.manage.inputA, cmd = m.manage.inputA.Update(msg)
			return m, cmd
		}

	case manageRemoveSlack:
		switch msg.String() {
		case "esc":
			m.manage.step = manageMenu
		case "enter":
			id := strings.TrimSpace(m.manage.inputA.Value())
			if id != "" {
				var chs []channelEntry
				for _, ch := range m.cfg.Slack.Channels {
					if ch.ID != id {
						chs = append(chs, ch)
					}
				}
				m.cfg.Slack.Channels = chs
				if err := saveConfig(m.cfg); err == nil {
					m.manage.statusMsg = "Removed " + id
					m.items = removeItem(m.items, "slack", id)
					if m.selected >= selectableCount(m.buildListRows()) {
						m.selected = max(0, selectableCount(m.buildListRows())-1)
					}
				} else {
					m.manage.statusMsg = "Error: " + err.Error()
				}
				m.manage.step = manageMenu
			}
		default:
			var cmd tea.Cmd
			m.manage.inputA, cmd = m.manage.inputA.Update(msg)
			return m, cmd
		}

	case manageRemoveGH:
		switch msg.String() {
		case "esc":
			m.manage.step = manageMenu
		case "enter":
			val := strings.TrimSpace(m.manage.inputA.Value())
			parts := strings.SplitN(val, "/", 2)
			if len(parts) == 2 {
				var repos []githubRepo
				for _, r := range m.cfg.GitHub.Repos {
					if r.Owner != parts[0] || r.Repo != parts[1] {
						repos = append(repos, r)
					}
				}
				m.cfg.GitHub.Repos = repos
				if err := saveConfig(m.cfg); err == nil {
					m.manage.statusMsg = "Removed " + val
					m.items = removeItem(m.items, "github", val)
					if m.selected >= selectableCount(m.buildListRows()) {
						m.selected = max(0, selectableCount(m.buildListRows())-1)
					}
				} else {
					m.manage.statusMsg = "Error: " + err.Error()
				}
				m.manage.step = manageMenu
			}
		default:
			var cmd tea.Cmd
			m.manage.inputA, cmd = m.manage.inputA.Update(msg)
			return m, cmd
		}

	}
	return m, nil
}

func removeItem(items []item, sourceType, sourceID string) []item {
	var out []item
	for _, it := range items {
		if !(it.sourceType == sourceType && it.sourceID == sourceID) {
			out = append(out, it)
		}
	}
	return out
}

func selectableCount(rows []listRow) int {
	n := 0
	for _, r := range rows {
		if r.itemIdx >= 0 {
			n++
		}
	}
	return n
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ── content rendering ─────────────────────────────────────────────────────────

func renderContent(text string, width, activeLink int) string {
	if width < 1 {
		width = 80
	}
	text = normalizeSummary(text)
	lines := strings.Split(text, "\n")

	// Pre-scan to find which line contains the active link.
	activeLine := -1
	linkCount := 0
	for i, line := range lines {
		linksInLine := strings.Count(line, "https://")
		if activeLink >= linkCount && activeLink < linkCount+linksInLine {
			activeLine = i
		}
		linkCount += linksInLine
	}

	var out []string
	for i, line := range lines {
		colored := colorizeContentLine(line, i == activeLine)
		out = append(out, wordwrap.String(colored, width))
	}
	return strings.Join(out, "\n")
}

var styleActiveLine = lipgloss.NewStyle().Foreground(colCyan).Bold(true)

func colorizeContentLine(line string, isActive bool) string {
	if strings.HasPrefix(line, "**") {
		end := strings.Index(line[2:], "**")
		if end >= 0 {
			label := line[2 : end+2]
			rest := line[end+4:]
			if isActive {
				return styleActiveLine.Render("**"+label+"**"+stripURLs(rest))
			}
			return styleBold.Render("**"+label+"**") + styleText.Render(stripURLs(rest))
		}
	}
	if content, ok := strings.CutPrefix(line, "- "); ok {
		if isActive {
			return styleActiveLine.Render("  • " + stripURLs(content))
		}
		return styleCyan.Render("  •") + " " + styleText.Render(stripURLs(content))
	}
	if content, ok := strings.CutPrefix(line, "  - "); ok {
		if isActive {
			return styleActiveLine.Render("    ◦ " + stripURLs(content))
		}
		return styleSubtle.Render("    ◦") + " " + styleMuted.Render(stripURLs(content))
	}
	if strings.TrimSpace(line) == "" {
		return ""
	}
	if isActive {
		return styleActiveLine.Render(stripURLs(line))
	}
	return styleText.Render(stripURLs(line))
}

// stripURLs removes https:// URLs from text, keeping surrounding text.
func stripURLs(text string) string {
	const prefix = "https://"
	idx := strings.Index(text, prefix)
	if idx < 0 {
		return text
	}
	before := text[:idx]
	rest := text[idx:]
	end := urlEnd(rest, idx > 0 && text[idx-1] == '(')
	after := rest[end:]
	before = strings.TrimSuffix(before, "(")
	after = strings.TrimPrefix(after, ")")
	return before + stripURLs(after)
}

// linkLineNum returns the viewport YOffset that centers the line containing
// the Nth URL in the raw summary text.
func linkLineNum(rawText string, idx, vpHeight int) int {
	lines := strings.Split(rawText, "\n")
	count := 0
	for i, line := range lines {
		n := strings.Count(line, "https://")
		if n > 0 && idx >= count && idx < count+n {
			offset := i - vpHeight/2
			if offset < 0 {
				return 0
			}
			return offset
		}
		count += n
	}
	return 0
}

func urlEnd(s string, trailingParen bool) int {
	for i, ch := range s {
		if ch == ' ' || ch == '\t' || ch == '\n' {
			return i
		}
		if trailingParen && ch == ')' {
			return i
		}
	}
	return len(s)
}

func extractURLs(text string) []string {
	var urls []string
	rest := text
	for {
		idx := strings.Index(rest, "https://")
		if idx < 0 {
			break
		}
		chunk := rest[idx:]
		end := urlEnd(chunk, idx > 0 && rest[idx-1] == '(')
		urls = append(urls, chunk[:end])
		rest = chunk[end:]
	}
	return urls
}

func (m model) selectedItem() *item {
	rows := m.buildListRows()
	selectable := selectableIndices(rows)
	if m.selected >= len(selectable) {
		return nil
	}
	idx := rows[selectable[m.selected]].itemIdx
	if idx < 0 || idx >= len(m.items) {
		return nil
	}
	return &m.items[idx]
}

func openURLs(urls []string) tea.Cmd {
	return func() tea.Msg {
		for _, u := range urls {
			exec.Command("open", u).Start() //nolint
		}
		return nil
	}
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	if w <= 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}

func runTUI(header string, items []item, cfg *brfConfig) error {
	p := tea.NewProgram(newModel(header, items, cfg), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
