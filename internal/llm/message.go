package llm

// Role identifies the author of a Message. Only user and assistant turns
// exist in a conversation: the system prompt is carried by Request.System and
// tool results are BlockToolResult blocks.
type Role string

const (
	// RoleUser marks a message authored by the user.
	RoleUser Role = "user"
	// RoleAssistant marks a message authored by the model.
	RoleAssistant Role = "assistant"
)

// Message is a single turn in a conversation.
type Message struct {
	// Role identifies the author of the message.
	Role Role

	// Blocks is the ordered content of the message.
	Blocks []Block
}
