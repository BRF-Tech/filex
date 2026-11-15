package watchin

import (
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/watchproto"
)

// AgentStatus is what one filex-watch key's agent last said, since this
// process started - the storage page's version, host and watched folder. The
// key's last use is the API token row's own last_used_at, which survives a
// restart; this does not, and the agent's next report (a heartbeat at the
// latest) fills it again.
type AgentStatus struct {
	Agent  watchproto.Agent `json:"agent"`
	SeenAt time.Time        `json:"seen_at"`
	// Seq is the last report taken from this agent.
	Seq uint64 `json:"seq"`
	// Reports, Folders and Resyncs count since this process started.
	Reports      int64      `json:"reports"`
	Folders      int64      `json:"folders"`
	Resyncs      int64      `json:"resyncs"`
	LastResyncAt *time.Time `json:"last_resync_at,omitempty"`
}

// Agents remembers each key's agent. Safe for concurrent use; the zero value
// is ready.
type Agents struct {
	mu    sync.Mutex
	byKey map[int64]*AgentStatus
	// now is the clock; nil = time.Now.
	now func() time.Time
}

// Note records an accepted report of key and reports whether it is a
// duplicate: the same agent sent the same batch again because it did not hear
// the first answer. Its folders are already queued and are not queued twice.
//
// ⚠ A report the server took (202) but had not handed to the catalogue when
// filex stopped is lost with the process. Every answer names the process
// (watchproto.Answer.Instance, handlers.StorageWatch): an agent that hears a
// new one asks for ONE resync in its next report, so a storage fed by an
// agent is brought up to date once after a restart - and only when an agent
// reports, not by the server guessing at every start. The agent also asks for
// one after any report it could not deliver, which covers what it sent while
// filex was down.
func (a *Agents) Note(keyID int64, rep watchproto.Report) (duplicate bool) {
	now := time.Now()
	if a.now != nil {
		now = a.now()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.byKey == nil {
		a.byKey = map[int64]*AgentStatus{}
	}
	st := a.byKey[keyID]
	if st == nil {
		st = &AgentStatus{}
		a.byKey[keyID] = st
	} else if rep.Seq != 0 && st.Agent.ID == rep.Agent.ID && st.Seq == rep.Seq {
		duplicate = true
	}
	st.Agent = rep.Agent
	st.SeenAt = now
	st.Reports++
	if duplicate {
		return true
	}
	st.Seq = rep.Seq
	st.Folders += int64(len(rep.Folders) + len(rep.Subtrees))
	if rep.Resync {
		st.Resyncs++
		at := now
		st.LastResyncAt = &at
	}
	return false
}

// Get is key's agent as last heard, ok false when this process has heard
// nothing from it.
func (a *Agents) Get(keyID int64) (AgentStatus, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.byKey[keyID]
	if st == nil {
		return AgentStatus{}, false
	}
	return *st, true
}

// Forget drops what was heard from key (the key was revoked).
func (a *Agents) Forget(keyID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.byKey, keyID)
}
