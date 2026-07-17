# filex — E2E şifreli klasörler (tasarım + MVP referansı)

> **Durum:** MVP, v0.7.0 "Kimlik & Güven" dalgası (E2). İstemci tarafı
> WebCrypto — sunucuya **hiçbir anahtar/parola gitmez**, sunucu içeriği
> **okuyamaz**. Bu doküman tehdit modelini, anahtar yönetimini, dosya/marker
> formatlarını, özellik trade-off tablosunu ve v2 yol haritasını tanımlar.

---

## 1. Tehdit modeli

**Koruduğu şey:** klasör içindeki dosyaların **İÇERİĞİ** — sunucu diski,
S3 kovası, veritabanı yedeği, sunucu operatörü, sunucuya sızan bir saldırgan
ya da hukuki el koyma senaryosunda dosya içerikleri okunamaz. Şifreleme ve
çözme yalnız tarayıcıda (WebCrypto) yapılır; sunucu yalnız `filexe2e` magic'li
opak blob görür.

**Korumadığı şeyler (bilinçli MVP sınırları):**

| Sızıntı | Neden | Durum |
|---------|-------|-------|
| **Dosya/klasör ADLARI** | Adlar şifrelenmez (v2) | Sunucu ve listing'ler adları görür |
| Dosya boyutları (~yaklaşık) | Ciphertext ≈ plaintext + 97B header | Görünür |
| Klasör yapısı / dosya sayısı | Ağaç şifrelenmez | Görünür |
| Erişim zamanları / audit | Normal audit akışı sürer | Görünür |
| Aktif oturumdaki bellek | Anahtar RAM'de (yalnız oturum) | XSS/uzantı riski hosta ait |
| Sunucunun İSTEMCİYE verdiği JS | Kötü niyetli sunucu, kötü JS servis edebilir | Web tabanlı E2E'nin doğal sınırı |

**Güven varsayımı:** İstemci (tarayıcı + filex frontend bundle'ı) güvenilir.
Sunucu "honest-but-curious" (meraklı ama protokole uyan) kabul edilir — klasik
web E2E modeli (Proton/Bitwarden web istemcileriyle aynı sınıf).

## 2. Anahtar yönetimi

```
klasör parolası ──PBKDF2-SHA256 (600.000 iter, klasör-başına random 16B salt)──▶ KEK (AES-256-GCM)
                                                                                  │
dosya başına random 32B DEK (AES-256-GCM) ── içerik tek-shot şifreler ─┐          │
                                                                       │          │
DEK ◀── KEK ile AES-GCM wrap (dosya header'ında saklanır) ◀────────────┴──────────┘
```

- **KEK (klasör anahtarı):** `PBKDF2(parola, salt, iter=600000, SHA-256)` →
  AES-256-GCM anahtarı, `extractable=false` olarak import edilir. **Yalnız
  bellekte** yaşar (component state) — localStorage/sessionStorage/IndexedDB'ye
  ASLA yazılmaz. Sekme kapanınca / "Kilitle" aksiyonuyla / sayfa yenilenince
  gider.
- **DEK (dosya anahtarı):** her dosya için `crypto.getRandomValues(32)`. İçerik
  DEK ile AES-256-GCM tek-shot şifrelenir; DEK, KEK ile AES-GCM'de sarılıp
  (wrap) dosyanın kendi header'ına gömülür. Böylece **parola değişimi v2'de
  yalnız DEK'lerin yeniden sarılmasını gerektirir**, tüm içerik yeniden
  şifrelenmez.
- **Parola doğrulama:** marker'daki `verify` alanı = sabit metnin
  (`filex-e2e-verify-v1`) KEK ile şifreli hali. Yanlış parola → GCM tag
  doğrulaması patlar → "yanlış parola" hatası; sunucuya hiçbir doğrulama
  isteği gitmez.

### 2.1 Klasör marker'ı — `.filex-e2e.json`

Şifreli klasörün kökünde durur; **API listing'lerinde gizlenir** (`.filex-trash`
deseni), ama path ile `preview` üzerinden okunabilir (unlock bunun içeriğine
ihtiyaç duyar):

```json
{
  "v": 1,
  "salt": "<base64 16B>",
  "iter": 600000,
  "verify": "<base64: 12B IV || AES-GCM('filex-e2e-verify-v1')>"
}
```

