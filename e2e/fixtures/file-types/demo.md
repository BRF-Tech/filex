# filex demo

**filex** is a self-hosted file manager. This fixture demonstrates a
broad markdown surface: headings, lists, fences, tables.

## Features

- multi-driver storage (local, s3, ftp, sftp, webdav)
- persistent queue (sqlite / redis / postgres)
- live thumbnails
- replica + reconcile

## Code

```ts
import { mountFileExplorer } from '@brftech/filex-core';
mountFileExplorer('#root', { api: { baseURL: '/api/files' } });
```

## Tabular

| Module  | Owner    | Status |
|---------|----------|--------|
| Storage | core     | stable |
| Search  | platform | stable |
| Replica | infra    | beta   |

See <https://gitlab.com/brftech/filemanager> for source.
