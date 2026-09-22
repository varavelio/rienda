package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/varavelio/rienda/internal/llm"
)

// version identifies the session file format understood by this package.
const version = 1

// lineEnvelope reads the kind discriminator of a session file line.
type lineEnvelope struct {
	Kind Kind `json:"kind"`
}

// storedHeader is the header line of a session file.
type storedHeader struct {
	Kind      Kind      `json:"kind"`
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	Agent     string    `json:"agent"`
	Model     string    `json:"model"`
	Workdir   string    `json:"workdir,omitempty"`
}

// storedEntry is a conversation turn line of a session file.
type storedEntry struct {
	Kind               Kind           `json:"kind"`
	ID                 string         `json:"id"`
	ParentID           string         `json:"parentId,omitempty"`
	CreatedAt          time.Time      `json:"createdAt"`
	Role               llm.Role       `json:"role,omitempty"`
	Blocks             []storedBlock  `json:"blocks,omitempty"`
	ItemID             string         `json:"itemId,omitempty"`
	ResponseModel      string         `json:"responseModel,omitempty"`
	ResponseStopReason llm.StopReason `json:"responseStopReason,omitempty"`
	ResponseUsage      *storedUsage   `json:"responseUsage,omitempty"`
}

// storedLeaf is a leaf marker line of a session file. An empty target moves
// the leaf before the first message, so the next one opens the tree again.
type storedLeaf struct {
	Kind      Kind      `json:"kind"`
	TargetID  string    `json:"targetId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// storedTag is a tag marker line of a session file. An empty tag removes the
// label of its target, because a marker always describes the whole tag.
type storedTag struct {
	Kind      Kind      `json:"kind"`
	TargetID  string    `json:"targetId"`
	CreatedAt time.Time `json:"createdAt"`
	Tag       string    `json:"tag,omitempty"`
}

// storedTitle is a title marker line of a session file. An empty title removes
// the name of the session, because a marker always describes the whole name.
// The marker targets the session itself, not an entry, so it carries no
// identifier: the name belongs to the whole conversation, whatever branch it
// was written from.
type storedTitle struct {
	Kind      Kind      `json:"kind"`
	CreatedAt time.Time `json:"createdAt"`
	Title     string    `json:"title,omitempty"`
}

// storedCompaction is a compaction line of a session file. It reuses the
// response fields of a message line for the summarization call, so the stored
// usage of a session stays complete without a second concept.
type storedCompaction struct {
	Kind          Kind         `json:"kind"`
	ID            string       `json:"id"`
	ParentID      string       `json:"parentId,omitempty"`
	CreatedAt     time.Time    `json:"createdAt"`
	Summary       string       `json:"summary"`
	KeptID        string       `json:"keptId"`
	TokensBefore  int          `json:"tokensBefore,omitempty"`
	ResponseModel string       `json:"responseModel,omitempty"`
	ResponseUsage *storedUsage `json:"responseUsage,omitempty"`
}

// storedAgent is an agent selection line of a session file. The selection
// hangs from the active leaf and carries no content, so the next message
// continues from it under another agent. It reuses no response field, because
// it makes no model call.
type storedAgent struct {
	Kind      Kind      `json:"kind"`
	ID        string    `json:"id"`
	ParentID  string    `json:"parentId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	AgentID   string    `json:"agentId"`
}

// entry converts a stored agent selection into its in-memory form.
func (s storedAgent) entry() Entry {
	return Entry{
		ID:        s.ID,
		ParentID:  s.ParentID,
		CreatedAt: s.CreatedAt,
		Kind:      KindAgent,
		AgentID:   s.AgentID,
	}
}

// entry converts a stored compaction into its in-memory form.
func (s storedCompaction) entry() Entry {
	return Entry{
		ID:                     s.ID,
		ParentID:               s.ParentID,
		CreatedAt:              s.CreatedAt,
		Kind:                   KindCompaction,
		CompactionSummary:      s.Summary,
		CompactionKeptID:       s.KeptID,
		CompactionTokensBefore: s.TokensBefore,
		ResponseModel:          s.ResponseModel,
		ResponseUsage:          s.responseUsage(),
	}
}

