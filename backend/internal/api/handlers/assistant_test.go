package handlers_test

// The AI assistant in the explorer (task #269), end to end over real HTTP: a
// person's request, the model (a fake that speaks OpenAI's Chat Completions
// wire shape), filex's own MCP tools run as that person, and what is kept.
// The drivers' own wire shapes (Anthropic, Ollama) are held by
// internal/assistant's tests.
//
// RED PROOF: before this branch /api/files/assistant did not exist (404 on
// every route below), capabilities carried no `assistant` block, and the
// explorer's button was a disabled "coming soon".

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/assistant"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// llmTurn is one scripted reply: words, or one tool call.
type llmTurn struct {
	text string
	tool string
	args string
}

// llmFake is a model behind OpenAI's Chat Completions API: it answers each
// request with the next scripted turn ("done." when the script ran out) and
// keeps every request body.
type llmFake struct {
	srv    *httptest.Server
	mu     sync.Mutex
	script []llmTurn
	bodies []string
}

func newLLMFake(t *testing.T, turns ...llmTurn) *llmFake {
	t.Helper()
	l := &llmFake{script: turns}
	l.srv = httptest.NewServer(http.HandlerFunc(l.serve))
	t.Cleanup(l.srv.Close)
	return l
}

func (l *llmFake) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/models":
		_, _ = io.WriteString(w, `{"data":[{"id":"fake-model"}]}`)
		return
	case "/v1/chat/completions":
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	b, _ := io.ReadAll(r.Body)
	l.mu.Lock()
	n := len(l.bodies)
	l.bodies = append(l.bodies, string(b))
	turn := llmTurn{text: "done."}
	if len(l.script) > 0 {
		turn, l.script = l.script[0], l.script[1:]
	}
	l.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	chunk := func(v any) {
		raw, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
	}
	choice := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}}
	}
	if turn.tool != "" {
		chunk(choice(map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": fmt.Sprintf("call_%d", n), "type": "function",
			"function": map[string]any{"name": turn.tool, "arguments": turn.args},
		}}}, nil))
		chunk(choice(map[string]any{}, "tool_calls"))
	} else {
		chunk(choice(map[string]any{"content": turn.text}, nil))
		chunk(choice(map[string]any{}, "stop"))
	}
	chunk(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5}})
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

func (l *llmFake) requests() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.bodies...)
}

// assistantOn switches the assistant on, pointed at the fake.
func assistantOn(t *testing.T, store db.Store, l *llmFake, mutate func(*assistant.Settings)) {
	t.Helper()
	s := assistant.Defaults()
	s.Enabled, s.Provider, s.BaseURL, s.Model = true, assistant.KindOpenAI, l.srv.URL+"/v1", "fake-model"
	if mutate != nil {
		mutate(&s)
	}
	b, err := json.Marshal(s)
	require.NoError(t, err)
	require.NoError(t, store.UpsertSetting(context.Background(), handlers.AssistantSettingKey, string(b)))
}

// personKey is a key a PERSON made for themselves (kind user) - the desktop
// app's kind. testutil.NewAPIToken makes an app's key, which the assistant
// refuses: an embed's shared key has no person behind it.
func personKey(t *testing.T, store db.Store, userID int64, scopes string) string {
	t.Helper()
	var raw [24]byte
	_, err := rand.Read(raw[:])
	require.NoError(t, err)
	secret := "filex_" + hex.EncodeToString(raw[:])
	_, err = store.CreateAPIToken(context.Background(), &model.APIToken{
		UserID: userID, Label: "person", Kind: model.TokenKindUser,
		TokenHash: apitoken.HashToken(secret), Scopes: scopes,
	})
	require.NoError(t, err)
	return secret
}

type sseEvent struct {
	Name string
	Data map[string]any
}

// readSSE splits a finished event stream.
func readSSE(t *testing.T, raw string) []sseEvent {
	t.Helper()
	var out []sseEvent
	for _, block := range strings.Split(raw, "\n\n") {
		var ev sseEvent
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.Name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev.Data), line)
			}
		}
		if ev.Name != "" {
			out = append(out, ev)
		}
	}
	return out
}

