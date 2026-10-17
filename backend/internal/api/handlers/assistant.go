package handlers

// The AI assistant in the explorer (task #269): a person's conversations with
// a model the administrator chose, and the routes the explorer's panel calls
// (mounted at /api/files/assistant, so an embed's proxy that forwards
// /api/files/* reaches them as it reaches every other explorer door).
//
//	GET    /api/files/assistant                         the assistant as it stands for this person
//	PUT    /api/files/assistant/prefs                   the person's own choice (approve every change)
//	GET    /api/files/assistant/conversations           their conversations
//	DELETE /api/files/assistant/conversations           all of them, gone
//	GET    /api/files/assistant/conversations/{id}      one, as the panel draws it
//	DELETE /api/files/assistant/conversations/{id}      one, gone
//	POST   /api/files/assistant/chat                    a message (server-sent events)
//	POST   /api/files/assistant/conversations/{id}/decide  the answer to an approval (server-sent events)
//	POST   /api/files/assistant/conversations/{id}/undo    undo one step
//
// Who may: a signed-in person - a session or their own key, never an app's
// key (an embed's shared key has no person behind it) - holding ai.assistant,
// while the administrator has it on, set up and not hidden. Off, the routes
// that talk to a model answer 404 (`assistant_off`, `assistant_not_configured`);
// without the permission 403. A person's own history can be deleted, and
// their choice changed, whatever the state.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/assistant"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/clientip"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// Assistant serves the explorer's AI assistant.
type Assistant struct {
	Store   db.Store
	Config  *AssistantConfig
	MCP     *AIMCP
	ACL     *acl.Resolver
	Limiter *assistant.Limiter
	// Now is the clock (time.Now when nil).
	Now func() time.Time

	locks     sync.Map
	lastSweep atomic.Int64
}

// NewAssistant builds the handler.
func NewAssistant(store db.Store, cfg *AssistantConfig, m *AIMCP, resolver *acl.Resolver) *Assistant {
	return &Assistant{Store: store, Config: cfg, MCP: m, ACL: resolver, Limiter: &assistant.Limiter{}}
}

func (h *Assistant) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// ── who may ──────────────────────────────────────────────────────────────────

// personState is the assistant's state as a person is told it: hidden by
// the administrator reads as off - there is nothing of it for them.
func personState(l assistantLoaded) string {
	if l.Settings.HideButton {
		return assistant.StateOff
	}
	return l.State()
}

// permitted reports whether the caller may use the assistant: a person (not
// an app's key) holding ai.assistant.
func (h *Assistant) permitted(ctx context.Context) bool {
	if auth.UserFrom(ctx) == nil || auth.TokenFrom(ctx).IsApp() {
		return false
	}
	return h.ACL == nil || callerCan(ctx, h.ACL, perm.AIAssistant)
}

// Capability is the `assistant` block of GET /api/capabilities for this
// caller: its state, and whether the explorer draws the button - on, set up,
// not hidden, and the caller holds ai.assistant.
func (h *Assistant) Capability(ctx context.Context) map[string]any {
	l := h.Config.Load(ctx)
	state := personState(l)
	ok := h.permitted(ctx)
	return map[string]any{"state": state, "permitted": ok, "button": state == assistant.StateOn && ok}
}

// gate answers the caller's refusal and reports whether the request may go
// on. needOn: the route talks to a model, so the assistant must be on and
// the caller hold ai.assistant.
func (h *Assistant) gate(w http.ResponseWriter, r *http.Request, needOn bool) (assistantLoaded, *model.User, bool) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return assistantLoaded{}, nil, false
	}
	if deniedToApp(w, r) {
		return assistantLoaded{}, nil, false
	}
	l := h.Config.Load(r.Context())
	if !needOn {
		return l, u, true
	}
	switch state := personState(l); state {
	case assistant.StateOn:
	case assistant.StateNotConfigured:
		writeError(w, r, http.StatusNotFound, "assistant_not_configured", nil)
		return l, u, false
	default:
		writeError(w, r, http.StatusNotFound, "assistant_off", nil)
		return l, u, false
	}
	if h.ACL != nil && !requirePerm(w, r, h.ACL, perm.AIAssistant) {
		return l, u, false
	}
	return l, u, true
}

// ── the person's own ─────────────────────────────────────────────────────────