// responseUsage converts the stored usage of a compaction into its canonical
// form.
func (s storedCompaction) responseUsage() llm.Usage {
	if s.ResponseUsage == nil {
		return llm.Usage{}
	}
	return llm.Usage{
		InputTokens:      s.ResponseUsage.InputTokens,
		OutputTokens:     s.ResponseUsage.OutputTokens,
		ReasoningTokens:  s.ResponseUsage.ReasoningTokens,
		CacheReadTokens:  s.ResponseUsage.CacheReadTokens,
		CacheWriteTokens: s.ResponseUsage.CacheWriteTokens,
	}
}

// storedBlock mirrors an llm.Block in the session file format.
type storedBlock struct {
	Type                 string          `json:"type"`
	Text                 string          `json:"text,omitempty"`
	Thinking             string          `json:"thinking,omitempty"`
	ThinkingSignature    string          `json:"thinkingSignature,omitempty"`
	ThinkingID           string          `json:"thinkingId,omitempty"`
	ThinkingRedactedData string          `json:"thinkingRedactedData,omitempty"`
	ToolCallID           string          `json:"toolCallId,omitempty"`
	ToolCallName         string          `json:"toolCallName,omitempty"`
	ToolCallArguments    json.RawMessage `json:"toolCallArguments,omitempty"`
	ToolResultCallID     string          `json:"toolResultCallId,omitempty"`
	ToolResult           []storedBlock   `json:"toolResult,omitempty"`
	ToolResultIsError    bool            `json:"toolResultIsError,omitempty"`
}

