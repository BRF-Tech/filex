-- +goose Up
-- WHERE AN EDITING SESSION'S SAVES GO WHEN IT IS NOT ITS OWN FILE (filex 0.57,
-- #268; internal/officesave write.go, handlers app_ui_beside.go).
--
-- An app's save of a document in a format the editor cannot write (a .doc,
-- .xls, .ppt...) goes beside it as OOXML (`rapor.docx`, under a name filex
-- chooses), and a save of a document that changed while it was open goes to
-- a conflict copy beside it. Every member's later save of the same session
-- goes to that same file, whoever of them saves: saved_to is that file's
-- path on the session's storage, and saved_sig is then its signature. ''
-- (every row before this migration): the saves go over the session's own
-- file.
ALTER TABLE coedit_sessions ADD COLUMN saved_to TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE coedit_sessions DROP COLUMN saved_to;