// SurfaceAssistant is the user_prefs row (00047) that holds a person's
// assistant choices: one per account, like the language - never per browser.
// openwith.go reads only the web and desktop rows, so this one is nobody
// else's.
const SurfaceAssistant = "assistant"

type assistantPrefs struct {
	// ConfirmAll: every change waits for the person's approval, not only
	// the ones that cannot be undone.
	ConfirmAll bool `json:"confirm_all"`
}

func (h *Assistant) prefs(ctx context.Context, userID int64) assistantPrefs {
	var p assistantPrefs
	if raw, err := h.Store.GetUserPrefs(ctx, userID, SurfaceAssistant); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &p)
	}
	return p
}

// Me is the assistant as it stands for the caller.
func (h *Assistant) Me(w http.ResponseWriter, r *http.Request) {
	l, u, ok := h.gate(w, r, false)
	if !ok {
		return
	}
	h.maybeSweep(r.Context())
	capab := h.Capability(r.Context())
	out := map[string]any{
		"state":          capab["state"],
		"permitted":      capab["permitted"],
		"button":         capab["button"],
		"retention_days": l.Settings.RetentionDays,
		"prefs":          h.prefs(r.Context(), u.ID),
	}
	if capab["state"] == assistant.StateOn {
		// Who reads what the person writes: said to them, not hidden.
		out["provider"], out["model"] = l.Settings.Provider, l.Settings.Model
	}
	writeJSON(w, http.StatusOK, out)
}

// SetPrefs changes the person's choice.
func (h *Assistant) SetPrefs(w http.ResponseWriter, r *http.Request) {
	_, u, ok := h.gate(w, r, false)
	if !ok {
		return
	}
	var p assistantPrefs
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_json", nil)
		return
	}
	b, _ := json.Marshal(p)
	if err := h.Store.SetUserPrefs(r.Context(), u.ID, SurfaceAssistant, string(b)); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// List is the caller's conversations, the latest first.
func (h *Assistant) List(w http.ResponseWriter, r *http.Request) {
	_, u, ok := h.gate(w, r, true)
	if !ok {
		return
	}
	h.maybeSweep(r.Context())
	rows, err := h.Store.ListAssistantConversations(r.Context(), u.ID, 100)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
		return
	}
	if rows == nil {
		rows = []*model.AssistantConversation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": rows})
}

// DeleteAll removes every conversation of the caller.
func (h *Assistant) DeleteAll(w http.ResponseWriter, r *http.Request) {
	_, u, ok := h.gate(w, r, false)
	if !ok {
		return
	}
	if err := h.Store.DeleteAssistantConversations(r.Context(), u.ID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func convID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id
}

// Delete removes one conversation of the caller.
func (h *Assistant) Delete(w http.ResponseWriter, r *http.Request) {
	_, u, ok := h.gate(w, r, false)
	if !ok {
		return
	}
	found, err := h.Store.DeleteAssistantConversation(r.Context(), u.ID, convID(r))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
		return
	}
	if !found {
		writeError(w, r, http.StatusNotFound, "assistant_not_found", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// load reads one conversation of u: nil when there is none of theirs (the
// refusal is written).
func (h *Assistant) load(w http.ResponseWriter, r *http.Request, u *model.User, id int64) (*model.AssistantConversation, *assistantConv) {
	row, err := h.Store.GetAssistantConversation(r.Context(), u.ID, id)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
		return nil, nil
	}
	if row == nil {
		writeError(w, r, http.StatusNotFound, "assistant_not_found", nil)
		return nil, nil
	}
	conv, err := parseAssistantConv(row.State)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
		return nil, nil
	}
	return row, conv
}

// Get is one conversation, as the panel draws it.
func (h *Assistant) Get(w http.ResponseWriter, r *http.Request) {
	_, u, ok := h.gate(w, r, true)
	if !ok {
		return
	}
	row, conv := h.load(w, r, u, convID(r))
	if row == nil {
		return
	}
	writeJSON(w, http.StatusOK, conv.view(row, requestLang(r)))
}

// ── retention ────────────────────────────────────────────────────────────────

// Sweep deletes every conversation last used before the retention the
// administrator set - whether the assistant is on or not.
func (h *Assistant) Sweep(ctx context.Context) {
	l := h.Config.Load(ctx)
	days := l.Settings.RetentionDays
	if days <= 0 {
		return
	}
	cutoff := h.now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	if err := h.Store.DeleteAssistantConversationsBefore(ctx, cutoff); err != nil {
		slog.Warn("assistant: retention sweep failed", slog.String("err", err.Error()))
	}
	h.lastSweep.Store(h.now().UnixMilli())
}

// maybeSweep sweeps when the last sweep is an hour old: a server that was
// down when the hour struck still keeps the retention.
func (h *Assistant) maybeSweep(ctx context.Context) {
	if h.now().UnixMilli()-h.lastSweep.Load() < assistantSweepEvery.Milliseconds() {
		return
	}
	h.Sweep(ctx)
}

// StartRetention sweeps now and every hour until ctx ends.
func (h *Assistant) StartRetention(ctx context.Context) {
	if h == nil {
		return
	}
	go func() {
		t := time.NewTicker(assistantSweepEvery)
		defer t.Stop()
		h.Sweep(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h.Sweep(ctx)
			}
		}
	}()
}

