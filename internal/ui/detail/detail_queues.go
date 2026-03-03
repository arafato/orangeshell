package detail

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oarafat/orangeshell/internal/service"
	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// --- Queue Message Inspector helpers ---

// queueSnapshot holds the cached state for a single queue's inspector view.
// Saved when navigating away, restored when navigating back.
type queueSnapshot struct {
	Messages          []service.QueueMessage
	Consumers         []service.QueueConsumer
	Backlog           int
	PulledAt          time.Time
	Cursor            int
	Scroll            int
	NeedsPull         bool
	HasWorkerConsumer bool
	ConsErr           string
}

// InitQueueInspector initializes the queue message inspector for a queue.
// If a cached snapshot exists for this queue, it is restored instead of
// starting from a blank state.
func (m *Model) InitQueueInspector(queueID string) tea.Cmd {
	m.queueActive = true
	m.queueQueueID = queueID
	m.queueLoading = false
	m.queueErr = ""
	m.queueInputFocus = false
	m.queuePushing = false
	m.queuePushResult = ""
	m.queueEnabling = false

	// Restore from cache if available
	if snap, ok := m.queueCache[queueID]; ok {
		m.queueMessages = snap.Messages
		m.queueConsumers = snap.Consumers
		m.queueBacklog = snap.Backlog
		m.queuePulledAt = snap.PulledAt
		m.queueCursor = snap.Cursor
		m.queueScroll = snap.Scroll
		m.queueNeedsPull = snap.NeedsPull
		m.queueHasWorkerConsumer = snap.HasWorkerConsumer
		m.queueConsErr = snap.ConsErr
		m.queueConsLoading = false
	} else {
		m.queueMessages = nil
		m.queueConsumers = nil
		m.queueCursor = 0
		m.queueScroll = 0
		m.queueBacklog = 0
		m.queuePulledAt = time.Time{}
		m.queueNeedsPull = false
		m.queueHasWorkerConsumer = false
		m.queueConsErr = ""
		m.queueConsLoading = false
	}

	ti := textinput.New()
	ti.Prompt = "msg> "
	ti.PromptStyle = theme.QueuePromptStyle
	ti.TextStyle = theme.ValueStyle
	ti.PlaceholderStyle = theme.DimStyle
	ti.Placeholder = `{"key": "value"} or plain text...`
	ti.CharLimit = 0
	m.queueInput = ti
	// Don't focus the input by default — start with message table focused
	return nil
}

// QueueActive returns whether the queue inspector is active.
func (m Model) QueueActive() bool {
	return m.queueActive
}

// QueueInputFocused returns whether the queue message input has focus.
func (m Model) QueueInputFocused() bool {
	return m.queueInputFocus
}

// QueueQueueID returns the current queue UUID.
func (m Model) QueueQueueID() string {
	return m.queueQueueID
}

// SetQueuePullResult stores the pulled message snapshot.
func (m *Model) SetQueuePullResult(result *service.QueuePullResult, err error) {
	m.queueLoading = false
	m.queueNeedsPull = false
	m.queueHasWorkerConsumer = false
	if err != nil {
		if errors.Is(err, service.ErrHTTPPullNotEnabled) {
			m.queueNeedsPull = true
			m.queueErr = ""
			// Check if consumers are already loaded and include a worker consumer.
			// This allows the UI to show the right message immediately.
			for _, c := range m.queueConsumers {
				if c.Type == "worker" {
					m.queueHasWorkerConsumer = true
					break
				}
			}
		} else {
			m.queueErr = err.Error()
		}
		m.queueMessages = nil
		return
	}
	m.queueErr = ""
	m.queueBacklog = result.BacklogCount
	m.queuePulledAt = time.Now()
	if result.Messages == nil {
		m.queueMessages = []service.QueueMessage{}
	} else {
		m.queueMessages = result.Messages
	}
	m.queueCursor = 0
	m.queueScroll = 0
}

// SetQueueConsumers stores the consumer list for the queue.
func (m *Model) SetQueueConsumers(consumers []service.QueueConsumer, err error) {
	m.queueConsLoading = false
	if err != nil {
		m.queueConsErr = err.Error()
		m.queueConsumers = nil
		return
	}
	m.queueConsErr = ""
	m.queueConsumers = consumers

	// If we're in the needsPull state, update the worker consumer flag
	// now that consumer data has arrived.
	if m.queueNeedsPull {
		m.queueHasWorkerConsumer = false
		for _, c := range consumers {
			if c.Type == "worker" {
				m.queueHasWorkerConsumer = true
				break
			}
		}
	}
}