func eventsNamed(evs []sseEvent, name string) []sseEvent {
	var out []sseEvent
	for _, e := range evs {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

// lastDone is the `done` event's conversation.
func lastDone(t *testing.T, evs []sseEvent) map[string]any {
	t.Helper()
	done := eventsNamed(evs, "done")
	require.NotEmpty(t, done, "no done event in %v", evs)
	conv, _ := done[len(done)-1].Data["conversation"].(map[string]any)
	require.NotNil(t, conv)
	return conv
}

func (f *e2eAIFixture) chat(t *testing.T, key string, body map[string]any) (int, []sseEvent, string) {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	code, raw := f.call(t, nil, key, http.MethodPost, "/api/files/assistant/chat", string(b))
	if code != http.StatusOK {
		return code, nil, raw
	}
	return code, readSSE(t, raw), raw
}

// steps are the step items of a conversation view, in order.
func steps(conv map[string]any) []map[string]any {
	var out []map[string]any
	items, _ := conv["items"].([]any)
	for _, it := range items {
		m := it.(map[string]any)
		if m["kind"] == "step" {
			out = append(out, m["step"].(map[string]any))
		}
	}
	return out
}

// Off, not set up, hidden, not permitted, an app's key: each its own answer,
// and the capability says whether the explorer draws the button.
func TestAssistant_WhoMayAndTheButton(t *testing.T) {
	f := newE2eAIFixture(t)
	llm := newLLMFake(t)
	key := personKey(t, f.store, f.memberID, "read,write,delete")
	ask := map[string]any{"message": "hi"}
	capab := func(k string) map[string]any {
		_, raw := f.call(t, nil, k, http.MethodGet, "/api/capabilities", "")
		var c map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &c))
		a, _ := c["assistant"].(map[string]any)
		require.NotNil(t, a, raw)
		return a
	}

	code, _, raw := f.chat(t, key, ask)
	assert.Equal(t, http.StatusNotFound, code)
	assert.Contains(t, raw, `"assistant_off"`, "off until an administrator turns it on")
	assert.Equal(t, "off", capab(key)["state"])
	assert.Equal(t, false, capab(key)["button"])

	assistantOn(t, f.store, llm, func(s *assistant.Settings) { s.Model = "" })
	code, _, raw = f.chat(t, key, ask)
	assert.Equal(t, http.StatusNotFound, code)
	assert.Contains(t, raw, `"assistant_not_configured"`)
	assert.Equal(t, "not_configured", capab(key)["state"])

	assistantOn(t, f.store, llm, func(s *assistant.Settings) { s.HideButton = true })
	code, _, raw = f.chat(t, key, ask)
	assert.Equal(t, http.StatusNotFound, code, "hidden is closed for everybody")
	assert.Contains(t, raw, `"assistant_off"`)
	assert.Equal(t, false, capab(key)["button"])

	assistantOn(t, f.store, llm, nil)
	assert.Equal(t, map[string]any{"state": "on", "permitted": true, "button": true}, capab(key))

	// A person whose role does not allow ai.assistant: no button, 403.
	require.NoError(t, f.store.SetUserPermissionOverrides(context.Background(), f.viewerID, map[string]string{"ai.assistant": model.PermDeny}, nil))
	viewerKey := personKey(t, f.store, f.viewerID, "read")
	assert.Equal(t, map[string]any{"state": "on", "permitted": false, "button": false}, capab(viewerKey))
	code, _, _ = f.chat(t, viewerKey, ask)
	assert.Equal(t, http.StatusForbidden, code)

	// An app's key (an embed's shared one): no person behind it.
	appKey := testutil.NewAPIToken(t, f.store, f.memberID, "read,write")
	assert.Equal(t, false, capab(appKey)["button"])
	code, _, _ = f.chat(t, appKey, ask)
	assert.Equal(t, http.StatusForbidden, code)
	assert.Empty(t, llm.requests(), "nothing of it reached the model")

	// A person's own history goes whatever the state.
	assistantOn(t, f.store, llm, func(s *assistant.Settings) { s.Enabled = false })
	code, _ = f.call(t, nil, key, http.MethodDelete, "/api/files/assistant/conversations", "")
	assert.Equal(t, http.StatusNoContent, code)
}