// ── a request to the model ───────────────────────────────────────────────────

// assistantSSE writes server-sent events to the panel.
type assistantSSE struct {
	w  http.ResponseWriter
	rc *http.ResponseController
	mu sync.Mutex
}

func newAssistantSSE(w http.ResponseWriter) *assistantSSE {
	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream; charset=utf-8")
	hd.Set("Cache-Control", "no-cache, no-transform")
	// A proxy that buffers (nginx) would hold the reply back to its end.
	hd.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	s := &assistantSSE{w: w, rc: http.NewResponseController(w)}
	_ = s.rc.Flush()
	return s
}

func (s *assistantSSE) send(event string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, b)
	_ = s.rc.Flush()
}

// lock serialises the requests of one conversation in this process: two
// tabs answering the same approval are taken one after the other. Across
// processes the row's revision decides (compare-and-set).
func (h *Assistant) lock(id int64) func() {
	v, _ := h.locks.LoadOrStore(id, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// allow answers the rate limit's refusal and reports whether the request
// may go on.
func (h *Assistant) allow(w http.ResponseWriter, r *http.Request, u *model.User, l assistantLoaded) bool {
	if ok, _ := h.Limiter.Allow(u.ID, l.Settings.RequestsPerMinute); ok {
		return true
	}
	writeError(w, r, http.StatusTooManyRequests, "assistant_busy", nil)
	return false
}

// tools opens the toolbox for one request, as the person on r. The
// conversation is the caller's to attach (attach), once it has one.
func (h *Assistant) tools(r *http.Request, u *model.User, l assistantLoaded, lang string) (*assistantToolbox, context.Context, func(), error) {
	call := &assistantCallInfo{provider: string(l.Settings.Provider), model: l.Settings.Model}
	ctx := withAssistantCall(r.Context(), call)
	ctx = clientip.WithIP(ctx, clientip.FromRequest(r))
	ctx = withDoorOrigin(ctx, r)
	tb, closer, err := openAssistantTools(ctx, h.MCP, h.Store)
	if err != nil {
		return nil, ctx, closer, err
	}
	tb.lang, tb.call, tb.user = lang, call, u
	tb.confirmAll = h.prefs(ctx, u.ID).ConfirmAll
	tb.now = h.now
	return tb, ctx, closer, nil
}

// attach gives the toolbox the conversation its steps are kept in.
func (tb *assistantToolbox) attach(row *model.AssistantConversation, conv *assistantConv) {
	tb.conv = conv
	tb.call.conversation = row.ID
}

// assistantChatReq is a message from the panel. Paths are what the person
// had selected (confine.Middleware holds them to the root like every
// `paths`).
type assistantChatReq struct {
	ConversationID int64    `json:"conversation_id,omitempty"`
	Message        string   `json:"message"`
	Paths          []string `json:"paths,omitempty"`
}

const (
	assistantMaxMessage = 32000
	assistantMaxPaths   = 50
)

// Chat takes a message and answers with the model's reply as server-sent
// events: `conversation` {id, title}, then `text` {delta} as the reply
// streams, `step` and `approval` (a step as the panel draws it), and at the
// end `done` {reason, conversation} - or `error` {error, message, detail}.
func (h *Assistant) Chat(w http.ResponseWriter, r *http.Request) {
	l, u, ok := h.gate(w, r, true)
	if !ok {
		return
	}
	var req assistantChatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_json", nil)
		return
	}
	text := strings.TrimSpace(req.Message)
	switch {
	case text == "":
		writeError(w, r, http.StatusBadRequest, "assistant_empty", nil)
		return
	case utf8.RuneCountInString(text) > assistantMaxMessage:
		writeError(w, r, http.StatusRequestEntityTooLarge, "assistant_message_long", apierr.Params{"max": strconv.Itoa(assistantMaxMessage)})
		return
	case len(req.Paths) > assistantMaxPaths:
		writeError(w, r, http.StatusBadRequest, "assistant_too_many_paths", apierr.Params{"max": strconv.Itoa(assistantMaxPaths)})
		return
	}
	if !h.allow(w, r, u, l) {
		return
	}
	ctx := r.Context()
	lang := requestLang(r)
	h.maybeSweep(ctx)

	tb, tctx, closer, err := h.tools(r, u, l, lang)
	defer closer()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
		return
	}
	// The selection is held like a call's paths - to the root, and out of
	// encrypted folders - before anything is kept or sent.
	paths := make([]any, 0, len(req.Paths))
	for _, p := range req.Paths {
		paths = append(paths, p)
	}
	selection := map[string]any{"paths": paths}
	if refusal := tb.guard(tctx, selection); refusal != "" {
		code := "assistant_out_of_root"
		if strings.HasPrefix(refusal, "ENCRYPTED") {
			code = "assistant_encrypted"
		}
		writeError(w, r, http.StatusForbidden, code, nil)
		return
	}

	var row *model.AssistantConversation
	var conv *assistantConv
	if req.ConversationID > 0 {
		unlock := h.lock(req.ConversationID)
		defer unlock()
		if row, conv = h.load(w, r, u, req.ConversationID); row == nil {
			return
		}
		if len(row.State) > assistantMaxState {
			writeError(w, r, http.StatusRequestEntityTooLarge, "assistant_too_long", nil)
			return
		}
	} else {
		created, err := h.create(ctx, u, l, lang, text)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
			return
		}
		unlock := h.lock(created.ID)
		defer unlock()
		row, conv = created, &assistantConv{V: assistantStateVersion}
		_ = json.Unmarshal([]byte(created.State), conv)
	}
	tb.attach(row, conv)

	// A batch that waited for an answer the person never gave: they moved
	// on, and every API refuses a call left without an answer.
	if conv.Pending != nil {
		h.closeBatch(conv, conv.Pending, "moved_on")
		conv.Pending = nil
	}
	var parts []assistant.Part
	if undone := h.undoneNote(conv); undone != "" {
		parts = append(parts, assistant.Part{Kind: assistant.PartText, Text: undone, Context: true, Args: json.RawMessage("[]")})
	}
	var selected []string
	for _, p := range selection["paths"].([]any) {
		if s, ok := p.(string); ok && s != "" {
			selected = append(selected, s)
		}
	}
	if len(selected) > 0 {
		parts = append(parts, assistantContextPart(selected))
	}
	parts = append(parts, assistant.Part{Kind: assistant.PartText, Text: text})
	conv.Messages = append(conv.Messages, assistant.Message{Role: assistant.RoleUser, Parts: parts})

	sse := newAssistantSSE(w)
	tb.emit = sse.send
	sse.send("conversation", map[string]any{"id": row.ID, "title": row.Title})
	h.run(tctx, sse, tb, l, row, conv, nil, lang)
}

