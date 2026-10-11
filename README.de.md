<!-- Translated from README.md as of v0.56.0 (the shortened README of #218). The English README is the source: change it first, then carry the change here. -->

<div align="center">

<img src="docs/logo.png" alt="filex-Logo" width="96">

# filex

**Selbst gehosteter Dateimanager mit Freigaben und Weboberfläche, in einer einzigen Go-Binärdatei - eine Open-Source-Alternative zu Nextcloud, Dropbox und Google Drive.**<br>
Webbasierter Dateibrowser für lokalen, S3-, SFTP-, WebDAV-, FTP- und SMB-Speicher: eine private Cloud, bereitgestellt als S3, SFTP, NFS, WebDAV.<br>
SSO/LDAP, mandantenfähig, verschlüsselte Ordner, einbettbare Webkomponente, Desktop-App, MCP-Server.

[![Live-Demo ausprobieren](https://img.shields.io/badge/Try_the_live_demo-f59e0b?style=for-the-badge)](https://demo.filex.sh)
[![Schnellstart](https://img.shields.io/badge/Quick_start-2f6ceb?style=for-the-badge)](#schnellstart)
[![Dokumentation](https://img.shields.io/badge/Documentation-374151?style=for-the-badge)](https://docs.filex.sh)

[![Release](https://img.shields.io/github/v/release/BRF-Tech/filex?color=2f6ceb)](https://github.com/BRF-Tech/filex/releases)
[![License: MIT](https://img.shields.io/github/license/BRF-Tech/filex?color=22c55e)](LICENSE)

[English](README.md) · [Türkçe](README.tr.md) · **Deutsch** · [Español](README.es.md) · [Français](README.fr.md) · [简体中文](README.zh-CN.md)

<sub>Dies ist eine Übersetzung der [englischen README](README.md); wo die beiden voneinander abweichen, gilt der englische Text. Sie wurde maschinell übersetzt, eine Durchsicht durch Muttersprachler steht noch aus - Korrekturen sind willkommen. Die Dokumente, auf die sie verweist, sind auf Englisch.</sub>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://filex.sh/shots/explorer-grid-dark.e032d7456831.png">
  <img src="https://filex.sh/shots/explorer-grid-light.de398671d43b.png" alt="filex-Explorer - Raster mit Vorschaubildern" width="900">
</picture>

</div>

<table>
<tr>
<td width="50%" valign="top">

**🗂️ Jeder Speicher, ein Baum**

Binden Sie nebeneinander ein, was Sie schon haben, und kopieren oder schneiden Sie in einem Speicher aus und fügen Sie in einem anderen ein. Lassen Sie `rclone`, `restic`, WinSCP oder FileZilla auf denselben Baum zeigen.

[Speicher und Protokolle →](#speicher-und-protokolle)

</td>
<td width="50%" valign="top">

**🔗 Teilen und schützen**

Links mit PIN, Ablaufdatum und Download-Limit. Papierkorb, Versionsverlauf und Ende-zu-Ende-verschlüsselte Ordner.

[Freigaben und Schutz →](#freigaben-und-schutz)

</td>
</tr>
<tr>
<td width="50%" valign="top">

**🔑 Ihre Konten, Ihre Rollen**

OIDC, LDAP / Active Directory oder die Windows- und Linux-Konten des Servers. Rollen, Gruppen, mandantenfähig.

[Personen und Zugriff →](#personen-und-zugriff)

</td>
<td width="50%" valign="top">

**💻 Eine Desktop-App mit Live-Ordner-Sync**

Windows, Linux und macOS (Apple Silicon): Eine Änderung auf der einen Seite ist in etwa einer Sekunde auf der anderen.

[Desktop-App & CLI →](#desktop-app--cli)

</td>
</tr>
<tr>
<td width="50%" valign="top">

**🧩 Lässt sich in Ihr Produkt einbetten**

Dieselbe Oberfläche als Vue-3-Komponente, als React-Komponente und als Webkomponente `<filex-explorer>`.

[In Ihre Anwendung einbetten →](#in-ihre-anwendung-einbetten)

</td>
<td width="50%" valign="top">

**🤖 Apps und KI-Agenten**

Apps in einer Sandbox - E-Signatur, Konvertieren, draw.io - und ein integrierter MCP-Server, den ein Agent steuern kann.

[Apps →](#apps) · [KI-Agenten / MCP →](#ki-agenten--mcp)

</td>
</tr>
</table>

<p align="center"><a href="#warum-filex">Warum filex</a> · <a href="#umstieg-von-nextcloud-dropbox-oder-google-drive">Umstieg von einem anderen Werkzeug</a> · <a href="#selbst-hosten-mit-compose-oder-helm">Selbst hosten</a> · <a href="#funktionen">Funktionen</a></p>

<br>

# Schnellstart

> [!TIP]
> **Erst ansehen, dann installieren:** [demo.filex.sh](https://demo.filex.sh) - melden Sie sich mit
> `demo@demo.com` / `demo` an (Admin-Rolle, Sandbox wird jede Nacht zurückgesetzt).

Starten Sie Ihre eigene Instanz, aus dem Ordner, den Sie bereitstellen wollen:

```bash
docker run -p 5212:5212 \
  -e FILEX_DEFAULT_STORAGE_DRIVER=local -e FILEX_DEFAULT_STORAGE_PATH=/srv/files \
  -v filex-data:/data -v "$PWD:/srv/files" \
  ghcr.io/brf-tech/filex:latest
```

Öffnen Sie http://localhost:5212/admin - der erste Start gibt Admin-Anmeldedaten und eine
Anleitung zum Einbetten auf der Konsole aus. Diese URL gehört dem Betreiber; die Personen,
denen Sie ein Konto geben, bekommen **http://localhost:5212/drive**, denselben
Dateimanager ohne den Adminbereich darum herum.

<details>
<summary><b>Was dieser Befehl einbindet und als welcher Benutzer er läuft</b></summary>

Das stellt **den Ordner bereit, in dem Sie es gestartet haben** - öffnen Sie die
Oberfläche, und Ihre Dateien sind schon da. `/data` ist das eigene Verzeichnis von filex
(SQLite-Datenbank, Suchindex, Vorschaubild-Cache), deshalb ist es ein benanntes Volume
und nicht der Ordner, in dem Sie Dateien ablegen; beides ist absichtlich getrennt. Lassen
Sie `$PWD` auf einen anderen Ort zeigen, oder fügen Sie später im Adminbereich weitere
Speicher hinzu - ein Bucket mit mehreren Ordnern der obersten Ebene lässt sich in einem
Schritt als je ein Speicher pro Ordner einbinden
(*Speicher → Speicher hinzufügen → Mehrere Ordner auf einmal einbinden*).

Der Container läuft standardmäßig als **root**, daher gehört alles, was er in `/data`
schreibt, root; setzen Sie `PUID`/`PGID`, damit er als Ihr eigener Benutzer läuft
([docs/DOCKER.md](docs/DOCKER.md#which-user-the-container-runs-as)).

</details>

<details>
<summary><b>Kein Docker? Starten Sie die Binärdatei</b></summary>

Laden Sie das Archiv für Ihre Plattform aus dem
[neuesten Release](https://github.com/BRF-Tech/filex/releases/latest) herunter, entpacken
Sie es und starten Sie die Binärdatei ([docs/INSTALLATION.md](docs/INSTALLATION.md#binary)):

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

Für eine produktive Installation gibt es Compose-Stacks und ein Helm-Chart:
[Selbst hosten mit Compose oder Helm](#selbst-hosten-mit-compose-oder-helm). Das Image liegt auf
[GitHub Packages](https://github.com/BRF-Tech/filex/pkgs/container/filex).

<br>

# Warum filex

Die meisten selbst gehosteten Dateimanager sind entweder **zu klein** (eine
Verzeichnisliste mit Uploads) oder **zu groß** (eine Groupware-Suite, die man wegen des
Datei-Tabs installiert). filex zielt auf die Lücke dazwischen. Der Rundgang unten hat acht
Teile, jeder mit seinen Screenshots darunter eingeklappt. Jede Funktion im Detail, mit
ihren Grenzen und der Anleitung, die sie beschreibt, steht in [docs/FEATURES.md](docs/FEATURES.md).

<br>

## Der Explorer

**Ein Dateimanager, den man schon zu bedienen weiß.**

- Ein Navigationsbereich mit **Start · Meine Dateien · Mit mir geteilt · Meine Freigaben ·
  Zuletzt verwendet · Favoriten · Entwürfe · Papierkorb**; Listen-, Raster- und
  Galerieansicht; Tabs, Tags und eine ⌘K-Befehlspalette.
- Anwesenheits-Avatare und Dateiänderungen kommen live an, über WebSocket.
- Stellen Sie ein Design in Ihren eigenen Farben zusammen und machen Sie es zum Standard:
  Die Anmeldeseite und jeder öffentliche Link tragen es ebenfalls, und ein Smartphone
  installiert die Web-App unter dem Namen und dem Logo Ihrer Instanz.
- Englisch und Türkisch sind eingebaut, weitere Sprachen werden als Pakete installiert, und
  für Sprachen, die so gelesen werden, richtet sich die Oberfläche von rechts nach links aus.

Alle Funktionen des Explorers im Detail: [docs/FEATURES.md → The explorer](docs/FEATURES.md#the-explorer).

<p align="center"><img src="https://filex.sh/shots/driveshell/driveshell-hero-1440.16ace59f07c3.png" alt="Die filex-Shell" width="860"></p>

<details>
<summary><b>Screenshots des Explorers</b> - die Shell, die Suche, die Navigation, Tags, der Papierkorb, die Glocke, Designs, Symlinks, CSV in ONLYOFFICE</summary>

<table>
<tr>
<td width="50%" valign="top">

![Die filex-Shell](https://filex.sh/shots/driveshell/driveshell-hero-1440.16ace59f07c3.png)

<sub>Die Shell - wo alle landen</sub>

</td>
<td width="50%" valign="top">

![Einen Ordner durchsuchen](https://filex.sh/shots/driveshell/driveshell-search-1440.c0f3ceda0ec4.png)

<sub>Diesen Ordner durchsuchen; `⌘K` / `Ctrl K` übergibt die Suchanfrage an die Befehlspalette</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Navigationsbereich](https://filex.sh/shots/sidenav/sidenav-expanded-1440.461d5aaaff2a.png)

<sub>Navigationsbereich - Start, Mit mir geteilt, Meine Freigaben, Zuletzt verwendet, Favoriten, Papierkorb und die Speicher, die Sie erreichen können</sub>

</td>
<td width="50%" valign="top">

![Zu einer Leiste eingeklappt](https://filex.sh/shots/sidenav/sidenav-rail-1440.843a2158380d.png)

<sub>Zur Icon-Leiste eingeklappt</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Persönliche Tags und Team-Tags](https://filex.sh/shots/tags/tags-kinds-1440.64e4d6a5ccbc.png)

<sub>Tags - Ihre eigenen oder die Ihres Teams; ein Tag öffnet alle Dateien, die es tragen, aus allen Ordnern, in denen sie liegen</sub>

</td>
<td width="50%" valign="top">

![Die Ansicht „Papierkorb“](https://filex.sh/shots/sidenav/view-trash-1440.ab3cfb3b01cf.png)

<sub>Papierkorb - was gelöscht wurde, woher es kam und wie viel Zeit bleibt, bis es verschwindet</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Die Glocke mit ihrem Badge für Ungelesenes, geöffnet](https://filex.sh/shots/signing/bell-badge-1440.61396b9ab714.png)

<sub>Die Glocke - darauf die Zahl der ungelesenen Benachrichtigungen, jede Zeile führt an den Ort, den sie nennt</sub>

</td>
<td width="50%" valign="top">

![Die vollständige Liste der Benachrichtigungen über dem Explorer](https://filex.sh/shots/signing/notifications-list-1440.3176161f2110.png)

<sub>Alle Ihre Benachrichtigungen, im Explorer - für alle, nicht nur für Administratoren</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Der Design-Editor](https://filex.sh/shots/appearance/theme-editor-1440.5b28006557f8.png)

<sub>Darstellung - stellen Sie ein Design in Ihren eigenen Farben zusammen, mit Vorschau, während Sie tippen</sub>

</td>
<td width="50%" valign="top">

![Der Explorer trägt das Design des Betreibers](https://filex.sh/shots/appearance/themed-explorer-1440.dbe464fc39c7.png)

<sub>Ist es als Standard festgelegt, trägt es der Explorer bei allen…</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Die Anmeldeseite trägt das Design des Betreibers](https://filex.sh/shots/appearance/themed-signin-1440.98801a84bea2.png)

<sub>…und die Anmeldeseite, bevor sich irgendjemand angemeldet hat</sub>

</td>
<td width="50%" valign="top">

![Ein Symlink, der den Speicher verlässt, gekennzeichnet](https://filex.sh/shots/symlinks/symlink-badge-1440.0d128385d287.png)

<sub>Ein Symlink, dem filex nicht folgt, sagt das - in der Dateiliste und in Worten in seinen Details</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Eine CSV-Datei mit Semikolon als Trennzeichen, in der Tabellenkalkulation von ONLYOFFICE geöffnet](https://filex.sh/shots/csvoffice/csv-view-1440.83237ba55d3d.png)

<sub>Eine `.csv`-Datei öffnet sich in der Tabellenkalkulation von ONLYOFFICE, wenn eines angebunden ist - zuerst zum Ansehen und ohne Dialog für das Trennzeichen: Das eigene Trennzeichen der Datei wird mitgegeben ([CSV-Dateien](docs/ONLYOFFICE.md#csv-files))</sub>

</td>
<td width="50%" valign="top">

![Die CSV-Datei im Editor von ONLYOFFICE, mit dem Hinweis, was beim Speichern erhalten bleibt](https://filex.sh/shots/csvoffice/csv-edit-1440.4efc379a293d.png)

<sub>…und ihr Editor, der sagt, was beim Speichern als CSV erhalten bleibt; die Datei geht als dieselbe Art von CSV zurück, und Zellen, die niemand geändert hat, behalten ihren Text - `007` bleibt `007` ([unveränderte Zellen](docs/ONLYOFFICE.md#cells-nobody-changed-keep-their-text))</sub>

</td>
</tr>
</table>

</details>

<br>

## Speicher und Protokolle

**Jeder Speicher in einem Baum, und dieser Baum ist von überall erreichbar.**

- Binden Sie lokale Datenträger, S3, FTP, SFTP, WebDAV und SMB/NAS-Freigaben nebeneinander
  ein, und kopieren oder schneiden Sie in einem aus und fügen Sie in einem anderen ein.
- Erreichen Sie denselben Baum als **S3**, **SFTP**, **FTPS**, **NFSv3** und **WebDAV**:
  Lassen Sie `rclone`, `restic`, `aws s3`, WinSCP oder FileZilla darauf zeigen. Es gelten
  dieselben filex-Berechtigungen (pro Person, Gruppe und Ordner), derselbe Papierkorb und
  dasselbe Kontingent wie in der Weboberfläche; die POSIX-Bits und ACLs auf dem Datenträger
  werden keinen filex-Benutzern zugeordnet.
- `filex mount` bindet einen entfernten Server über gewöhnliches HTTPS ein: als Ordner unter
  Linux, als Laufwerksbuchstabe unter Windows (macOS wird nicht unterstützt).
- Für einen Speicher, den filex nicht mitliefert, installieren Sie ein Plug-in aus dem
  Adminbereich oder über den Link eines App-Stores.

Alle Funktionen zu Speicher und Protokollen im Detail: [docs/FEATURES.md → Storage and protocols](docs/FEATURES.md#storage-and-protocols).

<p align="center"><img src="https://filex.sh/shots/sidenav/connect-1440.8bfbe35780f4.png" alt="So verbinden Sie sich" width="860"></p>

<details>
<summary><b>Screenshots zu Speicher und Protokollen</b> - die Verbindungsanleitungen, API-Schlüssel und ein Speicher-Plug-in</summary>

<table>
<tr>
<td width="50%" valign="top">

![So verbinden Sie sich](https://filex.sh/shots/sidenav/connect-1440.8bfbe35780f4.png)

<sub>So verbinden Sie sich - die Anleitungen, aus *Ihrer* Installation erzeugt</sub>

</td>
<td width="50%" valign="top">

![API-Schlüssel](https://filex.sh/shots/sidenav/apikeys-minted-1440.cfc100f30069.png)

<sub>API-Schlüssel - erstellen Sie Ihre eigenen, im Explorer oder in einer Einbettung (Sitzung oder Schlüssel einer Person; eine Einbettung, die über einen Proxy mit einem gemeinsamen *App*-Schlüssel läuft, bekommt diesen Eintrag nicht)</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Verbindungsanleitung](https://filex.sh/shots/connections-guide.98a4f82ecd9d.png)

<sub>filex von allem aus erreichen - S3, SFTP, FTPS, NFS, WebDAV. Jeder Befehl wird aus *Ihrer* Installation erzeugt</sub>

</td>
<td width="50%" valign="top">

![Plug-ins](https://filex.sh/shots/admin-plugins.c25fa69cfc7c.png)

<sub>Ein Speicher, den filex nicht mitliefert - unter **Plug-ins → Speicher-Plug-ins** als Plug-in installiert, das sein eigenes Konfigurationsformular beschreibt</sub>

</td>
</tr>
</table>

</details>

<br>

## Freigaben und Schutz

**Teilen Sie mit einem Link, den Sie begrenzen und widerrufen können. Machen Sie ein versehentliches Löschen oder Überschreiben rückgängig.**

- Öffentliche Links mit PIN, Ablaufdatum und Download-Limit sowie Dateianforderungen für das,
  was hereinkommen soll. **Meine Freigaben** listet jeden Link auf, den Sie erstellt haben,
  zum Kopieren oder Widerrufen.
- Teilen Sie einen Ordner mit einer Person oder einer Gruppe, und er erscheint bei ihnen unter
  **Mit mir geteilt**. Dafür muss beim Speicher die **Zugriffskontrolle pro Element (RBAC)**
  eingeschaltet sein, was sie standardmäßig nicht ist.
- Löschungen sind innerhalb einer Aufbewahrungsdauer umkehrbar, und Schreibvorgänge
  hinterlassen Snapshots, beides in dem Speicher, den Sie bereits eingebunden haben.
- Ende-zu-Ende-verschlüsselte Ordner: im Browser verschlüsselt, und der Server erhält nie
  einen Schlüssel. Eine App, die Sie zulassen, bearbeitet eine solche Datei in Ihrem Browser,
  der sie für die App entschlüsselt und verschlüsselt, was sie speichert. Optionaler
  ClamAV-Scan jeder geschriebenen Datei.

Alle Funktionen zu Freigaben und Schutz im Detail: [docs/FEATURES.md → Sharing and protection](docs/FEATURES.md#sharing-and-protection).

<p align="center"><img src="https://filex.sh/shots/public-share.0e3ba07f7c88.png" alt="Ein öffentlicher Freigabelink, wie ihn der Empfänger sieht" width="860"></p>

<details>
<summary><b>Screenshots zu Freigaben und Schutz</b> - der Dialog „Teilen“, was der Empfänger sieht, Meine Freigaben, wer verschlüsseln darf</summary>

<table>
<tr>
<td width="50%" valign="top">

![Der Dialog „Teilen“](https://filex.sh/shots/share-modal.d6f6572228ea.png)

<sub>Freigaben - PIN, Ablaufdatum, Download-Limit, `curl`-Einzeiler</sub>

</td>
<td width="50%" valign="top">

![Markdown-Vorschau](https://filex.sh/shots/viewer-markdown.1789ecdcfbc5.png)

<sub>Markdown-Vorschau</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Ein öffentlicher Freigabelink, wie ihn der Empfänger sieht](https://filex.sh/shots/public-share.0e3ba07f7c88.png)

<sub>…und was die Person am anderen Ende öffnet. filex hat EINEN Bildschirm nach außen - eine geteilte Datei, ein Ordner, eine Dateianforderung, die Unterschriftsseite einer App und die PIN vor jedem davon sind alle diese Seite, im Namen Ihrer Instanz</sub>

</td>
<td width="50%" valign="top">

![Meine Freigaben, das Menü „Aktionen“ einer Zeile geöffnet](https://filex.sh/shots/signing/my-shares-1440.6f8676cbb555.png)

<sub>Meine Freigaben - die Links, die Sie erstellt haben, und deren PINs, wenn Sie eine weitergeben müssen</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Admin → Freigaben, das Menü „Aktionen“ einer Zeile geöffnet](https://filex.sh/shots/signing/admin-table-actions-1440.330fcc5fda73.png)

<sub>Jede Tabelle im Adminbereich - ein angeheftetes Menü **Aktionen** pro Zeile, dasselbe Menü, das sich über das ⋮ des Explorers öffnet</sub>

</td>
<td width="50%" valign="top">

![Admin → Verschlüsselung: die Genehmigungsrichtlinie und drei wartende Anfragen](https://filex.sh/shots/encryption/admin-encryption-1440.b74163acb93c.png)

<sub>Wer verschlüsseln darf - aus, nur Administratoren, alle, deren Rolle es erlaubt, oder nach Genehmigung durch einen Administrator; die wartenden Anfragen, jeweils mit der Angabe, wer gefragt hat und warum ([wer verschlüsseln darf](docs/E2E-ENCRYPTION.md#who-may-encrypt))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Die Anfrage nach einem verschlüsselten Ordner, mit Begründung](https://filex.sh/shots/encryption/request-new-folder.e74894fba30f.png)

<sub>…und aus Sicht der Person: die Anfrage, die der Dialog „Neuer Ordner“ öffnet - ein einzelner neuer verschlüsselter Ordner, bei einem Administrator mit Begründung angefragt</sub>

</td>
</tr>
</table>

</details>

<br>

## Personen und Zugriff

**Die Konten, die Ihre Leute schon haben, und für jeden genau eine Rolle.**

- Anmeldung mit einem lokalen Passwort, OIDC, LDAP / Active Directory, einem
  authentifizierenden Proxy oder dem Windows- oder Linux-Konto des Rechners, auf dem filex
  läuft.
- Jeder hat genau eine Rolle - Administrator, Benutzer, Betrachter oder eine eigene -,
  aufgebaut aus benannten Berechtigungen. Gruppen bringen Ordnerzugriff und eine Rolle mit,
  können den Gruppen Ihres Verzeichnisses folgen, und Ausnahmen pro Person gehen der Rolle vor.
- Passwörter zu erraten dauert lange: Falsche Passwörter werden pro Konto und pro Adresse
  gezählt, im Webformular ebenso wie bei WebDAV, FTPS und SFTP.
- Von Grund auf mandantenfähig, hinter einem einzigen Schalter: Jeder Mandant hat eigene
  Anmeldeanbieter, eine eigene Domain und ein eigenes Zertifikat und verwaltet sich selbst.
  Ausgeschaltet zeigt keine Seite einen Mandanten.

Alle Funktionen zu Personen und Zugriff im Detail: [docs/FEATURES.md → People and access](docs/FEATURES.md#people-and-access).

<p align="center"><img src="https://filex.sh/shots/roles/roles-list-1440.77477a2c7001.png" alt="Admin → Rollen: die integrierten Rollen und zwei benutzerdefinierte" width="860"></p>

<details>
<summary><b>Screenshots zu Personen und Zugriff</b> - Rollen, Gruppen, Anmeldesicherheit, ein gesperrtes Konto, der Adminbereich und sein Menü</summary>

<table>
<tr>
<td width="50%" valign="top">

![Admin → Rollen: die integrierten Rollen und zwei benutzerdefinierte](https://filex.sh/shots/roles/roles-list-1440.77477a2c7001.png)

<sub>Rollen - Administrator, Benutzer, Betrachter und eigene Rollen: wer sie jeweils hat, was sie erlauben, wo sie je nach Ordner abweichen, ihre Einschränkungen ([docs/PERMISSIONS.md](docs/PERMISSIONS.md))</sub>

</td>
<td width="50%" valign="top">

![Admin → Gruppen](https://filex.sh/shots/groups/groups-list-1440.73224b70e40b.png)

<sub>Gruppen - benannte Mengen von Personen mit Ordnerzugriff und einer Rolle; Mitglieder von Hand oder im Gleichschritt mit den Gruppen, die eine Anmeldung mitbringt ([docs/GROUPS.md](docs/GROUPS.md))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Einen Ordner mit einer Gruppe teilen](https://filex.sh/shots/groups/share-group-1440.31c2a81e1eeb.png)

<sub>Einen Ordner mit einer Gruppe teilen, neben Personen - „Eigentümer“ wird im Dialog erst nach Rückfrage erteilt, nicht per Klick</sub>

</td>
<td width="50%" valign="top">

![Admin → Anmeldesicherheit](https://filex.sh/shots/loginsecurity/login-security-1440.077cec3f1b0a.png)

<sub>Anmeldesicherheit - die Begrenzung der Anmeldeversuche und die erlaubten Adressen, die nie mitgezählt werden; vertrauenswürdige Proxys, die Sperren und das Anmeldeprotokoll folgen auf derselben Seite ([Begrenzung der Anmeldeversuche](docs/CONFIGURATION.md#sign-in-attempt-limits))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Das Anmeldeformular bei einem gesperrten Konto](https://filex.sh/shots/loginsecurity/login-locked-1440.3f4c6d3233f3.png)

<sub>…und was das Anmeldeformular eines gesperrten Kontos sagt, während es die Sperre auf seiner Schaltfläche herunterzählt</sub>

</td>
<td width="50%" valign="top">

![Übersicht im Adminbereich](https://filex.sh/shots/admin-dashboard.d94a065baab6.png)

<sub>Adminbereich</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Startseite der Demo](https://filex.sh/shots/demo-landing.bb2b8d97d4ff.png)

<sub>Startseite der Demo</sub>

</td>
<td width="50%" valign="top">

![Der Bereich „Personen & Sicherheit“ des Adminmenüs, geöffnet über der Seite Admin → Benutzer](https://filex.sh/shots/megamenu/people-panel-1440.2efdcb9a685a.png)

<sub>Das Adminmenü - alle Seiten in drei Bereichen, **Dateien & Speicher**, **Personen & Sicherheit** und **System**, mit einer kurzen Zeile unter jeder Seite; ein Smartphone bekommt dieselben Seiten in einer ausfahrbaren Leiste ([docs/ADMIN-PANEL.md](docs/ADMIN-PANEL.md))</sub>

</td>
</tr>
</table>

</details>

<br>

## Desktop-App & CLI

**Ordner auf Ihrem Computer bleiben live auf demselben Stand wie der Server.**

- Eine App für Windows, Linux und macOS (Apple Silicon): derselbe Explorer in einem Fenster,
  mit mehreren Konten oder Mandanten nebeneinander.
- Rechtsklick auf einen Ordner → **Auf diesem Computer behalten**, und er bleibt in beiden
  Richtungen auf demselben Stand wie der Server, in etwa einer Sekunde. Alles andere bleibt im
  Fenster nur online.
- Sie meldet sich über Ihren Browser an, sodass sich SSO und MFA wie im Web verhalten, und
  sie öffnet Office-Dokumente von Ihrer eigenen Festplatte in dem Editor, den Ihr Server
  betreibt.
- Headless-Rechner bekommen dieselbe Engine: `filex sync` und `filex client`.

**Installieren Sie sie** aus dem Microsoft Store (Windows 10/11) oder dem Snap Store
(Ubuntu und andere Linux-Systeme mit snapd):

<p>
<a href="https://apps.microsoft.com/detail/9PKXDJLVZWXW"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/badges/ms-store-light.svg"><img src="docs/badges/ms-store-dark.svg" alt="Aus dem Microsoft Store herunterladen" height="52"></picture></a>&nbsp;&nbsp;&nbsp;
<a href="https://snapcraft.io/filex-app"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/badges/snap-store-white.svg"><img src="docs/badges/snap-store-black.svg" alt="Im Snap Store erhältlich" height="52"></picture></a>
</p>

oder mit einem Paketmanager:

```bash
brew install brf-tech/filex/filex-app       # macOS 13+ (Apple Silicon), Homebrew tap BRF-Tech/homebrew-filex
sudo snap install filex-app                 # the same snap as the badge above
```

<details>
<summary><b>Mehr zur Installation und zur CLI</b> - der Store-Build, winget, die Release-Downloads, Linux, arm64, die Client-Befehle</summary>

Der Store-Build (*filex File Manager*) ist die einzige Windows-Variante, die signiert ist -
Microsoft signiert sie -, und der Store hält sie aktuell. Das winget-Paket der Desktop-App
(`BRFTech.filex-app`) wird mit jedem Release eingereicht und wartet auf seine erste
Prüfung durch die winget-Moderatoren, sodass `winget install BRFTech.filex-app` das Paket
noch nicht findet. Installer, portable `.exe`, AppImage, `.deb`, `.rpm` und `.dmg` sind dem
[neuesten Release](https://github.com/BRF-Tech/filex/releases/latest) angehängt - noch
nicht signiert, rechnen Sie also beim Windows-Installer mit einer SmartScreen-Meldung.
Details: [docs/DESKTOP.md](docs/DESKTOP.md). Die CLI allein:
`brew install brf-tech/filex/filex`, unter Windows `winget install BRFTech.filex`
([docs/CLI.md](docs/CLI.md)).

Unter Linux brauchen `.deb` und `.rpm` nichts; ab Ubuntu 23.10 braucht ein AppImage einmalig
ein AppArmor-Profil ([AppImage on recent Ubuntu](docs/DESKTOP.md#appimage-on-recent-ubuntu)),
und das Snap läuft innerhalb der strikten Snap-Isolierung
([The snap and the sandbox](docs/DESKTOP.md#the-snap-and-the-sandbox)). Jedes Release baut
auch für arm64: den Server, die Images und die Desktop-App unter Linux, Windows on Arm und
Apple Silicon ([docs/INSTALLATION.md → ARM (arm64)](docs/INSTALLATION.md#arm-arm64)). Alles,
was die App kann: [docs/FEATURES.md → Desktop app, phone and CLI](docs/FEATURES.md#desktop-app-phone-and-cli).

Dieselbe Binärdatei ist auch ein Client für Server, Skripte und Headless-Rechner:

```bash
filex client login --url https://files.example.com
filex client upload build/report.pdf docs://ci-artifacts/
filex client mv docs://ci-artifacts/report.pdf archive://2026/   # across storages, waits for the job
filex client run convert convert docs://data/table.csv --param target=xlsx

filex sync add ~/Documents/work docs://work   # the engine the desktop app uses
filex sync run --watch 30s
```

Siehe [docs/CLI.md](docs/CLI.md) und [docs/SYNC.md](docs/SYNC.md).

</details>

<br>

## In Ihre Anwendung einbetten

**Ein echter Dateimanager in Ihrem eigenen Produkt.**

Dieselbe Oberfläche gibt es als Vue-3-Komponente, als React-Komponente und als
frameworkunabhängige Webkomponente `<filex-explorer>`, mit Ihrem eigenen filex-Server
dahinter. Ein beschränkter API-Schlüssel bindet sie an einen Ordner pro Mandant, und das
setzt das Backend durch, nicht das Widget. Die Webkomponente braucht zwei Zeilen:

```html
<script type="module" src="https://cdn.jsdelivr.net/npm/@brftech/filex/dist/filex.js"></script>
<filex-explorer api-base="http://localhost:5212" sidenav connections ui-profile="simple"></filex-explorer>
```

<p align="center"><img src="https://filex.sh/shots/sidenav/embed-webcomponent-1440.c8c25d893d24.png" alt="Eingebettete Webkomponente" width="860"></p>

<details>
<summary><b>Vue 3 und React</b> - die beiden anderen Wrapper und wo die Details stehen</summary>

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

Stylesheets, Bundler, die Attribute `sidenav` / `connections` / `ui-profile`, beschränkte
API-Schlüssel für mandantenfähige Host-Anwendungen, ein Sitzungs-Cookie von einem anderen
Ursprung (Origin) und das Drucken aus einer App: [docs/INTEGRATION.md](docs/INTEGRATION.md),
zusammengefasst in [docs/FEATURES.md → Embedding](docs/FEATURES.md#embedding). ⚠ Halten Sie
Pakete und Server auf derselben Version ([docs/API.md](docs/API.md)).

<table>
<tr>
<td width="50%" valign="top">

![Mit mir geteilt](https://filex.sh/shots/sidenav/view-shared-1440.475ec2d8b49f.png)

<sub>Mit mir geteilt - Ordner, für die Ihnen andere Zugriff erteilt haben, keine Anleitung zum Einbinden</sub>

</td>
<td width="50%" valign="top">

![Eingebettete Webkomponente](https://filex.sh/shots/sidenav/embed-webcomponent-1440.c8c25d893d24.png)

<sub>Eingebettet in die Seite eines anderen Produkts</sub>

</td>
</tr>
</table>

</details>

<br>

## Apps

**Neue Möglichkeiten mit Dateien, mit genau den Berechtigungen, die Sie genehmigt haben.**

Eine App ist ein WebAssembly-Modul, das in filex in einer Sandbox läuft, eine eigene
Oberfläche in einem Sandbox-Frame oder beides. Sie installieren sie über den Link eines
Stores oder über ihre GitHub-Adresse, lesen jede Berechtigung, die sie verlangt, und nichts
aktualisiert sich von selbst. Die Personen Ihres filex können auf der Seite **App-Store**
einen vertrauenswürdigen Store durchsuchen und Sie um eine App oder ein Speicher-Plug-in
bitten. Fünf gibt es als öffentliche Repositorys:

| App | Was sie hinzufügt |
|---|---|
| [E-Signatur](https://github.com/BRF-Tech/filex-sign) | Ein PDF unterschreiben oder andere darum bitten: Personen auf diesem filex unterschreiben darin, alle anderen über einen privaten Link, standardmäßig hinter einer PIN |
| [Konvertieren](https://github.com/BRF-Tech/filex-convert) | **Konvertieren…** für jede Datei oder Auswahl: Bilder, Video, Audio, Dokumente, E-Books, Archive, Daten, Untertitel und Schriftarten |
| [Office editor](https://github.com/BRF-Tech/filex-office-editor) | Word-, Excel- und PowerPoint-Dokumente im Editor von ONLYOFFICE, im Browser und ohne Document Server; seine für filex 0.56 gebauten Versionen 0.2.0 und 0.3.0 bringen verschlüsselte Ordner und mehrere Personen gleichzeitig dazu |
| [filextext](https://github.com/BRF-Tech/filextext-app) | Ein Ende-zu-Ende-verschlüsselter Text-Arbeitsbereich in einer einzigen `.fxtxt`-Datei |
| [draw.io](https://github.com/BRF-Tech/filex-drawio) | Der Diagrammeditor draw.io, für `.drawio`- und `.dio`-Dateien |

Eine App, die Sie zulassen, kann eine Datei **gemeinsam** mit anderen bearbeiten - das Relay
von filex bringt ihre Änderungen in eine Reihenfolge, ohne sie zu lesen, und wer nur lesen
darf, sieht live zu - und eine Datei eines **verschlüsselten Ordners** öffnen, die Ihr Browser
für sie entschlüsselt und vor dem Speichern wieder verschlüsselt.

Auch ein Sprachpaket ist eine App: Spanisch, Deutsch und Französisch gibt es als Beispiele.

In wenigen Minuten eine installieren: [docs/APP-PLUGINS.md → Install one](docs/APP-PLUGINS.md#install-one). Alle Funktionen der Apps im Detail: [docs/FEATURES.md → Apps](docs/FEATURES.md#apps).

<p align="center"><img src="https://filex.sh/shots/signing/sign-place-1440.696a10b65be8.png" alt="Die Felder auf dem Dokument platzieren" width="860"></p>

<details>
<summary><b>Screenshots der Apps</b> - eine Unterschriftsanfrage von Anfang bis Ende, eine App installieren, der Konverter, eine App mit eigener Oberfläche, gemeinsames Bearbeiten, die Datei eines verschlüsselten Ordners in einer App, ein Sprachpaket, Standard-Apps, Installation aus einem Store</summary>

Dana bittet einen Kollegen auf demselben filex und einen Partner außerhalb davon, eine
Vereinbarung zu unterschreiben. Die App ist
[E-Signatur](https://github.com/BRF-Tech/filex-sign); jeden Bildschirm zeichnet filex, und
der Link, den der Partner bekommt, ist eine gewöhnliche Freigabe.

<table>
<tr>
<td width="50%" valign="top">

![Die Felder einer Unterschriftsanfrage festlegen](https://filex.sh/shots/signing/sign-define-1440.63ea72b13724.png)

<sub>Legen Sie die Felder fest - benennen Sie jedes und geben Sie an, wem es gehört; das Dokument kommt als Nächstes</sub>

</td>
<td width="50%" valign="top">

![Die Felder auf dem Dokument platzieren](https://filex.sh/shots/signing/sign-place-1440.696a10b65be8.png)

<sub>Platzieren Sie sie - wählen Sie ein Feld, tippen Sie dort auf die Seite, wo es hingehört</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Die PIN-Abfrage des externen Unterzeichners](https://filex.sh/shots/signing/sign-outside-pin-1440.2cd8ccb99483.png)

<sub>Der Link des Partners - der eine öffentliche Bildschirm von filex, im Namen Ihrer Instanz, hinter einer PIN</sub>

</td>
<td width="50%" valign="top">

![Der externe Unterzeichner füllt seine Felder aus](https://filex.sh/shots/signing/sign-outside-fill-1440.9f50aeb7abef.png)

<sub>…und was sich dahinter öffnet: nur die eigenen Felder - hier ein Name, getippt in der Schrift, die die anfordernde Person gewählt hat (gezeichnet und hochgeladen sind die beiden anderen)</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Das gesperrte Dokument mit geöffnetem Bereich „Unterschriften“](https://filex.sh/shots/signing/sign-status-1440.9f93226381ed.png)

<sub>Solange es im Umlauf ist - das Dokument ist für alle eingefroren, in seinen Details steht, wer unterschrieben hat</sub>

</td>
<td width="50%" valign="top">

![Die Berechtigungsprüfung des Installationsassistenten](https://filex.sh/shots/apps/apps-install-review-1440.cdb1a4ebf8f6.png)

<sub>Eine App installieren - jede Berechtigung, die sie verlangt, in klaren Worten, bevor irgendetwas läuft</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Die Detailansicht einer installierten App](https://filex.sh/shots/apps/apps-detail-1440.4e27b40c5ce7.png)

<sub>Eine installierte App - woher sie stammt, ihr Fingerabdruck und jede Berechtigung, die sie hat, in klaren Worten (ihre Einstellungen und ihre Aktionen folgen weiter unten auf der Seite)</sub>

</td>
<td width="50%" valign="top">

![Der Assistent des Konverters](https://filex.sh/shots/apps/convert-wizard-1440.231ada006fd6.png)

<sub>Der Konverter, eine weitere App - jedes Ziel unter seiner Kategorie, drei Schritte</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Die Prüfung bei der Installation einer App mit eigener Oberfläche](https://filex.sh/shots/apps/app-interface-review-1440.51df0aa460ad.png)

<sub>Eine App, die ihre eigene Oberfläche mitbringt - die Prüfung zeigt den Fingerabdruck des Pakets, jede Adresse außerhalb davon (eine Live-Adresse ist eine Berechtigung, in Gelb) und was ein Browser nicht versprechen kann</sub>

</td>
<td width="50%" valign="top">

![Die eigene Oberfläche einer App, als Vorschau einer Datei geöffnet](https://filex.sh/shots/apps/app-interface-viewer-1440.519b23618156.png)

<sub>…und diese Oberfläche, mit ihrem eigenen Dateityp geöffnet, dort, wo sonst die Vorschau von filex wäre. Sie liest und speichert die Datei über filex, in einem Sandbox-Frame (eine kleine Beispiel-App, für diese Bilder geschrieben)</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Zwei Personen in einem Dokument: eine bearbeitet, die andere sieht zu](https://filex.sh/shots/officeapp/coedit-watcher-1280.904b31e20f36.png)

<sub>Gemeinsam bearbeiten - Dana tippt, und Sam, der nur lesen darf, sieht jede Änderung und ihren Cursor über das Relay von filex ankommen ([Gemeinsam bearbeiten](docs/APP-PLUGINS-API.md#editing-together-coedit-056); eine Beispiel-App, für diese Bilder geschrieben)</sub>

</td>
<td width="50%" valign="top">

![Eine Datei eines verschlüsselten Ordners, in einer App geöffnet, vom Browser entschlüsselt](https://filex.sh/shots/e2eapp/app-encrypted-folder-1280.856d3586a9e2.png)

<sub>Eine Datei eines Ende-zu-Ende-verschlüsselten Ordners in einer App, die Sie zugelassen haben - Ihr Browser entschlüsselt sie für die App und verschlüsselt, was gespeichert wird, und der Server hält weiterhin nur Chiffretext (dieselbe Beispiel-App; [In einer App bearbeiten](docs/E2E-ENCRYPTION.md#editing-in-an-app))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Die Liste „Apps“, ein Sprachpaket unter den Apps](https://filex.sh/shots/langpack/apps-list-1440.004f0c819981.png)

<sub>Jede App auf der Instanz, darunter ein **Sprachpaket** - ein Manifest, in dem nichts läuft: Es sagt, wie viel von diesem filex es übersetzt, und mit ihm geht seine Sprache wieder</sub>

</td>
<td width="50%" valign="top">

![Plug-ins → Standard-Apps](https://filex.sh/shots/defaultapps/default-apps-1440.27d3c64fe457.png)

<sub>Standard-Apps - jeder Dateityp, den außer filex noch etwas verarbeitet: wer ihn öffnet und wer sein Vorschaubild erzeugt, in der Reihenfolge, die Sie festlegen ([Standard-Apps](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Ordner, gezeichnet mit ihren neuesten Dateien](https://filex.sh/shots/thumbnails/folders-grid-1440.2b408fb54f7e.png)

<sub>Ordnervorschauen - jeder Ordner, gezeichnet mit den drei Dateien, die zuletzt hineingekommen sind; die SVGs zeichnet die integrierte Engine von filex ([docs/thumbnails.md](docs/thumbnails.md#folder-previews))</sub>

</td>
<td width="50%" valign="top">

![Die Installationsprüfung, geöffnet über den Link eines Stores](https://filex.sh/shots/store/store-review-1440.d93b2b571d93.png)

<sub>Installation aus einem Store - dieselbe Prüfung, markiert mit **Aus dem Store …**; der Lizenzschlüssel der kostenpflichtigen App ist nur an seinen ersten Zeichen zu sehen ([Installation aus einem Store](docs/APP-PLUGINS.md#installing-from-a-store))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Der erste Link von einem Store: vertrauen?](https://filex.sh/shots/store/store-trust-1440.94bebecac9fe.png)

<sub>Der erste Link von einem Store - seine Adresse und die Fingerabdrücke seiner Schlüssel, zum Vergleichen, bevor Sie ihm vertrauen ([Vertrauenswürdige Stores](docs/APP-PLUGINS.md#trusted-stores))</sub>

</td>
<td width="50%" valign="top">

![Eine angehaltene kostenpflichtige App, das Hinweisband auf einer Seite des Adminbereichs](https://filex.sh/shots/store/store-license-held-1440.2dd9518d7e87.png)

<sub>Eine Lizenz, die der Store widerrufen hat - die App angehalten, *Nicht lizenziert*, nichts entfernt, und ein Hinweisband auf jeder Seite des Adminbereichs ([Kostenpflichtige Apps](docs/APP-PLUGINS.md#paid-apps))</sub>

</td>
</tr>
</table>

</details>

<br>

## KI-Agenten / MCP

**Ein Agent arbeitet in dem Ordner, den Sie ihm gegeben haben, und nirgendwo sonst.**

- Ein nativer **MCP-Server** unter `/api/ai/mcp` und eine REST-Schnittstelle unter
  `/api/ai`, beschrieben in einer OpenAPI-3.1-Datei.
- Ein API-Schlüssel trägt Berechtigungen pro Verb und kann auf einen einzigen Ordner
  beschränkt werden, und nichts, was er ausgibt, kann weiter reichen als der Schlüssel selbst.
- Eine große Datei geht über ein **Upload-Ticket**: eine kurzlebige, nur einmal verwendbare
  URL, sodass ihre Bytes nie durch den Kontext des Modells wandern.

```bash
claude mcp add filex --transport http https://files.example.com/api/ai/mcp \
  --header "Authorization: Bearer <api-token>"
```

Was ein Agent tun kann, was ein Schlüssel nie ausgeben kann, verschlüsselte Ordner und
Upload-Tickets: [docs/FEATURES.md → AI agents and MCP](docs/FEATURES.md#ai-agents-and-mcp);
die Referenz ist [docs/MCP.md](docs/MCP.md).

<br>

# Umstieg von Nextcloud, Dropbox oder Google Drive

filex ist ein Dateimanager, keine Groupware-Suite. Was das neben vier Dingen bedeutet, von
denen Sie vielleicht kommen (File Browser ist eines davon):

| Sie kommen von | Mit filex |
|---|---|
| **Nextcloud**<br>Eine Plattform für die Zusammenarbeit, die Sie selbst betreiben oder von einem Anbieter beziehen: Dateien und rundherum Kalender, Kontakte, E-Mail, Chat, Videoanrufe und ein Online-Office. Eine PHP-Anwendung hinter einem Webserver, mit einer Datenbank. | Nur der Dateiteil.<br>Eine einzige Go-Binärdatei mit SQLite darin (PostgreSQL oder MySQL, wenn Sie wollen).<br>Eine Desktop-App mit Live-Ordner-Sync, Freigabelinks, SSO und LDAP.<br>Office-Dokumente, allein oder gemeinsam, in der Office-Editor-App oder in dem ONLYOFFICE, das Sie anbinden.<br>Kein Kalender, keine Kontakte, keine E-Mail, kein Chat, keine Videoanrufe. |
| **Dropbox**, **Google Drive**<br>Gehostete Dienste: Ihre Dateien liegen auf den Servern des Anbieters, unter seinem Kontingent und seinen Bedingungen. | Ihr eigener Server und der Speicher, den Sie schon haben: eine Festplatte, eine NAS-Freigabe, ein S3-Bucket.<br>Das Alltägliche ist da: eine Weboberfläche, Freigabelinks mit PIN und Ablaufdatum, Dateianforderungen, Papierkorb und Versionsverlauf.<br>Eine Desktop-App, die die Ordner, die Sie wählen, auf demselben Stand hält. |
| **File Browser**<br>Eine einzige Binärdatei, die eine Weboberfläche über ein Verzeichnis legt, auf das Sie sie richten, mit Benutzerkonten (jeweils mit eigenem Bereich und eigenen Berechtigungsschaltern), Erlaubnis- und Sperrregeln pro Pfad und Freigabelinks mit Passwort und Ablaufdatum. Sein Repository ist archiviert, und seine [README](https://github.com/filebrowser/filebrowser) sagt, dass es keine weiteren Releases, Fehlerbehebungen oder Sicherheitskorrekturen geben wird. | Derselbe Start mit einem einzigen Befehl.<br>Mehrere Speicher nebeneinander.<br>Rollen und Gruppen über dem Zugriff pro Ordner; OIDC-Single-Sign-on und LDAP eingebaut.<br>Papierkorb und Versionen, Volltextsuche.<br>Derselbe Baum, erreichbar als S3, SFTP, FTPS, NFSv3 und WebDAV. |

**Was filex nicht hat**, egal, woher Sie kommen:

- **Eine Android- oder iOS-App.** Auf dem Smartphone ist filex die Web-App, die sich wie
  eine App installieren lässt
  ([Auf einem Smartphone oder Tablet](docs/DESKTOP.md#on-a-phone-or-a-tablet-the-web-app)),
  aber eine Verbindung braucht, um Dateien anzuzeigen; ist sie geschlossen, kommen
  Benachrichtigungen als Web Push, sobald Sie das für das Gerät einschalten.
- **Platzhalterdateien im Finder oder Explorer.** Die Desktop-App kopiert die Ordner, die
  Sie auf dem Computer behalten, und alles andere bleibt in ihrem eigenen Fenster online
  ([docs/DESKTOP.md](docs/DESKTOP.md#keeping-folders-on-this-computer)).
- **Einen in die Binärdatei eingebauten Office-Editor.** Office-Dokumente werden - allein
  oder gemeinsam - in der Office-Editor-App bearbeitet, die Sie installieren
  ([`filex-office-editor`](https://github.com/BRF-Tech/filex-office-editor), der Editor von
  ONLYOFFICE im Browser, ohne Document Server), oder in einem ONLYOFFICE Document Server, den
  Sie anbinden; ohne beides öffnen sie sich in einer schreibgeschützten Vorschau
  ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md)).

<details>
<summary><b>Der Umzug</b> - was mitkommt und was nicht</summary>

- Wo die Dateien schon an einem Ort liegen, den filex einbinden kann - ein Ordner auf einer
  Festplatte oder einer NAS-Freigabe, ein Präfix in einem S3-Bucket, ein SFTP-, FTP- oder
  WebDAV-Server -, ist der Umzug ein Einbinden, keine Migration. filex speichert Dateien
  nicht selbst: Richten Sie einen Speicher auf diesen Ordner, und die Dateien sind da, so
  wie sie sind.
- Dateien in Dropbox oder Google Drive müssen zuerst an einen solchen Ort kopiert werden:
  filex bringt für keinen der beiden einen Speichertreiber mit.
- Nur die Dateien ziehen um: Die Links, Berechtigungen und der Versionsverlauf, die das
  alte System hatte, werden nicht importiert.
- Was filex hinzufügt - Papierkorb, Versionsverlauf, Entwürfe -, liegt in versteckten
  Ordnern im Wurzelverzeichnis des Speichers, und ein Ordner, den Sie Ende-zu-Ende
  verschlüsseln, enthält von da an Geheimtext
  ([docs/STORAGE.md](docs/STORAGE.md), [docs/TRASH-VERSIONING.md](docs/TRASH-VERSIONING.md)).

</details>

<br>

# Selbst hosten mit Compose oder Helm

Das `docker run` oben genügt, um filex auszuprobieren. Für eine produktive Installation
liegen fertige Stacks in [`deploy/`](deploy/):

- **[`deploy/compose/`](deploy/compose/)** - Docker Compose:
  - **minimal** - filex + SQLite + lokaler Datenträger (ein Dienst, null Abhängigkeiten).
  - **full** - filex + PostgreSQL + Redis + Caddy (automatisches HTTPS) sowie zuschaltbare
    Add-ons: **ONLYOFFICE**, **Drawio** und ein **S3-Server** (Versity S3 Gateway). Schalten
    Sie jedes mit einem Compose-Profil in `.env` ein oder aus. Die Konvertierung übernimmt
    die [App „Konvertieren“](#apps), kein Sidecar.
- **[`deploy/helm/filex/`](deploy/helm/filex/)** - ein Helm-Chart für Kubernetes
  (Deployment + PVC + optionaler Ingress), mit jedem Release veröffentlicht:
  `helm repo add brf-tech https://helm.brf.sh`, dann `helm install filex brf-tech/filex`
  (oder `oci://ghcr.io/brf-tech/charts/filex`). Jedes der oben genannten Add-ons ist ein
  `enabled`-Schalter in `values.yaml` - installieren Sie PostgreSQL / Redis / einen
  S3-Server mit, oder binden Sie ein externes ONLYOFFICE / Drawio an.

Schritt-für-Schritt-Anleitungen für jede Ausbaustufe stehen in
[docs/INSTALLATION.md](docs/INSTALLATION.md).

filex läuft im Wurzelpfad eines eigenen Hosts oder unter einem Pfad eines gemeinsam
genutzten Hosts (`https://example.com/filex/`): eine Einstellung, `FILEX_BASE_PATH`, und
ein Proxy, der den vollständigen Pfad weitergibt - Beispiele für Caddy, nginx und Helm in
[docs/DEPLOYMENT.md → Serving filex under a sub-path](docs/DEPLOYMENT.md#serving-filex-under-a-sub-path).

<br>

# Dokumentation

Die Anleitungen sind als Website unter [docs.filex.sh](https://docs.filex.sh)
veröffentlicht, und das [vollständige Dokumentationsverzeichnis](docs/README.md) führt
jede Seite auf.

Beginnen Sie mit [Installation](docs/INSTALLATION.md), [Konfiguration](docs/CONFIGURATION.md),
der [Desktop-App](docs/DESKTOP.md), den [Protokollen](docs/PROTOCOLS.md), den
[Apps](docs/APP-PLUGINS.md) und [ONLYOFFICE](docs/ONLYOFFICE.md). [Funktionen](docs/FEATURES.md)
gibt jeder Funktion einen Eintrag mit ihren Grenzen und ihrer Anleitung.

<br>

# Funktionen

| Bereich | Was Sie bekommen |
|---|---|
| **Der Explorer** | Ein einziges Layout im Adminbereich, in der Desktop-App und in jeder Einbettung: ein Navigationsbereich, Listen-, Raster- und Galerieansicht, Tabs, persönliche Tags und Team-Tags, eine ⌘K-Befehlspalette, neu belegbare Tastenkürzel, neue Dokumente aus dem Menü **+ Neu** und Designs, die ein Betreiber zusammenstellt ([Integration](docs/INTEGRATION.md), [Designs](docs/INTEGRATION.md#themes)) |
| **Speicher** | Lokale Datenträger, S3, FTP, SFTP, WebDAV und SMB/NAS nebeneinander eingebunden, Kopieren und Verschieben zwischen ihnen und ein Plug-in für alles andere, aus dem Adminbereich oder aus einem App-Store ([Speicher](docs/STORAGE.md), [Speicher-Plug-ins](docs/PLUGINS.md)) |
| **Ohne Browser** | Derselbe Baum, bereitgestellt als S3, SFTP, FTPS, NFSv3 und WebDAV - ein Verschieben oder Löschen dort ist neben einem Speicher-Scan so sicher wie eines in der Weboberfläche -, dazu `filex mount` über HTTPS ([Protokolle](docs/PROTOCOLS.md)) |
| **Anmeldung** | OIDC, LDAP / Active Directory, Windows- und Linux-Konten und die Begrenzung der Anmeldeversuche ([SSO](docs/SSO.md), [LDAP](docs/LDAP.md), [Betriebssystemkonten](docs/OS-LOGIN.md)) |
| **Personen und Zugriff** | Rollen und Berechtigungen pro Person, Gruppen, Zugriff pro Ordner und native Mandantenfähigkeit ([Berechtigungen](docs/PERMISSIONS.md), [Gruppen](docs/GROUPS.md), [Mandantenfähigkeit](docs/MULTI-TENANCY.md)) |
| **Freigaben** | Öffentliche Links mit PIN, Ablaufdatum und Download-Limit, Dateianforderungen und Einladungen zur Freigabe per E-Mail ([Freigaben](docs/SHARING.md)) |
| **Schutz** | Papierkorb und Versionsverlauf, optionaler ClamAV-Scan, Ende-zu-Ende-verschlüsselte Ordner, Replikation auf einen zweiten Speicher (mit einer ersten Kopie dessen, was schon da ist) und ein Audit-Protokoll ([Papierkorb & Versionierung](docs/TRASH-VERSIONING.md), [Schutz](docs/PROTECTION.md), [E2E-Verschlüsselung](docs/E2E-ENCRYPTION.md), [Replikation](docs/REPLICATION.md), [Audit-Protokoll](docs/BACKEND.md#admin-audit-log)) |
| **Vorschauen und Editoren** | Bilder, Video, Audio, PDF, Markdown, Code (Monaco), Office-Dokumente in der Office-Editor-App oder über ONLYOFFICE, draw.io-Diagramme, Mermaid-Diagramme, gezeichnet und als Text bearbeitet, 3D-Modelle und Archive in ZIP, 7z und TAR ([ONLYOFFICE](docs/ONLYOFFICE.md), [Archive](docs/ARCHIVES.md)) |
| **Suche und Vorschaubilder** | Eingebettete Volltextsuche, die Berechtigungen beachtet, und Vorschaubilder für Bilder, Video, PDF, Ordner und, mit angebundenem ONLYOFFICE, Office-Dokumente ([Suche](docs/SEARCH.md), [Vorschaubilder](docs/thumbnails.md)) |
| **Echtzeit** | Anwesenheit und Live-Aktualisierungen von Dateien über WebSocket, und gemeinsames Bearbeiten einer Datei in einer App, über das Relay von filex ([Echtzeit](docs/REALTIME.md), [Gemeinsam bearbeiten](docs/APP-PLUGINS-API.md#editing-together-coedit-056)) |
| **Clients** | Eine Desktop-App mit Ordner-Sync, eine Web-App, die sich auf dem Smartphone unter dem Namen und dem Logo Ihrer Instanz installieren lässt, eine CLI, Einbettungen als Vue-, React- und Webkomponente, eine REST-API und ein MCP-Server ([Desktop](docs/DESKTOP.md), [Sync](docs/SYNC.md), [CLI](docs/CLI.md), [Integration](docs/INTEGRATION.md), [MCP](docs/MCP.md)) |
| **Apps und Sprachen** | Apps in einer Sandbox (E-Signatur, Konvertieren, Office editor, filextext, draw.io), die über filex drucken, eine Datei über sein Relay gemeinsam bearbeiten und die Datei eines verschlüsselten Ordners nur öffnen, wenn Sie es zulassen, in Ihrem Browser entschlüsselt; Sprachpakete und ein Layout von rechts nach links ([Apps](docs/APP-PLUGINS.md), [In einer App bearbeiten](docs/E2E-ENCRYPTION.md#editing-in-an-app), [RTL](docs/RTL.md)) |
| **Betrieb** | Eine Binärdatei oder ein Container; SQLite, PostgreSQL oder MySQL; ein Adminbereich mit einer einzigen Suche für jede Seite, jede Einstellung und jeden Datensatz; Webhooks, eine Glocke in der Anwendung und Web Push auf Smartphones und Browser, mit optionaler Zusammenfassung; eine Ansicht für Nutzung und Kosten (heute Backblaze B2); Update-Prüfungen, mit Patch-Releases, die sich selbst installieren, sobald Sie es erlauben ([Datenbanken](docs/DATABASES.md), [Adminbereich](docs/ADMIN-PANEL.md), [Benachrichtigungen](docs/NOTIFICATIONS.md), [Nutzung](docs/USAGE.md), [Updates](docs/UPDATES.md)) |

Jede Funktion als eigener Eintrag - wie sie sich verhält, ihre Grenzen und die Seite, die
sie beschreibt: [docs/FEATURES.md](docs/FEATURES.md).

<br>

# Architektur

Siehe [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

<details>
<summary><b>Die Bausteine auf einen Blick</b> - Treiber, Protokolle und Pakete in einem Diagramm</summary>

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

# Wie filex entsteht

filex hat einen einzigen Maintainer, der es mit KI-Coding-Agenten (Claude Code) baut. Der
Maintainer entscheidet, was filex ist und wie es funktioniert: die Architektur, das Daten-
und das Sicherheitsmodell und wie sich jede Funktion verhält. Die Agenten schreiben den
größten Teil des Codes innerhalb dieser Vorgaben, in Sprachen, mit denen der Maintainer
arbeitet. Etwa 82 % der Commits im Entwicklungsrepository tragen eine Zeile
`Co-Authored-By: Claude`; verborgen wird das also nicht.

Es ist KI-gestützt, aber kein ungeprüfter generierter Code:

- Jede Änderung durchläuft die Testkette, bevor sie aufgenommen wird: Go-Tests (auch unter
  dem Race-Detector), etwa 500 Vitest-Dateien, etwa 120 Playwright-Spezifikationen unter
  Chromium, Firefox und WebKit, Cypress sowie Datenbanktests mit SQLite, PostgreSQL und
  MySQL. Jedes Release wird erst getaggt, nachdem die vollständige Testmatrix von GitHub und
  ein Probelauf des Release-Workflows auf genau diesem Commit bestanden haben.
- Änderungen an Anmeldung, Berechtigungen, Freigaben und Verschlüsselung durchlaufen
  zusätzliche Prüfrunden, und ein roter Test wird repariert, nicht gelockert.
- Sicherheitsmeldungen werden offen behandelt: siehe [SECURITY.md](SECURITY.md) und die
  veröffentlichten Sicherheitshinweise. Eine unabhängige Prüfung hat es noch nicht gegeben.
- Die deutsche, spanische, französische und chinesische README sowie die deutschen,
  spanischen und französischen Sprachpakete sind maschinelle Übersetzungen, die noch kein
  Muttersprachler geprüft hat.

# Mitwirken

[![CI](https://img.shields.io/github/actions/workflow/status/BRF-Tech/filex/ci.yml?branch=main&label=ci)](https://github.com/BRF-Tech/filex/actions)

Issues und Pull-Requests sind willkommen; öffnen Sie vor einem umfangreicheren
Pull-Request ein Issue, das sagt, was Sie vorhaben. [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md)
beschreibt den Arbeitsablauf, die Tests, die eine Änderung bestehen muss, und die Regeln für
die Dokumentation, und das Projekt folgt dem [Contributor Covenant](CODE_OF_CONDUCT.md). Ein
Sicherheitsproblem wird vertraulich gemeldet, wie [SECURITY.md](SECURITY.md) es beschreibt.
Was sich in jedem Release geändert hat, steht in [CHANGELOG.md](CHANGELOG.md). Jeden Tag um
10:00 Uhr Istanbuler Zeit wird aus dem, was `main` dann enthält, ein Release erstellt, sofern
es etwas Neues enthält; ein Pull-Request, der nach diesem Schnitt gemergt wird, erscheint mit
dem Release des nächsten Tages ([Release-Prozess](docs/CONTRIBUTING.md#release-process)).

<details>
<summary><b>Entwicklung</b> - aus dem Quellcode bauen, und was wo liegt</summary>

```bash
git clone https://github.com/BRF-Tech/filex.git
cd filex
pnpm install
pnpm run build:all    # builds packages, web, then Go binary
./bin/filex serve
```

Unterverzeichnisse:
- `backend/` - Go-HTTP-Dienst (cmd/filex, internal/*, db/queries, db/migrations)
- `packages/core` - `@brftech/filex-core` (Vue-3-SFC, maßgebliche Quelle)
- `packages/webcomponent` - `@brftech/filex` (Webkomponenten-Wrapper)
- `packages/react` - `@brftech/filex-react` (React-Adapter über @lit/react)
- `web/` - Vue-3-Oberfläche des Adminbereichs (per `go:embed` in die Go-Binärdatei eingebettet)
- `desktop/` - Electron-App (gebündelter Hauptprozess, Synchronisierung im Infobereich,
  automatische Updates)
- `demo/` - eigenständige HTML-Demos für jedes Framework
- `e2e/` - Playwright-Suiten (Web, Einbettungen, paketierte Desktop-App) + `shots/`, die
  Skripte, die `pnpm shots` ausführt, um die oben gezeigten Screenshots aufzunehmen
  (veröffentlicht auf filex.sh, nicht im Repository abgelegt)
- `docker/` - Dockerfiles + compose
- `deploy/` - fertige Compose-Stacks + Helm-Chart (siehe [`deploy/`](deploy/))
- `docs/` - Markdown-Dokumentation
- `docs-site/` - VitePress-Website, veröffentlicht unter [docs.filex.sh](https://docs.filex.sh)

</details>

<br>

# Lizenz

MIT - siehe [LICENSE](LICENSE).

Die Store-Badges in [`docs/badges/`](docs/badges/) sind die eigenen Grafiken der Stores,
werden unverändert verwendet und fallen nicht unter diese Lizenz: Microsoft und das
Microsoft-Store-Badge sind Marken der Microsoft-Unternehmensgruppe; das Snap-Store-Badge
ist © Canonical Ltd., lizenziert unter
[CC BY-ND 2.0 UK](https://creativecommons.org/licenses/by-nd/2.0/uk/).

Nextcloud, Dropbox, Google Drive, File Browser und ONLYOFFICE sind Namen von Produkten
anderer Anbieter und werden hier nur verwendet, um diese zu beschreiben. filex ist mit
keinem davon verbunden und wird von keinem davon gesponsert oder empfohlen.
