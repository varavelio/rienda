package slash

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseInvocation(t *testing.T) {
	t.Run("bare name", func(t *testing.T) {
		name, args, ok := ParseInvocation("/test")
		require.True(t, ok)
		require.Equal(t, "test", name)
		require.Empty(t, args)
	})
	t.Run("name with args", func(t *testing.T) {
		name, args, ok := ParseInvocation("/doc @foo/bar extra")
		require.True(t, ok)
		require.Equal(t, "doc", name)
		require.Equal(t, "@foo/bar extra", args)
	})
	t.Run("at without space stays one token", func(t *testing.T) {
		name, _, ok := ParseInvocation("/doc@foo")
		require.True(t, ok)
		require.Equal(t, "doc@foo", name)
	})
	t.Run("lone slash is not ok", func(t *testing.T) {
		_, _, ok := ParseInvocation("/")
		require.False(t, ok)
	})
	t.Run("missing slash is not ok", func(t *testing.T) {
		_, _, ok := ParseInvocation("test x")
		require.False(t, ok)
	})
	t.Run("multiline args preserved", func(t *testing.T) {
		name, args, ok := ParseInvocation("/doc\n@foo/bar")
		require.True(t, ok)
		require.Equal(t, "doc", name)
		require.Equal(t, "@foo/bar", args)
	})
}

func TestSplitArgs(t *testing.T) {
	t.Run("empty yields nil", func(t *testing.T) {
		require.Empty(t, SplitArgs(""))
		require.Empty(t, SplitArgs("   "))
	})
	t.Run("plain split", func(t *testing.T) {
		require.Equal(t, []string{"a", "b"}, SplitArgs("a b"))
	})
	t.Run("double quotes group", func(t *testing.T) {
		require.Equal(t, []string{"a b", "c"}, SplitArgs(`"a b" c`))
	})
	t.Run("single quotes stay ordinary", func(t *testing.T) {
		require.Equal(t, []string{"'a", "b'"}, SplitArgs("'a b'"))
	})
	t.Run("unterminated quote consumes rest", func(t *testing.T) {
		require.Equal(t, []string{"abc def"}, SplitArgs(`"abc def`))
	})
}

func TestExpand(t *testing.T) {
	t.Run("all args", func(t *testing.T) {
		require.Equal(t, "Run tests: --coverage", Expand("Run tests: $*", "--coverage"))
	})
	t.Run("positionals", func(t *testing.T) {
		require.Equal(t, "Create config.json in src", Expand("Create $1 in $2", "config.json src"))
	})
	t.Run("repeats identically", func(t *testing.T) {
		require.Equal(t, "A x B x C y", Expand("A $1 B $1 C $2", "x y"))
	})
	t.Run("escape stays literal", func(t *testing.T) {
		require.Equal(t, "Price $5", Expand("Price $$5", ""))
		require.Equal(t, "Show $*", Expand("Show $$*", "ignored"))
	})
	t.Run("other dollars stay literal", func(t *testing.T) {
		require.Equal(
			t,
			"Home is $HOME and ${x} and $0",
			Expand("Home is $HOME and ${x} and $0", ""),
		)
	})
	t.Run("missing positional is empty", func(t *testing.T) {
		require.Equal(t, "Got ", Expand("Got $5", ""))
	})
	t.Run("quoted grouping feeds positionals", func(t *testing.T) {
		require.Equal(t, "Echo a b", Expand("Echo $1", `"a b" c`))
	})
	t.Run("arguments never rescanned", func(t *testing.T) {
		require.Equal(t, "Show $1", Expand("Show $*", "$1"))
	})
}

func TestExpandAppend(t *testing.T) {
	t.Run("appends after blank line", func(t *testing.T) {
		require.Equal(t, "Fixed text\n\nextra words", Expand("Fixed text", "extra words"))
	})
	t.Run("no append with placeholder", func(t *testing.T) {
		require.Equal(t, "Got ", Expand("Got $1", ""))
		require.Equal(t, "Run a", Expand("Run $*", "a"))
	})
	t.Run("no trailing newline when empty", func(t *testing.T) {
		require.Equal(t, "Fixed text", Expand("Fixed text", ""))
	})
}

func TestContainsPlaceholder(t *testing.T) {
	t.Run("detects forms", func(t *testing.T) {
		require.True(t, ContainsPlaceholder("a $* b"))
		require.True(t, ContainsPlaceholder("a $1 b"))
		require.False(t, ContainsPlaceholder("a $$ b"))
		require.False(t, ContainsPlaceholder("a $HOME b"))
	})
}
