// Package assistant is the server half of the AI assistant built into the
// explorer (task #269): one conversation shape every provider speaks, the
// three drivers (OpenAI and every server that speaks its Chat Completions
// API, Anthropic's Messages API, Ollama), and the loop that runs a model's
// tool calls until it answers.
//
// What the tools ARE is not this package's: the handlers hand the loop a
// Toolbox that runs filex's own MCP tools as the person who asked
// (handlers/assistant_tools.go). This package knows nothing about files,
// permissions or encryption - it moves words and tool calls between a model
// and that toolbox, within the budget an administrator set.
//
// ⚠ The provider's API key is held in memory by the driver and nowhere else
// here: it is never part of an error, a log line or an answer (redact).
package assistant

import (
	"context"
	"encoding/json"
	"strings"
)

// Role is who said a message.
type Role string

const (
	// RoleUser is the person - and the tool results the server sends back
	// for them, which every API files under the asking side.
	RoleUser Role = "user"
	// RoleAssistant is the model.
	RoleAssistant Role = "assistant"
)

// PartKind is what one piece of a message is.
type PartKind string

const (
	// PartText is words.
	PartText PartKind = "text"
	// PartToolCall is the model asking for a tool: CallID, Tool, Args.
	PartToolCall PartKind = "tool_call"
	// PartToolResult is a tool's answer to the call CallID: Text, IsError.
	PartToolResult PartKind = "tool_result"
	// PartOpaque is a block a provider asked to be sent back exactly as it
	// came (Anthropic's thinking blocks, with their signatures): Raw, and the
	// Provider it is for. Every other provider leaves it out.
	PartOpaque PartKind = "opaque"
)

// Part is one piece of a message.
type Part struct {
	Kind     PartKind        `json:"kind"`
	Text     string          `json:"text,omitempty"`
	CallID   string          `json:"call_id,omitempty"`
	Tool     string          `json:"tool,omitempty"`
	Args     json.RawMessage `json:"args,omitempty"`
	IsError  bool            `json:"is_error,omitempty"`
	Provider string          `json:"provider,omitempty"`
	Raw      json.RawMessage `json:"raw,omitempty"`
	// Context marks a text part the server wrote into the person's message:
	// what they had selected (its paths in Args). Sent to the model like any
	// text; the panel shows it as the selection, not as their words.
	Context bool `json:"context,omitempty"`
}

// Message is one turn of the conversation.
type Message struct {
	Role  Role   `json:"role"`
	Parts []Part `json:"parts"`
}

// Text is the message's words, its text parts joined.
func (m Message) Text() string {
	out := ""
	for _, p := range m.Parts {
		if p.Kind == PartText {
			out += p.Text
		}
	}
	return out
}

// ToolCalls are the message's tool calls, in order.
func (m Message) ToolCalls() []ToolCall {
	var out []ToolCall
	for _, p := range m.Parts {
		if p.Kind == PartToolCall {
			out = append(out, ToolCall{ID: p.CallID, Name: p.Tool, Args: p.Args})
		}
	}
	return out
}

// ArgsFromText makes the arguments a model wrote storable: valid JSON as it
// is, `{}` for nothing, and anything else as a JSON string holding the text -
// a reply cut off mid-call, or a model that wrote broken JSON. The loop
// refuses arguments that are not an object (ArgsObject) and tells the model;
// they are kept so the conversation still says what it asked.
func ArgsFromText(s string) json.RawMessage {
	t := strings.TrimSpace(s)
	if t == "" {
		return json.RawMessage("{}")
	}
	if json.Valid([]byte(t)) {
		return json.RawMessage(t)
	}
	b, _ := json.Marshal(s)
	return b
}

// ArgsText is the text a model wrote for a call's arguments (ArgsFromText
// read back).
func ArgsText(a json.RawMessage) string {
	var s string
	if len(a) > 0 && a[0] == '"' && json.Unmarshal(a, &s) == nil {
		return s
	}
	return string(a)
}

// ArgsObject is a call's arguments when they are a JSON object; nil when
// they are anything else.
func ArgsObject(a json.RawMessage) map[string]any {
	var m map[string]any
	if json.Unmarshal(a, &m) != nil || m == nil {
		return nil
	}
	return m
}

// ToolDef is one tool as a model is told about it: its name, what it does and
// the JSON schema of its arguments.
type ToolDef struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// Request is one call to a model.
type Request struct {
	Model     string
	System    string
	Messages  []Message
	Tools     []ToolDef
	MaxTokens int
}

// Usage is what a call (or a run of calls) cost, in tokens.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Total is input and output together.
func (u Usage) Total() int { return u.InputTokens + u.OutputTokens }

// Add sums two usages.
func (u Usage) Add(o Usage) Usage {
	return Usage{InputTokens: u.InputTokens + o.InputTokens, OutputTokens: u.OutputTokens + o.OutputTokens}
}

// Stop is why a model stopped.
type Stop string

const (
	// StopEnd: it answered.
	StopEnd Stop = "end"
	// StopToolUse: it asked for tools and waits for their answers.
	StopToolUse Stop = "tool_use"
	// StopMaxTokens: the reply hit the output limit (MaxTokens).
	StopMaxTokens Stop = "max_tokens"
	// StopRefusal: the provider declined the request (a safety classifier,
	// a content filter).
	StopRefusal Stop = "refusal"
	// StopOther: anything the drivers do not map.
	StopOther Stop = "other"
)

// Turn is what one call to a model answered.
type Turn struct {
	// Parts is the model's message: text, tool calls and the blocks its
	// provider wants back (PartOpaque), in the order they came.
	Parts []Part
	Stop  Stop
	Usage Usage
	// Model is the model that answered (a provider may name the exact
	// version, or the model it fell back to).
	Model string
	// RefusalCategory is the provider's word for why it declined, when it
	// said one.
	RefusalCategory string
}

// Provider is one model API.
type Provider interface {
	// Kind is the provider's name: KindOpenAI, KindAnthropic, KindOllama.
	Kind() Kind
	// Chat sends req and streams the reply: onText is called with every
	// piece of text as it arrives (from this goroutine; never nil-checked by
	// the caller - pass a no-op).
	Chat(ctx context.Context, req Request, onText func(string)) (*Turn, error)
	// Models lists the models the address offers, for the settings page.
	Models(ctx context.Context) ([]string, error)
}
