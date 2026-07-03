package handlers

import (
	"context"
	"strings"
	"testing"

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

func TestCappedScopes_EmptyNeverAll(t *testing.T) {
	h := &SelfTokens{}
	// Empty must never become "" (== all == includes admin). It must expand to
	// an explicit, admin-free default.
	got, err := h.cappedScopes(context.Background(), &model.User{Role: model.RoleUser}, "")
	if err != nil {
		t.Fatalf("empty scopes should default, got err: %v", err)
	}
	if got == "" {
		t.Fatal("empty scopes must expand to an explicit set, not stay empty")
	}
	if strings.Contains(got, "admin") {
		t.Fatalf("default scopes must never include admin: %q", got)
	}
	vgot, _ := h.cappedScopes(context.Background(), &model.User{Role: model.RoleViewer}, "")
	if strings.Contains(vgot, "write") || strings.Contains(vgot, "delete") {
		t.Fatalf("viewer default must be read-only: %q", vgot)
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
