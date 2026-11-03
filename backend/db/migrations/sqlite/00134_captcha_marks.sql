-- +goose Up
-- THE BOT CHECK'S SHARED STATE (filex 0.58, #294; internal/captcha marks.go).
--
-- One row per mark, until expires_at (unix seconds): the outage flag
-- (`captcha:fallback:active`, raised when the provider cannot be reached, for
-- 120 seconds), and the once-only marks of a provider token, a local proof of
-- work and a file request's upload pass. In the database and not in memory
-- because several filex instances can share it: the instance that answered
-- 409 with a challenge and the one that receives the solution are not always
-- the same. Ended rows are pruned by the check itself.
CREATE TABLE IF NOT EXISTS captcha_marks (
    mark_key   TEXT    NOT NULL PRIMARY KEY,
    expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_captcha_marks_expires ON captcha_marks (expires_at);

-- +goose Down
DROP INDEX IF EXISTS idx_captcha_marks_expires;
DROP TABLE IF EXISTS captcha_marks;
