package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// anthropic speaks Anthropic's Messages API (POST {base}/v1/messages,
// streamed as server-sent events), with tool use.
//
// Three things the API asks of a client that this driver does, each from the
// API's documentation:
//
//   - The model's thinking blocks come back with a signature and must be sent
//     back exactly as they came, in the conversation that made them
//     (PartOpaque). A request whose earlier turns changed has them refused
//     with a 400 naming the signature; the driver then sends the request once
//     more without any (stripThinking) - the model answers without the
//     reasoning those blocks carried, and nothing breaks.
//   - On Anthropic's own address, a request the model's safety classifiers
//     decline is run once more on the model Anthropic recommends for that
//     kind of request (`fallbacks: "default"`, the server-side fallback),
//     for the models that offer it. A `fallback` block marks where it
//     switched; what came before it is kept only as text.
//   - A tool's arguments stream as they are written (eager input streaming,
//     on Anthropic's own address only - a gateway in front of it may not know
//     the field). Arguments that do not parse are not run: the loop answers
//     the call with an error the model can correct.
type anthropic struct {
	t    *transport
	base string
}

const anthropicVersion = "2023-06-01"

// anthropicFallbackBeta gates `fallbacks: "default"`.
const anthropicFallbackBeta = "server-side-fallback-2026-07-01"

// anthropicFallbackModels are the models the server-side fallback serves.
var anthropicFallbackModels = map[string]bool{
	"claude-fable-5-1":  true,
	"claude-opus-5-5":   true,
	"claude-opus-5":     true,
	"claude-sonnet-5-5": true,
}

func (a *anthropic) Kind() Kind { return KindAnthropic }

// official reports whether the address is Anthropic's own API, where the
// fields a proxy may not know (eager input streaming, the fallback, the
// request-wide cache marker) are sent.
func (a *anthropic) official() bool { return hostOf(a.base) == "api.anthropic.com" }

func (a *anthropic) headers(model string) map[string]string {
	h := map[string]string{
		"x-api-key":         a.t.key,
		"anthropic-version": anthropicVersion,
		"Accept":            "text/event-stream",
	}
	if a.official() && anthropicFallbackModels[model] {
		h["anthropic-beta"] = anthropicFallbackBeta
	}
	return h
}

type anTool struct {
	Name                string          `json:"name"`
	Description         string          `json:"description,omitempty"`
	InputSchema         json.RawMessage `json:"input_schema"`
	EagerInputStreaming bool            `json:"eager_input_streaming,omitempty"`
}

type anMessage struct {
	Role    string            `json:"role"`
	Content []json.RawMessage `json:"content"`
}

type anCache struct {
	Type string `json:"type"`
}

