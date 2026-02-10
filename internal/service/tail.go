package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	cloudflare "github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/workers"
	"github.com/gorilla/websocket"
)

const (
	tailMaxLines = 200
)

// TailLine represents a single formatted log line from a tail session.
type TailLine struct {
	Timestamp time.Time
	Level     string // "log", "info", "warn", "error", "request", "exception", "system"
	Text      string
}

// TailSession manages a live tail connection to a Worker's log stream.
type TailSession struct {
	ID         string
	ScriptName string
	AccountID  string
	ExpiresAt  string

	conn    *websocket.Conn
	cancel  context.CancelFunc
	linesCh chan []TailLine // sends batches of new lines to the consumer

	mu    sync.Mutex
	lines []TailLine // ring buffer
}

// Lines returns a copy of the current log buffer.
func (t *TailSession) Lines() []TailLine {
	t.mu.Lock()
	defer t.mu.Unlock()
	cp := make([]TailLine, len(t.lines))
	copy(cp, t.lines)
	return cp
}

// LinesChan returns the channel that receives new tail line batches.
// The consumer should read from this channel to get live updates.
func (t *TailSession) LinesChan() <-chan []TailLine {
	return t.linesCh
}

// appendLines adds lines to the ring buffer, evicting old ones if over capacity.
func (t *TailSession) appendLines(newLines []TailLine) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, newLines...)
	if len(t.lines) > tailMaxLines {
		t.lines = t.lines[len(t.lines)-tailMaxLines:]
	}
}

// Stop closes the websocket connection and cancels the reader goroutine.
func (t *TailSession) Stop() {
	if t.cancel != nil {
		t.cancel()
	}
	if t.conn != nil {
		t.conn.Close()
	}
}

// StartTail creates a new tail session for the given Worker script.
// It calls the Cloudflare API to create the tail, connects to the websocket,
// and starts a background goroutine to read log messages.
func StartTail(ctx context.Context, client *cloudflare.Client, accountID, scriptName string) (*TailSession, error) {
	// Create the tail via the API
	resp, err := client.Workers.Scripts.Tail.New(ctx, scriptName, workers.ScriptTailNewParams{
		AccountID: cloudflare.F(accountID),
		Body:      map[string]interface{}{},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create tail: %w", err)
	}

	// Connect to the websocket URL
	header := http.Header{}
	header.Set("Sec-WebSocket-Protocol", "trace-v1")

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, resp.URL, header)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to tail websocket: %w", err)
	}

	// Send initial config message
	configMsg := map[string]interface{}{"debug": false}
	if err := conn.WriteJSON(configMsg); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send tail config: %w", err)
	}

	readerCtx, cancel := context.WithCancel(ctx)

	session := &TailSession{
		ID:         resp.ID,
		ScriptName: scriptName,
		AccountID:  accountID,
		ExpiresAt:  resp.ExpiresAt,
		conn:       conn,
		cancel:     cancel,
		linesCh:    make(chan []TailLine, 64),
	}

	// Start reader goroutine
	go session.readLoop(readerCtx)

	return session, nil
}

// StopTail stops a tail session and deletes it from the API.
func StopTail(ctx context.Context, client *cloudflare.Client, session *TailSession) {
	session.Stop()

	// Best-effort delete — don't block on errors
	deleteCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, _ = client.Workers.Scripts.Tail.Delete(deleteCtx, session.ScriptName, session.ID, workers.ScriptTailDeleteParams{
		AccountID: cloudflare.F(session.AccountID),
	})
}

// tailEventMessage represents a single event from the Workers tail websocket.
type tailEventMessage struct {
	Outcome        string          `json:"outcome"`
	ScriptName     string          `json:"scriptName"`
	EventTimestamp float64         `json:"eventTimestamp"`
	Event          tailEvent       `json:"event"`
	Logs           []tailLogEntry  `json:"logs"`
	Exceptions     []tailException `json:"exceptions"`
}

type tailEvent struct {
	Request *tailRequest `json:"request,omitempty"`
}

type tailRequest struct {
	URL    string `json:"url"`
	Method string `json:"method"`
}

type tailLogEntry struct {
	Message   json.RawMessage `json:"message"`
	Level     string          `json:"level"`
	Timestamp float64         `json:"timestamp"`
}

type tailException struct {
	Name      string  `json:"name"`
	Message   string  `json:"message"`
	Timestamp float64 `json:"timestamp"`
}

// readLoop reads messages from the websocket and sends parsed lines to the channel.
func (t *TailSession) readLoop(ctx context.Context) {
	defer close(t.linesCh)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_, message, err := t.conn.ReadMessage()
		if err != nil {
			// Connection closed or error — send a system message and exit
			if ctx.Err() == nil {
				// Only report if we didn't cancel ourselves
				sysLine := TailLine{
					Timestamp: time.Now(),
					Level:     "system",
					Text:      "Tail connection closed",
				}
				t.appendLines([]TailLine{sysLine})
				select {
				case t.linesCh <- []TailLine{sysLine}:
				default:
				}
			}
			return
		}

		var event tailEventMessage
		if err := json.Unmarshal(message, &event); err != nil {
			continue // skip unparseable messages
		}

		lines := t.parseEvent(event)
		if len(lines) == 0 {
			continue
		}

		t.appendLines(lines)

		// Non-blocking send to channel
		select {
		case t.linesCh <- lines:
		default:
			// Consumer is slow — drop this batch (they can read from Lines())
		}
	}
}

// parseEvent converts a tail event message into formatted TailLines.
func (t *TailSession) parseEvent(event tailEventMessage) []TailLine {
	var lines []TailLine
	ts := time.UnixMilli(int64(event.EventTimestamp))

	// Request line
	if event.Event.Request != nil {
		req := event.Event.Request
		outcome := event.Outcome
		text := fmt.Sprintf("%s %s  %s", req.Method, req.URL, outcome)
		lines = append(lines, TailLine{
			Timestamp: ts,
			Level:     "request",
			Text:      text,
		})
	}

	// Log entries
	for _, log := range event.Logs {
		logTs := time.UnixMilli(int64(log.Timestamp))
		text := formatLogMessage(log.Message)
		lines = append(lines, TailLine{
			Timestamp: logTs,
			Level:     log.Level,
			Text:      text,
		})
	}

	// Exceptions
	for _, exc := range event.Exceptions {
		excTs := time.UnixMilli(int64(exc.Timestamp))
		text := fmt.Sprintf("%s: %s", exc.Name, exc.Message)
		lines = append(lines, TailLine{
			Timestamp: excTs,
			Level:     "exception",
			Text:      text,
		})
	}

	return lines
}

// formatLogMessage converts the raw JSON message array into a readable string.
// console.log can take multiple arguments, which appear as a JSON array.
func formatLogMessage(raw json.RawMessage) string {
	// Try to unmarshal as array of interface{}
	var arr []interface{}
	if err := json.Unmarshal(raw, &arr); err == nil {
		parts := make([]string, len(arr))
		for i, v := range arr {
			switch val := v.(type) {
			case string:
				parts[i] = val
			default:
				b, _ := json.Marshal(val)
				parts[i] = string(b)
			}
		}
		return strings.Join(parts, " ")
	}

	// Fallback: try as a single string
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	// Fallback: raw JSON
	return string(raw)
}
