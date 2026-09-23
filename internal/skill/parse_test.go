package skill

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// countingReader reports how many bytes were read from it, so a test can prove
// that the reader of a SKILL.md file stopped early.
type countingReader struct {
	reader io.Reader
	read   int
}

// Read fills the buffer and accumulates the bytes it handed over.
func (c *countingReader) Read(buffer []byte) (int, error) {
	count, err := c.reader.Read(buffer)
	c.read += count
	if err != nil {
		return count, fmt.Errorf("countingReader: %w", err)
	}
	return count, nil
}

// frontmatterFile wraps a frontmatter block in its delimiters and closes it
// with a Markdown body, which is the shape of a real SKILL.md file.
func frontmatterFile(block string) string {
	return frontmatterDelimiter + "\n" + block + frontmatterDelimiter + "\nbody\n"
}

// parse runs the bounded read and the lenient parse over the contents of a
// SKILL.md file, exactly as discovery does.
func parse(t *testing.T, contents string) (frontmatter, []string) {
	t.Helper()

	block, err := readFrontmatter(strings.NewReader(contents))
	require.NoError(t, err, "the frontmatter of a readable file is read")
	return parseFrontmatter(block)
}

// TestReadFrontmatter verifies the bounded read of the frontmatter block.
func TestReadFrontmatter(t *testing.T) {
	t.Run("returns the lines between the delimiters", func(t *testing.T) {
		block, err := readFrontmatter(strings.NewReader("---\nname: x\n---\nbody\n"))

		require.NoError(t, err)
		require.Equal(t, "name: x\n", string(block))
	})

	t.Run("tolerates a byte order mark", func(t *testing.T) {
		block, err := readFrontmatter(strings.NewReader("\uFEFF---\nname: x\n---\nbody\n"))

		require.NoError(t, err)
		require.Equal(t, "name: x\n", string(block))
	})

	t.Run("tolerates windows line endings", func(t *testing.T) {
		block, err := readFrontmatter(strings.NewReader("---\r\nname: x\r\n---\r\nbody\r\n"))

		require.NoError(t, err)
		require.Equal(t, "name: x\n", string(block))
	})

	t.Run("returns an empty block when the frontmatter holds no field", func(t *testing.T) {
		block, err := readFrontmatter(strings.NewReader("---\n---\nbody\n"))

		require.NoError(t, err)
		require.Empty(t, block)
	})

	t.Run("rejects an empty file", func(t *testing.T) {
		_, err := readFrontmatter(strings.NewReader(""))

		require.ErrorIs(t, err, errNoFrontmatter)
	})

	t.Run("rejects a file without an opening delimiter", func(t *testing.T) {
		_, err := readFrontmatter(strings.NewReader("name: x\n---\n"))

		require.ErrorIs(t, err, errNoFrontmatter)
	})

	t.Run("rejects a file without a closing delimiter", func(t *testing.T) {
		_, err := readFrontmatter(strings.NewReader("---\nname: x\nbody\n"))

		require.ErrorIs(t, err, errUnterminated)
	})

	t.Run("rejects a closing delimiter reached without a newline", func(t *testing.T) {
		_, err := readFrontmatter(strings.NewReader("---\nname: x\nbody"))

		require.ErrorIs(t, err, errUnterminated)
	})

	t.Run("accepts a closing delimiter that ends the file", func(t *testing.T) {
		block, err := readFrontmatter(strings.NewReader("---\nname: x\n---"))

		require.NoError(t, err)
		require.Equal(t, "name: x\n", string(block))
	})

	t.Run("never reads the markdown body", func(t *testing.T) {
		body := strings.Repeat("x", 8<<20)
		reader := &countingReader{
			reader: strings.NewReader(frontmatterFile("name: x\n") + body),
		}

		block, err := readFrontmatter(reader)

		require.NoError(t, err)
		require.Equal(t, "name: x\n", string(block))
		require.Less(t, reader.read, len(body), "the body of the file was not read")
	})

	t.Run("bounds a frontmatter that never closes", func(t *testing.T) {
		contents := frontmatterDelimiter + "\n" + strings.Repeat("name: x\n", maxFrontmatterBytes)
		reader := &countingReader{reader: strings.NewReader(contents)}

		_, err := readFrontmatter(reader)

		require.ErrorIs(t, err, errUnterminated)
		require.LessOrEqual(
			t,
			reader.read,
			maxFrontmatterBytes,
			"the read stopped at the bound instead of consuming the file",
		)
	})

	t.Run("bounds a single unterminated line", func(t *testing.T) {
		contents := frontmatterDelimiter + "\n" + strings.Repeat("x", 4*maxFrontmatterBytes)
		reader := &countingReader{reader: strings.NewReader(contents)}

		_, err := readFrontmatter(reader)

		require.ErrorIs(t, err, errUnterminated)
		require.LessOrEqual(t, reader.read, maxFrontmatterBytes)
	})
}

