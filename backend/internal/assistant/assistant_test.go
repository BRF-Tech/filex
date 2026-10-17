package assistant

// The three drivers against fake providers that speak each API's own wire
// shape (task #269), the loop against a scripted provider, and the limits.
// Each fake answers as the provider's documentation says it streams: OpenAI's
// Chat Completions chunks, Anthropic's Messages events, Ollama's lines.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAPI records every request body and answers with fn.
type fakeAPI struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies []string
	heads  []http.Header
	paths  []string
}

func newFakeAPI(t *testing.T, fn func(n int, w http.ResponseWriter, r *http.Request, body string)) *fakeAPI {
	t.Helper()
	f := &fakeAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		n := len(f.bodies)
		f.bodies = append(f.bodies, string(b))
		f.heads = append(f.heads, r.Header.Clone())
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()
		fn(n, w, r, string(b))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) body(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[i]
}

func (f *fakeAPI) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bodies)
}

func sse(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		_, _ = io.WriteString(w, e+"\n\n")
	}
}

func userMsg(text string) []Message {
	return []Message{{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: text}}}}
}

var listTool = ToolDef{Name: "file_list", Description: "List a folder.", Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}

func provider(t *testing.T, kind Kind, base, key string) Provider {
	t.Helper()
	p, err := New(Settings{Provider: kind, BaseURL: base, Model: "m"}, key, nil)
	require.NoError(t, err)
	return p
}

// OpenAI: text and a tool call whose arguments arrive in pieces across
// chunks, the usage chunk, and the request in Chat Completions' shape - the
// tools as functions, the limit as max_tokens on an address that is not
// OpenAI's own.
func TestOpenAI_StreamsTextAndAToolCallInPieces(t *testing.T) {
	api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ string) {
		sse(w,
			`data: {"model":"m-2026","choices":[{"delta":{"content":"Looking "}}]}`,
			`data: {"choices":[{"delta":{"content":"now."}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"file_list","arguments":"{\"pa"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"docs://\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: {"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7}}`,
			`data: [DONE]`,
		)
	})
	p := provider(t, KindOpenAI, api.srv.URL+"/v1", "sk-test")
	var streamed strings.Builder
	turn, err := p.Chat(context.Background(), Request{Model: "m", System: "sys", Messages: userMsg("hi"), Tools: []ToolDef{listTool}, MaxTokens: 100}, func(s string) { streamed.WriteString(s) })
	require.NoError(t, err)
	assert.Equal(t, "Looking now.", streamed.String())
	assert.Equal(t, StopToolUse, turn.Stop)
	assert.Equal(t, "m-2026", turn.Model)
	assert.Equal(t, Usage{InputTokens: 11, OutputTokens: 7}, turn.Usage)
	calls := Message{Role: RoleAssistant, Parts: turn.Parts}.ToolCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "call_1", calls[0].ID)
	assert.Equal(t, "file_list", calls[0].Name)
	assert.JSONEq(t, `{"path":"docs://"}`, string(calls[0].Args))

	var sent map[string]any
	require.NoError(t, json.Unmarshal([]byte(api.body(0)), &sent))
	assert.Equal(t, "/v1/chat/completions", api.paths[0])
	assert.Equal(t, "Bearer sk-test", api.heads[0].Get("Authorization"))
	assert.Equal(t, true, sent["stream"])
	assert.EqualValues(t, 100, sent["max_tokens"], "a compatible server is sent max_tokens")
	assert.Nil(t, sent["max_completion_tokens"])
	tools := sent["tools"].([]any)
	assert.Equal(t, "function", tools[0].(map[string]any)["type"])
	msgs := sent["messages"].([]any)
	assert.Equal(t, "system", msgs[0].(map[string]any)["role"])
}

