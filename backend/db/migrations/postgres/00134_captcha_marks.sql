-- +goose Up
-- THE BOT CHECK'S SHARED STATE (filex 0.58, #294): the outage flag and the
-- once-only marks, until expires_at (unix seconds). See the sqlite migration
-- of the same number.
CREATE TABLE IF NOT EXISTS captcha_marks (
    mark_key   TEXT   NOT NULL PRIMARY KEY,
    expires_at BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_captcha_marks_expires ON captcha_marks (expires_at);

-- +goose Down
DROP INDEX IF EXISTS idx_captcha_marks_expires;
DROP TABLE IF EXISTS captcha_marks CASCADE;