// SetQueuePushResult handles the result of pushing a message.
// On success, optimistically appends the sent message to the local snapshot
// so the user sees it immediately without a destructive re-pull.
func (m *Model) SetQueuePushResult(body string, err error) {
	m.queuePushing = false
	if err != nil {
		m.queuePushResult = fmt.Sprintf("Error: %s", err)
		return
	}
	m.queuePushResult = "Message sent"
	m.queueInput.Reset()
	m.queueBacklog++

	// Optimistic local append — the message won't have a real ID or timestamp
	// from the API, but showing it immediately gives better feedback than
	// re-pulling (which would increment attempts on all messages).
	m.queueMessages = append(m.queueMessages, service.QueueMessage{
		ID:          "(pending)",
		Body:        body,
		Attempts:    0,
		TimestampMs: time.Now().UnixMilli(),
	})
}

// SetQueueEnabling marks that HTTP pull is being enabled.
func (m *Model) SetQueueEnabling() {
	m.queueEnabling = true
}

// SetQueueHTTPPullEnabled handles the result of enabling HTTP pull on a queue.
// On success, clears the needsPull flag so the inspector can retry pulling.
func (m *Model) SetQueueHTTPPullEnabled(err error) {
	m.queueEnabling = false
	if err != nil {
		// If the queue already has a worker consumer, stay in the needsPull
		// state so the UI shows the contextual help instead of a raw error.
		if errors.Is(err, service.ErrQueueHasConsumer) {
			m.queueNeedsPull = true
			m.queueHasWorkerConsumer = true
			m.queueErr = ""
			return
		}
		m.queueErr = err.Error()
		return
	}
	m.queueNeedsPull = false
	m.queueHasWorkerConsumer = false
	// Don't auto-pull — the user can press 'r' to take a snapshot.
}

// ClearQueue saves the current inspector state to the per-queue cache and
// resets the live state. The cached snapshot is restored if the user navigates
// back to the same queue.
func (m *Model) ClearQueue() {
	// Save to cache if there's anything worth caching (a snapshot was taken
	// or consumers were loaded, or we know the queue needs HTTP pull).
	if m.queueQueueID != "" && (m.queueMessages != nil || m.queueConsumers != nil || m.queueNeedsPull) {
		m.queueCache[m.queueQueueID] = &queueSnapshot{
			Messages:          m.queueMessages,
			Consumers:         m.queueConsumers,
			Backlog:           m.queueBacklog,
			PulledAt:          m.queuePulledAt,
			Cursor:            m.queueCursor,
			Scroll:            m.queueScroll,
			NeedsPull:         m.queueNeedsPull,
			HasWorkerConsumer: m.queueHasWorkerConsumer,
			ConsErr:           m.queueConsErr,
		}
	}

	m.queueActive = false
	m.queueMessages = nil
	m.queueLoading = false
	m.queueQueueID = ""
	m.queueCursor = 0
	m.queueScroll = 0
	m.queueErr = ""
	m.queueBacklog = 0
	m.queuePulledAt = time.Time{}
	m.queueConsumers = nil
	m.queueConsLoading = false
	m.queueConsErr = ""
	m.queueInputFocus = false
	m.queuePushing = false
	m.queuePushResult = ""
	m.queueNeedsPull = false
	m.queueEnabling = false
	m.queueHasWorkerConsumer = false
	m.queueInput.Blur()
}

// ClearQueueCache removes all cached queue snapshots. Called on account switch
// or when leaving the Queues service entirely.
func (m *Model) ClearQueueCache() {
	for k := range m.queueCache {
		delete(m.queueCache, k)
	}
}

// PreloadQueueConsumers sets the consumers loading state for preview mode.
func (m *Model) PreloadQueueConsumers(queueID string) {
	m.queueQueueID = queueID
	m.queueConsLoading = true
	m.queueConsumers = nil
	m.queueConsErr = ""
}