// The answer to a call goes back as a `tool` message naming the call, after
// the model's turn with the call in it.
func TestOpenAI_SendsToolResultsAsToolMessages(t *testing.T) {
	api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ string) {
		sse(w, `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`, `data: [DONE]`)
	})
	p := provider(t, KindOpenAI, api.srv.URL+"/v1", "")
	history := []Message{
		{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "list"}}},
		{Role: RoleAssistant, Parts: []Part{{Kind: PartToolCall, CallID: "c1", Tool: "file_list", Args: json.RawMessage(`{"path":"a://"}`)}}},
		{Role: RoleUser, Parts: []Part{{Kind: PartToolResult, CallID: "c1", Tool: "file_list", Text: `{"entries":[]}`}}},
	}
	turn, err := p.Chat(context.Background(), Request{Model: "m", Messages: history, MaxTokens: 50}, func(string) {})
	require.NoError(t, err)
	assert.Equal(t, StopEnd, turn.Stop)
	var sent struct {
		Messages []map[string]any `json:"messages"`
	}
	require.NoError(t, json.Unmarshal([]byte(api.body(0)), &sent))
	require.Len(t, sent.Messages, 3)
	assert.Nil(t, sent.Messages[1]["content"], "a turn of tool calls alone says content: null")
	assert.Equal(t, "tool", sent.Messages[2]["role"])
	assert.Equal(t, "c1", sent.Messages[2]["tool_call_id"])
	assert.Empty(t, api.heads[0].Get("Authorization"), "no key, no header")
}

// On OpenAI's own address the reply limit is max_completion_tokens.
func TestOpenAI_OwnAddressTakesMaxCompletionTokens(t *testing.T) {
	o := &openAI{t: &transport{}, base: "https://api.openai.com/v1"}
	var r oaRequest
	o.oaTokenField(&r, 300)
	assert.Equal(t, 300, r.MaxCompletionTokens)
	assert.Zero(t, r.MaxTokens)
}

