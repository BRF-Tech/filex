-- +goose Up
-- THE BOT CHECK'S SHARED STATE (filex 0.58, #294): the outage flag and the
-- once-only marks, until expires_at (unix seconds). See the sqlite migration
-- of the same number. The key is ASCII (a prefix and hex), compared byte for
-- byte.
CREATE TABLE IF NOT EXISTS captcha_marks (
    mark_key   VARCHAR(160) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    expires_at BIGINT NOT NULL,
    PRIMARY KEY (mark_key),
    INDEX idx_captcha_marks_expires (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS captcha_marks;
