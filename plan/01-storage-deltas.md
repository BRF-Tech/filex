# Δ Storage delta'ları

> Üç madde: FTP driver, root path guard, storage prefix UI.
> SPEC.md §4.5 (root guard) ve §5 (driver matrisi) referans.

---

## §1 — FTP driver

### Niye lazım
SFTP zaten var (`backend/internal/storage/drivers/sftp/`). FTP plain text protokol — yaygın legacy use case (eski hosting paneller, basit web FTP). Burak: "biri sshftp biri direk ftp".

### Tasarım
Mevcut SFTP driver pattern'ı bire bir kopya.

**Dosya:** `backend/internal/storage/drivers/ftp/ftp.go`

```go
package ftp

import (
    "context"
    "errors"
    "fmt"
    "io"
    "net"
    "path"
    "strings"
    "sync"
    "time"

    ftplib "github.com/jlaffaye/ftp"
    "gitlab.com/brftech/filemanager/backend/internal/storage"
)

func init() {
    storage.Register("ftp", func() storage.Driver { return &Driver{} })
}

type Driver struct {
    host     string
    port     int
    user     string
    password string
    root     string
    tls      bool   // FTPS explicit
    passive  bool   // default true

    mu     sync.Mutex
    client *ftplib.ServerConn
}

func (d *Driver) Name() string { return "ftp" }

func (d *Driver) Init(_ context.Context, cfg map[string]any) error {
    d.host, _ = cfg["host"].(string)
    if v, ok := cfg["port"].(int); ok { d.port = v }
    if d.port == 0 { d.port = 21 }
    d.user, _ = cfg["user"].(string)
    d.password, _ = cfg["password"].(string)
    d.root, _ = cfg["root"].(string)
    if d.root == "" { d.root = "/" }
    d.tls, _ = cfg["tls"].(bool)
    if v, ok := cfg["passive"].(bool); ok { d.passive = v } else { d.passive = true }
    if d.host == "" || d.user == "" || d.password == "" {
        return errors.New("ftp: host, user, password required")
    }
    return nil
}

func (d *Driver) Capabilities() storage.Capabilities {
    return storage.Capabilities{
        Read: true, Write: true, Move: true, Copy: true,
        Delete: true, Mkdir: true,
    }
}

// dial reuses connection; redial-on-error.
func (d *Driver) dial(ctx context.Context) (*ftplib.ServerConn, error) {
    d.mu.Lock(); defer d.mu.Unlock()
    if d.client != nil {
        if err := d.client.NoOp(); err == nil { return d.client, nil }
        d.client.Quit() // dead — reconnect
        d.client = nil
    }
    addr := net.JoinHostPort(d.host, fmt.Sprintf("%d", d.port))
    opts := []ftplib.DialOption{
        ftplib.DialWithTimeout(10 * time.Second),
        ftplib.DialWithContext(ctx),
    }
    if d.tls {
        opts = append(opts, ftplib.DialWithExplicitTLS(nil))
    }
    if !d.passive {
        // jlaffaye/ftp default'u passive; aktif desteklenmiyor — log uyarısı
    }
    c, err := ftplib.Dial(addr, opts...)
    if err != nil { return nil, err }
    if err := c.Login(d.user, d.password); err != nil {
        c.Quit()
        return nil, err
    }
    d.client = c
    return c, nil
}

func (d *Driver) full(p string) string {
    return path.Join(d.root, strings.TrimPrefix(p, "/"))
}

// List, Stat, Read, Write, Move, Copy, Delete, Mkdir methodları —
// FTP komutları (LIST/MLSD, RETR, STOR, RNFR/RNTO, DELE, MKD, RMD).
// 550 hatasını storage.ErrNotFound'a map et.
// ... (detay implementasyon, jlaffaye/ftp API'sine göre)
```

### Test
**Dosya:** `backend/internal/storage/drivers/ftp/ftp_test.go`

- Init validation (host, user, password zorunlu)
- Capabilities() doğru (Read/Write/Move/Copy/Delete/Mkdir true; Presign/Multipart/Watch false)
- Live test ENV ile opsiyonel (FTP_TEST_HOST varsa). Yoksa skip.

### Bağımlılık
```bash
cd backend
go get github.com/jlaffaye/ftp@latest
go mod tidy
```

### Blank import
`backend/cmd/filex/main.go` veya driver auto-register dosyasında:
```go
import _ "gitlab.com/brftech/filemanager/backend/internal/storage/drivers/ftp"
```

### WIP durumu
Subagent başlattı. `backend/internal/storage/drivers/ftp/` klasörü mevcut commit'te bulunmalı. Lokal'de:
```bash
ls backend/internal/storage/drivers/ftp/
go build ./internal/storage/drivers/ftp/...
go test ./internal/storage/drivers/ftp/...
```

---

## §2 — Storage root path guard

### Niye lazım
Burak: "primaryde de replicada da root klasör olamaz alt ve boş bir klasörde olmalı tüm filemanager yönetimi"

Storage'ın root'u (`/`, boş, veya yalnız `/` whitespace) yasak. Yoksa filemanager bucket'ın tamamını kendi alanı sayar — mevcut dosyalar gölgede kalır, yanlışlıkla silinebilir.

