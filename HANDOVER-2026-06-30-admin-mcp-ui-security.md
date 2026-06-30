# HANDOVER — filex admin MCP/REST + güvenlik + admin UI (v0.1.35)

**Tarih:** 2026-06-30 (gece, otonom çalışma) · **Branch→main:** `feat/admin-mcp-fixes` → `main` (ff) · **Tag:** `v0.1.35` · **Deploy:** demo-fm + fm (filex-standalone)

Burak: "API + MCP olayı, admin panelde JSON yerine inputlar, github/brftech sponsor linkini kaldır, support kaldır, tüm uygulamayı test et + mock data temizliği, handover çıkar, versiyonla, demo-fm + fm'e deploy." Soru sormadan, inisiyatifle bitirildi. Sabah review için bu doküman.

---

## 0) ÖNEMLİ KEŞİF — bayat taban

Lokal `G:\filex` checkout'u **19 commit geride** (`8dc6bfa`) idi; origin/main `d8691d9`. İlk başlattığım MCP ajanı bu yüzden `/api/ai` (token-auth AI REST + MCP) yüzeyini **göremedi** ve yanlışlıkla ayrı bir `mcpserver` paketi (mark3labs) yazdı → **çöpe atıldı** (`wip/mcp-ui-stale-base` branch'inde duruyor, kullanılmadı). Doğru tabana (`origin/main`) geçildi; işin tamamı oradan yeniden yapıldı. **Sonuç:** dosya işlemleri AI REST + MCP ZATEN ÜRÜNDE VARDI (`handlers/ai*.go`, resmi `modelcontextprotocol/go-sdk`, 8 dosya tool'u); eksik olan **admin paneli** idi → eklendi.

---

## 1) Admin MCP / REST genişletme (ana iş)

Mevcut `ai_mcp.go` (resmi SDK) + `apitoken` auth GENİŞLETİLDİ — ayrı server kurulmadı:

- **Yeni scope `admin`** (`auth/drivers/apitoken/apitoken.go`: `ScopeRead/Write/Delete/MCP/Admin` + `IsValidScope`; `ai_tokens_admin.go` token üretiminde geçersiz scope'u 400 ile reddeder).
- **Admin REST:** `/api/ai/admin/*`, `RequireScope("admin")` + admin-elevation. dashboard, settings, users, storages, sync-runs, shares, trash, search, auth-providers, external, replica (rules/settings/targets/failures/report), replication-targets, queue, notifications, audit. (Tam yol listesi: `ai_admin.go::Register`.)
- **MCP admin tool'ları:** 59 adet, namespace `admin_*` (`ai_admin.go::registerAdminTools`); MCP server'a **yalnız token `admin` scope'una sahipse** register edilir (`ai_mcp.go::getServer`).
- **Yetki:** RequireAdmin-sarmalı HTTP route'u DEĞİL, admin handler mantığı **in-process** çağrılıyor; `elevatedPrincipal` rolü admin'e çeker ama **gerçek user ID'yi korur** (audit/FK güvenli). MCP'de sentetik `*http.Request` + `bufRecorder` ile handler doğrudan sürülür (soket yok, kod tekrarı yok).

**Kullanım (admin-scope token ile):**
```bash
# admin (+mcp) scope'lu token üret (admin session cookie ile):
curl -s -X POST https://fm.brf.sh/api/admin/ai-tokens -H 'Content-Type: application/json' \
  --cookie "<admin-session>" -d '{"label":"ai-admin","scopes":"mcp,admin"}'   # plaintext token BİR KEZ döner
# Claude'a ekle:
claude mcp add --transport http filex-admin https://fm.brf.sh/api/ai/mcp \
  --header "X-Filex-Token: <PLAINTEXT_TOKEN>"
```
Dosya tool'ları için `mcp`, admin tool'ları için ek `admin` scope gerekir. `Authorization: Bearer <token>` de kabul edilir.

---

## 2) Admin UI düzeltmeleri (Burak'ın gösterdiği)

- **Ayarlar 405 fix** (`settings.go`): "Kaydet" → `PATCH /api/admin/settings` 405 dönüyordu (route'ta sadece GET + PUT/{key} vardı). **Bulk `Update` handler + `r.Patch("/", seth.Update)`** eklendi. Aynı taşla **secret redaksiyon**: `List`/`Update` artık `auth.<provider>.client_secret`/`bind_password` gibi sırları `***` ile maskeliyor (eskiden admin GET'i tüm sağlayıcı sırlarını DÜZ METİN döndürüyordu).
- **Kimlik sağlayıcılar: JSON → yapılandırılmış input'lar** (`AuthProviders.vue` baştan yazıldı): her sağlayıcı ham `{}` textarea yerine alanlara sahip (OIDC: issuer/client_id/client_secret🔒/redirect_url/scopes/role_claim/admin_group; LDAP: url/base_dn/bind_dn/bind_password🔒/user_filter/email_attr/start_tls; proxy-header: trusted_proxies/header_*/admin_role/auto_provision; local & api-token: config yok → "sadece aç/kapat"). Secret'lar maskeli + "boş bırakırsan korunur" (`***` geri yazılmaz). Bilinmeyen sağlayıcı için JSON fallback korundu. i18n: eksik `api-token` anahtarı + tüm alan etiketleri (tr+en) eklendi.
- **Settings.vue save scope**: artık tüm settings map'ini değil yalnız 6 yönetilen anahtarı PATCH'liyor (auth.* sırlarını geri yazmaz).
- **About.vue**: `github.com/sponsors/brftech` (Bağış/Support) linki KALDIRILDI (başkasına ait GitHub + support istenmedi). Kalan linkler GitLab (bizim).

---

## 3) Kritik güvenlik fix'leri (denetimde çıktı, hepsi current base'de doğrulandı + düzeltildi)

- **TOTP/2FA sahteydi** (`auth_self.go`): `verifyTOTP` herhangi 6 haneyi geçiriyordu. Artık gerçek `totp.Validate` (pquerna/otp), gerçek QR (skip2/go-qrcode), **login enforcement** (`auth.go`: TOTP açık kullanıcı geçerli kod olmadan giremiyor; yanlış kodda session revoke). **postgres yan-bug:** `totp_enabled` hiç yüklenmiyordu → postgres'te enforcement bypass olurdu; tüm user query'leri ortak `userCols`'a alındı.
- **Parola değiştirme**: backend artık `old_password` VE `current_password` ikisini de kabul ediyor (frontend `current_password` gönderiyordu → kırıktı, şimdi çalışıyor).
- **Son admin / kendini silme**: `users.go` Delete koşulsuz siliyordu (lockout riski). Guard: son admin/kendini silme **409**; son admin'in rolünü düşürme **409**; geçersiz rol **400**.
- **`display_name` hayalet alandı**: frontend kullanıyordu ama model/store/migration'da yoktu → sessizce kayboluyordu. Uçtan uca eklendi + migration `00011_user_display_name.sql` (sqlite/postgres/mysql).
- **SFTP host key doğrulaması yoktu** (`sftp.go` `InsecureIgnoreHostKey`): TOFU + known_hosts/pinned-key seçenekleri eklendi (varsayılan: ilk bağlantıda öğren, sonra MITM reddet).
- **Audit log boştu** (canlı testte yakalandı): kök neden = `auth.AuditMiddleware` tanımlı ama **hiçbir yere `r.Use()` ile bağlı değildi**. Admin + authenticated gruplara bağlandı + `actionFor`'a generic `/api/admin/*` fallback eklendi (replica/targets vb. mutasyonlar artık audit'leniyor).

---

## 4) Canlı test bulguları (deploy öncesi v0.1.34 panelinde)

17 admin sayfası tek tek gezildi. **Hiçbir sayfada uydurma/mock data YOK** — hepsi gerçek DB/servis verisi. Sorunlar "eksik/bozuk davranış" tipindeydi (yukarıda düzeltildi). Ek küçük gözlemler:
- Bildirimler: **416 okunmamış** = saatlik `replica_status_report` cron'u "0 failure" olsa bile her saat broadcast bildirim üretiyor + "no webhook URL" ile geçiyor → çan spam'i. (DÜZELTİLMEDİ — aşağıda açık iş.)
- Kullanıcılar: "Last login" kolon başlığı çevrilmemiş (i18n, küçük); "Şifre sıfırlandı" buton etiketi (reset success string'i buton aria'sında — küçük).
- Dashboard "Kuyruk derinliği 1" ↔ Kuyruk sayfası 0 (küçük tutarsızlık).

---

## 5) BİLİNÇLİ ATLANANLAR / AÇIK İŞLER (sabah karar)

1. **External servis secret'ları hâlâ DÜZ METİN at-rest** (`external_admin.go` `// TODO: encrypt`). Kod tabanında master-key altyapısı YOK; güvensiz ad-hoc kripto icat etmemek için dokunulmadı. Önce env/config master-key + envelope-encrypt katmanı gerekir.
2. **AI-admin mutasyonları (`/api/ai/admin/*`) audit'e düşmüyor** (`shouldAudit` yalnız `/api/admin/` prefix'ine bakıyor). Bilinçli bırakıldı.
3. **Bildirim spam'i** (416 unread replica cron) — replica cron'u "nothing to report + no webhook" durumunda bildirim üretmemeli veya unread saymamalı. Düzeltilmedi.
4. **MCP'de sarılmayan uçlar**: quota (`/api/admin/quota/*`), versions hard-delete, birkaç replica alt-aksiyonu (fix/fix-one ayrı tool değil). İstenirse eklenir.
5. **Dashboard depo kartları** (denetim notu, stale-base): nested `stats` okumama + `last_sync_state` boş gelme — current base'de tekrar doğrulanmalı (model değişmişti). Düzeltilmedi; düşük öncelik.

