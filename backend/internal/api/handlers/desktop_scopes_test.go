package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TestDesktopScopes_NeverExceedTheSelfServiceCeiling ties the desktop pairing
// to the rule /api/tokens already enforces: whatever a paired desktop gets, its
// owner could have minted for themselves. If cappedScopes ever tightens, this
// fails instead of the desktop quietly keeping the old, wider grant.
func TestDesktopScopes_NeverExceedTheSelfServiceCeiling(t *testing.T) {
	h := &SelfTokens{}
	for _, role := range []string{model.RoleAdmin, model.RoleUser, model.RoleViewer} {
		u := &model.User{Role: role}
		want, err := desktopScopes(u)
		if err != nil {
			t.Fatalf("%s: desktop scopes refused by the issuance rule: %v", role, err)
		}
		// The strict token model (migration 00054): an explicit list, never
		// empty, never admin.
		if want == "" || strings.Contains(","+want+",", ",admin,") {
			t.Fatalf("%s: desktop scopes %q must be an explicit list without admin", role, want)
		}
		got, err := h.cappedScopes(context.Background(), u, want)
		if err != nil {
			t.Fatalf("%s: desktop scopes %q are refused by the self-service ceiling: %v", role, want, err)
		}
		if got != want {
			t.Fatalf("%s: desktop scopes %q, self-service ceiling allows %q", role, want, got)
		}
	}
}