### Tasarım
**Dosya:** `backend/internal/storage/validate.go`

```go
package storage

import (
    "errors"
    "strings"
)

// ErrRootPathForbidden returned when a storage prefix/path is empty or root.
var ErrRootPathForbidden = errors.New("ROOT_PATH_FORBIDDEN: storage prefix/path cannot be empty or root '/'; use a sub-folder like 'fileman' or 'data/files'")

// ValidateNonRootPath checks the storage config has a non-root path.
// Driver-specific keys: s3=prefix, local=path|root, ftp/sftp/webdav=root|remote_path.
func ValidateNonRootPath(driver string, cfg map[string]any) error {
    p := extractPath(driver, cfg)
    p = strings.Trim(p, "/ \t")
    if p == "" {
        return ErrRootPathForbidden
    }
    return nil
}

func extractPath(driver string, cfg map[string]any) string {
    switch driver {
    case "s3":
        if v, _ := cfg["prefix"].(string); v != "" { return v }
    case "local":
        if v, _ := cfg["path"].(string); v != "" { return v }
        if v, _ := cfg["root"].(string); v != "" { return v }
    case "ftp", "sftp", "webdav":
        if v, _ := cfg["root"].(string); v != "" { return v }
        if v, _ := cfg["remote_path"].(string); v != "" { return v }
    }
    // Generic fallback
    for _, k := range []string{"prefix", "path", "root", "remote_path"} {
        if v, ok := cfg[k].(string); ok && v != "" { return v }
    }
    return ""
}
```

### Test
**Dosya:** `backend/internal/storage/validate_test.go`

| cfg | beklenen |
|-----|----------|
| `{prefix: ""}` | ErrRootPathForbidden |
| `{prefix: "/"}` | ErrRootPathForbidden |
| `{prefix: "  "}` | ErrRootPathForbidden |
| `{prefix: "//"}` | ErrRootPathForbidden |
| `{prefix: "fileman"}` | nil |
| `{prefix: "/fileman/"}` | nil (trim sonrası "fileman") |
| `{prefix: "data/files"}` | nil |
| `{path: ""} (local)` | ErrRootPathForbidden |
| `{path: "/var/data"} (local)` | nil |

### Endpoint çağrısı
Storage create/update handler'ında (büyük olasılıkla `backend/internal/api/handlers/storages.go`):

```go
// Driver + cfg parse'ladıktan sonra, DB insert öncesi:
if err := storage.ValidateNonRootPath(driver, cfgMap); err != nil {
    http.Error(w, err.Error(), http.StatusBadRequest)
    return
}
```

### Driver Init defansif (opsiyonel, V2)
Driver'ların kendi Init()'lerinde `root == ""` veya `root == "/"` ise default değer (örneğin "/") kullanmak yerine reddetmek. Ama API katmanında validation yeterli.

### WIP durumu
Subagent dosyaları yazmış. Kontrol:
```bash
ls backend/internal/storage/validate*.go
go test ./internal/storage/...
git diff backend/internal/api/handlers/storages.go  # validation çağrısı eklenmiş mi
```

---

## §3 — Storage prefix UI

### Niye lazım
S3 driver backend'de prefix destekliyor (`backend/internal/storage/drivers/s3/s3.go:36`). Ama Admin UI'da Storage create/edit form'unda `prefix` alanı yok. Burak: "fileman istersem filemanv2 belirleyip istediğim yerde filemanager çalıştırmış olurum".

### Tasarım

**Frontend:** `web/src/views/StorageNew.vue` ve `web/src/views/StorageEdit.vue`.

Mevcut form alanlarını incele (her driver için config alanları farklı). S3 için `endpoint, bucket, region, access_key, secret_key, path_style` muhtemelen var; `prefix` alanı eklenecek.

```vue
<!-- StorageNew.vue (S3 sekmesi içinde) -->
<label>
  Prefix <span class="required">*</span>
  <input v-model="form.config.prefix" placeholder="fileman" required>
  <small>Bucket içinde kullanılacak alt klasör. Root ('/' veya boş) yasak.</small>
</label>
```

**Diğer driver'lar:**
- Local: `path` alanı zaten var (driver Init'i kullanıyor) — required işaretle
- FTP/SFTP/WebDAV: `root` alanı (mevcut form'a bak)

**Validation:** Backend'deki `ValidateNonRootPath` zaten hata dönecek; frontend'de aynı kontrolü ekle (Backend hata mesajını göster).

### Pinia store
`web/src/stores/storages.ts` → `createStorage(payload)` payload'ında `config.prefix` (ya da driver'a göre `path`/`root`) include edilmeli.

### Tests
`web/tests/stores/storages.test.ts`'e:
- Boş prefix ile create → backend 400 → toast error
- Geçerli prefix → 201 → liste yenile

### Bağımlılık
- §2 root guard (backend) önce gelmeli
- Frontend tarafından bağımsız çalışılabilir

### Tahmini efor
1-2 saat (mevcut form'a bir alan eklemek + validation + test)