// storedUsage mirrors an llm.Usage in the session file format.
type storedUsage struct {
	InputTokens      int `json:"inputTokens,omitempty"`
	OutputTokens     int `json:"outputTokens,omitempty"`
	ReasoningTokens  int `json:"reasoningTokens,omitempty"`
	CacheReadTokens  int `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int `json:"cacheWriteTokens,omitempty"`
}

// entry converts a stored entry into its in-memory form.
func (s storedEntry) entry() (Entry, error) {
	if s.Role != llm.RoleUser && s.Role != llm.RoleAssistant {
		return Entry{}, fmt.Errorf("invalid message role %q", s.Role)
	}

	blocks, err := fromStoredBlocks(s.Blocks)
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		ID:                 s.ID,
		ParentID:           s.ParentID,
		CreatedAt:          s.CreatedAt,
		Kind:               KindMessage,
		Message:            llm.Message{Role: s.Role, Blocks: blocks, ItemID: s.ItemID},
		ResponseModel:      s.ResponseModel,
		ResponseStopReason: s.ResponseStopReason,
		ResponseUsage:      s.responseUsage(),
	}, nil
}

// responseUsage converts the stored usage of an entry into its canonical form.
func (s storedEntry) responseUsage() llm.Usage {
	if s.ResponseUsage == nil {
		return llm.Usage{}
	}
	return llm.Usage{
		InputTokens:      s.ResponseUsage.InputTokens,
		OutputTokens:     s.ResponseUsage.OutputTokens,
		ReasoningTokens:  s.ResponseUsage.ReasoningTokens,
		CacheReadTokens:  s.ResponseUsage.CacheReadTokens,
		CacheWriteTokens: s.ResponseUsage.CacheWriteTokens,
	}
}

// toStoredUsage converts canonical usage into its stored form, nil when empty.
func toStoredUsage(usage llm.Usage) *storedUsage {
	if usage == (llm.Usage{}) {
		return nil
	}
	return &storedUsage{
		InputTokens:      usage.InputTokens,
		OutputTokens:     usage.OutputTokens,
		ReasoningTokens:  usage.ReasoningTokens,
		CacheReadTokens:  usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
	}
}

// toStoredBlocks converts canonical blocks into their stored form.
func toStoredBlocks(blocks []llm.Block) []storedBlock {
	converted := make([]storedBlock, 0, len(blocks))
	for _, block := range blocks {
		converted = append(converted, toStoredBlock(block))
	}
	return converted
}

// fromStoredBlocks converts stored blocks into their canonical form.
func fromStoredBlocks(blocks []storedBlock) ([]llm.Block, error) {
	converted := make([]llm.Block, 0, len(blocks))
	for i, block := range blocks {
		result, err := fromStoredBlock(block)
		if err != nil {
			return nil, fmt.Errorf("block %d: %w", i, err)
		}
		converted = append(converted, result)
	}
	return converted, nil
}

// toStoredBlock converts a canonical block into its stored form.
func toStoredBlock(block llm.Block) storedBlock {
	converted := storedBlock{
		Type:                 string(block.Type),
		Text:                 block.Text,
		Thinking:             block.Thinking,
		ThinkingSignature:    block.ThinkingSignature,
		ThinkingID:           block.ThinkingID,
		ThinkingRedactedData: block.ThinkingRedactedData,
		ToolCallID:           block.ToolCallID,
		ToolCallName:         block.ToolCallName,
		ToolCallArguments:    block.ToolCallArguments,
		ToolResultCallID:     block.ToolResultCallID,
		ToolResultIsError:    block.ToolResultIsError,
	}
	if len(block.ToolResult) > 0 {
		converted.ToolResult = toStoredBlocks(block.ToolResult)
	}
	return converted
}

// fromStoredBlock converts a stored block into its canonical form. Unknown
// block types are an error because silently dropping content would corrupt the
// replayed conversation.
func fromStoredBlock(block storedBlock) (llm.Block, error) {
	switch llm.BlockType(block.Type) {
	case llm.BlockText:
		return llm.Block{Type: llm.BlockText, Text: block.Text}, nil
	case llm.BlockThinking:
		return llm.Block{
			Type:              llm.BlockThinking,
			Thinking:          block.Thinking,
			ThinkingSignature: block.ThinkingSignature,
			ThinkingID:        block.ThinkingID,
		}, nil
	case llm.BlockRedactedThinking:
		return llm.Block{
			Type:                 llm.BlockRedactedThinking,
			ThinkingRedactedData: block.ThinkingRedactedData,
		}, nil
	case llm.BlockToolCall:
		return llm.Block{
			Type:              llm.BlockToolCall,
			ToolCallID:        block.ToolCallID,
			ToolCallName:      block.ToolCallName,
			ToolCallArguments: block.ToolCallArguments,
		}, nil
	case llm.BlockToolResult:
		nested, err := fromStoredBlocks(block.ToolResult)
		if err != nil {
			return llm.Block{}, err
		}
		return llm.Block{
			Type:              llm.BlockToolResult,
			ToolResultCallID:  block.ToolResultCallID,
			ToolResult:        nested,
			ToolResultIsError: block.ToolResultIsError,
		}, nil
	default:
		return llm.Block{}, fmt.Errorf("unknown block type %q", block.Type)
	}
}

// decode parses the contents of a session file.
func decode(data []byte) (storedHeader, []Entry, string, string, error) {
	lines := bytes.Split(data, []byte("\n"))
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return storedHeader{}, nil, "", "", errors.New("the file is empty")
	}

	header, err := decodeHeader(lines[0])
	if err != nil {
		return storedHeader{}, nil, "", "", err
	}

	entries := make([]Entry, 0, len(lines)-1)
	known := make(map[string]int, len(lines)-1)
	leaf, title, pendingLeaf := "", "", ""
	lastWasLeaf := false

	for i, line := range lines[1:] {
		lineNumber := i + 2
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var envelope lineEnvelope
		if err := json.Unmarshal(line, &envelope); err != nil {
			return storedHeader{}, nil, "", "", fmt.Errorf("line %d: %w", lineNumber, err)
		}

		switch envelope.Kind {
		case KindMessage:
			entry, err := decodeMessage(lineNumber, line, known)
			if err != nil {
				return storedHeader{}, nil, "", "", err
			}
			known[entry.ID] = len(entries)
			entries = append(entries, entry)
			leaf = entry.ID
			lastWasLeaf = false

		case KindLeaf:
			var marker storedLeaf
			if err := json.Unmarshal(line, &marker); err != nil {
				return storedHeader{}, nil, "", "", fmt.Errorf("line %d: %w", lineNumber, err)
			}
			pendingLeaf = marker.TargetID
			lastWasLeaf = true

		case KindTag:
			var marker storedTag
			if err := json.Unmarshal(line, &marker); err != nil {
				return storedHeader{}, nil, "", "", fmt.Errorf("line %d: %w", lineNumber, err)
			}
			index, found := known[marker.TargetID]
			if !found {
				return storedHeader{}, nil, "", "", fmt.Errorf(
					"line %d: the tag references unknown entry %q",
					lineNumber,
					marker.TargetID,
				)
			}
			// A tag labels a turn without moving the conversation, so it
			// leaves the leaf of the session where it found it.
			entries[index].Tag = marker.Tag

		case KindTitle:
			var marker storedTitle
			if err := json.Unmarshal(line, &marker); err != nil {
				return storedHeader{}, nil, "", "", fmt.Errorf("line %d: %w", lineNumber, err)
			}
			// A title names the session without moving the conversation, so it
			// leaves both the leaf and a leaf marker still pending in the file
			// exactly where it found them: renaming after a rewind never undoes
			// the rewind.
			title = marker.Title

		case KindCompaction:
			entry, err := decodeCompaction(lineNumber, line, known)
			if err != nil {
				return storedHeader{}, nil, "", "", err
			}
			known[entry.ID] = len(entries)
			entries = append(entries, entry)
			leaf = entry.ID
			lastWasLeaf = false

		case KindAgent:
			entry, err := decodeAgent(lineNumber, line, known)
			if err != nil {
				return storedHeader{}, nil, "", "", err
			}
			known[entry.ID] = len(entries)
			entries = append(entries, entry)
			leaf = entry.ID
			lastWasLeaf = false

		default:
			// Entries written by newer versions are ignored so that an old
			// binary keeps loading the session.
		}
	}

	if lastWasLeaf {
		// A leaf marker without a target moves the leaf before the first
		// message, which leaves the session ready to be written again from
		// its very beginning.
		if pendingLeaf != "" {
			if _, found := known[pendingLeaf]; !found {
				return storedHeader{}, nil, "", "", fmt.Errorf(
					"the leaf marker references unknown entry %q",
					pendingLeaf,
				)
			}
		}
		leaf = pendingLeaf
	}
	return header, entries, leaf, title, nil
}

// decodeHeader decodes and validates the header line.
func decodeHeader(line []byte) (storedHeader, error) {
	var envelope lineEnvelope
	if err := json.Unmarshal(line, &envelope); err != nil {
		return storedHeader{}, fmt.Errorf("line 1: %w", err)
	}
	if envelope.Kind != KindHeader {
		return storedHeader{}, fmt.Errorf(
			"line 1: expected a %q entry, found %q",
			KindHeader,
			envelope.Kind,
		)
	}

	var header storedHeader
	if err := json.Unmarshal(line, &header); err != nil {
		return storedHeader{}, fmt.Errorf("line 1: %w", err)
	}
	switch {
	case header.Version != version:
		return storedHeader{}, fmt.Errorf("unsupported session version %d", header.Version)
	case header.ID == "":
		return storedHeader{}, errors.New("line 1: the session id is required")
	case strings.TrimSpace(header.Agent) == "":
		return storedHeader{}, errors.New("line 1: the agent is required")
	case strings.TrimSpace(header.Model) == "":
		return storedHeader{}, errors.New("line 1: the model is required")
	}
	return header, nil
}

// decodeMessage decodes and validates a message line, which must carry an
// identifier of its own and follow an entry the file already holds.
func decodeMessage(lineNumber int, line []byte, known map[string]int) (Entry, error) {
	var stored storedEntry
	if err := json.Unmarshal(line, &stored); err != nil {
		return Entry{}, fmt.Errorf("line %d: %w", lineNumber, err)
	}

	_, duplicate := known[stored.ID]
	_, hasParent := known[stored.ParentID]
	switch {
	case stored.ID == "":
		return Entry{}, fmt.Errorf("line %d: the entry id is required", lineNumber)
	case duplicate:
		return Entry{}, fmt.Errorf("line %d: duplicate entry id %q", lineNumber, stored.ID)
	case stored.ParentID != "" && !hasParent:
		return Entry{}, fmt.Errorf(
			"line %d: parent %q is not an earlier entry",
			lineNumber,
			stored.ParentID,
		)
	}

	entry, err := stored.entry()
	if err != nil {
		return Entry{}, fmt.Errorf("line %d: %w", lineNumber, err)
	}
	return entry, nil
}

// decodeCompaction decodes and validates a compaction line. Its kept entry
// must already be known, because the checkpoint always replaces entries of the
// branch it hangs from and a reference that does not resolve would silently
// hide turns. The failure is loud, following the precedent of the tag and leaf
// checks: nothing is repaired silently.
func decodeCompaction(lineNumber int, line []byte, known map[string]int) (Entry, error) {
	var stored storedCompaction
	if err := json.Unmarshal(line, &stored); err != nil {
		return Entry{}, fmt.Errorf("line %d: %w", lineNumber, err)
	}

	_, duplicate := known[stored.ID]
	_, hasParent := known[stored.ParentID]
	_, hasKept := known[stored.KeptID]
	switch {
	case stored.ID == "":
		return Entry{}, fmt.Errorf("line %d: the entry id is required", lineNumber)
	case duplicate:
		return Entry{}, fmt.Errorf("line %d: duplicate entry id %q", lineNumber, stored.ID)
	case stored.ParentID != "" && !hasParent:
		return Entry{}, fmt.Errorf(
			"line %d: parent %q is not an earlier entry",
			lineNumber,
			stored.ParentID,
		)
	case stored.KeptID == "":
		return Entry{}, fmt.Errorf("line %d: the kept entry id is required", lineNumber)
	case !hasKept:
		return Entry{}, fmt.Errorf(
			"line %d: the compaction references unknown entry %q",
			lineNumber,
			stored.KeptID,
		)
	}
	return stored.entry(), nil
}

// decodeAgent decodes and validates an agent selection line, which must carry
// an identifier of its own, name the agent it selects and follow an entry the
// file already holds.
func decodeAgent(lineNumber int, line []byte, known map[string]int) (Entry, error) {
	var stored storedAgent
	if err := json.Unmarshal(line, &stored); err != nil {
		return Entry{}, fmt.Errorf("line %d: %w", lineNumber, err)
	}

	_, duplicate := known[stored.ID]
	_, hasParent := known[stored.ParentID]
	switch {
	case stored.ID == "":
		return Entry{}, fmt.Errorf("line %d: the entry id is required", lineNumber)
	case duplicate:
		return Entry{}, fmt.Errorf("line %d: duplicate entry id %q", lineNumber, stored.ID)
	case stored.ParentID != "" && !hasParent:
		return Entry{}, fmt.Errorf(
			"line %d: parent %q is not an earlier entry",
			lineNumber,
			stored.ParentID,
		)
	case strings.TrimSpace(stored.AgentID) == "":
		return Entry{}, fmt.Errorf("line %d: the agent id is required", lineNumber)
	}
	return stored.entry(), nil
}

// encodeLine marshals value into a single JSONL line.
func encodeLine[T any](value T) ([]byte, error) {
	line, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("session: encode entry: %w", err)
	}
	return append(line, '\n'), nil
}
