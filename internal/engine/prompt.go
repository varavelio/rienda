package engine

import (
	"fmt"
	"strings"

	"github.com/varavelio/rienda/internal/agent"
	"github.com/varavelio/rienda/internal/instructions"
	"github.com/varavelio/rienda/internal/skill"
)

// sectionSeparator divides two sections of the system prompt, whatever they
// are: the system prompt of the agent, the instructions of the project and the
// skills of the workspace.
const sectionSeparator = "\n\n---\n\n"

// systemPrompt builds the system instruction of the next turn as a straight
// pipeline of the sections that carry content, in this order: the system
// prompt of the agent the branch runs, the instructions the project declares
// and the skills the workspace declares. The instructions and the skills are
// read from the workspace on every call, so an edit to an instruction file or
// to a skill applies to the next run.
//
// The diagnostics of the workspace are returned alongside the prompt. They ride
// the run start event, which only the turn that opens a run emits, so a context
// measurement and the later turns of the same run read them and report
// nothing.
func (e *Engine) systemPrompt(definition agent.Agent) (string, []string, error) {
	systemPrompt := strings.TrimSpace(definition.SystemPrompt)

	projectSection, err := instructions.Section(e.workdir)
	if err != nil {
		return "", nil, fmt.Errorf("engine: %w", err)
	}
	projectSection = strings.TrimSpace(projectSection)

	skills := skill.Discover(e.workdir)
	skillsSection := strings.TrimSpace(skills.Section)

	return joinSections(systemPrompt, projectSection, skillsSection), skills.Diagnostics, nil
}

// joinSections joins the sections that carry content with the section
// separator, so a missing section never leaves a leading, a trailing or a
// doubled separator, and no section at all yields no prompt.
func joinSections(sections ...string) string {
	present := make([]string, 0, len(sections))
	for _, current := range sections {
		if current != "" {
			present = append(present, current)
		}
	}
	return strings.Join(present, sectionSeparator)
}
