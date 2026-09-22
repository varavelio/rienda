package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/varavelio/rienda/internal/compaction"
	"github.com/varavelio/rienda/internal/config"
	"github.com/varavelio/rienda/internal/engine"
	"github.com/varavelio/rienda/internal/llm"
	"github.com/varavelio/rienda/internal/provider"
	"github.com/varavelio/rienda/internal/session"
)

// compactionPromptFile is the name of the file that overrides the built-in
// summarization prompt inside the Rienda directory of the user.
const compactionPromptFile = "COMPACTION.md"

// compactor summarizes the branch of a session through internal/compaction. It
// holds the resolved client and model of the summarization, so a session may
// run on one model and be summarized by another.
type compactor struct {
	client    llm.Client
	model     string
	maxTokens int
	settings  compaction.Settings
	prompt    string
}

// Refusal reports why the branch holds nothing to compact, and false when it
// holds something.
func (c *compactor) Refusal(branch []session.Entry) (compaction.Refusal, bool) {
	if _, ok := compaction.Prepare(branch, c.settings); ok {
		return compaction.Refusal{}, false
	}
	return compaction.Classify(branch, c.settings), true
}

// Compact summarizes the branch, returning ok as false when there is nothing
// to compact.
func (c *compactor) Compact(
	ctx context.Context,
	branch []session.Entry,
) (compaction.Result, bool, error) {
	prep, ok := compaction.Prepare(branch, c.settings)
	if !ok {
		return compaction.Result{}, false, nil
	}

	prompt, err := compaction.Prompt(c.prompt)
	if err != nil {
		return compaction.Result{}, false, fmt.Errorf("harness: %w", err)
	}

	result, err := compaction.Compact(ctx, prep, compaction.Deps{
		Client:    c.client,
		Model:     c.model,
		Prompt:    prompt,
		MaxTokens: c.maxTokens,
	})
	if err != nil {
		return compaction.Result{}, false, fmt.Errorf("harness: %w", err)
	}
	return result, true, nil
}

// newCompactor builds the compactor of a session from the resolved settings of
// the summarization model. The session model summarizes unless the
// configuration declares compaction.model, which is resolved and built the
// same way the session client is.
func newCompactor(
	cfg *config.Config,
	resolved config.Resolved,
	sessionClient llm.Client,
) (engine.Compactor, error) {
	client, model, maxTokens := sessionClient, resolved.ModelID, resolved.MaxTokens

	if ref := strings.TrimSpace(cfg.Compaction.Model); ref != "" {
		summary, err := cfg.Resolve(ref)
		if err != nil {
			return nil, fmt.Errorf("harness: compaction: %w", err)
		}
		built, err := provider.New(summary.Protocol, summary.ProviderConfig)
		if err != nil {
			return nil, fmt.Errorf("harness: build compaction client: %w", err)
		}
		client, model, maxTokens = built, summary.ModelID, summary.MaxTokens
	}

	promptPath, err := compactionPromptPath()
	if err != nil {
		return nil, err
	}

	return &compactor{
		client:    client,
		model:     model,
		maxTokens: maxTokens,
		settings:  compaction.Settings{KeepRecentTokens: cfg.Compaction.KeepRecentTokens},
		prompt:    promptPath,
	}, nil
}

// compactionPromptPath returns the path of the file that overrides the
// summarization prompt, which lives next to the configuration of the user. The
// path is resolved once, but the file is read on every compaction, so an edit
// applies to the next one without restarting the process.
func compactionPromptPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("harness: locate home directory: %w", err)
	}
	return filepath.Join(home, ".rienda", compactionPromptFile), nil
}
