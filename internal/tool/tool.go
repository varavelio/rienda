package tool

import (
	"context"
	"encoding/json"

	"github.com/varavelio/rienda/internal/llm"
)

// Tool is a capability the model can invoke during a conversation.
//
// Implementations must be safe for concurrent use: the engine may run several
// invocations of the same tool at once.
type Tool interface {
	// Definition describes the tool to the model: its name, its purpose and
	// the JSON Schema of its arguments.
	Definition() llm.Tool

	// Execute runs one invocation. Output produced while the invocation is in
	// flight is streamed to out, and the returned Result carries what the
	// model reads. A non-nil error means the invocation could not run at all,
	// for example because the arguments are malformed, while Result.IsError
	// marks an invocation that ran and failed.
	Execute(ctx context.Context, call Call, out Sink) (Result, error)
}

// Call is a single tool invocation requested by the model.
type Call struct {
	// ID is the provider-assigned identifier of the call.
	ID string

	// Name is the name of the invoked tool.
	Name string

	// Arguments holds the invocation arguments as a JSON object.
	Arguments json.RawMessage
}

// Result is the outcome of a tool invocation, ready to be sent back to the
// model as a tool result message.
type Result struct {
	// Blocks is the ordered content of the result. Built-in tools produce a
	// single text block.
	Blocks []llm.Block

	// IsError marks an invocation that ran but failed, for example a command
	// that exited with a non-zero status.
	IsError bool
}

// TextResult returns a successful result carrying a single text block.
func TextResult(text string) Result {
	return Result{Blocks: []llm.Block{{Type: llm.BlockText, Text: text}}}
}

// ErrorResult returns a failed result carrying a single text block.
func ErrorResult(text string) Result {
	return Result{Blocks: []llm.Block{{Type: llm.BlockText, Text: text}}, IsError: true}
}

// Stream identifies the output stream a tool writes to.
type Stream string

const (
	// StreamStdout carries standard output chunks.
	StreamStdout Stream = "stdout"
	// StreamStderr carries standard error chunks.
	StreamStderr Stream = "stderr"
)

// Sink receives the incremental output of a running tool.
//
// Implementations are called from the goroutines that produce output, so they
// must be safe for concurrent use. The data slice is only valid for the
// duration of the call: retain a copy to keep it.
type Sink interface {
	// Emit reports a chunk of output produced on stream.
	Emit(stream Stream, data []byte)
}

// workdirKey is the context key carrying the working directory of an invocation.
type workdirKey struct{}

// WithWorkdir attaches dir as the working directory of the tools invoked with
// ctx. Tools fall back to their configured directory when it is absent.
func WithWorkdir(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, workdirKey{}, dir)
}

// WorkdirFromContext returns the working directory attached to ctx, if any.
func WorkdirFromContext(ctx context.Context) (string, bool) {
	dir, ok := ctx.Value(workdirKey{}).(string)
	if !ok || dir == "" {
		return "", false
	}
	return dir, true
}