// Anthropic: a thinking block (empty text, a signature that arrives as its
// own delta), words, a tool_use whose input arrives in pieces - and the
// thinking block goes back exactly as it came on the next request.
func TestAnthropic_ThinkingAndToolUseAndTheBlockGoesBackAsItCame(t *testing.T) {
	api := newFakeAPI(t, func(n int, w http.ResponseWriter, _ *http.Request, _ string) {
		if n > 0 {
			sse(w,
				`event: message_start`+"\n"+`data: {"type":"message_start","message":{"model":"claude-opus-5-5","usage":{"input_tokens":30}}}`,
				`event: content_block_start`+"\n"+`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`event: content_block_delta`+"\n"+`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Done."}}`,
				`event: message_delta`+"\n"+`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
				`event: message_stop`+"\n"+`data: {"type":"message_stop"}`,
			)
			return
		}
		sse(w,
			`event: message_start`+"\n"+`data: {"type":"message_start","message":{"model":"claude-opus-5-5","usage":{"input_tokens":20,"cache_read_input_tokens":5}}}`,
			`event: content_block_start`+"\n"+`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
			`event: content_block_delta`+"\n"+`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-abc"}}`,
			`event: content_block_stop`+"\n"+`data: {"type":"content_block_stop","index":0}`,
			`event: content_block_start`+"\n"+`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
			`event: content_block_delta`+"\n"+`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Let me look."}}`,
			`event: content_block_start`+"\n"+`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"file_list","input":{}}}`,
			`event: content_block_delta`+"\n"+`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`,
			`event: content_block_delta`+"\n"+`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"a://\"}"}}`,
			`event: ping`+"\n"+`data: {"type":"ping"}`,
			`event: message_delta`+"\n"+`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}`,
			`event: message_stop`+"\n"+`data: {"type":"message_stop"}`,
		)
	})
	p := provider(t, KindAnthropic, api.srv.URL, "sk-ant-test")
	turn, err := p.Chat(context.Background(), Request{Model: "claude-opus-5-5", System: "sys", Messages: userMsg("hi"), Tools: []ToolDef{listTool}, MaxTokens: 1000}, func(string) {})
	require.NoError(t, err)
	assert.Equal(t, StopToolUse, turn.Stop)
	assert.Equal(t, Usage{InputTokens: 25, OutputTokens: 12}, turn.Usage)
	require.Len(t, turn.Parts, 3)
	assert.Equal(t, PartOpaque, turn.Parts[0].Kind)
	assert.JSONEq(t, `{"type":"thinking","thinking":"","signature":"sig-abc"}`, string(turn.Parts[0].Raw))
	assert.Equal(t, "Let me look.", turn.Parts[1].Text)
	assert.Equal(t, "toolu_1", turn.Parts[2].CallID)
	assert.JSONEq(t, `{"path":"a://"}`, string(turn.Parts[2].Args))

	assert.Equal(t, "/v1/messages", api.paths[0])
	assert.Equal(t, "sk-ant-test", api.heads[0].Get("x-api-key"))
	assert.Equal(t, "2023-06-01", api.heads[0].Get("anthropic-version"))
	assert.Empty(t, api.heads[0].Get("anthropic-beta"), "a test address is not Anthropic's own: no fallback beta")
	var first map[string]any
	require.NoError(t, json.Unmarshal([]byte(api.body(0)), &first))
	assert.Nil(t, first["fallbacks"])
	tool := first["tools"].([]any)[0].(map[string]any)
	assert.NotNil(t, tool["input_schema"])
	assert.Nil(t, tool["eager_input_streaming"], "off on an address that may be a proxy")

	history := append(userMsg("hi"),
		Message{Role: RoleAssistant, Parts: turn.Parts},
		Message{Role: RoleUser, Parts: []Part{{Kind: PartToolResult, CallID: "toolu_1", Tool: "file_list", Text: "[]"}}},
	)
	_, err = p.Chat(context.Background(), Request{Model: "claude-opus-5-5", Messages: history, Tools: []ToolDef{listTool}, MaxTokens: 1000}, func(string) {})
	require.NoError(t, err)
	var second struct {
		Messages []struct {
			Role    string            `json:"role"`
			Content []json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal([]byte(api.body(1)), &second))
	require.Len(t, second.Messages, 3)
	assert.JSONEq(t, `{"type":"thinking","thinking":"","signature":"sig-abc"}`, string(second.Messages[1].Content[0]), "sent back exactly as it came")
	assert.Contains(t, string(second.Messages[2].Content[0]), `"tool_use_id":"toolu_1"`)
}

// A thinking block that no longer belongs to the conversation is refused
// with a 400 naming its signature: the driver asks once more without any.
func TestAnthropic_ARefusedSignatureIsAskedAgainWithoutThinking(t *testing.T) {
	api := newFakeAPI(t, func(n int, w http.ResponseWriter, _ *http.Request, _ string) {
		if n == 0 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"messages.1.content.0: Invalid `+"`signature`"+` in `+"`thinking`"+` block. The block is bound to a different conversation."}}`)
			return
		}
		sse(w,
			`data: {"type":"message_start","message":{"model":"m","usage":{"input_tokens":1}}}`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"fine"}}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
			`data: {"type":"message_stop"}`,
		)
	})
	p := provider(t, KindAnthropic, api.srv.URL, "k")
	history := append(userMsg("a"),
		Message{Role: RoleAssistant, Parts: []Part{{Kind: PartOpaque, Provider: string(KindAnthropic), Raw: json.RawMessage(`{"type":"thinking","thinking":"","signature":"old"}`)}, {Kind: PartText, Text: "hello"}}},
		Message{Role: RoleUser, Parts: []Part{{Kind: PartText, Text: "b"}}},
	)
	turn, err := p.Chat(context.Background(), Request{Model: "m", Messages: history, MaxTokens: 100}, func(string) {})
	require.NoError(t, err)
	assert.Equal(t, "fine", turn.Parts[0].Text)
	require.Equal(t, 2, api.count())
	assert.Contains(t, api.body(0), `"signature":"old"`)
	assert.NotContains(t, api.body(1), "signature", "the second request carries no thinking block")
}

// A refusal stops the turn with the provider's category.
func TestAnthropic_ARefusalSaysItsCategory(t *testing.T) {
	api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ string) {
		sse(w,
			`data: {"type":"message_start","message":{"model":"m","usage":{"input_tokens":1}}}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber"}},"usage":{"output_tokens":0}}`,
			`data: {"type":"message_stop"}`,
		)
	})
	turn, err := provider(t, KindAnthropic, api.srv.URL, "k").Chat(context.Background(), Request{Model: "m", Messages: userMsg("x"), MaxTokens: 10}, func(string) {})
	require.NoError(t, err)
	assert.Equal(t, StopRefusal, turn.Stop)
	assert.Equal(t, "cyber", turn.RefusalCategory)
}

