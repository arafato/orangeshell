package ai

import (
	"fmt"
	"sort"
	"strings"
)

// systemPromptTemplate is the base system prompt for the AI log analyst.
const systemPromptTemplate = `You are an expert Cloudflare Workers log analyst integrated into the orangeshell TUI tool. You help developers debug and understand their Workers deployment.

## Your Capabilities
- Analyze log output from Cloudflare Workers tail sessions
- Identify errors, patterns, and correlations across workers
- Trace request chains in distributed Worker architectures
- Suggest concrete fixes based on error patterns
- Explain Cloudflare-specific behaviors (subrequests, bindings, limits)

## Instructions
- Reference specific log lines by timestamp when explaining issues
- If logs show request chains across workers, trace the full request path
- Be concise but thorough — developers are debugging in a terminal
- When you see error patterns, explain the likely root cause and suggest fixes
- If you see binding-related errors (KV, D1, R2, Queues), explain the binding context
- When analyzing logs from multiple workers, look for correlated events (same cf-ray, trace IDs, timestamps)`

// maxContextChars is the approximate character budget for log context.
// Leaves room for system prompt, conversation history, and response.
// ~120K chars ≈ 30K tokens, well within 128K context window.
const maxContextChars = 120000

// BuildSystemPrompt constructs the system prompt with context from selected log sources.
func BuildSystemPrompt(sources []ContextSourceData) string {
	if len(sources) == 0 {
		return systemPromptTemplate + "\n\nNo log context is currently selected. The user may ask general Cloudflare Workers questions."
	}

	var sb strings.Builder
	sb.WriteString(systemPromptTemplate)

	// Worker names
	sb.WriteString("\n\n## Active Workers\n")
	for _, s := range sources {
		sb.WriteString(fmt.Sprintf("- %s", s.Name))
		if s.IsDev {
			sb.WriteString(" [dev mode]")
		}
		sb.WriteString("\n")
	}

	// Cross-worker analysis note
	if len(sources) > 1 {
		sb.WriteString("\n**Note:** Multiple workers are selected. Look for correlated events across workers ")
		sb.WriteString("(e.g., requests from one worker triggering actions in another via Service Bindings, ")
		sb.WriteString("Queues, or fetch calls).\n")
	}

	// Interleaved log context with smart truncation
	sb.WriteString("\n## Log Context\n")
	sb.WriteString("The following logs are from the selected workers, interleaved chronologically.\n\n")

	totalLines := 0
	for _, s := range sources {
		totalLines += len(s.Lines)
	}

	if totalLines == 0 {
		sb.WriteString("(No log lines captured yet — tailing is active but no events received)\n")
	} else {
		// Apply smart truncation if needed
		truncatedSources := TruncateContext(sources, maxContextChars)
		lines := InterleaveLines(truncatedSources)

		sb.WriteString("```\n")
		for _, line := range lines {
			sb.WriteString(line)
			sb.WriteString("\n")
		}
		sb.WriteString("```\n")

		if totalLines > len(lines) {
			sb.WriteString(fmt.Sprintf("\n(Showing most recent %d of %d total lines — older lines truncated to fit context window)\n",
				len(lines), totalLines))
		}
	}

	return sb.String()
}

// ContextSourceData holds the log data from a single context source.
type ContextSourceData struct {
	Name  string
	IsDev bool
	Lines []TimestampedLine
}

// TimestampedLine is a single log line with its timestamp for interleaving.
type TimestampedLine struct {
	Timestamp int64  // Unix milliseconds
	Source    string // Worker name
	Text      string // The log line text
}

// InterleaveLines merges log lines from multiple sources sorted by timestamp,
// with a [worker-name] prefix on each line.
func InterleaveLines(sources []ContextSourceData) []string {
	// Collect all lines
	var all []TimestampedLine
	for _, s := range sources {
		all = append(all, s.Lines...)
	}

	// Sort by timestamp
	sort.Slice(all, func(i, j int) bool {
		return all[i].Timestamp < all[j].Timestamp
	})

	// Format
	result := make([]string, len(all))
	for i, line := range all {
		result[i] = fmt.Sprintf("[%s] %s", line.Source, line.Text)
	}
	return result
}

// TruncateContext applies smart truncation to keep context within the char budget.
// It distributes the budget evenly across sources, keeping the most recent lines
// from each source. Error lines are prioritized.
func TruncateContext(sources []ContextSourceData, maxChars int) []ContextSourceData {
	// First, check if we're within budget
	totalChars := 0
	for _, s := range sources {
		for _, line := range s.Lines {
			totalChars += len(line.Source) + len(line.Text) + 5 // brackets, space, newline
		}
	}

	if totalChars <= maxChars {
		return sources // within budget, no truncation needed
	}

	// Budget per source (evenly distributed)
	perSourceChars := maxChars / len(sources)

	result := make([]ContextSourceData, len(sources))
	for i, s := range sources {
		result[i] = ContextSourceData{
			Name:  s.Name,
			IsDev: s.IsDev,
		}

		if len(s.Lines) == 0 {
			continue
		}

		// Prioritize error lines and most recent lines
		// Strategy: take most recent lines until we hit the per-source budget,
		// but always include error/exception lines from anywhere in the buffer.
		errorLines := extractErrorLines(s.Lines)
		recentLines := s.Lines

		// Calculate how many recent lines we can afford
		charBudget := perSourceChars
		var errorChars int
		for _, el := range errorLines {
			errorChars += len(el.Source) + len(el.Text) + 5
		}
		recentBudget := charBudget - errorChars
		if recentBudget < 0 {
			recentBudget = charBudget / 2 // at least half for recent
		}

		// Take from the end (most recent)
		var truncated []TimestampedLine
		usedChars := 0
		for j := len(recentLines) - 1; j >= 0; j-- {
			lineChars := len(recentLines[j].Source) + len(recentLines[j].Text) + 5
			if usedChars+lineChars > recentBudget {
				break
			}
			truncated = append([]TimestampedLine{recentLines[j]}, truncated...)
			usedChars += lineChars
		}

		// Merge error lines that aren't already in the recent window
		if len(truncated) > 0 {
			minTS := truncated[0].Timestamp
			for _, el := range errorLines {
				if el.Timestamp < minTS {
					truncated = append([]TimestampedLine{el}, truncated...)
				}
			}
		}

		result[i].Lines = truncated
	}

	return result
}

// extractErrorLines returns lines that contain error/exception indicators.
func extractErrorLines(lines []TimestampedLine) []TimestampedLine {
	var errors []TimestampedLine
	for _, l := range lines {
		lower := strings.ToLower(l.Text)
		if strings.Contains(lower, "error") ||
			strings.Contains(lower, "exception") ||
			strings.Contains(lower, "fatal") ||
			strings.Contains(lower, "panic") ||
			strings.Contains(lower, "status: 5") || // 5xx status codes
			strings.Contains(lower, "failed") {
			errors = append(errors, l)
		}
	}
	return errors
}

// EstimateTokens gives a rough token count estimate (~4 chars per token).
func EstimateTokens(text string) int {
	return len(text) / 4
}
