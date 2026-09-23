// Package skill discovers the Agent Skills a workspace declares.
//
// A skill is a directory that holds a SKILL.md file whose YAML frontmatter
// declares a name and a description. The frontmatter is the whole of what this
// package reads: the Markdown body is left to the model, which loads it with
// its own tools when a task calls for it. That is what makes the format a
// progressive disclosure one: the catalog published to the model costs a few
// tokens per skill, and the instructions behind it cost nothing until they are
// needed.
//
// The only scope is the .agents/skills directory of the session workspace,
// resolved from the same working directory the project instructions are read
// from. Discovery is one level deep, it is repeated on every turn, and it holds
// no state: a skill created, edited or removed while a session is open is
// reflected by the next turn, and nothing has to be invalidated for that to be
// true.
//
// Parsing is deliberately lenient, unlike the strict decoding this project
// applies to agent definitions, because a skill is written for every client of
// the format and not only for Rienda. Only a non-empty name and a non-empty
// description are required; every other field is ignored, a value that departs
// from the recommendations of the format is reported as a diagnostic, and a
// skill that cannot be used at all is skipped on its own.
//
// Nothing about a skill ever fails a run. Discovery returns no error for that
// reason: every problem it finds is a diagnostic, and a workspace without
// skills, without a readable skills directory, or with skills that are all
// broken simply contributes no catalog.
package skill
