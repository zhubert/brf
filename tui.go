package main

import (
	"fmt"
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
	colOrange  = lipgloss.Color("#ff9e64")
	colRed     = lipgloss.Color("#f7768e")
	colGreenC  = lipgloss.Color("#9ece6a")
	colYellow  = lipgloss.Color("#e0af68")
	colBorderC = lipgloss.Color("#3b4261")
	colSelBGC  = lipgloss.Color("#283457")
	colPurple  = lipgloss.Color("#bb9af7")

	styleTitle    = lipgloss.NewStyle().Foreground(colBlue).Bold(true)
	styleTitleDim = lipgloss.NewStyle().Foreground(colMutedC).Bold(true)
	styleText     = lipgloss.NewStyle().Foreground(colFgDark)
	styleMuted    = lipgloss.NewStyle().Foreground(colMutedC)
	styleSubtle   = lipgloss.NewStyle().Foreground(colSubtleC)
	styleSelected = lipgloss.NewStyle().Foreground(colFg).Background(colSelBGC).Bold(true)
	styleHot      = lipgloss.NewStyle().Foreground(colOrange).Bold(true)
	styleWarn     = lipgloss.NewStyle().Foreground(colRed)
	styleGreen    = lipgloss.NewStyle().Foreground(colGreenC)
	styleBold     = lipgloss.NewStyle().Foreground(colYellow).Bold(true)
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
	fetchedAt  time.Time
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
	manageAddSlackID
	manageAddGHRepo
	manageRemoveSlack
	manageRemoveGH
)

type manageState struct {
	step       manageStep
	inputA     textinput.Model // name / owner+repo
	inputB     textinput.Model // id (slack only)
	statusMsg  string
	pendingVal string // holds inputA value between steps
}

func newManageState() manageState {
	a := textinput.New()
	a.Prompt = "> "
	a.CharLimit = 200

	b := textinput.New()
	b.Prompt = "> "
	b.CharLimit = 64

	return manageState{inputA: a, inputB: b}
}

// ── pane ─────────────────────────────────────────────────────────────────────

type pane int

const (
	paneList pane = iota
	paneContent
)

// ── model ────────────────────────────────────────────────────────────────────

type model struct {
	items        []item
	header       string
	selected     int
	listTop      int
	focus        pane
	width        int
	height       int
	viewport     viewport.Model
	ready        bool
	spinFrame    int
	cfg          *briefConfig
	store        *Store
	managing     bool
	manage       manageState
}

