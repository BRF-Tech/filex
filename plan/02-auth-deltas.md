# Δ Auth delta'ları

> İki madde: Proxy-header auth driver, Role-based admin button.
> SPEC.md §4.6 referans.

---

## §1 — Proxy-header auth driver

### Niye lazım
README'de "auth drivers: local · oidc · ldap · proxy-header" yazıyor ama `backend/internal/auth/drivers/` altında sadece `local`, `oidc`, `ldap` var. Proxy-header eksik.

**Use case:** Reverse proxy (nginx, Caddy, oauth2-proxy, Cloudflare Access) önde auth yapmış. Kullanıcı bilgisi header olarak iletiliyor (`X-Auth-User`, `X-Auth-Email`, `X-Auth-Roles`). Driver bu header'ları okur, kullanıcıyı set eder.

### Tasarım

**Dosya:** `backend/internal/auth/drivers/proxyheader/proxyheader.go`

Mevcut driver pattern: `backend/internal/auth/driver.go` (interface), `registry.go` (Register), `drivers/local/` veya `drivers/oidc/` örnek.

```go
package proxyheader

import (
    "context"
    "errors"
    "net"
    "net/http"
    "strings"

    "gitlab.com/brftech/filemanager/backend/internal/auth"
)

func init() {
    auth.Register("proxy-header", func() auth.Driver { return &Driver{} })
}

type Driver struct {
    headerUser    string
    headerEmail   string
    headerName    string
    headerRoles   string
    trustedNets   []*net.IPNet
    autoProvision bool
}

func (d *Driver) Name() string { return "proxy-header" }

func (d *Driver) Init(_ context.Context, cfg map[string]any) error {
    d.headerUser, _  = cfg["header_user"].(string)
    d.headerEmail, _ = cfg["header_email"].(string)
    d.headerName, _  = cfg["header_name"].(string)
    d.headerRoles, _ = cfg["header_roles"].(string)
    if d.headerUser == ""  { d.headerUser  = "X-Auth-User" }
    if d.headerEmail == "" { d.headerEmail = "X-Auth-Email" }
    if d.headerName == ""  { d.headerName  = "X-Auth-Name" }
    if d.headerRoles == "" { d.headerRoles = "X-Auth-Roles" }
    d.autoProvision = true
    if v, ok := cfg["auto_provision"].(bool); ok { d.autoProvision = v }

    raw, _ := cfg["trusted_proxies"].([]any)
    if len(raw) == 0 {
        return errors.New("proxyheader: trusted_proxies (CIDR list) is required for security")
    }
    for _, e := range raw {
        cidr, _ := e.(string)
        _, n, err := net.ParseCIDR(cidr)
        if err != nil {
            return errors.New("proxyheader: invalid CIDR: " + cidr)
        }
        d.trustedNets = append(d.trustedNets, n)
    }
    return nil
}

// Authenticate is invoked by the auth middleware. The exact signature must
// match the auth.Driver interface — check backend/internal/auth/driver.go.
func (d *Driver) Authenticate(ctx context.Context, r *http.Request) (auth.User, error) {
    // 1. RemoteAddr trusted CIDR'ler içinde mi?
    host, _, _ := net.SplitHostPort(r.RemoteAddr)
    ip := net.ParseIP(host)
    trusted := false
    for _, n := range d.trustedNets {
        if n.Contains(ip) { trusted = true; break }
    }
    if !trusted {
        return auth.User{}, auth.ErrUnauthorized
    }

    user := r.Header.Get(d.headerUser)
    if user == "" {
        return auth.User{}, auth.ErrUnauthorized
    }

    u := auth.User{
        Username: user,
        Email:    r.Header.Get(d.headerEmail),
        Name:     r.Header.Get(d.headerName),
    }
    if rolesStr := r.Header.Get(d.headerRoles); rolesStr != "" {
        for _, r := range strings.Split(rolesStr, ",") {
            r = strings.TrimSpace(r)
            if r != "" { u.Roles = append(u.Roles, r) }
        }
    }
    // auto_provision: DB lookup, yoksa create
    // (auth.Driver interface'i Authenticate dışında ekstra Provision callback verebilir)
    return u, nil
}
```

### Test
**Dosya:** `backend/internal/auth/drivers/proxyheader/proxyheader_test.go`

