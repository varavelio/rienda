//go:build e2e

package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// sessionExtension is the file extension of session files.
const sessionExtension = ".jsonl"

// Entry kinds of the stored format.
const (
	// entryKindHeader opens every session file.
	entryKindHeader = "header"
	// entryKindMessage carries a conversation turn.
	entryKindMessage = "message"
	// entryKindCompaction replaces every entry before its kept one with a
	// summary.
	entryKindCompaction = "compaction"
	// entryKindAgent selects the agent the branch runs from that entry onward.
	entryKindAgent = "agent"
	// entryKindModel selects the model the branch runs from that entry onward.
	entryKindModel = "model"
)

// Session is one session file of an instance, decoded without the help of the
// application packages so the suite validates the stored format as an external
// reader would.
type Session struct {
	// Path is the path of the session file.
	Path string

	// Header holds the metadata that opens the file.
	Header SessionHeader

	// Entries lists the conversation turns in append order.
	Entries []SessionEntry

	// Compactions lists the compaction entries in append order.
	Compactions []SessionCompaction

	// Agents lists the agent selection entries in append order.
	Agents []SessionAgent

	// Models lists the model selection entries in append order.
	Models []SessionModel
}

// SessionHeader is the decoded header line of a session file.
type SessionHeader struct {
	// Kind is the line discriminator, always "header".
	Kind string `json:"kind"`
	// Version identifies the file format.
	Version int `json:"version"`
	// ID is the session identifier.
	ID string `json:"id"`
	// CreatedAt is the moment the session was created.
	CreatedAt time.Time `json:"createdAt"`
	// Agent is the identifier of the agent that owns the session.
	Agent string `json:"agent"`
	// Model is the model reference of the session.
	Model string `json:"model"`
	// Workdir is the workspace the session runs in.
	Workdir string `json:"workdir"`
}

// SessionEntry is one decoded conversation entry of a session file.
type SessionEntry struct {
	// Kind is the line discriminator.
	Kind string `json:"kind"`
	// ID is the identifier of the entry.
	ID string `json:"id"`
	// ParentID links the entry to the one it follows.
	ParentID string `json:"parentId"`
	// CreatedAt is the moment the entry was appended.
	CreatedAt time.Time `json:"createdAt"`
	// Role is the author of the message.
	Role string `json:"role"`
	// Blocks is the ordered content of the message.
	Blocks []SessionBlock `json:"blocks"`
	// ItemID is the provider item identifier of an assistant message.
	ItemID string `json:"itemId"`
	// ResponseModel names the model that produced an assistant message.
	ResponseModel string `json:"responseModel"`
	// ResponseStopReason explains why an assistant message ended.
	ResponseStopReason string `json:"responseStopReason"`
	// ResponseUsage reports the token consumption of an assistant message.
	ResponseUsage *SessionUsage `json:"responseUsage"`
}

// SessionBlock is one decoded content block of a session entry. Only the
// fields valid for its type carry meaning.
type SessionBlock struct {
	// Type discriminates the content of the block.
	Type string `json:"type"`
	// Text holds the content of a text block.
	Text string `json:"text"`
	// Thinking holds the reasoning of a thinking block.
	Thinking string `json:"thinking"`
	// ThinkingSignature holds the provider signature of a thinking block.
	ThinkingSignature string `json:"thinkingSignature"`
	// ThinkingID is the provider identifier of a thinking block.
	ThinkingID string `json:"thinkingId"`
	// ToolCallID is the identifier of an invoked tool.
	ToolCallID string `json:"toolCallId"`
	// ToolCallName is the name of an invoked tool.
	ToolCallName string `json:"toolCallName"`
	// ToolCallArguments holds the arguments of an invocation as JSON.
	ToolCallArguments json.RawMessage `json:"toolCallArguments"`
	// ToolResultCallID references the invocation a result answers.
	ToolResultCallID string `json:"toolResultCallId"`
	// ToolResult holds the content of a result.
	ToolResult []SessionBlock `json:"toolResult"`
	// ToolResultIsError marks a failed invocation.
	ToolResultIsError bool `json:"toolResultIsError"`
}

// SessionCompaction is one decoded compaction entry of a session file.
type SessionCompaction struct {
	// Kind is the line discriminator, always "compaction".
	Kind string `json:"kind"`
	// ID is the identifier of the entry.
	ID string `json:"id"`
	// ParentID links the entry to the one it follows.
	ParentID string `json:"parentId"`
	// CreatedAt is the moment the entry was appended.
	CreatedAt time.Time `json:"createdAt"`
	// Summary is the checkpoint text.
	Summary string `json:"summary"`
	// KeptID identifies the first entry kept verbatim after the compaction.
	KeptID string `json:"keptId"`
	// TokensBefore is what the summarized range measured.
	TokensBefore int `json:"tokensBefore"`
	// ResponseModel names the model that produced the summary.
	ResponseModel string `json:"responseModel"`
	// ResponseUsage reports the token consumption of the summarization call.
	ResponseUsage *SessionUsage `json:"responseUsage"`
}

// SessionAgent is one decoded agent selection entry of a session file.
type SessionAgent struct {
	// Kind is the line discriminator, always "agent".
	Kind string `json:"kind"`
	// ID is the identifier of the entry.
	ID string `json:"id"`
	// ParentID links the entry to the one it follows.
	ParentID string `json:"parentId"`
	// CreatedAt is the moment the entry was appended.
	CreatedAt time.Time `json:"createdAt"`
	// AgentID is the identifier of the selected agent.
	AgentID string `json:"agentId"`
}

