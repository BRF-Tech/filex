/**
 * connectionGuides — "how do I connect to this thing?", generated from the
 * live deployment instead of written as prose.
 *
 * A published document says `https://fm.example.com/dav/` and leaves the
 * reader to substitute three values, one of which (their own username) they
 * usually get wrong. What ships here is the real host, the real storage
 * name and the caller's own credential, with a copy button — which is the
 * difference between a doc and a working paste. No surveyed product
 * (Backblaze, R2, Storj, MinIO, Garage) does this; it is the cheapest win
 * on the whole surface.
 *
 * Shape of the thing: a protocol is a `GuideBuilder` registered in
 * `GUIDE_BUILDERS`. WebDAV is built here because WebDAV is what filex
 * serves today; S3 and SFTP are a builder each and a registry line when
 * their servers land — no second implementation, no second UI.
 *
 * The commands are not invented. They are the ones verified on 2026-08-14
 * and written down in `docs/WEBDAV.md` and §13.5 of the write-path
 * handover, including the three Windows registry limits that otherwise
 * look exactly like filex bugs.
 */

export type Translate = (key: string, vars?: Record<string, string | number>) => string;

/** Everything a guide is allowed to know about the deployment. */
export interface GuideContext {
  /** Absolute origin of the server, e.g. `https://fm.example.com`. */
  origin: string;
  /** The caller's own account e-mail — the username on every protocol. */
  user: string;
  /** Storage names the caller may see. */
  storages: string[];
  /** The storage the page is currently focused on, when any. */
  storage?: string;
  /**
   * The S3 endpoint, as the server computes it.
   *
   * ⚠ NOT derived from `origin` here. With a dedicated host the endpoint is
   * that host's root; without one it lives under `/s3`, and a client pointed
   * at the application root reaches the web app — rclone reported
   * "XML syntax error on line 10" against an HTML redirect page. The server
   * returns this with every key, and the guide repeats it verbatim.
   */
  s3Endpoint?: string;
  /** True when clients must be told to force path-style addressing. */
  s3PathStyle?: boolean;
  /** The access key id to write into the examples, when the caller has one. */
  s3AccessKeyID?: string;
  /** The freshly minted secret, shown only while the user is looking at it. */
  s3Secret?: string;
  /** The SFTP endpoint, as the server reports it. */
  sftpHost?: string;
  sftpPort?: number;
  /** False when the operator has the endpoint switched off. */
  sftpEnabled?: boolean;
  /** The login name a client must use — the username when there is one. */
  sftpLogin?: string;
  /** True when the account has at least one usable key registered. */
  sftpHasKey?: boolean;
  /** The FTPS endpoint, as the server reports it. */
  ftpsHost?: string;
  ftpsPort?: number;
  ftpsEnabled?: boolean;
  /** The passive data-port range, which the client's firewall must allow. */
  ftpsPasvMin?: number;
  ftpsPasvMax?: number;
  /** True when the server is using a self-signed certificate. */
  ftpsSelfSigned?: boolean;
  /** The NFS endpoint, as the server reports it. */
  nfsHost?: string;
  nfsPort?: number;
  nfsEnabled?: boolean;
  /** The freshly minted export path — the credential, shown once. */
  nfsPath?: string;
  /** True when the active export refuses writes. */
  nfsReadOnly?: boolean;
}

export type GuideBlockKind = 'text' | 'steps' | 'code' | 'note' | 'warn';

export interface GuideBlock {
  kind: GuideBlockKind;
  /** Prose for `text` / `note` / `warn`. */
  text?: string;
  /** Ordered instructions for `steps`. */
  steps?: string[];
  /** Copyable payload for `code`. */
  code?: string;
  /** Syntax hint / file name shown above a code block. */
  caption?: string;
}

export interface GuideClient {
  id: string;
  /** Product name — never translated ("Cyberduck" is Cyberduck). */
  name: string;
  platform: 'windows' | 'macos' | 'linux' | 'any';
  blocks: GuideBlock[];
}

/** One connection fact, rendered as a copyable row. */
export interface GuideFact {
  label: string;
  value: string;
  hint?: string;
  /** Rendered as a hint rather than a value — filex never has the
   *  plaintext of your password and must not pretend otherwise. */
  placeholderOnly?: boolean;
}

export interface ProtocolGuide {
  id: string;
  /** Protocol name — a wire protocol is not translated either. */
  name: string;
  summary: string;
  /** Server + credential, filled in from this deployment. */
  facts: GuideFact[];
  clients: GuideClient[];
  notes: GuideBlock[];
}

export type GuideBuilder = (ctx: GuideContext, t: Translate) => ProtocolGuide;