// The loop: the model asks for a tool, the tool runs as the person, its answer
// goes back, the model answers - streamed, kept, and drawn.
func TestAssistant_TheToolLoopRunsAsThePerson(t *testing.T) {
	f := newE2eAIFixture(t)
	llm := newLLMFake(t, llmTurn{tool: "file_list", args: `{"path":"depo://docs"}`}, llmTurn{text: "There are four files."})
	assistantOn(t, f.store, llm, nil)
	key := personKey(t, f.store, f.memberID, "read,write,delete")

	code, evs, raw := f.chat(t, key, map[string]any{"message": "What is in docs?"})
	require.Equal(t, http.StatusOK, code, raw)
	assert.NotEmpty(t, eventsNamed(evs, "conversation"))
	var text string
	for _, e := range eventsNamed(evs, "text") {
		text += e.Data["delta"].(string)
	}
	assert.Equal(t, "There are four files.", text)
	conv := lastDone(t, evs)
	st := steps(conv)
	require.Len(t, st, 1)
	assert.Equal(t, "file_list", st[0]["tool"])
	assert.Equal(t, "done", st[0]["status"])
	assert.Equal(t, "depo://docs", st[0]["target"])

	reqs := llm.requests()
	require.Len(t, reqs, 2)
	assert.Contains(t, reqs[1], "a.txt", "the tool's answer went back to the model")
	assert.Contains(t, reqs[0], `"file_list"`, "the tools are offered")
	assert.NotContains(t, reqs[0], `"file_upload_ticket"`, "not every tool is the assistant's")
	assert.NotContains(t, reqs[0], `"admin_`, "never an admin tool")

	// The conversation is kept, and read back the same.
	id := int64(conv["id"].(float64))
	code, raw = f.call(t, nil, key, http.MethodGet, fmt.Sprintf("/api/files/assistant/conversations/%d", id), "")
	require.Equal(t, http.StatusOK, code, raw)
	assert.Contains(t, raw, "There are four files.")
	// Somebody else's conversation is no conversation.
	otherKey := personKey(t, f.store, f.editorID, "read")
	code, _ = f.call(t, nil, otherKey, http.MethodGet, fmt.Sprintf("/api/files/assistant/conversations/%d", id), "")
	assert.Equal(t, http.StatusNotFound, code)
}

// A file the person may not read is not read for them: the tool refuses, the
// model is told so, and nothing of it leaves.
func TestAssistant_WhatThePersonCannotDoTheAssistantCannot(t *testing.T) {
	f := newE2eAIFixture(t)
	llm := newLLMFake(t, llmTurn{tool: "file_read", args: `{"path":"rbac://docs/gizli.txt"}`}, llmTurn{text: "I cannot."})
	assistantOn(t, f.store, llm, nil)
	key := personKey(t, f.store, f.memberID, "read,write,delete")

	code, evs, raw := f.chat(t, key, map[string]any{"message": "read the secret"})
	require.Equal(t, http.StatusOK, code, raw)
	st := steps(lastDone(t, evs))
	require.Len(t, st, 1)
	assert.Equal(t, "failed", st[0]["status"])
	reqs := llm.requests()
	require.Len(t, reqs, 2)
	assert.Contains(t, reqs[1], `"tool_call_id":"call_0"`)
	assert.NotContains(t, reqs[1], `"encoding":"utf-8"`, "no content went back")

	entries, err := f.store.ListAuditRecent(context.Background(), 50)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotEqual(t, handlers.AuditActionAssistantContentSent, e.Action, "nothing was sent, nothing is logged as sent")
	}
}

// ⚠⚠ Encrypted folders, encrypted files and the vault never reach a model:
// a call that names one is refused before the tool runs, a row inside one is
// taken out of an answer, and the person cannot hand one over as context.
func TestAssistant_EncryptedFoldersNeverReachTheModel(t *testing.T) {
	f := newE2eAIFixture(t)
	llm := newLLMFake(t,
		llmTurn{tool: "file_read", args: `{"path":"depo://kasa/rapor.pdf"}`},
		llmTurn{tool: "file_list", args: `{"path":"depo://kasa"}`},
		llmTurn{tool: "file_read", args: `{"path":"depo://docs/tek.fxe"}`},
		llmTurn{tool: "file_search", args: `{"path":"depo://","query":"rapor","content":false}`},
		llmTurn{text: "Closed."},
	)
	assistantOn(t, f.store, llm, nil)
	key := personKey(t, f.store, f.memberID, "read,write,delete")

	code, evs, raw := f.chat(t, key, map[string]any{"message": "open the vault"})
	require.Equal(t, http.StatusOK, code, raw)
	st := steps(lastDone(t, evs))
	require.Len(t, st, 4)
	for _, s := range st[:3] {
		assert.Equal(t, "failed", s["status"], s)
		assert.Contains(t, s["error"], "ENCRYPTED", s)
	}
	assert.Equal(t, "done", st[3]["status"], "a search runs; its rows inside the folder are taken out")
	for _, body := range llm.requests() {
		assert.NotContains(t, body, "ciphertext", "no byte of an encrypted file reached the model")
		assert.NotContains(t, body, "derin.txt", "no name inside an encrypted folder reached the model")
		assert.NotContains(t, body, `rapor.pdf\",\"name`, "the row inside the folder was taken out of the search")
	}

	// As the selection a message carries: refused before anything is sent.
	before := len(llm.requests())
	code, _, raw = f.chat(t, key, map[string]any{"message": "summarise", "paths": []string{"depo://kasa/rapor.pdf"}})
	assert.Equal(t, http.StatusForbidden, code)
	assert.Contains(t, raw, `"assistant_encrypted"`)
	assert.Len(t, llm.requests(), before)
}