// TestParseFrontmatter verifies the lenient parse of the frontmatter fields.
func TestParseFrontmatter(t *testing.T) {
	t.Run("reads the name and the description", func(t *testing.T) {
		parsed, problems := parse(t, frontmatterFile("name: pdfs\ndescription: Handle PDFs.\n"))

		require.Empty(t, problems)
		require.Equal(t, "pdfs", parsed.name)
		require.Equal(t, "Handle PDFs.", parsed.description)
	})

	t.Run("ignores the fields of the specification it does not use", func(t *testing.T) {
		parsed, problems := parse(t, frontmatterFile(
			"name: pdfs\ndescription: Handle PDFs.\nlicense: MIT\n"+
				"compatibility: needs python\nallowed-tools: [shell]\n"+
				"metadata:\n  author: someone\n",
		))

		require.Empty(t, problems)
		require.Equal(t, "pdfs", parsed.name)
		require.Equal(t, "Handle PDFs.", parsed.description)
	})

	t.Run("ignores an unknown field", func(t *testing.T) {
		_, problems := parse(t, frontmatterFile("name: pdfs\ndescription: x\nwhatever: 1\n"))

		require.Empty(t, problems)
	})

	t.Run("treats a value that is not a string as absent", func(t *testing.T) {
		cases := map[string]string{
			"a number":  "123",
			"a boolean": "true",
			"a list":    "[a, b]",
			"a map":     "{a: b}",
			"null":      "~",
		}

		for name, value := range cases {
			t.Run(name, func(t *testing.T) {
				parsed, problems := parse(t, frontmatterFile(
					"name: "+value+"\ndescription: Handle PDFs.\n",
				))

				require.Equal(t, []string{"the name is missing or empty"}, problems)
				require.Empty(t, parsed.name)
				require.Equal(t, "Handle PDFs.", parsed.description)
			})
		}
	})

	t.Run("accepts a quoted number", func(t *testing.T) {
		parsed, problems := parse(t, frontmatterFile("name: \"123\"\ndescription: x\n"))

		require.Empty(t, problems)
		require.Equal(t, "123", parsed.name)
	})

	t.Run("trims the surrounding whitespace", func(t *testing.T) {
		parsed, problems := parse(
			t,
			frontmatterFile("name: \"  pdfs  \"\ndescription: \"  x  \"\n"),
		)

		require.Empty(t, problems)
		require.Equal(t, "pdfs", parsed.name)
		require.Equal(t, "x", parsed.description)
	})

	t.Run("keeps a multi-line description", func(t *testing.T) {
		parsed, problems := parse(t, frontmatterFile(
			"name: pdfs\ndescription: |\n  Handle PDFs.\n  Use when a PDF appears.\n",
		))

		require.Empty(t, problems)
		require.Equal(t, "Handle PDFs.\nUse when a PDF appears.", parsed.description)
	})

	t.Run("treats an empty value as absent", func(t *testing.T) {
		parsed, problems := parse(t, frontmatterFile("name: pdfs\ndescription: \"   \"\n"))

		require.Equal(t, []string{"the description is missing or empty"}, problems)
		require.Equal(t, "pdfs", parsed.name)
	})

	t.Run("keeps the last occurrence of a field declared twice", func(t *testing.T) {
		parsed, problems := parse(t, frontmatterFile("name: first\nname: second\ndescription: x\n"))

		require.Empty(t, problems)
		require.Equal(t, "second", parsed.name)
	})

	t.Run("recovers an unquoted colon", func(t *testing.T) {
		parsed, problems := parse(t, frontmatterFile(
			"name: pdfs\ndescription: Use this when: the user asks\n",
		))

		require.Empty(t, problems)
		require.Equal(t, "pdfs", parsed.name)
		require.Equal(t, "Use this when: the user asks", parsed.description)
	})

	t.Run("recovers a description that holds only a colon", func(t *testing.T) {
		parsed, problems := parse(t, frontmatterFile(
			"name: pdfs\ndescription: Use this when: the user asks\nlicense: MIT\n",
		))

		require.Empty(t, problems)
		require.Equal(t, "pdfs", parsed.name)
		require.Equal(t, "Use this when: the user asks", parsed.description)
	})

	t.Run("does not recover a frontmatter that is not a mapping", func(t *testing.T) {
		for name, block := range map[string]string{
			"a sequence": "- name: pdfs\n- description: x\n",
			"a scalar":   "just some text\n",
			"a comment":  "# nothing here\n",
			"nothing":    "",
		} {
			t.Run(name, func(t *testing.T) {
				_, problems := parse(t, frontmatterFile(block))

				require.Equal(t, []string{errShape.Error()}, problems)
			})
		}
	})

	t.Run("reports a missing name", func(t *testing.T) {
		_, problems := parse(t, frontmatterFile("description: x\n"))

		require.Equal(t, []string{"the name is missing or empty"}, problems)
	})

	t.Run("reports a missing description", func(t *testing.T) {
		_, problems := parse(t, frontmatterFile("name: pdfs\n"))

		require.Equal(t, []string{"the description is missing or empty"}, problems)
	})

	t.Run("reports both missing fields in one list", func(t *testing.T) {
		_, problems := parse(t, frontmatterFile("license: MIT\n"))

		require.Equal(t, []string{
			"the name is missing or empty",
			"the description is missing or empty",
		}, problems)
	})
}