// updateQueue handles key events when the queue inspector is active.
func (m Model) updateQueue(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		// Exit interactive mode, switch focus to list pane
		m.interacting = false
		m.focus = FocusList
		return m, nil

	case tea.KeyTab:
		// Toggle focus between message table and text input
		m.queueInputFocus = !m.queueInputFocus
		if m.queueInputFocus {
			return m, m.queueInput.Focus()
		}
		m.queueInput.Blur()
		return m, nil

	case tea.KeyEnter:
		// If input is focused, submit the message
		if m.queueInputFocus {
			body := strings.TrimSpace(m.queueInput.Value())
			if body == "" || m.queuePushing {
				return m, nil
			}
			m.queuePushing = true
			m.queuePushResult = ""
			qID := m.queueQueueID
			return m, func() tea.Msg {
				return QueuePushMsg{QueueID: qID, Body: body}
			}
		}
		return m, nil

	case tea.KeyUp:
		if !m.queueInputFocus {
			if m.queueCursor > 0 {
				m.queueCursor--
				if m.queueCursor < m.queueScroll {
					m.queueScroll = m.queueCursor
				}
			}
		}
		return m, nil

	case tea.KeyDown:
		if !m.queueInputFocus {
			if m.queueCursor < len(m.queueMessages)-1 {
				m.queueCursor++
			}
		}
		return m, nil
	}

	// Check for specific key strings
	switch msg.String() {
	case "r":
		if m.queueInputFocus {
			break // let textinput handle it
		}
		// Refresh: pull fresh snapshot
		if m.queueLoading {
			return m, nil
		}
		m.queueLoading = true
		m.queueErr = ""
		m.queuePushResult = ""
		qID := m.queueQueueID
		return m, func() tea.Msg {
			return QueuePullMsg{QueueID: qID, BatchSize: 10}
		}

	case "ctrl+e":
		// Enable HTTP pull consumer on this queue
		if m.queueNeedsPull && !m.queueEnabling {
			m.queueEnabling = true
			qID := m.queueQueueID
			return m, func() tea.Msg {
				return QueueEnableHTTPPullMsg{QueueID: qID}
			}
		}
		return m, nil

	case "ctrl+y":
		// Copy selected message body to clipboard
		if !m.queueInputFocus && len(m.queueMessages) > 0 && m.queueCursor < len(m.queueMessages) {
			body := m.queueMessages[m.queueCursor].Body
			return m, func() tea.Msg {
				return CopyToClipboardMsg{Text: body}
			}
		}
		return m, nil

	case "k":
		if m.queueInputFocus {
			break // let textinput handle it
		}
		if m.queueCursor > 0 {
			m.queueCursor--
			if m.queueCursor < m.queueScroll {
				m.queueScroll = m.queueCursor
			}
		}
		return m, nil

	case "j":
		if m.queueInputFocus {
			break // let textinput handle it
		}
		if m.queueCursor < len(m.queueMessages)-1 {
			m.queueCursor++
		}
		return m, nil
	}

	// Forward all other keys to the textinput if focused
	if m.queueInputFocus {
		var cmd tea.Cmd
		m.queueInput, cmd = m.queueInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

// viewResourceDetailQueue renders the right pane for queues with the message inspector.
func (m Model) viewResourceDetailQueue(width, height int, title, sep string, topFieldLines []string, copyLineMap map[int]string) []string {
	// Compact metadata at top
	topLines := []string{title, sep}
	topLines = append(topLines, m.renderQueueCompactFields(copyLineMap)...)

	metaHeight := len(topLines)

	// Separator between metadata and the inspector pane
	panesSepWidth := width - 3
	if panesSepWidth < 0 {
		panesSepWidth = 0
	}
	panesSep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("─", panesSepWidth))
	topLines = append(topLines, panesSep)
	metaHeight++

	// Inspector region
	paneHeight := height - metaHeight
	if paneHeight < 10 {
		paneHeight = 10
	}

	inspectorPane := m.renderQueueInspector(width-2, paneHeight)

	// Register copy targets for metadata lines
	m.registerCopyTargets(copyLineMap, 0, len(topLines))

	result := strings.Join(topLines, "\n") + "\n" + strings.Join(inspectorPane, "\n")
	lines := strings.Split(result, "\n")

	// Pad to height
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

// renderQueueCompactFields renders compact metadata rows for the queue inspector.
func (m Model) renderQueueCompactFields(copyLineMap map[int]string) []string {
	if m.detail == nil {
		return nil
	}

	fields := m.detail.Fields
	fieldMap := make(map[string]string)
	for _, f := range fields {
		fieldMap[f.Label] = f.Value
	}

	// Row 1: Queue ID, Producers, Consumers, Backlog
	var parts []string
	if v, ok := fieldMap["Queue ID"]; ok {
		short := v
		if len(short) > 12 {
			short = short[:12] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s %s%s",
			theme.LabelStyle.Render("ID"), theme.ValueStyle.Render(short), copyIcon()))
		copyLineMap[2] = v // title=0, sep=1, this row=2
	}
	if v, ok := fieldMap["Producers"]; ok {
		display := v
		if len(display) > 40 {
			display = display[:37] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s %s",
			theme.LabelStyle.Render("Producers"), theme.ValueStyle.Render(display)))
	}
	if v, ok := fieldMap["Consumers"]; ok {
		parts = append(parts, fmt.Sprintf("%s %s",
			theme.LabelStyle.Render("Consumers"), theme.ValueStyle.Render(v)))
	}
	if m.queueBacklog > 0 || !m.queuePulledAt.IsZero() {
		parts = append(parts, fmt.Sprintf("%s %s",
			theme.LabelStyle.Render("Backlog"), theme.ValueStyle.Render(fmt.Sprintf("%d", m.queueBacklog))))
	}

	var rows []string
	if len(parts) > 0 {
		rows = append(rows, "  "+strings.Join(parts, "   "))
	}
	return rows
}