// An action that cannot be undone waits for the person: nothing happens, the
// model is not asked again, until they answer. Approved, it runs; declined,
// the model is told.
func TestAssistant_APublicLinkWaitsForApproval(t *testing.T) {
	f := newE2eAIFixture(t)
	llm := newLLMFake(t,
		llmTurn{tool: "file_share", args: `{"path":"depo://docs/a.txt"}`},
		llmTurn{text: "Here is the link."},
		llmTurn{tool: "file_share", args: `{"path":"depo://docs/b.txt"}`},
		llmTurn{text: "Understood."},
	)
	assistantOn(t, f.store, llm, nil)
	key := personKey(t, f.store, f.memberID, "read,write,delete")

	code, evs, raw := f.chat(t, key, map[string]any{"message": "share a.txt"})
	require.Equal(t, http.StatusOK, code, raw)
	approvals := eventsNamed(evs, "approval")
	require.Len(t, approvals, 1)
	assert.Equal(t, "waiting", approvals[0].Data["status"])
	assert.NotEmpty(t, approvals[0].Data["question"], "the server says what will happen")
	conv := lastDone(t, evs)
	require.NotNil(t, conv["waiting"])
	assert.Equal(t, "pending", eventsNamed(evs, "done")[0].Data["reason"])
	assert.Len(t, llm.requests(), 1, "the model waits with the person")
	id := int64(conv["id"].(float64))

	decide := func(approve bool) []sseEvent {
		code, raw := f.call(t, nil, key, http.MethodPost, fmt.Sprintf("/api/files/assistant/conversations/%d/decide", id), fmt.Sprintf(`{"approve":%t}`, approve))
		require.Equal(t, http.StatusOK, code, raw)
		return readSSE(t, raw)
	}
	evs = decide(true)
	st := steps(lastDone(t, evs))
	require.NotEmpty(t, st)
	assert.Equal(t, "done", st[0]["status"])
	reqs := llm.requests()
	require.Len(t, reqs, 2)
	assert.Contains(t, reqs[1], "/s/", "the link went back to the model")

	// Nothing waits any more.
	code, _ = f.call(t, nil, key, http.MethodPost, fmt.Sprintf("/api/files/assistant/conversations/%d/decide", id), `{"approve":true}`)
	assert.Equal(t, http.StatusConflict, code)

	// Declined: not done, and the model hears it.
	b, _ := json.Marshal(map[string]any{"conversation_id": id, "message": "and b.txt"})
	code, raw = f.call(t, nil, key, http.MethodPost, "/api/files/assistant/chat", string(b))
	require.Equal(t, http.StatusOK, code, raw)
	evs = decide(false)
	st = steps(lastDone(t, evs))
	require.Len(t, st, 2)
	assert.Equal(t, "denied", st[1]["status"])
	reqs = llm.requests()
	assert.Contains(t, reqs[len(reqs)-1], "DENIED")
}

