-- +goose Up
-- AN APP'S SETUP (filex 0.60, docs/APP-PLUGINS-API.md → Setting an app up).
--
-- One row per app that is set up by an administrator before it runs: the
-- addresses its public routes were given and whether the wizard is done
-- (record_json, the host's own record - nothing of the app's data, which is
-- kept outside filex's database, internal/appdata). By app NAME, so a
-- reinstall finds no row and is set up again; removing the app deletes it.
-- updated_at is unix seconds.
CREATE TABLE IF NOT EXISTS app_plugin_setup (
    app         VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    record_json MEDIUMTEXT NOT NULL DEFAULT ('{}'),
    updated_at  BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (app)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- An uploader's answer may be its link's SHORT address (0.60, the
-- link-shortener role): off by default, the uploader's own choice.
ALTER TABLE uploaders ADD COLUMN short_link TINYINT(1) NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE uploaders DROP COLUMN short_link;
DROP TABLE IF EXISTS app_plugin_setup;