// Ollama: a line per chunk, a tool call with its arguments as an object and
// no id (the driver names it), the counts on the last line.
func TestOllama_LinesAndAToolCallWithoutAnID(t *testing.T) {
	api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ string) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, line := range []string{
			`{"model":"qwen3","message":{"role":"assistant","content":"Hm"},"done":false}`,
			`{"model":"qwen3","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"file_list","arguments":{"path":"a://"}}}]},"done":false}`,
			`{"model":"qwen3","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":9,"eval_count":4}`,
		} {
			_, _ = io.WriteString(w, line+"\n")
		}
	})
	p := provider(t, KindOllama, api.srv.URL, "")
	turn, err := p.Chat(context.Background(), Request{Model: "qwen3", System: "s", Messages: userMsg("hi"), Tools: []ToolDef{listTool}, MaxTokens: 64}, func(string) {})
	require.NoError(t, err)
	assert.Equal(t, StopToolUse, turn.Stop)
	assert.Equal(t, Usage{InputTokens: 9, OutputTokens: 4}, turn.Usage)
	calls := Message{Role: RoleAssistant, Parts: turn.Parts}.ToolCalls()
	require.Len(t, calls, 1)
	assert.NotEmpty(t, calls[0].ID)
	assert.JSONEq(t, `{"path":"a://"}`, string(calls[0].Args))
	assert.Equal(t, "/api/chat", api.paths[0])
	var sent map[string]any
	require.NoError(t, json.Unmarshal([]byte(api.body(0)), &sent))
	assert.EqualValues(t, 64, sent["options"].(map[string]any)["num_predict"])

	// The answer goes back as a `tool` message naming the tool.
	history := append(userMsg("hi"),
		Message{Role: RoleAssistant, Parts: turn.Parts},
		Message{Role: RoleUser, Parts: []Part{{Kind: PartToolResult, CallID: calls[0].ID, Text: "[]"}}},
	)
	_, err = p.Chat(context.Background(), Request{Model: "qwen3", Messages: history, MaxTokens: 64}, func(string) {})
	require.NoError(t, err)
	assert.Contains(t, api.body(1), `"role":"tool"`)
	assert.Contains(t, api.body(1), `"tool_name":"file_list"`)
}

// A model that cannot call tools: Ollama's 400, said as the provider said it.
func TestOllama_AModelWithoutToolsIsRefused(t *testing.T) {
	api := newFakeAPI(t, func(_ int, w http.ResponseWriter, _ *http.Request, _ string) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"registry.ollama.ai/library/gemma:2b does not support tools"}`)
	})
	_, err := provider(t, KindOllama, api.srv.URL, "").Chat(context.Background(), Request{Model: "gemma:2b", Messages: userMsg("x"), Tools: []ToolDef{listTool}}, func(string) {})
	var perr *ProviderError
	require.ErrorAs(t, err, &perr)
	assert.Equal(t, 400, perr.Status)
	assert.Contains(t, perr.Message, "does not support tools")
}

// 429 with Retry-After: asked again, and the second answer is the one.
func TestTransport_ARateLimitIsAskedAgain(t *testing.T) {
	api := newFakeAPI(t, func(n int, w http.ResponseWriter, _ *http.Request, _ string) {
		if n == 0 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"slow down"}}`)
			return
		}
		sse(w, `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`, `data: [DONE]`)
	})
	turn, err := provider(t, KindOpenAI, api.srv.URL, "").Chat(context.Background(), Request{Model: "m", Messages: userMsg("x")}, func(string) {})
	require.NoError(t, err)
	assert.Equal(t, "ok", turn.Parts[0].Text)
	assert.Equal(t, 2, api.count())
}

