# LDAP / Active Directory & reverse-proxy header auth

Besides local passwords and [OIDC/SSO](SSO.md), filex ships two more auth
drivers for enterprise directories and gateway-fronted deployments:

- **`ldap`** - simple-bind against an LDAP or Active Directory server.
- **`proxy-header`** - trust identity headers set by an authenticating reverse
  proxy (oauth2-proxy, Authelia, Cloudflare Access, …).

> **Both can be configured either way.** `auth.ldap.*` / `auth.header_proxy.*`
> in `config.yaml`, or the `FILEX_LDAP_*` / `FILEX_HEADER_*` environment
> variables - env wins where both are set, and a container-only deployment never
> needs a config file. (Older releases were file-only; that restriction is gone.)
> You pick which drivers are *enabled* the usual way (`FILEX_AUTH_DRIVERS` or
> `auth.drivers`). See
> [CONFIGURATION.md → Authentication](CONFIGURATION.md#authentication).
> Since v0.43.0 LDAP and the proxy header can also be configured on **Admin →
> Identity providers**, applied without a restart and added after `local`; a
> driver the environment lists wins and is shown there read-only. See
> [SSO.md → Managing providers on the Identity providers page](SSO.md#managing-providers-on-the-identity-providers-page).

Both drivers **upsert the user into filex's local users table** on success -
unless `auto_create` is off or `allowed_groups` does not match
([the first sign-in rule](#the-first-sign-in-rule-who-gets-an-account)) - so
[RBAC](RBAC.md) grants, shares and the rest of filex treat them like any other
account.

- [LDAP / Active Directory](#ldap--active-directory)
  - [Directory accounts on the file protocols](#directory-accounts-on-the-file-protocols)
- [Reverse-proxy header auth](#reverse-proxy-header-auth)
- [Which tenant a new account lands in](#which-tenant-a-new-account-lands-in)
- [See also](#see-also)

---

## LDAP / Active Directory

### How it works

LDAP plugs into the **normal password login form**. When a user submits their
e-mail (or username) + password, filex tries each enabled login driver **in the
order they appear in `auth.drivers` / `FILEX_AUTH_DRIVERS`** and the first one
that accepts wins. Keep `local` first: it is a hash comparison against a row
filex already holds, so `admin@local` and every break-glass password stay
answerable even while the directory is unreachable.

The LDAP driver performs a classic *search-then-bind*:

```
 filex                                     Directory (LDAP/AD)
   │  dial url (ldap:// or ldaps://) ─────────►│
   │  (optional) StartTLS upgrade ────────────►│   ← only for ldap:// + start_tls
   │  (optional) bind as service account ─────►│   ← only if bind_dn set
   │  subtree search: user_filter(email) ─────►│
   │  ◄──────────────── user DN + email attr ──│
   │  re-bind as that DN with the user's pw ──►│   ← this is the auth check
   │  ◄──────────────────────── bind result ───│
   │  upsert into local users table            │
   │  mint 12h `filex_session` cookie          │
```

1. **Dial.** The `url` scheme decides the transport: `ldaps://` is implicit TLS,
   `ldap://` is plaintext (optionally upgraded - see `start_tls`).
2. **Optional StartTLS** upgrades a plain `ldap://` connection in-band.
3. **Optional service bind.** If `bind_dn` is set, filex binds with it to run the
   search; if omitted, the search runs **anonymously**.
4. **Search.** A whole-subtree search under `base_dn` using `user_filter` with
   `%s` replaced by the login email (lower-cased and LDAP-escaped), returning at
   most one entry.
5. **Re-bind.** filex re-binds as the **found DN** with the **user's own
   password** - that bind succeeding *is* the authentication.
6. **Upsert.** The account's e-mail is always an address: `email_attr` when the
   entry holds one, else the typed name when it already is an address (a UPN),
   else `<name>@local` - see
   [E-mail address for an account that has only a login name](#e-mail-address-for-an-account-that-has-only-a-login-name).
   The user is created if new (the [first sign-in rule](#the-first-sign-in-rule-who-gets-an-account)
   decides), then a normal **12-hour `filex_session` cookie** is minted (same
   session machinery as local login).

> **A new LDAP account starts with the built-in `user` role**, or with the
> custom role whose SSO-group target matches one of its directory groups
> (`group_attr`, see [the first sign-in rule](#the-first-sign-in-rule-who-gets-an-account));
> a filex group linked to one of its groups can give it a role too
> ([GROUPS.md](GROUPS.md#members-and-sso-links)). To make administrators from
> the directory, link a filex group to the LDAP group (e.g.
> `cn=it-admins,ou=groups,dc=example,dc=com`) and pick **Administrator (full
> access)** as its role - see [GROUPS.md → Administrators](GROUPS.md#administrators).
> Its members are administrators from their next sign-in or directory sync,
> and stop being one when they leave it. One person can also be made an
> administrator by hand on their page (**Users**); that sticks whatever their
> groups do.

**TLS note.** `ldaps://` is selected purely by the URL scheme. With no `ca_file`
both `ldaps://` and StartTLS verify against the **system trust roots**, so a
certificate signed by an internal CA fails the handshake. Point `ca_file`
(`FILEX_LDAP_CA_FILE`) at the PEM bundle holding that CA and it is **appended**
to the system pool - the public roots keep working, and you no longer have to
rebuild the container's `/etc/ssl/certs/ca-certificates.crt` to reach your own
directory. The file is read and validated **at boot**: a wrong path is a startup
error, not a login that fails hours later with a TLS message.

### Configuration - `auth.ldap.*`

| Key | Required | Default | Meaning |
|---|---|---|---|
| `url` | **yes** | - | Directory URL. `ldap://host:389` or `ldaps://host:636`. Scheme = transport. |
| `base_dn` | **yes** | - | Search base for the user subtree, e.g. `ou=people,dc=example,dc=com`. |
| `bind_dn` | no | - | Service-account DN for the search bind. **Omit → anonymous search.** |
| `bind_password` | no | - | Password for `bind_dn`. |
| `user_filter` | no | `(mail=%s)` | LDAP filter. **Every** `%s` is substituted with the escaped, lower-cased identifier, so a filter may use it more than once. For AD, `(userPrincipalName=%s)` or `(sAMAccountName=%s)` are common. |
| `email_attr` | no | `mail` | Attribute read back as the account's canonical email. |
| `start_tls` | no | `false` | Upgrade a plain `ldap://` connection via StartTLS. Ignored for `ldaps://`. |
| `ca_file` | no | - | PEM bundle holding a private/internal CA, **appended** to the system trust store. Applies to `ldaps://` and StartTLS alike. Validated at boot. |
| `protocol_login` | no | `true` | Let directory accounts sign in over WebDAV, SFTP and FTPS with their directory password. S3 and NFS never receive a password - they use the access keys and exports the account mints in the web app. See [the protocols section](#directory-accounts-on-the-file-protocols). |
| `auto_create` | no | `true` | Open an account at a person's first sign-in. See [the first sign-in rule](#the-first-sign-in-rule-who-gets-an-account). |
| `allowed_groups` | no | - | Comma list: only members of one of these directory groups get an account on the first sign-in. |
| `group_attr` | no | - (`memberOf` once `allowed_groups` is set) | The entry attribute listing a person's groups. Here groups are read only when this or `allowed_groups` is set; on the Identity providers page it defaults to `memberOf` and groups are always read. The same attribute feeds filex groups linked to LDAP groups - see [Groups](#groups). |
| `provider` | no | - | **Multi-tenant installs only.** Tenant slug a newly created account is homed in when the login names no tenant - neither a realm nor a tenant's own address (a bare name over SFTP, FTPS without SNI, an empty Realm on the platform's page). Environment or `config.yaml` only: the Identity providers page has no such field. See [Which tenant a new account lands in](#which-tenant-a-new-account-lands-in). |
| `group_filter` | no | - | Find the person's groups for the LDAP links by a **search** instead of `group_attr`: `%s` is their DN, `%u` the name they signed in with (both escaped). E.g. `(member=%s)`, `(uniqueMember=%s)`, `(memberUid=%u)`. Runs as `bind_dn`. |
| `group_base_dn` | no | `base_dn` | Where `group_filter` searches. |
| `sync_interval` | no | - (off) | Run [directory sync](#directory-sync) on its own this often: a duration such as `30m` or `6h`, at least `5m`. Without it, sync runs only from **Sync now**. |
| `sync_filter` | no | `user_filter` with `*` | The search that lists every person for directory sync. By default `user_filter` with the sign-in name replaced by `*` - `(mail=%s)` becomes `(mail=*)` - so sync finds exactly the people who can sign in. |
| `sync_disable_missing` | no | `false` | Directory sync switches off an account the directory made once the directory no longer lists it. |
| `sync_groups` | no | `true` | Directory sync brings every directory group in as a filex group. See [Groups from the directory](#groups-from-the-directory). |
| `sync_group_filter` | no | every group | Which directory groups sync brings in, searched under `group_base_dn` (default `base_dn`). Default: `(|(objectClass=groupOfNames)(objectClass=groupOfUniqueNames)(objectClass=group)(objectClass=posixGroup))`. Narrow it to leave groups out, e.g. `(&(objectClass=groupOfNames)(cn=dept-*))`. |
| `email_domains` | no | any | Comma-separated domains this directory may sign in or make accounts for, e.g. `partner.com`. See [Several directories](#several-directories). |

`url` and `base_dn` are the only hard requirements; everything else has a working
default.

#### Matching more than one attribute

A filter may repeat the placeholder, which is how you let people sign in with
either their mail address or their UPN:

```yaml
user_filter: "(&(objectCategory=person)(objectClass=user)(|(mail=%s)(userPrincipalName=%s)))"
```

> Filters are also searched with a size limit of **2** rather than 1, because
> Active Directory answers a subtree search from the domain root with
> continuation references (`DomainDnsZones`, `ForestDnsZones`, `Configuration`)
> alongside the match. If the filter genuinely matches **two accounts**, the
> login is refused and a warning names the filter - filex will not pick one.

### Example `config.yaml`

```yaml
auth:
  drivers: [local, ldap]        # or set FILEX_AUTH_DRIVERS=local,ldap
  ldap:                         # or the FILEX_LDAP_* env vars below
    url: ldaps://ldap.example.com
    bind_dn: "cn=filex-svc,ou=services,dc=example,dc=com"
    bind_password: "s3cr3t"
    base_dn: "ou=people,dc=example,dc=com"
    user_filter: "(mail=%s)"
    email_attr: mail
    start_tls: false
    # ca_file: /etc/filex/ldap-ca.pem   # only for a private/internal CA
```

Active Directory variant (bind by UPN, upgrade plaintext with StartTLS):

```yaml
auth:
  drivers: [local, ldap]
  ldap:
    url: ldap://ad.example.com
    bind_dn: "CN=filex svc,CN=Users,DC=example,DC=com"
    bind_password: "s3cr3t"
    base_dn: "DC=example,DC=com"
    user_filter: "(&(objectCategory=person)(objectClass=user)(|(mail=%s)(userPrincipalName=%s)))"
    email_attr: mail
    start_tls: true
    ca_file: /etc/filex/ad-root-ca.pem   # internal CA - the usual AD case
```

The same install from environment variables only (no `config.yaml`):

```
FILEX_AUTH_DRIVERS=local,ldap
FILEX_LDAP_URL=ldap://ad.example.com
FILEX_LDAP_BIND_DN=CN=filex svc,CN=Users,DC=example,DC=com
FILEX_LDAP_BIND_PASSWORD=s3cr3t
FILEX_LDAP_BASE_DN=DC=example,DC=com
FILEX_LDAP_USER_FILTER=(&(objectCategory=person)(objectClass=user)(|(mail=%s)(userPrincipalName=%s)))
FILEX_LDAP_EMAIL_ATTR=mail
FILEX_LDAP_START_TLS=true
FILEX_LDAP_CA_FILE=/etc/filex/ad-root-ca.pem
# FILEX_LDAP_PROVIDER=acme        # multi-tenant only; see "Which tenant …" below
# FILEX_LDAP_GROUP_ATTR=memberOf   # groups, for filex groups linked to them
# FILEX_LDAP_GROUP_FILTER=(member=%s)
# FILEX_LDAP_GROUP_BASE_DN=OU=Groups,DC=example,DC=com
# FILEX_LDAP_SYNC_INTERVAL=6h       # directory sync on its own (off by default)
# FILEX_LDAP_SYNC_DISABLE_MISSING=true
# FILEX_LDAP_SYNC_GROUPS=false       # don't bring directory groups in as filex groups
# FILEX_LDAP_SYNC_GROUP_FILTER=(&(objectClass=group)(cn=filex-*))
```

> Keep `local` in the driver list if you still want the built-in `admin@local`
> account (and any other password users) to work alongside LDAP. To retire
> `admin@local`, first make the directory's administrators administrators
> here (a group with **Administrator (full access)**, above), sign in as one
> of them, then delete it.

### Groups

A filex group can be **linked to LDAP groups** ([GROUPS.md → LDAP
links](GROUPS.md#ldap-links)): at every sign-in to the web UI, filex reads the
person's directory groups and puts them in - and takes them out of - every
filex group linked to one, and the role and folder access those groups carry
follow.

Where the groups are read:

- **`group_attr`** - the attribute on the person's own entry, read in the
  same search that found them; no extra round trip. Active Directory has it
  (`memberOf`), and OpenLDAP with the `memberof` overlay. The Identity
  providers page fills in `memberOf`; from the environment, set
  `FILEX_LDAP_GROUP_ATTR`.
- **`group_filter`** - a search for the groups instead, as the service
  account, under `group_base_dn` (default `base_dn`). For OpenLDAP without the
  overlay: `(member=%s)` (`groupOfNames`), `(uniqueMember=%s)`
  (`groupOfUniqueNames`) or `(memberUid=%u)` (`posixGroup`). For nested groups
  on Active Directory: `(member:1.2.840.113556.1.4.1941:=%s)`.

A group is matched by its DN or its common name, without case. Each group's
DN and common name are kept per person (`user_ldap_groups`), so a link added
to a group later takes effect at once.

```yaml
auth:
  ldap:
    # …
    group_attr: memberOf                 # the page's default
    # or, for OpenLDAP without memberof:
    # group_filter: "(member=%s)"
    # group_base_dn: "ou=groups,dc=example,dc=com"
```

Neither set, groups are not read and nobody's LDAP-linked memberships move.
Only the web sign-in reads them for the links - the file protocols present
the password on every request and are left alone. A failed group read never refuses a
sign-in and never takes anybody out of a group; it is logged as `ldap: could
not read the account's LDAP groups`.

An account an LDAP sign-in made is labelled **LDAP** on the Users page
([GROUPS.md → Where people come from](GROUPS.md#where-people-come-from)).

### Several directories

An install can sign people in from more than one directory - a second
Active Directory domain, a partner's directory. The first is `ldap` (the
environment's `FILEX_LDAP_*`, or the page's); Admin → Identity providers →
**Add a provider** → LDAP adds more, each with its own slug, page, settings,
**Test now**, **Sync now**, schedule and tenants
([TENANT-ADMIN.md](TENANT-ADMIN.md)). A new one starts switched off. One made
that way can be deleted; the accounts and groups it made stay, with their
files.

- **Sign-in** asks the local password first, then each directory in the
  order they were added, and the file protocols (WebDAV, SFTP, FTPS) the same
  way.
- **An account belongs to the directory that made it**
  (`users.auth_directory`, its slug): only that directory signs it in, so a
  directory that lists someone else's address cannot take their account. An
  account made here (with a password here) is signed in by the first
  directory only, as when there was one. An LDAP account from before is the
  first one's. An account from before 0.52 with no password here and no SSO
  identity carries no label yet (any directory signed such an account in
  then): the first directory that signs it in takes it, and from then on it
  is that directory's.
- **Email domains** (`email_domains`, e.g. `partner.com`) limits a directory
  to the addresses it may sign in or make accounts for, and the Users page
  makes no local account at them - asking only the directories that sign
  people in to the tenant the account is made in, never another tenant's.
- **Sync** counts only a directory's own accounts as no longer listed, and
  flags only its own groups as removed (a synced group's id starts with its
  directory's slug: `ldap:…`, `partner:…`). An address another directory owns
  is skipped and named in the report.
- A filex group can link LDAP groups of any directory; a link by DN names
  one directory's group, a link by name alone (`staff`) matches that name in
  every directory. A group that makes its members administrators counts
  only full DNs, and only for people of its own directory
  ([GROUPS.md → Administrators](GROUPS.md#administrators)).

### Testing a configuration

**Test now** on an LDAP provider's page (Admin → Identity providers) - and every save -
checks, in order: the address, the connection (and StartTLS), the service
bind, the base DN, then what sign-in and sync will find there: how many
people the user filter lists and how many of them have an e-mail
(`email_attr`); whether their groups can be read - listed in `group_attr`,
or found by `group_filter` for the first person found; and how many groups
directory sync would bring in. It counts up to 1000 of each ("1000+"). No
one with an e-mail is a failure; no groups is said, not failed - groups are
optional. Nothing is written. The page lays its settings out as
**Connection**, **People**, **Groups** and **Directory sync**.

### Directory sync

Sign-in alone makes an account only when a person first signs in, and moves
their groups only when they sign in again. **Directory sync** reads the whole
directory instead - Admin → Identity providers → an LDAP provider → **Sync
now**, and every
`sync_interval` on its own:

1. With `sync_groups` (on by default): every group `sync_group_filter`
   finds becomes a filex group - see below.
2. One paged search (500 a page) as `bind_dn` under `base_dn` with
   `sync_filter`, reading each person's e-mail and groups.
3. Each person with no account gets one, exactly as at their first sign-in
   (same tenant homing, labelled **LDAP**); an entry with no e-mail is
   skipped - but an account it already has, known by the person's permanent
   id, still counts as listed.
4. Each person's LDAP groups are recorded and their memberships of filex
   groups linked to LDAP groups brought in step - members added by hand stay.
5. A person the directory has **switched off** is switched off here too -
   administrators included - so their sessions, API keys and SFTP keys stop
   working along with their password. Only accounts the directory holds: an
   account with a password of its own here is left on, and the report says
   so. That is Active Directory's "account
   disabled" (`userAccountControl`), 389-ds's `nsAccountLock`, and an OpenLDAP
   password-policy lock with no end (`pwdAccountLockedTime` =
   `000001010000Z`; a lockout after wrong passwords ends on its own and is
   ignored). Nobody gets a new account while switched off there. The last
   administrator still on is never switched off: the report lists them as a
   problem instead, so a directory change cannot lock everyone out.
6. Accounts the directory made (**LDAP**) that it no longer lists lose their
   LDAP memberships; with `sync_disable_missing` they are also switched off
   (never an administrator; an account made here is never touched).
7. An account sync switched off (5 or 6) is switched back on when the
   directory lets the person back in - at the next sync, or at their next
   sign-in. An account switched off or on **by hand** stays as the
   administrator left it.

⚠ A search that finds **nobody** changes nothing - that is far likelier a
wrong `base_dn` or filter than an empty directory - and the run reports an
error instead. A run in which an account could not be looked up (the
database did not answer) counts nobody as no longer listed: step 6 waits for
the next run.

- `group_filter` with `%u` (the name a person signs in with) cannot be asked
  by sync, which does not know that name: their LDAP-linked memberships are
  left as they are and follow at their sign-in. `%s` (their DN) works in both.
- On a **multi-tenant** install sync has no realm to judge by: it reaches
  the accounts the directory already holds (it made them, or a sign-in in
  their realm took them) and those of the tenant it belongs to or is pinned
  to (`provider`). Another account at the same address is not touched; its
  person's own sign-in, in their realm, decides.
- **Sync now** needs an administrator signed in to the panel; an API key is
  refused (`session_required`).

One run at a time; a run goes on in the background and the card shows what
the last one did (found, new accounts, groups changed, no longer listed,
switched off, back on, e-mails changed, and the first problems).

Without a `sync_interval`, step 5 waits for someone to press **Sync now**:
the directory refuses the person's password at once, but the rest of their
access lasts until the next run.

### Who is who

A person is known by their **permanent id** in the directory - `entryUUID`
(OpenLDAP, lldap, 389-ds) or `objectGUID` (Active Directory) - before their
e-mail, recorded on the account at its first sign-in or sync:

- **E-mail changed there** (a new surname, a new domain): the person keeps
  their account, files and shares, and the account's e-mail follows. Their
  SFTP/FTP username does not change. If another account already has the new
  address, the account keeps the old one and sync lists it as a problem.
- **Address given to someone new**: the newcomer is **not** signed in to the
  previous owner's account. Sign-in is refused and sync lists the problem -
  rename or delete the old account, and the newcomer gets their own at their
  next sign-in or sync.

A directory that offers neither attribute works by e-mail alone, as before. The report is kept, so the schedule
carries on across a restart. Starting a run is audited as
`auth_provider.sync`; each run is logged as `auth: directory sync`.

#### Groups from the directory

Every directory group becomes a filex group of the same name, linked to it,
so its members fill it; on the Groups page they carry **LDAP**, and the
filter **Synced from LDAP** lists them. Give them folders and roles in
filex as with any group. **Which groups exist is managed on the directory**:

- **Renamed there** - each group is followed by its directory group's
  permanent id (`entryUUID`; `objectGUID` on Active Directory), so it is
  renamed here too, keeping its folders, role and members. A name an
  administrator changed here is kept. A directory group with neither is
  followed by its DN (kept as the DN's SHA-256, so a group under a deep OU
  fits every database; a rename there is then a new group here), and its
  directory name is cut to 255 characters.
- **Removed there** - the filex group is **not** deleted (its folders and
  role were set up here): its LDAP members leave it and it is flagged
  **Removed from LDAP**. On its page an administrator deletes it,
  or **keeps it as a filex group**, which no longer follows the directory.
  If the directory lists that same group again (a filter change, a
  restore), the group follows it again; a group deleted and made anew there
  has a new id and comes in as a new group.
- **Deleted here** - while the directory still has the group, the next sync
  makes it again (without its folders or role). To leave a group out,
  remove it on the directory or narrow `sync_group_filter`.
- **Linked by hand already** - a directory group a filex group already
  names in its LDAP links is not brought in twice.
- A synced group's LDAP link is the directory's: sync keeps it on the
  group's current DN, and the page does not edit it.
- A group search that finds **no** groups changes nothing.
- On a multi-tenant install, a tenant's own directory brings its groups in
  to that tenant; the platform's directory to the tenant its accounts are
  homed in (`provider`), or install-wide without one.

```yaml
auth:
  ldap:
    # …
    sync_interval: 6h
    # sync_filter: "(&(objectClass=person)(mail=*))"
    # sync_disable_missing: true
```

### Directory accounts on the file protocols

A directory account is upserted into filex's users table with **no password
hash** - filex never learns the password, the directory keeps it. So the file
protocols that take a password (WebDAV, SFTP, FTPS) cannot check it the way
they check a local one; they ask the directory instead, and `protocol_login` is
what allows that. It is **on by default**, so an account that can sign in to
the web UI can also mount `/dav` with the same credentials. S3 and NFS never
receive a password - they use the access keys and exports the account mints in
the web app.

- **Order.** The local hash is compared first (no network), the directory only
  when that cannot answer. A directory outage therefore never blocks
  `admin@local`.
- **Caching.** A successful verification is remembered for 5 minutes, exactly
  like a local password - Basic-auth protocols present the credential on every
  request, and without a cache a PROPFIND storm would be one LDAPS bind per
  request. ⚠ That TTL is also how long a password revoked **at the directory**
  keeps working on these protocols.
- **Usernames.** SFTP and FTPS carry only a username field. Once the account
  exists in filex, its canonical e-mail (from `email_attr`) is what gets sent to
  the directory, so signing in by filex username works too. An account whose
  entry has no e-mail is `alex@local`; when that address matches nothing, the
  driver asks the directory again as `alex`, so a `(uid=%s)` filter works over
  the protocols as well. Only an address of the installation's e-mail token is
  taken apart that way - a real mailbox never is.
- **2FA.** An account with TOTP enabled is refused on these protocols, whether
  its password is local or in the directory - none of them can carry a second
  factor. Such an account must use an API key (file explorer → navigation panel →
  **Connections → API keys**; an embed proxied with a shared *app* token does
  not show that entry - see [MCP.md](MCP.md#token-kinds---user-vs-app)).

Set `protocol_login: false` (or `FILEX_LDAP_PROTOCOL_LOGIN=false`) to keep
directory passwords on the login form only and require an API token everywhere
else.

### Failure modes & troubleshooting

| Symptom / log | Cause & fix |
|---|---|
| `ldap: url and base_dn required` (warning at boot) | `url` or `base_dn` missing. The driver is **skipped** and filex boots without it - LDAP logins silently won't work. Fill both keys. |
| `ldap: dial: …` on login | Can't reach the server - wrong host/port/scheme or a firewall. Confirm the `url` and that filex's network can reach it. |
| `ldap: starttls: …` on login | StartTLS negotiation failed: the server doesn't offer it, or its certificate is signed by a CA the system trust store does not hold. Point `ca_file` (`FILEX_LDAP_CA_FILE`) at the PEM bundle carrying your internal CA - it is **appended** to the system roots and applies to StartTLS exactly as it does to `ldaps://`. |
| `ldap: service bind: …` on login | `bind_dn` / `bind_password` are wrong, or the service account is locked. |
| Login rejected (generic "unauthorized") | Either the user wasn't found by `user_filter` under `base_dn`, or the final re-bind failed (wrong password). filex deliberately does **not** distinguish the two to the caller (no user enumeration) - but it **does** log the difference: run with `FILEX_LOG_LEVEL=debug` and look for `ldap: no directory entry matched` (filter/base problem) versus `ldap: user bind refused` (password/account problem). Test your filter with `ldapsearch -b <base_dn> '<filter with a real email>'`. |
| `ldap: search: …` on login | The directory could not be *asked* - connection reset, an expired service account, a base DN the account may not read. This is logged and reported separately from a wrong password on purpose: those two used to be indistinguishable. |
| `ldap: user_filter matched more than one entry` (warning) | The filter is ambiguous under `base_dn` (a duplicate or a stale account in another OU). filex refuses rather than picking one. Narrow the filter or the base DN. |
| Web login works, WebDAV/SFTP/FTPS answers 401 | `protocol_login` is off (or the account has TOTP enabled - see [above](#directory-accounts-on-the-file-protocols)). With TOTP on, mint an API token and use that as the password. |
| Login rejected even with a correct password, empty password box | An empty password is rejected up front - this guards against directories that treat an empty-password bind as a successful *anonymous* bind. |
| Login rejected even with a correct password | filex's sign-in limit has locked the account or the address (`429` with `Retry-After` on the web form). **Admin → Sign-in security** lists the locks and unlocks them ([CONFIGURATION.md → Sign-in attempt limits](CONFIGURATION.md#sign-in-attempt-limits)). |
| User can log in but has no admin rights | Expected - LDAP has no admin mapping. Elevate them in **admin UI → Users**. There is no `admin_group` for LDAP. |
| After the upgrade an account's e-mail changed from `alex` to `alex@local` | Expected: the account was [adopted](#accounts-an-older-filex-opened-under-the-bare-name) at its first sign-in (audit `auth.account_adopted`). Its files, shares and role are unchanged. |
| A new `alex@local` (in a tenant's realm `alex@<realm>.local`) account appeared beside an old `alex` | The old account is in another tenant than the sign-in was for (multi-tenant): the notification `ldap_legacy_account_elsewhere` and the audit row `auth.account_not_adopted` (`reason: other_tenant`) say which. filex does not cross the tenant boundary and does not merge two accounts: the old one keeps its files and can share them with the new one. |

> **Boot vs. login errors.** A *config* problem (`url and base_dn required`) is
> reported once at startup as a warning and the driver is skipped. *Connection*
> problems (dial/StartTLS/bind) happen per login attempt and surface as a failed
> sign-in; check the server logs for the wrapped `ldap: …` error.

---

## Reverse-proxy header auth

Driver name **`proxy-header`** (the loader also accepts `proxyheader` and
`header_proxy`; the config block is always `auth.header_proxy`). Use it when an
**authenticating reverse proxy** in front of filex - oauth2-proxy, Authelia,
Cloudflare Access, nginx `auth_request`, etc. - has already logged the user in
and forwards their identity as request headers.

### How it works

Unlike LDAP, this driver has no login form of its own. It runs on **every
request**, reads the identity from headers, and resolves (or provisions) the
user directly - **no session cookie is minted**, because the proxy is the source
of truth on each request.

```
 client ──► [ auth proxy ] ──► filex
                 │                 │  1. is the DIRECT peer IP in trusted_ips?  (no → ignore headers)
   sets headers ─┘                 │  2. read X-Auth-User (required), X-Auth-Email, X-Auth-Roles
   X-Auth-User / -Email / -Roles   │  3. roles ∋ admin_group? → admin, else user
                                    │  4. upsert user (first sight: the first sign-in rule)
```

1. **Source check.** filex compares the **direct peer address** (`RemoteAddr`)
   against `trusted_ips`. If it doesn't match, the headers are **ignored** and
   the request falls through to the next driver (typically unauthenticated).
2. **Identity.** It reads the user header (`X-Auth-User`). If empty →
   unauthorized. The email comes from `email_header`; if that's empty, filex uses
   the user value when it looks like an email, otherwise synthesizes
   `<user>@proxy.local`.
3. **Role.** The `group_header` value is split on commas (every line of the
   header, when the proxy sends it more than once); if any entry equals
   `admin_group` (case-insensitive) a **new** account is created as **admin**,
   otherwise as **user**. That is decided once, when the account is created:
   afterwards its role is its own, changed on its page, and the header neither
   promotes nor demotes it.
4. **Provision.** The user is looked up by email. A first-seen user gets an
   account unless `auto_create` is off, and with `allowed_groups` set only when
   the roles header names one of those groups
   ([the first sign-in rule](#the-first-sign-in-rule-who-gets-an-account)).
   On a multi-tenant install the new account is homed in
   the tenant whose host the request arrived on - see
   [Which tenant a new account lands in](#which-tenant-a-new-account-lands-in).
5. **Groups.** The same values are the person's groups: recorded, with the
   starting role of a new account and the filex groups linked to them - see
   [Groups from the roles header](#groups-from-the-roles-header).

> **Security - trust is by the DIRECT peer IP, and `X-Forwarded-For` is
> deliberately NOT honored.** If filex trusted XFF, any client could send
> `X-Forwarded-For: <trusted>` alongside forged `X-Auth-User: admin@…` headers
> and elevate themselves. So the check is on the actual TCP peer only.
> **This driver is only safe when the proxy is the *sole* ingress to filex** - if
> a client can reach filex directly from an address inside `trusted_ips`, it can
> forge any identity. Bind filex to the proxy's private network / localhost and
> never expose it directly.
>
> `trusted_ips` is a separate list from
> [`FILEX_TRUSTED_PROXIES`](CONFIGURATION.md#server--networking), which names
> the proxies whose forwarded client address filex believes (for the sign-in
> limit and the logs). Being on that list does not let a proxy set identity
> headers, and this check still reads the direct peer only.

`trusted_ips` is **mandatory**: the driver **refuses to initialize** without it
(no unrestricted header trust). The user header name is **fixed to `X-Auth-User`** -
it is not configurable via `config.yaml`. A first-seen user gets an account
unless `auto_create` is off (`FILEX_HEADER_AUTO_CREATE=false`), and with
`allowed_groups` set only when the roles header names one of those groups - see
[the first sign-in rule](#the-first-sign-in-rule-who-gets-an-account).

### Groups from the roles header

The values of the roles header (`group_header`; `header_roles` on the Identity
providers page) are the person's **groups**, as a claim is for OIDC and
`memberOf` for LDAP. filex records them whenever the request carries that
header - with `allowed_groups` set or not - through the one rule every provider
shares ([GROUPS.md](GROUPS.md#members-and-sso-links)):

- they **replace** the groups recorded before: the proxy is the authority on
  membership;
- a **new** account whose groups name a role starts with it
  ([PERMISSIONS.md](PERMISSIONS.md#starting-role-for-sso-groups)) - an account
  the admin value made an administrator gets none;
- the account joins every filex group **of its own tenant** linked to one of
  them, and leaves every group it was in only through such a link - on a
  multi-tenant install whichever tenant's address the request came to, never
  another tenant's group or an install-wide one for a tenant's account.

Every request is a sign-in for this provider, so the groups are written only
when the set differs from the one recorded (order and repeats do not count).

**No header and an empty header are two answers.** A request that carries no
roles header at all says nothing about groups: the recorded groups and the
memberships they made stay as they are - a proxy that sends the header only
now and then does not throw people out of their groups in between. A roles
header that is there but empty (or only commas) says "no groups": they are
cleared and the account leaves its linked groups. For `allowed_groups` both
mean "in no group", since a person without an account has nothing recorded yet.

> ⚠ **The proxy must set or strip the roles header on every request**, as it
> does the user header. filex believes whatever arrives from `trusted_ips`: a
> proxy that forwards a roles header the *client* sent lets that client choose
> its groups - the folders and roles of every group linked to them, and admin
> on a first sign-in.

### Configuration - `auth.header_proxy.*`

| Key | Required | Default | Meaning |
|---|---|---|---|
| `trusted_ips` | **yes** | - | CIDR list (bare IPs allowed → treated as `/32` or `/128`) of proxies whose identity headers filex will trust. **Empty ⇒ the driver refuses to start.** |
| `email_header` | no | `X-Auth-Email` | Header carrying the user's email. |
| `group_header` | no | `X-Auth-Roles` | Header carrying comma-separated roles/groups - the person's groups, recorded whenever the header is there ([Groups from the roles header](#groups-from-the-roles-header)). |
| `admin_group` | no | `admin` | The value within `group_header` that creates a new account as admin. |
| `auto_create` | no | `true` | Open an account for a first-seen user (`FILEX_HEADER_AUTO_CREATE`). See [the first sign-in rule](#the-first-sign-in-rule-who-gets-an-account). |
| `allowed_groups` | no | - | Comma list (`FILEX_HEADER_ALLOWED_GROUPS`): only a user whose `group_header` names one of these gets an account (see [the first sign-in rule](#the-first-sign-in-rule-who-gets-an-account)). It is only a door: the header's groups are recorded with or without it. |
| `provider` | no | - | **Multi-tenant installs only.** Tenant slug a newly created account is homed in when the request Host maps to no tenant. Environment or `config.yaml` only: the Identity providers page has no such field. See [Which tenant a new account lands in](#which-tenant-a-new-account-lands-in). |

> The **user identifier header is `X-Auth-User`** (fixed). A name header is
> accepted but unused (filex's users table has no name field today).

### Example `config.yaml`

```yaml
auth:
  drivers: [proxy-header]       # or FILEX_AUTH_DRIVERS=proxy-header
  header_proxy:                 # or the FILEX_HEADER_* env vars
    email_header: X-Auth-Email
    group_header: X-Auth-Roles
    admin_group: filex-admins
    trusted_ips:
      - "10.0.0.0/8"            # the proxy's private network
      - "172.18.0.0/16"         # e.g. the Docker bridge the proxy sits on
```

Your proxy must set, at minimum, `X-Auth-User`. Typical oauth2-proxy config:

```
--set-xauthrequest                 # emits X-Auth-Request-User/-Email/-Groups
# (rename to X-Auth-User / X-Auth-Email / X-Auth-Roles at the proxy, or point
#  email_header/group_header at whatever names your proxy actually sends)
```

> Only list drivers you actually front with the proxy. Running `proxy-header`
> **and** `local` together is fine, but remember the header check applies to
> every request - a request from outside `trusted_ips` simply falls through to
> local password auth.

### Failure modes & troubleshooting

| Symptom / log | Cause & fix |
|---|---|
| `proxyheader: trusted_proxies is required (CIDR list); refusing to start …` (warning at boot) | `trusted_ips` is empty/missing. The driver is **skipped**; if it's your only login path, **nobody can authenticate** (yet filex still boots - easy to miss). Add at least one CIDR. |
| `proxyheader: invalid trusted_proxy entry "…"` / `parse CIDR "…"` | A malformed `trusted_ips` entry. Use valid CIDRs (`10.0.0.0/8`) or bare IPs (`10.1.2.3`). Same skip behavior as above. |
| Every request is unauthorized even though the proxy sets headers | The **direct peer** isn't in `trusted_ips`. Something between the proxy and filex (a load balancer, the Docker userland proxy, a service mesh) changed the source IP, so `RemoteAddr` isn't your proxy's address. Add the *actual* direct-peer CIDR - **only** if that hop is itself trusted (XFF is not consulted, and `FILEX_TRUSTED_PROXIES` does not change this check). |
| Unauthorized despite a trusted source | The user header is empty. Confirm the proxy sets **`X-Auth-User`** (the fixed name), not just an email/roles header. |
| User logs in but never gets admin | The role value doesn't match. Check the exact string in `group_header` (e.g. `X-Auth-Roles`) and that `admin_group` equals one of the comma-separated values (comparison is case-insensitive). It is read when the account is **created** only: an account that already exists is made an administrator on its page. |
| Any client can impersonate anyone | filex is reachable directly from within a trusted CIDR. Lock filex behind the proxy (private network / localhost bind); the header trust model assumes the proxy is the *only* way in. |

---

## The first sign-in rule (who gets an account)

When somebody signs in through OIDC, LDAP, the header proxy or an
operating-system provider ([OS-LOGIN.md](OS-LOGIN.md)) and filex has no account
for them, one rule - the same for all of them - decides what happens:

1. **The account exists** (same e-mail): they sign in. Nothing below runs.
   For LDAP this includes the account an older filex opened under the bare
   login name: it is [adopted](#accounts-an-older-filex-opened-under-the-bare-name),
   and an adopted account is an existing one - `auto_create` and
   `allowed_groups` do not judge it, and it keeps the role it had.
2. **`auto_create` is off**: refused.
3. **`allowed_groups` is set and none of the person's groups is in it**: refused.
4. Otherwise the account is created, homed in the right tenant (see the next
   section), and a *new* account whose groups name a role starts with that role.

| Setting | Default | Meaning |
|---|---|---|
| `auto_create` | `true` (`false` for the operating-system providers) | Open an account on the first sign-in. The default keeps today's behaviour: an upgrade leaves nobody outside. For the header proxy, a value the Identity providers page saved under the older name `auto_provision` still reads; `auto_create` wins when both are set. |
| `allowed_groups` | empty | Comma list. When set, only members of one of these groups get an account. Names are compared case-insensitively, with the Turkish dotted and dotless **I** treated as one letter. |
| `show_refusal_reason` (LDAP, PAM, Windows) | `false` | Tell a person whose password was right why the rule still refuses them. Off, they get the wrong-password answer; see below for what turning it on costs. |
| `group_attr` (LDAP) | `memberOf` | The directory attribute listing a person's groups. On the Identity providers page it defaults to `memberOf` and groups are always read; configured from the environment or `config.yaml`, groups are read only when `allowed_groups` (or `group_attr`) is set. A value such as `CN=Editors,OU=Groups,DC=corp` counts as both the whole value and `Editors`. |

Where the groups come from: OIDC - the claim named by `role_claim`; LDAP - the
`group_attr` attribute of the person's entry; header proxy - the values of the
roles header (`header_roles` on the page, `group_header` in `config.yaml`; a
request without it is in no group here); the
operating-system providers - the login's or the token's groups
([OS-LOGIN.md](OS-LOGIN.md#groups-and-roles)). A provider
test fails the step `first_login` when `allowed_groups` is set but the provider
has nowhere to read groups from, because that configuration would refuse
everybody.

The same groups are recorded at every sign-in of an existing account too, and
keep the filex [groups](GROUPS.md#members-and-sso-links) linked to them in step:
the person joins a linked group and leaves it when the directory (or the claim,
the header, the machine) no longer carries it - one rule for every provider.
The header proxy records its groups whenever the request carries the roles
header, `allowed_groups` set or not; a request with no roles header leaves them
as they were, an empty one clears them
([Groups from the roles header](#groups-from-the-roles-header)).

Environment: `FILEX_OIDC_AUTO_CREATE`, `FILEX_OIDC_ALLOWED_GROUPS`,
`FILEX_LDAP_AUTO_CREATE`, `FILEX_LDAP_ALLOWED_GROUPS`, `FILEX_LDAP_GROUP_ATTR`,
`FILEX_LDAP_SHOW_REFUSAL_REASON`,
`FILEX_HEADER_AUTO_CREATE`, `FILEX_HEADER_ALLOWED_GROUPS` (or the same keys under
`auth.<provider>.*` in `config.yaml`, or on the Identity providers page).

**Group → role is not configured here.** It is the permission rules that already
target an SSO group ([PERMISSIONS.md](PERMISSIONS.md)): a new account starts with
the lowest-numbered enabled role whose SSO-group target matches one of its
groups. `allowed_groups` is only a door.

**What the person is told depends on how they signed in.**

- **Through SSO** the identity provider has already said who the person is, so
  the sign-in page names the rule - no account is opened at a first sign-in,
  or the person is in no allowed group - and says to ask the administrator
  ([SSO.md](SSO.md#when-an-sso-sign-in-is-refused)). An SSO address that has
  an account in another tenant is still answered like any failure.
- **Through the header proxy** the proxy has said who the person is: the 401
  of every request it names them in carries the reason code
  (`{"error":"unauthorized","reason":"auto_create_off"}`), and the sign-in page
  says it. A request the proxy names nobody in is the plain 401 it always was.
- **On the password form** (LDAP, PAM, Windows) a refusal is, by default, the
  answer a wrong password gets: the rule runs only after the directory has
  accepted the password, so a different answer would tell anybody guessing
  that the password is right - and the same directory usually signs people
  in to other services too. A provider's `show_refusal_reason` (off by
  default, per provider, a tenant's own directory included) changes that: on,
  a refusal that comes after a right password answers `403
  {"error":"sign-in refused","reason":"group_not_allowed"}` and the form says
  why - for LDAP and PAM the first sign-in rule, for Windows also a system
  account the SID named. A wrong password, an unknown account, a name refused
  before any password is tried, an account the operating system itself
  refused (locked, expired) and an account of another tenant are never told
  apart, whatever the setting. Changing it is audited like any provider
  change (`changed_fields`).

The operator gets the reason either way: a log line and an audit row
`auth.first_login_refused` with `provider`, `reason` (`auto_create_off`,
`group_not_allowed`, and - for the operating-system providers -
`forbidden_account`, `invalid_name`), the identifier the person gave and the
host. Never a password, a token or the group list.

### E-mail address for an account that has only a login name

A filex account is keyed by its e-mail address, and for a directory account that
address is **always** `name@domain`:

| The entry | The person types | The account's e-mail |
|---|---|---|
| has `mail` (`email_attr`) = `Alex.Smith@corp.example` | anything the filter matches | `alex.smith@corp.example` |
| has no e-mail | `alex@corp.example` (a UPN) | `alex@corp.example` |
| has no e-mail | `alex` | `alex@local` |
| has no e-mail, `FILEX_OS_LOGIN_EMAIL_TOKEN=evim` | `alex` | `alex@evim` |
| has no e-mail, a sign-in in tenant realm `acme` ([multi-tenant](MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)) | `alex` | `alex@acme.local` |

An `email_attr` value that is not an address (the attribute pointed at `uid`)
counts as no e-mail. The username is still the directory name in lower case
(`alex`); one that is already taken, or is not a valid username, is made one the
way every new account's is (`alex2`).

`FILEX_OS_LOGIN_EMAIL_TOKEN` (default `local`; `auth.login_email_token` in
`config.yaml`) is the same token the operating-system providers use for a login
name with no e-mail domain: `alex@local`. With a domain the person's own address
is used (`alex@corp.example`).

On a multi-tenant install the tenant's **realm** goes in front of the token:
`alex@acme.local` in realm `acme`, `alex@local` for the platform's own tenant -
so two tenants' `alex` are two accounts, where before the second one could never
get one (addresses are unique across the platform). The realm is the tenant the
account belongs to: the realm the sign-in named, else the login host's, else the
`provider` pin ([below](#which-tenant-a-new-account-lands-in)). The file
protocols hand the driver the account's address back; `alex@acme.local` is read
back as `alex` in realm `acme` only - another realm's address, or the platform's
`alex@local`, is never taken apart there. A real address (the mail attribute, a
UPN) is the same in every realm; if it belongs to another tenant's account, the
sign-in is refused before anything is written to that account.

> ⚠ **Choose the token once, at installation, and never change it.** It is part
> of every such account's e-mail address, and the address *is* the account. If it
> changes later, `alex` signs in as `alex@evim` and the `alex@local` account -
> with its files, shares and quota - stays where it was: two accounts for one
> person. For that reason it is read from the environment only; no page or API
> edits it. An invalid value (letters, digits, dots and dashes only) stops filex
> at boot.

#### Accounts an older filex opened under the bare name

Before this rule, an entry with no e-mail attribute was keyed by the bare name
(unless a token was set): `alex` signed in to an account whose e-mail is
`alex`. After the upgrade that person's next sign-in - the web form or a file
protocol - finds no `alex@local` and **adopts** the old account: the same
account, re-keyed to `alex@local`, with its files, shares, permissions, role
and quota where they were. Nothing is created. It happens once, and the audit
log has a row `auth.account_adopted` (`provider`, `old_email`, `new_email`,
`renamed`, the host; never a password) and the server log a line.

The old account is the one whose e-mail is exactly the login name (no `@`):
only the old directory path ever opened such a row - every other way of making
an account asks for an address. What happens to it depends on what it is, and
**nobody is refused by it and nobody has to do anything by hand**:

| The old account | What happens |
|---|---|
| is an ordinary directory account | adopted as above - in a tenant's realm to `alex@<realm>.local` (`alex@acme.local`) |
| also has a **local password** (an administrator set one) | adopted the same way; the password is left exactly as it was, so the local sign-in (`alex` or `alex@local` with that password) and the directory one both reach the account. The audit row carries `had_local_password: true` |
| is **bound to SSO** (it has an OIDC subject) | signed in to **as it is**: its e-mail stays `alex`, because that is what the SSO provider finds it by. Every later sign-in finds it again through the bare name, on the web and on the file protocols. One audit row `auth.account_adopted` with `renamed: false`, `reason: sso_account` |
| is in **another tenant** than the one the sign-in is for ([multi-tenant](MULTI-TENANCY.md)) | **left alone** - the tenant boundary is never crossed, the account is neither signed in to nor changed. The person gets the first sign-in rule in their own tenant (`auto_create` on opens `alex@<realm>.local` there, off refuses, as for anybody new). The platform operator is told **once per old account**: a notification (`ldap_legacy_account_elsewhere`) that only a platform administrator's bell carries - never a tenant administrator's - naming the login name and both tenants, and an audit row `auth.legacy_account_elsewhere` |

Every account left alone also gets an audit row `auth.account_not_adopted` with
the `reason` (`other_tenant`, or `no_tenant` - below). These two rows name no
user, so only the platform operator reads them.

> ⚠ **Set `FILEX_OS_LOGIN_EMAIL_TOKEN` before the upgrade** if `local` is not the
> token you want: the adoption re-keys the old accounts to it, and it cannot be
> changed afterwards.
>
> ⚠ **Multi-tenant:** a file-protocol sign-in says which tenant it is for by its
> address (WebDAV `Host`, FTPS SNI) or by `realm/name` in the user name (SFTP,
> [PROTOCOLS.md](PROTOCOLS.md#multi-tenant-installs-the-realm)). One that names
> nothing is for the platform's own tenant, where a directory account is never
> made: it adopts nothing (`no_tenant`) and, with no `provider` pinned, creates
> nothing either. Tell people to write their realm, or pin `provider`. An
> account an older build left in the supertenant (see
> [the last paragraph of the tenant section](#which-tenant-a-new-account-lands-in))
> is another tenant's as far as adoption goes: re-home it into its tenant
> **before** the upgrade, or its owner gets a new, empty account beside it (and
> you get the notification above).

Operating-system logins never create `root`, `Administrator`, `guest`, service
or system accounts (`daemon`, `www-data`, `systemd-*`, `_*`, …): those names are
refused in every configuration, and so is any name that is not a valid filex
username (at least 3 characters, not starting with a digit, `a-z 0-9 . - _`).

---

## Which tenant a new account lands in

Skip this on a single-tenant install: with `multi_tenant` off nothing here runs,
and a directory login behaves exactly as it always has.

On a [multi-tenant](MULTI-TENANCY.md) install it matters, because
`CreateUser` homes a new account in the `default` provider and `default` is the
**supertenant** - which is *confine-exempt*: it can reach every storage on the
box. An account created there is not merely mis-filed, it is privileged.

⚠ Both of these drivers did exactly that until the release this note ships in. If you ran either of them
with auto-provisioning on a multi-tenant install, read the last paragraph of this
section.

**The signal is the realm the sign-in names, else the request Host** - the host
being the same one that picks an OIDC realm
([realms](MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)):

| Login arrives as | Tenant named? | Where the account is created |
|---|---|---|
| The browser login form with a Realm (`POST /api/auth/login` → LDAP) | yes, by the realm | that realm's tenant |
| The browser login form on a tenant's own address | yes, by the host | the tenant whose `host` matches |
| A reverse-proxy header request | by the host | the tenant whose `host` matches |
| WebDAV / FTPS on a tenant's own address (`Host`, SNI), or `realm/name` over SFTP / FTPS | yes | that tenant |
| Nothing named: the platform's page with an empty Realm, a bare name over SFTP | **no** | the tenant named by `provider`, else **refused** |
| Any of the above, host matches no tenant and no realm is typed | - | the tenant named by `provider`, else **refused** |

⚠ **"Refused" is the deliberate answer, not an oversight.** The only fallback
available is `default`, and `default` is the supertenant - so provisioning anyway
would hand a confine-exempt, every-storage account to whoever the directory says
exists. Set `provider` (or `FILEX_LDAP_PROVIDER` / `FILEX_HEADER_PROVIDER`) to a
tenant slug if you need just-in-time creation on a login that names no tenant.
Such a sign-in is then that tenant's: its account carries that tenant's realm
(`alex@acme.local`) and is admitted for the sign-in the pin homed:

```yaml
auth:
  ldap:
    provider: acme          # logins that name no tenant create accounts in tenant `acme`
```

**Which tenants a directory serves, and where it creates accounts** (0.50,
[TENANT-ADMIN.md](TENANT-ADMIN.md#sign-in-providers-bound-to-tenants)): on a
multi-tenant install every sign-in provider is bound to tenants, and a
password is only ever checked against the directories of the tenant the
sign-in is for. The upgrade binds the directory that exists to every tenant
that exists (what it did before). A directory added later on Admin → Identity
providers serves the platform's own tenant only, until it is bound to more
there.

The `provider` pin exists only for LDAP and the header proxy configured from
the environment or `config.yaml`, and counts as a binding to the pinned
tenant. A provider configured on the Identity providers page has none, and
neither have the operating-system providers ([OS-LOGIN.md](OS-LOGIN.md)). For
those, a sign-in that names one of the tenants they serve (by its realm or its
own address) creates the account there, and a sign-in that names no tenant, or
the platform's own, creates none: a directory account is never made in the
platform's own tenant, so a directory bound to it alone signs in the accounts
that already exist there and creates nothing.

A tenant's administrator can add the tenant's **own** LDAP (Admin → My
tenant): it must use `ldaps://` or StartTLS and reach a public address, with
its CA pasted as PEM (`ca_pem`), unless the platform operator allowed that
tenant internal and insecure providers.

⚠ Homing happens at **creation only**. An account that already belongs to a
tenant is never moved by where it logged in - a login on another tenant's host
does not migrate somebody between customers, it just signs them into their own
account.

⚠ **Accounts an older build already created are not moved for you.** They sit in
the supertenant, and no upgrade can safely relocate them: nothing records which
driver created a row, and the break-glass `admin@local` is deliberately in the
supertenant too, so a blanket re-home would take an operator's own account away
from them. A multi-tenant install with either driver enabled now prints one WARN
at boot naming every non-admin account homed in the supertenant. Review them:

```sql
SELECT id, email FROM users WHERE provider_id = (SELECT id FROM providers WHERE is_supertenant = 1);
```

and re-home the ones that should move, as a supertenant admin:

```
PATCH /api/admin/users/{id}   {"provider_id": <tenant id>}
```


## See also

- [SSO.md](SSO.md) - OIDC/OAuth2 single sign-on (has env vars; role → admin mapping)
- [CONFIGURATION.md](CONFIGURATION.md#authentication) - full config/env reference, the `config.yaml` schema, and the file-only note
- [RBAC.md](RBAC.md) - per-storage and per-file access control (applies to LDAP/proxy users too)
- [STORAGE.md](STORAGE.md) - mounting the backends these users will browse
- [INSTALLATION.md](INSTALLATION.md) - running filex / first-run `admin@local`
