package slash

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	t.Run("parses valid definition", func(t *testing.T) {
		got, err := Parse("test", []byte("---\ndescription: Run tests\n---\nRun tests: $*\n"))
		require.NoError(t, err)
		require.Equal(t, "test", got.ID)
		require.Equal(t, "Run tests", got.Description)
		require.Equal(t, "Run tests: $*", got.Template)
		require.Empty(t, got.Path)
	})
	t.Run("rejects invalid id", func(t *testing.T) {
		_, err := Parse("../evil", []byte("---\ndescription: x\n---\ny\n"))
		require.Error(t, err)
	})
	t.Run("requires opening delimiter", func(t *testing.T) {
		_, err := Parse("test", []byte("no frontmatter\n"))
		require.Error(t, err)
	})
	t.Run("requires closing delimiter", func(t *testing.T) {
		_, err := Parse("test", []byte("---\ndescription: x\nno close\n"))
		require.Error(t, err)
	})
	t.Run("rejects empty frontmatter", func(t *testing.T) {
		_, err := Parse("test", []byte("---\n---\nbody\n"))
		require.Error(t, err)
	})
	t.Run("rejects multi document", func(t *testing.T) {
		_, err := Parse("test", []byte("---\ndescription: x\n...\ndescription: y\n---\nbody\n"))
		require.ErrorContains(t, err, "single YAML document")
	})
	t.Run("rejects unknown field", func(t *testing.T) {
		_, err := Parse("test", []byte("---\ndescription: x\nmodel: y\n---\nbody\n"))
		require.Error(t, err)
	})
	t.Run("requires description", func(t *testing.T) {
		_, err := Parse("test", []byte("---\ndescription: \"  \"\n---\nbody\n"))
		require.ErrorContains(t, err, "description is required")
	})
	t.Run("requires template", func(t *testing.T) {
		_, err := Parse("test", []byte("---\ndescription: x\n---\n   \n"))
		require.ErrorContains(t, err, "template is empty")
	})
	t.Run("tolerates BOM and CRLF", func(t *testing.T) {
		got, err := Parse("test", []byte("\uFEFF---\r\ndescription: x\r\n---\r\nbody\r\n"))
		require.NoError(t, err)
		require.Equal(t, "body", got.Template)
	})
}