// ⚠ The key never comes back out: a gateway that echoes the request into its
// error has the key taken out of what the driver says.
func TestTransport_TheKeyIsNeverInAnError(t *testing.T) {
	const key = "sk-live-0123456789abcdef"
	api := newFakeAPI(t, func(_ int, w http.ResponseWriter, r *http.Request, _ string) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid key `+strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")+`"}}`)
	})
	_, err := provider(t, KindOpenAI, api.srv.URL, key).Chat(context.Background(), Request{Model: "m", Messages: userMsg("x")}, func(string) {})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), key)
	assert.Contains(t, err.Error(), "***")
	var perr *ProviderError
	require.ErrorAs(t, err, &perr)
	assert.True(t, perr.Unauthorized())
}

// Models: each provider's listing endpoint.
func TestModels_EachProvidersList(t *testing.T) {
	api := newFakeAPI(t, func(_ int, w http.ResponseWriter, r *http.Request, _ string) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = io.WriteString(w, `{"data":[{"id":"b"},{"id":"a"}]}`)
		case "/api/tags":
			_, _ = io.WriteString(w, `{"models":[{"name":"llama3.2"}]}`)
		default:
			w.WriteHeader(404)
		}
	})
	for _, c := range []struct {
		kind Kind
		base string
		want []string
	}{
		{KindOpenAI, api.srv.URL + "/v1", []string{"a", "b"}},
		{KindAnthropic, api.srv.URL, []string{"a", "b"}},
		{KindOllama, api.srv.URL, []string{"llama3.2"}},
	} {
		got, err := provider(t, c.kind, c.base, "k").Models(context.Background())
		require.NoError(t, err, c.kind)
		assert.Equal(t, c.want, got, c.kind)
	}
}

// ── the loop ─────────────────────────────────────────────────────────────────

// scripted is a provider that answers each call with the next turn.
type scripted struct {
	turns []*Turn
	reqs  []Request
}

func (s *scripted) Kind() Kind                               { return KindOpenAI }
func (s *scripted) Models(context.Context) ([]string, error) { return nil, nil }
func (s *scripted) Chat(_ context.Context, req Request, onText func(string)) (*Turn, error) {
	s.reqs = append(s.reqs, req)
	if len(s.turns) == 0 {
		return &Turn{Parts: []Part{{Kind: PartText, Text: "end"}}, Stop: StopEnd}, nil
	}
	t := s.turns[0]
	s.turns = s.turns[1:]
	for _, p := range t.Parts {
		if p.Kind == PartText {
			onText(p.Text)
		}
	}
	return t, nil
}

func callTurn(ids ...string) *Turn {
	t := &Turn{Stop: StopToolUse, Usage: Usage{InputTokens: 10, OutputTokens: 10}}
	for _, id := range ids {
		t.Parts = append(t.Parts, Part{Kind: PartToolCall, CallID: id, Tool: "file_list", Args: json.RawMessage(`{}`)})
	}
	return t
}

// box is a toolbox that answers every call, and waits for the person on the
// ones named in wait.
type box struct {
	wait map[string]bool
	ran  []ToolCall
}

func (b *box) Defs() []ToolDef { return []ToolDef{listTool} }
func (b *box) Run(_ context.Context, c ToolCall) ToolOutcome {
	if b.wait[c.ID] && c.Decision == DecisionNone {
		return ToolOutcome{Pending: true}
	}
	b.ran = append(b.ran, c)
	if c.Decision == DecisionDeny {
		return ToolOutcome{Text: "DENIED", IsError: true}
	}
	return ToolOutcome{Text: "ran " + c.ID}
}

