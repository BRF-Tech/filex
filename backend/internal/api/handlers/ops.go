package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/storage"
)

// Ops handles async copy/move/delete tasks. Submissions return an opID and
// the worker reports progress on /api/files/ops/{id}.
//
// State is kept in-memory — restarts forget pending ops. Callers should
// poll within a few seconds; long-running batches are not yet recoverable.
type Ops struct {
	Store           db.Store
	StorageResolver func(int64) (storage.Driver, error)

	mu  sync.RWMutex
	ops map[string]*opStatus
}

// NewOps constructs an Ops handler.
func NewOps(store db.Store, resolver func(int64) (storage.Driver, error)) *Ops {
	return &Ops{
		Store:           store,
		StorageResolver: resolver,
		ops:             map[string]*opStatus{},
	}
}

// opStatus is the in-memory progress record for a task.
type opStatus struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Total    int       `json:"total"`
	Done     int       `json:"done"`
	Failed   int       `json:"failed"`
	Status   string    `json:"status"` // running, ok, failed
	Error    string    `json:"error,omitempty"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitempty"`
}

type opsRequest struct {
	Kind      string   `json:"kind"`       // copy, move, delete
	StorageID int64    `json:"storage_id"`
	Sources   []string `json:"sources"`
	Dest      string   `json:"dest,omitempty"`
}

// Submit queues a new op and returns the opID.
func (o *Ops) Submit(w http.ResponseWriter, r *http.Request) {
	var req opsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if len(req.Sources) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no sources"})
		return
	}
	drv, err := o.StorageResolver(req.StorageID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage"})
		return
	}
	st := &opStatus{
		ID:      uuid.NewString(),
		Kind:    req.Kind,
		Total:   len(req.Sources),
		Status:  "running",
		Started: time.Now(),
	}
	o.mu.Lock()
	o.ops[st.ID] = st
	o.mu.Unlock()

	go o.run(req, drv, st)

	writeJSON(w, http.StatusAccepted, map[string]string{"op_id": st.ID})
}

// Status returns the live or final state of a submitted op.
func (o *Ops) Status(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	o.mu.RLock()
	st, ok := o.ops[id]
	o.mu.RUnlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown op"})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (o *Ops) run(req opsRequest, drv storage.Driver, st *opStatus) {
	for _, src := range req.Sources {
		if err := o.doOne(req, drv, src); err != nil {
			st.Failed++
			st.Error = err.Error()
		} else {
			st.Done++
		}
	}
	st.Finished = time.Now()
	if st.Failed == 0 {
		st.Status = "ok"
	} else if st.Done == 0 {
		st.Status = "failed"
	} else {
		st.Status = "partial"
	}
}

func (o *Ops) doOne(req opsRequest, drv storage.Driver, src string) error {
	switch req.Kind {
	case "delete":
		d, ok := drv.(storage.Deleter)
		if !ok {
			return errors.New("driver not deletable")
		}
		return d.Delete(noctx{}, src)
	case "move":
		m, ok := drv.(storage.Mover)
		if !ok {
			return errors.New("driver not movable")
		}
		return m.Move(noctx{}, src, req.Dest)
	case "copy":
		c, ok := drv.(storage.Copier)
		if !ok {
			return errors.New("driver not copyable")
		}
		return c.Copy(noctx{}, src, req.Dest)
	default:
		return errors.New("unknown kind: " + req.Kind)
	}
}

// noctx is a context.Context implementation that's never cancelled — used
// for background ops that outlive the HTTP request that started them.
type noctx struct{}

func (noctx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (noctx) Done() <-chan struct{}       { return nil }
func (noctx) Err() error                  { return nil }
func (noctx) Value(key any) any           { return nil }