// SessionModel is one decoded model selection entry of a session file.
type SessionModel struct {
	// Kind is the line discriminator, always "model".
	Kind string `json:"kind"`
	// ID is the identifier of the entry.
	ID string `json:"id"`
	// ParentID links the entry to the one it follows.
	ParentID string `json:"parentId"`
	// CreatedAt is the moment the entry was appended.
	CreatedAt time.Time `json:"createdAt"`
	// ModelRef is the provider/model reference the entry selects.
	ModelRef string `json:"modelRef"`
}

// SessionUsage is the token consumption persisted with an assistant entry.
type SessionUsage struct {
	// InputTokens is the number of tokens in the request input.
	InputTokens int `json:"inputTokens"`
	// OutputTokens is the number of tokens generated by the model.
	OutputTokens int `json:"outputTokens"`
	// ReasoningTokens is the subset of OutputTokens spent on reasoning.
	ReasoningTokens int `json:"reasoningTokens"`
	// CacheReadTokens is the number of input tokens served from the cache.
	CacheReadTokens int `json:"cacheReadTokens"`
	// CacheWriteTokens is the number of input tokens written to the cache.
	CacheWriteTokens int `json:"cacheWriteTokens"`
}

// Text returns the concatenated text of the entry.
func (e SessionEntry) Text() string {
	var text strings.Builder
	for _, block := range e.Blocks {
		text.WriteString(block.Text)
	}
	return text.String()
}

// ToolCalls returns every tool call block of the session, in order.
func (s Session) ToolCalls() []SessionBlock {
	return s.blocksOfType("tool_call")
}

// ToolResults returns every tool result block of the session, in order.
func (s Session) ToolResults() []SessionBlock {
	return s.blocksOfType("tool_result")
}

// ResultText returns the concatenated text a tool result carries.
func (b SessionBlock) ResultText() string {
	var text strings.Builder
	for _, nested := range b.ToolResult {
		text.WriteString(nested.Text)
	}
	return text.String()
}

// blocksOfType returns every block of the session with the given type, in
// order.
func (s Session) blocksOfType(blockType string) []SessionBlock {
	blocks := make([]SessionBlock, 0, len(s.Entries))
	for _, entry := range s.Entries {
		for _, block := range entry.Blocks {
			if block.Type == blockType {
				blocks = append(blocks, block)
			}
		}
	}
	return blocks
}

// Sessions returns every session stored in the instance, oldest first.
func (h *Harness) Sessions(t *testing.T) []Session {
	t.Helper()

	pattern := filepath.Join(h.home, riendaDirName, "sessions", "*", "*"+sessionExtension)
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("harness: list sessions: %v", err)
	}

	sessions := make([]Session, 0, len(paths))
	for _, path := range paths {
		sessions = append(sessions, readSession(t, path))
	}
	slices.SortFunc(sessions, func(a, b Session) int {
		return a.Header.CreatedAt.Compare(b.Header.CreatedAt)
	})
	return sessions
}

// Session returns the session with the given identifier, failing the test when
// the instance stores no such session.
func (h *Harness) Session(t *testing.T, id string) Session {
	t.Helper()

	pattern := filepath.Join(h.home, riendaDirName, "sessions", "*", id+sessionExtension)
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("harness: look up session %s: %v", id, err)
	}
	if len(paths) == 0 {
		t.Fatalf("harness: the instance stores no session %s", id)
	}
	return readSession(t, paths[0])
}

// readSession decodes one session file, failing the test when it does not
// follow the stored format.
func readSession(t *testing.T, path string) Session {
	t.Helper()

	//nolint:gosec // the path comes from a glob over a test temporary directory.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("harness: read session %s: %v", path, err)
	}

	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		t.Fatalf("harness: session %s is empty", path)
	}

	session := Session{Path: path}
	if err := json.Unmarshal([]byte(lines[0]), &session.Header); err != nil {
		t.Fatalf("harness: decode header of %s: %v", path, err)
	}
	if session.Header.Kind != entryKindHeader {
		t.Fatalf("harness: %s does not open with a header line", path)
	}

	for number, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}

		var envelope struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			t.Fatalf("harness: decode line %d of %s: %v", number+2, path, err)
		}
		switch envelope.Kind {
		case entryKindMessage:
			var entry SessionEntry
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatalf("harness: decode line %d of %s: %v", number+2, path, err)
			}
			session.Entries = append(session.Entries, entry)
		case entryKindCompaction:
			var compaction SessionCompaction
			if err := json.Unmarshal([]byte(line), &compaction); err != nil {
				t.Fatalf("harness: decode line %d of %s: %v", number+2, path, err)
			}
			session.Compactions = append(session.Compactions, compaction)
		case entryKindAgent:
			var selection SessionAgent
			if err := json.Unmarshal([]byte(line), &selection); err != nil {
				t.Fatalf("harness: decode line %d of %s: %v", number+2, path, err)
			}
			session.Agents = append(session.Agents, selection)
		case entryKindModel:
			var selection SessionModel
			if err := json.Unmarshal([]byte(line), &selection); err != nil {
				t.Fatalf("harness: decode line %d of %s: %v", number+2, path, err)
			}
			session.Models = append(session.Models, selection)
		}
	}

	return session
}