Marker'da gizli hiçbir şey yoktur (salt + doğrulama blob'u publiktir); parola
olmadan işe yaramaz.

### 2.2 Şifreli dosya formatı — `filexe2e` magic

Her şifreli dosya, orijinal adıyla saklanır; içerik şu binary yapıdadır
(sabit 97 baytlık header + ciphertext):

| Offset | Uzunluk | Alan |
|--------|---------|------|
| 0 | 8 | Magic: ASCII `filexe2e` |
| 8 | 1 | Sürüm: `0x01` |
| 9 | 12 | wrapIV — DEK sarmalamasının GCM IV'ü |
| 21 | 48 | wrappedDEK — `AES-GCM(KEK, wrapIV, rawDEK)` (32B + 16B tag) |
| 69 | 12 | dataIV — içeriğin GCM IV'ü |
| 81 | 16 | rezerve (0x00; v2 chunk/metadata için) |
| 97 | n+16 | ciphertext — `AES-GCM(DEK, dataIV, içerik)` (+16B tag) |

Sunucu tarafı **yalnız magic prefix'i tanır** (thumb/çıkarım/convert skip); asla
çözemez.

### 2.3 KURTARMA YOK ⚠

**Parola kaybı = veri kaybı.** Tasarım gereği:

- Sunucuda parola/anahtar/kurtarma kodu YOKTUR.
- Admin dahil hiç kimse parolasız içerik çözemez.
- "Parolamı unuttum" akışı YOKTUR ve OLMAYACAKTIR (varlığı sunucuya güveni geri
  getirir, tüm modeli anlamsızlaştırır).

UI, klasör oluşturma modalında bu uyarıyı açıkça gösterir ve onay ister.

## 3. Özellik trade-off tablosu

Sunucu içeriği okuyamadığı için içerik-bilgisi gerektiren her sunucu-tarafı
özellik şifreli klasörde **çalışmaz** ya da kısıtlıdır:

| Özellik | Şifreli klasörde davranış |
|---------|---------------------------|
| **Ad araması** | ÇALIŞIR (adlar şifrelenmez — bilinçli sızıntı, §1) |
| **İçerik araması** ("Bul") | ÇALIŞMAZ — backend, marker'lı ağaç + magic'li dosyada içerik çıkarımını atlar (CPU israfı + endeks sızıntısı önlemi). İçerik endekslenmez → içerik aramada hit çıkmaz |
| **Thumbnail** | ÜRETİLMEZ — thumb pipeline marker'lı ağaç altını `skipped` işaretler; grid/galeri ikon gösterir |
| **Önizleme (text + görsel + medya + PDF)** | ÇALIŞIR (kilit açıkken) — istemci indirir, çözer, blob URL ile mevcut viewer'lara verir |
| **Metin düzenleme / kaydetme** | MVP'de KAPALI — önizleme salt-okunur; save-text plaintext'i sunucuya yazacağı için engellenir (v2) |
| **OnlyOffice** | KAPALI — sunucu doc'u DS'e verirken içeriği okumak zorunda; backend config endpoint'i magic'li dosyada **415** döner, UI da OnlyOffice'i devre dışı bırakır |
| **Convert** | KAPALI — UI aksiyonu gizler; magic'li içerik convert servisine anlamsız |
| **Paylaşım (public link) / Dosya İste** | MVP'de KAPALI (UI gizler) — link alıcısı ciphertext indirirdi. v2: parola-taşıyan şifreli paylaşım |
| **DAV / CLI / ShareX / AI (REST+MCP) okuma** | Ciphertext döner (magic'li) — bu yüzeyler çözemez |
| **DAV / CLI / AI YAZMA** ⚠ | Şifrelemez! filex-dışı yüzeyden şifreli klasöre yazılan dosya **düz metin** kalır (sunucu şifreleyemez — anahtarı yok). Kural: şifreli klasöre yalnız filex web arayüzünden yükleyin |
| **Sürümler (versioning)** | ÇALIŞIR — sürümler ciphertext saklar; geri yükleme aynı klasör parolasıyla çözülür |
| **Çöp / geri yükleme** | ÇALIŞIR — içerik dokunulmaz |
| **ClamAV** | Ciphertext tarar → etkisiz (zararsız no-op). Belgelendi |
| **Kopyala/taşı** | ÇALIŞIR (bayt-bayt) — ama şifreli klasör DIŞINA taşınan dosya şifreli kalır ve UI orada çözemez (marker yok). v2: taşımada şeffaf re-encrypt |
| **Yükleme boyutu** | MVP tek-shot bellek şifrelemesi: **200 MB üstü reddedilir** (uyarı). v2: streaming chunk |

## 4. UI akışları (MVP)

- **Oluşturma:** Yeni Klasör modalında "🔒 Şifreli klasör" seçeneği →
  `EncryptedFolderModal` (ad + parola ×2, ≥8 karakter + GERİ DÖNÜŞSÜZ uyarısı
  onay kutusu). Akış: `newfolder` → marker üret → `.filex-e2e.json`'u klasöre
  yükle.
- **Rozet:** listing'de şifreli klasör satırı 🔒 ikonuyla çizilir (backend dir
  satırına `e2e:true` işler).
