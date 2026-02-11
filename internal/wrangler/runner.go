package wrangler

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Command represents a wrangler CLI command to execute.
type Command struct {
	Action     string // "deploy", "rollback", "versions list", "deployments status"
	ConfigPath string // --config <path>
	EnvName    string // --env <name> (empty for default)
}

// OutputLine is a single line of output from a wrangler command.
type OutputLine struct {
	Text      string
	IsStderr  bool
	Timestamp time.Time
}

// RunResult holds the final result of a command execution.
type RunResult struct {
	ExitCode int
	Err      error
}

// Runner manages the execution of a single wrangler CLI command.
// A Runner is single-use: after Start() completes (either successfully or not),
// the Runner cannot be restarted. Create a new Runner for each command.
type Runner struct {
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	linesCh chan OutputLine
	doneCh  chan RunResult

	mu      sync.Mutex
	started bool // once true, never reset — enforces single-use
	running bool
}

// NewRunner creates a Runner. It does NOT start the command — call Start().
// Each Runner is single-use.
func NewRunner() *Runner {
	return &Runner{
		linesCh: make(chan OutputLine, 256),
		doneCh:  make(chan RunResult, 1),
	}
}

// buildArgs constructs the wrangler CLI arguments for a command.
func buildArgs(wcmd Command) []string {
	args := []string{"wrangler"}

	// Split multi-word actions like "versions list" or "deployments status"
	parts := strings.Fields(wcmd.Action)
	args = append(args, parts...)

	if wcmd.ConfigPath != "" {
		args = append(args, "--config", wcmd.ConfigPath)
	}
	// Always pass --env to explicitly target the environment.
	// For default/top-level env, pass --env="" to avoid ambiguity.
	if wcmd.EnvName == "" || wcmd.EnvName == "default" {
		args = append(args, "--env=")
	} else {
		args = append(args, "--env", wcmd.EnvName)
	}

	return args
}

// scannerBufSize is the max line size for reading command output (1 MB).
const scannerBufSize = 1024 * 1024

// Start begins executing the wrangler command in a background goroutine.
// Output lines are sent to LinesCh(). When all output is consumed,
// LinesCh() is closed. Then the final RunResult is sent to DoneCh().
func (r *Runner) Start(ctx context.Context, wcmd Command) error {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return fmt.Errorf("runner already used — create a new Runner")
	}
	r.started = true
	r.mu.Unlock()

	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel

	args := buildArgs(wcmd)
	r.cmd = exec.CommandContext(runCtx, "npx", args...)

	stdout, err := r.cmd.StdoutPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := r.cmd.StderrPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := r.cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("failed to start wrangler: %w", err)
	}

	r.mu.Lock()
	r.running = true
	r.mu.Unlock()

	// Read stdout and stderr concurrently, merge into linesCh
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, scannerBufSize), scannerBufSize)
		for scanner.Scan() {
			line := OutputLine{
				Text:      scanner.Text(),
				IsStderr:  false,
				Timestamp: time.Now(),
			}
			select {
			case r.linesCh <- line:
			case <-runCtx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil && runCtx.Err() == nil {
			r.linesCh <- OutputLine{
				Text:      fmt.Sprintf("[read error: %s]", err),
				IsStderr:  true,
				Timestamp: time.Now(),
			}
		}
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, scannerBufSize), scannerBufSize)
		for scanner.Scan() {
			line := OutputLine{
				Text:      scanner.Text(),
				IsStderr:  true,
				Timestamp: time.Now(),
			}
			select {
			case r.linesCh <- line:
			case <-runCtx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil && runCtx.Err() == nil {
			r.linesCh <- OutputLine{
				Text:      fmt.Sprintf("[read error: %s]", err),
				IsStderr:  true,
				Timestamp: time.Now(),
			}
		}
	}()

	// Wait for command completion in a goroutine.
	// Close linesCh FIRST so the consumer can drain all output,
	// then send the result to doneCh.
	go func() {
		wg.Wait()

		// Close lines channel first — consumer drains output before reading done
		close(r.linesCh)

		err := r.cmd.Wait()
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				// Non-ExitError (context cancelled, signal, etc.)
				exitCode = -1
			}
		}

		r.mu.Lock()
		r.running = false
		r.mu.Unlock()

		r.doneCh <- RunResult{ExitCode: exitCode, Err: err}
		close(r.doneCh)
	}()

	return nil
}

// Stop cancels the running command.
func (r *Runner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cancel != nil {
		r.cancel()
	}
}

// IsRunning returns whether a command is currently executing.
func (r *Runner) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// LinesCh returns the channel that receives output lines.
// This channel is closed when all output has been sent (before doneCh fires).
func (r *Runner) LinesCh() <-chan OutputLine {
	return r.linesCh
}

// DoneCh returns the channel that receives the final result.
// This channel fires after linesCh is closed.
func (r *Runner) DoneCh() <-chan RunResult {
	return r.doneCh
}

// CommandLabel returns a human-readable label for a command action.
func CommandLabel(action string) string {
	switch action {
	case "deploy":
		return "Deploy"
	case "rollback":
		return "Rollback"
	case "versions list":
		return "List Versions"
	case "deployments status":
		return "Deployment Status"
	default:
		return action
	}
}

// CommandDescription returns a short description for a command action.
func CommandDescription(action string) string {
	switch action {
	case "deploy":
		return "Deploy worker to Cloudflare"
	case "rollback":
		return "Rollback to previous version"
	case "versions list":
		return "Show recent versions"
	case "deployments status":
		return "Show current deployment status"
	default:
		return ""
	}
}
