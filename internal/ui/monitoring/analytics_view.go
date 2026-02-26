package monitoring

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/oarafat/orangeshell/internal/ui/theme"
)

// chartBucket holds aggregated metrics for a single time point in charts.
type chartBucket struct {
	dt       time.Time
	requests int64
	errors   int64
}

// View renders the analytics dashboard for a single worker.
func (a AnalyticsModel) View(width, height int) string {
	if width < 20 || height < 5 {
		return ""
	}

	var sections []string

	// Title bar
	sections = append(sections, a.viewTitle(width))

	if a.loading && a.metrics == nil {
		sections = append(sections, a.viewLoading(width))
	} else if a.err != nil {
		sections = append(sections, a.viewError(width))
	} else if a.metrics == nil {
		sections = append(sections, a.viewNoData(width))
	} else {
		// Summary cards
		sections = append(sections, a.viewSummaryCards(width))
		sections = append(sections, "")

		// Requests bar chart
		sections = append(sections, a.viewRequestsChart(width))
		sections = append(sections, "")

		// Status breakdown table
		sections = append(sections, a.viewStatusBreakdown(width))
		sections = append(sections, "")

		// Error log
		if len(a.metrics.Errors) > 0 {
			sections = append(sections, a.viewErrorLog(width))
			sections = append(sections, "")
		}
	}

	content := strings.Join(sections, "\n")

	// Apply scroll
	lines := strings.Split(content, "\n")
	if a.scrollY > 0 && a.scrollY < len(lines) {
		lines = lines[a.scrollY:]
	}

	// Truncate/pad to exact height
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}

	// Apply width
	widthStyle := lipgloss.NewStyle().Width(width)
	var output []string
	for _, line := range lines {
		output = append(output, widthStyle.Render(line))
	}
	return strings.Join(output, "\n")
}

// --- Title ---

func (a AnalyticsModel) viewTitle(width int) string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.ColorWhite)
	nameStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.ColorOrange)
	rangeStyle := lipgloss.NewStyle().Foreground(theme.ColorBlue).Bold(true)

	title := fmt.Sprintf(" %s  %s  %s",
		titleStyle.Render("Analytics"),
		nameStyle.Render(a.scriptName),
		rangeStyle.Render("["+a.TimeRangeLabel()+"]"))

	if a.loading {
		title += " " + theme.DimStyle.Render("loading...")
	}
	if a.autoRefresh {
		title += " " + lipgloss.NewStyle().Foreground(theme.ColorGreen).Render("⟳")
	}

	return title
}

// --- Loading / Error / No Data ---

func (a AnalyticsModel) viewLoading(_ int) string {
	return " " + theme.DimStyle.Render("Fetching analytics data...")
}

func (a AnalyticsModel) viewError(_ int) string {
	return " " + theme.ErrorStyle.Render(fmt.Sprintf("Error: %v", a.err))
}

func (a AnalyticsModel) viewNoData(_ int) string {
	return " " + theme.DimStyle.Render("No analytics data available for this worker.")
}

// --- Summary Cards ---

func (a AnalyticsModel) viewSummaryCards(width int) string {
	m := a.metrics

	// Compute error rate
	errorRate := float64(0)
	if m.TotalRequests > 0 {
		errorRate = float64(m.TotalErrors) / float64(m.TotalRequests) * 100
	}

	// Format CPU time (microseconds → ms)
	cpuP50 := formatCPUTime(m.CPUTimeP50)
	cpuP99 := formatCPUTime(m.CPUTimeP99)

	// Card definitions
	type card struct {
		label string
		value string
		color lipgloss.Color
	}
	cards := []card{
		{"Requests", formatCount(m.TotalRequests), theme.ColorBlue},
		{"Errors", fmt.Sprintf("%s (%.1f%%)", formatCount(m.TotalErrors), errorRate), theme.ColorRed},
		{"CPU p50", cpuP50, theme.ColorGreen},
		{"CPU p99", cpuP99, theme.ColorYellow},
		{"Subreqs", formatCount(m.TotalSubrequests), theme.ColorGray},
	}

	// Calculate card width (distribute evenly, accounting for border characters)
	cardCount := len(cards)
	cardWidth := (width - 1) / cardCount
	if cardWidth < 14 {
		cardWidth = 14
	}
	// Inner width = card width minus border (2 chars) minus padding (2 chars)
	innerWidth := cardWidth - 4
	if innerWidth < 8 {
		innerWidth = 8
	}

	var cardViews []string
	for _, c := range cards {
		labelStyle := lipgloss.NewStyle().Foreground(theme.ColorGray).Width(innerWidth)
		valueStyle := lipgloss.NewStyle().Bold(true).Foreground(c.color).Width(innerWidth)

		cardContent := labelStyle.Render(c.label) + "\n" + valueStyle.Render(c.value)

		cardStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(c.color).
			Width(innerWidth).
			Padding(0, 1)

		cardViews = append(cardViews, cardStyle.Render(cardContent))
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, cardViews...)
}

