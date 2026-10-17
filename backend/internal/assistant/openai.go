package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// openAI speaks the Chat Completions API (POST {base}/chat/completions,
// streamed as server-sent events). Chosen over OpenAI's newer Responses API
// on purpose: Chat Completions is the one every OpenAI-compatible server
// speaks - OpenRouter, vLLM, LM Studio, the gateways companies put in front
// of their models - and the driver is for all of them.
type openAI struct {
	t    *transport
	base string
}

func (o *openAI) Kind() Kind { return KindOpenAI }

func (o *openAI) headers() map[string]string {
	h := map[string]string{"Accept": "text/event-stream"}
	if o.t.key != "" {
		h["Authorization"] = "Bearer " + o.t.key
	}
	return h
}

type oaFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type oaTool struct {
	Type     string     `json:"type"`
	Function oaFunction `json:"function"`
}

type oaCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaToolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function oaCallFunction `json:"function"`
}

type oaMessage struct {
	Role string `json:"role"`
	// Content is a pointer so an assistant turn of tool calls alone says
	// `"content": null`, as the API wants it.
	Content    *string      `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type oaRequest struct {
	Model         string           `json:"model"`
	Messages      []oaMessage      `json:"messages"`
	Tools         []oaTool         `json:"tools,omitempty"`
	Stream        bool             `json:"stream"`
	StreamOptions *oaStreamOptions `json:"stream_options,omitempty"`
	// ⚠ OpenAI's own API takes max_completion_tokens (max_tokens is
	// deprecated there and refused by its reasoning models); the
	// compatible servers know max_tokens. The driver sends the one the
	// address speaks (oaTokenField).
	MaxTokens           int `json:"max_tokens,omitempty"`
	MaxCompletionTokens int `json:"max_completion_tokens,omitempty"`
}

type oaChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			Refusal   string `json:"refusal"`
			ToolCalls []struct {
				Index    int            `json:"index"`
				ID       string         `json:"id"`
				Function oaCallFunction `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error json.RawMessage `json:"error"`
}

func strp(s string) *string { return &s }

// oaMessages is the conversation in Chat Completions' shape: the system
// prompt first; a tool result is a `tool` message of its own, sent before
// the person's words of the same turn.
func oaMessages(req Request) []oaMessage {
	out := []oaMessage{}
	if req.System != "" {
		out = append(out, oaMessage{Role: "system", Content: strp(req.System)})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case RoleAssistant:
			msg := oaMessage{Role: "assistant"}
			if text := m.Text(); text != "" {
				msg.Content = strp(text)
			}
			for _, c := range m.ToolCalls() {
				args := ArgsText(c.Args)
				if strings.TrimSpace(args) == "" {
					args = "{}"
				}
				msg.ToolCalls = append(msg.ToolCalls, oaToolCall{ID: c.ID, Type: "function", Function: oaCallFunction{Name: c.Name, Arguments: args}})
			}
			if msg.Content == nil && len(msg.ToolCalls) == 0 {
				msg.Content = strp("")
			}
			out = append(out, msg)
		default:
			for _, p := range m.Parts {
				if p.Kind == PartToolResult {
					out = append(out, oaMessage{Role: "tool", ToolCallID: p.CallID, Content: strp(p.Text)})
				}
			}
			if text := m.Text(); text != "" {
				out = append(out, oaMessage{Role: "user", Content: strp(text)})
			}
		}
	}
	return out
}

func oaTools(defs []ToolDef) []oaTool {
	var out []oaTool
	for _, d := range defs {
		out = append(out, oaTool{Type: "function", Function: oaFunction{Name: d.Name, Description: d.Description, Parameters: d.Schema}})
	}
	return out
}

// oaTokenField sets the reply limit under the name the address speaks.
func (o *openAI) oaTokenField(r *oaRequest, n int) {
	if hostOf(o.base) == "api.openai.com" {
		r.MaxCompletionTokens = n
	} else {
		r.MaxTokens = n
	}
}

func (o *openAI) Chat(ctx context.Context, req Request, onText func(string)) (*Turn, error) {
	body := oaRequest{
		Model:         req.Model,
		Messages:      oaMessages(req),
		Tools:         oaTools(req.Tools),
		Stream:        true,
		StreamOptions: &oaStreamOptions{IncludeUsage: true},
	}
	o.oaTokenField(&body, req.MaxTokens)
	resp, err := o.t.do(ctx, KindOpenAI, postJSON(o.base+"/chat/completions", o.headers(), body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	type pending struct {
		id, name string
		args     strings.Builder
	}
	calls := map[int]*pending{}
	var text strings.Builder
	turn := &Turn{Model: req.Model}
	finish := ""
	refused := ""
	err = readSSE(resp.Body, func(_, data string) error {
		if strings.TrimSpace(data) == "[DONE]" {
			return errStop
		}
		var c oaChunk
		if err := json.Unmarshal([]byte(data), &c); err != nil {
			return &ProviderError{Provider: KindOpenAI, Message: "a stream chunk is not JSON"}
		}
		if len(c.Error) > 0 && string(c.Error) != "null" {
			return &ProviderError{Provider: KindOpenAI, Message: o.t.redact(errorWords([]byte(`{"error":` + string(c.Error) + `}`)))}
		}
		if c.Model != "" {
			turn.Model = c.Model
		}
		if c.Usage != nil {
			turn.Usage = Usage{InputTokens: c.Usage.PromptTokens, OutputTokens: c.Usage.CompletionTokens}
		}
		for _, ch := range c.Choices {
			if ch.Delta.Content != "" {
				text.WriteString(ch.Delta.Content)
				onText(ch.Delta.Content)
			}
			if ch.Delta.Refusal != "" {
				refused += ch.Delta.Refusal
			}
			for _, tc := range ch.Delta.ToolCalls {
				p := calls[tc.Index]
				if p == nil {
					p = &pending{}
					calls[tc.Index] = p
				}
				if tc.ID != "" {
					p.id = tc.ID
				}
				if tc.Function.Name != "" {
					p.name += tc.Function.Name
				}
				p.args.WriteString(tc.Function.Arguments)
			}
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				finish = *ch.FinishReason
			}
		}
		return nil
	})
	if err != nil {
		return nil, o.t.streamError(KindOpenAI, err)
	}

	if s := text.String(); s != "" {
		turn.Parts = append(turn.Parts, Part{Kind: PartText, Text: s})
	}
	idx := make([]int, 0, len(calls))
	for i := range calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for n, i := range idx {
		p := calls[i]
		id := p.id
		if id == "" {
			// A server that streams no id (some local ones): one of ours,
			// unique within the turn, so the answer can name its call.
			id = fmt.Sprintf("call_%d", n)
		}
		turn.Parts = append(turn.Parts, Part{Kind: PartToolCall, CallID: id, Tool: p.name, Args: ArgsFromText(p.args.String())})
	}
	switch {
	case refused != "" || finish == "content_filter":
		turn.Stop, turn.RefusalCategory = StopRefusal, "content_filter"
	case finish == "length":
		turn.Stop = StopMaxTokens
	case len(idx) > 0:
		// "tool_calls", and a server that says "stop" while it calls tools.
		turn.Stop = StopToolUse
	case finish == "stop" || finish == "":
		turn.Stop = StopEnd
	default:
		turn.Stop = StopOther
	}
	return turn, nil
}

func (o *openAI) Models(ctx context.Context) ([]string, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	h := o.headers()
	delete(h, "Accept")
	if err := o.t.getJSON(ctx, KindOpenAI, o.base+"/models", h, &out); err != nil {
		return nil, err
	}
	var ids []string
	for _, m := range out.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}
