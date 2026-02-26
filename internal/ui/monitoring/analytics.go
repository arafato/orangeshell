package monitoring

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oarafat/orangeshell/internal/api"
)

// AnalyticsModel holds the state for the per-worker analytics dashboard.
// Displayed in the monitoring right pane when the user presses 'a' on a worker.
type AnalyticsModel struct {
	scriptName     string
	timeRangeIndex int // index into api.TimeRanges
	metrics        *api.WorkerMetrics
	loading        bool
	err            error
	scrollY        int // vertical scroll offset
	errorCursor    int // cursor for error log navigation
	autoRefresh    bool
	width          int
	height         int
	lastFetch      time.Time
}

// NewAnalytics creates a new analytics model for the given worker.
func NewAnalytics(scriptName string) AnalyticsModel {
	return AnalyticsModel{
		scriptName:     scriptName,
		timeRangeIndex: 2, // default to 24h
		autoRefresh:    false,
	}
}

// SetSize updates the available dimensions.
func (a *AnalyticsModel) SetSize(w, h int) {
	a.width = w
	a.height = h
}

// ScriptName returns the worker being analyzed.
func (a AnalyticsModel) ScriptName() string {
	return a.scriptName
}

// IsLoading returns whether data is currently being fetched.
func (a AnalyticsModel) IsLoading() bool {
	return a.loading
}

// TimeRangeLabel returns the current time range display label.
func (a AnalyticsModel) TimeRangeLabel() string {
	if a.timeRangeIndex >= 0 && a.timeRangeIndex < len(api.TimeRanges) {
		return api.TimeRanges[a.timeRangeIndex].Label
	}
	return "?"
}

// SetMetrics stores the fetched analytics data.
func (a *AnalyticsModel) SetMetrics(m *api.WorkerMetrics) {
	a.metrics = m
	a.loading = false
	a.err = nil
	a.lastFetch = time.Now()
	a.scrollY = 0
	a.errorCursor = 0
}

// SetError records a fetch error.
func (a *AnalyticsModel) SetError(err error) {
	a.err = err
	a.loading = false
}

// SetLoading marks the model as loading.
func (a *AnalyticsModel) SetLoading() {
	a.loading = true
	a.err = nil
}

// autoRefreshTickMsg fires every 30 seconds when auto-refresh is enabled.
type autoRefreshTickMsg struct {
	scriptName string
}

// Update handles key events for the analytics view.
func (a AnalyticsModel) Update(msg tea.Msg) (AnalyticsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			// Close analytics view, return to grid
			return a, func() tea.Msg { return AnalyticsCloseMsg{} }

		case ",":
			// Previous time range
			if a.timeRangeIndex > 0 {
				a.timeRangeIndex--
				a.loading = true
				idx := a.timeRangeIndex
				name := a.scriptName
				return a, func() tea.Msg {
					return AnalyticsRequestMsg{ScriptName: name, TimeRangeIndex: idx}
				}
			}

		case ".":
			// Next time range
			if a.timeRangeIndex < len(api.TimeRanges)-1 {
				a.timeRangeIndex++
				a.loading = true
				idx := a.timeRangeIndex
				name := a.scriptName
				return a, func() tea.Msg {
					return AnalyticsRequestMsg{ScriptName: name, TimeRangeIndex: idx}
				}
			}

		case "r":
			// Manual refresh
			a.loading = true
			idx := a.timeRangeIndex
			name := a.scriptName
			return a, func() tea.Msg {
				return AnalyticsRequestMsg{ScriptName: name, TimeRangeIndex: idx}
			}

		case "R":
			// Toggle auto-refresh
			a.autoRefresh = !a.autoRefresh
			if a.autoRefresh {
				return a, a.autoRefreshCmd()
			}

		case "j", "down":
			a.scrollY++
			a.clampScroll()

		case "k", "up":
			if a.scrollY > 0 {
				a.scrollY--
			}
		}

	case autoRefreshTickMsg:
		if msg.scriptName != a.scriptName {
			return a, nil
		}
		if !a.autoRefresh {
			return a, nil
		}
		a.loading = true
		idx := a.timeRangeIndex
		name := a.scriptName
		return a, tea.Batch(
			func() tea.Msg {
				return AnalyticsRequestMsg{ScriptName: name, TimeRangeIndex: idx}
			},
			a.autoRefreshCmd(),
		)
	}

	return a, nil
}

func (a AnalyticsModel) autoRefreshCmd() tea.Cmd {
	name := a.scriptName
	return tea.Tick(30*time.Second, func(t time.Time) tea.Msg {
		return autoRefreshTickMsg{scriptName: name}
	})
}

func (a *AnalyticsModel) clampScroll() {
	maxScroll := a.maxScroll()
	if a.scrollY > maxScroll {
		a.scrollY = maxScroll
	}
	if a.scrollY < 0 {
		a.scrollY = 0
	}
}

func (a AnalyticsModel) maxScroll() int {
	// Estimate total content height; clamp scroll based on view
	// Total sections: summary(5) + charts(~20) + errors(~12) + help(1) = ~38
	totalLines := 40
	if a.metrics != nil && len(a.metrics.Errors) > 0 {
		totalLines += len(a.metrics.Errors)
	}
	maxS := totalLines - a.height
	if maxS < 0 {
		return 0
	}
	return maxS
}
