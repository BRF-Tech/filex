package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/model"
)

func TestCappedScopes_AdminRejected(t *testing.T) {
	h := &SelfTokens{}
	u := &model.User{Role: model.RoleUser}
	if _, err := h.cappedScopes(context.Background(), u, "read,admin"); err == nil {
		t.Fatal("admin scope must be rejected for self-service tokens")
	}
}

func TestCappedScopes_ViewerReadOnly(t *testing.T) {
	h := &SelfTokens{}
	v := &model.User{Role: model.RoleViewer}
	if _, err := h.cappedScopes(context.Background(), v, "read,write"); err == nil {
		t.Fatal("viewer must not get write scope")
	}
	got, err := h.cappedScopes(context.Background(), v, "read")
	if err != nil {
		t.Fatalf("viewer read should be allowed: %v", err)
	}
	if !strings.Contains(got, "read") || strings.Contains(got, "write") {
		t.Fatalf("unexpected viewer scopes: %q", got)
	}
}

// An empty list is REFUSED — on this door as on every other
// (apitoken.ParseIssued). It used to be quietly filled with a role default
// here, a second copy of a guard the admin door did not have; the owner's
// rule since v0.43.0 is that no door issues a token without an explicit list.
// A root on its own names no verb and is refused the same way.
func TestCappedScopes_EmptyIsRefused(t *testing.T) {
	h := &SelfTokens{}
	for _, raw := range []string{"", "  ", ",", "root:main://docs"} {
		got, err := h.cappedScopes(context.Background(), &model.User{Role: model.RoleUser}, raw)
		if !errors.Is(err, apitoken.ErrScopesRequired) {
			t.Fatalf("%q: want ErrScopesRequired, got %q, %v", raw, got, err)
		}
	}
}

func TestCappedScopes_UserVerbsPreserved(t *testing.T) {
	h := &SelfTokens{}
	got, err := h.cappedScopes(context.Background(), &model.User{Role: model.RoleUser}, "read,write,delete,mcp")
	if err != nil {
		t.Fatalf("user verbs should be allowed: %v", err)
	}
	for _, want := range []string{"read", "write", "delete", "mcp"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing scope %q in %q", want, got)
		}
	}
}
