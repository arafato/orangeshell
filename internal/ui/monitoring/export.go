package monitoring

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	svc "github.com/oarafat/orangeshell/internal/service"
)

// LogExporter writes tail log lines to files in ~/.orangeshell/logs/.
// It maintains a combined file (all workers interleaved) and per-worker files.
//
// Usage:
//
//	exporter := NewLogExporter()
//	exporter.Start()            // creates files, opens handles
//	exporter.WriteLines(...)    // called from app layer on each TailLogMsg / DevOutputMsg
//	exporter.Stop()             // closes all file handles
type LogExporter struct {
	mu        sync.Mutex
	active    bool
	startTs   string // timestamp string used in filenames
	combined  *os.File
	perWorker map[string]*os.File
	logDir    string
}

// NewLogExporter creates a new exporter. Call Start() to begin writing.
func NewLogExporter() *LogExporter {
	return &LogExporter{
		perWorker: make(map[string]*os.File),
	}
}

// IsActive returns whether the exporter is currently writing.
func (e *LogExporter) IsActive() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.active
}

// Start creates timestamped log files and begins capturing.
func (e *LogExporter) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.active {
		return nil // already running
	}

	// Determine log directory
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	e.logDir = filepath.Join(home, ".orangeshell", "logs")

	if err := os.MkdirAll(e.logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	e.startTs = time.Now().Format("20060102-150405")

	// Open the combined log file
	combinedPath := filepath.Join(e.logDir, fmt.Sprintf("export-%s-combined.log", e.startTs))
	f, err := os.OpenFile(combinedPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to create combined log: %w", err)
	}
	e.combined = f
	e.perWorker = make(map[string]*os.File)
	e.active = true

	return nil
}

// Stop closes all file handles and stops capturing.
func (e *LogExporter) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.active {
		return
	}

	if e.combined != nil {
		e.combined.Close()
		e.combined = nil
	}
	for _, f := range e.perWorker {
		f.Close()
	}
	e.perWorker = make(map[string]*os.File)
	e.active = false
}

// WriteLines writes log lines for a given worker to both the combined and
// per-worker files. Safe to call from any goroutine.
func (e *LogExporter) WriteLines(workerName string, lines []svc.TailLine) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.active {
		return
	}

	// Ensure per-worker file exists
	workerFile := e.ensureWorkerFile(workerName)

	for _, line := range lines {
		formatted := formatLogLine(workerName, line)

		// Write to combined file
		if e.combined != nil {
			fmt.Fprintln(e.combined, formatted)
		}

		// Write to per-worker file
		if workerFile != nil {
			// Per-worker lines don't need the worker name prefix
			perWorkerFormatted := formatLogLineNoWorker(line)
			fmt.Fprintln(workerFile, perWorkerFormatted)
		}
	}
}

// LogDir returns the directory where log files are written.
func (e *LogExporter) LogDir() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.logDir
}

// ensureWorkerFile creates the per-worker file if it doesn't exist yet.
// Must be called with e.mu held.
func (e *LogExporter) ensureWorkerFile(workerName string) *os.File {
	if f, ok := e.perWorker[workerName]; ok {
		return f
	}

	// Sanitize worker name for filename (replace special chars)
	safeName := sanitizeFilename(workerName)
	path := filepath.Join(e.logDir, fmt.Sprintf("export-%s-%s.log", e.startTs, safeName))

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return nil // silently skip — don't fail the whole export for one worker
	}
	e.perWorker[workerName] = f
	return f
}

// formatLogLine formats a log line for the combined file.
// Format: 2024-01-15T10:30:45.123Z [worker-name] [level] message
func formatLogLine(workerName string, line svc.TailLine) string {
	ts := line.Timestamp.UTC().Format(time.RFC3339Nano)
	return fmt.Sprintf("%s [%s] [%s] %s", ts, workerName, line.Level, line.Text)
}

// formatLogLineNoWorker formats a log line without the worker name prefix.
// Format: 2024-01-15T10:30:45.123Z [level] message
func formatLogLineNoWorker(line svc.TailLine) string {
	ts := line.Timestamp.UTC().Format(time.RFC3339Nano)
	return fmt.Sprintf("%s [%s] %s", ts, line.Level, line.Text)
}

// sanitizeFilename replaces characters that are invalid in filenames.
func sanitizeFilename(name string) string {
	// Strip dev: prefix for cleaner filenames
	name = strings.TrimPrefix(name, "dev:")

	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		":", "-",
		"*", "-",
		"?", "-",
		"\"", "-",
		"<", "-",
		">", "-",
		"|", "-",
		" ", "-",
	)
	return replacer.Replace(name)
}
