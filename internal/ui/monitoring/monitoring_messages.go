package monitoring

import (
	"github.com/oarafat/orangeshell/internal/api"
	svc "github.com/oarafat/orangeshell/internal/service"
)

// --- Constants ---

const (
	gridMaxLines   = 200 // max log lines retained per grid pane
	gridMinPaneH   = 6   // minimum grid pane height
	gridCols       = 2   // fixed number of columns in the tail grid
	leftPaneRatio  = 20  // left pane gets ~20% of width
	rightPaneRatio = 80  // right pane gets ~80% of width
)

// --- Focus ---

// FocusPane identifies which pane has keyboard focus.
type FocusPane int

const (
	FocusLeft  FocusPane = iota // Worker tree browser
	FocusRight                  // Live tail grid
)

// --- Messages emitted by the monitoring model ---

// TailAddMsg requests the app to start tailing a worker and add it to the grid.
type TailAddMsg struct {
	ScriptName string
}

// TailRemoveMsg requests the app to stop tailing a worker and remove it from the grid.
type TailRemoveMsg struct {
	ScriptName string
}

// TailToggleMsg requests the app to toggle tailing for a specific grid pane.
type TailToggleMsg struct {
	ScriptName string
	Start      bool // true = start, false = stop
}

// TailToggleAllMsg requests the app to toggle all grid panes on or off.
type TailToggleAllMsg struct {
	Start bool // true = start all, false = stop all
}

// DevCronTriggerMsg requests the app to trigger a cron handler on a dev worker
// via the /cdn-cgi/handler/scheduled endpoint.
type DevCronTriggerMsg struct {
	ScriptName string // dev:worker-name
}

// TailStopMsg requests the app to stop the active single tail session (backward compat).
type TailStopMsg struct{}

// ParallelTailStopMsg requests the app to stop all parallel tail sessions (backward compat).
type ParallelTailStopMsg struct{}

// --- Worker tree ---

// WorkerTreeEntry represents a single item in the left-pane worker tree.
type WorkerTreeEntry struct {
	ProjectName string // wrangler project name (e.g. "express-d1-app")
	ScriptName  string // resolved worker name (empty for headers)
	EnvName     string // environment name (e.g. "default", "staging")
	IsHeader    bool   // true for project group headers (non-selectable)
	IsDev       bool   // true for dev-mode entries (wrangler dev sessions)
	DevKind     string // "local" or "remote" (only set when IsDev is true)
	DevPort     string // e.g. "8787" — extracted from wrangler dev output
}

// --- Tail pane (grid) ---

// TailPane holds per-worker tail state in the live grid.
type TailPane struct {
	ScriptName string
	URL        string
	Lines      []svc.TailLine
	Connecting bool
	Connected  bool
	Active     bool // true if tail is running (or starting)
	Error      string
	SessionID  string
	IsDev      bool   // true for dev-mode panes (wrangler dev output)
	DevKind    string // "local" or "remote"
}

func (p *TailPane) appendLines(lines []svc.TailLine) {
	p.Lines = append(p.Lines, lines...)
	if len(p.Lines) > gridMaxLines {
		p.Lines = p.Lines[len(p.Lines)-gridMaxLines:]
	}
}

// ParallelTailTarget identifies a worker to tail (used by app layer).
type ParallelTailTarget struct {
	ScriptName string
	URL        string
}

// --- Backward-compat Mode type (still used by app layer queries) ---

// Mode describes the monitoring state for external queries.
type Mode int

const (
	ModeIdle     Mode = iota // No active tailing
	ModeSingle               // Single worker being tailed (via grid)
	ModeParallel             // Multiple workers being tailed (via grid)
)

// GridPaneInfo holds exportable information about a grid pane for use by the AI context panel.
type GridPaneInfo struct {
	ScriptName string
	IsDev      bool
	DevKind    string // "local" or "remote" (only set for dev panes)
	Active     bool
	LineCount  int
	Lines      []svc.TailLine // copy of the log lines
}

// --- Analytics messages ---

// AnalyticsRequestMsg requests the app to fetch analytics for a worker.
type AnalyticsRequestMsg struct {
	ScriptName     string
	TimeRangeIndex int // index into api.TimeRanges
}

// AnalyticsDataMsg carries the analytics response back to the monitoring model.
type AnalyticsDataMsg struct {
	ScriptName string
	Data       *api.WorkerMetrics
	Err        error
}

// AnalyticsCloseMsg signals that the analytics view should be closed.
type AnalyticsCloseMsg struct{}