// --- Requests Bar Chart ---

func (a AnalyticsModel) viewRequestsChart(width int) string {
	m := a.metrics
	if len(m.Buckets) == 0 {
		return " " + theme.DimStyle.Render("No request data to chart.")
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.ColorWhite)
	header := " " + headerStyle.Render("Requests over Time")

	// Aggregate buckets by time (merge status dimensions)
	merged := make(map[string]*chartBucket)
	var orderedKeys []string
	for _, b := range m.Buckets {
		key := b.Datetime.Format(time.RFC3339)
		if _, ok := merged[key]; !ok {
			merged[key] = &chartBucket{dt: b.Datetime}
			orderedKeys = append(orderedKeys, key)
		}
		merged[key].requests += b.Requests
		merged[key].errors += b.Errors
	}

	// Convert to slice
	var buckets []chartBucket
	for _, k := range orderedKeys {
		buckets = append(buckets, *merged[k])
	}

	// Downsample if too many buckets for display width
	chartWidth := width - 4 // padding
	if chartWidth < 10 {
		chartWidth = 10
	}
	maxBars := chartWidth
	if len(buckets) > maxBars {
		buckets = downsampleChartBuckets(buckets, maxBars)
	}

	// Find max for scaling
	var maxReq int64
	for _, b := range buckets {
		if b.requests > maxReq {
			maxReq = b.requests
		}
	}

	if maxReq == 0 {
		return header + "\n " + theme.DimStyle.Render("No requests in this period.")
	}

	// Render bar chart (8 vertical levels using block characters)
	chartHeight := 8
	chart := renderBarChart(buckets, chartHeight, maxReq)

	// Time axis labels
	timeAxis := renderTimeAxis(buckets, chartWidth)

	// Y-axis label
	yLabel := fmt.Sprintf(" max: %s", formatCount(maxReq))

	return strings.Join([]string{
		header + "  " + theme.DimStyle.Render(yLabel),
		chart,
		timeAxis,
	}, "\n")
}

func renderBarChart(buckets []chartBucket, chartHeight int, maxReq int64) string {
	barCount := len(buckets)

	// Block characters for fractional fills (bottom to top)
	blocks := []string{" ", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

	var lines []string
	for row := chartHeight - 1; row >= 0; row-- {
		var line strings.Builder
		line.WriteString(" ")
		for i := 0; i < barCount; i++ {
			val := float64(buckets[i].requests)
			// Scale to chart height
			scaled := val / float64(maxReq) * float64(chartHeight)
			level := scaled - float64(row)

			var ch string
			if level >= 1.0 {
				ch = "█"
			} else if level > 0 {
				idx := int(level * 8)
				if idx >= len(blocks) {
					idx = len(blocks) - 1
				}
				ch = blocks[idx]
			} else {
				ch = " "
			}

			// Color: blue for normal, red/orange if errors
			hasErrors := buckets[i].errors > 0
			if ch != " " && hasErrors {
				errRatio := float64(buckets[i].errors) / float64(buckets[i].requests)
				if errRatio > 0.5 {
					line.WriteString(lipgloss.NewStyle().Foreground(theme.ColorRed).Render(ch))
				} else {
					line.WriteString(lipgloss.NewStyle().Foreground(theme.ColorOrange).Render(ch))
				}
			} else if ch != " " {
				line.WriteString(lipgloss.NewStyle().Foreground(theme.ColorBlue).Render(ch))
			} else {
				line.WriteString(ch)
			}
		}
		lines = append(lines, line.String())
	}

	return strings.Join(lines, "\n")
}

func renderTimeAxis(buckets []chartBucket, chartWidth int) string {
	if len(buckets) == 0 {
		return ""
	}

	first := buckets[0].dt
	last := buckets[len(buckets)-1].dt

	firstLabel := formatTimeLabel(first)
	lastLabel := formatTimeLabel(last)

	gap := chartWidth - len(firstLabel) - len(lastLabel)
	if gap < 1 {
		gap = 1
	}

	return " " + theme.DimStyle.Render(firstLabel+strings.Repeat(" ", gap)+lastLabel)
}

// --- Status Breakdown ---

// allStatusCategories defines the full set of invocation statuses to always show.
var allStatusCategories = []struct {
	key         string
	displayName string
	color       lipgloss.Color
}{
	{"success", "success", theme.ColorGreen},
	{"scriptThrewException", "exception", theme.ColorRed},
	{"exceededResources", "exceeded resources", theme.ColorRed},
	{"internalError", "internal error", theme.ColorRed},
	{"clientDisconnected", "client disconnected", theme.ColorYellow},
}

func (a AnalyticsModel) viewStatusBreakdown(width int) string {
	m := a.metrics

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.ColorWhite)
	header := " " + headerStyle.Render("Invocation Status")

	var lines []string
	lines = append(lines, header)

	for _, cat := range allStatusCategories {
		count := m.StatusCounts[cat.key]

		// Percentage bar
		pct := float64(0)
		if m.TotalRequests > 0 {
			pct = float64(count) / float64(m.TotalRequests) * 100
		}

		barWidth := width - 42
		if barWidth < 10 {
			barWidth = 10
		}
		filledWidth := int(math.Round(pct / 100 * float64(barWidth)))
		if filledWidth > barWidth {
			filledWidth = barWidth
		}

		bar := lipgloss.NewStyle().Foreground(cat.color).Render(strings.Repeat("█", filledWidth))
		bar += strings.Repeat("░", barWidth-filledWidth)

		nameStr := lipgloss.NewStyle().Foreground(cat.color).Render(fmt.Sprintf("  %-22s", cat.displayName))
		countStr := theme.DimStyle.Render(fmt.Sprintf("%8s", formatCount(count)))
		pctStr := theme.DimStyle.Render(fmt.Sprintf(" %5.1f%%", pct))

		lines = append(lines, nameStr+countStr+pctStr+" "+bar)
	}

	// Show any unexpected statuses from the data that aren't in our predefined list
	knownKeys := make(map[string]bool)
	for _, cat := range allStatusCategories {
		knownKeys[cat.key] = true
	}
	for key, count := range m.StatusCounts {
		if knownKeys[key] || count == 0 {
			continue
		}
		pct := float64(0)
		if m.TotalRequests > 0 {
			pct = float64(count) / float64(m.TotalRequests) * 100
		}
		nameStr := lipgloss.NewStyle().Foreground(theme.ColorGray).Render(fmt.Sprintf("  %-22s", key))
		countStr := theme.DimStyle.Render(fmt.Sprintf("%8s", formatCount(count)))
		pctStr := theme.DimStyle.Render(fmt.Sprintf(" %5.1f%%", pct))
		lines = append(lines, nameStr+countStr+pctStr)
	}

	return strings.Join(lines, "\n")
}

