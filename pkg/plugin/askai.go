package plugin

import "context"

// ActionAskAI asks the launcher's AI chat to handle some text
// (docs/plugin-guide.md "Asking AI"): the host hands the Payload, an
// AskAI, to the enabled plugin implementing AIAsker (builtin.ai), which
// opens a new chat sending Instruction with Text as a quote. With AI turned
// off, the chat first shows the prompt to turn it on and sends once it is.
// Plugins cannot call each other; this host action is how one uses the AI
// chat. For an AI using the launcher's tools it is a "change" action (it
// sends text to the AI).
const ActionAskAI ActionKind = "AskAI"

// AskAI is the Payload of ActionAskAI.
type AskAI struct {
	// Instruction is what the AI should do, e.g. "Translate the quoted text
	// into Japanese.". Without Text it is the whole message.
	Instruction string `json:"instruction"`
	// Text is the text to handle, sent as a quote after Instruction.
	Text string `json:"text,omitempty"`
	// Title is the chat's title (user-visible: from your catalog); "" for
	// the start of the message.
	Title string `json:"title,omitempty"`
}

// AIAsker is implemented by the plugin that answers ActionAskAI
// (builtin.ai). The host calls the first enabled one; its Result is applied
// as an action's (usually a Push of its chat view).
type AIAsker interface {
	AskAI(ctx context.Context, q AskAI) (Result, error)
}