func newModel(header string, items []item, cfg *briefConfig, store *Store) model {
	return model{
		items:  items,
		header: header,
		focus:  paneList,
		cfg:    cfg,
		store:  store,
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
		store := m.store
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
				logRaw(ch.Name, text, ferr)
				if ferr != nil {
					err = ferr
				} else {
					_, secs := parseSections(text)
					if len(secs) > 0 && secs[0].title != "NO_ACTIVITY" {
						summary = secs[0].content
					}
				}
			} else {
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
			if err == nil && summary != "" {
				store.saveSummary(sourceType, sourceID, summary) //nolint
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
				m.items[idx].fetchedAt = time.Now()
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
	hasSlack := false
	hasGH := false
	for _, it := range m.items {
		if it.sourceType == "slack" {
			hasSlack = true
		} else {
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
		rows = append(rows, listRow{itemIdx: -1, label: "GitHub"})
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
		rendered = renderContent(s.summary, m.viewport.Width)
	} else if s.loading {
		rendered = styleMuted.Render("Fetching…")
	} else {
		rendered = styleMuted.Render(fmt.Sprintf("No activity in the last %s.", formatLookback(m.cfg.Slack.LookbackHours)))
	}
	m.viewport.SetContent(rendered)
	m.viewport.GotoTop()
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
		return styleMuted.Render("  brief — terminal too small")
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

	titleStr := " Projects "
	title := styleTitle.Render(titleStr)
	if m.focus != paneList {
		title = styleTitleDim.Render(titleStr)
	}

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
	lines = append(lines, title)
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

	titleStyle := styleTitle
	if m.focus != paneContent {
		titleStyle = styleTitleDim
	}

	rows := m.buildListRows()
	selectable := selectableIndices(rows)
	var sectionTitle string
	var fetchedAt time.Time
	if m.selected < len(selectable) {
		rowIdx := selectable[m.selected]
		itemIdx := rows[rowIdx].itemIdx
		if itemIdx >= 0 && itemIdx < len(m.items) {
			sectionTitle = m.items[itemIdx].name
			fetchedAt = m.items[itemIdx].fetchedAt
		}
	}

	label := titleStyle.Render(" " + truncate(sectionTitle, innerW-4) + " ")
	if !fetchedAt.IsZero() {
		age := styleMuted.Render(" updated " + fetchedAt.Format("3:04pm"))
		label = label + age
	}

	body := label + "\n" + m.viewport.View()
	border := borderNormal
	if m.focus == paneContent {
		border = borderFocused
	}
	return border.Width(innerW).Height(innerH).Render(body)
}

func (m model) renderStatus() string {
	sep := styleMuted.Render(" · ")
	parts := []string{
		styleHot.Render("brief"),
		sep,
		styleCyan.Render(m.header),
		sep,
		styleMuted.Render("tab ") + styleSubtle.Render("switch pane"),
		sep,
		styleMuted.Render("↑↓/jk ") + styleSubtle.Render("navigate"),
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
		sb.WriteString(styleMuted.Render("Channel name:\n"))
		sb.WriteString(m.manage.inputA.View())
	case manageAddSlackID:
		sb.WriteString(styleTitle.Render("Add Slack channel") + "\n\n")
		sb.WriteString(styleMuted.Render("Channel ID (e.g. C01234ABCDE):\n"))
		sb.WriteString(m.manage.inputB.View())
	case manageAddGHRepo:
		sb.WriteString(styleTitle.Render("Add GitHub repo") + "\n\n")
		sb.WriteString(styleMuted.Render("owner/repo:\n"))
		sb.WriteString(m.manage.inputA.View())
	case manageRemoveSlack:
		sb.WriteString(styleTitle.Render("Remove Slack channel") + "\n\n")
		sb.WriteString(styleMuted.Render("Channel ID to remove:\n"))
		sb.WriteString(m.manage.inputA.View())
		sb.WriteString("\n\n" + styleMuted.Render("Current channels:\n"))
		for _, it := range m.items {
			if it.sourceType == "slack" {
				sb.WriteString(styleMuted.Render("  "+it.sourceID+" — "+it.name+"\n"))
			}
		}
	case manageRemoveGH:
		sb.WriteString(styleTitle.Render("Remove GitHub repo") + "\n\n")
		sb.WriteString(styleMuted.Render("owner/repo to remove:\n"))
		sb.WriteString(m.manage.inputA.View())
		sb.WriteString("\n\n" + styleMuted.Render("Current repos:\n"))
		for _, it := range m.items {
			if it.sourceType == "github" {
				sb.WriteString(styleMuted.Render("  "+it.sourceID+"\n"))
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
				m.manage.pendingVal = name
				m.manage.step = manageAddSlackID
				m.manage.inputB.SetValue("")
				m.manage.inputB.Focus()
			}
		default:
			var cmd tea.Cmd
			m.manage.inputA, cmd = m.manage.inputA.Update(msg)
			return m, cmd
		}

	case manageAddSlackID:
		switch msg.String() {
		case "esc":
			m.manage.step = manageAddSlackName
		case "enter":
			id := strings.TrimSpace(m.manage.inputB.Value())
			name := m.manage.pendingVal
			if id != "" {
				if err := m.store.addSlackChannel(id, name); err == nil {
					m.manage.statusMsg = "Added #" + name
					newItem := item{sourceType: "slack", sourceID: id, name: name, loading: true}
					m.items = append(m.items, newItem)
					m.manage.step = manageMenu
					return m, func() tea.Msg {
						return refreshStartMsg{sourceType: "slack", sourceID: id}
					}
				} else {
					m.manage.statusMsg = "Error: " + err.Error()
					m.manage.step = manageMenu
				}
			}
		default:
			var cmd tea.Cmd
			m.manage.inputB, cmd = m.manage.inputB.Update(msg)
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
				if err := m.store.addGithubRepo(owner, repo); err == nil {
					sourceID := owner + "/" + repo
					m.manage.statusMsg = "Added " + sourceID
					newItem := item{sourceType: "github", sourceID: sourceID, name: sourceID, loading: true}
					m.items = append(m.items, newItem)
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
				if err := m.store.removeSlackChannel(id); err == nil {
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
				if err := m.store.removeGithubRepo(parts[0], parts[1]); err == nil {
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

func renderContent(text string, width int) string {
	if width < 1 {
		width = 80
	}
	lines := strings.Split(text, "\n")
	var out []string
	for _, line := range lines {
		colored := colorizeContentLine(line)
		out = append(out, wordwrap.String(colored, width))
	}
	return strings.Join(out, "\n")
}

func colorizeContentLine(line string) string {
	if strings.HasPrefix(line, "**") {
		end := strings.Index(line[2:], "**")
		if end >= 0 {
			label := line[2 : end+2]
			rest := line[end+4:]
			return styleBold.Render("**"+label+"**") + styleText.Render(rest)
		}
	}
	if strings.HasPrefix(line, "- ") {
		content := strings.TrimPrefix(line, "- ")
		return styleCyan.Render("  •") + " " + styleText.Render(content)
	}
	if strings.HasPrefix(line, "  - ") {
		content := strings.TrimPrefix(line, "  - ")
		return styleSubtle.Render("    ◦") + " " + styleMuted.Render(content)
	}
	lower := strings.ToLower(line)
	if strings.Contains(lower, "blocker") || strings.Contains(lower, "blocked") {
		return styleWarn.Render(line)
	}
	if strings.TrimSpace(line) == "" {
		return ""
	}
	return styleText.Render(line)
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

func runTUI(header string, items []item, cfg *briefConfig, store *Store) error {
	p := tea.NewProgram(newModel(header, items, cfg, store), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
