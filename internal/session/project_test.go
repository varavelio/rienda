package session

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/varavelio/rienda/internal/llm"
)

// projectHashPattern matches the trailing hash of a project identifier.
var projectHashPattern = regexp.MustCompile(`-[0-9a-f]{8}$`)

// TestProjectID verifies project identifier normalization.
func TestProjectID(t *testing.T) {
	t.Run("derives a readable identifier with a hash suffix", func(t *testing.T) {
		id, err := ProjectID("/home/eduardo/projects/myproject")
		require.NoError(t, err)

		require.True(t, strings.HasPrefix(id, "home-eduardo-projects-myproject-"))
		require.Regexp(t, projectHashPattern, id)
	})

	t.Run("is pure and stable", func(t *testing.T) {
		first, err := ProjectID("/home/eduardo/projects/myproject")
		require.NoError(t, err)
		second, err := ProjectID("/home/eduardo/projects/myproject")
		require.NoError(t, err)

		require.Equal(t, first, second)
	})

	t.Run("normalizes equivalent spellings", func(t *testing.T) {
		plain, err := ProjectID("/home/eduardo/projects/myproject")
		require.NoError(t, err)

		for _, project := range []string{
			"/home/eduardo/projects/myproject/",
			"/home/eduardo/./projects/../projects/myproject",
			"  /home/eduardo/projects/myproject  ",
		} {
			id, err := ProjectID(project)
			require.NoError(t, err)
			require.Equal(t, plain, id, "project %q", project)
		}
	})

	t.Run("separates colliding slugs", func(t *testing.T) {
		dashed, err := ProjectID("/home/eduardo/projects/my-project")
		require.NoError(t, err)
		nested, err := ProjectID("/home/eduardo/projects/my/project")
		require.NoError(t, err)

		require.NotEqual(t, dashed, nested)
	})

	t.Run("keeps readable characters", func(t *testing.T) {
		tests := []struct {
			project string
			prefix  string
		}{
			{project: "/home/user/my_project", prefix: "home-user-my_project-"},
			{project: "/home/user/proyectos/año", prefix: "home-user-proyectos-año-"},
			{project: "/home/user/My.Project", prefix: "home-user-My-Project-"},
			{project: "/home/user/a//b", prefix: "home-user-a-b-"},
			{project: "my_project", prefix: "my_project-"},
			{project: "rpc-client/42", prefix: "rpc-client-42-"},
		}

		for _, test := range tests {
			id, err := ProjectID(test.project)
			require.NoError(t, err, "project %q", test.project)
			require.True(t, strings.HasPrefix(id, test.prefix), "id %q", id)
		}
	})

	t.Run("caps the readable part", func(t *testing.T) {
		long := "/home/user/" + strings.Repeat("very-long-segment/", 20)

		id, err := ProjectID(long)
		require.NoError(t, err)

		require.LessOrEqual(t, len(id), projectSlugMaxBytes+1+projectHashLength)
		require.Regexp(t, projectHashPattern, id)
	})

	t.Run("cuts long unicode slugs at rune boundaries", func(t *testing.T) {
		long := "/a/" + strings.Repeat("测试", 60)

		id, err := ProjectID(long)
		require.NoError(t, err)

		require.LessOrEqual(t, len(id), projectSlugMaxBytes+1+projectHashLength)
		require.True(t, utf8.ValidString(id))
		require.Regexp(t, projectHashPattern, id)

		_, err = ProjectDir(t.TempDir(), id)
		require.NoError(t, err)
	})

	t.Run("rejects unusable projects", func(t *testing.T) {
		for _, project := range []string{"", "   ", "/", ".", "..", "..."} {
			_, err := ProjectID(project)

			require.ErrorContains(t, err, "session:", "project %q", project)
		}
	})
}

// TestProjectDir verifies project directory composition.
func TestProjectDir(t *testing.T) {
	t.Run("joins the base directory and the identifier", func(t *testing.T) {
		id, err := ProjectID("/home/eduardo/projects/myproject")
		require.NoError(t, err)

		dir, err := ProjectDir("/base/sessions", id)
		require.NoError(t, err)

		require.Equal(t, filepath.Join("/base/sessions", id), dir)
	})

	t.Run("rejects invalid identifiers", func(t *testing.T) {
		for _, id := range []string{"", ".", "..", ".hidden", "with/slash", "with\\slash"} {
			_, err := ProjectDir("/base/sessions", id)

			require.ErrorContains(t, err, "invalid project id", "id %q", id)
		}
	})

	t.Run("requires a base directory", func(t *testing.T) {
		id, err := ProjectID("/home/eduardo/projects/myproject")
		require.NoError(t, err)

		_, err = ProjectDir("   ", id)

		require.ErrorContains(t, err, "base directory is required")
	})

	t.Run("scopes sessions per project", func(t *testing.T) {
		base := t.TempDir()
		generator := &stubGenerator{}

		firstID, err := ProjectID("/home/eduardo/projects/first")
		require.NoError(t, err)
		firstDir, err := ProjectDir(base, firstID)
		require.NoError(t, err)

		secondID, err := ProjectID("/home/eduardo/projects/second")
		require.NoError(t, err)
		secondDir, err := ProjectDir(base, secondID)
		require.NoError(t, err)
		require.NotEqual(t, firstDir, secondDir)

		first, err := Create(
			t.Context(),
			firstDir,
			Header{Agent: "coder", Model: "test/model"},
			generator,
		)
		require.NoError(t, err)
		appendMessage(t, first, llm.RoleUser, "first project session")
		require.NoError(t, first.Close())

		second, err := Create(
			t.Context(),
			secondDir,
			Header{Agent: "coder", Model: "test/model"},
			generator,
		)
		require.NoError(t, err)
		appendMessage(t, second, llm.RoleUser, "second project session")
		require.NoError(t, second.Close())

		firstInfos, err := List(firstDir)
		require.NoError(t, err)
		require.Len(t, firstInfos, 1)
		require.Equal(t, first.ID(), firstInfos[0].ID)
		require.Equal(t, "first project session", firstInfos[0].Title)

		secondInfos, err := List(secondDir)
		require.NoError(t, err)
		require.Len(t, secondInfos, 1)
		require.Equal(t, second.ID(), secondInfos[0].ID)
		require.Equal(t, "second project session", secondInfos[0].Title)
	})
}
