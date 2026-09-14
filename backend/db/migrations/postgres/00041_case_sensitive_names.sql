-- +goose Up
-- No-op on this engine. See mysql/00041_case_sensitive_names.sql: MySQL's
-- default collation compared file names ignoring case and accents, so two
-- files that differ only by either could not both be catalogued. PostgreSQL
-- compares names exactly and always has. The file exists so the three
-- dialects keep one numbering.
SELECT 1;

-- +goose Down
SELECT 1;