/** `https://fm.example.com` → `fm.example.com`; anything unparseable comes back whole. */
export function hostOf(origin: string): string {
  try {
    return new URL(origin).host;
  } catch {
    return origin.replace(/^https?:\/\//i, '').replace(/\/+$/, '');
  }
}

/** True when the deployment is not on TLS — several clients refuse that,
 *  and Windows refuses it silently, which is worse. */
export function isPlainHttp(origin: string): boolean {
  return /^http:\/\//i.test(origin);
}

/**
 * The username a client should be given. Falls back to a placeholder so a
 * guide rendered before `/api/auth/me` answers is still readable rather
 * than showing `undefined` in the middle of a command line.
 */
function userOf(ctx: GuideContext, t: Translate): string {
  return ctx.user || t('conn.guide.userPlaceholder');
}

// ─────────────────────────────────────────────────────────────────────
// WebDAV
// ─────────────────────────────────────────────────────────────────────

export const buildWebdavGuide: GuideBuilder = (ctx, t) => {
  const origin = ctx.origin.replace(/\/+$/, '');
  const root = `${origin}/dav/`;
  const target = ctx.storage ? `${origin}/dav/${ctx.storage}/` : root;
  const user = userOf(ctx, t);
  const secret = t('conn.guide.secretPlaceholder');

  const facts: GuideFact[] = [
    { label: t('conn.guide.fact.url'), value: target, hint: ctx.storage ? t('conn.guide.fact.urlStorageHint', { storage: ctx.storage }) : t('conn.guide.fact.urlRootHint') },
    { label: t('conn.guide.fact.user'), value: user, hint: t('conn.guide.webdav.userHint') },
    { label: t('conn.guide.fact.password'), value: secret, hint: t('conn.guide.webdav.passwordHint'), placeholderOnly: true },
  ];

  const clients: GuideClient[] = [
    {
      id: 'windows',
      name: 'Windows Explorer',
      platform: 'windows',
      blocks: [
        {
          kind: 'steps',
          steps: [
            t('conn.guide.webdav.win.s1'),
            t('conn.guide.webdav.win.s2', { url: target }),
            t('conn.guide.webdav.win.s3'),
          ],
        },
        {
          kind: 'code',
          caption: t('conn.guide.webdav.win.cmdCaption'),
          code: `net use Z: "${target}" /user:${user} ${secret} /persistent:yes`,
        },
        {
          kind: 'warn',
          text: t('conn.guide.webdav.win.limits'),
        },
        {
          kind: 'code',
          caption: t('conn.guide.webdav.win.regCaption'),
          code: [
            'reg add "HKLM\\SYSTEM\\CurrentControlSet\\Services\\WebClient\\Parameters" /v FileSizeLimitInBytes /t REG_DWORD /d 4294967295 /f',
            'reg add "HKLM\\SYSTEM\\CurrentControlSet\\Services\\WebClient\\Parameters" /v FileAttributesLimitInBytes /t REG_DWORD /d 20000000 /f',
            'net stop webclient && net start webclient',
          ].join('\n'),
        },
        { kind: 'note', text: t('conn.guide.webdav.win.https') },
        { kind: 'note', text: t('conn.guide.webdav.win.persist') },
        { kind: 'note', text: t('conn.guide.webdav.win.service') },
      ],
    },
    {
      id: 'macos',
      name: 'macOS Finder',
      platform: 'macos',
      blocks: [
        {
          kind: 'steps',
          steps: [
            t('conn.guide.webdav.mac.s1'),
            t('conn.guide.webdav.mac.s2', { url: target }),
            t('conn.guide.webdav.mac.s3'),
          ],
        },
        { kind: 'note', text: t('conn.guide.webdav.mac.note') },
      ],
    },
    {
      id: 'linux',
      name: 'Linux (davfs2 / GNOME / KDE)',
      platform: 'linux',
      blocks: [
        {
          kind: 'code',
          caption: t('conn.guide.webdav.linux.mountCaption'),
          code: [
            `sudo mount -t davfs ${target} /mnt/filex`,
            '',
            `# ${t('conn.guide.webdav.linux.gvfsComment')}`,
            `gio mount ${target.replace(/^https:/i, 'davs:').replace(/^http:/i, 'dav:')}`,
          ].join('\n'),
        },
        {
          kind: 'warn',
          text: t('conn.guide.webdav.linux.locks'),
        },
        {
          kind: 'code',
          caption: '/etc/davfs2/davfs2.conf',
          code: 'use_locks 0',
        },
      ],
    },
    {
      id: 'rclone',
      name: 'rclone',
      platform: 'any',
      blocks: [
        {
          kind: 'code',
          caption: t('conn.guide.webdav.rclone.obscureCaption'),
          code: `rclone obscure "${secret}"`,
        },
        {
          kind: 'code',
          caption: '~/.config/rclone/rclone.conf',
          code: [
            '[filex]',
            'type = webdav',
            `url = ${origin}/dav`,
            'vendor = other',
            `user = ${user}`,
            `pass = ${t('conn.guide.webdav.rclone.passPlaceholder')}`,
          ].join('\n'),
        },
        {
          kind: 'code',
          caption: t('conn.guide.webdav.rclone.useCaption'),
          code: [
            'rclone lsd filex:',
            ctx.storage ? `rclone lsl filex:${ctx.storage}` : 'rclone lsl filex:<storage>',
            ctx.storage
              ? `rclone copy ./local filex:${ctx.storage}/backup`
              : 'rclone copy ./local filex:<storage>/backup',
            'rclone mount filex: /mnt/filex',
          ].join('\n'),
        },
      ],
    },
    {
      id: 'cyberduck',
      name: 'Cyberduck / Mountain Duck',
      platform: 'any',
      blocks: [
        {
          kind: 'steps',
          steps: [
            t('conn.guide.webdav.duck.s1'),
            t('conn.guide.webdav.duck.s2', { host: hostOf(origin) }),
            t('conn.guide.webdav.duck.s3', { path: ctx.storage ? `/dav/${ctx.storage}/` : '/dav/' }),
            t('conn.guide.webdav.duck.s4', { user }),
          ],
        },
      ],
    },
  ];

  const notes: GuideBlock[] = [
    { kind: 'note', text: t('conn.guide.webdav.note.delete') },
    { kind: 'note', text: t('conn.guide.webdav.note.locks') },
    { kind: 'note', text: t('conn.guide.webdav.note.permissions') },
  ];

  if (isPlainHttp(origin)) {
    // Not decoration: over plain HTTP Windows refuses to send Basic
    // credentials at all, and says nothing useful about why.
    notes.unshift({ kind: 'warn', text: t('conn.guide.webdav.note.http') });
  }

  return {
    id: 'webdav',
    name: 'WebDAV',
    summary: t('conn.guide.webdav.summary'),
    facts,
    clients,
    notes,
  };
};

// ─────────────────────────────────────────────────────────────────────
// S3
// ─────────────────────────────────────────────────────────────────────

/**
 * Every command here was RUN against the endpoint on 2026-08-16, not copied
 * from a vendor page: rclone 1.73.5, aws-cli 2.11.6, restic 0.19.1, mc
 * 2025-08-13 and s3fs 1.93 each did a full round trip, and the flags below are
 * the ones those runs actually needed. Five of the endpoint's bugs were found
 * that way — a guide assembled from documentation would have shipped them.
 */
export const buildS3Guide: GuideBuilder = (ctx, t) => {
  const endpoint = (ctx.s3Endpoint || `${ctx.origin.replace(/\/+$/, '')}/s3`).replace(/\/+$/, '');
  const bucket = ctx.storage || ctx.storages[0] || '<bucket>';
  const akid = ctx.s3AccessKeyID || t('conn.guide.s3.keyPlaceholder');
  const secret = ctx.s3Secret || t('conn.guide.s3.secretPlaceholder');
  const pathStyle = ctx.s3PathStyle !== false;
  const host = hostOf(endpoint);

  const facts: GuideFact[] = [
    { label: t('conn.guide.s3.fact.endpoint'), value: endpoint, hint: t('conn.guide.s3.fact.endpointHint') },
    { label: t('conn.guide.s3.fact.bucket'), value: bucket, hint: t('conn.guide.s3.fact.bucketHint') },
    { label: t('conn.guide.s3.fact.key'), value: akid, hint: t('conn.guide.s3.fact.keyHint') },
    {
      label: t('conn.guide.s3.fact.secret'),
      value: secret,
      hint: t('conn.guide.s3.fact.secretHint'),
      // ⚠ Only real right after minting. filex stores the secret sealed and
      // cannot show it again, and a guide that printed something secret-shaped
      // would be teaching a value that authenticates as nothing.
      placeholderOnly: !ctx.s3Secret,
    },
    { label: t('conn.guide.s3.fact.region'), value: 'us-east-1', hint: t('conn.guide.s3.fact.regionHint') },
    {
      label: t('conn.guide.s3.fact.addressing'),
      value: pathStyle ? t('conn.guide.s3.fact.pathStyle') : t('conn.guide.s3.fact.virtualHosted'),
      hint: pathStyle ? t('conn.guide.s3.fact.pathStyleHint') : t('conn.guide.s3.fact.virtualHostedHint'),
    },
  ];

  const clients: GuideClient[] = [
    {
      id: 'rclone',
      name: 'rclone',
      platform: 'any',
      blocks: [
        {
          kind: 'code',
          caption: '~/.config/rclone/rclone.conf',
          code: [
            '[filex]',
            'type = s3',
            'provider = Other',
            `endpoint = ${endpoint}`,
            `access_key_id = ${akid}`,
            `secret_access_key = ${secret}`,
            'region = us-east-1',
            ...(pathStyle ? ['force_path_style = true'] : []),
          ].join('\n'),
        },
        {
          kind: 'code',
          caption: t('conn.guide.s3.rclone.useCaption'),
          code: [
            'rclone lsd filex:',
            `rclone copy ./local filex:${bucket}/backup -P`,
            `rclone sync ./local filex:${bucket}/backup`,
            `rclone mount filex:${bucket} /mnt/filex`,
          ].join('\n'),
        },
        { kind: 'note', text: t('conn.guide.s3.rclone.mtime') },
      ],
    },
    {
      id: 'awscli',
      name: 'AWS CLI',
      platform: 'any',
      blocks: [
        {
          kind: 'code',
          caption: t('conn.guide.s3.aws.configureCaption'),
          code: [
            'aws configure set aws_access_key_id ' + akid,
            'aws configure set aws_secret_access_key ' + secret,
            'aws configure set region us-east-1',
          ].join('\n'),
        },
        {
          kind: 'code',
          caption: t('conn.guide.s3.aws.useCaption'),
          code: [
            `aws --endpoint-url ${endpoint} s3 ls`,
            `aws --endpoint-url ${endpoint} s3 cp ./file.pdf s3://${bucket}/file.pdf`,
            `aws --endpoint-url ${endpoint} s3 sync ./local s3://${bucket}/backup`,
            `aws --endpoint-url ${endpoint} s3 presign s3://${bucket}/file.pdf --expires-in 300`,
          ].join('\n'),
        },
        ...(pathStyle
          ? [
              {
                kind: 'note' as GuideBlockKind,
                text: t('conn.guide.s3.aws.pathStyle'),
              },
              {
                kind: 'code' as GuideBlockKind,
                caption: '~/.aws/config',
                code: ['[default]', 's3 =', '    addressing_style = path'].join('\n'),
              },
            ]
          : []),
      ],
    },
    {
      id: 'restic',
      name: 'restic',
      platform: 'any',
      blocks: [
        {
          kind: 'code',
          caption: t('conn.guide.s3.restic.envCaption'),
          code: [
            `export AWS_ACCESS_KEY_ID=${akid}`,
            `export AWS_SECRET_ACCESS_KEY=${secret}`,
            `export RESTIC_REPOSITORY="s3:${endpoint}/${bucket}/restic"`,
            'export RESTIC_PASSWORD=…',
          ].join('\n'),
        },
        {
          kind: 'code',
          caption: t('conn.guide.s3.restic.useCaption'),
          code: [
            `restic init${pathStyle ? ' -o s3.bucket-lookup=path' : ''}`,
            `restic backup ~/Documents${pathStyle ? ' -o s3.bucket-lookup=path' : ''}`,
            `restic check --read-data${pathStyle ? ' -o s3.bucket-lookup=path' : ''}`,
            `restic forget --keep-daily 7 --prune${pathStyle ? ' -o s3.bucket-lookup=path' : ''}`,
          ].join('\n'),
        },
        { kind: 'note', text: t('conn.guide.s3.restic.verified') },
      ],
    },
    {
      id: 'mc',
      name: 'MinIO Client (mc)',
      platform: 'any',
      blocks: [
        {
          kind: 'code',
          caption: t('conn.guide.s3.mc.aliasCaption'),
          code: `mc alias set filex ${endpoint} ${akid} ${secret} --api S3v4${pathStyle ? ' --path on' : ''}`,
        },
        {
          kind: 'code',
          caption: t('conn.guide.s3.mc.useCaption'),
          code: [
            'mc ls filex',
            `mc cp ./file.pdf filex/${bucket}/file.pdf`,
            `mc mirror ./local filex/${bucket}/backup`,
            `mc du filex/${bucket}`,
          ].join('\n'),
        },
      ],
    },
    {
      id: 's3fs',
      name: 's3fs (Linux / macOS)',
      platform: 'linux',
      blocks: [
        {
          kind: 'code',
          caption: t('conn.guide.s3.s3fs.credsCaption'),
          code: [
            `echo "${akid}:${secret}" > ~/.passwd-s3fs`,
            'chmod 600 ~/.passwd-s3fs',
          ].join('\n'),
        },
        {
          kind: 'code',
          caption: t('conn.guide.s3.s3fs.mountCaption'),
          code: [
            `s3fs ${bucket} /mnt/filex \\`,
            '  -o passwd_file=~/.passwd-s3fs \\',
            `  -o url=${endpoint} \\`,
            ...(pathStyle ? ['  -o use_path_request_style'] : []),
          ].join('\n'),
        },
        { kind: 'note', text: t('conn.guide.s3.s3fs.note') },
      ],
    },
    {
      id: 'cyberduck',
      name: 'Cyberduck / Mountain Duck',
      platform: 'any',
      blocks: [
        {
          kind: 'steps',
          steps: [
            t('conn.guide.s3.duck.s1'),
            t('conn.guide.s3.duck.s2', { host }),
            t('conn.guide.s3.duck.s3', { key: akid }),
            t('conn.guide.s3.duck.s4'),
          ],
        },
        ...(pathStyle ? [{ kind: 'warn' as GuideBlockKind, text: t('conn.guide.s3.duck.pathStyle') }] : []),
      ],
    },
    {
      id: 'sdk',
      name: 'SDK (Go / Python / JS)',
      platform: 'any',
      blocks: [
        {
          kind: 'code',
          caption: 'boto3',
          code: [
            'import boto3',
            's3 = boto3.client(',
            '    "s3",',
            `    endpoint_url="${endpoint}",`,
            `    aws_access_key_id="${akid}",`,
            `    aws_secret_access_key="${secret}",`,
            '    region_name="us-east-1",',
            ...(pathStyle
              ? [
                  '    config=boto3.session.Config(s3={"addressing_style": "path"}),',
                ]
              : []),
            ')',
            `print([o["Key"] for o in s3.list_objects_v2(Bucket="${bucket}").get("Contents", [])])`,
          ].join('\n'),
        },
        { kind: 'note', text: t('conn.guide.s3.sdk.note') },
      ],
    },
  ];

  const notes: GuideBlock[] = [
    { kind: 'note', text: t('conn.guide.s3.note.buckets') },
    { kind: 'note', text: t('conn.guide.s3.note.permissions') },
    { kind: 'note', text: t('conn.guide.s3.note.trash') },
    { kind: 'note', text: t('conn.guide.s3.note.mtime') },
  ];

  if (pathStyle) {
    // First, because it is the one setting that makes a current SDK fail with
    // a DNS error that names neither filex nor the cause.
    notes.unshift({ kind: 'warn', text: t('conn.guide.s3.note.pathStyle') });
  }
  if (isPlainHttp(endpoint)) {
    notes.unshift({ kind: 'warn', text: t('conn.guide.s3.note.http') });
  }

  return {
    id: 's3',
    name: 'S3',
    summary: t('conn.guide.s3.summary'),
    facts,
    clients,
    notes,
  };
};


// ─────────────────────────────────────────────────────────────────────
// SFTP
// ─────────────────────────────────────────────────────────────────────

/**
 * Every command here was RUN against the endpoint on 2026-08-16 with OpenSSH
 * 9.6 and rclone 1.73 — including the two settings rclone needs because filex
 * has no shell, which are the difference between a clean run and a pile of
 * warnings about a missing `md5sum`.
 */
export const buildSftpGuide: GuideBuilder = (ctx, t) => {
  const host = ctx.sftpHost || hostOf(ctx.origin);
  const port = ctx.sftpPort || 2022;
  const user = ctx.sftpLogin || userOf(ctx, t);
  const target = ctx.storage ? `/${ctx.storage}` : '/<storage>';
  const portFlag = port === 22 ? '' : ` -P ${port}`;

  const facts: GuideFact[] = [
    { label: t('conn.guide.sftp.fact.host'), value: host, hint: t('conn.guide.sftp.fact.hostHint') },
    { label: t('conn.guide.sftp.fact.port'), value: String(port), hint: t('conn.guide.sftp.fact.portHint') },
    { label: t('conn.guide.fact.user'), value: user, hint: t('conn.guide.sftp.fact.userHint') },
    {
      label: t('conn.guide.sftp.fact.auth'),
      value: ctx.sftpHasKey ? t('conn.guide.sftp.fact.authKey') : t('conn.guide.sftp.fact.authPassword'),
      hint: t('conn.guide.sftp.fact.authHint'),
      placeholderOnly: true,
    },
    { label: t('conn.guide.sftp.fact.path'), value: target, hint: t('conn.guide.sftp.fact.pathHint') },
  ];

  const clients: GuideClient[] = [
    {
      id: 'openssh',
      name: 'OpenSSH (sftp / scp)',
      platform: 'any',
      blocks: [
        {
          kind: 'code',
          caption: t('conn.guide.sftp.openssh.connectCaption'),
          code: [
            `sftp${portFlag} ${user}@${host}`,
            `scp${portFlag} ./report.pdf ${user}@${host}:${target}/report.pdf`,
            `scp${portFlag} ${user}@${host}:${target}/report.pdf ./`,
          ].join('\n'),
        },
        {
          kind: 'code',
          caption: '~/.ssh/config',
          code: [
            'Host filex',
            `  HostName ${host}`,
            `  Port ${port}`,
            `  User ${user}`,
            '  # IdentityFile ~/.ssh/id_ed25519',
          ].join('\n'),
        },
        { kind: 'note', text: t('conn.guide.sftp.openssh.noShell') },
      ],
    },
    {
      id: 'key',
      name: t('conn.guide.sftp.key.tab'),
      platform: 'any',
      blocks: [
        {
          kind: 'steps',
          steps: [
            t('conn.guide.sftp.key.s1'),
            t('conn.guide.sftp.key.s2'),
            t('conn.guide.sftp.key.s3'),
          ],
        },
        {
          kind: 'code',
          caption: t('conn.guide.sftp.key.genCaption'),
          code: ['ssh-keygen -t ed25519 -C "filex"', 'cat ~/.ssh/id_ed25519.pub'].join('\n'),
        },
        // ⚠ The one command people reach for, and the one that cannot work.
        { kind: 'warn', text: t('conn.guide.sftp.key.noCopyId') },
      ],
    },
    {
      id: 'winscp',
      name: 'WinSCP',
      platform: 'windows',
      blocks: [
        {
          kind: 'steps',
          steps: [
            t('conn.guide.sftp.winscp.s1'),
            t('conn.guide.sftp.winscp.s2', { host, port: String(port) }),
            t('conn.guide.sftp.winscp.s3', { user }),
            t('conn.guide.sftp.winscp.s4'),
          ],
        },
      ],
    },
    {
      id: 'filezilla',
      name: 'FileZilla',
      platform: 'any',
      blocks: [
        {
          kind: 'steps',
          steps: [
            t('conn.guide.sftp.filezilla.s1'),
            t('conn.guide.sftp.filezilla.s2', { host: `sftp://${host}`, port: String(port) }),
            t('conn.guide.sftp.filezilla.s3', { user }),
          ],
        },
      ],
    },
    {
      id: 'rclone',
      name: 'rclone',
      platform: 'any',
      blocks: [
        {
          kind: 'code',
          caption: '~/.config/rclone/rclone.conf',
          code: [
            '[filex-sftp]',
            'type = sftp',
            `host = ${host}`,
            `port = ${port}`,
            `user = ${user}`,
            'key_file = ~/.ssh/id_ed25519',
            '# filex has no shell, so tell rclone not to look for one:',
            'shell_type = none',
            'md5sum_command = none',
            'sha1sum_command = none',
          ].join('\n'),
        },
        {
          kind: 'code',
          caption: t('conn.guide.sftp.rclone.useCaption'),
          code: [
            'rclone lsd filex-sftp:',
            `rclone copy ./local filex-sftp:${target}/backup -P`,
            `rclone sync ./local filex-sftp:${target}/backup`,
          ].join('\n'),
        },
      ],
    },
    {
      id: 'sshfs',
      name: 'sshfs (Linux / macOS)',
      platform: 'linux',
      blocks: [
        {
          kind: 'code',
          caption: t('conn.guide.sftp.sshfs.mountCaption'),
          code: [
            `sshfs -p ${port} ${user}@${host}:${target} /mnt/filex`,
            'fusermount -u /mnt/filex',
          ].join('\n'),
        },
        { kind: 'note', text: t('conn.guide.sftp.sshfs.note') },
      ],
    },
  ];

  const notes: GuideBlock[] = [
    { kind: 'note', text: t('conn.guide.sftp.note.storages') },
    { kind: 'note', text: t('conn.guide.sftp.note.permissions') },
    { kind: 'note', text: t('conn.guide.sftp.note.trash') },
    { kind: 'note', text: t('conn.guide.sftp.note.totp') },
  ];
  if (ctx.sftpEnabled === false) {
    notes.unshift({ kind: 'warn', text: t('conn.guide.sftp.note.disabled') });
  }

  return {
    id: 'sftp',
    name: 'SFTP',
    summary: t('conn.guide.sftp.summary'),
    facts,
    clients,
    notes,
  };
};


// ─────────────────────────────────────────────────────────────────────
// FTPS
// ─────────────────────────────────────────────────────────────────────

/**
 * Every command here was RUN against the endpoint on 2026-08-16 with curl
 * 8.5, lftp 4.9 and rclone 1.73 — including the settings each of them needs to
 * negotiate explicit TLS, which is the half of FTPS that goes wrong quietly.
 */
export const buildFtpsGuide: GuideBuilder = (ctx, t) => {
  const host = ctx.ftpsHost || hostOf(ctx.origin);
  const port = ctx.ftpsPort || 2121;
  const user = ctx.sftpLogin || userOf(ctx, t);
  const secret = t('conn.guide.secretPlaceholder');
  const target = ctx.storage ? `/${ctx.storage}` : '/<storage>';
  const pasv =
    ctx.ftpsPasvMin && ctx.ftpsPasvMax ? `${ctx.ftpsPasvMin}-${ctx.ftpsPasvMax}` : '30000-30100';

  const facts: GuideFact[] = [
    { label: t('conn.guide.ftps.fact.host'), value: host, hint: t('conn.guide.ftps.fact.hostHint') },
    { label: t('conn.guide.ftps.fact.port'), value: String(port), hint: t('conn.guide.ftps.fact.portHint') },
    { label: t('conn.guide.ftps.fact.mode'), value: t('conn.guide.ftps.fact.modeValue'), hint: t('conn.guide.ftps.fact.modeHint') },
    { label: t('conn.guide.fact.user'), value: user, hint: t('conn.guide.ftps.fact.userHint') },
    { label: t('conn.guide.fact.password'), value: secret, hint: t('conn.guide.ftps.fact.passwordHint'), placeholderOnly: true },
    { label: t('conn.guide.ftps.fact.pasv'), value: pasv, hint: t('conn.guide.ftps.fact.pasvHint') },
  ];

  const clients: GuideClient[] = [
    {
      id: 'filezilla',
      name: 'FileZilla',
      platform: 'any',
      blocks: [
        {
          kind: 'steps',
          steps: [
            t('conn.guide.ftps.filezilla.s1'),
            t('conn.guide.ftps.filezilla.s2', { host, port: String(port) }),
            t('conn.guide.ftps.filezilla.s3', { user }),
            t('conn.guide.ftps.filezilla.s4'),
          ],
        },
      ],
    },
    {
      id: 'winscp',
      name: 'WinSCP',
      platform: 'windows',
      blocks: [
        {
          kind: 'steps',
          steps: [
            t('conn.guide.ftps.winscp.s1'),
            t('conn.guide.ftps.winscp.s2', { host, port: String(port) }),
            t('conn.guide.ftps.winscp.s3', { user }),
          ],
        },
      ],
    },
    {
      id: 'curl',
      name: 'curl',
      platform: 'any',
      blocks: [
        {
          kind: 'code',
          caption: t('conn.guide.ftps.curl.caption'),
          code: [
            `curl --ssl-reqd --ftp-pasv --user ${user} \\`,
            `  -T ./report.pdf "ftp://${host}:${port}${target}/report.pdf"`,
            '',
            `curl --ssl-reqd --ftp-pasv --user ${user} \\`,
            `  -o ./report.pdf "ftp://${host}:${port}${target}/report.pdf"`,
          ].join('\n'),
        },
        // ⚠ The flag that matters. Without it curl will happily fall back to
        // plaintext against a server that allows it — this one does not, but
        // the habit is what protects you against the ones that do.
        { kind: 'note', text: t('conn.guide.ftps.curl.sslReqd') },
      ],
    },
    {
      id: 'lftp',
      name: 'lftp',
      platform: 'any',
      blocks: [
        {
          kind: 'code',
          caption: '~/.lftprc',
          code: [
            'set ftp:ssl-force true',
            'set ftp:ssl-protect-data true',
            'set ftp:passive-mode true',
          ].join('\n'),
        },
        {
          kind: 'code',
          caption: t('conn.guide.ftps.lftp.caption'),
          code: [
            `lftp -u ${user} ftp://${host}:${port}`,
            `# then: cd ${target}; put ./report.pdf; ls`,
          ].join('\n'),
        },
        { kind: 'note', text: t('conn.guide.ftps.lftp.protectData') },
      ],
    },
    {
      id: 'rclone',
      name: 'rclone',
      platform: 'any',
      blocks: [
        {
          kind: 'code',
          caption: '~/.config/rclone/rclone.conf',
          code: [
            '[filex-ftp]',
            'type = ftp',
            `host = ${host}`,
            `port = ${port}`,
            `user = ${user}`,
            '# rclone obscure "<your password>"',
            'pass = …',
            'explicit_tls = true',
          ].join('\n'),
        },
        {
          kind: 'code',
          caption: t('conn.guide.ftps.rclone.useCaption'),
          code: [
            'rclone lsd filex-ftp:',
            `rclone copy ./local filex-ftp:${target}/backup -P`,
          ].join('\n'),
        },
      ],
    },
    {
      id: 'printer',
      name: t('conn.guide.ftps.printer.tab'),
      platform: 'any',
      blocks: [
        {
          kind: 'steps',
          steps: [
            t('conn.guide.ftps.printer.s1', { host, port: String(port) }),
            t('conn.guide.ftps.printer.s2', { user }),
            t('conn.guide.ftps.printer.s3', { path: target }),
            t('conn.guide.ftps.printer.s4'),
          ],
        },
        // ⚠ The honest warning: plenty of scan-to-FTP firmware cannot do TLS
        // at all, and this endpoint will not talk to it. Saying so beats an
        // afternoon spent debugging a device that was never going to connect.
        { kind: 'warn', text: t('conn.guide.ftps.printer.noTLS') },
      ],
    },
  ];

  const notes: GuideBlock[] = [
    { kind: 'warn', text: t('conn.guide.ftps.note.tls') },
    { kind: 'note', text: t('conn.guide.ftps.note.passive') },
    { kind: 'note', text: t('conn.guide.ftps.note.storages') },
    { kind: 'note', text: t('conn.guide.ftps.note.trash') },
    { kind: 'note', text: t('conn.guide.ftps.note.prefer') },
  ];
  if (ctx.ftpsSelfSigned) {
    notes.push({ kind: 'warn', text: t('conn.guide.ftps.note.selfSigned') });
  }
  if (ctx.ftpsEnabled === false) {
    notes.unshift({ kind: 'warn', text: t('conn.guide.ftps.note.disabled') });
  }

  return {
    id: 'ftps',
    name: 'FTPS',
    summary: t('conn.guide.ftps.summary'),
    facts,
    clients,
    notes,
  };
};


// ─────────────────────────────────────────────────────────────────────
// NFS
// ─────────────────────────────────────────────────────────────────────

/**
 * Every command here was RUN against the endpoint on 2026-08-16 with the LINUX
 * KERNEL's own NFSv3 client — including the two options a mount fails silently
 * without (`port=` and `mountport=`, because filex serves both on one port and
 * runs no portmapper).
 */
export const buildNfsGuide: GuideBuilder = (ctx, t) => {
  const host = ctx.nfsHost || hostOf(ctx.origin);
  const port = ctx.nfsPort || 2049;
  const path = ctx.nfsPath || t('conn.guide.nfs.pathPlaceholder');
  const ro = ctx.nfsReadOnly ? ',ro' : '';
  const opts = `nfsvers=3,tcp,port=${port},mountport=${port},nolock${ro}`;

  const facts: GuideFact[] = [
    { label: t('conn.guide.nfs.fact.host'), value: host, hint: t('conn.guide.nfs.fact.hostHint') },
    { label: t('conn.guide.nfs.fact.port'), value: String(port), hint: t('conn.guide.nfs.fact.portHint') },
    {
      label: t('conn.guide.nfs.fact.export'),
      value: path,
      hint: t('conn.guide.nfs.fact.exportHint'),
      // ⚠ Only real right after minting: the path is stored hashed and cannot
      // be shown again, and printing something path-shaped would produce a
      // mount line that fails with no clue why.
      placeholderOnly: !ctx.nfsPath,
    },
    { label: t('conn.guide.nfs.fact.options'), value: opts, hint: t('conn.guide.nfs.fact.optionsHint') },
  ];

  const clients: GuideClient[] = [
    {
      id: 'linux',
      name: 'Linux',
      platform: 'linux',
      blocks: [
        {
          kind: 'code',
          caption: t('conn.guide.nfs.linux.mountCaption'),
          code: [
            'sudo mkdir -p /mnt/filex',
            `sudo mount -t nfs -o ${opts} ${host}:${path} /mnt/filex`,
            '',
            '# and to unmount:',
            'sudo umount /mnt/filex',
          ].join('\n'),
        },
        {
          kind: 'code',
          caption: '/etc/fstab',
          code: `${host}:${path}  /mnt/filex  nfs  ${opts},_netdev,noauto,x-systemd.automount  0  0`,
        },
        // ⚠ The warning that belongs next to the fstab line rather than in a
        // document: that file is world-readable on most systems.
        { kind: 'warn', text: t('conn.guide.nfs.linux.fstabSecret') },
      ],
    },
    {
      id: 'macos',
      name: 'macOS',
      platform: 'macos',
      blocks: [
        {
          kind: 'code',
          caption: t('conn.guide.nfs.mac.mountCaption'),
          code: [
            'sudo mkdir -p /Volumes/filex',
            `sudo mount -t nfs -o vers=3,tcp,port=${port},mountport=${port},nolock${ro},resvport ${host}:${path} /Volumes/filex`,
          ].join('\n'),
        },
        { kind: 'note', text: t('conn.guide.nfs.mac.resvport') },
      ],
    },
    {
      id: 'windows',
      name: 'Windows (Client for NFS)',
      platform: 'windows',
      blocks: [
        {
          kind: 'steps',
          steps: [
            t('conn.guide.nfs.win.s1'),
            t('conn.guide.nfs.win.s2'),
            t('conn.guide.nfs.win.s3'),
          ],
        },
        {
          kind: 'code',
          caption: t('conn.guide.nfs.win.cmdCaption'),
          code: `mount -o anon nolock ${host}:${path} Z:`,
        },
        // ⚠ Windows' client has no way to say "the mount service is on this
        // port", so it only works when filex is on 2049.
        { kind: 'warn', text: t('conn.guide.nfs.win.port') },
      ],
    },
    {
      id: 'synology',
      name: t('conn.guide.nfs.nas.tab'),
      platform: 'any',
      blocks: [
        {
          kind: 'steps',
          steps: [
            t('conn.guide.nfs.nas.s1', { host, port: String(port) }),
            t('conn.guide.nfs.nas.s2'),
            t('conn.guide.nfs.nas.s3'),
          ],
        },
      ],
    },
  ];

  const notes: GuideBlock[] = [
    { kind: 'warn', text: t('conn.guide.nfs.note.unencrypted') },
    { kind: 'warn', text: t('conn.guide.nfs.note.pathIsSecret') },
    { kind: 'note', text: t('conn.guide.nfs.note.noPortmapper') },
    { kind: 'note', text: t('conn.guide.nfs.note.uid') },
    { kind: 'note', text: t('conn.guide.nfs.note.trash') },
    { kind: 'note', text: t('conn.guide.nfs.note.revoke') },
  ];
  if (ctx.nfsEnabled === false) {
    notes.unshift({ kind: 'warn', text: t('conn.guide.nfs.note.disabled') });
  }

  return {
    id: 'nfs',
    name: 'NFS',
    summary: t('conn.guide.nfs.summary'),
    facts,
    clients,
    notes,
  };
};


// ─────────────────────────────────────────────────────────────────────
// filex mount (FUSE)
// ─────────────────────────────────────────────────────────────────────

/**
 * `filex mount` is the odd one out here: there is no server to point a
 * third-party client at, because filex's OWN binary is the client. It speaks
 * the REST API over the same HTTPS the browser uses, which is what makes it
 * the only one of these that works from anywhere — NFS needs a LAN, SFTP needs
 * sshfs or WinFsp already configured, and this needs a URL and a token.
 *
 * ⚠ It is not a sync. Nothing is copied except a bounded read cache, so it
 * opens one file out of a hundred thousand without downloading the rest;
 * `filex sync` is still the answer for having the files offline. The guide says
 * so, because somebody who picks the wrong one finds out slowly.
 */
export const buildMountGuide: GuideBuilder = (ctx, t) => {
  const origin = ctx.origin.replace(/\/+$/, '');
  const storage = ctx.storage || ctx.storages[0] || 'main';

  const facts: GuideFact[] = [
    { label: t('conn.guide.mount.fact.url'), value: origin, hint: t('conn.guide.mount.fact.urlHint') },
    {
      label: t('conn.guide.mount.fact.token'),
      value: t('conn.guide.mount.fact.tokenPlaceholder'),
      hint: t('conn.guide.mount.fact.tokenHint'),
      // filex never has the plaintext of a token after it is minted, and a
      // fact row that looked like one would be a lie the user pastes.
      placeholderOnly: true,
    },
    { label: t('conn.guide.mount.fact.remote'), value: `${storage}://`, hint: t('conn.guide.mount.fact.remoteHint') },
  ];

  const clients: GuideClient[] = [
    {
      id: 'linux',
      name: 'Linux',
      platform: 'linux',
      blocks: [
        {
          kind: 'code',
          caption: t('conn.guide.mount.linux.mountCaption'),
          code: [
            `export FILEX_URL=${origin}`,
            'export FILEX_TOKEN=<token>',
            '',
            'mkdir -p ~/filex',
            'filex mount ~/filex',
            '',
            `# one storage only, or a subfolder of it:`,
            `filex mount --remote '${storage}://' ~/filex`,
            `filex mount --remote '${storage}://projects/acme' --read-only ~/acme`,
          ].join('\n'),
        },
        {
          kind: 'code',
          caption: t('conn.guide.mount.linux.umountCaption'),
          code: 'fusermount -u ~/filex',
        },
        // ⚠ The one that costs an afternoon if it is not said: killing the
        // process leaves a directory where every `ls` hangs.
        { kind: 'warn', text: t('conn.guide.mount.linux.umountWarn') },
        {
          kind: 'code',
          caption: t('conn.guide.mount.linux.systemdCaption'),
          code: [
            '# ~/.config/systemd/user/filex-mount.service',
            '[Unit]',
            'Description=filex mount',
            'After=network-online.target',
            '',
            '[Service]',
            `Environment=FILEX_URL=${origin}`,
            'Environment=FILEX_TOKEN=<token>',
            'ExecStart=%h/.local/bin/filex mount %h/filex',
            'ExecStop=/bin/fusermount -u %h/filex',
            'Restart=on-failure',
            '',
            '[Install]',
            'WantedBy=default.target',
          ].join('\n'),
        },
      ],
    },
    {
      id: 'windows',
      name: 'Windows',
      platform: 'windows',
      blocks: [
        { kind: 'note', text: t('conn.guide.mount.win.winfsp') },
        {
          kind: 'code',
          caption: t('conn.guide.mount.win.mountCaption'),
          code: [
            `$env:FILEX_URL   = "${origin}"`,
            '$env:FILEX_TOKEN = "<token>"',
            '',
            'filex mount Z:',
          ].join('\n'),
        },
        // ⚠ The drive letter is CREATED, not reused — pointing it at one that
        // already exists is the commonest way this fails, and the message the
        // driver gives for it says nothing useful.
        { kind: 'warn', text: t('conn.guide.mount.win.freeLetter') },
        { kind: 'note', text: t('conn.guide.mount.win.stop') },
      ],
    },
    {
      id: 'macos',
      name: 'macOS',
      platform: 'macos',
      blocks: [
        // ⚠⚠ A refusal stated up front rather than discovered. macFUSE's Go
        // binding needs a C toolchain filex deliberately does not use, and its
        // licence forbids a commercial program from installing it. Pretending
        // otherwise would mean a command that appears to work and does nothing.
        { kind: 'warn', text: t('conn.guide.mount.mac.unsupported') },
        { kind: 'note', text: t('conn.guide.mount.mac.alternatives') },
      ],
    },
  ];

  const notes: GuideBlock[] = [
    { kind: 'note', text: t('conn.guide.mount.note.notASync') },
    { kind: 'note', text: t('conn.guide.mount.note.reachable') },
    { kind: 'note', text: t('conn.guide.mount.note.wholeFileWrites') },
    { kind: 'note', text: t('conn.guide.mount.note.trash') },
    { kind: 'note', text: t('conn.guide.mount.note.revoke') },
  ];

  return {
    id: 'mount',
    name: 'filex mount',
    summary: t('conn.guide.mount.summary'),
    facts,
    clients,
    notes,
  };
};

// ─────────────────────────────────────────────────────────────────────
// Registry
// ─────────────────────────────────────────────────────────────────────

/**
 * Protocol id → builder.
 *
 * S3 and SFTP land here as one entry each once their servers exist; the
 * panel, the copy buttons, the client tabs and the i18n plumbing are
 * already written for them. That is the whole point of the shape.
 */
export const GUIDE_BUILDERS: Record<string, GuideBuilder> = {
  webdav: buildWebdavGuide,
  s3: buildS3Guide,
  sftp: buildSftpGuide,
  ftps: buildFtpsGuide,
  nfs: buildNfsGuide,
  mount: buildMountGuide,
};

/**
 * Display name per protocol id.
 *
 * ⚠ Not `id.toUpperCase()`, which the picker used to do. That reads fine for
 * S3 and NFS and turns `filex mount` into "MOUNT" — a name for a thing that is
 * not a protocol at all, in a list where every other entry is one.
 */
export const GUIDE_NAMES: Record<string, string> = {
  webdav: 'WebDAV',
  s3: 'S3',
  sftp: 'SFTP',
  ftps: 'FTPS',
  nfs: 'NFS',
  mount: 'filex mount',
};

/** The label to show for a protocol id in a picker. */
export function guideName(id: string): string {
  return GUIDE_NAMES[id] || id.toUpperCase();
}

/** Protocol ids that have a guide, in display order. */
export function guideProtocols(): string[] {
  return Object.keys(GUIDE_BUILDERS);
}

export function buildGuide(
  protocol: string,
  ctx: GuideContext,
  t: Translate,
): ProtocolGuide | null {
  const builder = GUIDE_BUILDERS[protocol];
  return builder ? builder(ctx, t) : null;
}