func TestLoop_RunsTheToolsUntilTheModelAnswers(t *testing.T) {
	p := &scripted{turns: []*Turn{callTurn("c1", "c2"), {Parts: []Part{{Kind: PartText, Text: "All done."}}, Stop: StopEnd}}}
	b := &box{}
	var added []Message
	l := &Loop{Provider: p, Model: "m", Tools: b, MaxRounds: 5, MaxOutputTokens: 100, Budget: 10000, OnMessage: func(m Message) { added = append(added, m) }}
	res, err := l.Run(context.Background(), userMsg("go"), nil)
	require.NoError(t, err)
	assert.Equal(t, ReasonEnd, res.Reason)
	assert.Len(t, b.ran, 2)
	require.Len(t, res.Added, 3, "the calls, their answers, the reply")
	assert.Equal(t, res.Added, added)
	assert.Equal(t, "ran c1", res.Added[1].Parts[0].Text)
	assert.Len(t, p.reqs[1].Messages, 3, "the answers went back")
}

func TestLoop_StopsAtTheRoundLimitWithoutRunningMore(t *testing.T) {
	p := &scripted{turns: []*Turn{callTurn("c1"), callTurn("c2"), callTurn("c3")}}
	b := &box{}
	res, err := (&Loop{Provider: p, Model: "m", Tools: b, MaxRounds: 2, Budget: 1 << 20}).Run(context.Background(), userMsg("go"), nil)
	require.NoError(t, err)
	assert.Equal(t, ReasonRounds, res.Reason)
	assert.Len(t, b.ran, 2)
	last := res.Added[len(res.Added)-1]
	assert.True(t, strings.HasPrefix(last.Parts[0].Text, "NOT_RUN"), last.Parts[0].Text)
}

func TestLoop_TheBudgetStopsIt(t *testing.T) {
	p := &scripted{turns: []*Turn{callTurn("c1"), callTurn("c2")}}
	b := &box{}
	res, err := (&Loop{Provider: p, Model: "m", Tools: b, MaxRounds: 10, Budget: 30}).Run(context.Background(), userMsg("go"), nil)
	require.NoError(t, err)
	assert.Equal(t, ReasonBudget, res.Reason)
	assert.Len(t, b.ran, 1, "the second turn's call is past the budget")
}

// A reply cut off by its length limit: its calls may be half-written, so
// none of them runs.
func TestLoop_ACutOffReplyRunsNothing(t *testing.T) {
	cut := callTurn("c1")
	cut.Stop = StopMaxTokens
	b := &box{}
	res, err := (&Loop{Provider: &scripted{turns: []*Turn{cut}}, Model: "m", Tools: b, MaxRounds: 5, Budget: 1 << 20}).Run(context.Background(), userMsg("go"), nil)
	require.NoError(t, err)
	assert.Equal(t, ReasonMaxTokens, res.Reason)
	assert.Empty(t, b.ran)
}

// A call that waits stops the run; the answer resumes it from that call.
func TestLoop_AWaitingCallStopsAndTheAnswerResumes(t *testing.T) {
	p := &scripted{turns: []*Turn{callTurn("c1", "c2", "c3")}}
	b := &box{wait: map[string]bool{"c2": true}}
	l := &Loop{Provider: p, Model: "m", Tools: b, MaxRounds: 5, Budget: 1 << 20}
	res, err := l.Run(context.Background(), userMsg("go"), nil)
	require.NoError(t, err)
	assert.Equal(t, ReasonPending, res.Reason)
	require.NotNil(t, res.Pending)
	assert.Equal(t, "c2", res.Pending.Waiting().ID)
	assert.Len(t, b.ran, 1, "c3 did not run before c2 was answered")

	history := append(userMsg("go"), res.Added...)
	res.Pending.Waiting().Decision = DecisionDeny
	res2, err := l.Run(context.Background(), history, res.Pending)
	require.NoError(t, err)
	assert.Equal(t, ReasonEnd, res2.Reason)
	require.Len(t, b.ran, 3)
	assert.Equal(t, DecisionDeny, b.ran[1].Decision)
	answers := res2.Added[0]
	require.Len(t, answers.Parts, 3)
	assert.Equal(t, "DENIED", answers.Parts[1].Text)
}

