-- +goose Up
-- RECENT SEARCHES (task #168, docs/ADMIN-PANEL.md → Search).
--
-- What a person searched for in the admin panel's search, kept on the server
-- so the list follows them from one browser or device to the next. One row
-- per (person, surface, query); searching the same words again moves the row
-- to the top (the old row is deleted and a new one written, so `id` is the
-- order). The handler keeps the newest 20 per person and surface.
--
--   user_id      whose search it was. A deleted account takes its searches
--                with it (ON DELETE CASCADE).
--   surface      which search box: 'admin' (the admin panel). A column of its
--                own so another search box can keep a list of its own later
--                without a second table.
--   query_text   the words as typed, a prefix (`file:`, `user:` …) included,
--                at most 200 characters.
--   searched_at  when it was last searched for.
CREATE TABLE IF NOT EXISTS recent_searches (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    surface     TEXT NOT NULL DEFAULT 'admin',
    query_text  TEXT NOT NULL,
    searched_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_recent_searches_user ON recent_searches (user_id, surface);

-- +goose Down
DROP TABLE IF EXISTS recent_searches;
