# filex

Fast, self-hosted file manager — a single Go binary with a modern web UI.

- **Storage backends**: local folders, S3/MinIO, SFTP, WebDAV, FTP — mix them in one instance.
- **Sharing**: public share links with PIN + expiry, "file drop" public upload links, share by e-mail.
- **Files**: full-text search, image/video/PDF thumbnails, trash + file versioning, in-browser previews.
- **Access**: role-based access control, per-item permissions, TOTP 2FA, optional SSO (OIDC / LDAP / proxy header).
- **Integrations**: OnlyOffice + draw.io editing, embeddable `<filex-explorer>` web component, AI/MCP API for agents, ShareX upload target.

## First login

Runtipi asks for an admin e-mail + password during install (both optional). If
you leave them empty, filex creates `admin@local` with a random password
printed **once** in the app logs and saved to `/data/.first-run.txt` inside the
container. Sign in at `/admin` and change it under Profile.
