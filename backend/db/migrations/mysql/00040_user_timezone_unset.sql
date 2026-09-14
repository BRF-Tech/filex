-- +goose Up
-- AN ACCOUNT THAT NEVER CHOSE A TIME ZONE HAS NONE.
--
-- Every account was created with timezone 'UTC' -- the column default, and the
-- same literal at six creation sites (first-run admin, admin API, invite,
-- OIDC, LDAP/directory provisioning, the CLI). Until the settings dialog got a
-- time-zone field that anything READ, that value was inert. Now the web app
-- honours the account's zone, so every one of those accounts drew every date
-- in UTC -- while the same explorer embedded on another page, which cannot see
-- the account, drew the browser's clock. Measured 2026-09-14 on one fresh
-- account: 11:57 PM UTC in the app beside 4:57 PM PDT in the embed, same file.
--
-- The empty string is model.TimezoneUnset: "use the clock of the device this
-- person is looking through". New accounts are created with it from this
-- version on; this clears the rows the old default wrote.
--
-- ⚠ It cannot tell a default 'UTC' from a deliberate one, and does not try:
-- before this version no screen that saved a zone had any effect (the old
-- profile page prefilled its field with the stored 'UTC' and wrote it back on
-- every save), so a stored 'UTC' is overwhelmingly the default. Anybody who
-- truly wants UTC picks it once in Settings -> Preferences and it sticks.
UPDATE users SET timezone = '' WHERE timezone = 'UTC';

-- +goose Down
-- Irreversible on purpose: which rows held the default and which a choice is
-- not recorded anywhere, and writing 'UTC' back would re-break every date for
-- the accounts that chose their device's clock after this ran.
SELECT 1;