// A refusal is not kept: the partial reply is not an answer.
func TestLoop_ARefusalKeepsNothing(t *testing.T) {
	p := &scripted{turns: []*Turn{{Parts: []Part{{Kind: PartText, Text: "Sure, here"}}, Stop: StopRefusal, RefusalCategory: "bio"}}}
	res, err := (&Loop{Provider: p, Model: "m", Tools: &box{}, MaxRounds: 5, Budget: 1 << 20}).Run(context.Background(), userMsg("x"), nil)
	require.NoError(t, err)
	assert.Equal(t, ReasonRefusal, res.Reason)
	assert.Equal(t, "bio", res.RefusalCategory)
	assert.Empty(t, res.Added)
}

func TestLimiter_PerPersonPerMinute(t *testing.T) {
	now := time.Unix(1000, 0)
	l := &Limiter{Now: func() time.Time { return now }}
	for i := 0; i < 3; i++ {
		ok, _ := l.Allow(7, 3)
		require.True(t, ok, i)
	}
	ok, retry := l.Allow(7, 3)
	assert.False(t, ok)
	assert.Equal(t, time.Minute, retry)
	ok, _ = l.Allow(8, 3)
	assert.True(t, ok, "another person is not slowed")
	now = now.Add(61 * time.Second)
	ok, _ = l.Allow(7, 3)
	assert.True(t, ok, "a minute later")
}

func TestSettings_StateAndBounds(t *testing.T) {
	s := Defaults()
	assert.Equal(t, StateOff, s.State(false), "off until an administrator turns it on")
	s.Enabled = true
	assert.Equal(t, StateNotConfigured, s.State(false))
	s.Provider, s.Model = KindAnthropic, "claude-opus-5-5"
	assert.Equal(t, StateNotConfigured, s.State(false), "Anthropic needs a key")
	assert.Equal(t, StateOn, s.State(true))
	s.Provider, s.Model = KindOllama, "llama3.2"
	assert.Equal(t, StateOn, s.State(false), "Ollama needs none")
	s.Provider, s.BaseURL = KindOpenAI, "http://vllm.internal:8000/v1"
	assert.Equal(t, StateOn, s.State(false), "a compatible server of one's own may need none")
	s.BaseURL = ""
	assert.Equal(t, StateNotConfigured, s.State(false), "OpenAI's own address needs a key")

	require.NoError(t, Defaults().Validate())
	bad := Defaults()
	bad.MaxToolRounds = 0
	var inv *Invalid
	require.ErrorAs(t, bad.Validate(), &inv)
	assert.Equal(t, "max_tool_rounds", inv.Field)
	bad = Defaults()
	bad.BaseURL = "file:///etc/passwd"
	require.ErrorAs(t, bad.Validate(), &inv)
	assert.Equal(t, "base_url", inv.Field)
	bad = Defaults()
	bad.BaseURL = "https://user:pw@example.com"
	require.Error(t, bad.Validate(), "no credentials in the address")
	assert.Equal(t, []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5-1"}, SuggestedModels(KindAnthropic))
	assert.Nil(t, SuggestedModels(KindOpenAI), "an OpenAI-compatible server has what its operator put on it")
}

func TestArgs_BrokenJSONIsKeptAsText(t *testing.T) {
	a := ArgsFromText(`{"path":`)
	assert.True(t, json.Valid(a))
	assert.Equal(t, `{"path":`, ArgsText(a))
	assert.Nil(t, ArgsObject(a))
	assert.JSONEq(t, `{}`, string(ArgsFromText("  ")))
	assert.Equal(t, map[string]any{"x": 1.0}, ArgsObject(ArgsFromText(`{"x":1}`)))
}
