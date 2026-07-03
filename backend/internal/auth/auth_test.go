package auth

import (
	"context"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
)

func TestUserContext(t *testing.T) {
	ctx := context.Background()
	if u := UserFrom(ctx); u != nil {
		t.Fatal("expected nil user from empty ctx")
	}
	want := &model.User{ID: 42, Email: "a@b.c"}
	got := UserFrom(WithUser(ctx, want))
	if got == nil || got.ID != 42 {
		t.Fatalf("expected user 42, got %v", got)
	}
}
