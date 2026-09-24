package testutil

import (
	"context"
	"testing"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tagname"
)

// TagNode puts TEAM tags of tenant `tenant` on a node — the shape every tag
// had before v0.43.0 (one shared label per file) and what migration 00055
// turns those into. 0 is "the instance": the tenant a single-tenant install's
// tags live in. Reuses a tag of the same name (tagname.Key) the way the
// handler does, so two calls build one vocabulary row, not two.
func TagNode(t *testing.T, store db.Store, nodeID, tenant int64, names ...string) {
	t.Helper()
	tagNodeAs(t, store, nodeID, model.TagQuery{Team: true, TeamTenants: []int64{tenant}},
		func(name string) *model.Tag {
			tid := tenant
			return &model.Tag{Kind: model.TagTeam, TenantID: &tid, Name: name}
		}, names)
}

// TagNodePersonal puts PERSONAL tags owned by `owner` on a node.
func TagNodePersonal(t *testing.T, store db.Store, nodeID, owner int64, names ...string) {
	t.Helper()
	tagNodeAs(t, store, nodeID, model.TagQuery{OwnerID: owner},
		func(name string) *model.Tag {
			uid := owner
			return &model.Tag{Kind: model.TagPersonal, OwnerID: &uid, Name: name}
		}, names)
}

func tagNodeAs(t *testing.T, store db.Store, nodeID int64, q model.TagQuery, mk func(string) *model.Tag, names []string) {
	t.Helper()
	ctx := context.Background()
	var add []int64
	for _, raw := range names {
		name, err := tagname.Clean(raw)
		if err != nil {
			t.Fatalf("testutil.TagNode: %q: %v", raw, err)
		}
		existing, err := store.ListTags(ctx, q)
		if err != nil {
			t.Fatalf("testutil.TagNode: list: %v", err)
		}
		var id int64
		for _, tg := range existing {
			if tagname.Same(tg.Name, name) {
				id = tg.ID
				break
			}
		}
		if id == 0 {
			tg := mk(name)
			tg.Key = tagname.Key(name)
			created, err := store.CreateTag(ctx, tg)
			if err != nil {
				t.Fatalf("testutil.TagNode: create %q: %v", name, err)
			}
			id = created.ID
		}
		add = append(add, id)
	}
	if err := store.LinkNodeTags(ctx, nodeID, add, nil); err != nil {
		t.Fatalf("testutil.TagNode: link: %v", err)
	}
}
