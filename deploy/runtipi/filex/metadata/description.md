# filex

Fast, self-hosted file manager — a single Go binary with a modern web UI.

- **Storage backends**: local folders, S3/MinIO, SFTP, WebDAV, FTP and SMB/NAS shares — mix them in one instance, or teach filex a new one with a storage plugin.
- **Reachable without a browser**: the same tree is served AS S3, SFTP, FTPS, NFS and WebDAV, so rclone, restic, WinSCP or a media player can point straight at it; `filex mount` attaches it as a drive.
- **Sharing**: public share links with PIN + expiry, "file drop" public upload links, share by e-mail.
- **Files**: full-text search, image/video/PDF thumbnails, trash + file versioning, in-browser previews, and live updates in an open folder over a WebSocket.
- **Protection**: optional virus scanning of every file written — point filex at a ClamAV container and infected files are quarantined into the trash.
- **Access**: role-based access control, per-item permissions, TOTP 2FA, optional SSO (OIDC / LDAP / proxy header).
- **Integrations**: OnlyOffice + draw.io editing, embeddable `<filex-explorer>` web component, AI/MCP API for agents, ShareX upload target, a desktop app with two-way folder sync, and outbound webhooks.

## First login

Runtipi asks for an admin e-mail + password during install (both optional). If
you leave them empty, filex creates `admin@local` with a random password
printed **once** in the app logs and saved to `/data/.first-run.txt` inside the
container. Sign in at `/admin` and change it under Profile. Accounts you create
afterwards with the `user` or `viewer` role use `/drive` — the same file manager
without the admin panel.
