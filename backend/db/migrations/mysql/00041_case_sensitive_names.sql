-- +goose Up
-- A FILE NAME IS COMPARED THE WAY THE STORAGE COMPARES IT: BYTE FOR BYTE.
--
-- 00001 gave every table utf8mb4_0900_ai_ci, which is case-insensitive AND
-- accent-insensitive. For an email or a setting key that is harmless; for a
-- file name it is wrong, because S3, SFTP, WebDAV, NFS and every Linux disk
-- treat `README.md` and `Readme.md` -- or `resume.txt` and `résumé.txt` -- as
-- two different files. The unique key on (storage_id, parent_id, name) did
-- not, so the second of any such pair never got a catalogue row: the upload
-- answered 200 and wrote the bytes, the file never listed, and every sync
-- logged "create node failed ... Duplicate entry" for it, forever. Measured
-- 2026-09-14 on MySQL 8.4.11 and 8.0.46; SQLite and PostgreSQL compare names
-- exactly and were never affected.
--
-- utf8mb4_0900_bin, not utf8mb4_bin: the older binary collation is PAD SPACE,
-- so `notes` and `notes ` would still collide. 0900_bin is NO PAD. It exists
-- from MySQL 8.0.17, and MariaDB 11.4 accepts the name.
--
-- The columns: the tree (nodes.name, nodes.path) and the two other places a
-- path is a unique key -- a grant on `/Docs` must not overwrite a grant on
-- `/docs`, and a replica failure for one must not merge into the other.
-- Search keeps matching regardless of case; Store.SearchNodes asks for the
-- accent- and case-insensitive collation explicitly on MySQL.
--
-- ⚠ Do not wrap these statements in a goose statement block: a wrapped block
-- reaches the server as one multi-statement query, which the MySQL driver
-- refuses unless the DSN opts into multiStatements.
--
-- ⚠ Rebuilds the nodes table. On a large catalogue this runs as long as an
-- ALTER TABLE of that table does on your server; it cannot fail on existing
-- data, because rows that were unique ignoring case are unique with it.
ALTER TABLE nodes MODIFY name VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL, MODIFY path VARCHAR(2048) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL;
ALTER TABLE file_grants MODIFY path_prefix VARCHAR(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL DEFAULT '';
ALTER TABLE replica_failures MODIFY path VARCHAR(1024) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL;

-- +goose Down
-- ⚠ Fails with a duplicate-key error once two names that differ only by case
-- or accent exist in one folder -- which is exactly what Up made possible.
-- Rename or remove one of each pair first.
ALTER TABLE replica_failures MODIFY path VARCHAR(1024) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL;
ALTER TABLE file_grants MODIFY path_prefix VARCHAR(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL DEFAULT '';
ALTER TABLE nodes MODIFY name VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL, MODIFY path VARCHAR(2048) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL;
