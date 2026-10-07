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
//
// ⚠ One named exception: a VIEWER's desktop also carries `write` (desktopScopes
// says why — the account dialog asks it, the viewer role still refuses every
// file change). It is the only verb the desktop may hold beyond the self-service
// ceiling, and only for a viewer; anything else wider is a failure here.
//
// Every desktop carries `comments:rw` (task #157, the maintainer's decision):
// that is not wider than the ceiling - anybody may mint it at /api/tokens - it
// is only the one door that hands it out without being asked.
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
		selfService := want
		if !strings.HasSuffix(want, ",comments:rw") {
			t.Fatalf("%s: desktop scopes %q, want comments:rw - the desktop comments as the browser does", role, want)
		}
		if role == model.RoleViewer {
			if want != "read,write,comments:rw" {
				t.Fatalf("viewer: desktop scopes %q, want read,write,comments:rw — the account dialog asks write", want)
			}
			selfService = "read,comments:rw"
		}
		got, err := h.cappedScopes(context.Background(), u, selfService)
		if err != nil {
			t.Fatalf("%s: desktop scopes %q are refused by the self-service ceiling: %v", role, selfService, err)
		}
		if got != selfService {
			t.Fatalf("%s: desktop scopes %q, self-service ceiling allows %q", role, selfService, got)
		}
	}
	// The exception stays an exception: a viewer still cannot mint `write` for
	// itself at /api/tokens.
	if _, err := h.cappedScopes(context.Background(), &model.User{Role: model.RoleViewer}, "read,write"); err == nil {
		t.Fatal("a viewer minted a write token at /api/tokens")
	}
}
