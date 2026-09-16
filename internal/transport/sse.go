package transport

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// sseMaxLineBytes caps a single SSE line, keeping pathological streams from
// exhausting memory.
const sseMaxLineBytes = 10 * 1024 * 1024

// SSEEvent is a single parsed server-sent event.
type SSEEvent struct {
	// Name is the event type from the "event" field. It is empty when the
	// stream only carries data lines, as with OpenAI chat chunks.
	Name string
	// Data is the concatenation of all "data" lines joined with "\n".
	Data string
}

// SSEScanner parses a server-sent events stream. Blank lines dispatch events,
// lines starting with ":" are keep-alive comments, and the final event is
// flushed even when the stream ends without a trailing blank line.
type SSEScanner struct {
	scanner *bufio.Scanner
}

// NewSSEScanner wraps r with an SSE parser.
func NewSSEScanner(r io.Reader) *SSEScanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), sseMaxLineBytes)
	return &SSEScanner{scanner: scanner}
}

// Next returns the next event in the stream, or io.EOF when the stream ends.
func (s *SSEScanner) Next() (SSEEvent, error) {
	var event SSEEvent
	var lines []string
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if line == "" {
			// Skip stray blank lines with no pending event.
			if len(lines) == 0 && event.Name == "" {
				continue
			}
			event.Data = strings.Join(lines, "\n")
			return event, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if found {
			value = strings.TrimPrefix(value, " ")
		}
		switch name {
		case "event":
			event.Name = value
		case "data":
			lines = append(lines, value)
		}
	}
	if err := s.scanner.Err(); err != nil {
		return SSEEvent{}, fmt.Errorf("transport: scan SSE stream: %w", err)
	}
	if len(lines) > 0 || event.Name != "" {
		event.Data = strings.Join(lines, "\n")
		return event, nil
	}
	return SSEEvent{}, io.EOF
}
