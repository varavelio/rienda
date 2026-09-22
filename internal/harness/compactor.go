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
	"github.com/varavelio/rienda/internal/session"
)

// compactionPromptFile is the name of the file that overrides the built-in
// summarization prompt inside the Rienda directory of the user.
const compactionPromptFile = "COMPACTION.md"

// compactor summarizes the branch of a session through internal/compaction. It
// resolves the model and the client of the summarization when it runs, from the
// resolver of the session, so a session may run on one model and be summarized
// by another, and a session that switched model is summarized by the model it
// runs.
type compactor struct {
	resolver engine.Resolver
	pinned   string
	settings compaction.Settings
	prompt   string
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
	modelRef string,
) (compaction.Result, bool, error) {
	prep, ok := compaction.Prepare(branch, c.settings)
	if !ok {
		return compaction.Result{}, false, nil
	}

	prompt, err := compaction.Prompt(c.prompt)
	if err != nil {
		return compaction.Result{}, false, fmt.Errorf("harness: %w", err)
	}

	// The model of the summary is the one the configuration pins, or the one
	// the branch runs at this moment, so a session that switched model is
	// summarized by the model the user selected.
	ref := c.pinned
	if ref == "" {
		ref = modelRef
	}
	model, client, err := c.resolver.Resolve(ref)
	if err != nil {
		return compaction.Result{}, false, fmt.Errorf("harness: %w", err)
	}

	result, err := compaction.Compact(ctx, prep, compaction.Deps{
		Client:    client,
		Model:     model.ID,
		Prompt:    prompt,
		MaxTokens: model.MaxTokens,
	})
	if err != nil {
		return compaction.Result{}, false, fmt.Errorf("harness: %w", err)
	}
	return result, true, nil
}

// newCompactor builds the compactor of a session from the resolver of the
// session, which is what turns a provider/model reference into a client:
//
//   - when the configuration declares compaction.model, the summary runs on it,
//     which is how a user pins a cheap model to summarize;
//   - otherwise the summary runs on the model the branch is running, so a
//     conversation that switched model summarizes with the model the user
//     selected, and nothing changes behind their back.
//
// The reference of the branch is resolved on every compaction, so a session
// that switched model is summarized by the model it runs at that moment.
func newCompactor(cfg *config.Config, resolver engine.Resolver) (engine.Compactor, error) {
	promptPath, err := compactionPromptPath()
	if err != nil {
		return nil, err
	}

	return &compactor{
		resolver: resolver,
		pinned:   strings.TrimSpace(cfg.Compaction.Model),
		settings: compaction.Settings{KeepRecentTokens: cfg.Compaction.KeepRecentTokens},
		prompt:   promptPath,
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