// renderQueueInspector renders the full inspector pane (snapshot header + message table + detail + input).
func (m Model) renderQueueInspector(width, height int) []string {
	var lines []string

	// Header line with snapshot status
	header := theme.QueueHeaderStyle.Render("Message Inspector")
	if m.queueLoading {
		header += "  " + m.spinner.View() + " " + theme.DimStyle.Render("pulling...")
	} else if !m.queuePulledAt.IsZero() {
		ts := m.queuePulledAt.Format("15:04:05")
		header += "  " + theme.DimStyle.Render(fmt.Sprintf("snapshot at %s — r to refresh", ts))
	}
	lines = append(lines, header)

	// Error state
	if m.queueErr != "" {
		lines = append(lines, theme.ErrorStyle.Render(fmt.Sprintf("Error: %s", m.queueErr)))
		lines = append(lines, "")
		lines = append(lines, theme.DimStyle.Render("Press r to retry"))
		return m.padQueueLines(lines, height)
	}

	// HTTP pull not enabled state
	if m.queueNeedsPull {
		lines = append(lines, "")
		warnStyle := lipgloss.NewStyle().Foreground(theme.ColorYellow).Bold(true)
		if m.queueEnabling {
			lines = append(lines, "  "+m.spinner.View()+" "+theme.DimStyle.Render("Enabling HTTP pull consumer..."))
		} else if m.queueHasWorkerConsumer {
			// Queue has a worker consumer — can't add HTTP pull alongside it.
			lines = append(lines, warnStyle.Render("  Cannot enable HTTP pull"))
			lines = append(lines, "")
			lines = append(lines, theme.DimStyle.Render("  This queue already has a worker consumer attached."))
			lines = append(lines, theme.DimStyle.Render("  Cloudflare queues only support one consumer type at a time."))
			lines = append(lines, "")
			lines = append(lines, theme.DimStyle.Render("  To inspect messages, remove the worker consumer first:"))
			cmdStyle := lipgloss.NewStyle().Foreground(theme.ColorWhite)
			lines = append(lines, "")
			// Show the consumer name if we have it
			consumerName := ""
			for _, c := range m.queueConsumers {
				if c.Type == "worker" {
					consumerName = c.Name
					break
				}
			}
			if consumerName != "" {
				lines = append(lines, "    "+cmdStyle.Render(fmt.Sprintf(
					"wrangler queues consumer remove %s %s", m.detail.Name, consumerName)))
			} else {
				lines = append(lines, "    "+cmdStyle.Render(fmt.Sprintf(
					"wrangler queues consumer remove %s <script-name>", m.detail.Name)))
			}
			lines = append(lines, "")
			lines = append(lines, theme.DimStyle.Render("  Then press ctrl+e to add an HTTP pull consumer."))
		} else {
			// No consumer conflict — offer to enable HTTP pull.
			lines = append(lines, warnStyle.Render("  HTTP pull consumer required"))
			lines = append(lines, "")
			lines = append(lines, theme.DimStyle.Render("  Message inspection requires an HTTP pull consumer on this queue."))
			lines = append(lines, "")
			keyStyle := lipgloss.NewStyle().Foreground(theme.ColorOrange).Bold(true)
			lines = append(lines, fmt.Sprintf("  Press %s to enable HTTP pull on this queue",
				keyStyle.Render("ctrl+e")))
		}
		return m.padQueueLines(lines, height)
	}

	// Initial state — no snapshot taken yet
	if !m.queueLoading && m.queueMessages == nil {
		lines = append(lines, "")
		keyStyle := lipgloss.NewStyle().Foreground(theme.ColorOrange).Bold(true)
		lines = append(lines, fmt.Sprintf("  Press %s to take a snapshot of messages",
			keyStyle.Render("r")))
		lines = append(lines, "")
		lines = append(lines, theme.DimStyle.Render("  Note: each snapshot counts as a delivery attempt for all pulled messages."))
		return m.padQueueLines(lines, height)
	}

	// Empty state — snapshot was taken but queue is empty
	if !m.queueLoading && m.queueMessages != nil && len(m.queueMessages) == 0 {
		lines = append(lines, "")
		lines = append(lines, theme.DimStyle.Render("  No messages in queue"))
		lines = append(lines, theme.DimStyle.Render("  Press r to check again"))
	}

	// Message table + detail (only when we have messages)
	if len(m.queueMessages) > 0 {
		// Allocate ~35% for table, ~45% for detail, rest for input + help
		tableHeight := (height - 4) * 35 / 100
		if tableHeight < 4 {
			tableHeight = 4
		}
		detailHeight := (height - 4) - tableHeight
		if detailHeight < 4 {
			detailHeight = 4
		}

		// Message table
		tableLines := m.renderQueueMessageTable(width, tableHeight)
		lines = append(lines, tableLines...)

		// Separator
		lines = append(lines, lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
			strings.Repeat("─", width-1)))

		// Message detail
		detailLines := m.renderQueueMessageDetail(width, detailHeight)
		lines = append(lines, detailLines...)
	}

	// Input line
	lines = append(lines, "")
	if m.queuePushing {
		lines = append(lines, fmt.Sprintf("  %s %s", m.spinner.View(), theme.DimStyle.Render("Sending...")))
	} else {
		inputLine := "  " + m.queueInput.View()
		lines = append(lines, inputLine)
	}
	if m.queuePushResult != "" {
		style := lipgloss.NewStyle().Foreground(theme.ColorGreen)
		if strings.HasPrefix(m.queuePushResult, "Error:") {
			style = theme.ErrorStyle
		}
		lines = append(lines, "  "+style.Render(m.queuePushResult))
	}

	// Help bar
	helpParts := []string{"r snapshot", "\u2191/\u2193 navigate", "tab input", "enter send", "ctrl+y copy", "esc exit"}
	help := theme.DimStyle.Render("  " + strings.Join(helpParts, " | "))
	lines = append(lines, help)

	return m.padQueueLines(lines, height)
}

