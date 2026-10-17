package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
)

// ollama speaks a local Ollama server's own chat API (POST {base}/api/chat,
// streamed as one JSON object per line). Everything stays on the operator's
// machine: no key, no outside address.
//
// Only models that can call tools can run the assistant: Ollama refuses a
// request with tools for one that cannot ("does not support tools", 400),
// and the settings page's connection test says so before anybody asks.
type ollama struct {
	t    *transport
	base string
}

func (o *ollama) Kind() Kind { return KindOllama }

type olFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type olTool struct {
	Type     string     `json:"type"`
	Function olFunction `json:"function"`
}

type olCall struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type olMessage struct {
	Role      string   `json:"role"`
	Content   string   `json:"content"`
	ToolCalls []olCall `json:"tool_calls,omitempty"`
	ToolName  string   `json:"tool_name,omitempty"`
}

type olRequest struct {
	Model    string         `json:"model"`
	Messages []olMessage    `json:"messages"`
	Tools    []olTool       `json:"tools,omitempty"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
}

type olChunk struct {
	Model   string `json:"model"`
	Message struct {
		Content   string   `json:"content"`
		ToolCalls []olCall `json:"tool_calls"`
	} `json:"message"`
	Done            bool   `json:"done"`
	DoneReason      string `json:"done_reason"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	Error           string `json:"error"`
}

// olCallSeq numbers the calls Ollama does not name: it streams a call
// without an id, and an answer is matched to its call by one.
var olCallSeq atomic.Int64

// olMessages is the conversation in Ollama's shape: a tool's answer is a
// `tool` message naming the tool, before the person's words of the turn.
func olMessages(req Request) []olMessage {
	out := []olMessage{}
	if req.System != "" {
		out = append(out, olMessage{Role: "system", Content: req.System})
	}
	names := map[string]string{}
	for _, m := range req.Messages {
		if m.Role == RoleAssistant {
			msg := olMessage{Role: "assistant", Content: m.Text()}
			for _, c := range m.ToolCalls() {
				names[c.ID] = c.Name
				var call olCall
				call.Function.Name = c.Name
				if obj := ArgsObject(c.Args); obj != nil {
					call.Function.Arguments = c.Args
				} else {
					call.Function.Arguments = json.RawMessage("{}")
				}
				msg.ToolCalls = append(msg.ToolCalls, call)
			}
			out = append(out, msg)
			continue
		}
		for _, p := range m.Parts {
			if p.Kind == PartToolResult {
				name := p.Tool
				if name == "" {
					name = names[p.CallID]
				}
				out = append(out, olMessage{Role: "tool", Content: p.Text, ToolName: name})
			}
		}
		if text := m.Text(); text != "" {
			out = append(out, olMessage{Role: "user", Content: text})
		}
	}
	return out
}

func (o *ollama) Chat(ctx context.Context, req Request, onText func(string)) (*Turn, error) {
	body := olRequest{Model: req.Model, Messages: olMessages(req), Stream: true}
	for _, d := range req.Tools {
		body.Tools = append(body.Tools, olTool{Type: "function", Function: olFunction{Name: d.Name, Description: d.Description, Parameters: d.Schema}})
	}
	if req.MaxTokens > 0 {
		body.Options = map[string]any{"num_predict": req.MaxTokens}
	}
	resp, err := o.t.do(ctx, KindOllama, postJSON(o.base+"/api/chat", nil, body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var text strings.Builder
	var calls []olCall
	turn := &Turn{Model: req.Model}
	reason := ""
	err = readLines(resp.Body, func(line string) error {
		if strings.TrimSpace(line) == "" {
			return nil
		}
		var c olChunk
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return &ProviderError{Provider: KindOllama, Message: "a stream line is not JSON"}
		}
		if c.Error != "" {
			return &ProviderError{Provider: KindOllama, Message: o.t.redact(c.Error)}
		}
		if c.Model != "" {
			turn.Model = c.Model
		}
		if c.Message.Content != "" {
			text.WriteString(c.Message.Content)
			onText(c.Message.Content)
		}
		calls = append(calls, c.Message.ToolCalls...)
		if c.Done {
			reason = c.DoneReason
			turn.Usage = Usage{InputTokens: c.PromptEvalCount, OutputTokens: c.EvalCount}
			return errStop
		}
		return nil
	})
	if err != nil && err != errStop {
		return nil, o.t.streamError(KindOllama, err)
	}
	if s := text.String(); s != "" {
		turn.Parts = append(turn.Parts, Part{Kind: PartText, Text: s})
	}
	for _, c := range calls {
		args := string(c.Function.Arguments)
		// Some versions send the arguments as a JSON string holding the
		// object rather than the object itself.
		var inner string
		if json.Unmarshal(c.Function.Arguments, &inner) == nil {
			args = inner
		}
		turn.Parts = append(turn.Parts, Part{
			Kind:   PartToolCall,
			CallID: fmt.Sprintf("ollama_%d", olCallSeq.Add(1)),
			Tool:   c.Function.Name,
			Args:   ArgsFromText(args),
		})
	}
	switch {
	case reason == "length":
		turn.Stop = StopMaxTokens
	case len(calls) > 0:
		turn.Stop = StopToolUse
	default:
		turn.Stop = StopEnd
	}
	return turn, nil
}

func (o *ollama) Models(ctx context.Context) ([]string, error) {
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := o.t.getJSON(ctx, KindOllama, o.base+"/api/tags", nil, &out); err != nil {
		return nil, err
	}
	var names []string
	for _, m := range out.Models {
		if m.Name != "" {
			names = append(names, m.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}
