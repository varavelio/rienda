package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/varavelio/rienda/internal/engine"
)

// render consumes the events of a run, writing the assistant text to stdout
// and the tool activity and retry notices to stderr. It returns an error when
// the run does not end by finishing its turn.
//
// The text of the response in flight is held back and written when the
// response completes or when the run ends, so a transient failure that
// discards a partial response leaves nothing of it on standard output: the
// retried response is the only one printed.
func render(events <-chan engine.Event, stdout, stderr io.Writer) error {
	streamed := map[string]bool{}
	pending := false
	var attempt strings.Builder
	var failure error

	flush := func() error {
		if attempt.Len() == 0 {
			return nil
		}
		text := attempt.String()
		attempt.Reset()
		if _, err := io.WriteString(stdout, text); err != nil {
			return fmt.Errorf("render: write assistant text: %w", err)
		}
		pending = !strings.HasSuffix(text, "\n")
		return nil
	}

	for event := range events {
		switch event.Type {
		case engine.EventTextDelta:
			attempt.WriteString(event.Text)
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
			if err := flush(); err != nil {
				return err
			}
			if err := flushLine(stdout, &pending); err != nil {
				return err
			}
		case engine.EventRetry:
			if event.Discard {
				attempt.Reset()
			}
			if err := renderRetry(stderr, event); err != nil {
				return err
			}
		case engine.EventError:
			failure = errors.New(event.Error)
		case engine.EventRunEnd:
			if err := flush(); err != nil {
				return err
			}
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

// renderRetry reports a model call that failed with a transient error and is
// being retried. A retry that discarded a partial response says that the
// response restarts, because the partial answer is not written to stdout.
func renderRetry(w io.Writer, event engine.Event) error {
	action := "retrying"
	if event.Discard {
		action = "restarting the response"
	}
	if _, err := fmt.Fprintf(
		w,
		"transient error, %s in %s: %s\n",
		action,
		event.RetryIn.Round(time.Millisecond),
		event.Error,
	); err != nil {
		return fmt.Errorf("render: write retry notice: %w", err)
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