// padQueueLines pads lines to exact height, truncating or padding as needed.
func (m Model) padQueueLines(lines []string, height int) []string {
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

// renderQueueMessageTable renders the message table with cursor navigation.
func (m Model) renderQueueMessageTable(width, maxRows int) []string {
	if len(m.queueMessages) == 0 {
		return nil
	}

	// Column widths: # (4), ID (14), BODY (remaining), AGE (7), ATT (5)
	numW := 4
	idW := 14
	ageW := 7
	attW := 5
	bodyW := width - numW - idW - ageW - attW - 6 // separators
	if bodyW < 10 {
		bodyW = 10
	}

	// Header
	headerLine := fmt.Sprintf("  %s%s%s%s%s",
		theme.LabelStyle.Render(padRight("#", numW)),
		theme.LabelStyle.Render(padRight("ID", idW)),
		theme.LabelStyle.Render(padRight("BODY", bodyW)),
		theme.LabelStyle.Render(padRight("AGE", ageW)),
		theme.LabelStyle.Render(padRight("ATT", attW)),
	)
	sepLine := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		strings.Repeat("─", width-1))

	var lines []string
	lines = append(lines, headerLine, sepLine)

	// Scroll: ensure cursor is visible
	dataRows := maxRows - 2 // subtract header + separator
	if dataRows < 1 {
		dataRows = 1
	}
	if m.queueCursor >= m.queueScroll+dataRows {
		m.queueScroll = m.queueCursor - (dataRows - 1)
	}
	if m.queueScroll < 0 {
		m.queueScroll = 0
	}

	startIdx := m.queueScroll
	endIdx := startIdx + dataRows
	if endIdx > len(m.queueMessages) {
		endIdx = len(m.queueMessages)
	}

	showCursor := m.focus == FocusDetail

	for i := startIdx; i < endIdx; i++ {
		msg := m.queueMessages[i]

		// Cursor indicator
		cursor := "  "
		numStyle := theme.DimStyle
		idStyle := theme.ValueStyle
		bodyStyle := theme.ValueStyle
		ageStyle := theme.DimStyle
		attStyle := theme.DimStyle
		if showCursor && i == m.queueCursor {
			cursor = lipgloss.NewStyle().Foreground(theme.ColorOrange).Render("\u25b8 ")
			numStyle = lipgloss.NewStyle().Foreground(theme.ColorOrange)
			idStyle = lipgloss.NewStyle().Foreground(theme.ColorOrange)
			bodyStyle = lipgloss.NewStyle().Foreground(theme.ColorOrange)
			ageStyle = lipgloss.NewStyle().Foreground(theme.ColorOrange)
			attStyle = lipgloss.NewStyle().Foreground(theme.ColorOrange)
		}

		// Number (1-indexed)
		numStr := padRight(fmt.Sprintf("%d", i+1), numW)

		// ID (truncated)
		idStr := msg.ID
		if len(idStr) > idW-2 {
			idStr = idStr[:idW-2] + ".."
		}
		idStr = padRight(idStr, idW)

		// Body preview (single line, truncated)
		bodyPreview := strings.ReplaceAll(msg.Body, "\n", "\\n")
		bodyPreview = strings.ReplaceAll(bodyPreview, "\t", " ")
		if len(bodyPreview) > bodyW-2 {
			bodyPreview = bodyPreview[:bodyW-2] + ".."
		}
		bodyPreview = padRight(bodyPreview, bodyW)

		// Age
		age := queueMsgAge(msg.TimestampMs)
		age = padRight(age, ageW)

		// Attempts
		attStr := padRight(fmt.Sprintf("%d", msg.Attempts), attW)

		line := fmt.Sprintf("%s%s%s%s%s%s",
			cursor,
			numStyle.Render(numStr),
			idStyle.Render(idStr),
			bodyStyle.Render(bodyPreview),
			ageStyle.Render(age),
			attStyle.Render(attStr),
		)
		lines = append(lines, line)
	}

	return lines
}

