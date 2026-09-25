# Microsoft Store listing — filex

What goes into Partner Center → the product → **Store listings** (one per
language) and **Submission options**. Kept here so a change to the product and a
change to what the Store says about it are reviewed together.

⚠ Every claim below is something the app does today. Before adding a feature
line, find it in `docs/DESKTOP.md`; before removing one, check the app still
does not do it. The first sentence of the description must say that filex
needs a server (Microsoft Store Policy 10.2.4: a product that depends on
another service says so up front).

Limits (Partner Center, MSIX): description ≤ 10,000 characters · "What's new"
≤ 1,500 · up to 20 product features of ≤ 200 characters · up to 7 search terms ·
screenshots PNG, 1366×768 or larger, at least one (four or more recommended).

---

## English (en-US)

**Product name:** filex

**Description**

filex connects to a filex server that you or your organisation run — it is the
desktop app for a self-hosted file manager, not a cloud service of its own.

Sign in through your browser, including single sign-on, and your server's
files are in a native window: browse, preview, rename, move, share and search
them across every storage your server connects to. Pick a folder on this
computer and filex keeps it in two-way sync with a folder on the server, in
the background, even when no window is open. Choose which server folders are
kept on this computer. Drag files out of the window straight onto your desktop
or into Explorer, and open Office documents with filex to edit them in your
server's document editor.

filex keeps your sign-in in Windows' secure storage and sends nothing about
you anywhere except the server you chose. It is open source (MIT); the server
is a single program you can run on your own machine, in Docker or on
Kubernetes — see filex.sh.

**Product features**

- Two-way folder sync with your filex server, running in the background
- Choose which server folders are kept on this computer
- Browser sign-in, including your organisation's single sign-on
- Several accounts and servers side by side
- Drag files and folders out onto the desktop or into Explorer
- "Open with filex" for Office documents, edited in your server's document editor
- Share links with a PIN, an expiry and a download limit
- Download and upload limits, and a time window for syncing
- Notifications for what happens on your server, in the tray
- Starts at sign-in if you want it to, quietly in the tray
- English and Turkish interface
- Open source (MIT); no telemetry, no ads

**What's new** — from `CHANGELOG.md` for the version being submitted, rewritten
for a person rather than a changelog reader. Store version ≠ app version: see
`desktop/scripts/appx-manifest.cjs`.

**Search terms:** file manager · file sync · self-hosted · WebDAV · SFTP · S3 · cloud storage

---

## Türkçe (tr-TR)

**Ürün adı:** filex

**Açıklama**

filex, sizin ya da kurumunuzun işlettiği bir filex sunucusuna bağlanır — kendi
başına bir bulut hizmeti değil, kendi sunucunuzda çalışan bir dosya
yöneticisinin masaüstü uygulamasıdır.

Tarayıcınızla (kurumunuzun tek oturum açma sistemi dahil) giriş yapın, sunucunuzdaki
dosyalar yerel bir pencerede karşınızda olsun: sunucunuzun bağlandığı her
depoda gezin, önizleyin, yeniden adlandırın, taşıyın, paylaşın ve arayın. Bu
bilgisayardan bir klasör seçin; filex onu sunucudaki bir klasörle iki yönlü
senkronda tutar — arka planda, hiçbir pencere açık değilken bile. Hangi sunucu
klasörlerinin bu bilgisayarda tutulacağını siz seçersiniz. Dosyaları
pencereden doğrudan masaüstüne ya da Dosya Gezgini'ne sürükleyin; Office
belgelerini filex ile açıp sunucunuzun belge düzenleyicisinde düzenleyin.

filex oturumunuzu Windows'un güvenli deposunda saklar ve seçtiğiniz sunucu
dışında hiçbir yere sizinle ilgili bilgi göndermez. Açık kaynaktır (MIT);
sunucu, kendi makinenizde, Docker'da ya da Kubernetes'te çalıştırabileceğiniz
tek bir programdır — ayrıntılar filex.sh'ta.

**Ürün özellikleri**

- filex sunucunuzla iki yönlü, arka planda çalışan klasör senkronu
- Hangi sunucu klasörlerinin bu bilgisayarda tutulacağını seçme
- Tarayıcıyla giriş; kurumunuzun tek oturum açma sistemi dahil
- Birden çok hesap ve sunucu yan yana
- Dosya ve klasörleri masaüstüne ya da Dosya Gezgini'ne sürükleyip bırakma
- Office belgeleri için "filex ile aç"; sunucunuzun belge düzenleyicisinde düzenleme
- PIN, süre sonu ve indirme sınırı olan paylaşım bağlantıları
- İndirme ve yükleme sınırları, senkron için zaman aralığı
- Sunucunuzda olanlar için tepside bildirimler
- İsterseniz oturum açılışında tepside sessizce başlar
- Türkçe ve İngilizce arayüz
- Açık kaynak (MIT); telemetri yok, reklam yok

**Arama terimleri:** dosya yöneticisi · dosya senkronu · kendi sunucun · WebDAV · SFTP · S3 · bulut depolama

---

## Shared fields

| Field | Value |
|---|---|
| Privacy policy URL | https://filex.sh/privacy/ |
| Website | https://filex.sh |
| Support contact | https://github.com/BRF-Tech/filex/issues |
| Category | Productivity (subcategory: none) |
| Copyright | © BRF Tech |
| Developed by | BRF Teknoloji |
| Store logo (1:1, 300×300) | `desktop/build/appx/LargeTile.png` is 310×310; export 300×300 from `build/icon.png` |
| Screenshots | EN listing: the English interface; TR listing: the Turkish interface. Taken from a signed-in desktop app against a demo server — file list, Settings → Sync folders, a share dialog, "Open with" in the editor window. The release screenshot rules apply (`docs/CONTRIBUTING.md` → Release process, step 2): current, and in the listing's language |

## Submission options

**Restricted capability — `runFullTrust`** (asked on upload; the box is short):

> Electron desktop app. It syncs folders the user picks by running the sync
> engine shipped in the package as a child process, and opens files the user
> drags out or asks to open.

**Notes for certification** (Policy 10.3: the tester needs a working server and
an account). Give the demo server address and a test account in this field in
Partner Center itself — never in this file or anywhere in the repository; the
account comes from the vault.

> filex is the client for a self-hosted server. Sign in: Connect → server
> address https://demo.filex.sh → the browser opens → sign in with the account
> below → the browser hands you back to the app. If the browser cannot hand
> back in your environment, the sign-in window asks "Browser couldn't return to
> the app? Paste the code it showed you" — paste the code the browser page
> shows.

**Age rating:** the IARC questionnaire in Partner Center. The honest answers:
no violence, no mature content; users can share files and links with other
people on the same server (user-generated content / sharing = yes); no
purchases; no location.
