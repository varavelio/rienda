package compaction

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/retry"
)

// Blocks of the summarization request. The conversation and the previous
// summary travel as plain text inside their own blocks, so the model can tell
// what it is reading apart from its instructions.
const (
	conversationOpen   = "<conversation>\n"
	conversationClose  = "\n</conversation>"
	previousSummaryTag = "previous-summary"
)

// Compact issues the summarization call of a preparation and returns the
// checkpoint. It issues one call with Generate rather than Stream, because the
// summary is a complete answer nobody watches arrive, and it reuses the retry
// policy of an ordinary turn. The caller persists the result: this package
// never touches a store.
//
// A failure that survives the retries is returned as an error, so the caller
// ends the run without writing a half-built checkpoint.
func Compact(ctx context.Context, prep Preparation, deps Deps) (Result, error) {
	if deps.Client == nil {
		return Result{}, errors.New("compaction: a client is required")
	}
	if len(prep.Messages) == 0 {
		return Result{}, errors.New("compaction: there is nothing to summarize")
	}

	request := &llm.Request{
		Model:  deps.Model,
		System: deps.Prompt,
		Messages: []llm.Message{
			{
				Role:   llm.RoleUser,
				Blocks: []llm.Block{{Type: llm.BlockText, Text: summarizationInput(prep)}},
			},
		},
		MaxTokens: deps.MaxTokens,
	}

	//nolint:wrapcheck // the failure already names the call it aborted.
	return retry.Do(ctx, func(ctx context.Context) (Result, error) {
		response, err := deps.Client.Generate(ctx, request)
		if err != nil {
			return Result{}, fmt.Errorf("compaction: generate summary: %w", err)
		}

		summary := strings.TrimSpace(textOf(response.Blocks))
		if summary == "" {
			return Result{}, errors.New("compaction: the model returned an empty summary")
		}

		model := response.Model
		if model == "" {
			model = deps.Model
		}
		return Result{
			Summary:      summary,
			KeptID:       prep.KeptID,
			TokensBefore: prep.TokensBefore,
			Model:        model,
			Usage:        response.Usage,
		}, nil
	}, nil)
}

// summarizationInput builds the user message the summarization call sends: the
// conversation to summarize, preceded by the summary of an earlier compaction
// when the range resumes from one. There is no merge step and no special case
// for a summary of a summary, because a summary is only ever text.
func summarizationInput(prep Preparation) string {
	var input strings.Builder
	if summary := strings.TrimSpace(prep.PreviousSummary); summary != "" {
		input.WriteString("<" + previousSummaryTag + ">\n")
		input.WriteString(summary)
		input.WriteString("\n</" + previousSummaryTag + ">\n\n")
	}
	input.WriteString(conversationOpen)
	input.WriteString(Serialize(prep.Messages))
	input.WriteString(conversationClose)
	return input.String()
}