Senaryolar:
- `trusted_proxies` boş → Init error
- `trusted_proxies` invalid CIDR → Init error
- Untrusted IP'den header → 401
- Trusted IP, header yok → 401
- Trusted IP, header var → User dönüş
- `header_roles=admin,user` → Roles=["admin","user"]
- Auto-provision off + DB'de yok → 401

### Güvenlik notu
`trusted_proxies` ZORUNLU. Bu olmadan herhangi biri header injekte ederek admin olabilir. Test'te bu güvenlik kontrolünün varlığı şart.

### Blank import
`backend/cmd/filex/main.go`:
```go
import _ "gitlab.com/brftech/filemanager/backend/internal/auth/drivers/proxyheader"
```

### WIP durumu
Subagent yazmış. Kontrol:
```bash
ls backend/internal/auth/drivers/proxyheader/
go build ./internal/auth/drivers/proxyheader/...
go test ./internal/auth/drivers/proxyheader/...
```

---

## §2 — Role-based admin button

### Niye lazım
Burak: "rolünde admin varsa filemanager ui'da bir butona tıklar admin paneline geçer gibi". Özel `/admin` URL ya da hardcoded liste yerine, kullanıcının rolünden tetikleniyor.

### Tasarım

#### 2.1 Backend: `/api/me` endpoint

`auth.User` struct'ında `Roles []string` alanı var (yukarıdaki proxyheader örneğine bak; mevcut `User` struct'ına Roles eklenmeli).

```go
// backend/internal/api/handlers/me.go (yeni dosya)
func MeHandler(authSvc *auth.Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        user := authSvc.UserFromContext(r.Context())
        if user.ID == 0 {
            http.Error(w, "unauthorized", 401)
            return
        }
        json.NewEncoder(w).Encode(map[string]any{
            "id": user.ID,
            "username": user.Username,
            "email": user.Email,
            "name": user.Name,
            "roles": user.Roles,  // ["admin"] varsa admin
        })
    }
}
```

Route: `GET /api/me`. Auth middleware ile korumalı.

#### 2.2 Role belirleme

Üç kaynak:
1. **OIDC**: id_token claim'i `realm_access.roles` (Keycloak) veya `roles` (generic). OIDC driver'da claim parse edilip `User.Roles` doldurulmalı.
2. **Local user**: `users` tablosunda `role` enum kolonu (`admin`|`user`). Migration:
   ```sql
   ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'user';
   -- Veya çoklu rol için: ayrı user_roles(user_id, role) tablo. Basit için tek kolon yeterli.
   ```
3. **Basic env override**: `FILEMANAGER_ADMIN_USERS=burak@brf.sh,gokcil@brf.sh` (CSV). Auth middleware'de user.Email bu listede ise `User.Roles` += "admin".

#### 2.3 Frontend: FileExplorer toolbar admin butonu

**Paket:** `packages/core/src/FileExplorer.vue` (eğer mevcut paket çalışıyorsa) ya da **yeni paket** `web/src/views/Dashboard.vue` (admin web app'inin kendi içinde — bu zaten admin sayfası, gereksiz).

Asıl gereksinim **embed paket** içindeki FileExplorer için. `packages/core/src/...` altında ana component'i bul (mevcut paketten geçirilmiş olması lazım).

```vue
<script setup>
import { ref, onMounted } from 'vue';

const me = ref(null);
const isAdmin = computed(() => me.value?.roles?.includes('admin'));

onMounted(async () => {
  const res = await fetch(`${apiBase}/api/me`, { credentials: 'include' });
  if (res.ok) me.value = await res.json();
});

function openAdmin() {
  // Aynı origin altında /admin'e git
  window.open(`${apiBase}/admin`, '_blank');
}
</script>

<template>
  <Toolbar>
    <!-- Mevcut butonlar -->
    <button v-if="isAdmin" class="fe-toolbar-btn fe-toolbar-btn--admin"
            @click="openAdmin" title="Admin paneli">
      ⚙
    </button>
  </Toolbar>
</template>
```

#### 2.4 ENV referans

```
FILEMANAGER_ADMIN_USERS=burak@brf.sh,gokcil@brf.sh
```

### Tahmini efor
- `/api/me` endpoint: 1 saat
- OIDC roles claim parse: 1 saat
- Local user role kolonu + migration: 30 dk
- FileExplorer toolbar admin button: 30 dk
- Test (frontend + backend): 1 saat

**Toplam:** 3-4 saat