type anRequest struct {
	Model     string      `json:"model"`
	MaxTokens int         `json:"max_tokens"`
	System    string      `json:"system,omitempty"`
	Messages  []anMessage `json:"messages"`
	Tools     []anTool    `json:"tools,omitempty"`
	Stream    bool        `json:"stream"`
	// CacheControl marks the request for automatic prompt caching: every
	// round of a tool loop sends the whole conversation again, and the
	// cached part is billed at a fraction.
	CacheControl *anCache `json:"cache_control,omitempty"`
	Fallbacks    string   `json:"fallbacks,omitempty"`
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// anMessages is the conversation in the Messages API's shape. A person's
// turn carries the tool results first, then their words; a block a provider
// other than Anthropic left (PartOpaque) is not sent.
func anMessages(msgs []Message, withThinking bool) []anMessage {
	out := []anMessage{}
	for _, m := range msgs {
		var content []json.RawMessage
		if m.Role == RoleAssistant {
			for _, p := range m.Parts {
				switch p.Kind {
				case PartText:
					if p.Text != "" {
						content = append(content, mustJSON(map[string]any{"type": "text", "text": p.Text}))
					}
				case PartToolCall:
					input := ArgsObject(p.Args)
					if input == nil {
						input = map[string]any{}
					}
					content = append(content, mustJSON(map[string]any{"type": "tool_use", "id": p.CallID, "name": p.Tool, "input": input}))
				case PartOpaque:
					if withThinking && p.Provider == string(KindAnthropic) && len(p.Raw) > 0 {
						content = append(content, p.Raw)
					}
				}
			}
			if len(content) > 0 {
				out = append(out, anMessage{Role: "assistant", Content: content})
			}
			continue
		}
		for _, p := range m.Parts {
			if p.Kind == PartToolResult {
				block := map[string]any{"type": "tool_result", "tool_use_id": p.CallID, "content": p.Text}
				if p.IsError {
					block["is_error"] = true
				}
				content = append(content, mustJSON(block))
			}
		}
		for _, p := range m.Parts {
			if p.Kind == PartText && p.Text != "" {
				content = append(content, mustJSON(map[string]any{"type": "text", "text": p.Text}))
			}
		}
		if len(content) > 0 {
			out = append(out, anMessage{Role: "user", Content: content})
		}
	}
	return out
}

// hasThinking reports whether msgs carry a block of Anthropic's to send back.
func hasThinking(msgs []Message) bool {
	for _, m := range msgs {
		for _, p := range m.Parts {
			if p.Kind == PartOpaque && p.Provider == string(KindAnthropic) {
				return true
			}
		}
	}
	return false
}

// signatureRefused is the 400 a request gets when a thinking block it sends
// back no longer belongs to the conversation (an earlier turn changed, the
// tools changed, another model wrote it).
func signatureRefused(err error) bool {
	var perr *ProviderError
	if !errors.As(err, &perr) || perr.Status != 400 {
		return false
	}
	m := strings.ToLower(perr.Message)
	return strings.Contains(m, "signature") && strings.Contains(m, "thinking")
}

func (a *anthropic) Chat(ctx context.Context, req Request, onText func(string)) (*Turn, error) {
	turn, err := a.chat(ctx, req, onText, true)
	if err != nil && hasThinking(req.Messages) && signatureRefused(err) {
		return a.chat(ctx, req, onText, false)
	}
	return turn, err
}

// anBlock is one content block of a streamed reply, as it is put together.
type anBlock struct {
	typ       string
	text      strings.Builder
	signature strings.Builder
	id, name  string
	input     strings.Builder
	raw       json.RawMessage
}

type anEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ContentBlock json.RawMessage `json:"content_block"`
	Delta        *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
		StopDetails *struct {
			Category string `json:"category"`
		} `json:"stop_details"`
	} `json:"delta"`
	Usage *struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (a *anthropic) chat(ctx context.Context, req Request, onText func(string), withThinking bool) (*Turn, error) {
	official := a.official()
	body := anRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		System:    req.System,
		Messages:  anMessages(req.Messages, withThinking),
		Stream:    true,
	}
	for _, d := range req.Tools {
		body.Tools = append(body.Tools, anTool{Name: d.Name, Description: d.Description, InputSchema: d.Schema, EagerInputStreaming: official})
	}
	if official {
		body.CacheControl = &anCache{Type: "ephemeral"}
		if anthropicFallbackModels[req.Model] {
			body.Fallbacks = "default"
		}
	}
	resp, err := a.t.do(ctx, KindAnthropic, postJSON(a.base+"/v1/messages", a.headers(req.Model), body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	blocks := map[int]*anBlock{}
	var order []int
	fallbackAt := -1
	turn := &Turn{Model: req.Model}
	stop := ""
	err = readSSE(resp.Body, func(_, data string) error {
		var ev anEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return &ProviderError{Provider: KindAnthropic, Message: "a stream event is not JSON"}
		}
		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				if ev.Message.Model != "" {
					turn.Model = ev.Message.Model
				}
				u := ev.Message.Usage
				turn.Usage.InputTokens = u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
			}
		case "content_block_start":
			var head struct {
				Type      string `json:"type"`
				Text      string `json:"text"`
				Thinking  string `json:"thinking"`
				Signature string `json:"signature"`
				ID        string `json:"id"`
				Name      string `json:"name"`
			}
			_ = json.Unmarshal(ev.ContentBlock, &head)
			b := &anBlock{typ: head.Type, id: head.ID, name: head.Name, raw: append(json.RawMessage(nil), ev.ContentBlock...)}
			b.text.WriteString(head.Text + head.Thinking)
			b.signature.WriteString(head.Signature)
			if head.Text != "" {
				onText(head.Text)
			}
			if head.Type == "fallback" {
				fallbackAt = len(order)
			}
			blocks[ev.Index] = b
			order = append(order, ev.Index)
		case "content_block_delta":
			b := blocks[ev.Index]
			if b == nil || ev.Delta == nil {
				return nil
			}
			switch ev.Delta.Type {
			case "text_delta":
				b.text.WriteString(ev.Delta.Text)
				onText(ev.Delta.Text)
			case "thinking_delta":
				b.text.WriteString(ev.Delta.Thinking)
			case "signature_delta":
				b.signature.WriteString(ev.Delta.Signature)
			case "input_json_delta":
				b.input.WriteString(ev.Delta.PartialJSON)
			}
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				stop = ev.Delta.StopReason
				if ev.Delta.StopDetails != nil {
					turn.RefusalCategory = ev.Delta.StopDetails.Category
				}
			}
			if ev.Usage != nil {
				turn.Usage.OutputTokens = ev.Usage.OutputTokens
			}
		case "message_stop":
			return errStop
		case "error":
			msg := "the stream reported an error"
			if ev.Error != nil && ev.Error.Message != "" {
				msg = ev.Error.Message
			}
			return &ProviderError{Provider: KindAnthropic, Message: a.t.redact(msg)}
		}
		return nil
	})
	if err != nil {
		return nil, a.t.streamError(KindAnthropic, err)
	}

	for pos, i := range order {
		b := blocks[i]
		// Before the last switch to a fallback model, only the words are
		// kept: the declined model's thinking and tool calls are not sent
		// back (the fallback's rule for echoing a turn).
		beforeSwitch := pos < fallbackAt
		switch b.typ {
		case "text":
			if s := b.text.String(); s != "" {
				turn.Parts = append(turn.Parts, Part{Kind: PartText, Text: s})
			}
		case "thinking":
			if beforeSwitch {
				continue
			}
			raw := mustJSON(map[string]any{"type": "thinking", "thinking": b.text.String(), "signature": b.signature.String()})
			turn.Parts = append(turn.Parts, Part{Kind: PartOpaque, Provider: string(KindAnthropic), Raw: raw})
		case "redacted_thinking":
			if beforeSwitch {
				continue
			}
			turn.Parts = append(turn.Parts, Part{Kind: PartOpaque, Provider: string(KindAnthropic), Raw: b.raw})
		case "tool_use":
			if beforeSwitch {
				continue
			}
			turn.Parts = append(turn.Parts, Part{Kind: PartToolCall, CallID: b.id, Tool: b.name, Args: ArgsFromText(b.input.String())})
		}
	}

	switch stop {
	case "end_turn", "stop_sequence":
		turn.Stop = StopEnd
	case "tool_use":
		turn.Stop = StopToolUse
	case "max_tokens", "model_context_window_exceeded":
		turn.Stop = StopMaxTokens
	case "refusal":
		turn.Stop = StopRefusal
	default:
		turn.Stop = StopOther
	}
	return turn, nil
}

func (a *anthropic) Models(ctx context.Context) ([]string, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	h := a.headers("")
	delete(h, "Accept")
	if err := a.t.getJSON(ctx, KindAnthropic, a.base+"/v1/models?limit=100", h, &out); err != nil {
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
