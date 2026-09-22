package session

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/llm"
)

// validHeaderLine is a header line used by the decode tests.
const validHeaderLine = `{"kind":"header","version":1,"id":"s1","createdAt":"2026-09-16T10:15:30Z","agent":"coder","model":"test/model"}`

// userMessageLine is a user message line used by the decode tests.
const userMessageLine = `{"kind":"message","id":"m1","createdAt":"2026-09-16T10:15:31Z","role":"user","blocks":[{"type":"text","text":"hello"}]}`

// leafMarkerLine builds a leaf marker line moving the active leaf to id, or
// before the first message when id is empty.
func leafMarkerLine(id string) string {
	return `{"kind":"leaf","targetId":"` + id + `","createdAt":"2026-09-16T10:15:33Z"}`
}

// tagMarkerLine builds a tag marker line labeling the entry id.
func tagMarkerLine(id, tag string) string {
	return `{"kind":"tag","targetId":"` + id + `","createdAt":"2026-09-16T10:15:33Z","tag":"` + tag + `"}`
}

// titleMarkerLine builds a title marker line naming the session, or removing
// its name when title is empty.
func titleMarkerLine(title string) string {
	return `{"kind":"title","createdAt":"2026-09-16T10:15:33Z","title":"` + title + `"}`
}

// agentLine builds an agent selection line selecting the agent with the id,
// hanging from parentID.
func agentLine(id, parentID, agentID string) string {
	return `{"kind":"agent","id":"` + id + `","parentId":"` + parentID + `",` +
		`"createdAt":"2026-09-16T10:15:35Z","agentId":"` + agentID + `"}`
}

// modelLine builds a model selection line selecting ref, hanging from
// parentID.
func modelLine(id, parentID, ref string) string {
	return `{"kind":"model","id":"` + id + `","parentId":"` + parentID + `",` +
		`"createdAt":"2026-09-16T10:15:36Z","modelRef":"` + ref + `"}`
}

// compactionLine builds a compaction line replacing everything before keptID.
func compactionLine(id, parentID, keptID, summary string) string {
	return `{"kind":"compaction","id":"` + id + `","parentId":"` + parentID + `",` +
		`"createdAt":"2026-09-16T10:15:34Z","summary":"` + summary + `",` +
		`"keptId":"` + keptID + `","tokensBefore":123,` +
		`"responseModel":"kimi-k2","responseUsage":{"inputTokens":7,"outputTokens":3}}`
}

// assistantMessageLine is an assistant message line used by the decode tests.
const assistantMessageLine = `{"kind":"message","id":"m2","parentId":"m1","createdAt":"2026-09-16T10:15:32Z","role":"assistant","responseModel":"kimi-k2","responseStopReason":"end_turn","itemId":"msg_2","blocks":[{"type":"text","text":"hi"}],"responseUsage":{"inputTokens":10,"outputTokens":5}}`

// TestStoredUsage verifies usage translation.
func TestStoredUsage(t *testing.T) {
	t.Run("keeps empty usage empty", func(t *testing.T) {
		require.Nil(t, toStoredUsage(llm.Usage{}))
	})

	t.Run("round trips every field", func(t *testing.T) {
		usage := llm.Usage{
			InputTokens:      1,
			OutputTokens:     2,
			ReasoningTokens:  3,
			CacheReadTokens:  4,
			CacheWriteTokens: 5,
		}

		entry := storedEntry{ResponseUsage: toStoredUsage(usage)}

		require.Equal(t, usage, entry.responseUsage())
	})

	t.Run("reads missing usage as empty", func(t *testing.T) {
		require.Equal(t, llm.Usage{}, storedEntry{}.responseUsage())
	})
}