// create writes a new conversation with its frozen system prompt.
func (h *Assistant) create(ctx context.Context, u *model.User, l assistantLoaded, lang, first string) (*model.AssistantConversation, error) {
	conv := &assistantConv{
		V:        assistantStateVersion,
		System:   assistantSystemPrompt(u, lang, h.now()),
		Provider: string(l.Settings.Provider),
		Model:    l.Settings.Model,
	}
	state, err := conv.encode()
	if err != nil {
		return nil, err
	}
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	now := h.now().UnixMilli()
	return h.Store.CreateAssistantConversation(ctx, &model.AssistantConversation{
		UserID: u.ID, Key: hex.EncodeToString(key), Title: assistantTitle(first), State: state,
		CreatedMs: now, UpdatedMs: now,
	})
}

// assistantDecideReq is the person's answer to the step that waits.
type assistantDecideReq struct {
	Approve bool `json:"approve"`
}

// Decide takes the person's answer to the step that waits, runs it (or
// tells the model it was declined) and goes on, as server-sent events like
// Chat's.
func (h *Assistant) Decide(w http.ResponseWriter, r *http.Request) {
	l, u, ok := h.gate(w, r, true)
	if !ok {
		return
	}
	var req assistantDecideReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_json", nil)
		return
	}
	if !h.allow(w, r, u, l) {
		return
	}
	id := convID(r)
	unlock := h.lock(id)
	defer unlock()
	row, conv := h.load(w, r, u, id)
	if row == nil {
		return
	}
	waiting := conv.Pending.Waiting()
	if waiting == nil {
		writeError(w, r, http.StatusConflict, "assistant_nothing_waiting", nil)
		return
	}
	waiting.Decision = assistant.DecisionDeny
	if req.Approve {
		waiting.Decision = assistant.DecisionApprove
	}
	lang := requestLang(r)
	tb, tctx, closer, err := h.tools(r, u, l, lang)
	defer closer()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
		return
	}
	tb.attach(row, conv)
	batch := conv.Pending
	conv.Pending = nil
	sse := newAssistantSSE(w)
	tb.emit = sse.send
	sse.send("conversation", map[string]any{"id": row.ID, "title": row.Title})
	h.run(tctx, sse, tb, l, row, conv, batch, lang)
}

