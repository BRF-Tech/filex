<!-- Translated from README.md as of v0.56.0 (the shortened README of #218). The English README is the source: change it first, then carry the change here. -->

<div align="center">

<img src="docs/logo.png" alt="filex logosu" width="96">

# filex

**Web arayüzlü, tek bir Go ikili dosyasında kendi sunucunuzda çalışan dosya yöneticisi ve paylaşım aracı - Nextcloud, Dropbox ve Google Drive'a açık kaynaklı bir alternatif.**<br>
Yerel, S3, SFTP, WebDAV, FTP ve SMB depoları için web tabanlı dosya tarayıcısı: S3, SFTP, NFS ve WebDAV olarak sunulan özel bulut.<br>
SSO/LDAP, çok kiracılılık, şifreli klasörler, gömülebilir web bileşeni, masaüstü uygulaması, MCP sunucusu.

[![Canlı demoyu deneyin](https://img.shields.io/badge/Try_the_live_demo-f59e0b?style=for-the-badge)](https://demo.filex.sh)
[![Hızlı başlangıç](https://img.shields.io/badge/Quick_start-2f6ceb?style=for-the-badge)](#hızlı-başlangıç)
[![Belgeler](https://img.shields.io/badge/Documentation-374151?style=for-the-badge)](https://docs.filex.sh)

[![Release](https://img.shields.io/github/v/release/BRF-Tech/filex?color=2f6ceb)](https://github.com/BRF-Tech/filex/releases)
[![License: MIT](https://img.shields.io/github/license/BRF-Tech/filex?color=22c55e)](LICENSE)

[English](README.md) · **Türkçe** · [Deutsch](README.de.md) · [Español](README.es.md) · [Français](README.fr.md) · [简体中文](README.zh-CN.md)

<sub>Bu sayfa, [İngilizce README](README.md) dosyasının çevirisidir; ikisi arasında fark olduğunda İngilizce metin geçerlidir. Bağlantı verdiği belgeler İngilizcedir.</sub>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://filex.sh/shots/explorer-grid-dark.e032d7456831.png">
  <img src="https://filex.sh/shots/explorer-grid-light.de398671d43b.png" alt="filex gezgini - küçük resim ızgarası" width="900">
</picture>

</div>

<table>
<tr>
<td width="50%" valign="top">

**🗂️ Her depo, tek ağaç**

Zaten sahip olduklarınızı yan yana bağlayın; bir depoda kopyalayın ya da kesin, ötekinde yapıştırın. `rclone`, `restic`, WinSCP ya da FileZilla'yı aynı ağaca yöneltin.

[Depolama ve protokoller →](#depolama-ve-protokoller)

</td>
<td width="50%" valign="top">

**🔗 Paylaşın ve koruyun**

PIN'li, son kullanma tarihli ve indirme limitli bağlantılar. Çöp kutusu, sürüm geçmişi ve uçtan uca şifreli klasörler.

[Paylaşım ve koruma →](#paylaşım-ve-koruma)

</td>
</tr>
<tr>
<td width="50%" valign="top">

**🔑 Sizin hesaplarınız, sizin rolleriniz**

OIDC, LDAP / Active Directory ya da sunucunun Windows ve Linux hesapları. Roller, gruplar, çok kiracılılık.

[Kişiler ve erişim →](#kişiler-ve-erişim)

</td>
<td width="50%" valign="top">

**💻 Canlı klasör eşitlemeli bir masaüstü uygulaması**

Windows, Linux ve macOS (Apple Silicon): iki taraftan birinde yapılan bir değişiklik yaklaşık bir saniyede karşıya geçer.

[Masaüstü uygulaması ve CLI →](#masaüstü-uygulaması-ve-cli)

</td>
</tr>
<tr>
<td width="50%" valign="top">

**🧩 Ürününüze gömülür**

Aynı arayüz bir Vue 3 bileşeni, bir React bileşeni ve bir `<filex-explorer>` web bileşeni olarak gelir.

[Uygulamanıza gömün →](#uygulamanıza-gömün)

</td>
<td width="50%" valign="top">

**🤖 Uygulamalar ve yapay zekâ ajanları**

Yalıtılmış uygulamalar - e-İmza, Dönüştür, draw.io - ve bir ajanın kullanabileceği yerleşik bir MCP sunucusu.

[Uygulamalar →](#uygulamalar) · [Yapay zekâ ajanları / MCP →](#yapay-zekâ-ajanları--mcp)

</td>
</tr>
</table>

<p align="center"><a href="#neden-filex">Neden filex</a> · <a href="#nextcloud-dropbox-ya-da-google-drivedan-geliyorsanız">Başka bir araçtan geliyorsanız</a> · <a href="#compose-ya-da-helm-ile-kendi-sunucunuzda-çalıştırın">Kendi sunucunuzda</a> · <a href="#özellikler">Özellikler</a></p>

<br>

# Hızlı başlangıç

> [!TIP]
> **Kurmadan önce bakın:** [demo.filex.sh](https://demo.filex.sh) - `demo@demo.com` / `demo`
> ile oturum açın (yönetici rolü, deneme alanı her gece sıfırlanır).

Kendi kurulumunuzu, sunmak istediğiniz klasörden çalıştırın:

```bash
docker run -p 5212:5212 \
  -e FILEX_DEFAULT_STORAGE_DRIVER=local -e FILEX_DEFAULT_STORAGE_PATH=/srv/files \
  -v filex-data:/data -v "$PWD:/srv/files" \
  ghcr.io/brf-tech/filex:latest
```

http://localhost:5212/admin adresini açın - ilk çalıştırmada yönetici kimlik bilgileri ve
gömme talimatları konsola yazılır. Bu URL işletmecinindir; hesap açtığınız kişilerin adresi
ise **http://localhost:5212/drive**, yani çevresinde panel olmayan aynı dosya yöneticisidir.

<details>
<summary><b>Bu komut neyi bağlar, hangi kullanıcıyla çalışır</b></summary>

Bu komut **çalıştırdığınız klasörü** sunar - arayüzü açın, dosyalarınız zaten orada.
`/data`, filex'in kendi dizinidir (SQLite veritabanı, arama indeksi, küçük resim önbelleği),
bu yüzden dosya bıraktığınız klasör değil, adlandırılmış bir birimdir; ikisi bilerek ayrı
tutulur. `$PWD` yerine başka bir yer gösterin ya da sonradan yönetim panelinden başka depolar
ekleyin - birkaç üst düzey klasörü olan bir kova (bucket), klasör başına bir depo olarak
tek seferde bağlanabilir (*Depolar → Depo ekle → Birden çok klasörü tek seferde bağla*).

Varsayılan olarak konteyner **root** olarak çalışır, bu yüzden `/data` dizinine yazdıkları
root'a aittir; kendi kullanıcınızla çalıştırmak için `PUID`/`PGID` değişkenlerini ayarlayın
([docs/DOCKER.md](docs/DOCKER.md#which-user-the-container-runs-as)).

</details>

<details>
<summary><b>Docker yok mu? İkili dosyayı çalıştırın</b></summary>

Platformunuzun arşivini
[son sürümden](https://github.com/BRF-Tech/filex/releases/latest) indirin, açın ve ikili
dosyayı çalıştırın ([docs/INSTALLATION.md](docs/INSTALLATION.md#binary)):

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

</details>

Gerçek bir kurulum için Compose yığınları ve bir Helm chart'ı var:
[Compose ya da Helm ile kendi sunucunuzda çalıştırın](#compose-ya-da-helm-ile-kendi-sunucunuzda-çalıştırın).
İmaj [GitHub Packages](https://github.com/BRF-Tech/filex/pkgs/container/filex) üzerindedir.

<br>

# Neden filex

Kendi sunucunuzda çalışan dosya yöneticilerinin çoğu ya **fazla küçük** (yükleme
yapılabilen bir dizin listesi) ya da **fazla büyük** (dosya sekmesi için kurduğunuz koca
bir grup çalışması paketi). filex aradaki boşluğu hedefler. Aşağıdaki tur sekiz bölümden
oluşur; her bölümün ekran görüntüleri altında katlanmıştır. Her özelliğin sınırlarıyla ve
onu anlatan kılavuzla birlikte tam hâli [docs/FEATURES.md](docs/FEATURES.md) belgesindedir.

<br>

## Gezgin

**İnsanların zaten kullanmayı bildiği bir dosya yöneticisi.**

- **Ana sayfa · Dosyalarım · Benimle paylaşılanlar · Paylaştıklarım · Son kullanılanlar ·
  Yıldızlılar · Taslaklar · Çöp kutusu** içeren bir gezinti paneli; liste, ızgara ve galeri
  görünümleri; sekmeler, etiketler ve bir ⌘K paleti.
- Kimin açık olduğunu gösteren avatarlar ve dosya değişiklikleri WebSocket üzerinden canlı
  gelir.
- Kendi renklerinizle bir tema oluşturup varsayılan yapın: oturum açma sayfası ve herkese
  açık her bağlantı da onu kullanır; telefon da web uygulamasını kurulumunuzun adı ve
  logosuyla kurar.
- İngilizce ve Türkçe yerleşik gelir, diğer diller paket olarak kurulur; arayüz, sağdan
  sola okunan dillerde sağdan sola döner.

Gezginin tüm özellikleri ayrıntısıyla: [docs/FEATURES.md → The explorer](docs/FEATURES.md#the-explorer).

<p align="center"><img src="https://filex.sh/shots/driveshell/driveshell-hero-1440.16ace59f07c3.png" alt="filex kabuğu" width="860"></p>

<details>
<summary><b>Gezginin ekran görüntüleri</b> - kabuk, arama, gezinti, etiketler, çöp kutusu, çan, temalar, sembolik bağlar, ONLYOFFICE'te CSV</summary>

<table>
<tr>
<td width="50%" valign="top">

![filex kabuğu](https://filex.sh/shots/driveshell/driveshell-hero-1440.16ace59f07c3.png)

<sub>Kabuk - herkesin karşılaştığı düzen</sub>

</td>
<td width="50%" valign="top">

![Bir klasörde arama](https://filex.sh/shots/driveshell/driveshell-search-1440.c0f3ceda0ec4.png)

<sub>Bu klasörde arama; `⌘K` / `Ctrl K` sorguyu palete devreder</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Gezinti paneli](https://filex.sh/shots/sidenav/sidenav-expanded-1440.461d5aaaff2a.png)

<sub>Gezinti paneli - Ana sayfa, Benimle paylaşılanlar, Paylaştıklarım, Son kullanılanlar, Yıldızlılar, Çöp kutusu ve erişebildiğiniz depolar</sub>

</td>
<td width="50%" valign="top">

![Bir şeride daraltılmış](https://filex.sh/shots/sidenav/sidenav-rail-1440.843a2158380d.png)

<sub>Simge şeridine daraltılmış</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Kişisel etiketler ve ekip etiketleri](https://filex.sh/shots/tags/tags-kinds-1440.64e4d6a5ccbc.png)

<sub>Etiketler - kendi etiketleriniz ya da ekibinizinkiler; bir etiket, onu taşıyan her dosyayı hangi klasörde durursa dursun açar</sub>

</td>
<td width="50%" valign="top">

![Çöp kutusu görünümü](https://filex.sh/shots/sidenav/view-trash-1440.ab3cfb3b01cf.png)

<sub>Çöp kutusu - neyin silindiği, nereden geldiği ve gitmesine ne kadar kaldığı</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Okunmamış rozetini taşıyan çan, açık hâlde](https://filex.sh/shots/signing/bell-badge-1440.61396b9ab714.png)

<sub>Çan - üzerinde okunmamışların sayısı, her satır yazdığı yere götürür</sub>

</td>
<td width="50%" valign="top">

![Gezginin üzerinde bildirimlerin tam listesi](https://filex.sh/shots/signing/notifications-list-1440.3176161f2110.png)

<sub>Tüm bildirimleriniz, gezginin içinde - yalnızca yöneticiler için değil, herkes için</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Tema düzenleyicisi](https://filex.sh/shots/appearance/theme-editor-1440.5b28006557f8.png)

<sub>Görünüm - kendi renklerinizle bir tema oluşturun, siz yazdıkça önizlenir</sub>

</td>
<td width="50%" valign="top">

![İşletmecinin temasını taşıyan gezgin](https://filex.sh/shots/appearance/themed-explorer-1440.dbe464fc39c7.png)

<sub>Varsayılan yapıldığında herkesin gezgini onu taşır…</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![İşletmecinin temasını taşıyan oturum açma sayfası](https://filex.sh/shots/appearance/themed-signin-1440.98801a84bea2.png)

<sub>…oturum açma sayfası da, henüz kimse oturum açmadan</sub>

</td>
<td width="50%" valign="top">

![Deponun dışına çıkan bir sembolik bağ, rozetli](https://filex.sh/shots/symlinks/symlink-badge-1440.0d128385d287.png)

<sub>filex'in izlemeyeceği bir sembolik bağ bunu söyler - listede, ayrıntılarında da sözle</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Noktalı virgülle ayrılmış bir CSV, ONLYOFFICE'in hesap tablosunda açık](https://filex.sh/shots/csvoffice/csv-view-1440.83237ba55d3d.png)

<sub>Bir `.csv` dosyası, ONLYOFFICE bağlıysa onun hesap tablosunda açılır - önce görüntülenir, ayırıcı soran iletişim kutusu da çıkmaz: dosyanın kendi ayırıcısı iletilir ([CSV dosyaları](docs/ONLYOFFICE.md#csv-files))</sub>

</td>
<td width="50%" valign="top">

![CSV, ONLYOFFICE'in düzenleyicisinde, kaydedilince nelerin kaldığını söyleyen notla birlikte](https://filex.sh/shots/csvoffice/csv-edit-1440.4efc379a293d.png)

<sub>…ve CSV olarak kaydedilince nelerin kaldığını söyleyen düzenleyicisi; dosya aynı türden bir CSV olarak geri döner, kimsenin değiştirmediği hücreler de metnini korur - `007`, `007` kalır ([kimsenin değiştirmediği hücreler](docs/ONLYOFFICE.md#cells-nobody-changed-keep-their-text))</sub>

</td>
</tr>
</table>

</details>

<br>

## Depolama ve protokoller

**Her depo tek bir ağaçta, o ağaç da her şeyden erişilebilir.**

- Yerel diskleri, S3'ü, FTP'yi, SFTP'yi, WebDAV'ı ve SMB/NAS paylaşımlarını yan yana bağlayın;
  birinde kopyalayın ya da kesin, ötekinde yapıştırın.
- Aynı ağaca **S3**, **SFTP**, **FTPS**, **NFSv3** ve **WebDAV** olarak erişin: `rclone`,
  `restic`, `aws s3`, WinSCP ya da FileZilla'yı ona yöneltin. Aynı filex izinleri (kişi, grup
  ve klasör bazında), çöp kutusu ve kota web arayüzündeki gibi geçerlidir; diskteki POSIX izin
  bitleri ve ACL'ler filex kullanıcılarına eşlenmez.
- `filex mount` uzak bir sunucuyu sıradan HTTPS üzerinden bağlar: Linux'ta bir klasör,
  Windows'ta bir sürücü harfi olarak (macOS desteklenmez).
- filex'le birlikte gelmeyen bir depo için yönetim panelinden ya da bir uygulama mağazasının
  bağlantısından bir eklenti kurun.

Depolama ve protokollerin tüm özellikleri ayrıntısıyla: [docs/FEATURES.md → Storage and protocols](docs/FEATURES.md#storage-and-protocols).

<p align="center"><img src="https://filex.sh/shots/sidenav/connect-1440.8bfbe35780f4.png" alt="Nasıl bağlanılır" width="860"></p>

<details>
<summary><b>Depolama ve protokollerin ekran görüntüleri</b> - bağlantı kılavuzları, API anahtarları ve bir depolama eklentisi</summary>

<table>
<tr>
<td width="50%" valign="top">

![Nasıl bağlanılır](https://filex.sh/shots/sidenav/connect-1440.8bfbe35780f4.png)

<sub>Nasıl bağlanılır - kılavuzlar, *sizin* kurulumunuzdan üretilir</sub>

</td>
<td width="50%" valign="top">

![API anahtarları](https://filex.sh/shots/sidenav/apikeys-minted-1440.cfc100f30069.png)

<sub>API anahtarları - kendinizinkini oluşturun, gezginde ya da gömülü bir gezginde (bir kişinin oturumuyla ya da API anahtarıyla; tek bir ortak *uygulama* API anahtarıyla vekil sunucu üzerinden çalışan gömülü bir gezginde bu öğe yer almaz)</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Bağlantı kılavuzu](https://filex.sh/shots/connections-guide.98a4f82ecd9d.png)

<sub>filex'e her şeyden erişmek - S3, SFTP, FTPS, NFS, WebDAV. Her komut *sizin* kurulumunuzdan üretilir</sub>

</td>
<td width="50%" valign="top">

![Eklentiler](https://filex.sh/shots/admin-plugins.c25fa69cfc7c.png)

<sub>filex'le birlikte gelmeyen bir depo - **Eklentiler → Depolama eklentileri** sayfasında eklenti olarak kurulur, yapılandırma formunu kendisi tanımlar</sub>

</td>
</tr>
</table>

</details>

<br>

## Paylaşım ve koruma

**Sınırlayıp iptal edebileceğiniz bir bağlantıyla paylaşın. Yanlış bir silmeyi ya da yanlış bir üzerine yazmayı geri alın.**

- PIN'li, son kullanma tarihli ve indirme limitli herkese açık bağlantılar, gelmesi
  gerekenler için de dosya istekleri. **Paylaştıklarım**, oluşturduğunuz her bağlantıyı
  kopyalamanız ya da iptal etmeniz için listeler.
- Bir klasörü bir kişiyle ya da bir grupla paylaşın, klasör onların **Benimle
  paylaşılanlar** görünümünde belirir. Bunun için deponun **Öğe bazlı erişim denetimi
  (RBAC)** ayarı açık olmalıdır; varsayılan olarak kapalıdır.
- Silmeler bir saklama süresi boyunca geri alınabilir, yazmalar anlık kopya bırakır; ikisi
  de zaten bağladığınız deponun içinde durur.
- Uçtan uca şifreli klasörler: tarayıcıda şifrelenir, sunucu hiçbir zaman bir anahtar almaz.
  İzin verdiğiniz bir uygulama böyle bir dosyayı tarayıcınızda düzenler: tarayıcınız dosyayı
  uygulama için çözer ve kaydettiğini yeniden şifreler. Yazılan her dosyanın isteğe
  bağlı ClamAV taraması.

Paylaşım ve korumanın tüm özellikleri ayrıntısıyla: [docs/FEATURES.md → Sharing and protection](docs/FEATURES.md#sharing-and-protection).

<p align="center"><img src="https://filex.sh/shots/public-share.0e3ba07f7c88.png" alt="Herkese açık bir paylaşım bağlantısı, alıcısının gördüğü hâliyle" width="860"></p>

<details>
<summary><b>Paylaşım ve korumanın ekran görüntüleri</b> - Paylaş iletişim kutusu, alıcının gördüğü, Paylaştıklarım, kim şifreleyebilir</summary>

<table>
<tr>
<td width="50%" valign="top">

![Paylaşım iletişim kutusu](https://filex.sh/shots/share-modal.d6f6572228ea.png)

<sub>Paylaşım - PIN, son kullanma tarihi, indirme limiti, tek satırlık `curl`</sub>

</td>
<td width="50%" valign="top">

![Markdown görüntüleyici](https://filex.sh/shots/viewer-markdown.1789ecdcfbc5.png)

<sub>Markdown görüntüleyici</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Herkese açık bir paylaşım bağlantısı, alıcısının gördüğü hâliyle](https://filex.sh/shots/public-share.0e3ba07f7c88.png)

<sub>…ve karşı taraftaki kişinin açtığı şey. filex'in dışarıya dönük TEK bir ekranı vardır - paylaşılan bir dosya, bir klasör, bir dosya isteği, bir uygulamanın imzalama sayfası ve bunlardan herhangi birinin önündeki PIN, hepsi bu sayfadır, kurulumunuzun adıyla</sub>

</td>
<td width="50%" valign="top">

![Paylaştıklarım, bir satırın Aksiyon menüsü açık](https://filex.sh/shots/signing/my-shares-1440.6f8676cbb555.png)

<sub>Paylaştıklarım - oluşturduğunuz bağlantılar ve birine iletmeniz gerektiğinde bunların PIN'leri</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Yönetim → Paylaşımlar, bir satırın Aksiyon menüsü açık](https://filex.sh/shots/signing/admin-table-actions-1440.330fcc5fda73.png)

<sub>Her yönetim tablosu - satır başına sabitlenmiş tek bir **Aksiyon** menüsü, gezgindeki ⋮ ile açılan menünün aynısı</sub>

</td>
<td width="50%" valign="top">

![Yönetim → Şifreleme: onay politikası ve bekleyen üç istek](https://filex.sh/shots/encryption/admin-encryption-1440.b74163acb93c.png)

<sub>Kim şifreleyebilir - kapalı, yalnız yöneticiler, rolü izin veren herkes ya da yönetici onayıyla; bekleyen istekler, kimin neden istediğiyle birlikte ([kim şifreleyebilir](docs/E2E-ENCRYPTION.md#who-may-encrypt))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Şifreli klasör isteği, gerekçesiyle](https://filex.sh/shots/encryption/request-new-folder.e74894fba30f.png)

<sub>…ve isteyen kişinin tarafı: Yeni klasör iletişim kutusunun açtığı istek - bir yöneticiden, gerekçesiyle birlikte, tek bir yeni şifreli klasör</sub>

</td>
</tr>
</table>

</details>

<br>

## Kişiler ve erişim

**İnsanların zaten sahip olduğu hesaplar ve her birine tek bir rol.**

- Yerel bir parola, OIDC, LDAP / Active Directory, kimlik doğrulayan bir vekil sunucu ya da
  filex'in çalıştığı makinedeki Windows veya Linux hesabıyla oturum açın.
- Herkesin tek bir rolü vardır - Yönetici, Kullanıcı, İzleyici ya da sizin tanımladığınız
  bir rol - ve roller adlandırılmış izinlerden oluşur. Gruplar klasör erişimi ve bir rol
  taşır, dizininizin gruplarını izleyebilir; kişiye özel istisnalar rolün önüne geçer.
- Parola tahmin etmek yavaştır: yanlış parolalar web formunda, WebDAV'da, FTPS'te ve SFTP'de
  aynı şekilde hesap başına ve adres başına sayılır.
- Tasarımı gereği çok kiracılı, tek bir anahtarın ardında: her kiracının kendi oturum açma
  sağlayıcıları, kendi alan adı ve sertifikası vardır ve kiracı kendini yönetir. Anahtar
  kapalıyken hiçbir ekran bir kiracı göstermez.

Kişiler ve erişimle ilgili tüm özellikler ayrıntısıyla: [docs/FEATURES.md → People and access](docs/FEATURES.md#people-and-access).

<p align="center"><img src="https://filex.sh/shots/roles/roles-list-1440.77477a2c7001.png" alt="Yönetim → Roller: yerleşik roller ve iki özel rol" width="860"></p>

<details>
<summary><b>Kişiler ve erişimin ekran görüntüleri</b> - roller, gruplar, giriş güvenliği, kilitli bir hesap, yönetim paneli ve menüsü</summary>

<table>
<tr>
<td width="50%" valign="top">

![Yönetim → Roller: yerleşik roller ve iki özel rol](https://filex.sh/shots/roles/roles-list-1440.77477a2c7001.png)

<sub>Roller - Yönetici, Kullanıcı, İzleyici ve kendi rolleriniz: her birinin kimde olduğu, neye izin verdiği, klasöre göre nerede farklılaştığı, sınırları ([docs/PERMISSIONS.md](docs/PERMISSIONS.md))</sub>

</td>
<td width="50%" valign="top">

![Yönetim → Gruplar](https://filex.sh/shots/groups/groups-list-1440.73224b70e40b.png)

<sub>Gruplar - klasör erişimi ve bir rolü olan adlandırılmış kişi kümeleri; üyeler elle eklenir ya da oturum açmanın taşıdığı gruplarla eşitlenir ([docs/GROUPS.md](docs/GROUPS.md))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Bir klasörü bir grupla paylaşmak](https://filex.sh/shots/groups/share-group-1440.31c2a81e1eeb.png)

<sub>Bir klasörü kişilerin yanında bir grupla paylaşmak - Sahip seviyesi tek tıkla verilmez, iletişim kutusunda istenir</sub>

</td>
<td width="50%" valign="top">

![Yönetim → Giriş güvenliği](https://filex.sh/shots/loginsecurity/login-security-1440.077cec3f1b0a.png)

<sub>Giriş güvenliği - giriş denemesi sınırı ve hiç sayılmayan izinli adresler; güvenilir vekil sunucular, kilitler ve oturum açma olayları aynı sayfada devam eder ([giriş denemesi sınırları](docs/CONFIGURATION.md#sign-in-attempt-limits))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Kilitli bir hesapta oturum açma formu](https://filex.sh/shots/loginsecurity/login-locked-1440.3f4c6d3233f3.png)

<sub>…ve kilitli bir hesabın oturum açma formunun söylediği; düğmesinde kilidin kalan süresi geri sayar</sub>

</td>
<td width="50%" valign="top">

![Yönetim paneli](https://filex.sh/shots/admin-dashboard.d94a065baab6.png)

<sub>Yönetim paneli</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Demo açılış sayfası](https://filex.sh/shots/demo-landing.bb2b8d97d4ff.png)

<sub>Demo açılış sayfası</sub>

</td>
<td width="50%" valign="top">

![Yönetim menüsünün Kişiler ve güvenlik paneli, Yönetim → Kullanıcılar sayfasının üzerinde açık](https://filex.sh/shots/megamenu/people-panel-1440.2efdcb9a685a.png)

<sub>Yönetim menüsü - bütün sayfalar üç panelde, **Dosyalar ve depolama**, **Kişiler ve güvenlik** ve **Sistem**, her sayfanın altında kısa bir satırla; telefonda aynı sayfalar bir çekmecede yer alır ([docs/ADMIN-PANEL.md](docs/ADMIN-PANEL.md))</sub>

</td>
</tr>
</table>

</details>

<br>

## Masaüstü uygulaması ve CLI

**Bilgisayarınızdaki klasörler sunucuyla canlı olarak eşitlenmiş kalır.**

- Bir Windows, Linux ve macOS (Apple Silicon) uygulaması: bir pencerede aynı gezgin, birden
  çok hesap ya da kiracı yan yana.
- Bir klasöre sağ tıklayın → **Bilgisayarda tut**; klasör iki yönde de, yaklaşık bir
  saniyede sunucuyla eşitlenmiş kalır. Gerisi pencerede yalnızca çevrimiçi kalır.
- Tarayıcınız üzerinden oturum açar, böylece SSO ve MFA web'deki gibi davranır; Office
  belgelerini de kendi diskinizden, sunucunuzun çalıştırdığı düzenleyicide açar.
- Ekransız makinelere aynı motor gelir: `filex sync` ve `filex client`.

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

<details>
<summary><b>Kurulum ve CLI hakkında daha fazlası</b> - Store derlemesi, winget, sürüm indirmeleri, Linux, arm64, istemci komutları</summary>

Store derlemesi (*filex File Manager*), kod imzalı tek Windows kopyasıdır - onu Microsoft
imzalar - ve Store onu güncel tutar. Masaüstü uygulamasının winget paketi
(`BRFTech.filex-app`) her sürümle birlikte gönderiliyor ve winget moderatörlerinin ilk
incelemesini bekliyor; bu yüzden `winget install BRFTech.filex-app` komutu onu henüz
bulamıyor. Kurulum programı, taşınabilir `.exe`, AppImage, `.deb`, `.rpm` ve `.dmg`
[son sürüme](https://github.com/BRF-Tech/filex/releases/latest) eklidir - henüz kod imzalı
değil; Windows kurulum programında bir SmartScreen uyarısı bekleyin. Ayrıntılar:
[docs/DESKTOP.md](docs/DESKTOP.md). Yalnızca CLI: `brew install brf-tech/filex/filex`,
Windows'ta ise `winget install BRFTech.filex` ([docs/CLI.md](docs/CLI.md)).

Linux'ta `.deb` ve `.rpm` paketleri hiçbir şey gerektirmez; Ubuntu 23.10 ve sonrasında
AppImage tek seferlik bir AppArmor profili ister
([AppImage on recent Ubuntu](docs/DESKTOP.md#appimage-on-recent-ubuntu)), Snap ise snap'in
katı kısıtlaması içinde çalışır
([The snap and the sandbox](docs/DESKTOP.md#the-snap-and-the-sandbox)). Her sürüm arm64 için
de derlenir: sunucu, imajlar ve Linux'ta, Windows on Arm'da ve Apple Silicon'da masaüstü
uygulaması ([docs/INSTALLATION.md → ARM (arm64)](docs/INSTALLATION.md#arm-arm64)).
Uygulamanın yaptığı her şey: [docs/FEATURES.md → Desktop app, phone and CLI](docs/FEATURES.md#desktop-app-phone-and-cli).

Aynı ikili dosya ayrıca sunucular, betikler ve ekransız makineler için bir istemcidir:

```bash
filex client login --url https://files.example.com
filex client upload build/report.pdf docs://ci-artifacts/
filex client mv docs://ci-artifacts/report.pdf archive://2026/   # across storages, waits for the job
filex client run convert convert docs://data/table.csv --param target=xlsx

filex sync add ~/Documents/work docs://work   # the engine the desktop app uses
filex sync run --watch 30s
```

Bkz. [docs/CLI.md](docs/CLI.md) ve [docs/SYNC.md](docs/SYNC.md).

</details>

<br>

## Uygulamanıza gömün

**Kendi ürününüzün içinde gerçek bir dosya yöneticisi.**

Aynı arayüz bir Vue 3 bileşeni, bir React bileşeni ve çatıdan bağımsız bir
`<filex-explorer>` web bileşeni olarak gelir ve kendi filex sunucunuza dayanır.
Sınırlandırılmış bir API anahtarı onu kiracıya özel bir klasöre kilitler; bunu bileşen
değil, arka uç uygular. Web bileşeni iki satırdır:

```html
<script type="module" src="https://cdn.jsdelivr.net/npm/@brftech/filex/dist/filex.js"></script>
<filex-explorer api-base="http://localhost:5212" sidenav connections ui-profile="simple"></filex-explorer>
```

<p align="center"><img src="https://filex.sh/shots/sidenav/embed-webcomponent-1440.c8c25d893d24.png" alt="Gömülü web bileşeni" width="860"></p>

<details>
<summary><b>Vue 3 ve React</b> - öteki iki sarmalayıcı ve ayrıntıların nerede olduğu</summary>

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

Stiller, paketleyiciler, `sidenav` / `connections` / `ui-profile` öznitelikleri, çok
kiracılı barındıran uygulamalar için sınırlandırılmış API anahtarları, başka bir kökenden
gelen oturum çerezi ve bir uygulamadan yazdırma:
[docs/INTEGRATION.md](docs/INTEGRATION.md); özeti
[docs/FEATURES.md → Embedding](docs/FEATURES.md#embedding) içinde. ⚠ Paketleri ve sunucuyu
aynı sürümde tutun ([docs/API.md](docs/API.md)).

<table>
<tr>
<td width="50%" valign="top">

![Benimle paylaşılanlar](https://filex.sh/shots/sidenav/view-shared-1440.475ec2d8b49f.png)

<sub>Benimle paylaşılanlar - başkalarının size yetki verdiği klasörler, bağlama talimatı yok</sub>

</td>
<td width="50%" valign="top">

![Gömülü web bileşeni](https://filex.sh/shots/sidenav/embed-webcomponent-1440.c8c25d893d24.png)

<sub>Başka bir ürünün sayfasına gömülü</sub>

</td>
</tr>
</table>

</details>

<br>

## Uygulamalar

**Dosyalarla yapılacak yeni işler, tam olarak onayladığınız izinlerle.**

Bir uygulama, filex'in içinde bir yalıtım alanında çalışan bir WebAssembly modülü,
yalıtılmış bir çerçevede kendi arayüzü ya da ikisi birdendir. Onu bir mağaza
bağlantısından ya da GitHub adresinden kurarsınız, istediği her izni okursunuz ve hiçbir
şey kendini güncellemez. filex'inizin kişileri güvenilen bir mağazaya **Uygulama mağazası**
ekranında göz atıp sizden bir uygulama ya da depolama eklentisi isteyebilir. Beşi
herkese açık repo olarak gelir:

| Uygulama | Ne ekler |
|---|---|
| [e-İmza](https://github.com/BRF-Tech/filex-sign) | Bir PDF'i imzalayın ya da başkalarından imzalamalarını isteyin: bu filex kurulumundaki kişiler filex'in içinde imzalar, diğer herkes varsayılan olarak PIN arkasında duran özel bir bağlantıyla |
| [Dönüştür](https://github.com/BRF-Tech/filex-convert) | Herhangi bir dosyada ya da seçimde **Dönüştür…**: görseller, video, ses, belgeler, e-kitaplar, arşivler, veri, altyazılar ve yazı tipleri |
| [Ofis düzenleyici](https://github.com/BRF-Tech/filex-office-editor) | Word, Excel ve PowerPoint belgeleri ONLYOFFICE düzenleyicisinde, tarayıcıda ve Document Server olmadan; filex 0.56 için yapılan 0.2.0 ve 0.3.0 sürümleri şifreli klasörleri ve aynı anda birkaç kişiyle düzenlemeyi ekler |
| [filextext](https://github.com/BRF-Tech/filextext-app) | Tek bir `.fxtxt` dosyasında uçtan uca şifreli bir metin çalışma alanı |
| [draw.io](https://github.com/BRF-Tech/filex-drawio) | `.drawio` ve `.dio` dosyalarında draw.io diyagram düzenleyicisi |

İzin verdiğiniz bir uygulama bir dosyayı başkalarıyla **birlikte** düzenleyebilir - filex
kişilerin değişikliklerini okumadan tek bir sıraya koyup aktarır, yalnız okuyabilen kişi de
canlı izler - ve **şifreli bir klasördeki** bir dosyayı açabilir; tarayıcınız dosyayı
uygulama için çözer ve kaydedilmeden önce yeniden şifreler.

Dil paketi de bir uygulamadır: İspanyolca, Almanca ve Fransızca örnek olarak gelir.

Birkaç dakikada bir uygulama kurun: [docs/APP-PLUGINS.md → Install one](docs/APP-PLUGINS.md#install-one). Uygulamaların tüm özellikleri ayrıntısıyla: [docs/FEATURES.md → Apps](docs/FEATURES.md#apps).

<p align="center"><img src="https://filex.sh/shots/signing/sign-place-1440.696a10b65be8.png" alt="Kutuları belgeye yerleştirme" width="860"></p>

<details>
<summary><b>Uygulamaların ekran görüntüleri</b> - baştan sona bir imza isteği, bir uygulama kurmak, dönüştürücü, kendi arayüzü olan bir uygulama, birlikte düzenleme, bir uygulamada şifreli klasördeki bir dosya, bir dil paketi, Varsayılan uygulamalar, mağazadan kurmak</summary>

Dana, aynı filex kurulumundaki bir iş arkadaşından ve kurulumun dışındaki bir iş ortağından
bir sözleşmeyi imzalamalarını ister. Uygulama [e-İmza](https://github.com/BRF-Tech/filex-sign);
her ekranı filex çizer, iş ortağının aldığı bağlantı da sıradan bir paylaşımdır.

<table>
<tr>
<td width="50%" valign="top">

![Bir imza isteğinin kutularını tanımlama](https://filex.sh/shots/signing/sign-define-1440.63ea72b13724.png)

<sub>Kutuları tanımlayın - her birine ad verin ve kimin olduğunu söyleyin; belge sonra gelir</sub>

</td>
<td width="50%" valign="top">

![Kutuları belgeye yerleştirme](https://filex.sh/shots/signing/sign-place-1440.696a10b65be8.png)

<sub>Kutuları yerleştirin - bir kutu seçin, sayfada gideceği yere dokunun</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Dış imzacının PIN ekranı](https://filex.sh/shots/signing/sign-outside-pin-1440.2cd8ccb99483.png)

<sub>İş ortağının bağlantısı - filex'in herkese açık tek ekranı, kurulumunuzun adıyla, PIN arkasında</sub>

</td>
<td width="50%" valign="top">

![Kutularını dolduran dış imzacı](https://filex.sh/shots/signing/sign-outside-fill-1440.9f50aeb7abef.png)

<sub>…ve açtığı şey: yalnızca kendi kutuları - burada, isteği gönderenin seçtiği yazı tipiyle yazılmış bir ad (öteki iki yol çizmek ve yüklemek)</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Belge kilitli, İmzalar paneli açık](https://filex.sh/shots/signing/sign-status-1440.9f93226381ed.png)

<sub>Belge imzadayken - herkes için dondurulmuş, kimin imzaladığı ayrıntılarında</sub>

</td>
<td width="50%" valign="top">

![Kurulum sihirbazının izin incelemesi](https://filex.sh/shots/apps/apps-install-review-1440.cdb1a4ebf8f6.png)

<sub>Uygulama kurulurken - istediği her izin, sade bir dille, hiçbir şey çalışmadan önce</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Kurulu bir uygulamanın ayrıntıları](https://filex.sh/shots/apps/apps-detail-1440.4e27b40c5ce7.png)

<sub>Kurulu bir uygulama - nereden geldiği, parmak izi ve sahip olduğu her izin, sade bir dille (ayarları ve işlemleri sayfanın daha aşağısında gelir)</sub>

</td>
<td width="50%" valign="top">

![Dönüştürücünün sihirbazı](https://filex.sh/shots/apps/convert-wizard-1440.231ada006fd6.png)

<sub>Dönüştürücü, bir başka uygulama - her hedef kendi kategorisinin altında, üç adım</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Kendi arayüzü olan bir uygulamanın kurulum incelemesi](https://filex.sh/shots/apps/app-interface-review-1440.51df0aa460ad.png)

<sub>Kendi arayüzünü getiren bir uygulama - inceleme, paketin parmak izini, paketin dışındaki her adresi (canlı olanı bir izindir, sarı renkte) ve bir tarayıcının söz veremeyeceği şeyi gösterir</sub>

</td>
<td width="50%" valign="top">

![Bir uygulamanın kendi arayüzü, bir dosyanın görüntüleyicisi olarak açık](https://filex.sh/shots/apps/app-interface-viewer-1440.519b23618156.png)

<sub>…ve o arayüz, filex'in önizlemesinin duracağı yerde, kendi dosya türü için açılmış. Dosyayı filex üzerinden, yalıtılmış bir çerçevede okur ve kaydeder (bu görüntüler için yazılmış küçük bir örnek uygulama)</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Bir belgede iki kişi: biri düzenliyor, öbürü izliyor](https://filex.sh/shots/officeapp/coedit-watcher-1280.904b31e20f36.png)

<sub>Birlikte düzenleme - Dana yazar; yalnız okuyabilen Sam her değişikliğin ve Dana'nın imlecinin filex üzerinden geldiğini izler ([Birlikte düzenleme](docs/APP-PLUGINS-API.md#editing-together-coedit-056); bu görüntüler için yazılmış bir örnek uygulama)</sub>

</td>
<td width="50%" valign="top">

![Şifreli bir klasördeki bir dosya bir uygulamada açık, tarayıcının çözdüğü haliyle](https://filex.sh/shots/e2eapp/app-encrypted-folder-1280.856d3586a9e2.png)

<sub>Uçtan uca şifreli bir klasördeki bir dosya, izin verdiğiniz bir uygulamada - dosyayı uygulama için tarayıcınız çözer, kaydedileni yeniden şifreler; sunucuda yine yalnız şifreli veri durur (aynı örnek uygulama; [Bir uygulamada düzenleme](docs/E2E-ENCRYPTION.md#editing-in-an-app))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Uygulamalar listesi, uygulamaların arasında bir dil paketi](https://filex.sh/shots/langpack/apps-list-1440.004f0c819981.png)

<sub>Kurulumdaki her uygulama ve aralarında bir **dil paketi** - çalışan hiçbir şeyi olmayan bir manifest; bu filex kurulumunun ne kadarını çevirdiğini söyler, paket gidince dili de gider</sub>

</td>
<td width="50%" valign="top">

![Eklentiler → Varsayılan uygulamalar](https://filex.sh/shots/defaultapps/default-apps-1440.27d3c64fe457.png)

<sub>Varsayılan uygulamalar - filex dışında bir şeyin işlediği her dosya türü: onu kimin açtığı ve küçük resmini kimin çizdiği, sizin belirlediğiniz sırayla ([Varsayılan uygulamalar](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![En yeni dosyalarıyla çizilmiş klasörler](https://filex.sh/shots/thumbnails/folders-grid-1440.2b408fb54f7e.png)

<sub>Klasör önizlemeleri - her klasör, içine en son gelen üç dosyayla çizilir; SVG'leri filex'in yerleşik motoru çizer ([docs/thumbnails.md](docs/thumbnails.md#folder-previews))</sub>

</td>
<td width="50%" valign="top">

![Bir mağazanın bağlantısıyla açılan kurulum incelemesi](https://filex.sh/shots/store/store-review-1440.d93b2b571d93.png)

<sub>Mağazadan kurmak - aynı inceleme, *… mağazasından* diye işaretli; ücretli uygulamanın lisans anahtarı yalnızca ilk karakterleriyle görünür ([Mağazadan kurmak](docs/APP-PLUGINS.md#installing-from-a-store))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Bir mağazadan gelen ilk bağlantı: güvenilsin mi?](https://filex.sh/shots/store/store-trust-1440.94bebecac9fe.png)

<sub>Bir mağazadan gelen ilk bağlantı - adresi ve anahtarlarının parmak izleri, güvenmeden önce karşılaştırmanız için ([Güvenilen mağazalar](docs/APP-PLUGINS.md#trusted-stores))</sub>

</td>
<td width="50%" valign="top">

![Bekletilen ücretli bir uygulama, bir yönetim sayfasındaki şerit](https://filex.sh/shots/store/store-license-held-1440.2dd9518d7e87.png)

<sub>Mağazanın iptal ettiği bir lisans - uygulama bekletilir, *Lisanssız* görünür, hiçbir şey kaldırılmaz ve her yönetim sayfasında bir şerit onu adıyla anar ([Ücretli uygulamalar](docs/APP-PLUGINS.md#paid-apps))</sub>

</td>
</tr>
</table>

</details>

<br>

## Yapay zekâ ajanları / MCP

**Bir ajan, ona verdiğiniz klasörde çalışır, başka hiçbir yerde değil.**

- `/api/ai/mcp` adresinde yerleşik bir **MCP sunucusu** ve `/api/ai` adresinde, bir
  OpenAPI 3.1 dosyasıyla tanımlanan bir REST yüzeyi.
- Bir API anahtarı izinlerini eylem bazında taşır ve tek bir klasöre sınırlandırılabilir;
  verdiği hiçbir şey anahtarın kendisinden geniş olamaz.
- Büyük bir dosya bir **yükleme bileti** üzerinden gider: kısa ömürlü, tek kullanımlık bir
  URL; böylece baytları hiçbir zaman modelin bağlamından geçmez.

```bash
claude mcp add filex --transport http https://files.example.com/api/ai/mcp \
  --header "Authorization: Bearer <api-token>"
```

Bir ajanın neler yapabildiği, bir anahtarın asla veremeyecekleri, şifreli klasörler ve
yükleme biletleri: [docs/FEATURES.md → AI agents and MCP](docs/FEATURES.md#ai-agents-and-mcp);
başvuru belgesi [docs/MCP.md](docs/MCP.md).

<br>

# Nextcloud, Dropbox ya da Google Drive'dan geliyorsanız

filex bir dosya yöneticisidir, bir grup çalışması paketi değil. Bu, gelmiş olabileceğiniz
dört şeyin yanında şu anlama gelir (aralarında File Browser da var):

| Geldiğiniz yer | filex ile |
|---|---|
| **Nextcloud**<br>Kendi sunucunuzda çalıştırdığınız ya da bir sağlayıcıdan aldığınız bir iş birliği platformu: dosyalar, çevrelerinde de takvim, kişiler, e-posta, sohbet, görüntülü görüşme ve çevrimiçi bir ofis. Bir web sunucusunun arkasında, veritabanıyla çalışan bir PHP uygulaması. | Yalnızca dosyalar kısmı.<br>İçinde SQLite olan tek bir Go ikili dosyası (isterseniz PostgreSQL ya da MySQL).<br>Canlı klasör eşitlemeli bir masaüstü uygulaması, paylaşım bağlantıları, SSO ve LDAP.<br>Ofis belgeleri, tek başınıza ya da birlikte, ofis düzenleyici uygulamasında ya da bağladığınız ONLYOFFICE'te.<br>Takvim, kişiler, e-posta, sohbet ya da görüntülü görüşme yok. |
| **Dropbox**, **Google Drive**<br>Barındırılan hizmetler: dosyalarınız sağlayıcının sunucularında, onun kotası ve koşulları altında durur. | Kendi sunucunuz ve zaten sahip olduğunuz depo: bir disk, bir NAS paylaşımı, bir S3 kovası.<br>Gündelik işler yerli yerinde: bir web arayüzü, PIN'li ve son kullanma tarihli paylaşım bağlantıları, dosya istekleri, çöp kutusu ve sürüm geçmişi.<br>Seçtiğiniz klasörleri eşitlenmiş tutan bir masaüstü uygulaması. |
| **File Browser**<br>Gösterdiğiniz tek bir dizinin üzerine bir web arayüzü koyan tek bir ikili dosya: kullanıcı hesapları (her birinin kendi kapsamı ve izin anahtarları), yol başına izin ve engelleme kuralları, parolalı ve son kullanma tarihli paylaşım bağlantıları. Reposu arşivlenmiştir ve [README](https://github.com/filebrowser/filebrowser) dosyası artık yeni sürüm, hata düzeltmesi ya da güvenlik düzeltmesi çıkmayacağını söyler. | Aynı tek komutla başlangıç.<br>Yan yana birkaç depo.<br>Klasör bazlı erişimin üzerinde roller ve gruplar; yerleşik OIDC çoklu oturum açma ve LDAP.<br>Çöp kutusu ve sürümler, tam metin arama.<br>S3, SFTP, FTPS, NFSv3 ve WebDAV olarak erişilebilen aynı ağaç. |

**filex'te olmayanlar**, hangisinden gelirseniz gelin:

- **Android ya da iOS uygulaması.** Telefonda filex web uygulamasıdır; kendini bir uygulama
  gibi kurulmaya sunar ([On a phone or a tablet](docs/DESKTOP.md#on-a-phone-or-a-tablet-the-web-app)),
  ama dosyaları göstermek için bağlantı ister; o kapalıyken bildirimler, cihaz için
  açtığınızda Web Push olarak gelir.
- **Finder'da ya da Dosya Gezgini'nde yer tutucu dosyalar.** Masaüstü uygulaması
  bilgisayarda tuttuğunuz klasörleri kopyalar, gerisi kendi penceresinde çevrimiçi kalır
  ([docs/DESKTOP.md](docs/DESKTOP.md#keeping-folders-on-this-computer)).
- **İkili dosyaya gömülü bir ofis düzenleyicisi.** Ofis belgeleri, kurduğunuz ofis
  düzenleyicisi uygulamasında
  ([`filex-office-editor`](https://github.com/BRF-Tech/filex-office-editor), tarayıcıda
  çalışan ONLYOFFICE düzenleyicisi, Document Server gerekmez) tek başınıza ya da birlikte
  düzenlenir, ya da bağladığınız bir ONLYOFFICE Document Server'da;
  ikisi de yoksa salt okunur bir önizlemede açılır ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md)).

<details>
<summary><b>Taşınırken</b> - yanınızda gelenler ve gelmeyenler</summary>

- Dosyalar zaten filex'in bağlayabileceği bir yerdeyse - bir diskte ya da NAS paylaşımında
  bir klasör, bir S3 kovasında bir önek, bir SFTP, FTP ya da WebDAV sunucusu - taşınmak bir
  göç değil, bir bağlamadır. filex dosyaları kendisi saklamaz: bir depoyu o klasöre
  yöneltin, dosyalar olduğu gibi oradadır.
- Dropbox'ta ya da Google Drive'da tutulan dosyaların önce böyle bir yere kopyalanması
  gerekir: filex ikisi için de bir depolama sürücüsü getirmez.
- Yalnızca dosyalar taşınır: eski sistemin tuttuğu bağlantılar, izinler ve sürüm geçmişi
  içe aktarılmaz.
- filex'in ekledikleri - çöp kutusu, sürüm geçmişi, taslaklar - deponun kökündeki gizli
  klasörlerde durur; uçtan uca şifrelediğiniz bir klasör de o andan itibaren şifreli veri
  tutar ([docs/STORAGE.md](docs/STORAGE.md), [docs/TRASH-VERSIONING.md](docs/TRASH-VERSIONING.md)).

</details>

<br>

# Compose ya da Helm ile kendi sunucunuzda çalıştırın

Yukarıdaki `docker run` komutu filex'i denemek için yeter. Gerçek bir kurulum için hazır
yığınlar [`deploy/`](deploy/) dizinindedir:

- **[`deploy/compose/`](deploy/compose/)** - Docker Compose:
  - **minimal** - filex + SQLite + yerel disk (tek servis, sıfır bağımlılık).
  - **full** - filex + PostgreSQL + Redis + Caddy (otomatik HTTPS), ayrıca açılıp
    kapatılabilen ek bileşenler: **OnlyOffice**, **Drawio** ve bir **S3 sunucusu**
    (Versity S3 Gateway). Her birini `.env` dosyasındaki bir Compose profiliyle açıp
    kapatın. Dönüştürme bir yan konteyner değil, [Dönüştür uygulaması](#uygulamalar).
- **[`deploy/helm/filex/`](deploy/helm/filex/)** - Kubernetes için bir Helm chart'ı
  (Deployment + PVC + isteğe bağlı Ingress); her sürümle yayımlanır:
  `helm repo add brf-tech https://helm.brf.sh`, ardından `helm install filex brf-tech/filex`
  (ya da `oci://ghcr.io/brf-tech/charts/filex`). Yukarıdaki her ek bileşen, `values.yaml`
  dosyasında bir `enabled` anahtarıdır - PostgreSQL / Redis / bir S3 sunucusunu birlikte
  kurun ya da dışarıdaki bir OnlyOffice / Drawio'yu bağlayın.

Her kurulum türü için adım adım talimatlar
[docs/INSTALLATION.md](docs/INSTALLATION.md) belgesindedir.

filex kendi alan adının kökünde ya da paylaştığı bir alan adının alt yolunda
(`https://example.com/filex/`) çalışır: tek bir ayar, `FILEX_BASE_PATH`, ve yolun tamamını
ileten bir vekil sunucu - Caddy, nginx ve Helm örnekleri
[docs/DEPLOYMENT.md → Serving filex under a sub-path](docs/DEPLOYMENT.md#serving-filex-under-a-sub-path)
bölümünde.

<br>

# Belgeler

Kılavuzlar [docs.filex.sh](https://docs.filex.sh) adresinde bir site olarak yayımlanır;
[belgelerin tam dizini](docs/README.md) de her sayfayı listeler.

[Kurulum](docs/INSTALLATION.md), [Yapılandırma](docs/CONFIGURATION.md),
[masaüstü uygulaması](docs/DESKTOP.md), [protokoller](docs/PROTOCOLS.md),
[uygulamalar](docs/APP-PLUGINS.md) ve [ONLYOFFICE](docs/ONLYOFFICE.md) ile başlayın.
[Özellikler](docs/FEATURES.md) her özelliğe, sınırları ve kılavuzuyla birlikte bir madde
ayırır.

<br>

# Özellikler

| Alan | Neler sunar |
|---|---|
| **Gezgin** | Yönetim uygulamasında, masaüstü uygulamasında ve her gömülü gezginde tek düzen: gezinti paneli, liste, ızgara ve galeri görünümleri, sekmeler, kişisel ve ekip etiketleri, ⌘K komut paleti, yeniden atanabilen klavye kısayolları, **+ Yeni** menüsünden yeni belgeler ve işletmecinin oluşturduğu temalar ([Entegrasyon](docs/INTEGRATION.md), [Temalar](docs/INTEGRATION.md#themes)) |
| **Depolama** | Yan yana bağlanan yerel diskler, S3, FTP, SFTP, WebDAV ve SMB/NAS, aralarında kopyalama ve taşıma, geri kalan her şey için de yönetim panelinden ya da bir uygulama mağazasından kurulan bir eklenti ([Depolama](docs/STORAGE.md), [Depolama eklentileri](docs/PLUGINS.md)) |
| **Tarayıcı olmadan** | S3, SFTP, FTPS, NFSv3 ve WebDAV olarak sunulan aynı ağaç - orada yapılan bir taşıma ya da silme, bir depo taramasının yanında web arayüzündeki kadar güvenlidir - ayrıca HTTPS üzerinden `filex mount` ([Protokoller](docs/PROTOCOLS.md)) |
| **Oturum açma** | OIDC, LDAP / Active Directory, Windows ve Linux hesapları ve giriş denemesi sınırları ([SSO](docs/SSO.md), [LDAP](docs/LDAP.md), [İşletim sistemi hesapları](docs/OS-LOGIN.md)) |
| **Kişiler ve erişim** | Roller ve kişiye özel izinler, gruplar, klasör bazlı erişim ve yerleşik çok kiracılılık ([İzinler](docs/PERMISSIONS.md), [Gruplar](docs/GROUPS.md), [Çok kiracılılık](docs/MULTI-TENANCY.md)) |
| **Paylaşım** | PIN'li, son kullanma tarihli ve indirme limitli herkese açık bağlantılar, dosya istekleri ve e-postayla paylaşım davetleri ([Paylaşım](docs/SHARING.md)) |
| **Koruma** | Çöp kutusu ve sürüm geçmişi, isteğe bağlı ClamAV taraması, uçtan uca şifreli klasörler, ikinci bir depoya replikasyon (depoda zaten bulunanların ilk kopyasıyla birlikte) ve bir denetim kaydı ([Çöp kutusu ve sürümleme](docs/TRASH-VERSIONING.md), [Koruma](docs/PROTECTION.md), [Uçtan uca şifreleme](docs/E2E-ENCRYPTION.md), [Replikasyon](docs/REPLICATION.md), [Denetim kaydı](docs/BACKEND.md#admin-audit-log)) |
| **Görüntüleyiciler ve düzenleyiciler** | Görseller, video, ses, PDF, Markdown, kod (Monaco), ofis düzenleyici uygulamasında ya da ONLYOFFICE üzerinden Office belgeleri, draw.io diyagramları, çizilen ve metin olarak düzenlenen Mermaid diyagramları, 3B modeller ve ZIP, 7z, TAR arşivleri ([ONLYOFFICE](docs/ONLYOFFICE.md), [Arşivler](docs/ARCHIVES.md)) |
| **Arama ve küçük resimler** | İzinlere uyan gömülü tam metin arama; görseller, video, PDF, klasörler ve ONLYOFFICE bağlıyken Office belgeleri için küçük resimler ([Arama](docs/SEARCH.md), [Küçük resimler](docs/thumbnails.md)) |
| **Gerçek zamanlı** | WebSocket üzerinden kimin açık olduğu ve canlı dosya güncellemeleri, bir de bir uygulamada filex üzerinden birlikte düzenleme ([Gerçek zamanlılık](docs/REALTIME.md), [Birlikte düzenleme](docs/APP-PLUGINS-API.md#editing-together-coedit-056)) |
| **İstemciler** | Klasör eşitlemeli bir masaüstü uygulaması, telefonun kurulumunuzun adı ve logosuyla kurduğu bir web uygulaması, bir CLI, Vue / React / web bileşeni olarak gömme, bir REST API ve bir MCP sunucusu ([Masaüstü](docs/DESKTOP.md), [Eşitleme](docs/SYNC.md), [CLI](docs/CLI.md), [Entegrasyon](docs/INTEGRATION.md), [MCP](docs/MCP.md)) |
| **Uygulamalar ve diller** | filex üzerinden yazdıran, bir dosyayı filex üzerinden birlikte düzenleyen ve şifreli bir klasördeki dosyayı yalnız siz izin verdiğinizde, tarayıcınızda çözülmüş olarak açan yalıtılmış uygulamalar (e-İmza, Dönüştür, Ofis düzenleyici, filextext, draw.io); dil paketleri ve sağdan sola yerleşim ([Uygulamalar](docs/APP-PLUGINS.md), [Bir uygulamada düzenleme](docs/E2E-ENCRYPTION.md#editing-in-an-app), [Sağdan sola diller](docs/RTL.md)) |
| **İşletme** | Tek bir ikili dosya ya da tek bir konteyner; SQLite, PostgreSQL ya da MySQL; her sayfa, ayar ve kayıt için tek bir araması olan yönetim paneli; webhook'lar, uygulama içi çan ve telefonlara ve tarayıcılara Web Push, isteğe bağlı bir özetle; bir kullanım ve maliyet görünümü (bugün Backblaze B2); güncelleme denetimleri ve izin verdiğinizde kendini kuran yama sürümleri ([Veritabanları](docs/DATABASES.md), [Yönetim paneli](docs/ADMIN-PANEL.md), [Bildirimler](docs/NOTIFICATIONS.md), [Kullanım](docs/USAGE.md), [Güncellemeler](docs/UPDATES.md)) |

Her özellik kendi maddesiyle - nasıl davrandığı, sınırları ve onu anlatan sayfa:
[docs/FEATURES.md](docs/FEATURES.md).

<br>

# Mimari

Bkz. [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

<details>
<summary><b>Parçalara bir bakışta</b> - sürücüler, protokoller ve paketler tek bir şemada</summary>

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
│  Co-editing:     │  sealed relay for apps · watchers        │
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

</details>

<br>

# filex nasıl geliştiriliyor

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

# Katkı

[![CI](https://img.shields.io/github/actions/workflow/status/BRF-Tech/filex/ci.yml?branch=main&label=ci)](https://github.com/BRF-Tech/filex/actions)

Issue ve pull request'lerinizi bekliyoruz; büyükçe bir pull request'ten önce ne yapmak
üzere olduğunuzu anlatan bir issue açın. [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) iş
akışını, bir değişikliğin geçmesi gereken testleri ve belge kurallarını anlatır; proje
[Contributor Covenant](CODE_OF_CONDUCT.md) kurallarına uyar. Bir güvenlik sorunu,
[SECURITY.md](SECURITY.md) dosyasında anlatıldığı gibi gizlice bildirilir. Her sürümde
nelerin değiştiği [CHANGELOG.md](CHANGELOG.md) dosyasındadır. Her gün İstanbul saatiyle
10:00'da, `main` o anda yeni bir şey içeriyorsa içerdiklerinden bir sürüm çıkarılır;
bu kesimden sonra birleştirilen bir pull request ertesi günün sürümüyle gelir
([Sürüm süreci](docs/CONTRIBUTING.md#release-process)).

<details>
<summary><b>Geliştirme</b> - kaynaktan derleme ve neyin nerede durduğu</summary>

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
  uygulaması) + `shots/`: yukarıdaki ekran görüntülerini almak için `pnpm shots`
  komutunun çalıştırdığı betikler (görüntüler depoda tutulmaz, filex.sh'ta yayımlanır)
- `docker/` - Dockerfile'lar + compose
- `deploy/` - hazır Compose yığınları + Helm chart'ı (bkz. [`deploy/`](deploy/))
- `docs/` - Markdown belgeleri
- `docs-site/` - [docs.filex.sh](https://docs.filex.sh) adresinde yayımlanan VitePress sitesi

</details>

<br>

# Lisans

MIT - bkz. [LICENSE](LICENSE).

[`docs/badges/`](docs/badges/) içindeki mağaza rozetleri mağazaların kendi görselleridir;
değiştirilmeden kullanılmıştır ve bu lisansın kapsamında değildir: Microsoft ve
Microsoft Store rozeti, Microsoft şirketler grubunun ticari markalarıdır; Snap Store rozeti
© Canonical Ltd.; [CC BY-ND 2.0 UK](https://creativecommons.org/licenses/by-nd/2.0/uk/) ile
lisanslanmıştır.

Nextcloud, Dropbox, Google Drive, File Browser ve ONLYOFFICE başka tarafların ürünlerinin
adlarıdır ve burada yalnızca onları tanımlamak için kullanılmıştır. filex bunların hiçbiriyle
bağlantılı değildir, hiçbiri tarafından desteklenmez ya da onaylanmaz.