// Every file content that leaves for a model is in the audit log: who, which
// file, which provider and model.
func TestAssistant_FileContentSentIsAudited(t *testing.T) {
	f := newE2eAIFixture(t)
	llm := newLLMFake(t, llmTurn{tool: "file_read", args: `{"path":"depo://docs/a.txt"}`}, llmTurn{text: "It says hello."})
	assistantOn(t, f.store, llm, nil)
	key := personKey(t, f.store, f.memberID, "read,write,delete")

	code, _, raw := f.chat(t, key, map[string]any{"message": "read a.txt"})
	require.Equal(t, http.StatusOK, code, raw)
	assert.Contains(t, llm.requests()[1], "content of a.txt")

	entries, err := f.store.ListAuditRecent(context.Background(), 50)
	require.NoError(t, err)
	var row *model.AuditEntry
	for _, e := range entries {
		if e.Action == handlers.AuditActionAssistantContentSent {
			row = e
		}
	}
	require.NotNil(t, row, "no content_sent row")
	require.NotNil(t, row.UserID)
	assert.Equal(t, f.memberID, *row.UserID)
	assert.Equal(t, "depo://docs/a.txt", row.TargetID)
	assert.Equal(t, "openai", row.Metadata["ai_provider"])
	assert.Equal(t, "fake-model", row.Metadata["ai_model"])
	assert.Equal(t, "assistant", row.Metadata["via"])
}

// A change that can be undone happens at once, and the undo puts it back -
// as the person, through the same tools.
func TestAssistant_AMoveIsUndone(t *testing.T) {
	f := newE2eAIFixture(t)
	llm := newLLMFake(t, llmTurn{tool: "file_move", args: `{"src":"depo://docs/c.txt","dst":"depo://docs/moved.txt"}`}, llmTurn{text: "Moved."})
	assistantOn(t, f.store, llm, nil)
	key := personKey(t, f.store, f.memberID, "read,write,delete")

	code, evs, raw := f.chat(t, key, map[string]any{"message": "rename c.txt"})
	require.Equal(t, http.StatusOK, code, raw)
	assert.Empty(t, eventsNamed(evs, "approval"), "a move does not wait")
	conv := lastDone(t, evs)
	st := steps(conv)
	require.Len(t, st, 1)
	assert.Equal(t, true, st[0]["can_undo"])
	assert.True(t, f.exists("docs/moved.txt"))
	assert.False(t, f.exists("docs/c.txt"))

	id := int64(conv["id"].(float64))
	stepID := int(st[0]["id"].(float64))
	code, raw = f.call(t, nil, key, http.MethodPost, fmt.Sprintf("/api/files/assistant/conversations/%d/undo", id), fmt.Sprintf(`{"step_id":%d}`, stepID))
	require.Equal(t, http.StatusOK, code, raw)
	assert.True(t, f.exists("docs/c.txt"), "back where it was")
	assert.False(t, f.exists("docs/moved.txt"))
	assert.Contains(t, raw, `"status":"undone"`)

	// Once.
	code, _ = f.call(t, nil, key, http.MethodPost, fmt.Sprintf("/api/files/assistant/conversations/%d/undo", id), fmt.Sprintf(`{"step_id":%d}`, stepID))
	assert.Equal(t, http.StatusConflict, code)
}

// The person can ask to approve every change: a move waits then too.
func TestAssistant_ApproveEveryChangeIsThePersonsChoice(t *testing.T) {
	f := newE2eAIFixture(t)
	llm := newLLMFake(t, llmTurn{tool: "file_move", args: `{"src":"depo://docs/d.txt","dst":"depo://docs/d2.txt"}`})
	assistantOn(t, f.store, llm, nil)
	key := personKey(t, f.store, f.memberID, "read,write,delete")
	code, raw := f.call(t, nil, key, http.MethodPut, "/api/files/assistant/prefs", `{"confirm_all":true}`)
	require.Equal(t, http.StatusOK, code, raw)

	code, evs, raw := f.chat(t, key, map[string]any{"message": "rename d.txt"})
	require.Equal(t, http.StatusOK, code, raw)
	require.Len(t, eventsNamed(evs, "approval"), 1)
	assert.True(t, f.exists("docs/d.txt"), "nothing moved before the answer")
}

