-- +goose Up
-- +goose StatementBegin

-- DRAFTS (issue #71, filex 0.48.0, docs/API.md → Drafts).
--
-- "New document" no longer creates the file where it was asked for. It writes
-- a DRAFT: a real file in the chosen storage's drafts area,
-- `.filex-drafts/<user id>/<draft_key>/<name>` (internal/syspath.Drafts), which
-- every editor opens like any other file. This table is what the file itself
-- cannot say: whose it is, and where it is meant to go. "Save" moves the file
-- to target_dir/target_name (or `name (2).ext` beside what is already there)
-- and drops the row; "Discard" moves the file to the trash and KEEPS the row,
-- so a restore from the trash brings the draft back into Drafts.
--
-- node_id      the draft file's catalogue row. A draft is live while that row
--              is (deleted_at IS NULL); the trash's retention purge deletes the
--              row, and the draft goes with it (ON DELETE CASCADE).
-- draft_key    the `<draft_key>` folder segment: random hex, unique, what the
--              API and the client address a draft by.
-- target_dir   the storage-relative folder the document is meant for, '' for
--              the storage root.
-- target_name  the file name it is meant to have there.
-- doc_type     the New-document type it was made as (internal/newdoc), so an
--              extensionless name (LICENSE) still opens in the right editor.
--
-- 00064: the highest number in every filex worktree on disk was 00063.
CREATE TABLE IF NOT EXISTS drafts (
    id          BIGSERIAL PRIMARY KEY,
    draft_key   TEXT NOT NULL,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    storage_id  BIGINT NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    node_id     BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    target_dir  TEXT NOT NULL DEFAULT '',
    target_name TEXT NOT NULL,
    doc_type    TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_drafts_key ON drafts (draft_key);
CREATE UNIQUE INDEX IF NOT EXISTS idx_drafts_node ON drafts (node_id);
CREATE INDEX IF NOT EXISTS idx_drafts_user ON drafts (user_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS drafts;
-- +goose StatementEnd
