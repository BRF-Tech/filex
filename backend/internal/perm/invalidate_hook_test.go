package perm_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/perm"
)

type hookCtxKey struct{}

// #196 - every write that invalidates the cached permissions (a role, a rule,
// a group membership, a person's role) also tells the hook, with the writer's
// context (its tenant scope) and the accounts it named; the server points the
// hook at the realtime hub so the open explorers it can concern ask their
// menu answers again, and nobody else's.
func TestInvalidate_TellsTheHookWhoMayBeConcerned(t *testing.T) {
	type call struct {
		marked bool
		ids    []int64
	}
	var calls []call
	perm.SetInvalidateHook(func(ctx context.Context, ids []int64) {
		calls = append(calls, call{marked: ctx.Value(hookCtxKey{}) == "writer", ids: ids})
	})
	t.Cleanup(func() { perm.SetInvalidateHook(nil) })

	writer := context.WithValue(context.Background(), hookCtxKey{}, "writer")
	perm.InvalidateFor(writer)
	perm.InvalidateFor(writer, 7, 9)
	perm.Invalidate()

	assert.Equal(t, []call{
		{marked: true, ids: nil},
		{marked: true, ids: []int64{7, 9}},
		{marked: false, ids: nil},
	}, calls, "the writer's context and its named accounts reach the hook; a bare Invalidate names nothing")

	perm.SetInvalidateHook(nil)
	perm.InvalidateFor(writer, 7)
	assert.Len(t, calls, 3, "a removed hook is not called")
}
