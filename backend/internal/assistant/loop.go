package assistant

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Decision is the person's answer to a call that waited for it.
type Decision string

const (
	// DecisionNone: not asked, or not answered yet.
	DecisionNone Decision = ""
	// DecisionApprove: go ahead.
	DecisionApprove Decision = "approve"
	// DecisionDeny: do not.
	DecisionDeny Decision = "deny"
)

// ToolCall is one call the loop hands the toolbox.
type ToolCall struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Args     json.RawMessage `json:"args"`
	Decision Decision        `json:"decision,omitempty"`
}

// ToolOutcome is what the toolbox made of a call.
type ToolOutcome struct {
	// Text is what the model is told: the tool's answer, or why it did not
	// run.
	Text    string
	IsError bool
	// Pending: the call waits for the person (it cannot be undone and they
	// have not answered). The loop stops here; Text is not used.
	Pending bool
}

// Toolbox runs the tools a model calls.
type Toolbox interface {
	// Defs are the tools the model is offered - the same list, in the same
	// order, for every call of a conversation (a provider ties what it
	// cached, and Anthropic its thinking blocks, to the tools).
	Defs() []ToolDef
	// Run runs one call (or decides it must wait for the person).
	Run(ctx context.Context, call ToolCall) ToolOutcome
}

// Batch is the tool calls of one model turn, as far as they were answered:
// what a conversation keeps while a call waits for the person.
type Batch struct {
	Calls []ToolCall `json:"calls"`
	// Results are the answers so far, in the calls' order: Results[i]
	// answers Calls[i]; the first call without one is the one waiting.
	Results []Part `json:"results"`
}

// Waiting is the call the batch waits on; nil when every call is answered.
func (b *Batch) Waiting() *ToolCall {
	if b == nil || len(b.Results) >= len(b.Calls) {
		return nil
	}
	return &b.Calls[len(b.Results)]
}

// Why a run ended, as Result.Reason says it.
const (
	ReasonEnd       = "end"
	ReasonPending   = "pending"
	ReasonRounds    = "rounds"
	ReasonBudget    = "budget"
	ReasonMaxTokens = "max_tokens"
	ReasonRefusal   = "refusal"
	ReasonOther     = "other"
)

// Result is what one run of the loop did.
type Result struct {
	// Added are the messages the run appended to the conversation: the
	// model's turns and the tool answers, in order.
	Added []Message
	// Pending is the batch that waits for the person (Reason "pending").
	Pending *Batch
	Reason  string
	Usage   Usage
	// Model is the model that answered last.
	Model string
	// RefusalCategory is the provider's reason for a refusal, when it gave
	// one.
	RefusalCategory string
}

// Loop runs a conversation with a model and its tools, within a budget.
type Loop struct {
	Provider Provider
	Model    string
	System   string
	Tools    Toolbox
	// MaxRounds is how many batches of tool calls one run may answer and go
	// back to the model with.
	MaxRounds int
	// MaxOutputTokens is one reply's limit; Budget the whole run's, input and
	// output of every call together.
	MaxOutputTokens int
	Budget          int
	// OnText receives the reply's text as it streams (nil: dropped).
	OnText func(string)
	// OnMessage receives every message the run appends, as it appends it -
	// before the toolbox runs that message's calls (nil: none).
	OnMessage func(Message)
}

// What the model is told about a call that was not run, by reason.
const (
	notRunCutOff = "NOT_RUN: your reply reached its length limit before this call was complete, so nothing was done. Ask for less at a time."
	notRunRounds = "NOT_RUN: this request reached the most tool rounds the administrator allows, so nothing was done. Tell the person what is left; they can ask you to go on."
	notRunBudget = "NOT_RUN: this request used up its token budget, so nothing was done. Tell the person what is left; they can ask you to go on."
)

// answerAll answers every call of calls that has no answer yet with text.
func answerAll(b *Batch, text string) Message {
	for i := len(b.Results); i < len(b.Calls); i++ {
		c := b.Calls[i]
		b.Results = append(b.Results, Part{Kind: PartToolResult, CallID: c.ID, Tool: c.Name, Text: text, IsError: true})
	}
	return Message{Role: RoleUser, Parts: b.Results}
}