// TestStoredBlocks verifies block translation.
func TestStoredBlocks(t *testing.T) {
	t.Run("round trips every block type", func(t *testing.T) {
		blocks := []llm.Block{
			{Type: llm.BlockText, Text: "hello"},
			{
				Type: llm.BlockThinking, Thinking: "plan",
				ThinkingSignature: "signature", ThinkingID: "rs_1",
			},
			{Type: llm.BlockRedactedThinking, ThinkingRedactedData: "opaque"},
			{
				Type:              llm.BlockToolCall,
				ToolCallID:        "call_1",
				ToolCallName:      "read",
				ToolCallArguments: json.RawMessage(`{"path":"a.go"}`),
			},
			{
				Type:              llm.BlockToolResult,
				ToolResultCallID:  "call_1",
				ToolResult:        []llm.Block{{Type: llm.BlockText, Text: "content"}},
				ToolResultIsError: true,
			},
		}

		converted, err := fromStoredBlocks(toStoredBlocks(blocks))
		require.NoError(t, err)
		require.Equal(t, blocks, converted)
	})

	t.Run("rejects unknown block types", func(t *testing.T) {
		_, err := fromStoredBlock(storedBlock{Type: "video"})

		require.ErrorContains(t, err, `unknown block type "video"`)
	})

	t.Run("reports nested block errors", func(t *testing.T) {
		_, err := fromStoredBlocks([]storedBlock{{
			Type:       string(llm.BlockToolResult),
			ToolResult: []storedBlock{{Type: "video"}},
		}})

		require.ErrorContains(t, err, "block 0")
		require.ErrorContains(t, err, "unknown block type")
	})
}

