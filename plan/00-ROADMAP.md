# filex v0.1.0 — Yol Haritası

> Yazılma tarihi: 2026-05-06 · Yazan: Claude (Opus 4.7) Coder workspace'inde
> Burak: lokal makinaya geçildiği için subagent'larla Go derleme bu workspace'i zorladı, plan dosyalara dökülüp push edildi
>
> **Önce oku:** `SPEC.md` (root) — tüm karar matrisi + mimari + ENV + endpoint kontratı

## Branch durumu

- `main` — phase-2 (cc65864 commit) + bu plan dosyaları
- WIP kodları (subagent çıktıları) bu commit'e dahil. Build edilebilirliği lokal'de doğrulanmalı.

## Bu commit'te ne var

1. **`SPEC.md`** — Tek dosyada tüm spec (370 satır)
2. **`plan/`** klasörü — Delta'lar için detaylı uygulama notları (bu dosyalar)
3. **WIP delta kodları** — subagent'lar bitirmeden Coder workspace'i durduğu için yarı doğrulanmış kodlar:
   - `backend/internal/storage/drivers/ftp/` (FTP driver)
   - `backend/internal/auth/drivers/proxyheader/` (Proxy-header auth driver)
   - `backend/internal/storage/validate.go` + `validate_test.go` (Root path guard)
   - `backend/internal/api/handlers/storages.go` (root guard çağrısı eklenmiş olabilir)
   - `backend/cmd/filex/main.go` (yeni driver blank import'ları eklenmiş olabilir)
   - `backend/internal/server/server.go` (?)
   - `backend/go.mod` + `backend/go.sum` (jlaffaye/ftp dep eklenmiş)
   - `deploy/` rename + içerik update (`files.brf.sh` → `demo-fm.brf.sh`)

   **Kontrol:** `cd backend && go build ./... && go test ./...` — lokal'de ilk iş

## Tamamlanan (mevcut iskelet — phase-2)

| # | Faz | Durum |
|---|-----|-------|
| 1 | Monorepo iskelet + core paket | ✅ (cc65864) |
| 2 | Web Component + React adapter | ✅ |
| 3a | Go backend iskelet + auth + storage | ✅ |
| 3b | API endpoint'leri | ✅ (audit gerek) |
| 3c | DB + sync worker (ETag + tombstone + fsnotify) | ✅ |
| 4 | Admin UI panel (mevcut sayfalar) | ✅ (delta sayfaları eksik) |
| 5 | Docker (slim + full) | ✅ |
| 6 | Docs (11 .md) | ✅ (delta'lar için update gerek) |
| 7 | GitLab CI/CD + goreleaser | ✅ |
| 11 | Δ Repo audit | ✅ |
| 27 | Δ files.brf.sh → demo-fm.brf.sh rename | ✅ |

## Bekleyen delta'lar (sıralama önerisi)

### Round A — küçük, paralel (hepsi WIP, lokal'de doğrula + commit)

| Delta | Dosya | Plan dosyası |
|-------|-------|--------------|
| FTP driver | `backend/internal/storage/drivers/ftp/` | `01-storage-deltas.md` §1 |
| Root path guard | `backend/internal/storage/validate.go` | `01-storage-deltas.md` §2 |
| Proxy-header auth | `backend/internal/auth/drivers/proxyheader/` | `02-auth-deltas.md` §1 |
| Demo URL rename | `deploy/`, `docs/DEPLOY_BRF.md` | tamamlandı (commit'te) |

### Round B — orta büyüklük, sıralı (yeni klasörler)

| # | Delta | Plan dosyası | Bağımlılık |
|---|-------|--------------|------------|
| 1 | Persistent queue (driver-based) | `03-queue.md` | (yok) |
| 2 | Notifications (webhook + in-app) | `04-notify.md` | DB tabloları |
| 3 | Replica storage layer | `05-replica.md` | Queue + Notify |

Round B'yi sırayla yapın çünkü Replica iki diğerini consume ediyor.

### Round C — frontend ağırlıklı

| Delta | Plan dosyası |
|-------|--------------|
| Role-based admin button | `02-auth-deltas.md` §2 |
| Storage prefix UI | `01-storage-deltas.md` §3 |
| Admin UI delta sayfaları (replica rules, replica failures, notifications, queue) | `06-admin-ui.md` |

### Round D — release

| Delta | Plan dosyası |
|-------|--------------|
| brf-mono entegrasyon (consumer #1) | `07-integration.md` §1 |
| fishapp PWA entegrasyon (consumer #2) | `07-integration.md` §2 |
| v0.1.0 tag + demo-fm.brf.sh deploy | `07-integration.md` §3 |

## Toplam efor tahmini (lokal'de full-time)

- Round A doğrulama + bug fix: 2-4 saat
- Round B: 2-3 gün
- Round C: 1 gün
- Round D: 0.5-1 gün

**Toplam:** 4-5 gün full-time, lokal'de.

## Subagent çıktısı — WIP durumu

Coder workspace'inde 6 subagent paralel çalıştırıldı. Tamamlananlar:

| Subagent | Durum | Çıktı |
|----------|-------|-------|
| Demo URL rename | ✅ Tamam (rapor alındı) | deploy/ + docs/ değişiklikleri commit'te |
| FTP driver | ⚠ Yarı tamam (rapor alınmadı, dosya var) | `backend/internal/storage/drivers/ftp/` |
| Proxy-header auth | ⚠ Yarı tamam (rapor alınmadı, dosya var) | `backend/internal/auth/drivers/proxyheader/` |
| Root path guard | ⚠ Yarı tamam (rapor alınmadı, dosya var) | `backend/internal/storage/validate*.go` |
| Persistent queue | ❌ Bilinmiyor (Coder durdu, output yok) | `backend/internal/queue/` muhtemelen yok |
| Notifications | ❌ Bilinmiyor (Coder durdu, output yok) | `backend/internal/notify/` muhtemelen yok |

`git status` ile ne geldiğini lokal'de doğrula. `go build ./...` çalışmıyorsa Round A doğrulama gerekir.

## Lokalde başlangıç adımları

```bash
git pull origin main
cd backend
go mod tidy
go build ./...
go test ./...
```

Build hatası varsa:
1. `git status` — son subagent kalıntılarını gör
2. Hatalı dosyayı (`backend/internal/...`) düzelt veya geri al
3. Plan dosyasındaki spec'e göre yeniden yaz

Round A doğrulandıktan sonra Round B'ye geç.
