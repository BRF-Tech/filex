-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS replica_rules (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    path_pattern    VARCHAR(512) NOT NULL,
    mode            VARCHAR(16) NOT NULL,
    priority        INT NOT NULL DEFAULT 100,
    enabled         TINYINT(1) NOT NULL DEFAULT 1,
    description     TEXT NOT NULL,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_replica_rules_priority (priority, enabled)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS replica_failures (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    path            VARCHAR(1024) NOT NULL,
    op              VARCHAR(16) NOT NULL,
    error_code      VARCHAR(64) NOT NULL DEFAULT '',
    error_msg       TEXT,
    attempts        INT NOT NULL DEFAULT 1,
    last_attempt_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at     TIMESTAMP NULL DEFAULT NULL,
    UNIQUE KEY uniq_replica_failures_path_op (path(255), op),
    INDEX idx_replica_failures_unresolved (resolved_at, last_attempt_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS replica_status_reports (
    id              TINYINT PRIMARY KEY DEFAULT 1,
    generated_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    total_files     BIGINT NOT NULL DEFAULT 0,
    failed_count    BIGINT NOT NULL DEFAULT 0,
    repaired_count  BIGINT NOT NULL DEFAULT 0,
    summary_json    JSON NOT NULL,
    CONSTRAINT replica_status_reports_singleton CHECK (id = 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS replica_settings (
    id              TINYINT PRIMARY KEY DEFAULT 1,
    report_cron     VARCHAR(64) NOT NULL DEFAULT '',
    report_enabled  TINYINT(1) NOT NULL DEFAULT 0,
    default_mode    VARCHAR(16) NOT NULL DEFAULT 'mirror',
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT replica_settings_singleton CHECK (id = 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

ALTER TABLE storages ADD COLUMN role           VARCHAR(16) NOT NULL DEFAULT 'primary';
ALTER TABLE storages ADD COLUMN replica_of_id  BIGINT NULL;
ALTER TABLE storages ADD COLUMN replica_mode   VARCHAR(16) NOT NULL DEFAULT 'async';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS replica_settings;
DROP TABLE IF EXISTS replica_status_reports;
DROP TABLE IF EXISTS replica_failures;
DROP TABLE IF EXISTS replica_rules;
ALTER TABLE storages DROP COLUMN replica_mode;
ALTER TABLE storages DROP COLUMN replica_of_id;
ALTER TABLE storages DROP COLUMN role;
-- +goose StatementEnd