// run asks the model, runs its tools, keeps what happened and says how it
// ended.
func (h *Assistant) run(ctx context.Context, sse *assistantSSE, tb *assistantToolbox, l assistantLoaded, row *model.AssistantConversation, conv *assistantConv, resume *assistant.Batch, lang string) {
	conv.Provider, conv.Model = string(l.Settings.Provider), l.Settings.Model
	var res assistant.Result
	p, err := h.Config.provider(l)
	if err == nil {
		loop := &assistant.Loop{
			Provider:        p,
			Model:           l.Settings.Model,
			System:          conv.System,
			Tools:           tb,
			MaxRounds:       l.Settings.MaxToolRounds,
			MaxOutputTokens: l.Settings.MaxOutputTokens,
			Budget:          l.Settings.MaxRequestTokens,
			OnText:          func(t string) { sse.send("text", map[string]string{"delta": t}) },
			OnMessage:       func(m assistant.Message) { conv.Messages = append(conv.Messages, m) },
		}
		// When a batch resumes, the model's turn that made it is in the
		// history already; the run goes on from its answers.
		history := append([]assistant.Message(nil), conv.Messages...)
		res, err = loop.Run(ctx, history, resume)
	}
	conv.Usage = conv.Usage.Add(res.Usage)
	if res.Model != "" {
		conv.Model = res.Model
	}
	conv.Pending = res.Pending
	// A run cut short (the person stopped it, the provider failed) can leave
	// the model's last calls without answers: they get one, or the next
	// request would be refused by the provider.
	h.closeDangling(conv)
	reason := res.Reason
	switch {
	case err != nil && errors.Is(err, context.Canceled):
		reason = "stopped"
		conv.note("stopped", nil)
	case err != nil:
		reason = "error"
		msg, detail := assistantProviderWords(lang, err)
		conv.note("provider_failed", map[string]string{"detail": firstLine(detail)})
		sse.send("error", map[string]any{"error": "assistant_provider", "message": msg, "detail": detail})
	case reason == assistant.ReasonRefusal:
		conv.note("refusal", map[string]string{"category": res.RefusalCategory})
	case reason != assistant.ReasonEnd && reason != assistant.ReasonPending:
		conv.note(reason, nil)
	}
	saved := h.save(context.WithoutCancel(ctx), row, conv)
	if !saved {
		sse.send("error", apierr.Map(lang, "assistant_conflict", nil, nil))
	}
	sse.send("done", map[string]any{"reason": reason, "conversation": conv.view(row, lang)})
}

