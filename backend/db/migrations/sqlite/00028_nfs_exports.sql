-- +goose Up
-- NFS exports: how an NFSv3 mount is bound to one filex account.
--
-- # The problem this table solves
--
-- NFSv3 has no usable authentication. Real per-request identity means
-- RPCSEC_GSS, which means Kerberos, which means a KDC and a machine keytab on
-- every client — for a self-hosted file server that is not a trade anybody
-- would take. And AUTH_SYS, the alternative every NAS ships, is just the client
-- asserting "I am uid 1000" with nothing to check it against.
--
-- # What is done instead
--
-- Bind the identity to the EXPORT rather than to the request. NFSv3's mount
-- handshake names an export path and the server answers with a file handle
-- rooted at it; nothing in the protocol says that path has to be guessable. So
-- each export gets its own path with 32 bytes of entropy in it, and THE PATH IS
-- THE SECRET — the same shape as a share link or a WebDAV URL carrying a token.
-- The mount is pinned to one principal for its whole lifetime, and every
-- operation inside it runs under that principal's scope.
--
-- ⚠ The uid/gid a client stamps on each request is then DISCARDED, not trusted.
-- Permission bits going out are synthesised from the ACL. RFC 5531's "AUTH_SYS
-- is known to be insecure" stops applying the moment nothing is decided from it.
--
-- ⚠⚠ Residual risk, stated plainly rather than hidden: NFSv3 is unencrypted and
-- unauthenticated on the wire. Anyone who can read the traffic, or who already
-- knows the path, can mount it. That is why the listener is OFF by default and
-- is meant for a LAN or a VPN — and why `filex mount` (FUSE) is the answer for
-- anything off-LAN.
--
-- # Why the token is hashed
--
-- Same reason an API token is: the server only ever has to COMPARE what a
-- client presented, never recompute it (unlike an S3 secret, which SigV4 needs
-- in recoverable form). A database dump then leaks no usable mount paths.
CREATE TABLE IF NOT EXISTS nfs_exports (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- The API token this export inherits from, or NULL when it was minted from
    -- the account. The FK cascades: revoking the token revokes the mount.
    api_token_id INTEGER REFERENCES api_tokens(id) ON DELETE CASCADE,
    label        TEXT NOT NULL DEFAULT '',
    -- sha256 of the path secret. UNIQUE because it is what a mount is looked
    -- up by.
    token_hash   TEXT NOT NULL UNIQUE,
    -- Optional confinement, the same (storage, prefix) shape the S3 keys use.
    storage_name TEXT NOT NULL DEFAULT '',
    prefix       TEXT NOT NULL DEFAULT '',
    -- ⚠ Read-only is a per-export choice here, unlike the other protocols. An
    -- NFS mount is used by a MACHINE — a media player, a build box, a backup
    -- reader — and "this one may only read" is the commonest thing an operator
    -- wants to say about one.
    read_only    INTEGER NOT NULL DEFAULT 0,
    -- Optional CIDR allow-list, comma-separated. Empty means any address the
    -- listener itself accepts.
    allow_cidrs  TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME,
    expires_at   DATETIME,
    disabled_at  DATETIME
);

CREATE INDEX IF NOT EXISTS nfs_exports_user_idx ON nfs_exports(user_id);
CREATE INDEX IF NOT EXISTS nfs_exports_token_idx ON nfs_exports(api_token_id);

-- +goose Down
DROP INDEX IF EXISTS nfs_exports_token_idx;
DROP INDEX IF EXISTS nfs_exports_user_idx;
DROP TABLE IF EXISTS nfs_exports;