// TestDecode verifies session file parsing.
func TestDecode(t *testing.T) {
	t.Run("parses a session with messages", func(t *testing.T) {
		data := validHeaderLine + "\n" + userMessageLine + "\n" + assistantMessageLine + "\n"

		header, entries, leaf, _, err := decode([]byte(data))
		require.NoError(t, err)

		require.Equal(t, "s1", header.ID)
		require.Equal(t, "coder", header.Agent)
		require.Equal(t, "test/model", header.Model)
		require.Len(t, entries, 2)
		require.Equal(t, "m1", entries[0].ID)
		require.Equal(t, llm.RoleUser, entries[0].Message.Role)
		require.Equal(t, "m2", entries[1].ID)
		require.Equal(t, "m1", entries[1].ParentID)
		require.Equal(t, "msg_2", entries[1].Message.ItemID)
		require.Equal(t, llm.StopReasonEndTurn, entries[1].ResponseStopReason)
		require.Equal(t, llm.Usage{InputTokens: 10, OutputTokens: 5}, entries[1].ResponseUsage)
		require.Equal(t, "m2", leaf)
	})

	t.Run("accepts a header without messages", func(t *testing.T) {
		header, entries, leaf, _, err := decode([]byte(validHeaderLine + "\n"))
		require.NoError(t, err)

		require.Equal(t, "s1", header.ID)
		require.Empty(t, entries)
		require.Empty(t, leaf)
	})

	t.Run("decodes an agent selection and advances the leaf", func(t *testing.T) {
		data := validHeaderLine + "\n" + userMessageLine + "\n" +
			agentLine("a1", "m1", "reviewer") + "\n"

		_, entries, leaf, _, err := decode([]byte(data))
		require.NoError(t, err)

		require.Len(t, entries, 2)
		selection := entries[1]
		require.Equal(t, KindAgent, selection.Kind)
		require.Equal(t, "a1", selection.ID)
		require.Equal(t, "m1", selection.ParentID)
		require.Equal(t, "reviewer", selection.AgentID)
		require.Equal(t, "a1", leaf)
	})

	t.Run("decodes a model selection and advances the leaf", func(t *testing.T) {
		data := validHeaderLine + "\n" + userMessageLine + "\n" +
			modelLine("s1", "m1", "anthropic/claude-sonnet") + "\n"

		_, entries, leaf, _, err := decode([]byte(data))
		require.NoError(t, err)

		require.Len(t, entries, 2)
		selection := entries[1]
		require.Equal(t, KindModel, selection.Kind)
		require.Equal(t, "s1", selection.ID)
		require.Equal(t, "m1", selection.ParentID)
		require.Equal(t, "anthropic/claude-sonnet", selection.ModelRef)
		require.Equal(t, "s1", leaf)
	})

	t.Run("ignores unknown entry kinds", func(t *testing.T) {
		data := validHeaderLine + "\n" +
			userMessageLine + "\n" +
			`{"kind":"custom","id":"c1","data":true}` + "\n" +
			assistantMessageLine + "\n"

		_, entries, leaf, _, err := decode([]byte(data))
		require.NoError(t, err)

		require.Len(t, entries, 2)
		require.Equal(t, "m2", leaf)
	})

	t.Run("skips blank lines", func(t *testing.T) {
		data := validHeaderLine + "\n\n" + userMessageLine + "\n\n"

		_, entries, _, _, err := decode([]byte(data))
		require.NoError(t, err)

		require.Len(t, entries, 1)
	})

	t.Run("honors a trailing leaf marker", func(t *testing.T) {
		data := validHeaderLine + "\n" + userMessageLine + "\n" +
			assistantMessageLine + "\n" + leafMarkerLine("m1") + "\n"

		_, entries, leaf, _, err := decode([]byte(data))
		require.NoError(t, err)

		require.Len(t, entries, 2)
		require.Equal(t, "m1", leaf)
	})

	t.Run("ignores a leaf marker followed by a message", func(t *testing.T) {
		data := validHeaderLine + "\n" + userMessageLine + "\n" + leafMarkerLine("m1") + "\n" +
			assistantMessageLine + "\n"

		_, _, leaf, _, err := decode([]byte(data))
		require.NoError(t, err)

		require.Equal(t, "m2", leaf)
	})

	t.Run("moves the leaf before the first message", func(t *testing.T) {
		data := validHeaderLine + "\n" + userMessageLine + "\n" +
			assistantMessageLine + "\n" + leafMarkerLine("") + "\n"

		_, entries, leaf, _, err := decode([]byte(data))
		require.NoError(t, err)

		require.Len(t, entries, 2)
		require.Empty(t, leaf)
	})

	t.Run("labels the entries the tags target", func(t *testing.T) {
		data := validHeaderLine + "\n" + userMessageLine + "\n" +
			assistantMessageLine + "\n" +
			tagMarkerLine("m1", "bug") + "\n" +
			tagMarkerLine("m2", "review") + "\n"

		_, entries, leaf, _, err := decode([]byte(data))
		require.NoError(t, err)

		require.Equal(t, "bug", entries[0].Tag)
		require.Equal(t, "review", entries[1].Tag)
		require.Equal(t, "m2", leaf)
	})

	t.Run("keeps the last tag of an entry", func(t *testing.T) {
		data := validHeaderLine + "\n" + userMessageLine + "\n" +
			tagMarkerLine("m1", "bug") + "\n" +
			tagMarkerLine("m1", "") + "\n"

		_, entries, _, _, err := decode([]byte(data))
		require.NoError(t, err)

		require.Empty(t, entries[0].Tag)
	})

	t.Run("keeps the leaf a tag does not move", func(t *testing.T) {
		data := validHeaderLine + "\n" + userMessageLine + "\n" +
			assistantMessageLine + "\n" + leafMarkerLine("m1") + "\n" +
			tagMarkerLine("m1", "bug") + "\n"

		_, _, leaf, _, err := decode([]byte(data))
		require.NoError(t, err)

		require.Equal(t, "m1", leaf)
	})

	t.Run("names the session with the title marker", func(t *testing.T) {
		data := validHeaderLine + "\n" + userMessageLine + "\n" +
			titleMarkerLine("Fix the parser") + "\n"

		_, entries, leaf, title, err := decode([]byte(data))
		require.NoError(t, err)

		require.Equal(t, "Fix the parser", title)
		require.Len(t, entries, 1, "the title names the session, it adds no entry")
		require.Equal(t, "m1", leaf, "the title leaves the leaf where it found it")
	})

	t.Run("keeps the last title of a session", func(t *testing.T) {
		data := validHeaderLine + "\n" + titleMarkerLine("first") + "\n" +
			titleMarkerLine("second") + "\n"

		_, _, _, title, err := decode([]byte(data))
		require.NoError(t, err)

		require.Equal(t, "second", title)
	})

	t.Run("removes the title of a session with an empty marker", func(t *testing.T) {
		data := validHeaderLine + "\n" + titleMarkerLine("first") + "\n" +
			titleMarkerLine("") + "\n"

		_, _, _, title, err := decode([]byte(data))
		require.NoError(t, err)

		require.Empty(t, title)
	})

	t.Run("keeps a leaf marker pending after a title", func(t *testing.T) {
		data := validHeaderLine + "\n" + userMessageLine + "\n" +
			assistantMessageLine + "\n" + leafMarkerLine("m1") + "\n" +
			titleMarkerLine("Fix the parser") + "\n"

		_, _, leaf, title, err := decode([]byte(data))
		require.NoError(t, err)

		require.Equal(t, "m1", leaf, "renaming after a rewind never undoes the rewind")
		require.Equal(t, "Fix the parser", title)
	})

	t.Run("decodes a compaction with its model and usage", func(t *testing.T) {
		data := validHeaderLine + "\n" + userMessageLine + "\n" + assistantMessageLine + "\n" +
			compactionLine("c1", "m2", "m1", "the summary") + "\n"

		_, entries, leaf, _, err := decode([]byte(data))
		require.NoError(t, err)

		require.Len(t, entries, 3)
		compaction := entries[2]
		require.Equal(t, KindCompaction, compaction.Kind)
		require.Equal(t, "c1", compaction.ID)
		require.Equal(t, "m2", compaction.ParentID)
		require.Equal(t, "the summary", compaction.CompactionSummary)
		require.Equal(t, "m1", compaction.CompactionKeptID)
		require.Equal(t, 123, compaction.CompactionTokensBefore)
		require.Equal(t, "kimi-k2", compaction.ResponseModel)
		require.Equal(t, llm.Usage{InputTokens: 7, OutputTokens: 3}, compaction.ResponseUsage)
		require.Equal(t, "c1", leaf)
	})

	t.Run("loads a file written before the kind existed", func(t *testing.T) {
		data := validHeaderLine + "\n" + userMessageLine + "\n" + assistantMessageLine + "\n"

		_, entries, leaf, _, err := decode([]byte(data))
		require.NoError(t, err)

		require.Len(t, entries, 2)
		require.Equal(t, "m2", leaf)
	})

	t.Run("rejects invalid files", func(t *testing.T) {
		tests := []struct {
			name    string
			data    string
			wantErr string
		}{
			{name: "empty file", data: "", wantErr: "the file is empty"},
			{
				name:    "first line is not a header",
				data:    userMessageLine + "\n",
				wantErr: `expected a "header" entry`,
			},
			{
				name:    "corrupt header",
				data:    `{"kind":"header",` + "\n",
				wantErr: "line 1",
			},
			{
				name:    "unsupported version",
				data:    `{"kind":"header","version":2,"id":"s1","agent":"a","model":"m"}` + "\n",
				wantErr: "unsupported session version 2",
			},
			{
				name:    "missing session id",
				data:    `{"kind":"header","version":1,"agent":"a","model":"m"}` + "\n",
				wantErr: "session id is required",
			},
			{
				name:    "missing agent",
				data:    `{"kind":"header","version":1,"id":"s1","model":"m"}` + "\n",
				wantErr: "agent is required",
			},
			{
				name:    "missing model",
				data:    `{"kind":"header","version":1,"id":"s1","agent":"a"}` + "\n",
				wantErr: "model is required",
			},
			{
				name:    "corrupt message",
				data:    validHeaderLine + "\n" + `{"kind":"message",` + "\n",
				wantErr: "line 2",
			},
			{
				name:    "missing entry id",
				data:    validHeaderLine + "\n" + `{"kind":"message","role":"user","blocks":[{"type":"text","text":"hi"}]}` + "\n",
				wantErr: "entry id is required",
			},
			{
				name: "duplicate entry id",
				data: validHeaderLine + "\n" + userMessageLine + "\n" +
					`{"kind":"message","id":"m1","parentId":"m1","role":"user","blocks":[{"type":"text","text":"again"}]}` + "\n",
				wantErr: `duplicate entry id "m1"`,
			},
			{
				name: "unknown parent",
				data: validHeaderLine + "\n" +
					`{"kind":"message","id":"m1","parentId":"nope","role":"user","blocks":[{"type":"text","text":"hi"}]}` + "\n",
				wantErr: `parent "nope" is not an earlier entry`,
			},
			{
				name: "invalid role",
				data: validHeaderLine + "\n" +
					`{"kind":"message","id":"m1","role":"system","blocks":[{"type":"text","text":"hi"}]}` + "\n",
				wantErr: `invalid message role "system"`,
			},
			{
				name: "unknown block type",
				data: validHeaderLine + "\n" +
					`{"kind":"message","id":"m1","role":"user","blocks":[{"type":"video"}]}` + "\n",
				wantErr: `unknown block type "video"`,
			},
			{
				name:    "corrupt leaf marker",
				data:    validHeaderLine + "\n" + `{"kind":"leaf",` + "\n",
				wantErr: "line 2",
			},
			{
				name:    "leaf marker with unknown target",
				data:    validHeaderLine + "\n" + leafMarkerLine("nope") + "\n",
				wantErr: `references unknown entry "nope"`,
			},
			{
				name:    "corrupt tag marker",
				data:    validHeaderLine + "\n" + `{"kind":"tag",` + "\n",
				wantErr: "line 2",
			},
			{
				name:    "tag marker without target",
				data:    validHeaderLine + "\n" + tagMarkerLine("", "bug") + "\n",
				wantErr: "references unknown entry",
			},
			{
				name: "tag marker with unknown target",
				data: validHeaderLine + "\n" + userMessageLine + "\n" + tagMarkerLine(
					"nope",
					"bug",
				) + "\n",
				wantErr: `references unknown entry "nope"`,
			},
			{
				name:    "corrupt compaction",
				data:    validHeaderLine + "\n" + `{"kind":"compaction",` + "\n",
				wantErr: "line 2",
			},
			{
				name: "compaction without a kept entry",
				data: validHeaderLine + "\n" + userMessageLine + "\n" +
					compactionLine("c1", "m1", "", "the summary") + "\n",
				wantErr: "kept entry id is required",
			},
			{
				name: "compaction naming an unknown kept entry",
				data: validHeaderLine + "\n" + userMessageLine + "\n" +
					compactionLine("c1", "m1", "nope", "the summary") + "\n",
				wantErr: `line 3: the compaction references unknown entry "nope"`,
			},
			{
				name: "compaction naming a kept entry that comes later",
				data: validHeaderLine + "\n" +
					compactionLine("c1", "", "m1", "the summary") + "\n" +
					userMessageLine + "\n",
				wantErr: `line 2: the compaction references unknown entry "m1"`,
			},
			{
				name: "compaction with a missing parent",
				data: validHeaderLine + "\n" + userMessageLine + "\n" +
					compactionLine("c1", "nope", "m1", "the summary") + "\n",
				wantErr: `parent "nope" is not an earlier entry`,
			},
			{
				name:    "corrupt agent selection",
				data:    validHeaderLine + "\n" + `{"kind":"agent",` + "\n",
				wantErr: "line 2",
			},
			{
				name: "agent selection without an agent",
				data: validHeaderLine + "\n" + userMessageLine + "\n" +
					agentLine("a1", "m1", "") + "\n",
				wantErr: "agent id is required",
			},
			{
				name: "agent selection with a missing parent",
				data: validHeaderLine + "\n" + userMessageLine + "\n" +
					agentLine("a1", "nope", "reviewer") + "\n",
				wantErr: `parent "nope" is not an earlier entry`,
			},
			{
				name:    "corrupt model selection",
				data:    validHeaderLine + "\n" + `{"kind":"model",` + "\n",
				wantErr: "line 2",
			},
			{
				name: "model selection without a reference",
				data: validHeaderLine + "\n" + userMessageLine + "\n" +
					modelLine("s1", "m1", "") + "\n",
				wantErr: "model reference is required",
			},
			{
				name: "model selection with a missing parent",
				data: validHeaderLine + "\n" + userMessageLine + "\n" +
					modelLine("s1", "nope", "anthropic/claude-sonnet") + "\n",
				wantErr: `parent "nope" is not an earlier entry`,
			},
			{
				name: "message hanging from an ignored kind",
				data: validHeaderLine + "\n" + userMessageLine + "\n" +
					`{"kind":"custom","id":"c1","data":true}` + "\n" +
					`{"kind":"message","id":"m2","parentId":"c1","role":"assistant","blocks":[{"type":"text","text":"hi"}]}` + "\n",
				wantErr: `parent "c1" is not an earlier entry`,
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				_, _, _, _, err := decode([]byte(test.data))

				require.ErrorContains(t, err, test.wantErr)
			})
		}
	})
}

// TestEncodeLine verifies JSONL encoding.
func TestEncodeLine(t *testing.T) {
	line, err := encodeLine(storedHeader{Kind: KindHeader, Version: version, ID: "s1"})
	require.NoError(t, err)

	require.True(t, json.Valid(line))
	require.Equal(t, byte('\n'), line[len(line)-1])
	require.Contains(t, string(line), `"kind":"header"`)

	var decoded storedHeader
	require.NoError(t, json.Unmarshal(line, &decoded))
	require.Equal(t, time.Time{}, decoded.CreatedAt)
	require.Equal(t, "s1", decoded.ID)
}
