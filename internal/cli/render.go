package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/varavelio/rienda/internal/engine"
)

// render consumes the events of a run, writing the assistant text to stdout
// and the tool activity to stderr. It returns an error when the run does not
// end by finishing its turn.
func render(events <-chan engine.Event, stdout, stderr io.Writer) error {
	streamed := map[string]bool{}
	pending := false
	var failure error

	for event := range events {
		switch event.Type {
		case engine.EventTextDelta:
			if _, err := io.WriteString(stdout, event.Text); err != nil {
				return fmt.Errorf("render: write assistant text: %w", err)
			}
			if event.Text != "" {
				pending = !strings.HasSuffix(event.Text, "\n")
			}
		case engine.EventToolCall:
			if err := flushLine(stdout, &pending); err != nil {
				return err
			}
			label := "tool: " + event.ToolName
			if len(event.Arguments) > 0 {
				label += " " + string(event.Arguments)
			}
			if _, err := fmt.Fprintln(stderr, label); err != nil {
				return fmt.Errorf("render: write tool call: %w", err)
			}
		case engine.EventToolOutput:
			streamed[event.ToolCallID] = true
			if _, err := io.WriteString(stderr, event.Output); err != nil {
				return fmt.Errorf("render: write tool output: %w", err)
			}
		case engine.EventToolResult:
			if err := renderToolResult(stderr, event, streamed[event.ToolCallID]); err != nil {
				return err
			}
		case engine.EventMessageEnd:
			if err := flushLine(stdout, &pending); err != nil {
				return err
			}
		case engine.EventError:
			failure = errors.New(event.Error)
		case engine.EventRunEnd:
			if err := flushLine(stdout, &pending); err != nil {
				return err
			}
			return runError(event.Reason, failure)
		}
	}
	return nil
}

// runError translates the end reason of a run into the command error.
func runError(reason engine.EndReason, failure error) error {
	switch reason {
	case engine.EndReasonTurn:
		return nil
	case engine.EndReasonInterrupted:
		return errors.New("the run was interrupted")
	default:
		if failure != nil {
			return failure
		}
		return errors.New("the run failed")
	}
}

// renderToolResult reports the end of a tool invocation. Results already
// visible as streamed output are not repeated.
func renderToolResult(w io.Writer, event engine.Event, streamed bool) error {
	switch {
	case !streamed && event.Text != "":
		label := "tool: "
		if event.IsError {
			label = "tool failed: "
		}
		if _, err := fmt.Fprintln(w, label+event.Text); err != nil {
			return fmt.Errorf("render: write tool result: %w", err)
		}
	case event.IsError:
		if _, err := fmt.Fprintln(w, "tool failed"); err != nil {
			return fmt.Errorf("render: write tool result: %w", err)
		}
	}
	return nil
}

// flushLine ends the current stdout line when a run wrote a partial one.
func flushLine(w io.Writer, pending *bool) error {
	if !*pending {
		return nil
	}
	*pending = false
	if _, err := fmt.Fprintln(w); err != nil {
		return fmt.Errorf("render: write newline: %w", err)
	}
	return nil
}