---

## 6) Sürüm / deploy

- **v0.1.35** tag'lendi; `packages/{core,webcomponent,react}` 0.1.34→0.1.35; main'e ff-merge + push (CI release tetiklendi).
- Image `filex:v0.1.35` main'de build edildi; **demo-fm** (`/root/filex`, container `filex`, :5212) ve **fm** (`/root/filex-standalone`, :5213) güncellendi. Eski `filex:v0.1.34` rollback için duruyor.
- Migration `00011` container restart'ta goose ile otomatik uygulanır (postgres). Deploy sonrası login + Ayarlar kaydet + auth-providers smoke test edildi.

**Rollback (gerekirse):** ilgili compose'da `image: filex:v0.1.35` → `filex:v0.1.34`, `docker compose up -d`.

---

## 7) Test durumu

- Backend: `go build ./...` ✓, `go vet ./...` ✓, `go test ./internal/api/... ./internal/auth/... ./internal/db/drivers/sqlite/...` ✓ (yeni: TOTP, login-enforcement, users-admin, ai-admin testleri).
- Frontend: `vue-tsc --noEmit` ✓, `vite build` ✓.
- (gofmt -l working-tree'de CRLF nedeniyle bazı dosyaları işaretliyor — git LF normalize ediyor, committed blob temiz.)