// Run goes on with the conversation history: answers resume's calls first
// when a batch was waiting (the waiting call carries the person's Decision),
// then asks the model until it answers, waits for the person, or reaches a
// limit. A provider failure ends the run with the error and what was done
// before it.
func (l *Loop) Run(ctx context.Context, history []Message, resume *Batch) (Result, error) {
	msgs := append([]Message(nil), history...)
	res := Result{Model: l.Model}
	add := func(m Message) {
		msgs = append(msgs, m)
		res.Added = append(res.Added, m)
		if l.OnMessage != nil {
			l.OnMessage(m)
		}
	}
	onText := l.OnText
	if onText == nil {
		onText = func(string) {}
	}
	rounds := 0
	batch := resume
	for {
		if batch != nil {
			if rounds >= l.MaxRounds {
				add(answerAll(batch, notRunRounds))
				res.Reason = ReasonRounds
				return res, nil
			}
			for i := len(batch.Results); i < len(batch.Calls); i++ {
				if err := ctx.Err(); err != nil {
					return res, err
				}
				call := batch.Calls[i]
				out := l.Tools.Run(ctx, call)
				if out.Pending {
					res.Pending, res.Reason = batch, ReasonPending
					return res, nil
				}
				batch.Results = append(batch.Results, Part{Kind: PartToolResult, CallID: call.ID, Tool: call.Name, Text: out.Text, IsError: out.IsError})
			}
			add(Message{Role: RoleUser, Parts: batch.Results})
			batch = nil
			rounds++
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
		turn, err := l.Provider.Chat(ctx, Request{
			Model:     l.Model,
			System:    l.System,
			Messages:  msgs,
			Tools:     l.Tools.Defs(),
			MaxTokens: l.MaxOutputTokens,
		}, onText)
		if err != nil {
			return res, err
		}
		res.Usage = res.Usage.Add(turn.Usage)
		if turn.Model != "" {
			res.Model = turn.Model
		}
		if turn.Stop == StopRefusal {
			// A refusal - before any output or part-way - is not kept: the
			// partial reply is not an answer.
			res.Reason, res.RefusalCategory = ReasonRefusal, turn.RefusalCategory
			return res, nil
		}
		reply := Message{Role: RoleAssistant, Parts: turn.Parts}
		calls := reply.ToolCalls()
		if len(turn.Parts) > 0 {
			add(reply)
		}
		if len(calls) == 0 {
			switch turn.Stop {
			case StopMaxTokens:
				res.Reason = ReasonMaxTokens
			case StopEnd, StopToolUse:
				res.Reason = ReasonEnd
			default:
				res.Reason = ReasonOther
			}
			return res, nil
		}
		next := &Batch{Calls: calls}
		switch {
		case turn.Stop == StopMaxTokens:
			// A call cut off by the length limit may be a half-written
			// one; nothing of the turn is run.
			add(answerAll(next, notRunCutOff))
			res.Reason = ReasonMaxTokens
			return res, nil
		case l.Budget > 0 && res.Usage.Total() >= l.Budget:
			add(answerAll(next, notRunBudget))
			res.Reason = ReasonBudget
			return res, nil
		}
		batch = next
	}
}

// Limiter counts the requests each person started in the last minute.
type Limiter struct {
	mu   sync.Mutex
	hits map[int64][]time.Time
	// Now is the clock (time.Now when nil).
	Now func() time.Time
}

// Allow records a request of key and reports whether it is within perMinute;
// when it is not, retry is how long until it would be.
func (l *Limiter) Allow(key int64, perMinute int) (ok bool, retry time.Duration) {
	now := time.Now
	if l.Now != nil {
		now = l.Now
	}
	t := now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[int64][]time.Time{}
	}
	recent := l.hits[key][:0]
	for _, h := range l.hits[key] {
		if t.Sub(h) < time.Minute {
			recent = append(recent, h)
		}
	}
	if perMinute > 0 && len(recent) >= perMinute {
		l.hits[key] = recent
		return false, time.Minute - t.Sub(recent[0])
	}
	l.hits[key] = append(recent, t)
	return true, 0
}