// ⚠⚠ The provider's API key goes one way: sealed when it arrives, never in an
// answer of the page, the generic settings list or the MCP tool, and a server
// without FILEX_SECRET_KEY refuses to store one.
func TestAssistant_TheKeyIsNeverAnswered(t *testing.T) {
	const secret = "sk-ant-very-secret-0123456789"
	srv, client, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.SecretKey = "assistant-test-secret-key" })
	email, pass := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pass)
	send := func(method, path, body string) (int, string) {
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	code, raw := send(http.MethodPatch, "/api/admin/assistant", `{"enabled":true,"provider":"anthropic","model":"claude-opus-5-5","api_key":"`+secret+`"}`)
	require.Equal(t, http.StatusOK, code, raw)
	assert.NotContains(t, raw, secret)
	assert.Contains(t, raw, `"has_key":true`)
	assert.Contains(t, raw, `"state":"on"`)

	for _, p := range []string{"/api/admin/assistant", "/api/admin/settings"} {
		code, raw = send(http.MethodGet, p, "")
		require.Equal(t, http.StatusOK, code, raw)
		assert.NotContains(t, raw, secret, p)
	}
	stored, err := store.GetSetting(context.Background(), handlers.AssistantSecretKey)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(stored, "enc:v1:"), "sealed at rest")
	assert.NotContains(t, stored, secret)

	// The audit row names the field, never its value.
	entries, err := store.ListAuditRecent(context.Background(), 20)
	require.NoError(t, err)
	b, _ := json.Marshal(entries)
	assert.NotContains(t, string(b), secret)

	// A test against an address typed but not saved does not get the key.
	l := newLLMFake(t)
	code, raw = send(http.MethodPost, "/api/admin/assistant/test", `{"provider":"openai","base_url":"`+l.srv.URL+`/v1","model":"fake-model"}`)
	require.Equal(t, http.StatusOK, code, raw)
	assert.Contains(t, raw, `"ok":true`)
	for _, body := range l.requests() {
		assert.NotContains(t, body, secret)
	}

	// Without a secret key nothing can be sealed: refused, not stored plain.
	srv2, client2, store2 := testutil.NewTestServer(t)
	email2, pass2 := testutil.SeedAdmin(t, store2)
	testutil.LoginAs(t, srv2, client2, email2, pass2)
	req, err := http.NewRequest(http.MethodPatch, srv2.URL+"/api/admin/assistant", strings.NewReader(`{"api_key":"`+secret+`"}`))
	require.NoError(t, err)
	resp, err := client2.Do(req)
	require.NoError(t, err)
	b, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Contains(t, string(b), "assistant_no_secret_key")
	stored2, _ := store2.GetSetting(context.Background(), handlers.AssistantSecretKey)
	assert.NotContains(t, stored2, secret)
}

// The admin MCP tools are the page's handler: the same validation, no key.
func TestAssistant_AdminMCPTools(t *testing.T) {
	f := newDoorFix(t)
	adm := testutil.NewAPIToken(t, f.Store, f.AdminID, "mcp,admin")
	tl, status, res := adminTool(t, f.URL, adm, "admin_ai_assistant_update", map[string]any{"body": map[string]any{"enabled": true, "provider": "ollama", "model": "llama3.2"}})
	require.False(t, tl.IsError, tl.Text)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "on", res["state"], "Ollama needs no key")
	tl, status, res = adminTool(t, f.URL, adm, "admin_ai_assistant_get", map[string]any{})
	require.False(t, tl.IsError, tl.Text)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "ollama", res["provider"])
	assert.Equal(t, false, res["has_key"])
	tl, status, _ = adminTool(t, f.URL, adm, "admin_ai_assistant_update", map[string]any{"body": map[string]any{"max_tool_rounds": 0}})
	assert.True(t, tl.IsError)
	assert.Equal(t, http.StatusBadRequest, status)
}

// The tenant boundary holds: a tenant's person's assistant cannot reach
// another tenant's storage, and nothing of it reaches the model.
func TestAssistant_TheTenantBoundaryHolds(t *testing.T) {
	f := newMTFix(t, true)
	f.seedFile(t, f.StB, f.RootB, "plans/bravo-only.txt", "BRAVO CONFIDENTIAL")
	llm := newLLMFake(t,
		llmTurn{tool: "file_read", args: `{"path":"bravo://plans/bravo-only.txt"}`},
		llmTurn{tool: "file_search", args: `{"path":"bravo://","query":"bravo","content":false}`},
		llmTurn{text: "Not mine."},
	)
	assistantOn(t, f.Store, llm, nil)

	req, err := http.NewRequest(http.MethodPost, f.URL+"/api/files/assistant/chat", strings.NewReader(`{"message":"read bravo's plans"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.A.Do(req)
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, string(b))
	st := steps(lastDone(t, readSSE(t, string(b))))
	require.Len(t, st, 2)
	assert.Equal(t, "failed", st[0]["status"])
	for _, body := range llm.requests() {
		assert.NotContains(t, body, "BRAVO CONFIDENTIAL")
		assert.NotContains(t, body, "bravo-only.txt\\\",\\\"name", "no row of the other tenant")
	}
}
