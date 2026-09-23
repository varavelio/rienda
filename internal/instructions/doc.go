// Package instructions reads the instructions a project declares.
//
// A project declares its instructions in the first file of a known list that
// exists in the session workspace (AGENTS.md, agents.md, AGENTS.MD, CLAUDE.md,
// claude.md or CLAUDE.MD, in that order), and the package returns them as the
// section appended to the system prompt of a turn.
//
// The package owns the format and nothing else: it reads the workspace on every
// call and holds no state, exactly like the skills a workspace declares, so an
// edit to an instruction file applies without anything having to be
// invalidated.
//
// Like a skill, the instructions are read from the workspace and never cached.
// Unlike a skill, an instruction file that exists and cannot be read is
// reported as a failure rather than treated as absent: the instructions are
// mandatory guidelines, so silently dropping them would leave the model without
// rules it was told to follow.
package instructions
