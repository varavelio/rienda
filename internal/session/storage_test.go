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

// assistantMessageLine is an assistant message line used by the decode tests.
const assistantMessageLine = `{"kind":"message","id":"m2","parentId":"m1","createdAt":"2026-09-16T10:15:32Z","role":"assistant","responseModel":"kimi-k2","responseStopReason":"end_turn","blocks":[{"type":"text","text":"hi"}],"responseUsage":{"inputTokens":10,"outputTokens":5}}`

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
			{Type: llm.BlockThinking, Thinking: "plan", ThinkingSignature: "signature"},
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

		header, entries, leaf, err := decode([]byte(data))
		require.NoError(t, err)

		require.Equal(t, "s1", header.ID)
		require.Equal(t, "coder", header.Agent)
		require.Equal(t, "test/model", header.Model)
		require.Len(t, entries, 2)
		require.Equal(t, "m1", entries[0].ID)
		require.Equal(t, llm.RoleUser, entries[0].Message.Role)
		require.Equal(t, "m2", entries[1].ID)
		require.Equal(t, "m1", entries[1].ParentID)
		require.Equal(t, llm.StopReasonEndTurn, entries[1].ResponseStopReason)
		require.Equal(t, llm.Usage{InputTokens: 10, OutputTokens: 5}, entries[1].ResponseUsage)
		require.Equal(t, "m2", leaf)
	})

	t.Run("accepts a header without messages", func(t *testing.T) {
		header, entries, leaf, err := decode([]byte(validHeaderLine + "\n"))
		require.NoError(t, err)

		require.Equal(t, "s1", header.ID)
		require.Empty(t, entries)
		require.Empty(t, leaf)
	})

	t.Run("ignores unknown entry kinds", func(t *testing.T) {
		data := validHeaderLine + "\n" +
			userMessageLine + "\n" +
			`{"kind":"custom","id":"c1","data":true}` + "\n" +
			assistantMessageLine + "\n"

		_, entries, leaf, err := decode([]byte(data))
		require.NoError(t, err)

		require.Len(t, entries, 2)
		require.Equal(t, "m2", leaf)
	})

	t.Run("skips blank lines", func(t *testing.T) {
		data := validHeaderLine + "\n\n" + userMessageLine + "\n\n"

		_, entries, _, err := decode([]byte(data))
		require.NoError(t, err)

		require.Len(t, entries, 1)
	})

	t.Run("honors a trailing leaf marker", func(t *testing.T) {
		marker := `{"kind":"leaf","id":"l1","parentId":"m1","createdAt":"2026-09-16T10:15:33Z"}`
		data := validHeaderLine + "\n" + userMessageLine + "\n" +
			assistantMessageLine + "\n" + marker + "\n"

		_, entries, leaf, err := decode([]byte(data))
		require.NoError(t, err)

		require.Len(t, entries, 2)
		require.Equal(t, "m1", leaf)
	})

	t.Run("ignores a leaf marker followed by a message", func(t *testing.T) {
		marker := `{"kind":"leaf","id":"l1","parentId":"m1","createdAt":"2026-09-16T10:15:33Z"}`
		data := validHeaderLine + "\n" + userMessageLine + "\n" + marker + "\n" +
			assistantMessageLine + "\n"

		_, _, leaf, err := decode([]byte(data))
		require.NoError(t, err)

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
				name: "leaf marker without target",
				data: validHeaderLine + "\n" +
					`{"kind":"leaf","id":"l1","createdAt":"2026-09-16T10:15:33Z"}` + "\n",
				wantErr: "does not reference an entry",
			},
			{
				name: "leaf marker with unknown target",
				data: validHeaderLine + "\n" +
					`{"kind":"leaf","id":"l1","parentId":"nope","createdAt":"2026-09-16T10:15:33Z"}` + "\n",
				wantErr: `references unknown entry "nope"`,
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				_, _, _, err := decode([]byte(test.data))

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
