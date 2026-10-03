package handlers

// The admin MCP tools run their handler in-process and write the audit row the
// HTTP middleware would have written (AIAdmin.auditInvoke). A handler that
// recorded its request itself (auth.SkipAuditRow) gets no second row there
// either, as it gets none from the middleware.

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// recordingAuditStore keeps the audit rows written through it.
type recordingAuditStore struct {
	db.Store
	rows []*model.AuditEntry
}

func (s *recordingAuditStore) InsertAuditEntry(_ context.Context, e *model.AuditEntry) error {
	s.rows = append(s.rows, e)
	return nil
}

func TestAuditInvoke_AHandlerThatRecordedItselfGetsNoSecondRow(t *testing.T) {
	st := &recordingAuditStore{}
	a := &AIAdmin{store: st}
	admin := &model.User{ID: 7, Role: model.RoleAdmin}
	saved := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	recorded := func(w http.ResponseWriter, r *http.Request) {
		auth.SkipAuditRow(r.Context())
		w.WriteHeader(http.StatusOK)
	}

	a.invoke(context.Background(), admin, saved, http.MethodPatch, "/api/ai/admin/settings", nil, nil, map[string]any{"site_name": "x"})
	require.Len(t, st.rows, 1, "a write its handler did not record")
	require.Equal(t, "ai.settings.update", st.rows[0].Action)

	a.invoke(context.Background(), admin, recorded, http.MethodPatch, "/api/ai/admin/settings", nil, nil, map[string]any{"e2e.policy": "off"})
	require.Len(t, st.rows, 1, "a write its handler recorded itself got a second row")
}
