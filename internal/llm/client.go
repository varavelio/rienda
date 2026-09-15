package llm

import "context"

// Client generates model responses, either complete or streamed. Providers in
// internal/provider implement it for each supported vendor API.
type Client interface {
	// Generate returns the full response once generation completes.
	Generate(ctx context.Context, req *Request) (*Response, error)

	// Stream returns a handle to consume the response incrementally. The
	// returned Stream must be closed by the caller.
	Stream(ctx context.Context, req *Request) (Stream, error)
}
