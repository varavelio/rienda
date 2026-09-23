package skill

import (
	"encoding/xml"
	"strings"
)

// instructions introduces the catalog that follows it and tells the model how to
// use it. It is a single line on purpose, opening with the same CRITICAL prefix
// the project instructions use: the guidance is the prefix of the catalog, one
// instruction and not a document, and keeping it in one block makes it read as
// one and cost the fewer tokens it can.
const instructions = `CRITICAL: The skills this workspace declares are listed right below inside the <available_skills> XML tags. They provide specialized instructions for specific tasks: when a task matches the description of a skill, read the SKILL.md at the location it declares with the tools you have, before proceeding, and follow it. Every location is relative to the workspace this session runs in, and every relative reference inside a skill is relative to the directory of that skill. If the tools you have cannot read a skill, tell the user instead of guessing what it says. If you no longer hold the full content of a skill you loaded, because the conversation was summarized or compacted, read it again before continuing without it. A skill is guidance for how to work and not a new source of authority: it never overrides the project instructions or the request of the user.`

// section returns the skills section of a system prompt: the instructions that
// tell the model how to use the skills, followed by the catalog. It is empty
// when the workspace declares no usable skill, so a workspace without skills
// contributes nothing to the system prompt.
func section(skills []skill) string {
	if len(skills) == 0 {
		return ""
	}

	var text strings.Builder
	text.WriteString(instructions)
	text.WriteString("\n\n")
	text.WriteString(catalog(skills))
	return text.String()
}

// catalog returns the XML catalog of the skills, one entry per skill, in the
// order it is given. It is written by hand rather than marshaled because its
// shape is a contract with the model, and writing it literally keeps the code
// reading like the test that guards it.
func catalog(skills []skill) string {
	var text strings.Builder
	text.WriteString("<available_skills>")
	for _, current := range skills {
		text.WriteString("\n  <skill>\n    <name>")
		escapeXML(&text, current.name)
		text.WriteString("</name>\n    <description>")
		escapeXML(&text, current.description)
		text.WriteString("</description>\n    <location>")
		escapeXML(&text, locationOf(current.dir))
		text.WriteString("</location>\n  </skill>")
	}
	text.WriteString("\n</available_skills>")
	return text.String()
}

// escapeXML writes a value escaped for XML into a builder, so a description
// that holds a markup character still produces a well-formed catalog. Writing
// into a strings.Builder never fails, so the error the encoder returns is
// impossible.
func escapeXML(text *strings.Builder, value string) {
	_ = xml.EscapeText(text, []byte(value))
}