// closeBatch answers every call of b that has none, after the person moved
// on or stopped: a call that ran says it ran, the rest that they did not.
func (h *Assistant) closeBatch(conv *assistantConv, b *assistant.Batch, why string) {
	notRun := "NOT_RUN: the person stopped the request before this call ran, so nothing was done."
	if why == "moved_on" {
		notRun = "NOT_RUN: the person sent a new message instead of answering this request, so nothing was done."
	}
	for i := len(b.Results); i < len(b.Calls); i++ {
		c := b.Calls[i]
		text := notRun
		if s := conv.step(c.ID); s != nil {
			switch s.Status {
			case stepDone:
				text = "DONE: this call ran before the request was stopped; its answer was not kept."
			case stepWaiting, stepRunning:
				s.Status = stepNotRun
			}
		}
		b.Results = append(b.Results, assistant.Part{Kind: assistant.PartToolResult, CallID: c.ID, Tool: c.Name, Text: text, IsError: true})
	}
	conv.Messages = append(conv.Messages, assistant.Message{Role: assistant.RoleUser, Parts: b.Results})
}

// closeDangling answers the calls of the conversation's last message when it
// is the model's and nothing answered them (and no batch waits on them).
func (h *Assistant) closeDangling(conv *assistantConv) {
	if conv.Pending != nil || len(conv.Messages) == 0 {
		return
	}
	last := conv.Messages[len(conv.Messages)-1]
	if last.Role != assistant.RoleAssistant {
		return
	}
	calls := last.ToolCalls()
	if len(calls) == 0 {
		return
	}
	h.closeBatch(conv, &assistant.Batch{Calls: calls}, "stopped")
}

// undoneNote tells the model which of its steps the person undid since it
// last heard from them, and forgets them.
func (h *Assistant) undoneNote(conv *assistantConv) string {
	if len(conv.Undone) == 0 {
		return ""
	}
	var lines []string
	for _, id := range conv.Undone {
		if s := conv.stepByID(id); s != nil {
			line := "- " + s.Tool + " " + s.Target
			if s.To != "" {
				line += " -> " + s.To
			}
			lines = append(lines, line)
		}
	}
	conv.Undone = nil
	if len(lines) == 0 {
		return ""
	}
	return "Since your last answer the person undid these steps of yours; things are back where they were:\n" + strings.Join(lines, "\n")
}

// save writes the conversation if nobody else wrote it meanwhile.
func (h *Assistant) save(ctx context.Context, row *model.AssistantConversation, conv *assistantConv) bool {
	state, err := conv.encode()
	if err != nil {
		slog.Warn("assistant: conversation does not encode", slog.String("err", err.Error()))
		return false
	}
	row.State, row.UpdatedMs = state, h.now().UnixMilli()
	ok, err := h.Store.UpdateAssistantConversation(ctx, row, row.Rev)
	if err != nil {
		slog.Warn("assistant: conversation not saved", slog.String("err", err.Error()))
		return false
	}
	return ok
}

// assistantUndoReq names the step to undo.
type assistantUndoReq struct {
	StepID int `json:"step_id"`
}

// Undo reverses one step, as the person: the undo the step was given when
// it ran, through the same tools.
func (h *Assistant) Undo(w http.ResponseWriter, r *http.Request) {
	l, u, ok := h.gate(w, r, true)
	if !ok {
		return
	}
	var req assistantUndoReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_json", nil)
		return
	}
	id := convID(r)
	unlock := h.lock(id)
	defer unlock()
	row, conv := h.load(w, r, u, id)
	if row == nil {
		return
	}
	s := conv.stepByID(req.StepID)
	if s == nil || s.Status != stepDone || len(s.Undo) == 0 {
		writeError(w, r, http.StatusConflict, "assistant_no_undo", nil)
		return
	}
	lang := requestLang(r)
	tb, tctx, closer, err := h.tools(r, u, l, lang)
	defer closer()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
		return
	}
	tb.attach(row, conv)
	if err := tb.runUndo(tctx, s); err != nil {
		writeError(w, r, http.StatusConflict, "assistant_undo_failed", apierr.Params{"detail": firstLine(err.Error())})
		return
	}
	s.Status = stepUndone
	conv.Undone = append(conv.Undone, s.ID)
	if !h.save(context.WithoutCancel(r.Context()), row, conv) {
		writeError(w, r, http.StatusConflict, "assistant_conflict", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"step": s.view(lang), "conversation": conv.view(row, lang)})
}