// renderQueueMessageDetail renders the selected message's full details.
func (m Model) renderQueueMessageDetail(width, maxRows int) []string {
	if len(m.queueMessages) == 0 || m.queueCursor >= len(m.queueMessages) {
		return []string{theme.DimStyle.Render("  No message selected")}
	}

	msg := m.queueMessages[m.queueCursor]

	var lines []string
	lines = append(lines, fmt.Sprintf("  %s %s",
		theme.LabelStyle.Render("Message"),
		theme.ValueStyle.Render(fmt.Sprintf("#%d", m.queueCursor+1))))

	// ID
	lines = append(lines, fmt.Sprintf("  %s  %s",
		theme.LabelStyle.Render("ID        "),
		theme.ValueStyle.Render(msg.ID)))

	// Attempts
	lines = append(lines, fmt.Sprintf("  %s  %s",
		theme.LabelStyle.Render("Attempts  "),
		theme.ValueStyle.Render(fmt.Sprintf("%d", msg.Attempts))))

	// Timestamp
	ts := time.UnixMilli(msg.TimestampMs)
	age := queueMsgAge(msg.TimestampMs)
	lines = append(lines, fmt.Sprintf("  %s  %s %s",
		theme.LabelStyle.Render("Timestamp "),
		theme.ValueStyle.Render(ts.UTC().Format("2006-01-02 15:04:05 UTC")),
		theme.DimStyle.Render(fmt.Sprintf("(%s ago)", age))))

	// Metadata
	if len(msg.Metadata) > 0 {
		// Sort keys for deterministic output
		keys := make([]string, 0, len(msg.Metadata))
		for k := range msg.Metadata {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i == 0 {
				lines = append(lines, fmt.Sprintf("  %s  %s: %s",
					theme.LabelStyle.Render("Metadata  "),
					theme.DimStyle.Render(k),
					theme.ValueStyle.Render(msg.Metadata[k])))
			} else {
				lines = append(lines, fmt.Sprintf("  %s  %s: %s",
					strings.Repeat(" ", 12),
					theme.DimStyle.Render(k),
					theme.ValueStyle.Render(msg.Metadata[k])))
			}
		}
	}

	// Body
	lines = append(lines, "")
	bodyLabel := "Body"
	formattedBody := msg.Body

	// Try to pretty-print JSON
	var parsed interface{}
	if err := json.Unmarshal([]byte(msg.Body), &parsed); err == nil {
		if pretty, err := json.MarshalIndent(parsed, "  ", "  "); err == nil {
			formattedBody = string(pretty)
			bodyLabel = "Body (JSON)"
		}
	}

	lines = append(lines, fmt.Sprintf("  %s",
		theme.LabelStyle.Render(bodyLabel+":")))

	bodyLines := strings.Split(formattedBody, "\n")
	for _, bl := range bodyLines {
		// Truncate very long lines
		if len(bl) > width-4 {
			bl = bl[:width-7] + "..."
		}
		lines = append(lines, "  "+theme.ValueStyle.Render(bl))
	}

	// Truncate to available space
	if len(lines) > maxRows {
		lines = lines[:maxRows-1]
		lines = append(lines, theme.DimStyle.Render("  ... (body truncated)"))
	}

	return lines
}