// --- Error Log ---

func (a AnalyticsModel) viewErrorLog(width int) string {
	m := a.metrics
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.ColorRed)
	header := " " + headerStyle.Render(fmt.Sprintf("Recent Errors (%d)", len(m.Errors)))

	var lines []string
	lines = append(lines, header)

	// Show up to 20 errors; note if truncated
	maxShow := 20
	if maxShow > len(m.Errors) {
		maxShow = len(m.Errors)
	}

	for i := 0; i < maxShow; i++ {
		e := m.Errors[i]
		ts := theme.LogTimestampStyle.Render(e.Datetime.Format(time.DateTime))
		desc := errorStatusDescription(e.Status)
		status := lipgloss.NewStyle().Foreground(theme.ColorRed).Render(desc)
		count := theme.DimStyle.Render(fmt.Sprintf("x%d", e.Count))
		lines = append(lines, fmt.Sprintf("  %s  %s %s", ts, status, count))
	}

	if len(m.Errors) > maxShow {
		lines = append(lines, theme.DimStyle.Render(fmt.Sprintf("  ... and %d more", len(m.Errors)-maxShow)))
	}

	return strings.Join(lines, "\n")
}

// errorStatusDescription returns a human-readable description for a CF worker error status.
func errorStatusDescription(status string) string {
	switch status {
	case "scriptThrewException":
		return "Script threw exception (uncaught error in worker code)"
	case "exceededResources":
		return "Exceeded resources (CPU/memory limit hit)"
	case "internalError":
		return "Internal error (Cloudflare platform error)"
	case "clientDisconnected":
		return "Client disconnected (request cancelled by caller)"
	case "exceededCpu":
		return "Exceeded CPU time limit"
	case "canceled":
		return "Request canceled"
	default:
		return status
	}
}

// --- Formatting helpers ---

func formatCount(n int64) string {
	if n >= 1_000_000_000 {
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func formatCPUTime(us float64) string {
	if us >= 1_000_000 {
		return fmt.Sprintf("%.1fs", us/1_000_000)
	}
	if us >= 1_000 {
		return fmt.Sprintf("%.1fms", us/1_000)
	}
	return fmt.Sprintf("%.0fus", us)
}

func formatTimeLabel(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	now := time.Now()
	if t.YearDay() == now.YearDay() && t.Year() == now.Year() {
		return t.Format("15:04")
	}
	return t.Format("Jan 02 15:04")
}

// downsampleChartBuckets reduces the number of chart buckets by merging adjacent ones.
func downsampleChartBuckets(buckets []chartBucket, target int) []chartBucket {
	if len(buckets) <= target || target <= 0 {
		return buckets
	}

	step := float64(len(buckets)) / float64(target)
	result := make([]chartBucket, target)

	for i := 0; i < target; i++ {
		start := int(float64(i) * step)
		end := int(float64(i+1) * step)
		if end > len(buckets) {
			end = len(buckets)
		}
		if start >= end {
			continue
		}

		result[i].dt = buckets[start].dt
		for j := start; j < end; j++ {
			result[i].requests += buckets[j].requests
			result[i].errors += buckets[j].errors
		}
	}

	return result
}
