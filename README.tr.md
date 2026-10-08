<!-- Translated from README.md as of bf857aad (v0.51.0). The English README is the source: change it first, then carry the change here. -->

<div align="center">

<img src="docs/logo.png" alt="filex logosu" width="96">

# filex - her yere gömülebilen, kendi sunucunuzda çalışan dosya yöneticisi

[![Release](https://img.shields.io/github/v/release/BRF-Tech/filex?color=2f6ceb)](https://github.com/BRF-Tech/filex/releases)
[![CI](https://img.shields.io/github/actions/workflow/status/BRF-Tech/filex/ci.yml?branch=main&label=ci)](https://github.com/BRF-Tech/filex/actions)
[![License: MIT](https://img.shields.io/github/license/BRF-Tech/filex?color=22c55e)](LICENSE)
[![Container](https://img.shields.io/badge/ghcr.io-brf--tech%2Ffilex-2496ed?logo=docker&logoColor=white)](https://github.com/BRF-Tech/filex/pkgs/container/filex)
[![Live demo](https://img.shields.io/badge/live_demo-demo.filex.sh-f59e0b)](https://demo.filex.sh)

[English](README.md) · **Türkçe** · [Deutsch](README.de.md) · [Español](README.es.md) · [Français](README.fr.md) · [简体中文](README.zh-CN.md)

<sub>Bu sayfa, [İngilizce README](README.md) dosyasının v0.51.0 sürümündeki hâlinin çevirisidir; ikisi arasında fark olduğunda İngilizce metin geçerlidir. Bağlantı verdiği belgeler İngilizcedir.</sub>

Tam donanımlı bir web arayüzü olan tek bir Go ikili dosyası (binary), takılabilir
depolama/kimlik doğrulama/veritabanı sürücüleri, **gerçek zamanlı iş birliği**,
**gömülebilir bir web bileşeni**, **klasör eşitlemesi canlı çalışan bir masaüstü
uygulaması** - iki taraftaki değişiklik yaklaşık bir saniyede karşıya geçer - yapay
zekâ ajanlarının onu doğrudan kullanabilmesi için **yerleşik bir MCP sunucusu**, bir de
**uygulamalar**: ona dosyalarla yapılacak yeni işler öğreten eklentiler - yalıtılmış
bir WebAssembly modülü, yalıtılmış bir çerçevede kendi arayüzü ya da ikisi birden -
bu işlerin ilki, kurumunuzun içindeki ve dışındaki kişilerle **belge imzalama**.
**Dil paketi** de bir uygulamadır; böylece filex yeni bir sürüm beklemeden
çevrilebilir - ve sağdan sola okunan dillerde kendini **sağdan sola** yerleştirir.
Herkes zaten sahip olduğu hesapla oturum açar - SSO, LDAP ya da filex'in çalıştığı
makinedeki **Windows ya da Linux hesabı** - ve çok kiracılı bir kurulumda
**her kiracı kendini yönetir**: kendi oturum açma sağlayıcıları, kendi alan adı
ve sertifikası.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://filex.sh/shots/explorer-grid-dark.5ddafe2dac64.png">
  <img src="https://filex.sh/shots/explorer-grid-light.484fb070ca19.png" alt="filex gezgini - küçük resim ızgarası" width="900">
</picture>

</div>

## Hemen deneyin

**Canlı demo:** [demo.filex.sh](https://demo.filex.sh) - `demo@demo.com` / `demo` ile oturum
açın (yönetici rolü, deneme alanı her gece sıfırlanır). Ya da kendi kurulumunuzu çalıştırın:

```bash
docker run -p 5212:5212 \
  -e FILEX_DEFAULT_STORAGE_DRIVER=local -e FILEX_DEFAULT_STORAGE_PATH=/srv/files \
  -v filex-data:/data -v "$PWD:/srv/files" \
  ghcr.io/brf-tech/filex:latest
```

Bu komut **çalıştırdığınız klasörü** sunar - arayüzü açın, dosyalarınız zaten orada.
`/data`, filex'in kendi dizinidir (SQLite veritabanı, arama indeksi, küçük resim önbelleği),
bu yüzden dosya bıraktığınız klasör değil, adlandırılmış bir birimdir; ikisi bilerek ayrı
tutulur. `$PWD` yerine başka bir yer gösterin ya da sonradan yönetim panelinden başka depolar
ekleyin - birkaç üst düzey klasörü olan bir kova (bucket), klasör başına bir depo olarak
tek seferde bağlanabilir (*Depolar → Depo ekle → Birden çok klasörü tek seferde bağla*).

Varsayılan olarak konteyner **root** olarak çalışır, bu yüzden `/data` dizinine yazdıkları
root'a aittir; kendi kullanıcınızla çalıştırmak için `PUID`/`PGID` değişkenlerini ayarlayın
([docs/DOCKER.md](docs/DOCKER.md#which-user-the-container-runs-as)).

http://localhost:5212/admin adresini açın - ilk çalıştırmada yönetici kimlik bilgileri ve
gömme talimatları konsola yazılır. Bu URL işletmecinindir; hesap açtığınız kişilerin adresi
ise **http://localhost:5212/drive**, yani çevresinde panel olmayan aynı dosya yöneticisidir.

Tarayıcı sekmesi yerine bir pencere mi istersiniz? **Masaüstü uygulaması** (Windows / Linux /
macOS) herhangi bir filex sunucusunda oturum açar ve klasörleri arka planda eşitler - ve her
platformda **kurulmadan** çalışan bir kopyası var (taşınabilir bir `.exe`, bir AppImage,
bir `.zip`). [Microsoft Store](https://apps.microsoft.com/detail/9PKXDJLVZWXW),
[Snap Store](https://snapcraft.io/filex-app) ya da
[son sürüm](https://github.com/BRF-Tech/filex/releases/latest) sayfasından edinin -
[docs/DESKTOP.md](docs/DESKTOP.md).

## Neden filex

Kendi sunucunuzda çalışan dosya yöneticilerinin çoğu ya **fazla küçük** (yükleme
yapılabilen bir dizin listesi) ya da **fazla büyük** (dosya sekmesi için kurduğunuz koca
bir grup çalışması paketi). filex aradaki boşluğu hedefler:

- **Yalnızca sizin için değil, kullanıcılarınız için de bir tarayıcı istemcisi** - birine
  bir `user` ya da `viewer` hesabı ile `…/drive` adresini verin, karşısına dosya
  yöneticisinin kendisi çıkar: depoları, yüklemeler, paylaşım, arama, düzenleyici.
  Aşılması gereken bir yönetim paneli yok, ayrıca kurulacak bir ön yüz yok. `…/admin`
  adresi, işletmecinin aynı uygulamaya açılan kapısıdır.
- **Herkesin zaten bildiği bir gezinti** - solda bir panel: belirgin bir **+ Yeni** menüsü,
  **Ana sayfa · Dosyalarım · Benimle paylaşılanlar · Paylaştıklarım · Son kullanılanlar ·
  Yıldızlılar · Taslaklar · Çöp kutusu**, bir de erişebildiğiniz depolar; biri sizinle bir
  depo paylaştığında o depo orada beliriverir: tek tık, bağlama talimatı yok. Herkes
  paneli üst çubuktan bir simge şeridine daraltabilir. **Ana sayfa**, uygulamanın yanında
  duran bir sayfa değil, uygulamanın *içinde* bir görünümdür - depolarınız, en son
  açtıklarınız ve yıldızladıklarınız, dosyalardakiyle aynı gezinti paneli ve aynı üst çubuk
  altında. Herkesin karşılaştığı kabuk budur: üst çubuk boyunca uzanan, ⌘K palet ipucunu
  taşıyan tek bir arama alanı, Tür / Sahibi / Değiştirilme / Boyut filtre satırı,
  Klasörler ve Dosyalar başlıklı bölümler, ayrıntılar panelinde Ayrıntılar ve Etkinlik,
  bir de depolama satırı. Dosya yöneticisi değil, sade bir dosya alanı isteyenler için
  `uiProfile: 'simple'` ayarı arayüzün geri kalan öğelerini kapalı getirir - tek bölme,
  tek klasör, liste ya da ızgara. Her durumda tek bir gezgin: birincisiyle uyumlu
  tutulması gereken ikinci bir arayüz yoktur.
- **Her yere gömülür** - aynı arayüz bir Vue 3 bileşeni, bir React bileşeni ve çatıdan
  bağımsız bir `<filex-explorer>` web bileşeni olarak gelir. *Kendi* ürününüzün içine,
  kendi filex sunucunuza dayanan ve kiracıya özel bir klasöre kilitlenmiş gerçek bir dosya
  yöneticisi koyun. Gezinti paneli de onunla birlikte gelir - JavaScript'e hiç dokunmayan
  bir sayfaya gömerken yazmanız gereken tek şey budur:
  `<filex-explorer sidenav ui-profile="simple">`.
- **Yapay zekâ ajanlarıyla doğrudan çalışır** - bir API anahtarının izinleriyle sınırlı
  bir REST yüzeyi (`/api/ai`), ayrıca yerleşik bir **MCP sunucusu** (`/api/ai/mcp`);
  `/api/ai` ve `/api/files` yüzeylerini, bir testin yönlendiriciyle uyumlu tuttuğu bir
  [OpenAPI 3.1 dosyası](backend/internal/api/openapi.json) tanımlar. Bir ajana, tek bir
  klasöre sınırlandırılmış bir API anahtarı verin, ajan orada gezginin kendi işlemleriyle
  çalışır - listeleme, okuma, yazma, kopyalama, dönüştürme, paylaşma, çöp kutusu,
  sürümler, arşivler - o klasörün dışında ise hiçbir şey yapmaz.
- **Yalnızca onayladığınız şeyi yapabilen uygulamalar** - hesabı olmayan bir iş ortağıyla
  sözleşme imzalamak, bir videoyu dönüştürmek, bir manifestin tanımladığı her şey bir
  **uygulama** olarak eklenir: filex'in içinde çalışan bir WebAssembly modülü, uygulamanın
  yalıtılmış bir çerçevede filex'in sunduğu kendi arayüzü ya da ikisi birden - tam olarak
  sizin kurarken okuyup verdiğiniz izinlerle. Modüle ne dosya sistemi, ne ağ, ne de
  sunucunuzdaki bir program verilir; arayüz filex'in oturumunu okuyamaz, ağla bağlantısını
  da filex'in kendi politikası keser. Hiçbir şey kendini güncellemez: yeni bir sürüm bir
  yöneticiyi bekler, önceki sürüm de tek tık ötededir. Dördü herkese açık repo olarak
  gelir - **e-İmza**, **Dönüştür**, **filextext** (uçtan uca şifreli bir metin çalışma
  alanı) ve **draw.io** - ve bir uygulamayı GitHub adresinden kurarsınız
  ([Uygulamalar](#uygulamalar)).
- **Sizin dilinizde, sizin yazı yönünüzde** - İngilizce ve Türkçe ikili dosyanın (binary)
  içinde gelir, diğer her dil ise bir **dil paketi**: çalışan hiçbir şeyi olmayan, diğer
  her uygulama gibi bir repodan kurulan bir uygulama; gezgini, yönetim panelini, bir
  yabancının açtığı herkese açık sayfaları *ve filex sunucusunun yazdığı metni* çevirir -
  e-posta, bildirimler, bir bağlantının ardındaki JavaScript'siz sayfalar. Paket bu
  sürümün ne kadarını kapsadığını söyler, eksik bıraktığı her şey İngilizce görünür;
  çoğul biçimleri CLDR'ye uyar, böylece her dil kendisinde gerçekten bulunan biçimleri alır.
  Arapça, İbranice, Farsça ve Urducada arayüz **sağdan sola döner** - ve aynalamanın
  yanlış olacağı yerde durur: belgenin kendi koordinatlarında ve makine metninde
  ([docs/RTL.md](docs/RTL.md), [paket yazın](docs/PLUGIN-KIT.md#writing-a-language-pack)).
  Bir kişinin tek bir dili vardır: web uygulamasında, gezginde ve masaüstü uygulamasında
  ekranın dili hesabın dilidir, herhangi birinde seçilen dil onu her yerde değiştirir ve
  ONLYOFFICE düzenleyicisi de o dilde açılır - ya da bir yöneticinin herkes için seçtiği
  dilde ([düzenleyicinin dili](docs/ONLYOFFICE.md#the-editors-language)).
- **Bizim değil, sizin markanızı taşır** - **Görünüm** ekranında kendi renklerinizle bir
  tema oluşturup varsayılan yapın: oturum açma sayfası ve herkese açık her bağlantı da onu
  taşır, kurulumunuzdan giden bir imza isteği de filex'in değil, sizin adınızı taşır.
- **Gerçek zamanlı** - kimin açık olduğunu gösteren avatarlar (hesapta bir kez ayarlanan,
  sizin adınızla oturum açmış her istemci için gösterilen bir profil fotoğrafı) ve
  WebSocket üzerinden canlı dosya güncellemeleri, yerleşik arayüzde *ve* gömülü
  gezginlerde (kısa ömürlü biletle kimlik doğrulama, yedek olarak API yoklaması). Toplu
  bir işin bildirimleri gönderilirken birleştirilir; böylece beş bin dosyalık bir arşivi
  çıkarmak, açık bir gezgine beş bin değil, sınırlı sayıda mesaj olarak ulaşır
  ([docs/REALTIME.md](docs/REALTIME.md)).
- **Masaüstünüzde de** - aynı gezgin bir Windows/Linux/macOS uygulaması olarak gelir:
  yerel klasörleri sistem tepsisinden sunucuyla eşitlenmiş tutar - **canlı**, yaklaşık bir
  saniyede, iki yönde de - kendini günceller ve birden çok hesabı (ya da kiracıyı) yan
  yana barındırır. Bir klasöre sağ tıklayın → **Bilgisayarda tut**, klasör tek bir filex
  klasörünün altında aynalanır; gerisi pencerede yalnızca çevrimiçi kalır.
  Ekransız makinelere aynı motor `filex sync` / `filex client` olarak gelir.
- **Protokolleri iki yönde de konuşur** - filex yerel disklere, S3'e, FTP'ye, SFTP'ye,
  WebDAV'a ve SMB/NAS paylaşımlarına *bağlanabilir*; kendisine de **S3**, **SFTP**,
  **FTPS**, **NFSv3** ve **WebDAV** olarak *erişilebilir*. `rclone`, `restic`, `aws s3`,
  WinSCP, FileZilla, yalnızca FTP öğrenmiş bir belge tarayıcısı ya da yalnızca NFS öğrenmiş
  bir medya oynatıcıyı filex'e yönlendirin, hepsi web arayüzüyle aynı ağaca, aynı izinlerle,
  aynı çöp kutusu ve aynı kotayla varır. Yerel ağın dışında bir de **`filex mount`**
  komutu var; bu komut uzak bir sunucuyu sıradan HTTPS üzerinden bağlar - Linux'ta bir
  klasör, Windows'ta bir sürücü harfi olarak ([docs/PROTOCOLS.md](docs/PROTOCOLS.md)).
- **Roller ve kullanıcıya özel izinler** - 29 adlandırılmış izin (her dosya işlemi, her
  paylaşım türü, her protokol, API anahtarları, masaüstü uygulaması, beş yönetim alanı),
  herkesin de tek bir rolü vardır: Yönetici, Kullanıcı, İzleyici ya da özel bir rol; özel
  rol bazı klasörlerde farklı olabilir ("Scratch dışında silme yok") ve sınırlar
  içerebilir (bağlantı süresi ve parolası, engellenen dosya türleri, en büyük dosya,
  zorunlu iki adımlı doğrulama). Kişiye özel istisnalar rolün önüne geçer; yetki
  devredilen bir yönetici, kendisinde olmayan hiçbir izni kimseye vermeden kullanıcıları
  yönetebilir ve her kapıda aynı yanıt geçerlidir - web uygulaması, ajan API'si, WebDAV,
  SFTP, FTPS, S3, NFS ve API anahtarları. Kurulu bir uygulama kendi izinlerini ekleyebilir
  ("İmza isteme"), bunlar da aynı yolla verilir; herkese açık bir bağlantı ise yalnızca onu
  oluşturan kişi hâlâ oluşturabildiği sürece açık kalır
  ([docs/PERMISSIONS.md](docs/PERMISSIONS.md)).
- **Gruplar** - adlandırılmış kişi kümeleri, kiracı başına: bir klasörü bir kişiyle
  paylaşır gibi bir grupla paylaşın ve gruba, kendi rolü olmayan her üyesinin alacağı bir
  rol verin (gruplar arasında kararı rol önceliği verir). Üyeler elle eklenir ya da oturum
  açarken taşıdıkları gruplar üzerinden katılır - bir OIDC claim'i, LDAP `memberOf`,
  işletim sisteminin grupları ya da bir vekil sunucu başlığı - kimlik sağlayıcı öyle
  söylediğinde de çıkar ([docs/GROUPS.md](docs/GROUPS.md)).
- **Herkesin zaten sahip olduğu hesapla oturum açma** - yerel bir parola, OIDC, LDAP /
  Active Directory, kimlik doğrulayan bir vekil sunucu ya da filex'in çalıştığı makinedeki
  **Windows ya da Linux hesabı**: parolayı işletim sistemi denetler, filex onu hiç
  saklamaz ve sağlayıcı ancak gerçek bir hesap onunla oturum açtıktan sonra açılır. Her
  sağlayıcı, ilk oturum açmada kime hesap açılacağı konusunda tek bir kurala uyar
  ([docs/OS-LOGIN.md](docs/OS-LOGIN.md)).
- **Parola tahmin etmek yavaştır** - yanlış parolalar web formunda, WebDAV'da, FTPS'te ve
  SFTP'de aynı şekilde hesap başına ve adres başına sayılır; kilit süresi her seferinde
  ikiye katlanarak 15 dakikaya kadar çıkar; geri dönüş yolu IP izin listesidir ve iletilen
  bir istemci adresine yalnızca güvenilir bir vekil sunucudan geldiğinde inanılır -
  varsayılan olarak bu makine ve filex'in yanındaki konteynerler, bunların dışında sizin
  belirttikleriniz
  ([giriş denemesi sınırları](docs/CONFIGURATION.md#sign-in-attempt-limits)). Başka bir
  sitenin, bir ziyaretçinin oturumuyla gönderdiği değişiklik reddedilir
  ([başka kökenlerden gelen istekler](docs/CONFIGURATION.md#requests-from-other-origins)).
- **Tasarımı gereği çok kiracılı** - yerleşik kiracılık moduyla kiracı başına depo, RBAC
  rolleri + öğe bazlı yetkiler, sınırlandırılmış API anahtarları, denetim kayıtları için
  API anahtarı başına kimlik ve uygulama ile kullanıcı olarak ayrılan API anahtarı türleri; böylece
  gömülü gezginde ortak kullanılan bir kimlik bilgisi kimsenin anahtarlarını yönetemez.
  Bir API anahtarı, sahip olduğu izinleri tek tek sayar - boş liste "her şey" diye okunmaz,
  reddedilir - ve **verdiği hiçbir kimlik bilgisi kendisinden geniş olamaz**: dar bir
  API anahtarıyla oluşturulan yeni bir API anahtarı, S3 anahtarı, NFS export'u ya da SSH anahtarı onun
  eylemlerini aşamaz, klasörünün dışına çıkamaz, son kullanma tarihinden uzun yaşayamaz
  (`403 token_ceiling`).
  Kiracı sınırı, yalnızca satırları listeleyen rotalarda değil, bir satırı adıyla anan
  her rotada uygulanır; kurulum genelindeki ayarlar da platformun kendi kiracısına
  (supertenant) ayrılmıştır.
  Her kiracının bir **realm**'i, yani oturum açma adı vardır: iki kiracının `alex`
  kullanıcısı iki ayrı kişidir, ister kiracının kendi adresinde oturum açsınlar, ister
  platformun sayfasında realm'i yazsınlar, ister SFTP üzerinden `realm/alex` yazsınlar
  ([realm'ler](docs/MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)). Ayrıca
  kiracı kendini yönetir: yöneticisi, kiracının kendi OIDC'sini ya da LDAP'ını ekler;
  işletmeci, ortak oturum açma sağlayıcılarını bir ya da birkaç kiracıya bağlar;
  kiracının kendi alan adı da bir CNAME ile kanıtlanır ve vekil sunucunuzun, filex'in
  kendisinin (ACME) ya da kiracının kendi sertifikasıyla sunulur
  ([docs/TENANT-ADMIN.md](docs/TENANT-ADMIN.md)).
- **Kurulumu sıkıcı derecede basit** - tek bir ikili dosya ya da tek bir konteyner,
  kendi alan adında ya da paylaştığınız bir alan adının alt yolunda;
  varsayılan olarak SQLite, isterseniz Postgres/MySQL; her sürücü ortam değişkenleriyle
  değiştirilir. Üç veritabanı motoru da her değişiklikte CI'da geçişlerden (migration)
  geçirilir, birbiriyle karşılaştırılır ve yazılarak sınanır, çünkü "destekleniyor"
  eskiden "derleniyor" demekti ([docs/DATABASES.md](docs/DATABASES.md)).

```
┌─────────────────────────────────────────────────────────────┐
│  filex (Go binary; image ~62 MB slim / ~241 MB full)        │
├─────────────────────────────────────────────────────────────┤
│  HTTP API (chi)  │  Admin UI (Vue 3, embedded)              │
│  Auth Drivers:   │  local · oidc · ldap · proxy-header      │
│                  │  pam · windows (OS accounts)             │
│  Sign-in guard:  │  attempt limits · IP allow-list · realms │
│  Storage Drivers:│  local · s3 · ftp · sftp · webdav · smb  │
│  Served as:      │  s3 · sftp · ftps · nfs · webdav         │
│  DB Drivers:     │  sqlite (default) · mysql · postgres     │
│  Queue Drivers:  │  follows the DB · redis                  │
│  Realtime:       │  WebSocket presence + live updates       │
│  RBAC:           │  roles + groups + grants + share invites │
│  AI / MCP:       │  /api/ai REST + native MCP server        │
│  Sync Worker:    │  etag / size+mtime diff + tombstone      │
│  Replica Layer:  │  primary→replica + rules + reconcile     │
│  Protection:     │  trash + versions + ClamAV (bin/clamd)   │
│  E2E folders:    │  client-side WebCrypto (server blind)    │
│  Notifications:  │  webhook + in-app bell + read/unread     │
│  Search:         │  Bleve (full-text, embedded)             │
│  Thumbnails:     │  image · svg · video · pdf · office      │
│                  │  heic · text · zip · folders · apps      │
│  Plug & Play:    │  OnlyOffice · Drawio · Mermaid           │
│  Apps (wasm):    │  sandboxed · e-Signature · Convert       │
│  Languages:      │  en · tr + language packs · RTL layout   │
│  Appearance:     │  operator themes · instance default      │
└─────────────────────────────────────────────────────────────┘
                          ▲
                          │ HTTP API
       ┌──────────────────┼──────────────────┐
       │                  │                  │
   @brftech/         @brftech/          @brftech/
   filex-core        filex             filex-react
   (Vue 3 SFC)       (Web Component)   (React adapter)
       │                  │                  │
       ▼                  ▼                  ▼
   Vue 3 apps       Any framework      React apps
                    (vanilla, Angular,
                    Svelte, Solid, …)

   Same API, no server plugins:  desktop app (Electron, Windows/Linux/macOS)
                                 CLI client (filex client · filex sync)
```

## Ekran görüntüleri

### Uygulamalar - ilki: belge imzalama

Dana, aynı filex kurulumundaki bir iş arkadaşından ve kurulumun dışındaki bir iş ortağından
bir sözleşmeyi imzalamalarını ister. Uygulama [e-İmza](https://github.com/BRF-Tech/filex-sign);
her ekranı filex çizer, iş ortağının aldığı bağlantı da sıradan bir paylaşımdır.

| Kutuları tanımlayın - her birine ad verin ve kimin olduğunu söyleyin; belge sonra gelir | Kutuları yerleştirin - bir kutu seçin, sayfada gideceği yere dokunun |
|---|---|
| ![Bir imza isteğinin kutularını tanımlama](https://filex.sh/shots/signing/sign-define-1440.63ea72b13724.png) | ![Kutuları belgeye yerleştirme](https://filex.sh/shots/signing/sign-place-1440.696a10b65be8.png) |

| İş ortağının bağlantısı - filex'in herkese açık tek ekranı, kurulumunuzun adıyla, PIN arkasında | …ve açtığı şey: yalnızca kendi kutuları - burada, isteği gönderenin seçtiği yazı tipiyle yazılmış bir ad (öteki iki yol çizmek ve yüklemek) |
|---|---|
| ![Dış imzacının PIN ekranı](https://filex.sh/shots/signing/sign-outside-pin-1440.2cd8ccb99483.png) | ![Kutularını dolduran dış imzacı](https://filex.sh/shots/signing/sign-outside-fill-1440.9f50aeb7abef.png) |

| Belge imzadayken - herkes için dondurulmuş, kimin imzaladığı ayrıntılarında | Uygulama kurulurken - istediği her izin, sade bir dille, hiçbir şey çalışmadan önce |
|---|---|
| ![Belge kilitli, İmzalar paneli açık](https://filex.sh/shots/signing/sign-status-1440.b5bfdbd379c3.png) | ![Kurulum sihirbazının izin incelemesi](https://filex.sh/shots/apps/apps-install-review-1440.cdb1a4ebf8f6.png) |

| Kurulu bir uygulama - nereden geldiği, parmak izi ve sahip olduğu her izin, sade bir dille (ayarları ve işlemleri sayfanın daha aşağısında gelir) | Dönüştürücü, bir başka uygulama - her hedef kendi kategorisinin altında, üç adım |
|---|---|
| ![Kurulu bir uygulamanın ayrıntıları](https://filex.sh/shots/apps/apps-detail-1440.4e27b40c5ce7.png) | ![Dönüştürücünün sihirbazı](https://filex.sh/shots/apps/convert-wizard-1440.231ada006fd6.png) |

| Kendi arayüzünü getiren bir uygulama - inceleme, paketin parmak izini, paketin dışındaki her adresi (canlı olanı bir izindir, sarı renkte) ve bir tarayıcının söz veremeyeceği şeyi gösterir | …ve o arayüz, filex'in önizlemesinin duracağı yerde, kendi dosya türü için açılmış. Dosyayı filex üzerinden, yalıtılmış bir çerçevede okur ve kaydeder (bu görüntüler için yazılmış küçük bir örnek uygulama) |
|---|---|
| ![Kendi arayüzü olan bir uygulamanın kurulum incelemesi](https://filex.sh/shots/apps/app-interface-review-1440.5e0e3009d2ba.png) | ![Bir uygulamanın kendi arayüzü, bir dosyanın görüntüleyicisi olarak açık](https://filex.sh/shots/apps/app-interface-viewer-1440.519b23618156.png) |

| Kurulumdaki her uygulama ve aralarında bir **dil paketi** - çalışan hiçbir şeyi olmayan bir manifest; bu filex kurulumunun ne kadarını çevirdiğini söyler, paket gidince dili de gider |
|---|
| ![Uygulamalar listesi, uygulamaların arasında bir dil paketi](https://filex.sh/shots/langpack/apps-list-1440.50e11eedf3d4.png) |

### Size ait olanlar, nerede olursanız olun

| Çan - üzerinde okunmamışların sayısı, her satır yazdığı yere götürür | Tüm bildirimleriniz, gezginin içinde - yalnızca yöneticiler için değil, herkes için |
|---|---|
| ![Okunmamış rozetini taşıyan çan, açık hâlde](https://filex.sh/shots/signing/bell-badge-1440.61396b9ab714.png) | ![Gezginin üzerinde bildirimlerin tam listesi](https://filex.sh/shots/signing/notifications-list-1440.3176161f2110.png) |

| Paylaştıklarım - oluşturduğunuz bağlantılar ve birine iletmeniz gerektiğinde bunların PIN'leri | Her yönetim tablosu - satır başına sabitlenmiş tek bir **Aksiyon** menüsü, gezgindeki ⋮ ile açılan menünün aynısı |
|---|---|
| ![Paylaştıklarım, bir satırın Aksiyon menüsü açık](https://filex.sh/shots/signing/my-shares-1440.6f8676cbb555.png) | ![Yönetim → Paylaşımlar, bir satırın Aksiyon menüsü açık](https://filex.sh/shots/signing/admin-table-actions-1440.ad8c2e623c72.png) |

### Markanız

| Görünüm - kendi renklerinizle bir tema oluşturun, siz yazdıkça önizlenir | Varsayılan yapıldığında herkesin gezgini onu taşır… |
|---|---|
| ![Tema düzenleyicisi](https://filex.sh/shots/appearance/theme-editor-1440.7cf3d997f7b1.png) | ![İşletmecinin temasını taşıyan gezgin](https://filex.sh/shots/appearance/themed-explorer-1440.dbe464fc39c7.png) |

| …oturum açma sayfası da, henüz kimse oturum açmadan | filex'in izlemeyeceği bir sembolik bağ bunu söyler - listede, ayrıntılarında da sözle |
|---|---|
| ![İşletmecinin temasını taşıyan oturum açma sayfası](https://filex.sh/shots/appearance/themed-signin-1440.1c420dacc12e.png) | ![Deponun dışına çıkan bir sembolik bağ, rozetli](https://filex.sh/shots/symlinks/symlink-badge-1440.0d128385d287.png) |

### Dosya yöneticisi

| Paylaşım - PIN, son kullanma tarihi, indirme limiti, tek satırlık `curl` | Markdown görüntüleyici |
|---|---|
| ![Paylaşım iletişim kutusu](https://filex.sh/shots/share-modal.c8a399c36424.png) | ![Markdown görüntüleyici](https://filex.sh/shots/viewer-markdown.1789ecdcfbc5.png) |

| …ve karşı taraftaki kişinin açtığı şey. filex'in dışarıya dönük TEK bir ekranı vardır - paylaşılan bir dosya, bir klasör, bir dosya isteği, bir uygulamanın imzalama sayfası ve bunlardan herhangi birinin önündeki PIN, hepsi bu sayfadır, kurulumunuzun adıyla |
|---|
| ![Herkese açık bir paylaşım bağlantısı, alıcısının gördüğü hâliyle](https://filex.sh/shots/public-share.0e3ba07f7c88.png) |

| Yönetim paneli | Demo açılış sayfası |
|---|---|
| ![Yönetim paneli](https://filex.sh/shots/admin-dashboard.d94a065baab6.png) | ![Demo açılış sayfası](https://filex.sh/shots/demo-landing.d2b345f6a229.png) |

| Yönetim menüsü - bütün sayfalar üç panelde, **Dosyalar ve depolama**, **Kişiler ve güvenlik** ve **Sistem**, her sayfanın altında kısa bir satırla; telefonda aynı sayfalar bir çekmecede yer alır ([docs/ADMIN-PANEL.md](docs/ADMIN-PANEL.md)) |
|---|
| ![Yönetim menüsünün Kişiler ve güvenlik paneli, Yönetim → Kullanıcılar sayfasının üzerinde açık](https://filex.sh/shots/megamenu/people-panel-1440.2efdcb9a685a.png) |

| Roller - Yönetici, Kullanıcı, İzleyici ve kendi rolleriniz: her birinin kimde olduğu, neye izin verdiği, klasöre göre nerede farklılaştığı, sınırları ([docs/PERMISSIONS.md](docs/PERMISSIONS.md)) |
|---|
| ![Yönetim → Roller: yerleşik roller ve iki özel rol](https://filex.sh/shots/roles/roles-list-1440.77477a2c7001.png) |

| Gruplar - klasör erişimi ve bir rolü olan adlandırılmış kişi kümeleri; üyeler elle eklenir ya da oturum açmanın taşıdığı gruplarla eşitlenir ([docs/GROUPS.md](docs/GROUPS.md)) | Bir klasörü kişilerin yanında bir grupla paylaşmak - Sahip seviyesi tek tıkla verilmez, iletişim kutusunda istenir |
|---|---|
| ![Yönetim → Gruplar](https://filex.sh/shots/groups/groups-list-1440.73224b70e40b.png) | ![Bir klasörü bir grupla paylaşmak](https://filex.sh/shots/groups/share-group-1440.31c2a81e1eeb.png) |

| Giriş güvenliği - giriş denemesi sınırı, izinli adresler, güvenilir vekil sunucular, kilitler ve oturum açma olayları ([giriş denemesi sınırları](docs/CONFIGURATION.md#sign-in-attempt-limits)) | …ve kilitli bir hesabın oturum açma formunun söylediği; düğmesinde kilidin kalan süresi geri sayar |
|---|---|
| ![Yönetim → Giriş güvenliği](https://filex.sh/shots/loginsecurity/login-security-1440.5a98c09e6f76.png) | ![Kilitli bir hesapta oturum açma formu](https://filex.sh/shots/loginsecurity/login-locked-1440.386b07b4543a.png) |

| Kim şifreleyebilir - kapalı, yalnız yöneticiler, rolü izin veren herkes ya da yönetici onayıyla; bekleyen istekler, kimin neden istediğiyle birlikte ([kim şifreleyebilir](docs/E2E-ENCRYPTION.md#who-may-encrypt)) | …ve isteyen kişinin tarafı: Yeni klasör iletişim kutusu, bir gerekçeyle birlikte, bir yöneticiden tek bir şifreli klasör ister |
|---|---|
| ![Yönetim → Şifreleme: onay politikası ve bekleyen üç istek](https://filex.sh/shots/encryption/admin-encryption-1440.b74163acb93c.png) | ![Yeni klasör iletişim kutusundan şifreli bir klasör istemek](https://filex.sh/shots/encryption/request-new-folder.e74894fba30f.png) |

| Varsayılan uygulamalar - filex dışında bir şeyin işlediği her dosya türü: onu kimin açtığı ve küçük resmini kimin çizdiği, sizin belirlediğiniz sırayla ([Varsayılan uygulamalar](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail)) | Klasör önizlemeleri - her klasör, içine en son gelen üç dosyayla çizilir; SVG'leri filex'in yerleşik motoru çizer ([docs/thumbnails.md](docs/thumbnails.md#folder-previews)) |
|---|---|
| ![Eklentiler → Varsayılan uygulamalar](https://filex.sh/shots/defaultapps/default-apps-1440.27d3c64fe457.png) | ![En yeni dosyalarıyla çizilmiş klasörler](https://filex.sh/shots/thumbnails/folders-grid-1440.2b408fb54f7e.png) |

| Bir `.csv` dosyası, ONLYOFFICE bağlıysa onun hesap tablosunda açılır - önce görüntülenir, ayırıcı soran iletişim kutusu da çıkmaz: dosyanın kendi ayırıcısı iletilir ([CSV dosyaları](docs/ONLYOFFICE.md#csv-files)) | …ve CSV olarak kaydedilince nelerin kaldığını söyleyen düzenleyicisi; dosya aynı türden bir CSV olarak geri döner |
|---|---|
| ![Noktalı virgülle ayrılmış bir CSV, ONLYOFFICE'in hesap tablosunda açık](https://filex.sh/shots/csvoffice/csv-view-1440.83237ba55d3d.png) | ![CSV, ONLYOFFICE'in düzenleyicisinde, kaydedilince nelerin kaldığını söyleyen notla birlikte](https://filex.sh/shots/csvoffice/csv-edit-1440.4efc379a293d.png) |

| Kabuk - herkesin karşılaştığı düzen | Bu klasörde arama; `⌘K` / `Ctrl K` sorguyu palete devreder |
|---|---|
| ![filex kabuğu](https://filex.sh/shots/driveshell/driveshell-hero-1440.16742e249165.png) | ![Bir klasörde arama](https://filex.sh/shots/driveshell/driveshell-search-1440.119e6bd43905.png) |

| Gezinti paneli - Ana sayfa, Benimle paylaşılanlar, Paylaştıklarım, Son kullanılanlar, Yıldızlılar, Çöp kutusu ve erişebildiğiniz depolar | Simge şeridine daraltılmış |
|---|---|
| ![Gezinti paneli](https://filex.sh/shots/sidenav/sidenav-expanded-1440.461d5aaaff2a.png) | ![Bir şeride daraltılmış](https://filex.sh/shots/sidenav/sidenav-rail-1440.843a2158380d.png) |

| Etiketler - kendi etiketleriniz ya da ekibinizinkiler; bir etiket, onu taşıyan her dosyayı hangi klasörde durursa dursun açar | Çöp kutusu - neyin silindiği, nereden geldiği ve gitmesine ne kadar kaldığı |
|---|---|
| ![Kişisel etiketler ve ekip etiketleri](https://filex.sh/shots/tags/tags-kinds-1440.a9f9fff4d4fd.png) | ![Çöp kutusu görünümü](https://filex.sh/shots/sidenav/view-trash-1440.ab3cfb3b01cf.png) |

| Benimle paylaşılanlar - başkalarının size yetki verdiği klasörler, bağlama talimatı yok | Başka bir ürünün sayfasına gömülü |
|---|---|
| ![Benimle paylaşılanlar](https://filex.sh/shots/sidenav/view-shared-1440.475ec2d8b49f.png) | ![Gömülü web bileşeni](https://filex.sh/shots/sidenav/embed-webcomponent-1440.c8c25d893d24.png) |

| Nasıl bağlanılır - kılavuzlar, *sizin* kurulumunuzdan üretilir | API anahtarları - kendinizinkini oluşturun, gezginde ya da gömülü bir gezginde (bir kişinin oturumuyla ya da API anahtarıyla; tek bir ortak *uygulama* API anahtarıyla vekil sunucu üzerinden çalışan gömülü bir gezginde bu öğe yer almaz) |
|---|---|
| ![Nasıl bağlanılır](https://filex.sh/shots/sidenav/connect-1440.7327ccaf3ae3.png) | ![API anahtarları](https://filex.sh/shots/sidenav/apikeys-minted-1440.bae083a0679a.png) |

| filex'e her şeyden erişmek - S3, SFTP, FTPS, NFS, WebDAV. Her komut *sizin* kurulumunuzdan üretilir |
|---|
| ![Bağlantı kılavuzu](https://filex.sh/shots/connections-guide.cf9a135724c9.png) |

| filex'le birlikte gelmeyen bir depo - **Eklentiler → Depolama eklentileri** sayfasında eklenti olarak kurulur, yapılandırma formunu kendisi tanımlar |
|---|
| ![Eklentiler](https://filex.sh/shots/admin-plugins.c25fa69cfc7c.png) |

## Hızlı başlangıç - ikili dosya

```bash
# Download from https://github.com/BRF-Tech/filex/releases
./filex serve
```

```
═══════════════════════════════════════════════════════════════
  filex · self-hosted file manager
═══════════════════════════════════════════════════════════════
  Listening on:   http://0.0.0.0:5212
  Admin UI:       http://0.0.0.0:5212/admin
  Files UI:       http://0.0.0.0:5212/drive
  Embed JS:       http://0.0.0.0:5212/embed.js

  First run detected. Initial admin user created:
    Email:    admin@local
    Password: <printed once>
  Saved to:  ~/.filex/.first-run.txt (mode 0600, shown ONCE)
  Change at: /admin/dashboard?settings=1
═══════════════════════════════════════════════════════════════
```

## Compose ya da Helm ile kendi sunucunuzda çalıştırın

Yukarıdaki `docker run` komutu filex'i denemek için yeter. Gerçek bir kurulum için hazır
yığınlar [`deploy/`](deploy/) dizinindedir:

- **[`deploy/compose/`](deploy/compose/)** - Docker Compose:
  - **minimal** - filex + SQLite + yerel disk (tek servis, sıfır bağımlılık).
  - **full** - filex + PostgreSQL + Redis + Caddy (otomatik HTTPS), ayrıca açılıp
    kapatılabilen ek bileşenler: **OnlyOffice**, **Drawio** ve bir **S3 sunucusu**
    (Versity S3 Gateway). Her birini `.env` dosyasındaki bir Compose profiliyle açıp
    kapatın. Dönüştürme bir yan konteyner değil, [Dönüştür uygulaması](#uygulamalar).
- **[`deploy/helm/filex/`](deploy/helm/filex/)** - Kubernetes için bir Helm chart'ı
  (Deployment + PVC + isteğe bağlı Ingress). Yukarıdaki her ek bileşen, `values.yaml`
  dosyasında bir `enabled` anahtarıdır - PostgreSQL / Redis / bir S3 sunucusunu birlikte
  kurun ya da dışarıdaki bir OnlyOffice / Drawio'yu bağlayın.

Her kurulum türü için adım adım talimatlar
[docs/INSTALLATION.md](docs/INSTALLATION.md) belgesindedir.

filex kendi alan adının kökünde ya da paylaştığı bir alan adının alt yolunda
(`https://example.com/filex/`) çalışır: tek bir ayar, `FILEX_BASE_PATH`, ve yolun tamamını
ileten bir vekil sunucu - Caddy, nginx ve Helm örnekleri
[docs/DEPLOYMENT.md → filex'i bir alt yol altında sunmak](docs/DEPLOYMENT.md#serving-filex-under-a-sub-path)
bölümünde.

## Uygulamanıza gömün

### Vue 3
```bash
pnpm add @brftech/filex-core
```
```vue
<script setup>
import { FileExplorer } from '@brftech/filex-core';
import '@brftech/filex-core/style.css';
</script>
<template>
  <FileExplorer :config="{ apiBase: 'http://localhost:5212', auth: { kind: 'bearer', token: '…' } }" />
</template>
```

### React
```bash
pnpm add @brftech/filex-react
```
```jsx
import { FileManager } from '@brftech/filex-react';
<FileManager config={{ apiBase: 'http://localhost:5212' }} onError={(e) => console.error(e)} />
```

**İçe aktarılacak bir stil dosyası yoktur** - görünüm paketin içinde gelir ve bileşen
sayfaya yerleştirilirken eklenir, dolayısıyla bu kod parçasında eksik bir şey yok.
⚠ Paketleyicide isteğe bağlı görüntüleyici paketlerini (`monaco-editor` ve benzerleri)
dışarıda bırakmak gerekir; [docs/INTEGRATION.md](docs/INTEGRATION.md) bunu tek bir
`rollupOptions.external` satırıyla gösterir. Bu içe aktarmaların her biri korumalıdır,
böylece görüntüleyiciler bozulmak yerine daha azıyla yetinir.

### Vanilla JS / herhangi bir çatı
```html
<script type="module" src="https://cdn.jsdelivr.net/npm/@brftech/filex/dist/filex.js"></script>
<filex-explorer api-base="http://localhost:5212" sidenav connections ui-profile="simple"></filex-explorer>
```

`sidenav` gezinti panelini açar (panel varsayılan olarak açıktır; öznitelik, barındıran
sayfa bunu iki türlü de belirtebilsin diye vardır), `connections` panele "Nasıl bağlanılır"
ve "API anahtarları" öğelerini ekler, `ui-profile="simple"` ise ileri düzey kullanıcılara
yönelik arayüz öğelerini kapalı getirir. Üçü de sıradan birer `config` anahtarıdır, bu
yüzden Vue ve React sarmalayıcıları onları aynı biçimde ayarlar - bkz.
[docs/INTEGRATION.md](docs/INTEGRATION.md).

Barındıran uygulama çok kiracılıysa genellikle API'ye sunucu tarafında vekillik eder, her
isteğe **sınırlandırılmış bir API anahtarı** (`root: tenant-folder`) ekler ve istemci başlıklarını
siler - sınırlamayı bileşen değil, arka uç uygular. Böyle bir API anahtarı `kind: "app"`
türündedir, bu yüzden panel tek bir kişiye ait bölümleri - API anahtarları, Son
kullanılanlar, Yıldızlılar, Benimle paylaşılanlar - gizler; Yükle, depolar, Çöp kutusu ve
"Nasıl bağlanılır" ise kalır. Bkz.
[docs/INTEGRATION.md](docs/INTEGRATION.md) ve
[docs/MCP.md](docs/MCP.md#token-kinds---user-vs-app).

⚠ Gömülü bir gezgin, başka bir kökendeki sayfada - kardeş bir alt alan adı dâhil -
ziyaretçinin kendi filex **oturum çereziyle** (API anahtarı olmadan) çalışıyorsa eskisi gibi okur,
ama gönderdiği her değişiklik, o köken `FILEX_CORS_ALLOWED_ORIGINS` ayarında yer alana kadar
reddedilir (`403 cross_origin_refused`); varsayılan `*` değeri bu izni vermez. Bearer olarak
gönderilen bir API anahtarı, anahtarla vekillik eden bir barındıran uygulama, masaüstü uygulaması ve kurulu web
uygulaması hiçbir şey gerektirmez
([docs/CONFIGURATION.md](docs/CONFIGURATION.md#requests-from-other-origins)).

⚠ Paketleri ve sunucuyu aynı sürümde tutun: **`@brftech/filex` 0.54, bir filex 0.54
sunucusu ister**. Hangi dosyaların düzenlemek için açıldığı, girdi sınırları ve sürüm
satırı sunucunun yeteneklerinden (capabilities) gelir; paketler geri dönebilecekleri bir
kopya tutmaz ([docs/API.md](docs/API.md)).

## Masaüstü uygulaması ve CLI

Gezgin, bir **Windows / Linux / macOS masaüstü uygulaması** olarak da gelir - ayrı, yarım
bir kopya değil, web arayüzünün ve gömülü gezginlerin gösterdiği bileşenin aynısı:

- **Aynı anda birden çok hesap** - her biri kendi kurumsal kimliğini gösteren bir
  sunucu/kiracı şeridi.
- **Dosyaları dışarı sürükleyin** - seçtiklerinizi masaüstüne ya da başka bir uygulamaya
  sürükleyin: klasörler ve çoklu seçimler ayrı ayrı gerçek dosya ve klasörler olarak
  gelir. Bu bilgisayarda zaten tutulan her şey anında sürüklenir; gerisi bir kez indirilip
  önbelleğe alınır ([docs/DESKTOP.md](docs/DESKTOP.md#dragging-files-out)).
- **Bilgisayarda tut** - herhangi bir klasöre, dosyaya ya da bütün bir depoya sağ tıklayıp
  onu makinedeki tek bir filex klasörünün altına aynalayın (bu klasör Ayarlar'dan
  taşınabilir); gerisi yalnızca çevrimiçi kalır ve her satır hangi durumda olduğunu söyler
  (✓ ◐ ⟳ ☁). "Yalnızca çevrimiçi tut" yerel kopyayı çöp kutusuna gönderir ya da yerinde
  bırakır ([docs/DESKTOP.md](docs/DESKTOP.md#keeping-folders-on-this-computer)).
- **Klasör eşitleme** - yerel bir klasörü sunucudaki bir klasörle eşleştirin; uygulama
  sistem tepsisinde dururken ikisi çift yönlü ve **canlı** olarak eşitlenmiş kalır:
  tarayıcıda yapılan bir kayıt yaklaşık bir saniyede diske, yerel bir kayıt da aynı hızla
  sunucuya ulaşır (motor, sunucunun değişiklik akışını ve dosya sistemini izler, güvenlik
  ağı olarak 30 saniyede bir tam denetimle); iki taraf aynı anda değiştiğinde iki sürüm de
  saklanır; paralel aktarımlar ve listelemeler, kesildiği yerden devam eden bir ilk
  çalıştırma, 30 günlük yerel çöp kutusu ve eksik bir klasörü toplu silmeye çevirmeyi
  reddeden bir motor ([docs/SYNC.md](docs/SYNC.md)).
- **Office belgelerini kendi diskinizden açar** - bir `.docx`/`.xlsx`/`.pptx` dosyasına
  (ya da on Office türünden herhangi birine veya bir `.csv` dosyasına) çift tıklayın; belge, Office kurulu olmayan
  bir makinede, sunucunuzun çalıştırdığı düzenleyicide açılır. Bu bilgisayarda tuttuğunuz
  bir klasörün içindeki belgenin kendisi açılır; bunun dışındaki her şey sunucuya
  kopyalanır, düzenlenir ve özgün dosyanın üzerine geri yazılır - düzenleyici onu başka bir
  biçimde kaydederse (eski bir `.doc` `.docx` olarak döner) yanına yazılır
  ([docs/DESKTOP.md](docs/DESKTOP.md#opening-documents-from-your-computer)).
- **Sürücü olarak bağla** - Ayarlar'daki bir düğme sunucuyu WebDAV üzerinden işletim
  sisteminin bir sürücüsü olarak bağlar, bir başkası ayırır; kimlik bilgisi hesabın kendi
  API anahtarıdır ve hiçbir komut satırında görünmez. Windows'ta sınandı; macOS ve Linux için
  kod yolları var ama henüz doğrulanmadı
  ([docs/DESKTOP.md](docs/DESKTOP.md#mounting-the-server-as-a-drive)).
- **⌘K her hesapta arar**: sonuçlar şeritteki her hesap için tek bir rozet altında
  gruplanır; arama, indirme ve dışarı sürükleme her hesapta o hesabın kendi oturumuyla
  yapılır ([docs/SEARCH.md](docs/SEARCH.md)).
- **Bildirimleriniz ve hesabınız pencerede** - üst çubuk web uygulamasındaki gibi biter:
  **çan** (okunmamışların sayısı, en yeni satırlar, *Tümünü okundu işaretle*, listenin
  tamamı) ve **avatar**; avatarda *Kullanıcı ayarları* - web uygulamasının,
  **pencerenin içinde** açılan kendi ayarlar iletişim kutusu - ve yöneticiler için
  *Yönetim paneli* bulunur. Bir bildirime tıklamak pencerenin içinde, dosyanın seçili
  olduğu klasöre götürür. Oturumu kapatma, uygulamanın kendi *Ayarlar → Hesaplar*
  bölümünde kalır ([docs/DESKTOP.md](docs/DESKTOP.md#notifications-and-your-account)).
- **Hesabınızın dilini konuşur** - pencere, sistem tepsisi menüsü, bildirimler ve
  eşitleme motorunun her klasörün altındaki iletileri ekrandaki hesabın dilini kullanır;
  *Ayarlar → Dil* hesabın dilini değiştirir, böylece web uygulaması da onu izler
  ([docs/DESKTOP.md](docs/DESKTOP.md#language)).
- **Tarayıcınız üzerinden oturum açar**; böylece SSO ve MFA tıpkı web'deki gibi davranır.
- **Kendini günceller** - sessizce indirir, çıkışta kurar; `FILEX_NO_UPDATE=1` bunu kapatır.
- **Kurulmadan çalışır**, ihtiyacınız olan buysa: Windows için **taşınabilir** `.exe`,
  Linux için AppImage ve macOS için `.zip` dosyası, hepsi nereye koyarsanız oradan
  çalışır. Taşınabilir Windows kopyası her şeyini yanındaki tek bir `filex-data`
  klasöründe tutar; böylece o klasörü sildiğinizde, sizin olmayan bir makinede size ait
  hiçbir şey kalmaz - karşılığında kendini güncellemez.

Microsoft Store'dan (Windows 10/11) ya da Snap Store'dan (Ubuntu ve snapd bulunan diğer
Linux dağıtımları) **kurun**:

<p>
<a href="https://apps.microsoft.com/detail/9PKXDJLVZWXW"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/badges/ms-store-light.svg"><img src="docs/badges/ms-store-dark.svg" alt="Microsoft Store'dan indirin" height="52"></picture></a>&nbsp;&nbsp;&nbsp;
<a href="https://snapcraft.io/filex-app"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/badges/snap-store-white.svg"><img src="docs/badges/snap-store-black.svg" alt="Snap Store'dan edinin" height="52"></picture></a>
</p>

ya da bir paket yöneticisiyle:

```bash
brew install brf-tech/filex/filex-app       # macOS 13+ (Apple Silicon), Homebrew tap BRF-Tech/homebrew-filex
sudo snap install filex-app                 # the same snap as the badge above
```

Store derlemesi (*filex File Manager*), kod imzalı tek Windows kopyasıdır - onu Microsoft
imzalar - ve Store onu güncel tutar. Masaüstü uygulamasının winget paketi
(`BRFTech.filex-app`) her sürümle birlikte gönderiliyor ve winget moderatörlerinin ilk
incelemesini bekliyor; bu yüzden `winget install BRFTech.filex-app` komutu onu henüz
bulamıyor. Kurulum programı, taşınabilir `.exe`, AppImage, `.deb`, `.rpm` ve `.dmg`
[son sürüme](https://github.com/BRF-Tech/filex/releases/latest) eklidir - henüz kod imzalı
değil; Windows kurulum programında bir SmartScreen uyarısı bekleyin. Ayrıntılar:
[docs/DESKTOP.md](docs/DESKTOP.md). Yalnızca CLI: `brew install brf-tech/filex/filex`,
Windows'ta ise `winget install BRFTech.filex` ([docs/CLI.md](docs/CLI.md)).

Linux'ta `.deb`, `.rpm` ve AppImage, Chromium'un yalıtım alanı olmadan asla çalışmaz.
`.deb` ve `.rpm` paketleri hiçbir şey gerektirmez; Ubuntu 23.10 ve sonrasında AppImage
tek seferlik bir AppArmor profili ister, uygulama da bunu söyler ve atılacak adımı
gösterir ([docs/DESKTOP.md](docs/DESKTOP.md#appimage-on-recent-ubuntu)). Snap ise
Chromium'un yalıtım alanı olmadan, snap'in katı kısıtlaması içinde çalışır ve o da
hiçbir şey gerektirmez ([docs/DESKTOP.md](docs/DESKTOP.md#the-snap-and-the-sandbox)).

**ARM (arm64)** - onun için neler geliyor (her sürümde bunlar derlenir ve yayımlanmadan
önce arm64 makinelerde çalıştırılır):

| | arm64 |
|---|---|
| Sunucu + CLI ikili dosyası | Linux, macOS ve Windows: `filex-<os>-arm64` ile `.tar.gz` / `.zip` arşivleri |
| Docker imajları (`ghcr.io/brf-tech/filex`, full ve slim) | çok mimarili - `docker pull` arm64'ü kendisi seçer |
| Masaüstü uygulaması - Linux | `filex-desktop-arm64.AppImage`, `filex-desktop-arm64.deb`, `filex-desktop-aarch64.rpm` ve Snap Store (`sudo snap install filex-app` arm64'ü seçer) - 0.48.1'den beri |
| Masaüstü uygulaması - Windows on Arm | `filex-desktop-arm64.exe` (kurulum programı) ve `filex-desktop-portable-arm64.exe` - 0.48.1'den beri; uygulama kendini arm64 derlemesine günceller |
| Masaüstü uygulaması - macOS | Yalnızca Apple Silicon (Intel derlemesi yok) |
| Homebrew | Apple Silicon'da ve Arm üzerinde Linux'ta CLI (`filex`); Apple Silicon'da masaüstü uygulaması (`filex-app`) |
| winget | Arm üzerinde Windows'ta CLI (`BRFTech.filex`) - winget arm64 derlemesini kendisi seçer |

Bir Arm makinede uygulamanın *filex masaüstü uygulamasını edinin* teklifi (ve
Ayarlar'daki kopyası) önce arm64 dosyasını sunar,
[filex.sh](https://filex.sh/#downloads) adresindeki indirme listesi de onu öne
çıkarır; bu, tarayıcının bildirdiğine göre yapılır (Chromium'un istemci
ipuçları, Firefox'un `aarch64` değeri). Bunu söylemeyen bir tarayıcıya (Safari,
Windows'ta Firefox) x64 dosyası sunulur, arm64 dosyası da yanında durur.

Aynı ikili dosya ayrıca sunucular, betikler ve ekransız makineler için bir istemcidir:

```bash
filex client login --url https://files.example.com
filex client upload build/report.pdf docs://ci-artifacts/
filex client mv docs://ci-artifacts/report.pdf archive://2026/   # across storages, waits for the job
filex client run convert convert docs://data/table.csv --param target=xlsx

filex sync add ~/Documents/work docs://work   # the engine the desktop app uses
filex sync run --watch 30s
```

Bkz. [docs/CLI.md](docs/CLI.md) ve [docs/SYNC.md](docs/SYNC.md). Motoru yöneten bir
program `filex sync run --json` çıktısını okur: satır başına bir JSON olayı, her birinde
değişmeyen bir kod ve motorun hesabın dilindeki cümlesi - masaüstü uygulaması tam olarak
bunları gösterir ([olay akışı](docs/SYNC.md#the-event-stream---json)).

## Yapay zekâ ajanları / MCP

filex, `/api/ai` adresinde API anahtarıyla kimlik doğrulayan bir otomasyon yüzeyi (listeleme,
okuma, yazma, taşıma, kopyalama, silme, arama, paylaşma, zip) sunar ve `/api/ai/mcp`
adresinde **Model Context Protocol** konuşur. Ajan, gezginin kendi işlemlerini de
yürütür - depolar arası kopyalama, **dönüştürme** gibi uygulama işlemleri, işlem kuyruğu,
çöp kutusu ve sürüm geçmişi, 7z/TAR arşivleri, kendi bağlantıları ve dosya istekleri, çan,
yıldızlar, yorumlar ve sahibi olduğu bir öğenin izinleri - ve bunu gezginin kendi
işleyicileri üzerinden yapar; bu yüzden kurallar gezginin kurallarıdır. Yönetici anahtarı,
panelin yaptığı işlere (kiracılar, kimlik sağlayıcılar, giriş güvenliği, Varsayılan
uygulamalar, webhook'lar, depolar) panelin kendi işleyicileri üzerinden ulaşır. `/api/ai`
ve `/api/files`, bir [OpenAPI 3.1 dosyası](backend/internal/api/openapi.json) içinde
tanımlanır:

```bash
claude mcp add filex --transport http https://files.example.com/api/ai/mcp \
  --header "Authorization: Bearer <api-token>"
```

API anahtarı, izinlerini eylem bazında taşır (`read`, `write`, `delete`, ayrıca `mcp` ve
`admin`), isteğe bağlı olarak **tek bir klasöre sınırlandırılmış** olabilir, arayüzle aynı
RBAC yetkilerine ve rollerine tabidir ve anahtara özel bir kimlikle damgalanır; böylece
denetim kayıtları, paylaşımlar ve kimin açık olduğu bilgisi, neyi *kimin* (hangi
entegrasyonun) yaptığını gösterir. Eylemler **anahtarın ulaştığı her yüzeyde**
geçerlidir - `/api/ai`, MCP araçları, gezginin kendi rotaları (dolayısıyla `filex client`
ve gömülü bir gezgin de), WebDAV, SFTP, FTPS ve bu anahtardan oluşturulan S3 anahtarları
ile NFS export'ları. Bazı işleri bir anahtar asla yapamaz: eklenti kurmak - ajan, bir
yöneticinin panelde onayladığı **bir kurulum isteği bırakır** - ve birini yönetici yapmak.
Her anahtar en az bir izin belirtmek zorundadır - boş liste reddedilir, asla "hepsi" diye
okunmaz - ve **verdiği hiçbir şey anahtarın kendisinden geniş olamaz**: salt okunur ya da
bir klasöre sınırlandırılmış bir anahtarla, daha fazla eyleme, anahtarın kökünün dışında
bir köke ya da daha uzun bir ömre sahip bir API anahtarı, S3 erişim anahtarı, NFS export'u
ya da SSH anahtarı istenirse bu istek, neyin fazla geniş olduğunu söyleyen bir
`403 token_ceiling` yanıtıyla reddedilir. Ajanın yaptığı **taşıma asla üzerine yazmaz**:
alınmış bir ada giden öğe, tıpkı arayüzdeki bir taşımada olduğu gibi, var olanın yanına
kullanılmayan bir adla (`report-copy.txt`) yerleşir; yanıt da öğenin gerçekte yerleştiği
yolu bildirir. **Şifreli klasörler** de tanınır: her satır şifreli olup olmadığını söyler,
şifreli veri asla dosyanın kendisiymiş gibi verilmez (`409 E2E_ENCRYPTED`) ve şifreli bir
klasöre yazılan şifresiz içerik, çağıran bunu bilerek yaptığını söylemedikçe
(`allow_plaintext`) reddedilir ([docs/MCP.md](docs/MCP.md#encrypted-folders-and-fxe)).

Ajanın diskinde zaten duran büyük bir dosya, bir araç çağrısına asla sığmaz - baytlarının
modelin bağlamından geçmesi gerekirdi. **Yükleme biletleri** bunu çözer: yetkilendirilmiş
tek bir çağrı, hedefi sabitler ve kısa ömürlü, tek kullanımlık, **kimlik bilgisi
gerektirmeyen** bir URL döndürür; böylece filex API anahtarı olmayan bir ajan bile aktarımı
`curl -T bigfile <url>` komutuyla tamamlayabilir. Ayrıntılar: [docs/MCP.md](docs/MCP.md).

filex bir şeyi reddettiğinde bunu her kapıda aynı biçimde söyler: dallanmak için
değişmeyen bir `error` kodu ve sunucunun okuyanın dilindeki cümlesi `message` içinde,
olduğu gibi aktarılsın diye ([docs/API-ERRORS.md](docs/API-ERRORS.md)). Bir ajanın
oluşturduğu bağlantı da kendi indirme komutuyla döner: sunucunun yazdığı `curl` ve
PowerShell satırları ([docs/SHARING.md](docs/SHARING.md)).

## Uygulamalar

Bir **depolama eklentisi**, filex'e hiç duymadığı bir arka ucu öğretir. Bir **uygulama**
ise ona *dosyalarla yapılacak bir iş* öğretir - onları imzalamak, dönüştürmek, dışarıdan
birine göndermek - ve bilerek farklı türde bir eklentidir: **filex'in içinde, bir yalıtım
alanında çalışan bir WebAssembly modülü**; bu alan ona, kendisine verilmemiş hiçbir şeyi
vermez. Dosya sistemi yok, ağ yok, ortam yok, sunucunuzdaki hiçbir program yok: yalnızca
manifestinin istediği, hiçbir şey kurulmadan önce her biri yöneticiye sade bir dille
gösterilen host işlevleri (filex'in modüle açtığı işlevler) ve yalnızca onu çalıştıran
kişinin gerçekten seçtiği dosyalar. Bir uygulamanın isteyebileceği ağır motorlar (ffmpeg,
ImageMagick, Ghostscript, poppler, rsvg) sunucunun kendi motorlarıdır ve motor başına bir
izinle sunulur; ofis belgeleri, ofis motoru olarak bağladığınız ONLYOFFICE Document
Server'dan geçer - filex LibreOffice çalıştırmaz.

Bir uygulama ayrıca - ya da yalnızca - **kendi arayüzünü** getirebilir: yazarının yazdığı
HTML, CSS ve JavaScript, bir biçimin düzenleyicisi ya da görüntüleyicisi. filex onu,
onayladığınız (SHA-256 özetiyle sabitlenmiş) paketten **yalıtılmış bir çerçevede** sunar:
filex'in oturumunu, çerezlerini ya da sayfalarını okuyamayan opak bir köken, uygulamaya
verilen izinlere göre filex'in yazdığı bir içerik politikası (bağlantı yok, depolama yok,
form yok, açılır pencere yok) ve denetlenen tek bir mesaj kanalı; filex bu kanaldan ona
yalnızca birlikte açıldığı dosyaları verir ve onların üzerine kaydeder - yeni bir sürüm
ya da bir taslak olarak. Bu arayüz **Yeni belge**'ye kendi dosya türünü ekleyebilir,
düzenleyici sekmesinde açılabilir ve saklamanız için size bir dosya verebilir - her
seferinde izninizle. ⚠ Tarayıcılar bir sayfanın dışarıya veri göndermesini tümüyle
engelleyemez (WebRTC, içerik politikasını yok sayar; Chrome, filex'in onu kapatmasına izin
verir, Firefox'ta filex onu yalnızca sayfadan çıkarabilir - duvar değil, emniyet kemeri);
bu yüzden kurulum incelemesi bunu açıkça söyler: **arayüzü olan bir uygulamaya, onunla
açtığınız dosyalar konusunda yazarına güvendiğiniz kadar güvenin.**

Bir uygulamanın ekledikleri, diğer her şeyin durduğu yerde durur: dosya menüsünde
satırlar, filex'in onun için çizdiği ekranlar ya da kendi arayüzü, bir kopyalamayla aynı
kuyruktaki işler - ilerleme, **İptal** ve her yazma gibi sürümlenen, taranan ve
indekslenen bir sonuçla - bir dosyanın ayrıntılarında bir bölüm, gezintide **Uygulamalar**
altında bir ana ekran ve hesabı olmayan birine ihtiyaç duyduğunda, sıradan bir
**paylaşım** olan bir bağlantı: aynı listede durur, aynı PIN kilitlenmesi ve son kullanma
tarihi politikasına tabidir, diğer her bağlantı gibi onu da siz iptal edebilirsiniz. Bunu
isteyen bir uygulama ayrıca kendi zamanlanmış işini yapmak üzere saatte bir uyandırılır -
son tarihi gelince kendini kapatan ve istediğiniz hatırlatmaları gönderen bir imza isteği.

Her uygulama kod çalıştırmaz. Bir **dil paketi**, metinlerden oluşan bir manifestten
ibarettir: yalnızca manifestten kurulur - modül yok, Go yok, yayımlanan sürüm yok - hiçbir
zaman bir çalışma ortamı başlatmaz ve dilini gezgine, yönetim paneline, herkese açık
sayfalara ve sunucunun yazdığı metne ekler. **Eklentiler → Uygulamalar** onu, çalışan
sürümün ne kadarını kapsadığıyla birlikte bir *Dil paketi* olarak listeler; eksik
bıraktığı her şey İngilizce görünür. İspanyolca, Almanca ve Fransızca örnek olarak gelir;
`BRF-Tech/filex-lang-template` ise bir çevirmeni dışa aktarımdan kuruluma kadar adım adım
götürür.

Dört uygulama, kurabileceğiniz, okuyabileceğiniz ve çatallayabileceğiniz herkese açık
repolar olarak filex'le birlikte gelir. İlk ikisi modüldür; son ikisi yalnızca bir
arayüzdür, sunucuda çalışan hiçbir şeyleri yoktur:

| Uygulama | Ne ekler |
|---|---|
| **[e-İmza](https://github.com/BRF-Tech/filex-sign)** - `BRF-Tech/filex-sign` | Bir PDF'te, yalnızca PDF'te **İmzala…**, **İmza iste…**, **İmzala / Doldur** ve **Doğrula** (bir ofis belgesi önce **Dönüştür** ile PDF'e çevrilir). İstek kısa bir sihirbazdır: kimin imzalayacağı - bu filex kurulumundaki kişiler filex'in içinde imzalar, adıyla ya da e-postasıyla belirtilen diğer herkes ise siz aksini söylemedikçe PIN arkasında duran **özel bir bağlantı** alır - hangi sırayla imzalanacağı, kutuların adlandırılıp her imzacıya verilmesi, sonra sayfaya yerleştirilmesi; isteğin ne kadar süre açık kalacağı, bu sırada dosyanın **dondurulmuş** olup olmayacağı ve sonunda bir **denetim izi PDF'i** yazılıp yazılmayacağı. Sonuç, **onaylanmış ve mühürlenmiş** PAdES imzalı bir PDF'tir: ilk imza belgeyi onaylar, böylece sonrakiler yalnızca doldurabilir ve imzalayabilir; son imza atıldığında da kurulumun kendi mührüyle **dosyanın tamamını filex'in kendisi mühürler** ve dosyayı öyle kilitler ki bundan sonraki her değişiklik izin verilmeyen değişiklik olarak bildirilir. **Tam olarak o mühürlenmiş baytların SHA-256 özeti**, mührün parmak izi ve bunların nasıl denetleneceği hem isteği gönderene hem içerideki ve dışarıdaki her imzacıya gider, denetim izine de girer. İsteğe bağlı olarak, imzalı dosya bir yönetici kilidi kaldırana kadar **filex'te kilitli** kalır. **İmza anahtarları sunucudan hiç çıkmaz**: kurulumun kendi sertifika makamı (ya da içe aktardığınız bir makam) imzacı başına bir sertifika düzenler, bir imzayı atan anahtar da saniyeler sonra yok edilir - tek istisna, filex'te tutulan ve hiçbir zaman dışarı verilmeyen mühür anahtarıdır. |
| **[Dönüştür](https://github.com/BRF-Tech/filex-convert)** - `BRF-Tech/filex-convert` | Herhangi bir dosyada **Dönüştür…**: görseller, video, ses, belgeler, e-kitaplar, arşivler, veri, altyazılar ve yazı tipleri. Hedef, kategorilerine göre gruplanmış düğmelerden seçilir; sonra yalnızca o hedefi ilgilendiren ayarlar, sonra da bir inceleme gelir. Dönüştürmelerin çoğu yalıtım alanının içinde saf Go ile çalışır; geri kalanı, sunucunun motorları kuruluysa onları kullanır, eksik bir motor gerektiren hedef de sessizce ortadan kaybolmak yerine bunu söyler. |
| **[filextext](https://github.com/BRF-Tech/filextext-app)** - `BRF-Tech/filextext-app` | Tek bir `.fxtxt` dosyasında **uçtan uca şifreli bir metin çalışma alanı**: solda sayfalar ve klasörler, üstte sekmeler, ortada AFFiNE'ın BlockSuite düzenleyicisi (başlıklar, listeler, yapılacaklar, kod, tablolar, görseller, sayfalar arası bağlantılar, Markdown içe ve dışa aktarma). filex'in [şifreli klasörleri](docs/E2E-ENCRYPTION.md) ile aynı anahtarlarla ve aynı kurtarma anahtarıyla **tarayıcınızda** şifrelenir; filex şifreli veriyi saklar, parolayı da metnin tek bir sözcüğünü de hiç görmez. Bir `.fxtxt` dosyası önizleme yerine bu uygulamada açılır, **Yeni belge**'ye de *Şifreli çalışma alanı (.fxtxt)* eklenir. |
| **[draw.io](https://github.com/BRF-Tech/filex-drawio)** - `BRF-Tech/filex-drawio` | **draw.io** diyagram düzenleyicisi, filex'in içinde: `.drawio` ve `.dio` dosyaları önizleme yerine bu düzenleyicide açılır, **Kaydet** yeni bir sürüm yazar, **Yeni belge**'ye de *draw.io diyagramı* eklenir. draw.io'nun kendi dosyaları uygulamanın paketinden (SHA-256 özetiyle sabitlenmiş) sunulur; uygulama, paketin dışında hiçbir şeye ulaşmaz. |

**GitHub'dan kurun** - *Yönetim → Eklentiler → **Uygulamalar** → **Uygulama kur** → GitHub
reposu*: `BRF-Tech/filex-sign` ile sürüm etiketini yazın. filex, reponun `filex-app.json`
dosyasını okur, orada adı geçen modülü (ya da arayüzün paketini) indirir ve SHA-256 özeti
eşleşmedikçe onu reddeder, sonra **izin incelemesinde** durur. Siz her izni okuyup
*Bu uygulamanın neler yapabileceğini anladım, kurulmasını istiyorum* kutusunu işaretleyene
kadar hiçbir şey kurulmaz; verilen izinler tam olarak o listedir ve daha fazlasını isteyen
bir yükseltme yine incelemede durur. `FILEX_PLUGIN_TRUSTED_KEYS` ayarı modüllerin imzalı
olmasını zorunlu kılar (GitHub'dan kurarken imza gelmez; bu yüzden böyle bir filex
kurulumunda bunun yerine modülü imzasıyla birlikte yükleyin). Her indirme - bir uygulama,
onun güncelleme denetimi, bir depolama eklentisi - yalnızca genel adreslere gider; adres
DNS'ten sonra ve her yönlendirmede denetlenir: kendi ağınızdaki bir sunucudan kurmak için
dosyaları yükleyin. Demo modunda uygulamalar kapalıdır. Bir API anahtarı - bir ajan, bir
betik, CLI - uygulama kuramaz: **bir istek bırakır**, filex kurulacak baytları ve izinleri
dondurur, bir yönetici de isteği **Eklentiler → Kurulum istekleri** altında onaylar.

**Kim kullanabilir.** Bir uygulama **kendi izinlerini** bildirebilir - bir imza uygulaması
*İmza isteme*'yi bir izne bağlar, size gönderileni imzalamak ise izin gerektirmez - siz de
bunları filex'in kendi izinleri gibi rol ve kişi bazında verirsiniz; birinin iznine sahip
olmadığı bir işlem onun menüsünde yer almaz, istenirse de reddedilir
([Uygulama izinleri](docs/APP-PLUGINS.md#app-permissions)).

**Hiçbir şey kendini güncellemez.** filex günde bir kez (ve **Güncellemeleri denetle**'ye
tıklandığında) her uygulamanın geldiği yere - GitHub'daki sürümlerine, bir dil paketinin
dalına ya da kurulduğu manifest adresine - bu filex kurulumunun çalıştırabileceği daha
yeni bir sürüm olup olmadığını sorar ve size bildirir: yeni sürüm, bir yönetici onun
neleri değiştirdiğini - izinler, modül, arayüz dosyaları, notlar - inceleyip onaylayana
kadar *Güncelleme var* (ya da daha fazlasını istiyorsa *Onay bekliyor*) altında bekler;
sonra herkes o sürümü kullanır. ***sürüm* sürümüne dön**, yeni sürümün yerini aldığı
sürümü geri yükler. Depolama eklentileri de bir kaynağı aynı şekilde izleyebilir. Bir
uygulama hangi filex sürümleriyle çalıştığını söyler (manifestinde `"filex": ">=0.47.0"`)
ve filex onu bu aralığın dışında kurmaz.

**İşletmeci kılavuzu:** [docs/APP-PLUGINS.md](docs/APP-PLUGINS.md) - uygulamaları kurma ve
denetleme, baştan sona imza turu, dönüştürücü, zamanlanmış uyandırmalar ve bir uygulamanın
herkese açık bağlantılarını neyin koruduğu. **Uygulama yazmak** (standart Go,
`GOOS=wasip1`, bir test kitiyle): şablon repo
[BRF-Tech/filex-app-template](https://github.com/BRF-Tech/filex-app-template) ve
[docs/PLUGIN-KIT.md](docs/PLUGIN-KIT.md) ile başlayın; iletişim sözleşmesi:
[docs/APP-PLUGINS-API.md](docs/APP-PLUGINS-API.md). Öteki eklenti türü, depolama arka ucu:
[docs/PLUGINS.md](docs/PLUGINS.md).

## Özellikler

- **Çoklu depo** - aynı anda birçok depo bağlayın (yerel, S3, FTP, SFTP, WebDAV, SMB/NAS); her biri üst düzey bir klasör olarak görünür. Her biri ayrıca hiç değişmeyen bir adres taşır: WebDAV, SFTP, NFS ve S3 API'sinde yolun ilk parçası deponun adıdır, bu yüzden bir depoyu yeniden adlandırmak adresini de değiştirir - **uid**'sine göre yazılmış bir bağlama ise hiçbir yeniden adlandırmadan etkilenmez. **Birinde kopyalayın ya da kesin, ötekinde yapıştırın**: filex ağacı iki sürücü arasında akıtır, her dosyanın zaman damgasını korur ve aslını ancak kopya doğrulandıktan sonra kaldırır. Erişilemeyen bir depo saniyeler içinde bildirilir ve zaman aşımına yalnızca sessizlik uğrar, ilerlemeyi sürdüren bir aktarım asla (S3, WebDAV, FTP, SFTP ve SMB: [docs/STORAGE.md](docs/STORAGE.md#when-the-store-is-down)). Deponun, durumu hakkında yanıt veremediği bir öğe (ne "burada" ne "bulunamadı") korunur, bir **!** ve deponun kendi yanıtıyla işaretlenir ve depo yeniden yanıt verene kadar üzerinde - gezginde, REST ve ajan API'lerinde, paylaşımda ve düzenleyicilerde (dosya protokolleri işareti okumaz) - hiçbir işlem yapılmaz ([PLUGINS.md](docs/PLUGINS.md#an-entry-your-stat-cannot-answer-for)).
- **Dosyaları masaüstünüze sürükleyin** - masaüstü uygulamasında seçtiklerinizi Windows Gezgini/Finder'a ya da başka bir programa sürüklediğinizde oraya arşiv olarak değil, ayrı ayrı gerçek dosya ve klasörler olarak iner; tarayıcıda tek bir dosya aynı şekilde dışarı sürüklenir - yönetim uygulamasında da, bir dakika geçerli olan ve bir kez çalışan tek dosyalık bir bağlantıyla ([docs/DESKTOP.md](docs/DESKTOP.md#dragging-files-out)).
- **Depolama eklentileri** - filex'in hiç duymadığı bir depo, yönetim panelinden kurduğunuz **ayrı bir programdır**: yapılandırma formunu kendisi tanımlar, filex onunla küçük bir HTTP/JSON protokolüyle konuşur, sürücüsü de bundan sonra yerleşik sürücülerden herhangi biri gibi davranır. Dil fark etmez; bir Go SDK'sı işi üç metoda indirir. filex, **eklentinin bildirdiği her yeteneği sınar** - kurulurken, sonra da onu kullanan bir depoyu kaydederken yazdığınız yapılandırmayla bir kez daha - ve söylediğini yapamayan eklentiyi reddeder, çünkü yarım çalışan bir sürücünün hataları filex bozukmuş gibi görünür. Yükseltme, ikili dosyayı (binary) yerinde değiştirir ve yenisi ayağa kalkmazsa eskisine geri döner; her başlatmada ikili dosyanın özeti ve imzası yeniden denetlenir, her eklenti de başlatmalarının, hatalarının ve yanıt veremediği öğelerin yazıldığı bir **günlük** tutar (*Aksiyon → Günlük*) ([docs/PLUGINS.md](docs/PLUGINS.md)).
- **Uygulamalar** - ikinci bir eklenti türü: **yalıtılmış bir WebAssembly** modülü, **yalıtılmış bir çerçevede kendi arayüzü** (bağlantı yok, filex'in oturumuna erişim yok; bir tarayıcının söz veremeyeceği şeyler için bkz. [Uygulamalar](#uygulamalar)) ya da ikisi birden - dosya menüsüne işlemler (*İmza iste…*, *Dönüştür…*), filex'in onun için çizdiği ekranlar, bir dosyanın ayrıntılarına bir bölüm, gezintideki **Uygulamalar** altına bir ana ekran ve bir dış katılımcının hesabı olmadan açtığı bağlantılar ekler. Bir GitHub reposundan, **izin incelemesinden** geçerek kurulur - uygulama tam olarak onayladığınız şeyi alır, başka hiçbir şey almaz: dosya sistemi yok, ağ yok, sunucunuzdaki hiçbir program yok; ağır motorlar (ffmpeg, ImageMagick, …) sunucunun kendisinindir, ofis belgeleri de bağladığınız ONLYOFFICE'ten geçer ve bunların her biri ayrı bir izin olarak sunulur. filex'in çizdiği ekranlar, onları kim yazmış olursa olsun filex'in kurallarına uyar - her seçenek açılır menüde saklı değil, görünür; hiçbir şey "gelişmiş" başlığının ardına gizlenmez; her adımda tek soru. Bir uygulamanın dış imzacıya gönderdiği bağlantı sıradan bir **paylaşımdır**, bu yüzden onu diğer her şeyle aynı listede görür ve iptal edersiniz, bağlantı da hiçbir zaman onu oluşturandan fazlasını yapamaz: bu bağlantıdan başlatılan bir iş, filex'in içinden başlatılan bir işle aynı denetimlerden geçer (kapattığınız bir işlem kapalı kalır, oluşturanın belgeye erişimi yeniden okunur) ve oluşturanın hesabı devre dışı bırakıldığında bağlantı artık çalışmaz - hesap yeniden açılana kadar. Bir uygulama **kendi arayüzünü** de getirebilir - filex'in, uygulamanın onaylanmış paketinden yalıtılmış bir çerçeveye sunduğu HTML ve JavaScript; çerçevenin politikası hiçbir bağlantıya, depolamaya ve çereze izin vermez, arayüz de denetlenen tek bir kanal üzerinden filex'le konuşur; sunucuda hiçbir şeye ihtiyaç duymayan bir düzenleyici (draw.io, filextext) ise hiç modülü olmayan bir uygulamadır ([bir uygulamanın kendi arayüzü](docs/APP-PLUGINS.md#an-apps-own-interface), SDK `@brftech/filex-app-ui`). `schedule` iznini verdiğiniz bir uygulama, kendi işini kendi seçtiği dakikada yapmak üzere saatte bir, kuyrukta sıradan bir iş olarak uyandırılır. Hiçbir şey kendini güncellemez: filex her uygulamanın kaynağını her gün denetler ve daha yeni bir sürüm çıktığında bunu söyler, bir yönetici bu sürümün neyi değiştirdiğini inceler ve onaylar, herkes onaylanan sürümü kullanır - ***sürüm* sürümüne dön** ise bir onayı geri alır. Uygulamalar hangi filex sürümleriyle çalıştıklarını söyler. Bir API anahtarı asla uygulama kurmaz - bir yöneticinin onayladığı bir **kurulum isteği** bırakır - ve bir uygulama **kendi izinlerini** bildirebilir, siz de bunları rol ve kişi bazında verirsiniz ([Uygulama izinleri](docs/APP-PLUGINS.md#app-permissions)). Bir uygulama, filex'in çizmediği türler için **küçük resim çizebilir** (eline tek bir dosyanın baytları verilir, başka hiçbir şey verilmez), **Varsayılan uygulamalar** ise her dosya türünü hangi uygulamanın açacağını, küçük resmini hangisinin çizeceğini ve bunların sırasını belirler; herkes açık bırakılan açanlar arasından kendi seçimini yapar, *Her zaman bu uygulamayla aç* da hesabında saklanır - tarayıcı, masaüstü uygulaması ve gömülü gezgin için tek bir seçim ([Varsayılan uygulamalar](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail)). Dördü herkese açık repo olarak gelir: **e-İmza**, **Dönüştür**, **filextext** ve **draw.io** ([Uygulamalar](#uygulamalar), [docs/APP-PLUGINS.md](docs/APP-PLUGINS.md#the-apps-that-ship-alongside-filex), [uygulama yazın](docs/PLUGIN-KIT.md)).
- **e-İmza** ([`BRF-Tech/filex-sign`](https://github.com/BRF-Tech/filex-sign), bir uygulama) - bir PDF'i kendiniz imzalayın ya da başkalarından imzalamalarını isteyin: bu filex kurulumundaki kişiler, doğru ekranı açan bir bildirimden filex'in içinde imzalar; diğer herkes, varsayılan olarak PIN arkasında duran özel bir bağlantı alır - bu PIN'i filex sizin için saklar (bkz. *Paylaşım*). Kutular **önce tanımlanır** - ad, kimin olduğu, zorunlu olup olmadığı, tarih biçimi - ve **sonra sayfaya yerleştirilir**, iki ekranda iki soru. Belge imzadayken yöneticiler dâhil herkes için **dondurulmuş** tutulabilir; hatırlatmalar ve son tarih kendiliğinden işler; isteği gönderen, her imzacıyı dosyanın ayrıntılarında ve uygulamanın ana ekranında izler; sonuç da ilk imzasıyla **onaylanmış**, son imzasından sonra **filex tarafından mühürlenmiş**, PAdES imzalı bir PDF'tir, böylece bir PDF okuyucu sonradan yapılan her değişikliği izin verilmeyen değişiklik olarak bildirir - yanında da isteği gönderene ve her imzacıya iletilen, **mühürlenmiş baytların SHA-256 özeti** ve mührün parmak izi, istediğinizde bir **denetim izi PDF'i**, her imzacı için bir makbuz ve tamamlanan dosyayı bir yönetici kilidi kaldırana kadar kilitli tutma seçeneği gelir. **Doğrula**, imzalı her PDF için rapor verir: her imza, onay, mühür ve özeti gönderilen dosyanın bu dosya olup olmadığı. İmza anahtarları sunucudan hiç çıkmaz: kurulumun kendi sertifika makamı ya da içe aktardığınız bir makam her imzacıya bir sertifika düzenler, bir imzayı atan anahtar da saniyeler sonra yok edilir ([docs/APP-PLUGINS.md](docs/APP-PLUGINS.md#signing-documents-end-to-end)).
- **Uygulama olarak Dönüştür** ([`BRF-Tech/filex-convert`](https://github.com/BRF-Tech/filex-convert)) - herhangi bir dosyada ya da seçimde *Dönüştür…*: görseller, video, ses, belgeler, e-kitaplar, arşivler, veri, altyazılar ve yazı tipleri. Hedef, kendi kategorisinin altında bir düğmedir, sonra yalnızca o hedefi ilgilendiren ayarlar, sonra da bir inceleme gelir; rotaların çoğu yalıtım alanının içinde saf Go ile çalışır, gerisi sunucunun motorlarından geçer, eksik bir motor gerektiren hedef de sessizce ortadan kaybolmaz, durumu belirtilerek listelenir ([docs/APP-PLUGINS.md](docs/APP-PLUGINS.md#converting-files)). (Eski iframe tabanlı dönüştürücü yan konteyneri 0.48'de kaldırıldı.)
- **Protokol ağ geçidi** - aynı ağaca **S3** (SigV4; aws-cli, rclone, restic, mc, s3fs), **SFTP** (OpenSSH, WinSCP, FileZilla, sshfs), **FTPS** (açık TLS, yalnızca FTP öğrenmiş ekipman için; FTPS'e ters vekil sunucunuzun kendiliğinden yenilenen sertifikasını verin - değiştiğinde yeniden okunur), **NFSv3** (yerel ağdaki NAS istemcileri, medya oynatıcılar) ve **WebDAV** olarak erişilebilir - her birinin, ötekilerden bağımsız iptal edebileceğiniz kendi kimlik bilgisi vardır ve hepsinde arayüzdekiyle aynı izinler, çöp kutusu ve kota geçerlidir ([docs/PROTOCOLS.md](docs/PROTOCOLS.md)).
- **`filex mount`** - uzak bir filex sunucusunu sıradan HTTPS üzerinden bir klasöre bağlar: Linux'ta bir klasör, **Windows'ta bir sürücü harfi** (`filex mount Z:`, ücretsiz [WinFsp](https://winfsp.dev) gerekir). Eşitleme değildir: sınırlı bir okuma önbelleği dışında hiçbir şey kopyalanmaz, bu yüzden yüz bin dosyadan birini, gerisini indirmeden açar.
- **Gerçek zamanlı iş birliği** - kimin açık olduğunu canlı avatarlar + odakla gösteren çubuk, WebSocket üzerinden anında gelen dosya değişikliği güncellemeleri, yedek olarak yoklama. Tek bir yazma, gerçekleştiği anda duyurulur; art arda gelen yazmalar (bir zip çıkarma, bir klasör yükleme, parça parça yazan bir NFS istemcisi) her zaman aralığında tek bir mesajda birleştirilir, böylece klasör sayfayı boğmadan canlı kalır ([docs/REALTIME.md](docs/REALTIME.md)).
- **Tablo gibi davranan bir liste** - bir sütunu yeniden boyutlandırın, birini gizleyin, birini başka bir yere sürükleyin; yer kalmadığında tablo bir sütunu atmak yerine yana kayar, Aksiyon sütunu da sağda sabit kalır. Ada, türe, tarihe ya da boyuta göre, iki yönde de sıralayın; **ızgara ve liste aynı sıralamaya uyar** - bu sürüme kadar "boyuta göre sıralı" yalnızca tek bir görünüm için doğruydu ve görünüm değiştirince satırlar gözünüzün önünde yeniden sıralanıyordu. Tarihe göre sıralandığında üç görünüm de satırları **Bugün · Dün · Bu hafta · Bu ay** başlıkları altında, ardından ay ay gruplar; tarayıcının değil, **sizin** saat diliminizde.
- **Klasör, onu nasıl bıraktığınızı hatırlar** - isteğe bağlı, Kullanıcı ayarları'ndan açılır: gerçekten ayarladığınız her klasörün görünüm modu ve sıralaması **sunucuda kişi başına** tutulur; böylece başka bir makineye ve masaüstü uygulamasına sizinle birlikte gelir, aynı klasöre bakan başka kimseye de asla sızmaz. Varsayılan olarak kapalıdır; o durumda son seçiminiz her yerde geçerli olur ([docs/INTEGRATION.md](docs/INTEGRATION.md)).
- **Dosyanın sahibi kim** - her düğümün sahibi kayıtlıdır, listede bir **Sahibi** sütunu, filtre satırında da bir **Sahibi** öğesi vardır; dosya da ona en son dokunanın değil, sahibinin kotasından düşer.
- **Yaygın her biçimde arşivler** - ZIP, 7z, TAR ve TAR'ın gzip/bzip2/xz biçimlerinde
  (sunucudaki 7-Zip destekliyorsa RAR da), ZIP ve 7z'de parolayla, ilerlemesi gösterilen
  bir arka plan işi olarak arşiv oluşturun, açın ve çıkarın. Arşivin içindeki bağlar ve
  aygıtlar, hiçbir şey yazılmadan önce reddedilir; boyut ve öğe sınırları da bir arşiv
  bombasını sınırda durdurur ([docs/ARCHIVES.md](docs/ARCHIVES.md)). Katkı: Alex (@ahjephson).
- **Seçtiklerinizi yanınıza alın** - birkaç dosya ve klasör seçin; **İndir** bunları anında oluşturulan tek bir arşiv olarak akıtır: deponuza geçici bir dosya yazılmaz, sekmede hiçbir şey arabelleğe alınmaz ve 700 MB'lık bir arşiv sunucuya bir megabayttan az belleğe mal olur. **Şuraya taşı** ve **Şuraya kopyala**, bütün depoları kapsayan bir klasör seçici açar; yazamayacağınız bir hedef reddedilir - yalnızca iletişim kutusunda değil, sunucu tarafında da.
- **Yeni belge** - **+ Yeni** menüsünden bir Word, Excel, PowerPoint ya da OpenDocument dosyası, ya da metin ve kod biçimlerinin herhangi birinde bir dosya oluşturun: ad verin - `LICENSE`, `Makefile` ya da `test.conf` dâhil herhangi bir ad - nereye gideceğini seçin; belge, o türü işleyen düzenleyicide açılır. Şablonlar ikili dosyanın içine derlenmiş gerçek, minimal, geçerli belgelerdir, böylece bu özellik hiç ofis paketi olmayan bir kurulumda da çalışır; bu kurulumun sonradan açamayacağı bir tür baştan sunulmaz; iletişim kutusu nedenini de söyler. Yeni belge, ilk kaydına kadar bir **taslaktır**: siz Kaydet'e basana kadar klasörde hiçbir şey görünmez (bu arada alınmış bir ad için size sorulur - `report (2).txt`? - var olan dosyanın üzerine asla yazılmaz), belgeyi kapatırken *Diske kaydet / Taslaklarda tut / Çöpe at* diye sorulur, gezinti panelindeki **Taslaklar** da işinizin bitmediği taslakları tutar; onları başka kimse görmez - varsayılan olarak kişi başına 50, yönetim panelindeki Koruma sayfasından ayarlanır ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md#drafts-nothing-is-in-the-folder-until-you-save)).
- **RBAC + öğe izinleri** - roller (Yönetici, Kullanıcı, İzleyici ve özel roller; her biri bir izin listesi - [docs/PERMISSIONS.md](docs/PERMISSIONS.md)), **Yönetim → Klasör erişimi** altında üst klasörden devralınarak uygulanan dosya/klasör bazlı yetkiler, bütün üyeleri için klasör erişimi ve bir rol taşıyan **gruplar** - üyeler elle eklenir ya da oturum açmanın taşıdığı gruplarla eşitlenir ([docs/GROUPS.md](docs/GROUPS.md)) - e-postayla (SMTP) paylaşım davetleri, yetkiye duyarlı arama ve listelemeler. **Benimle paylaşılanlar**, sorunun tersini alıcının gözünden yanıtlar - başkalarının size neler için yetki verdiğini ve hangi depolara yalnızca bir yetki üzerinden ulaştığınızı.
- **Kabuk** - işletmeci için de son kullanıcı için de, yönetim uygulamasında, masaüstü uygulamasında ve her gömülü gezginde tek bir yerleşim: sol kenarında daraltma düğmesi ile ürün logosunun bulunduğu, tam genişlikte bir üst çubuk, ⌘K / Ctrl+K çipi sorguyu komut paletine devreden tek bir **arama alanı** (alan bu klasörde arar; "Her yerde", kayıtlı aramalar ve komutlar ise palette durur), birincil bir **+ Yeni** menüsü (dosya yükle · yeni klasör · **yeni belge** · dosya iste), konum yolunun altında bir **Tür · Sahibi · Değiştirilme · Boyut** filtre satırı, ızgara görünümünde başlıklı bölümler olarak **Klasörler** ve **Dosyalar**, **Ayrıntılar** ("Erişimi olan kişiler" ve bir paylaşım bağlantısı satırıyla) ve **Etkinlik** (sürüm geçmişi ve yorumlar) olarak bölünmüş bir ayrıntılar paneli ve gezintinin altında bir **depolama satırı**. Tema, palet, dil, yoğunluk, saat dilimi, açılış sayfası ve bildirim anahtarlarının hepsi, avatardan ulaşılan **Kullanıcı ayarları**'nda durur - web uygulaması da tema, palet, yoğunluk ve dil seçiminizi tarayıcıda değil, **hesabınızda** tutar, böylece bir sonraki tarayıcıda sizi hazır bekler; kısayol düzenleyicisi ve *Turu tekrar başlat* da aynı menüdedir. Derlemeden hiçbir şey çıkarılmaz - ayarlar iletişim kutusu olmayan gömülü gezgin, bunları yine de barındıran bir "⋯" menüsü taşır ([docs/INTEGRATION.md](docs/INTEGRATION.md)).
- **Ana sayfa, kabuğun içinde** - yöneticiler dâhil herkes için açılış görünümü: depolarınız, en son açtıklarınız ve yıldızladıklarınız, içerik alanında kartlar olarak, dosyalardakiyle aynı gezinti paneli ve aynı üst çubukla. Ana sayfa ile bir klasör arasında geçiş yapınca içerik değişir, başka hiçbir şey değişmez. Açılış sayfası olarak yönetim panelini tercih eden işletmeci, bunu kullanıcı ayarlarından seçer.
- **Gezinti paneli** - birincil işlem olarak **+ Yeni** menüsü, Ana sayfa / Dosyalarım / Benimle paylaşılanlar / **Paylaştıklarım** / Son kullanılanlar / Yıldızlılar / **Taslaklar** / Çöp kutusu hedefleri, görebildiğiniz depolar - **kendi sıranızla** (bir satırı sürükleyin ya da menüsünden Yukarı taşı / Aşağı taşı / Ada göre sırala seçeneklerini kullanın; hesabınızda saklanır), yoksa yöneticinin Depolar sayfasında belirlediği sırayla ([docs/STORAGE.md](docs/STORAGE.md#ordering-storages)) - kurulu bir uygulamanın ana ekranı varsa bir **Uygulamalar** bölümü ve **Nasıl bağlanılır** + **API anahtarları**: her protokol için ayrı kılavuzlar ve kullanıcının kendi API anahtarlarını yönettiği ekran; bunlar gezginin içinden açılır, böylece gömülü bir kopyanın kullanıcıları WebDAV/FTPS/`filex mount` için gereken kimlik bilgisini bir yöneticiden istemek yerine kendileri oluşturabilir. Üst çubuktan bir simge şeridine daraltılabilir (her tarayıcıda ayrı hatırlanır), 560px'in altında sütun yerine çekmece olur. Web uygulamasında, masaüstü uygulamasında ve her gömülü gezginde varsayılan olarak açıktır; `uiProfile: 'simple'` ayarı ayrıca sekme şeridini, bölünmüş görünümü, galeri görünüm modunu ve "Nasıl bağlanılır" bölümünü kapatır, ama hiçbirini derlemeden çıkarmaz ([docs/INTEGRATION.md](docs/INTEGRATION.md)).
- **Paylaşım** - PIN'li, son kullanma tarihli ve indirme limitli herkese açık bağlantılar, yöneticinin belirlediği **en uzun bağlantı süresi** içinde (varsayılan 7 gün - iletişim kutusu yalnızca sunucunun tutacağı süreleri sunar); klasör bağlantıları ZIP olarak akıtılır (önbelleğe alınır, bir boyut üst sınırına kadar önceden hazırlanır, bir hafta sonra süpürülür); gelen dosyalar için **dosya isteği** yükleme bağlantıları; ShareX uyumlu yükleme ucu. **Paylaştıklarım**, oluşturduğunuz bağlantıları - yalnızca yöneticiler için değil, herkes için - *Bağlantıyı kopyala*, *PIN'i kopyala* ve *İptal et* ile birlikte listeler: bir bağlantının PIN'i, onu koruyan özetin yanında mühürlenerek saklanır; böylece biri ona tekrar ihtiyaç duyduğunda bağlantıyı oluşturan kişi ya da bir yönetici PIN'i yeniden okuyabilir, her okuma da denetim kaydına yazılır. Beş yanlış PIN, herkese açık herhangi bir bağlantıyı on dakikalığına kapatır. Bir indirme bağlantısı, bir dosya isteği ve bir uygulamanın sayfası **kurumsal kimliğinizi taşıyan tek bir herkese açık ekrandır** - kurulumunuzun adı, logosu ve renkleri, tek bir PIN ekranı, tek bir son kullanma tarihi mantığı ve bir dil seçici ([docs/SHARING.md](docs/SHARING.md)). Listelenen her bağlantı nerede durduğunu söyler - etkin, süresi dolmuş, hakkı tükenmiş ya da iptal edilmiş - ve yeni bir bağlantı kendi `curl` ve PowerShell indirme komutuyla gelir. Paylaşım penceresindeki e-posta satırından gönderilen bir bağlantının iletisini sunucu bağlantının kendisinden, her alıcının dilinde yazar ve bu ileti bağlantının PIN'ini asla taşımaz ([Bağlantıyı e-postayla göndermek](docs/SHARING.md#emailing-a-link)).
- **Masaüstü uygulaması + klasör eşitleme** - Windows/Linux/macOS uygulaması: sistem tepsisinde duran çift yönlü eşitleme, **seçmeli eşitleme** (sağ tık → *Bilgisayarda tut*, hesap başına tek bir kök klasör, gerisi yalnızca çevrimiçi), aynı anda birden çok hesap, sunucunun düzenleyicisinde **Office belgelerini kendi diskinizden açar**, kendini günceller (macOS: imzasız derleme, imzalanana kadar yeniden indirilerek güncellenir), ekrandaki hesabın dilinde. Her belge **kendi penceresinde** açılır (başlığı dosyanın adıdır), pencereler **çerçevesizdir** ve uygulamanın kendi düğmelerini taşır (macOS'te sistemin kendi trafik ışıkları), açmak için tek tık mı çift tık mı kullanılacağını da **Ayarlar → Dosyaları açma** belirler ([docs/DESKTOP.md](docs/DESKTOP.md), [docs/SYNC.md](docs/SYNC.md)).
- **Çöp kutusu ve sürüm geçmişi** - silmeler bir saklama süresi içinde geri alınabilir, yazmalar anlık görüntü bırakır; ikisi de zaten bağladığınız depoda durur ([docs/TRASH-VERSIONING.md](docs/TRASH-VERSIONING.md)). Çöp kutusu tuttuğu her şeyi sayfa sayfa gösterir; *Çöp kutusunu boşalt* da önce tam olarak neyi sileceğinin sunucudaki sayısını ve boyutunu gösterir.
- **Yazma koruması** - yazılan her dosya için isteğe bağlı ClamAV taraması - yerleşik düzenleyicinin yazdıkları da, filex üzerinden gelmeyip depo senkronunun arka uçta bulduğu dosyalar da dâhil - ClamAV'a yerel bir ikili dosya ya da ağ üzerinden bir clamd konteyneriyle ulaşılır; ayrıca çöp kutusu/sürüm saklama, tek bir yönetim bölümü altında. Aç/kapat anahtarı, virüs tarayıcısının modu ve adresi, boyut üst sınırı ve düzenleyici kayıt tarama penceresi **Ayarlar → Koruma** sayfasında durur; `FILEX_CLAMAV*` değişkenleri ilk açılışta bu ayarların başlangıç değerlerini verir, sonra aradan çekilir (virüs tarayıcısının ikili dosya yolu bilerek yalnızca ortamda bırakılır - bu sunucunun çalıştırdığı bir komuttur) ([docs/PROTECTION.md](docs/PROTECTION.md)).
- **Uçtan uca şifreli klasörler** - istemci tarafında WebCrypto; sunucu şifreli veri saklar ve hiçbir zaman anahtar almaz. Klasörün bir **seviyesi** vardır: yalnız içerik (varsayılan - WebDAV, CLI ve masaüstü eşitlemesi klasördeki adlarla çalışmayı sürdürür) ya da **içerik ve adlar** (AES-SIV, böylece sunucu okunabilir hiçbir ad tutmaz); seviye sonradan, kaldığı yerden devam edilebilecek biçimde, klasörün **Şifreleme ayarları**'ndan yükseltilebilir, parolası da orada değiştirilir. Üçüncü seviye olan **kasa** ağacın şeklini de gizler - sunucu yalnızca aynı boyuttaki paketleri ve şifreli bir dizini saklar, bir anda tek bir kişi, sunucunun tuttuğu bir kilit altında yazar; kasa hazırdır ve varsayılan olarak kapalıdır (`FILEX_E2E_VAULT`), onu web ve masaüstü uygulamaları, `filex decrypt` ve `filex vault mount` açar ([kasanın biçimi](docs/E2E-VAULT-FORMAT.md)). Zaten var olan bir klasörünüz **yerinde şifrelenir**, 200 MB'tan büyük dosyalar dâhil; **tek bir dosya da kendi başına şifrelenebilir** (kendi parolası ve kurtarma anahtarı olan, kendi kendine yeten bir `.fxe` dosyası); her boyuttaki dosya akış olarak şifrelenir; kilidi açık bir klasör, tarayıcıda oluşturulan **çözülmüş bir zip** olarak iner; `filex decrypt` komutu indirilmiş bir klasörü ya da `.fxe` dosyasını kendi makinenizde açar, **`filex encrypt`** komutu ise diskteki bir klasörden şifreli bir klasör oluşturur ya da sunucudaki bir klasörü bulunduğu yerde şifreler - bir sekmeye sığmayacak kadar büyük klasörler için, kaldığı yerden devam edebilir, anahtarlar makinenizde üretilir ([docs/CLI.md](docs/CLI.md#filex-encrypt---make-a-folder-an-encrypted-folder)). Her klasöre, bir kez gösterilen bir **kurtarma anahtarı** verilir, böylece unutulan bir parola kendiliğinden veri kaybı anlamına gelmez; işletmeci isteğe bağlı olarak **anahtar emanetini** etkinleştirebilir - kurulumda ya da çalışan bir kurulumda sonradan devreye alınarak; var olan klasörlere kendiliğinden ulaşmaz, ama sahiplerine kilidi açarken bu seçenek sunulur - ve kullanıldığında klasörün sahibine bildirilir ([docs/E2E-ENCRYPTION.md](docs/E2E-ENCRYPTION.md)). **Kim şifreleyebilir**, kurumun kararıdır: platform işletmecisinin kiracı başına bir aç/kapat anahtarı, bir kiracı politikası (kapalı, yalnız yöneticiler, rolü izin veren herkes ya da **yönetici onayıyla** - tek bir kişi, tek bir klasör ve tek bir şifreleme türü için bir kereliğine onaylanan, gerekçeli bir istek) ve şifreli yeni bir şey oluşturabilecek her kapıda, kopyalama dâhil, aranan `files.encrypt` izni ([docs/E2E-ENCRYPTION.md](docs/E2E-ENCRYPTION.md#who-may-encrypt)).
- **Yerleşik çok kiracılılık** - tek bir kurulumda, her kiracının yalıtıldığı sağlayıcı/kiracı modu. Her kiracının bir **realm**'i vardır - oluşturulurken verilen ve hiç değiştirilmeyen oturum açma adı - böylece her oturum açma, hangi kiracıya ait olduğunu ya o kiracının kendi adresiyle (web sayfası, WebDAV `Host`, FTPS sertifikasının adı) ya da realm ile belirtir: oturum açma formunda bir **Realm** alanı, SFTP üzerinden `realm/name`. Hesap araması kiracının dışına hiç çıkmaz; platformun sayfasında yazılan realm kendi adresi olan bir kiracınınsa, tek kullanımlık, 60 saniyelik bir biletle oraya **aktarılır** ([docs/MULTI-TENANCY.md](docs/MULTI-TENANCY.md), [realm'ler](docs/MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)). Kiracılar, **Yönetim → Kiracılar** ve **Kiracım** sayfalarında kendilerini yönetir: bir ya da birkaç kiracıya bağlı oturum açma sağlayıcıları, kiracının kendi OIDC'si ve LDAP'ı, her kiracı için bir platform alt alan adı ve bir CNAME ile kanıtlanan kendi alan adları; bunların sertifikasını vekil sunucu ya da filex'in kendisi (ACME) sağlar, ya da kiracının kendi sertifikası kullanılır ([docs/TENANT-ADMIN.md](docs/TENANT-ADMIN.md)).
- **Her şey takılabilir sürücülerle** - depolama / kimlik doğrulama / veritabanı / kuyruk sürücüleri ortam değişkenleriyle isteğe bağlı olarak açılır (`FILEX_AUTH_DRIVERS=local,oidc`, `FILEX_QUEUE_DRIVER=postgres`, …); işletim sistemiyle oturum açma (`windows`, `pam`) bunun istisnasıdır, testini geçtikten sonra yönetim panelinden açılır.
- **Önce OIDC SSO** - kimlik sağlayıcınıza isteğe bağlı otomatik yönlendirme, yanında kurtarma amaçlı yerel oturum açma (`?local=1`); yönetici rolü de her oturum açmada bir kimlik sağlayıcı grubunu izler.
- **LDAP / Active Directory** - dizin hesapları yerel hesaplarla aynı parola formunda, WebDAV, SFTP ve FTPS'te de aynı parolayla oturum açar (S3 ve NFS, bu hesapların oluşturduğu anahtarları ve export'ları kullanır); özel CA desteği, ayrıca `local` ilk sırada kalır, böylece dizin çalışmazken `admin@local` çalışır. Bir hesabın e-postası her zaman bir adrestir: kaydın e-posta niteliği, o yoksa `name@domain` biçiminde yazılmış bir ad, o da yoksa `name@local` (bir kiracının realm'inde `name@<realm>.local`; işletim sistemi sağlayıcılarının da kullandığı tek kural); eski bir filex sürümünün yalın adla açtığı bir hesap, bir sonraki oturum açışında dosyaları, paylaşımları ve rolü değişmeden **devralınır** ([docs/LDAP.md](docs/LDAP.md)).
- **Replika + uzlaştırma** - birincil→replika yayılımı (her yol glob'u kuralı için aynala / sadece ekle / atla), okumada yedeğe düşme, zamanlanmış durum raporu, tek tıkla "Tümünü onar".
- **Kalıcı işlem kuyruğu** - kendi veritabanınızda (SQLite / Postgres / MySQL) ya da Redis'te duran, yeniden başlatmaya dayanıklı kuyruk; yeniden denemeli işçi havuzu + iptal + yönetim paneli. Her sürücü önceliğe göre sıralar; böylece birinin az önce yüklediği bir dosyanın antivirüs taraması, ilk içe aktarmanın kuyruğa aldığı yirmi bin taramadan önce işlenir. Ayarlanmadığında sürücü, varsayılan olarak SQLite'ı seçmek yerine veritabanını izler - SQLite ifadelerini bir Postgres sunucusuna yöneltmek, her yoklamada bir sözdizimi hatası demektir ve hiçbir iş asla çalışmaz.
- **Veritabanı destekli dosya ağacı** - listelemeler depolama arka ucundan (~100 ms) değil, veritabanı önbelleğinden (1-5 ms) gelir; dönemsel bir senkron, filex dışından yapılan değişiklikleri arka ucun etag bildirdiği yerde etag ile, bildirmediği yerde boyut + değiştirilme zamanı ile yakalar. Bir deponun **Taramadan hariç tutulacak yollar** alanı (`.*`, `downloads/incomplete/**`, `*.tmp`), var olan bir ağacın, filex'in işine yaramayan kısımlarını taramanın, kataloğun, arama indeksinin ve virüs tarayıcısının dışında tutar - erişim denetimi değil, maliyet denetimi ([docs/STORAGE.md](docs/STORAGE.md#scan-exclusions)).
- **Büyük yerel ağaçlar için tembel katalog** - `sync_mode: lazy` ayarı baştaki taramayı atlar: açtığınız klasör hemen, doğrudan diskten listelenir ve önce o kataloglanır, gerisi ise kullanıcılara yol veren yavaş bir arka plan geçişiyle kataloglanır (ya da yalnızca klasörler açıldıkça). Açılan klasörler bir bütçe dâhilinde izlenir, kimsenin girmediği bir klasör asla silinmiş sayılmaz; arama, klasör boyutları ve kullanım da henüz her şeyi kapsamadıklarında bunu açıkça söyler ([docs/STORAGE.md](docs/STORAGE.md#lazy-catalogue), [tasarım](docs/LAZY-CATALOGUE.md)). Fikir: Alex ([#45](https://github.com/BRF-Tech/filex/issues/45)).
- **Görüntüleyiciler ve düzenleyiciler** - görsel/video/ses, PDF, Markdown (bölünmüş düzenleyici + önizleme), CSV (ONLYOFFICE yapılandırılmışsa onun hesap tablosu, değilse salt okunur bir tablo), kod (Monaco), OnlyOffice üzerinden Office, Drawio + Mermaid diyagramları, 3D modeller. ONLYOFFICE'in yalnızca daha yeni bir biçimde kaydedebildiği bir belge (düzenlenip DOCX olarak kaydedilen bir `.doc` dosyası) aslının **yanında**, doğru uzantıyla saklanır, asla onun üzerine yazılmaz; belgeyi düzenleyen kişilere de bildirilir ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md#a-save-in-another-format)). OnlyOffice'in **Şimdi test et** düğmesi, bir belgenin kullandığı kapının aynısından indirme yapar ve belge sunucusu JWT'yi zorunlu tutmuyorsa uyarır; *Download failed* ("indirme başarısız") iletisinden sonra da düzenleyici, bu iletinin ardındaki iki hatadan hangisinin yaşandığını söyler ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md#failure-editor-shows-download-failed)). Düzenleyici her kişinin kendi dilinde açılır ya da yöneticinin **Dış servisler → ONLYOFFICE** altında herkes için seçtiği dilde (`FILEX_ONLYOFFICE_LANG`) ([Düzenleyicinin dili](docs/ONLYOFFICE.md#the-editors-language)). Bir dosya türü için birden çok uygulama ya da görüntüleyici varsa **Birlikte aç** ve **Uygulama seç…** ile birini seçersiniz; *Her zaman bu uygulamayla aç* seçimi hesabınızda saklanır.
- **Bildirimler** - genel JSON webhook'ları (Slack/Discord'dan bağımsız): istediğiniz sayıda hedef, her birinin kendi imza gizli anahtarı ve olay bazında kendi aboneliği; ayrıca okunmuş/okunmamış durumu ve kullanıcı başına sessize alma matrisi olan bir uygulama içi çan. Okunmamışların sayısı **çandaki bir rozet** olarak görünür - 99'a kadar kesin sayı, üstünde `99+`, sistemde dock simgesi varsa masaüstü uygulamasının simgesinde de - bir satır yalnızca gidecek bir yeri varsa tıklanabilir (imza isteği bir bildirimler sayfasını değil, imzalama ekranını açar), **Tüm bildirimleri gör** de bildirimlerinizin hepsini gezginin üzerinde açar; yalnızca yöneticiler için değil, herkes için. Bir dosya **oluşturan** yazma ile bir dosyanın **yerine geçen** yazma ayrı olaylardır (`file.uploaded` / `file.updated`); işletmecinin en çok ayrı almak istediği olaylara da - virüslü bir yüklemenin karantinaya alınması, bir yüklemenin başarısız olması, şifreli bir klasörün kurtarma anahtarıyla açılması - tek tek abone olunabilir ([docs/NOTIFICATIONS.md](docs/NOTIFICATIONS.md)). **Her bildirimi sunucu söyler**: çan, masaüstü uygulamasının bildirimi, anlık bildirim ve e-posta aynı cümleyi okuyanın hesap dilinde gösterir; bir webhook kendisi için ayarlanan dilde bilgilendirilir ve kendisi çeviren bir alıcı için iletiyi çevrilmemiş hâliyle de alır ([Bir bildirim ne söyler](docs/NOTIFICATIONS.md#what-a-notification-says)). **Web Push** (kullanıcı ayarlarında *Bu cihazda anlık bildirim*) bildirimleri filex kapalıyken bir telefona ya da tarayıcıya getirir (iPhone ya da iPad'de, Ana Ekran'a eklenmiş web uygulaması, iOS 16.4 ve sonrası): çanla aynı türler, aynı sessize almalar ve aynı özet ([Web Push](docs/NOTIFICATIONS.md#web-push)).
- **Arama** - gömülü Bleve, tam metin + üst veri, yetkiye duyarlı. VS Code tarzı dosya adı puanlaması: klasörler hesaba katılır, sözcük sırası katılmaz (`main code` sorgusu `Code/main.go` dosyasını bulur), ayırıcılar ve yazım hataları hoş görülür (`invoice 2026` sorgusu `invoice_2026.pdf` dosyasını, `mian.go` sorgusu `main.go` dosyasını bulur) ama sayılar birebir eşleştirilir (`2026` asla `2025` anlamına gelmez), `tag:` filtreleri, tam eşleşmeler ilk sırada. Bir arama, sınırı saymaya başlamadan önce sunucuda daraltılır - türe, MIME türüne, tarihe, boyuta, klasöre ve sahibine göre - ve yanıt kaç sonuç bulunduğunu söyler ([Aramayı daraltmak](docs/SEARCH.md#narrowing-a-search)). Bir ⌘K sonucu indirilebilir (klasör, tek bir zip olarak) ya da durduğu yerden dışarı sürüklenebilir ([docs/SEARCH.md](docs/SEARCH.md)).
- **Okunabilen küçük resimler**: PDF, başlığı kartta kalsın diye üstten hizalanmış **ilk sayfasını** gösterir; video, siyah olmayan ilk karesini (siyahtan açılarak başlayan bir video eskiden siyah bir kare üretirdi, bir saniyeden kısa bir klip ise satır hâlâ "hazır" derken hiçbir şey üretmezdi); Office belgesi, çizilmiş ilk sayfasını; metin, kod ya da CSV dosyası ise satırın zaten yazdığı uzantıyı yinelemek yerine **kartı kendi ilk satırlarıyla doldurur**. Görsel, video (ffmpeg), PDF (ghostscript), Office (bağlı OnlyOffice); sunucunun yeteneklerine göre davranır ve bu ikili dosyalardan biri eksikse sunucu artık sessizce renkli dikdörtgenler çizmek yerine bunu açılışta günlüğüne yazar. Önbelleğe alınmış küçük resim, ait olduğu dosya kalıcı olarak silindiğinde serbest bırakılır, dönemsel bir uzlaştırıcı da eski bir kurulumun biriktirdiği sahipsiz kalanları geri alır. Küçük resim **dosyasını izler**: filex'in dışında değişmiş ya da hiç resmi olmamış bir dosya, bir listeleme ya da senkron onu gördüğünde yeniden çizilir; **SVG** her kurulumda yerleşik bir motorla (bir yöneticinin belirlediği boyut ve süre sınırlarıyla), **HEIC/AVIF** fotoğrafları ise ImageMagick ile çizilir; saydam resimler dama deseni üzerinde durur; **klasör, içine en son gelen dosyaları gösterir**, bunlar ızgarada, galeride ve listede klasörle birlikte çizilir, üzerine gelince de klasör, içinde ne olduğunu gösterir (bir yönetici bunu kapatabilir); metin dosyaları ilk satırlarını, arşivler ise içindekileri gösterir; aracı eksik olan dosya adıyla belirtilir, üstü örtülmez; **Yönetim → Araçlar → Küçük resim onarımı** ise bir dosyayı, bir klasörü ya da bir depoyu istendiğinde yeniden çizer ([docs/thumbnails.md](docs/thumbnails.md)).
- **Sekmeler, temalar ve derin bağlantılar** - yan yana açık birkaç klasör, aydınlık/karanlık/otomatik tema ve açık klasörü izleyen bir adres çubuğu, böylece yapıştırılan bir bağlantı o klasöre götürür. Tema galerisinde sekiz palet hazır gelir; her biri ikinci bir stil dosyası değil, `--fe-*` değişkenlerinin bir eşlemesidir, böylece barındıran bir sayfa ya da gömülü bir gezgin birini seçebilir - ya da kendi değerlerini verebilir - ve bunun için hiçbir CSS'i çatallaması gerekmez; işletmeci kendi paletlerini ekleyebilir (bkz. *Görünüm*).
- **Görünüm: sizin renkleriniz, her yerde** - yönetim panelindeki **Görünüm** ekranı, siz yazdıkça önizlenen adlandırılmış temalar oluşturur - açık ve koyu için on iki renk, köşe yarıçapı, yazı tipi yığını - ve birini **kurulum varsayılanı** yapar. Renkli bir düğmenin üzerindeki metin beyaz varsayılmaz, kontrasta göre seçilir; paletin geri kalanı sunucuda türetilir ve tema, oturum açma sayfasına ve herkese açık her bağlantıya ulaşır - kendi tonlarıyla ya da bu iki sayfaya özel olarak verdiğiniz renklerle - çünkü oturum açma sayfasına gelince duran bir marka, marka değildir: oturum açılmamış bir sayfa kurulum varsayılanını taşır, o tarayıcıyı en son kullanan kişinin paletini asla taşımaz ve oturum açmış kişinin kendi seçimi önceliklidir. Temalar tek bir JSON dosyası olarak dışa ve içe aktarılır. Onun yanındaki tehlikeli araç **özel CSS**'tir; artık siz açana kadar kapalıdır, oturum açmamış hiç kimseye sunulmaz, hiçbir şey çekemez ve onu kapatan ekrana ulaşamaz ([docs/INTEGRATION.md](docs/INTEGRATION.md#themes)).
- **Her yerde tek tablo** - filex'te geriye tek bir tablo kaldı, gezgininki; diğer her liste de o tablodur: yönetim panelinin menüleri, **Paylaştıklarım**, bir uygulamanın kendi ekranları. Her biri ilk sütununu solda, işlemlerini sağda dondurur, aynı biçimde boyutlandırılır, yeniden dizilir ve sıralanır, her satırı da o satırda yapılabilecek her şeyi içeren **sabitlenmiş tek bir Aksiyon menüsü** ile bitirir - gezgindeki ⋮ ile açılan menünün aynısı, böylece ikinci bir tablo birincisinden ayrışamaz. Ana ekranı olan kurulu bir uygulama, panelin gezintisinde **Uygulamalar** altında kendi satırıyla yer alır.
- **İçinde yolunuzu bulabileceğiniz bir yönetim paneli** - yöneticinin sayfaları üst çubuktaki bir mega menüde durur: önce Panel sayfası, ardından **Dosyalar ve depolama**, **Kişiler ve güvenlik** ve **Sistem**; bunların her biri adlandırılmış bölümlerden oluşan bir paneldir, her sayfanın altında da kısa bir satır bulunur. Her sayfa iki tık uzakta ve her zamanki adresindedir; yetki devredilen bir yöneticiye yalnızca izinlerinin açtığı sayfalar sunulur, klavye ve ekran okuyucular desteklenir, telefonda ise aynı sayfalar bir çekmecede liste olarak yer alır. Menünün yanındaki **arama** (Ctrl+K, telefonda bir düğme) bir sayfayı, tek bir ayarı, bir kişiyi, grubu, API anahtarını, uygulamayı, depoyu ya da paylaşımı arayüzün dilindeki ya da İngilizce adıyla bulur; dosyaları istendiğinde (`file:`), ve yalnızca açabildiklerinizi ([Yönetim paneli](docs/ADMIN-PANEL.md), [Arama](docs/ADMIN-PANEL.md#search)).
- **Sembolik bağlar, depo sınırında** - `local` bir deponun içindeki, yine o deponun içini gösteren bir bağ izlenir ve gösterdiği şey olarak açılır; deponun dışına çıkan bir bağ ise **bir rozetle ve nedeniyle listelenir**, okuma, yazma ve silmede reddedilir - o depo için *Bu klasörün dışına çıkan sembolik bağları izle* seçeneğini açmadığınız sürece ([docs/STORAGE.md](docs/STORAGE.md#symlinks)).
- **Her cihazda alışıldığı gibi açılır** - **fareyle** tek tık seçer, **çift tık açar** (Enter seçili olanı açar) - klasik dosya yöneticisi hareketi ve kişiye özel bir tercih (`ExplorerConfig.openTrigger`, varsayılan `'double'`; masaüstü uygulaması bunu **Ayarlar → Dosyaları açma** olarak sunar, `'single'` değeri ise tek tıkla açmayı geri getirir). **Dokunmatik ekranda** tek dokunuş her zaman açar - üzerine gelince seçme yoktur. Her cihazda seçim yapan tek tık ya da dokunuş **onay kutusundadır** (Shift aralığı genişletir), sağ tık ya da uzun basma ise menüyü açar; liste satırları, ızgara kartları ve galeri karoları, hepsi bu kutuyu taşır.
- **Klavyeyle kullanılır, tuşunu da yazar** - sağ tık menüsündeki ve araç çubuğundaki her eylem, kendisini çalıştıran tuşu yazar; tuş kısayol kaydından okunduğu için yeniden atamaya uyar. Otuz iki işlemin tuşu *Kısayol ayarları* üzerinden yeniden atanabilir (her tarayıcıda ayrı saklanır); tarayıcının kendine ayırdığı, `Ctrl+W` gibi bir avuç kombinasyon, hiç çalışmayacak bir tuş olarak saklanmak yerine nedeni belirtilerek reddedilir.
- **Kullanım ve maliyet** - filex sağlayıcınızın faturasını kendisi ölçmez; sağlayıcının zaten yazdığı raporu okur, normalleştirir ve düzenleyebileceğiniz bir tabloyla fiyatlandırır. Backblaze B2'nin günlük CSV'leri, filex'in zaten konuştuğu aynı S3 API'si üzerinden okunur; yani yeni bir bağımlılık da yeni bir kimlik bilgisi türü de yok. Ücretsiz kullanım hakları bir formüldeki sabitler değil, ayrı alanlardır ve sayfa, sağlayıcının hesap düzeyindeki satırını kova (bucket) başına satırlarından ayrı tutar - ikisini toplamak aynı işlemleri iki kez sayar; tam da kimsenin fark etmeyeceği kadar ([docs/USAGE.md](docs/USAGE.md)).
- **Denetim kaydı** - her değişiklik, onu yapanla, entegrasyon kimliğiyle ve üst verisiyle birlikte kaydedilir.
- **CLI istemcisi** - aynı ikili dosya, sunucu tarafında eklenti gerekmeden uzak bir sunucuya ulaşır (`filex client`, `filex sync`): depolar arasında kopyalama ve taşıma, çöp kutusu, sürümler, etiketler, uygulama işlemleri, arşivler ve bağlantılarınız, her sunucu işi sonuna kadar izlenerek; `filex client login --realm` komutu bir kiracıda oturum açar, `filex encrypt` komutu şifreli klasörler oluşturur, `filex vault` bir kasayı bağlar ve toparlar, `filex sync run --json` motorun olaylarını bir programa verir, bir ret sunucunun kendi cümlesiyle yazdırılır ve kaydedilmiş bir oturum, kaydedildiği adresten başka hiçbir yere gönderilmez ([docs/CLI.md](docs/CLI.md)).
- **Kendini günceller** - ara sürümler tek tıkla yükseltme için duyurulur, yama sürümleri ise siz izin verince kendiliğinden kurulur (`AUTO_UPGRADE=true`; varsayılan olarak filex yalnızca denetler ve size bildirir); paket yöneticisine ait bir kuruluma (Homebrew, winget, Snap, bir dağıtım paketi) ya da bir konteynere yeni sürümler ve onları alacak komut bildirilir, yönetim sayfası da yalnızca duyuracağını söyler ([docs/UPDATES.md](docs/UPDATES.md)).
- **Tek ikili dosya** - goreleaser matrisi: linux/macOS/Windows × amd64/arm64. CGO=0, modernc.org/sqlite.
- **i18n** - İngilizce + Türkçe hazır gelir, **herkese açık bağlantılar dâhil**: bir
  paylaşım bağlantısı, bir PIN ekranı, bir dosya isteği sayfası ya da bir uygulamanın
  imzalama ekranı ziyaretçinin dilinde görüntülenir ve herkese açık kabuk **bir dil
  seçici sunar**, çünkü bir yabancının tarayıcı dili yalnızca bir tahmindir ve
  sözleşme okuyan kişi bunu düzeltebilmelidir. JS'siz düz sayfalar önce `?lang=`
  parametresine, sonra `Accept-Language` başlığına, sonra sunucu varsayılanına bakar.
  **Sunucunun yazdığı metin de aynı katalogdan gelir** - e-posta, bildirim ifadeleri,
  JavaScript'siz sayfalar ve kurulum sırasındaki izin incelemesi - her biri, öteden
  beri hitap ettiği okura hitap eder ve anahtar bazında İngilizceye döner; yer
  tutucuları İngilizcedekilerle eşleşmeyen bir çeviri ise çalışma zamanında
  kullanılmaz, böylece bir e-posta, bağlantısını ya da PIN'ini asla yitirmez. Oturum
  açmış bir kişinin tek bir dili vardır, hesabınki: ekran ve her kanaldaki bildirimler
  onu izler, bir yönetici bir dili sabitlemedikçe ONLYOFFICE düzenleyicisi de.
- **Dil paketleri** - diğer her dil, **çalışan hiçbir şeyi olmayan bir uygulamadır**:
  metinlerden oluşan bir manifest, diğer her uygulama gibi bir GitHub reposundan, yüklenen
  bir dosyadan ya da bir URL'den kurulur ve **Eklentiler → Uygulamalar** altında,
  çalışan sürümün ne kadarını kapsadığıyla listelenir (*Español - %97 çevrilmiş ·
  gerisi İngilizce görünür*). Paketin dili her dil seçiciye eklenir - ayarlar
  iletişim kutusu, yönetim panelinin üst çubuğu, herkese açık paylaşım sayfaları -
  ve gezgini de yönetim panelini de herkese açık sayfaları da çevirir. Çoğul
  biçimleri **CLDR kategorilerine** uyar; böylece bir paket `zero`, `one`, `two`,
  `few`, `many` ve `other` biçimlerinden kendi dilinde bulunanları yazar. İspanyolca,
  Almanca ve Fransızca örnek olarak gelir; bir şablon repo ile `scripts/i18n-export.mjs` /
  `i18n-validate.mjs` betikleri de bir çevirmeni dışa aktarımdan kuruluma kadar adım adım
  götürür - doğrulayıcı, bir paketi yerleşik dillerin uyduğu kurallara tabi tutar,
  bir metnin uzun tire koymak isteyeceği yerde düz kısa çizgi kullanmak da bunlardan
  biri ([paket yazın](docs/PLUGIN-KIT.md#writing-a-language-pack)).
- **Sağdan sola yerleşim** - Arapça, İbranice, Farsça, Urduca ve sağdan sola okunan
  diğer bütün dillerde arayüzün tamamı sağdan sola yerleşir: gezinti paneli,
  tablolar, sürükle-bırak ve sütun boyutlandırma geometrisi, menüler ve yön
  simgeleri. **Aynalanmaması** gereken aynalanmaz - PDF alan düzenleyicisi belgenin
  kendi koordinatlarında çalışır ve bir yol, bir komut ya da başka herhangi bir
  makine metni yalıtılır; böylece sağdan sola bir cümlenin içinde soldan sağa
  okunur, sunucunun yazdığı cümleler dâhil. Kuralı bir koruma testi uygular:
  yerleşim yalnızca mantıksal CSS özellikleriyle yazılır
  ([docs/RTL.md](docs/RTL.md)).
- **Etiketler: kişisel ya da ekibinizin** - bir etiket ya **kişiseldir** - yalnızca
  sizindir, adı başka hiç kimseye söylenmez - ya da kiracı içinde paylaşılan bir
  **ekip** etiketidir; ekip etiketi eklemek ya da kaldırmak için dosyada düzenleme
  yetkisi gerekir. Bir etiket, onu taşıyan her dosyayı hangi klasörde durursa
  dursun açar, `tag:` ise aramayı daraltır. Büyük harfler yazıldığı gibi korunur.
- **Kimlik sağlayıcılar, panelden yönetilir** - **Yönetim → Kimlik sağlayıcılar**
  sayfası artık, hiçbir şeyin okumadığı ayarları saklamak yerine oturum açmayı
  gerçekten yönetir: OIDC, LDAP, başlık vekili, yerel parola formu ve işletim
  sisteminin kendi hesapları - **Windows** (yerel ya da etki alanı, `LogonUserW`,
  kurulacak bir şey yok) ve **Linux PAM** - her biri, onu gerçekten sınayan ve
  hangi adımı doğruladığını söyleyen bir **Şimdi test et** düğmesiyle. İşletim
  sistemi sağlayıcısı yalnızca gerçek bir hesapla oturum açan bir denemeyle açılır
  ve o hesap süper yönetici olur; bu sağlayıcı ortamdan açılamaz. Her sağlayıcı
  **tek bir ilk oturum açma kuralına** uyar - hesap açıp açamayacağı
  (`auto_create`, Windows ve PAM için varsayılan olarak kapalı) ve hangi gruplar
  için açabileceği (`allowed_groups`). Ortamda ya da `config.yaml` dosyasında
  ayarlanan **önceliklidir, bu da açıkça gösterilir**; istemci gizli anahtarı ya
  da bağlanma parolası salt yazılır, `FILEX_SECRET_KEY` ile mühürlenerek saklanır
  ve asla geri gönderilmez; son kalan giriş yolu da bu sayfadan kapatılamaz
  ([docs/SSO.md](docs/SSO.md), [docs/LDAP.md](docs/LDAP.md),
  [docs/OS-LOGIN.md](docs/OS-LOGIN.md)).
- **Giriş güvenliği** - yanlış parolalar, hesap tanımlayıcısı başına (hesap var
  olsun ya da olmasın, böylece sayaç hiçbir şeyi ele vermez) ve istemci adresi
  başına, web formunda da WebDAV, FTPS ve SFTP'de de sayılır: 10 dakika içinde
  hesap başına 5 ve adres başına 10 yanlış parola kapıyı bir dakikalığına kapatır,
  süre ikiye katlanarak 15 dakikaya kadar çıkar; kilit, doğru parolayı bile
  reddeder. Oturum açma formu, kaç deneme kaldığını okuyanın dilinde söyler. Geri
  dönüş yolu **IP izin listesidir** - hiçbir hesap ayrıcalıklı değildir, ilk
  yönetici dâhil (yalnızca adres başına sayılan tek hesap, herkese açık bir
  demonun ortak hesabıdır, [docs/DEMO.md](docs/DEMO.md)) - ve **Yönetim → Giriş
  güvenliği** sayfasında sınırlar, liste, *Kilidi aç* ile birlikte kilitler ve
  oturum açma olayları yer alır: her hatalı deneme, kilit, kilit açma ve ayar
  değişikliği, her biri bir kez, yönetici hangi kapıyı kullanmış olursa olsun
  (panel, bir API anahtarı, MCP). Soketin karşı ucu güvendiğiniz bir vekil sunucu
  olmadıkça, sayılan adres o karşı ucun adresidir (`FILEX_TRUSTED_PROXIES`,
  varsayılan olarak `auto`: bu makine, bir konteynerde çalışıyorsa bir de kendi
  ağındaki diğer konteynerler - ağ geçidi asla, yerel ağ asla; sayfa,
  güvenilmediği hâlde iletilen adres gönderen bir karşı ucu gösterir ve onu
  eklemeyi önerir), böylece bir istemci `X-Forwarded-For` yazarak kendi adresini
  seçemez ([docs/CONFIGURATION.md](docs/CONFIGURATION.md#sign-in-attempt-limits)).
- **Başka bir siteden gelen değişiklikler reddedilir** - bir şeyi değiştiren ve yalnızca
  ziyaretçinin oturumunu (çerezi ya da güvenilir bir vekil sunucunun oturum açma
  başlığını) taşıyan bir istek, filex'in kendi sayfalarından, kendi adresinden ya da
  `FILEX_CORS_ALLOWED_ORIGINS` ayarında listelenen bir kökenden gelmelidir; bunun
  dışındaki her istek, hiçbir rota çalışmadan önce `403 cross_origin_refused` yanıtını
  alır. Anahtarlar, paylaşım ve yükleme bağlantıları, yükleme biletleri, S3 ve betikler
  bundan etkilenmez
  ([docs/CONFIGURATION.md](docs/CONFIGURATION.md#requests-from-other-origins)).
- **Sunucunun sözü, her ret için tek biçim** - bir ret, değişmeyen bir `error` kodu ve
  sunucunun okuyanın dilindeki cümlesiyle (`message`) yanıtlanır; gezgin, yönetim paneli,
  masaüstü uygulaması, CLI ve bir ajan aynı sözleri gösterir
  ([docs/API-ERRORS.md](docs/API-ERRORS.md)). İstemcilerin eskiden kopyasını tuttuğu
  kurallar - hangi dosyaların düzenlemek için açıldığı, girdi sınırları, bu kurulumda
  gerçekleşemeyecek bildirim olayları - sunucu tarafından yayımlanır, böylece hiçbir ekran
  onları farklı değerlendirmez
  ([Sunucunun yayımladığı kurallar](docs/BACKEND.md#rules-the-server-publishes)).

## Mimari

Bkz. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Belgeler

**Başlarken** - [Kurulum](docs/INSTALLATION.md) ·
[Yapılandırma](docs/CONFIGURATION.md) · [Yönetim paneli](docs/ADMIN-PANEL.md) ·
[Veritabanları](docs/DATABASES.md) · [Sürümler](docs/RELEASES.md) ·
[Güncellemeler](docs/UPDATES.md) · [Demo modu](docs/DEMO.md)

**İstemciler** - [Masaüstü uygulaması](docs/DESKTOP.md) · [Klasör eşitleme](docs/SYNC.md) ·
[CLI](docs/CLI.md) · [Eşitlemenin olay akışı](docs/SYNC.md#the-event-stream---json) ·
[Entegrasyon / gömme](docs/INTEGRATION.md) · [Yapay zekâ ve MCP](docs/MCP.md)

**Tarayıcı olmadan** - [Protokoller (S3 · SFTP · FTPS · NFS · WebDAV ·
`filex mount`)](docs/PROTOCOLS.md) · [WebDAV](docs/WEBDAV.md)

**Uygulamalar** -
[Uygulamalar: kurma, denetleme, imzalama, dönüştürme](docs/APP-PLUGINS.md) ·
[Kurulum istekleri](docs/APP-PLUGINS.md#install-requests) ·
[Uygulama izinleri](docs/APP-PLUGINS.md#app-permissions) ·
[Varsayılan uygulamalar](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail) ·
[Uygulama yazma](docs/PLUGIN-KIT.md) ·
[Uygulama iletişim sözleşmesi](docs/APP-PLUGINS-API.md)

**Dil ve yerleşim** -
[Dil paketi yazma](docs/PLUGIN-KIT.md#writing-a-language-pack) ·
[Sağdan sola diller](docs/RTL.md)

**Depolama ve erişim** - [Depolama](docs/STORAGE.md) ·
[Depolama eklentileri](docs/PLUGINS.md) · [Kullanım ve maliyet](docs/USAGE.md) ·
[Yüklemeler ve kaldığı yerden devam](docs/UPLOADS.md) ·
[Kotalar](docs/QUOTAS.md) · [SSO (OIDC)](docs/SSO.md) ·
[LDAP ve vekil sunucuyla kimlik doğrulama](docs/LDAP.md) ·
[Windows ve Linux hesapları](docs/OS-LOGIN.md) ·
[Giriş denemesi sınırları ve
güvenilir vekil sunucular](docs/CONFIGURATION.md#sign-in-attempt-limits) ·
[RBAC, klasör erişimi ve API anahtarları](docs/RBAC.md) ·
[Roller ve kullanıcıya özel izinler](docs/PERMISSIONS.md) · [Gruplar](docs/GROUPS.md) ·
[Çok kiracılılık ve realm'ler](docs/MULTI-TENANCY.md) ·
[Kiracının kendini yönetmesi](docs/TENANT-ADMIN.md)

**Veri ve özellikler** - [Paylaşım ve dosya istekleri](docs/SHARING.md) ·
[ShareX](docs/SHAREX.md) ·
[Çöp kutusu ve sürümleme](docs/TRASH-VERSIONING.md) · [Koruma](docs/PROTECTION.md) ·
[Arşivler](docs/ARCHIVES.md) ·
[Uçtan uca şifreleme](docs/E2E-ENCRYPTION.md) ·
[Kim şifreleyebilir](docs/E2E-ENCRYPTION.md#who-may-encrypt) ·
[Kasa (3. seviye)](docs/E2E-VAULT-FORMAT.md) ·
[Şifreli ofis belgelerini düzenlemek (tasarım)](docs/E2E-OFFICE.md) · [Arama](docs/SEARCH.md) ·
[Gerçek zamanlılık ve kimin açık olduğu](docs/REALTIME.md) ·
[Bildirimler](docs/NOTIFICATIONS.md) · [Web Push](docs/NOTIFICATIONS.md#web-push) ·
[Küçük resimler](docs/thumbnails.md) ·
[Replikasyon](docs/REPLICATION.md) · [Temalar ve görünüm](docs/INTEGRATION.md#themes)

**İşletme ve genişletme** - [Dağıtım](docs/DEPLOYMENT.md) · [Docker](docs/DOCKER.md) ·
[Metrikler](docs/METRICS.md) · [Mimari](docs/ARCHITECTURE.md) ·
[Arka uç API belirtimi](docs/BACKEND.md) · [API hataları](docs/API-ERRORS.md) ·
[OpenAPI 3.1 (`/api/files`, `/api/ai`)](backend/internal/api/openapi.json) ·
[Bileşen API'si](docs/API.md) · [OnlyOffice](docs/ONLYOFFICE.md) ·
[Düzenleyicinin dili](docs/ONLYOFFICE.md#the-editors-language) ·
[ONLYOFFICE'te CSV](docs/ONLYOFFICE.md#csv-files) ·
[Başka kökenlerden gelen istekler](docs/CONFIGURATION.md#requests-from-other-origins)

[Tüm belgelerin dizini](docs/README.md)

## filex nasıl geliştiriliyor

filex'in tek bir geliştiricisi var ve onu yapay zekâ kodlama ajanlarıyla (Claude Code)
geliştiriyor. filex'in ne olduğuna ve nasıl çalıştığına geliştirici karar veriyor: mimari,
veri ve güvenlik modeli, her özelliğin davranışı. Kodun çoğunu ajanlar bu yönlendirmenin
içinde, geliştiricinin bildiği dillerde yazıyor. Geliştirme deposundaki commit'lerin
yaklaşık %82'sinde `Co-Authored-By: Claude` satırı var; yani gizli bir şey yok.

Yapay zekâ destekli, ama gözden geçirilmemiş üretilmiş kod değil:

- Her değişiklik birleşmeden önce test zincirinden geçer: Go testleri (yarış dedektörü
  altında da), yaklaşık 500 Vitest dosyası, Chromium, Firefox ve WebKit'te yaklaşık 120
  Playwright senaryosu, Cypress ve SQLite, PostgreSQL ile MySQL üzerinde veritabanı
  testleri. Her sürüm, GitHub'ın tam test matrisi ve yayın akışının deneme koşusu aynı
  commit üzerinde geçtikten sonra etiketlenir.
- Giriş, yetki, paylaşım ve şifrelemeye dokunan değişiklikler ek inceleme turlarından geçer;
  kırmızı bir test gevşetilmez, düzeltilir.
- Güvenlik bildirimleri açıkta ele alınır: [SECURITY.md](SECURITY.md) ve yayımlanmış
  güvenlik duyuruları. Henüz bağımsız bir denetim yapılmadı.
- Almanca, İspanyolca, Fransızca ve Çince README'ler ile Almanca, İspanyolca ve Fransızca
  dil paketleri makine çevirisidir; henüz ana dili konuşan biri gözden geçirmedi.

## Geliştirme

```bash
git clone https://github.com/BRF-Tech/filex.git
cd filex
pnpm install
pnpm run build:all    # builds packages, web, then Go binary
./bin/filex serve
```

Alt dizinler:
- `backend/` - Go HTTP servisi (cmd/filex, internal/*, db/queries, db/migrations)
- `packages/core` - `@brftech/filex-core` (Vue 3 SFC, asıl kaynak)
- `packages/webcomponent` - `@brftech/filex` (Web Component sarmalayıcısı)
- `packages/react` - `@brftech/filex-react` (@lit/react üzerinden React adaptörü)
- `web/` - Vue 3 yönetim arayüzü (`go:embed` ile Go ikili dosyasına gömülür)
- `desktop/` - Electron uygulaması (paketlenmiş ana süreç, tepsiden eşitleme,
  otomatik güncelleme)
- `demo/` - Her çatı için bağımsız HTML demoları
- `e2e/` - Playwright test takımları (web, gömülü gezginler, paketlenmiş masaüstü
  uygulaması) + `shots/`: yukarıdaki her ekran görüntüsünü yeniden almak için `pnpm shots`
  komutunun çalıştırdığı betikler
- `docker/` - Dockerfile'lar + compose
- `deploy/` - hazır Compose yığınları + Helm chart'ı (bkz. [`deploy/`](deploy/))
- `docs/` - Markdown belgeleri
- `docs-site/` - [docs.filex.sh](https://docs.filex.sh) adresinde yayımlanan VitePress sitesi

Katkılarınızı bekliyoruz - bkz. [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md).

## Lisans

MIT - bkz. [LICENSE](LICENSE).

[`docs/badges/`](docs/badges/) içindeki mağaza rozetleri mağazaların kendi görselleridir;
değiştirilmeden kullanılmıştır ve bu lisansın kapsamında değildir: Microsoft ve
Microsoft Store rozeti, Microsoft şirketler grubunun ticari markalarıdır; Snap Store rozeti
© Canonical Ltd.; [CC BY-ND 2.0 UK](https://creativecommons.org/licenses/by-nd/2.0/uk/) ile
lisanslanmıştır.