// renderConsumerTable renders the consumer table for preview mode.
func (m Model) renderConsumerTable(width int) []string {
	if len(m.queueConsumers) == 0 {
		return []string{theme.DimStyle.Render("  No consumers configured")}
	}

	// Column widths
	typeW := 10
	nameW := 20
	batchW := 7
	retriesW := 9
	dlqW := width - typeW - nameW - batchW - retriesW - 10
	if dlqW < 10 {
		dlqW = 10
	}

	// Header
	header := fmt.Sprintf("  %s%s%s%s%s",
		theme.LabelStyle.Render(padRight("TYPE", typeW)),
		theme.LabelStyle.Render(padRight("NAME", nameW)),
		theme.LabelStyle.Render(padRight("BATCH", batchW)),
		theme.LabelStyle.Render(padRight("RETRIES", retriesW)),
		theme.LabelStyle.Render(padRight("DLQ", dlqW)),
	)
	sep := lipgloss.NewStyle().Foreground(theme.ColorDarkGray).Render(
		"  " + strings.Repeat("─", width-5))

	lines := []string{header, sep}

	for _, c := range m.queueConsumers {
		typeStr := padRight(c.Type, typeW)
		nameStr := padRight(truncateRunes(c.Name, nameW-2), nameW)
		batchStr := padRight(fmt.Sprintf("%d", c.BatchSize), batchW)
		retriesStr := padRight(fmt.Sprintf("%d", c.MaxRetries), retriesW)
		dlqStr := c.DLQ
		if dlqStr == "" {
			dlqStr = "\u2014" // em-dash
		}
		dlqStr = padRight(truncateRunes(dlqStr, dlqW-2), dlqW)

		line := fmt.Sprintf("  %s%s%s%s%s",
			theme.ValueStyle.Render(typeStr),
			theme.ValueStyle.Render(nameStr),
			theme.DimStyle.Render(batchStr),
			theme.DimStyle.Render(retriesStr),
			theme.DimStyle.Render(dlqStr),
		)
		lines = append(lines, line)
	}

	return lines
}

// queueMsgAge returns a human-readable age string from a timestamp in milliseconds.
func queueMsgAge(timestampMs int64) string {
	ts := time.UnixMilli(timestampMs)
	d := time.Since(ts)
	if d < 0 {
		return "0s"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
