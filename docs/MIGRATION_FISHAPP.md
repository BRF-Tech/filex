# Migrating from `@brftech/file-explorer` → `@brftech/filex-core`

This guide is the FishApp-specific companion to
[MIGRATION.md](MIGRATION.md). It walks brf-mono (panel) and the
Capacitor-based fish.brf.sh PWA off the old vendor-shipped
`@brftech/file-explorer` package and onto the published
`@brftech/filex-core`.

## Why migrate

The old package was vendored from `infrastack/file-explorer/` into
both consumers' source trees and talked to Laravel routes inside
brf-mono (`/admin/fishapp/files/...`). The new package:

- Lives in its own monorepo (`brftech/filemanager`), versioned
  via SemVer, published to GitLab Package Registry.
- Backend speaks REST (`/api/files/...`) directly to the **filex**
  Go service instead of brf-mono's PHP controllers.
- Adds Monaco-eager-load + 9 viewers + Trash UI + tags/star/recent
  endpoints.

## Phase A — Decide on the backend

You have two options:

| Option | Backend | When to pick |
|--------|---------|--------------|
| **A1. filex** at `demo-fm.brf.sh` | New Go service from this repo | Recommended once `demo-fm.brf.sh` is live (see `DEPLOY_BRF.md`) |
| **A2. brf-mono** (status quo)   | Existing PHP controllers     | Keeps current setup running while migration is incremental |

The new package supports both — it just needs the right `apiBase` and
`auth` config. A1 is the long-term goal; A2 is a safety net.

## Phase B — brf-mono panel

```diff
# brf-mono/resources/js/file-manager.ts (or wherever FileExplorer is mounted)
- import { FileExplorer } from './vendor/file-explorer/file-explorer.js';
+ import { FileExplorer } from '@brftech/filex-core';
+ import '@brftech/filex-core/style.css';
```

```diff
# brf-mono/package.json
"dependencies": {
-   // (no entry; it was vendored)
+   "@brftech/filex-core": "^0.1.0"
}
```

For the Vite build to fetch the new package from GitLab's registry,
add a `.npmrc` to brf-mono (and to fish-mobile):

```
@brftech:registry=https://gitlab.com/api/v4/projects/<filemanager-project-id>/packages/npm/
//gitlab.com/api/v4/projects/<filemanager-project-id>/packages/npm/:_authToken=${GITLAB_TOKEN}
```

The `<filemanager-project-id>` is in GitLab → Settings → General → in
the project metadata. The `GITLAB_TOKEN` is a deploy/read-package
token; set it in CI variables (`CI_JOB_TOKEN` works automatically) and
in your local `.env` for dev.

After the swap, delete the vendored copy:

```
rm -rf brf-mono/resources/js/vendor/file-explorer/
```

## Phase C — Capacitor PWA (fish.brf.sh / fish-mobile)

Same swap as panel. The PWA's `src/views/FilesPage.vue` mounts the
component; just change the import path. Auth is `bearer` here:

```vue
<script setup lang="ts">
import { FileExplorer } from '@brftech/filex-core';
import '@brftech/filex-core/style.css';
import { useAuthStore } from '@/stores/auth';

const auth = useAuthStore();

const config = {
  apiBase: import.meta.env.VITE_FILES_API_BASE,           // https://demo-fm.brf.sh
  auth: { kind: 'bearer' as const, token: () => auth.token },
  locale: 'tr' as const,
  trashVisible: true,
};
</script>

<template>
  <ion-page>
    <ion-content>
      <FileExplorer :config="config" @error="onError" />
    </ion-content>
  </ion-page>
</template>
```

When pointing at the legacy backend (option A2), pass instead:

```ts
const config = {
  endpoint: '/admin/fishapp/files/manager',
  uploadInit: '/admin/fishapp/files/upload/init',
  // …per-route fields kept for back-compat…
  auth: { kind: 'bearer', token: () => auth.token },
};
```

## Phase D — Endpoint mapping

`apiBase`-based config auto-derives every URL. If you must continue
using legacy routes, here's the cheat sheet:

| Old (brf-mono PHP)                                     | New (filex Go)                                |
|--------------------------------------------------------|-----------------------------------------------|
| `GET    /admin/fishapp/files/manager?path=…`           | `GET    /api/files/manager?path=…`            |
| `POST   /admin/fishapp/files/upload/init`              | `POST   /api/files/upload/init`               |
| `POST   /admin/fishapp/files/upload/finalize`          | `POST   /api/files/upload/finalize`           |
| `POST   /admin/fishapp/files/share`                    | `POST   /api/files/share`                     |
| `GET    /admin/fishapp/files/share/{uuid}`             | `GET    /api/files/share/{token}`             |
| `DELETE /admin/fishapp/files/share/{uuid}`             | `DELETE /api/files/share/{id}`                |
| `POST   /admin/fishapp/files/archive/{list,extract,add}` | `POST /api/files/archive/{list,extract,add}` |
| `GET    /admin/fishapp/files/onlyoffice/config?path=…` | `GET    /api/files/onlyoffice/config?path=…`  |
| `GET    /admin/fishapp/files/thumb?path=…&exp=…&sig=…` | `GET    /api/files/thumb/{id}?exp=…&sig=…`    |

The thumb URL shape changed from path-keyed to id-keyed; update any
hand-rolled HTML referencing it.

## Phase E — Auth model swap

Legacy used either Laravel session cookies (panel) or a Sanctum bearer
token (PWA). Filex accepts the same shapes with one difference:

- `{ kind: 'bearer', token }` — same as before.
- `{ kind: 'csrf',   csrf  }` — same as before; brf-mono panel keeps
  using Laravel's `XSRF-TOKEN` cookie.
- New: `{ kind: 'basic', user, pass }` — for headless integrations.
- New: `{ kind: 'none' }` — public read-only mounts.

## Phase F — Verify

1. `pnpm -r build` succeeds in both repos.
2. Visiting the panel page renders the same UI as before.
3. Upload + share + delete + restore round-trip works against the new
   backend (run the Playwright `e2e/` suite if you wired it).
4. Sentry / notify shows no spike in 4xx/5xx after switching traffic.

## Phase G — Cleanup

```
rm -rf brf-mono/resources/js/vendor/file-explorer/
rm -rf fish-mobile/src/vendor/file-explorer/
git rm -r infrastack/file-explorer/                  # in the infrastack repo
```

Bump brf-mono's `composer.json` and `CHANGELOG.md`, and tag a fishapp
PWA release. The migration is done.