- **Kilit ekranı:** şifreli klasöre (ya da alt klasörüne) girince listing yerine
  parola ekranı. Backend listing yanıtına `e2e_root` (şifreli kökün yolu)
  ekler; frontend marker'ı kökten çeker, parolayı marker'a karşı doğrular.
  Yanlış parola → i18n hata. Doğru parola → KEK bellekte, listing açılır.
- **Kilit açıkken:** başlık şeridinde 🔒 + "Kilitle" düğmesi. Kilitle =
  bellekteki KEK atılır → tekrar parola sorulur.
- **Yükleme:** kilit açıkken upload şeffaf şifrelenir (dosya → ArrayBuffer →
  şifrele → aynı adla yükle). 200 MB üstü reddedilir.
- **İndirme / önizleme:** şeffaf çözülür — indirilen bayt çözülüp orijinal adla
  kaydedilir; önizleme çözülmüş blob URL ile mevcut viewer'lara verilir.

## 5. Sunucu tarafı dokunuşlar (MINIMAL)

Sunucu şifreden habersizdir; yalnız üç yerde marker/magic **farkındalığı** var:

1. `internal/e2e` — marker adı + magic sabitleri + `UnderEncrypted()` (bir
   node yolunun atalarında `.filex-e2e.json` node'u var mı; pathHash lookup)
   + `HasMagicPrefix()`.
2. **Thumb pipeline** (`internal/thumb/pipeline.go`): marker'lı ağaç altındaki
   dosyada üretim `skipped`.
3. **İçerik çıkarımı** (`internal/queue/content_index.go`): marker'lı ağaç
   altı + magic'li içerik → içerik endekslenmez (boş içerik + fingerprint,
   yeniden kuyruklanmaz).
4. **OnlyOffice config** (`internal/api/handlers/onlyoffice.go`): magic sniff →
   `415 file is e2e-encrypted`.
5. **Manager listing** (`internal/api/handlers/manager.go`): marker satırı
   gizlenir; yanıt `e2e:true` (klasör içi) + `e2e_root` (alt ağaç) + dir
   satırlarına `e2e:true` rozet alanı işler.

Şifreleme/çözme sunucuda YOKTUR; bu dokunuşların hepsi "işe yaramaz işi yapma
+ sızıntı riskini kapat" kategorisindedir.

## 6. v2 yol haritası

1. **Ad şifreleme** — dosya/klasör adları da şifreli (sunucuda opak ad + yerel
   ad haritası dosyası); listing UI çözülmüş ad gösterir.
2. **Şifreli paylaşım** — link URL fragment'ında (`#k=…`, sunucuya gitmez)
   anahtar taşıyan public paylaşım; alıcı tarayıcıda çözer.
3. **Streaming chunk şifreleme** — 200 MB sınırını kaldırmak için
   chunk-per-GCM (64 MB dilim + dilim sayacı IV'e karılır; header rezerve
   alanı bunun için ayrıldı).
4. **Parola değiştirme** — DEK'ler yeni KEK ile yeniden sarılır (içerik
   yeniden şifrelenmez); marker verify güncellenir.
5. **Şifreli klasörde düzenleme** — text editör kaydı istemcide şifrelenip
   upload yoluyla yazılır (save-text yerine).
6. **Sunucu tarafı yazma reddi (opsiyonel policy)** — marker'lı ağaç altına
   filex-dışı yüzeylerden (DAV/CLI/AI) düz metin yazımını reddeden opsiyonel
   guard.