// TestFrontmatterUnusable verifies the check that decides whether a skill is
// skipped instead of offered.
func TestFrontmatterUnusable(t *testing.T) {
	t.Run("reports a usable frontmatter", func(t *testing.T) {
		require.False(t, frontmatter{name: "pdfs", description: "x"}.unusable())
	})

	t.Run("reports a frontmatter without a name", func(t *testing.T) {
		require.True(t, frontmatter{description: "x"}.unusable())
	})

	t.Run("reports a frontmatter without a description", func(t *testing.T) {
		require.True(t, frontmatter{name: "pdfs"}.unusable())
	})
}

// TestRecommendations verifies the advisory rules of the format, which are
// reported without ever stopping a skill from being offered.
func TestRecommendations(t *testing.T) {
	base := frontmatter{name: "pdfs", description: "Handle PDFs."}

	t.Run("accepts a frontmatter that follows the format", func(t *testing.T) {
		require.Empty(t, recommendations(base, "pdfs"))
	})

	t.Run("reports a name longer than the limit", func(t *testing.T) {
		long := base
		long.name = strings.Repeat("a", maxNameRunes+1)

		require.Equal(t, []string{
			"the name is longer than 64 characters",
			"the name does not match the directory \"pdfs\"",
		}, recommendations(long, "pdfs"))
	})

	t.Run("reports a name outside the charset", func(t *testing.T) {
		invalid := base
		invalid.name = "PDF_Handler"

		require.Equal(t, []string{
			"the name uses characters outside lowercase letters, digits and hyphens",
			"the name does not match the directory \"pdfs\"",
		}, recommendations(invalid, "pdfs"))
	})

	t.Run("reports a name with a misplaced hyphen", func(t *testing.T) {
		for name, value := range map[string]string{
			"a leading hyphen":    "-pdfs",
			"a trailing hyphen":   "pdfs-",
			"consecutive hyphens": "pdf--s",
		} {
			t.Run(name, func(t *testing.T) {
				misplaced := base
				misplaced.name = value

				require.Equal(t, []string{
					"the name starts or ends with a hyphen or holds consecutive hyphens",
					"the name does not match the directory \"pdfs\"",
				}, recommendations(misplaced, "pdfs"))
			})
		}
	})

	t.Run("reports a name that does not match the directory", func(t *testing.T) {
		require.Equal(t, []string{"the name does not match the directory \"other\""},
			recommendations(base, "other"))
	})

	t.Run("reports a description longer than the limit", func(t *testing.T) {
		long := base
		long.description = strings.Repeat("a", maxDescriptionRunes+1)

		require.Equal(t, []string{"the description is longer than 1024 characters"},
			recommendations(long, "pdfs"))
	})

	t.Run("reports every problem in a fixed order", func(t *testing.T) {
		broken := frontmatter{
			name:        "-" + strings.Repeat("A", maxNameRunes),
			description: strings.Repeat("a", maxDescriptionRunes+1),
		}

		require.Equal(t, []string{
			"the name is longer than 64 characters",
			"the name uses characters outside lowercase letters, digits and hyphens",
			"the name starts or ends with a hyphen or holds consecutive hyphens",
			"the name does not match the directory \"pdfs\"",
			"the description is longer than 1024 characters",
		}, recommendations(broken, "pdfs"))
	})
}
