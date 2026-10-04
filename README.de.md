<!-- Translated from README.md as of bf857aad (v0.51.0). The English README is the source: change it first, then carry the change here. -->

<div align="center">

<img src="docs/logo.png" alt="filex-Logo" width="96">

# filex - selbst gehosteter Dateimanager, der sich überall einbetten lässt

[![Release](https://img.shields.io/github/v/release/BRF-Tech/filex?color=2f6ceb)](https://github.com/BRF-Tech/filex/releases)
[![CI](https://img.shields.io/github/actions/workflow/status/BRF-Tech/filex/ci.yml?branch=main&label=ci)](https://github.com/BRF-Tech/filex/actions)
[![License: MIT](https://img.shields.io/github/license/BRF-Tech/filex?color=22c55e)](LICENSE)
[![Container](https://img.shields.io/badge/ghcr.io-brf--tech%2Ffilex-2496ed?logo=docker&logoColor=white)](https://github.com/BRF-Tech/filex/pkgs/container/filex)
[![Live demo](https://img.shields.io/badge/live_demo-demo.filex.sh-f59e0b)](https://demo.filex.sh)

[English](README.md) · [Türkçe](README.tr.md) · **Deutsch** · [Español](README.es.md) · [Français](README.fr.md) · [简体中文](README.zh-CN.md)

<sub>Dies ist eine Übersetzung der [englischen README](README.md) auf dem Stand von v0.51.0; wo die beiden voneinander abweichen, gilt der englische Text. Sie wurde maschinell übersetzt, eine Durchsicht durch Muttersprachler steht noch aus - Korrekturen sind willkommen. Die Dokumente, auf die sie verweist, sind auf Englisch.</sub>

Eine einzige Go-Binärdatei mit vollständiger Weboberfläche, austauschbaren Treibern für
Speicher, Authentifizierung und Datenbank, **Zusammenarbeit in Echtzeit**, **einer
einbettbaren Webkomponente**, einer **Desktop-App, deren Ordner-Sync live läuft** - eine
Änderung auf der einen Seite ist in etwa einer Sekunde auf der anderen - einem
**integrierten MCP-Server**, über den KI-Agenten filex direkt steuern können, und
**Apps**: Plug-ins, die filex neue Dinge beibringen, die sich mit Dateien tun lassen -
ein WebAssembly-Modul in einer Sandbox, eine eigene Oberfläche in einem Sandbox-Frame
oder beides - angefangen beim **Unterschreiben von Dokumenten** mit Personen innerhalb
und außerhalb Ihrer Organisation. Auch ein **Sprachpaket** ist eine App, sodass sich
filex übersetzen lässt, ohne auf ein Release zu warten - und für Sprachen, die so gelesen
werden, ordnet sich die Oberfläche **von rechts nach links** an. Man meldet sich mit dem
Konto an, das man schon hat - SSO, LDAP oder das **Windows- oder Linux-Konto** des
Rechners, auf dem filex läuft - und in einer mandantenfähigen Installation **verwaltet
sich jeder Mandant selbst**: eigene Anmeldeanbieter, eigene Domain und eigenes Zertifikat.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/v0.51.0/explorer-grid-dark.png">
  <img src="docs/screenshots/v0.51.0/explorer-grid-light.png" alt="filex-Explorer - Raster mit Vorschaubildern" width="900">
</picture>

</div>

## Jetzt ausprobieren

**Live-Demo:** [demo.filex.sh](https://demo.filex.sh) - melden Sie sich mit
`demo@demo.com` / `demo` an (Admin-Rolle, Sandbox wird jede Nacht zurückgesetzt).
Oder starten Sie Ihre eigene Instanz:

```bash
docker run -p 5212:5212 \
  -e FILEX_DEFAULT_STORAGE_DRIVER=local -e FILEX_DEFAULT_STORAGE_PATH=/srv/files \
  -v filex-data:/data -v "$PWD:/srv/files" \
  ghcr.io/brf-tech/filex:latest
```

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

Öffnen Sie http://localhost:5212/admin - der erste Start gibt Admin-Anmeldedaten und eine
Anleitung zum Einbetten auf der Konsole aus. Diese URL gehört dem Betreiber; die Personen,
denen Sie ein Konto geben, bekommen **http://localhost:5212/drive**, denselben
Dateimanager ohne den Adminbereich darum herum.

Lieber ein Fenster als ein Browser-Tab? Die **Desktop-App** (Windows / Linux / macOS)
meldet sich bei jedem beliebigen filex-Server an und synchronisiert Ordner im
Hintergrund - und für jede Plattform gibt es eine Variante, die **ohne Installation**
läuft (eine portable `.exe`, ein AppImage, ein `.zip`).
Laden Sie sie aus dem [Microsoft Store](https://apps.microsoft.com/detail/9PKXDJLVZWXW),
dem [Snap Store](https://snapcraft.io/filex-app) oder dem
[neuesten Release](https://github.com/BRF-Tech/filex/releases/latest) herunter -
[docs/DESKTOP.md](docs/DESKTOP.md).

## Warum filex

Die meisten selbst gehosteten Dateimanager sind entweder **zu klein** (eine
Verzeichnisliste mit Uploads) oder **zu groß** (eine Groupware-Suite, die man wegen des
Datei-Tabs installiert). filex zielt auf die Lücke dazwischen:

- **Ein Browser-Client für Ihre Benutzer, nicht nur für Sie** - Geben Sie jemandem ein
  `user`- oder `viewer`-Konto und `…/drive`, und er bekommt den Dateimanager selbst: seine
  Speicher, Uploads, Freigaben, die Suche, den Editor. Kein Adminbereich, durch den man
  hindurchmuss, kein separates Frontend, das man bereitstellen muss. `…/admin` ist der
  Zugang des Betreibers zu derselben Anwendung.
- **Eine Navigation, die man schon kennt** - links ein Navigationsbereich mit einem
  hervorgehobenen Menü **+ Neu** und **Start · Meine Dateien · Mit mir geteilt ·
  Meine Freigaben · Zuletzt verwendet · Favoriten · Entwürfe · Papierkorb**, dazu die
  Speicher, die Sie erreichen können; ein Speicher, den jemand mit Ihnen geteilt hat,
  erscheint dort einfach, ein Klick, keine Anleitung zum Einbinden. Jeder kann den
  Navigationsbereich von der oberen Leiste aus zur Icon-Leiste einklappen. **Start** ist
  eine Ansicht *in* der Anwendung, keine Seite daneben - Ihre Speicher, was Sie zuletzt
  geöffnet haben, und Ihre Favoriten, mit derselben Seitenleiste und derselben Kopfzeile
  wie bei den Dateien. Das ist die Shell, die alle bekommen: ein einziges Suchfeld quer über
  die Kopfzeile mit dem ⌘K-Hinweis auf die Befehlspalette, eine Filterzeile mit
  Typ / Eigentümer / Geändert / Größe, „Ordner“ und „Dateien“ als beschriftete Abschnitte,
  „Details“ und „Aktivität“ im Detailbereich und eine Speicherplatzanzeige. Für alle, die
  eine Dateiablage statt eines Dateimanagers wollen, schaltet `uiProfile: 'simple'` den
  Rest der Bedienelemente per Voreinstellung aus - ein Bereich, ein Ordner, Liste oder
  Raster. In jedem Fall ein und derselbe Explorer: Es gibt keine zweite Oberfläche, die
  Schritt halten müsste.
- **Lässt sich überall einbetten** - Dieselbe Oberfläche gibt es als Vue-3-Komponente, als
  React-Komponente und als frameworkunabhängige Webkomponente `<filex-explorer>`. Bauen Sie
  einen echten Dateimanager in *Ihr* Produkt ein, mit Ihrem eigenen filex-Server dahinter
  und auf einen Ordner pro Mandant beschränkt. Der Navigationsbereich kommt mit -
  `<filex-explorer sidenav ui-profile="simple">` ist alles, was eine Host-Seite angeben
  muss, die nie JavaScript anfasst.
- **Für KI-Agenten gebaut** - eine REST-Schnittstelle (`/api/ai`), begrenzt durch die
  Berechtigungen eines API-Schlüssels, dazu ein nativer **MCP-Server** (`/api/ai/mcp`);
  `/api/ai` und `/api/files` sind in einer
  [OpenAPI-3.1-Datei](backend/internal/api/openapi.json) beschrieben, die ein Test gegen den
  Router prüft. Geben Sie einem Agenten einen auf einen Ordner beschränkten API-Schlüssel,
  und er arbeitet dort mit den eigenen Vorgängen des Explorers - auflisten, lesen,
  schreiben, kopieren, konvertieren, teilen, Papierkorb, Versionen, Archive - und nichts
  außerhalb davon.
- **Apps, die nur können, was Sie genehmigt haben** - Einen Vertrag mit einem Partner ohne
  Konto unterschreiben, ein Video konvertieren, alles, was ein Manifest beschreibt, kommt
  als **App** hinzu: ein WebAssembly-Modul, das in filex läuft, eine eigene Oberfläche, die
  filex in einem Sandbox-Frame ausliefert, oder beides - mit genau den Berechtigungen, die
  Sie bei der Installation gelesen und erteilt haben. Das Modul bekommt kein Dateisystem,
  kein Netzwerk, kein Programm auf Ihrem Server; die Oberfläche kann die Sitzung von filex
  nicht lesen und ist durch die filex-eigene Richtlinie vom Netzwerk abgeschnitten.
  Nichts aktualisiert sich von selbst: Eine neue Version wartet auf einen Administrator,
  und die vorherige ist einen Klick entfernt. Vier Apps gibt es als öffentliche
  Repositorys - **E-Signatur**, **Konvertieren**, **filextext** (ein
  Ende-zu-Ende-verschlüsselter Text-Arbeitsbereich) und **draw.io** - und Sie installieren
  eine über ihre GitHub-Adresse ([Apps](#apps)).
- **In Ihrer Sprache und in Ihrer Schreibrichtung** - Englisch und Türkisch sind in der
  Binärdatei enthalten, und alles andere ist ein **Sprachpaket**: eine App, in der nichts
  läuft und die wie jede andere aus einem Repository installiert wird; sie übersetzt den
  Explorer, den Adminbereich, die öffentlichen Seiten, die ein Fremder öffnet, *und den
  Text, den der filex-Server schreibt* - E-Mails, Benachrichtigungen, die Seiten ohne
  JavaScript hinter einem Link. Ein Paket gibt an, wie viel von dieser Version es abdeckt,
  und was ihm fehlt, wird auf Englisch angezeigt; die Pluralformen folgen CLDR, sodass eine
  Sprache die Formen bekommt, die sie tatsächlich hat. Für Arabisch, Hebräisch, Persisch
  und Urdu richtet sich die Oberfläche **von rechts nach links** aus - und hört dort auf,
  wo Spiegeln falsch wäre, im Koordinatenraum des Dokuments und in Maschinentext
  ([docs/RTL.md](docs/RTL.md), [ein Paket schreiben](docs/PLUGIN-KIT.md#writing-a-language-pack)).
- **Es trägt Ihre Marke, nicht unsere** - Stellen Sie auf der Seite **Darstellung** ein
  Design in Ihren eigenen Farben zusammen und machen Sie es zum Standard: Die Anmeldeseite
  und jeder öffentliche Link tragen es ebenfalls, und eine Unterschriftsanfrage von Ihrer
  Instanz trägt Ihren Namen, nicht den von filex.
- **Echtzeit** - Anwesenheits-Avatare (ein Profilbild, einmal im Konto festgelegt und für
  jeden Client angezeigt, der mit Ihrem Konto angemeldet ist) und Live-Aktualisierungen von
  Dateien über WebSocket, in der nativen Oberfläche *und* in Einbettungen (Authentifizierung
  per kurzlebigem Ticket, API-Polling als Ausweichlösung). Ein Stapelauftrag wird auf dem
  Weg nach draußen gebündelt, sodass das Entpacken eines Archivs mit fünftausend Dateien
  einen geöffneten Explorer ein begrenztes Rinnsal von Frames kostet statt fünftausend
  ([docs/REALTIME.md](docs/REALTIME.md)).
- **Auch auf Ihrem Desktop** - Denselben Explorer gibt es als Windows-/Linux-/macOS-App, die
  lokale Ordner vom Infobereich aus mit dem Server auf demselben Stand hält - **live**, in
  etwa einer Sekunde, in beide Richtungen -, sich selbst aktualisiert und mehrere Konten
  (oder Mandanten) nebeneinander führt. Rechtsklick auf einen Ordner → **Auf diesem Computer
  behalten**, und er wird unter einem einzigen filex-Ordner gespiegelt; alles andere bleibt
  im Fenster nur online. Headless-Rechner bekommen dieselbe Engine als `filex sync` /
  `filex client`.
- **Spricht die Protokolle in beide Richtungen** - filex kann sich *verbinden mit* lokalen
  Datenträgern, S3, FTP, SFTP, WebDAV und SMB/NAS-Freigaben und ist *erreichbar als* **S3**,
  **SFTP**, **FTPS**, **NFSv3** und **WebDAV**. Lassen Sie `rclone`, `restic`, `aws s3`,
  WinSCP, FileZilla, einen Scanner, der nur FTP gelernt hat, oder einen Mediaplayer, der nur
  NFS gelernt hat, auf filex zeigen, und sie landen im selben Baum, mit denselben
  Berechtigungen, demselben Papierkorb und demselben Kontingent wie die Weboberfläche.
  Außerhalb des LAN gibt es zudem **`filex mount`**, das einen entfernten Server über
  gewöhnliches HTTPS einbindet - als Ordner unter Linux, als Laufwerksbuchstabe unter Windows
  ([docs/PROTOCOLS.md](docs/PROTOCOLS.md)).
- **Rollen und Berechtigungen pro Benutzer** - 29 benannte Berechtigungen (jede Dateiaktion,
  jede Art von Freigabe, jedes Protokoll, API-Schlüssel, die Desktop-App, fünf Teile des
  Adminbereichs), und jeder hat genau eine Rolle: Administrator, Benutzer, Betrachter oder
  eine benutzerdefinierte Rolle, die in manchen Ordnern abweichen („kein Löschen, außer in
  Scratch“) und Einschränkungen enthalten kann (Gültigkeitsdauer und Passwort von Links,
  gesperrte Dateitypen, maximale Dateigröße, erforderliche Zwei-Faktor-Authentifizierung).
  Ausnahmen pro Person gehen der Rolle vor, ein delegierter Administrator kann Benutzer
  verwalten, ohne je mehr zu besitzen, als er vergibt, und an jedem Zugang gilt dieselbe
  Antwort - Web-App, Agenten-API, WebDAV, SFTP, FTPS, S3, NFS und API-Schlüssel. Eine
  installierte App kann eigene Berechtigungen hinzufügen („Unterschriften anfordern“), die
  auf dieselbe Weise vergeben werden, und ein öffentlicher Link bleibt nur offen, solange
  die Person, die ihn erstellt hat, das noch darf
  ([docs/PERMISSIONS.md](docs/PERMISSIONS.md)).
- **Gruppen** - benannte Mengen von Personen, pro Mandant: Teilen Sie einen Ordner mit
  einer Gruppe wie mit einer Person, und geben Sie einer Gruppe eine Rolle, die alle darin
  erhalten, sofern sie keine eigene haben (eine Rollenpriorität entscheidet zwischen
  Gruppen). Personen werden von Hand hinzugefügt oder treten über die Gruppen bei, die ihre
  Anmeldung mitbringt - ein OIDC-Claim, LDAP-`memberOf`, die Gruppen des Betriebssystems
  oder ein Proxy-Header -, und scheiden aus, wenn der Identitätsanbieter es meldet
  ([docs/GROUPS.md](docs/GROUPS.md)).
- **Anmeldung mit dem Konto, das man schon hat** - ein lokales Passwort, OIDC, LDAP /
  Active Directory, ein authentifizierender Proxy oder das **Windows- oder Linux-Konto** des
  Rechners, auf dem filex läuft: Das Betriebssystem prüft das Passwort, filex speichert es
  nie, und der Anmeldeanbieter wird erst eingeschaltet, nachdem sich ein echtes Konto damit
  angemeldet hat. Jeder Anmeldeanbieter folgt einer einzigen Regel dafür, wer bei der
  ersten Anmeldung ein Konto bekommt ([docs/OS-LOGIN.md](docs/OS-LOGIN.md)).
- **Passwörter zu erraten dauert lange** - Falsche Passwörter werden pro Konto und pro
  Adresse gezählt, im Webformular ebenso wie bei WebDAV, FTPS und SFTP; eine Sperre
  verdoppelt sich bis auf 15 Minuten, eine Liste erlaubter IP-Adressen ist der Weg zurück
  hinein, und eine weitergeleitete Client-Adresse wird nur einem vertrauenswürdigen Proxy
  geglaubt - standardmäßig dieser Rechner und die Container neben filex, dazu alles
  Weitere, was Sie benennen
  ([Begrenzung der Anmeldeversuche](docs/CONFIGURATION.md#sign-in-attempt-limits)). Eine
  Änderung, die eine andere Website mit der Sitzung eines Besuchers sendet, wird abgelehnt
  ([Anfragen von anderen Ursprüngen](docs/CONFIGURATION.md#requests-from-other-origins)).
- **Von Grund auf mandantenfähig** - Speicher pro Mandant mit nativem Mandantenmodus,
  RBAC-Rollen + Berechtigungen pro Element, beschränkte API-Schlüssel, Identitäten pro
  Schlüssel für Audit-Protokolle sowie die Schlüsselarten „App“ und „Benutzer“, sodass
  gemeinsame Zugangsdaten einer Einbettung niemandes Schlüssel verwalten können. Ein
  Schlüssel nennt die Berechtigungen, die er hat - eine leere Liste wird abgelehnt und
  nicht als „alles“ gelesen - und **keine Zugangsdaten, die er ausstellt, reichen je
  weiter als er selbst**: Ein API-Schlüssel, ein S3-Zugriffsschlüssel, ein NFS-Export oder
  ein SSH-Schlüssel, der über einen eng gefassten Schlüssel erstellt wurde, kann weder
  dessen Verben überschreiten noch dessen Ordner verlassen noch länger gelten als er
  (`403 token_ceiling`). Die Mandantengrenze wird auf jeder Route durchgesetzt, die einen
  Datensatz benennt, nicht nur auf denen, die Datensätze auflisten, und instanzweite
  Einstellungen sind dem eigenen Mandanten der Plattform (Supertenant) vorbehalten. Jeder
  Mandant hat einen **Realm**, seinen Anmeldenamen: Das `alex` zweier Mandanten sind zwei
  Personen, ob sie sich nun unter der eigenen Adresse des Mandanten anmelden, den Realm
  auf der Seite der Plattform eingeben oder über SFTP `realm/alex` schreiben
  ([Realms](docs/MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)). Und ein Mandant
  verwaltet sich selbst: Sein Administrator fügt das eigene OIDC oder LDAP des Mandanten
  hinzu, der Betreiber bindet gemeinsame Anmeldeanbieter an einen oder mehrere Mandanten,
  und die eigene Domain eines Mandanten wird per CNAME nachgewiesen und mit einem
  Zertifikat von Ihrem Proxy, von filex selbst (ACME) oder mit seinem eigenen bedient
  ([docs/TENANT-ADMIN.md](docs/TENANT-ADMIN.md)).
- **Langweilig unkompliziert bereitzustellen** - eine einzige Binärdatei oder ein einziger
  Container, auf einem eigenen Host oder unter einem Unterpfad eines gemeinsam genutzten
  Hosts; SQLite als Standard, Postgres/MySQL, wenn Sie wollen; jeder Treiber wird über
  Umgebungsvariablen umgeschaltet. Die CI migriert bei jeder Änderung alle drei Engines,
  vergleicht sie miteinander und schreibt in jede, weil „unterstützt“ früher „kompiliert“
  bedeutete ([docs/DATABASES.md](docs/DATABASES.md)).

```
┌─────────────────────────────────────────────────────────────┐
│  filex (Go binary; 43 MB slim / 511 MB w/ thumbnails)       │
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

## Screenshots

### Apps - die erste: ein Dokument unterschreiben

Dana bittet einen Kollegen auf demselben filex und einen Partner außerhalb davon, eine
Vereinbarung zu unterschreiben. Die App ist
[E-Signatur](https://github.com/BRF-Tech/filex-sign); jeden Bildschirm zeichnet filex, und
der Link, den der Partner bekommt, ist eine gewöhnliche Freigabe.

| Legen Sie die Felder fest - benennen Sie jedes und geben Sie an, wem es gehört; das Dokument kommt als Nächstes | Platzieren Sie sie - wählen Sie ein Feld, tippen Sie dort auf die Seite, wo es hingehört |
|---|---|
| ![Die Felder einer Unterschriftsanfrage festlegen](docs/screenshots/v0.51.0/signing/sign-define-1440.png) | ![Die Felder auf dem Dokument platzieren](docs/screenshots/v0.51.0/signing/sign-place-1440.png) |

| Der Link des Partners - der eine öffentliche Bildschirm von filex, im Namen Ihrer Instanz, hinter einer PIN | …und was sich dahinter öffnet: nur die eigenen Felder - hier ein Name, getippt in der Schrift, die die anfordernde Person gewählt hat (gezeichnet und hochgeladen sind die beiden anderen) |
|---|---|
| ![Die PIN-Abfrage des externen Unterzeichners](docs/screenshots/v0.51.0/signing/sign-outside-pin-1440.png) | ![Der externe Unterzeichner füllt seine Felder aus](docs/screenshots/v0.51.0/signing/sign-outside-fill-1440.png) |

| Solange es im Umlauf ist - das Dokument ist für alle eingefroren, in seinen Details steht, wer unterschrieben hat | Eine App installieren - jede Berechtigung, die sie verlangt, in klaren Worten, bevor irgendetwas läuft |
|---|---|
| ![Das gesperrte Dokument mit geöffnetem Bereich „Unterschriften“](docs/screenshots/v0.51.0/signing/sign-status-1440.png) | ![Die Berechtigungsprüfung des Installationsassistenten](docs/screenshots/v0.51.0/apps/apps-install-review-1440.png) |

| Eine installierte App - woher sie stammt, ihr Fingerabdruck und jede Berechtigung, die sie hat, in klaren Worten (ihre Einstellungen und ihre Aktionen folgen weiter unten auf der Seite) | Der Konverter, eine weitere App - jedes Ziel unter seiner Kategorie, drei Schritte |
|---|---|
| ![Die Detailansicht einer installierten App](docs/screenshots/v0.51.0/apps/apps-detail-1440.png) | ![Der Assistent des Konverters](docs/screenshots/v0.51.0/apps/convert-wizard-1440.png) |

| Eine App, die ihre eigene Oberfläche mitbringt - die Überprüfung zeigt den Fingerabdruck des Pakets, jede Adresse außerhalb davon (eine Live-Adresse ist eine Berechtigung, in Gelb) und was ein Browser nicht versprechen kann | …und diese Oberfläche, mit ihrem eigenen Dateityp geöffnet, dort, wo sonst die Vorschau von filex wäre. Sie liest und speichert die Datei über filex, in einem Sandbox-Frame (eine kleine Beispiel-App, für diese Bilder geschrieben) |
|---|---|
| ![Die Prüfung bei der Installation einer App mit eigener Oberfläche](docs/screenshots/v0.51.0/apps/app-interface-review-1440.png) | ![Die eigene Oberfläche einer App, als Vorschau einer Datei geöffnet](docs/screenshots/v0.51.0/apps/app-interface-viewer-1440.png) |

| Jede App auf der Instanz, darunter ein **Sprachpaket** - ein Manifest, in dem nichts läuft: Es sagt, wie viel von diesem filex es übersetzt, und mit ihm geht seine Sprache wieder |
|---|
| ![Die Liste „Apps“, ein Sprachpaket unter den Apps](docs/screenshots/v0.51.0/langpack/apps-list-1440.png) |

### Ihre eigenen Dinge, wo immer Sie sind

| Die Glocke - darauf die Zahl der ungelesenen Benachrichtigungen, jede Zeile führt an den Ort, den sie nennt | Alle Ihre Benachrichtigungen, im Explorer - für alle, nicht nur für Administratoren |
|---|---|
| ![Die Glocke mit ihrem Badge für Ungelesenes, geöffnet](docs/screenshots/v0.51.0/signing/bell-badge-1440.png) | ![Die vollständige Liste der Benachrichtigungen über dem Explorer](docs/screenshots/v0.51.0/signing/notifications-list-1440.png) |

| Meine Freigaben - die Links, die Sie erstellt haben, und deren PINs, wenn Sie eine weitergeben müssen | Jede Tabelle im Adminbereich - ein angeheftetes Menü **Aktionen** pro Zeile, dasselbe Menü, das sich über das ⋮ des Explorers öffnet |
|---|---|
| ![Meine Freigaben, das Menü „Aktionen“ einer Zeile geöffnet](docs/screenshots/v0.51.0/signing/my-shares-1440.png) | ![Admin → Freigaben, das Menü „Aktionen“ einer Zeile geöffnet](docs/screenshots/v0.51.0/signing/admin-table-actions-1440.png) |

### Ihre Marke

| Darstellung - stellen Sie ein Design in Ihren eigenen Farben zusammen, mit Vorschau, während Sie tippen | Ist es als Standard festgelegt, trägt es der Explorer bei allen… |
|---|---|
| ![Der Design-Editor](docs/screenshots/v0.51.0/appearance/theme-editor-1440.png) | ![Der Explorer trägt das Design des Betreibers](docs/screenshots/v0.51.0/appearance/themed-explorer-1440.png) |

| …und die Anmeldeseite, bevor sich irgendjemand angemeldet hat | Ein Symlink, dem filex nicht folgt, sagt das - in der Dateiliste und in Worten in seinen Details |
|---|---|
| ![Die Anmeldeseite trägt das Design des Betreibers](docs/screenshots/v0.51.0/appearance/themed-signin-1440.png) | ![Ein Symlink, der den Speicher verlässt, gekennzeichnet](docs/screenshots/v0.51.0/symlinks/symlink-badge-1440.png) |

### Der Dateimanager

| Freigaben - PIN, Ablaufdatum, Download-Limit, `curl`-Einzeiler | Markdown-Vorschau |
|---|---|
| ![Der Dialog „Teilen“](docs/screenshots/v0.51.0/share-modal.png) | ![Markdown-Vorschau](docs/screenshots/v0.51.0/viewer-markdown.png) |

| …und was die Person am anderen Ende öffnet. filex hat EINEN Bildschirm nach außen - eine geteilte Datei, ein Ordner, eine Dateianforderung, die Unterschriftsseite einer App und die PIN vor jedem davon sind alle diese Seite, im Namen Ihrer Instanz |
|---|
| ![Ein öffentlicher Freigabelink, wie ihn der Empfänger sieht](docs/screenshots/v0.51.0/public-share.png) |

| Adminbereich | Startseite der Demo |
|---|---|
| ![Übersicht im Adminbereich](docs/screenshots/v0.51.0/admin-dashboard.png) | ![Startseite der Demo](docs/screenshots/v0.51.0/demo-landing.png) |

| Das Adminmenü - alle Seiten in drei Bereichen, **Dateien & Speicher**, **Personen & Sicherheit** und **System**, mit einer kurzen Zeile unter jeder Seite; ein Smartphone bekommt dieselben Seiten in einer ausfahrbaren Leiste ([docs/ADMIN-PANEL.md](docs/ADMIN-PANEL.md)) |
|---|
| ![Der Bereich „Personen & Sicherheit“ des Adminmenüs, geöffnet über der Seite Admin → Benutzer](docs/screenshots/v0.51.0/megamenu/people-panel-1440.png) |

| Rollen - Administrator, Benutzer, Betrachter und eigene Rollen: wer sie jeweils hat, was sie erlauben, wo sie je nach Ordner abweichen, ihre Einschränkungen ([docs/PERMISSIONS.md](docs/PERMISSIONS.md)) |
|---|
| ![Admin → Rollen: die integrierten Rollen und zwei benutzerdefinierte](docs/screenshots/v0.51.0/roles/roles-list-1440.png) |

| Gruppen - benannte Mengen von Personen mit Ordnerzugriff und einer Rolle; Mitglieder von Hand oder im Gleichschritt mit den Gruppen, die eine Anmeldung mitbringt ([docs/GROUPS.md](docs/GROUPS.md)) | Einen Ordner mit einer Gruppe teilen, neben Personen - „Eigentümer“ wird im Dialog erst nach Rückfrage erteilt, nicht per Klick |
|---|---|
| ![Admin → Gruppen](docs/screenshots/v0.51.0/groups/groups-list-1440.png) | ![Einen Ordner mit einer Gruppe teilen](docs/screenshots/v0.51.0/groups/share-group-1440.png) |

| Anmeldesicherheit - die Begrenzung der Anmeldeversuche, erlaubte Adressen, vertrauenswürdige Proxys, die Sperren und das Anmeldeprotokoll ([Begrenzung der Anmeldeversuche](docs/CONFIGURATION.md#sign-in-attempt-limits)) | …und was das Anmeldeformular eines gesperrten Kontos sagt, während es die Sperre auf seiner Schaltfläche herunterzählt |
|---|---|
| ![Admin → Anmeldesicherheit](docs/screenshots/v0.51.0/loginsecurity/login-security-1440.png) | ![Das Anmeldeformular bei einem gesperrten Konto](docs/screenshots/v0.51.0/loginsecurity/login-locked-1440.png) |

| Wer verschlüsseln darf - aus, nur Administratoren, alle, deren Rolle es erlaubt, oder nach Genehmigung durch einen Administrator; die wartenden Anfragen, jeweils mit der Angabe, wer gefragt hat und warum ([wer verschlüsseln darf](docs/E2E-ENCRYPTION.md#who-may-encrypt)) | …und aus Sicht der Person: Der Dialog „Neuer Ordner“ bittet einen Administrator um einen einzelnen verschlüsselten Ordner, mit einer Begründung |
|---|---|
| ![Admin → Verschlüsselung: die Genehmigungsrichtlinie und drei wartende Anfragen](docs/screenshots/v0.51.0/encryption/admin-encryption-1440.png) | ![Einen verschlüsselten Ordner im Dialog „Neuer Ordner“ anfragen](docs/screenshots/v0.51.0/encryption/request-new-folder.png) |

| Standard-Apps - jeder Dateityp, den außer filex noch etwas verarbeitet: wer ihn öffnet und wer sein Vorschaubild erzeugt, in der Reihenfolge, die Sie festlegen ([Standard-Apps](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail)) | Ordnervorschauen - jeder Ordner, gezeichnet mit den drei Dateien, die zuletzt hineingekommen sind; die SVGs zeichnet die integrierte Engine von filex ([docs/thumbnails.md](docs/thumbnails.md#folder-previews)) |
|---|---|
| ![Plug-ins → Standard-Apps](docs/screenshots/v0.51.0/defaultapps/default-apps-1440.png) | ![Ordner, gezeichnet mit ihren neuesten Dateien](docs/screenshots/v0.51.0/thumbnails/folders-grid-1440.png) |

| Eine `.csv`-Datei öffnet sich in der Tabellenkalkulation von ONLYOFFICE, wenn eines angebunden ist - zuerst zum Ansehen und ohne Dialog für das Trennzeichen: Das eigene Trennzeichen der Datei wird mitgegeben ([CSV-Dateien](docs/ONLYOFFICE.md#csv-files)) | …und ihr Editor, der sagt, was beim Speichern als CSV erhalten bleibt; die Datei geht als dieselbe Art von CSV zurück |
|---|---|
| ![Eine CSV-Datei mit Semikolon als Trennzeichen, in der Tabellenkalkulation von ONLYOFFICE geöffnet](docs/screenshots/v0.51.0/csvoffice/csv-view-1440.png) | ![Die CSV-Datei im Editor von ONLYOFFICE, mit dem Hinweis, was beim Speichern erhalten bleibt](docs/screenshots/v0.51.0/csvoffice/csv-edit-1440.png) |

| Die Shell - wo alle landen | Diesen Ordner durchsuchen; `⌘K` / `Ctrl K` übergibt die Suchanfrage an die Befehlspalette |
|---|---|
| ![Die filex-Shell](docs/screenshots/v0.51.0/driveshell/driveshell-hero-1440.png) | ![Einen Ordner durchsuchen](docs/screenshots/v0.51.0/driveshell/driveshell-search-1440.png) |

| Navigationsbereich - Start, Mit mir geteilt, Meine Freigaben, Zuletzt verwendet, Favoriten, Papierkorb und die Speicher, die Sie erreichen können | Zur Icon-Leiste eingeklappt |
|---|---|
| ![Navigationsbereich](docs/screenshots/v0.51.0/sidenav/sidenav-expanded-1440.png) | ![Zu einer Leiste eingeklappt](docs/screenshots/v0.51.0/sidenav/sidenav-rail-1440.png) |

| Tags - Ihre eigenen oder die Ihres Teams; ein Tag öffnet alle Dateien, die es tragen, aus allen Ordnern, in denen sie liegen | Papierkorb - was gelöscht wurde, woher es kam und wie viel Zeit bleibt, bis es verschwindet |
|---|---|
| ![Persönliche Tags und Team-Tags](docs/screenshots/v0.51.0/tags/tags-kinds-1440.png) | ![Die Ansicht „Papierkorb“](docs/screenshots/v0.51.0/sidenav/view-trash-1440.png) |

| Mit mir geteilt - Ordner, für die Ihnen andere Zugriff erteilt haben, keine Anleitung zum Einbinden | Eingebettet in die Seite eines anderen Produkts |
|---|---|
| ![Mit mir geteilt](docs/screenshots/v0.51.0/sidenav/view-shared-1440.png) | ![Eingebettete Webkomponente](docs/screenshots/v0.51.0/sidenav/embed-webcomponent-1440.png) |

| So verbinden Sie sich - die Anleitungen, aus *Ihrer* Installation erzeugt | API-Schlüssel - erstellen Sie Ihre eigenen, im Explorer oder in einer Einbettung (Sitzung oder Schlüssel einer Person; eine Einbettung, die über einen Proxy mit einem gemeinsamen *App*-Schlüssel läuft, bekommt diesen Eintrag nicht) |
|---|---|
| ![So verbinden Sie sich](docs/screenshots/v0.51.0/sidenav/connect-1440.png) | ![API-Schlüssel](docs/screenshots/v0.51.0/sidenav/apikeys-minted-1440.png) |

| filex von allem aus erreichen - S3, SFTP, FTPS, NFS, WebDAV. Jeder Befehl wird aus *Ihrer* Installation erzeugt |
|---|
| ![Verbindungsanleitung](docs/screenshots/v0.51.0/connections-guide.png) |

| Ein Speicher, den filex nicht mitliefert - unter **Plug-ins → Speicher-Plug-ins** als Plug-in installiert, das sein eigenes Konfigurationsformular beschreibt |
|---|
| ![Plug-ins](docs/screenshots/v0.51.0/admin-plugins.png) |

## Schnellstart - Binärdatei

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

## Selbst hosten mit Compose oder Helm

Das `docker run` oben genügt, um filex auszuprobieren. Für eine produktive Installation
liegen fertige Stacks in [`deploy/`](deploy/):

- **[`deploy/compose/`](deploy/compose/)** - Docker Compose:
  - **minimal** - filex + SQLite + lokaler Datenträger (ein Dienst, null Abhängigkeiten).
  - **full** - filex + PostgreSQL + Redis + Caddy (automatisches HTTPS) sowie zuschaltbare
    Add-ons: **ONLYOFFICE**, **Drawio** und ein **S3-Server** (Versity S3 Gateway). Schalten
    Sie jedes mit einem Compose-Profil in `.env` ein oder aus. Die Konvertierung übernimmt
    die [App „Konvertieren“](#apps), kein Sidecar.
- **[`deploy/helm/filex/`](deploy/helm/filex/)** - ein Helm-Chart für Kubernetes
  (Deployment + PVC + optionaler Ingress). Jedes der oben genannten Add-ons ist ein
  `enabled`-Schalter in `values.yaml` - installieren Sie PostgreSQL / Redis / einen
  S3-Server mit, oder binden Sie ein externes ONLYOFFICE / Drawio an.

Schritt-für-Schritt-Anleitungen für jede Ausbaustufe stehen in
[docs/INSTALLATION.md](docs/INSTALLATION.md).

filex läuft im Wurzelpfad eines eigenen Hosts oder unter einem Pfad eines gemeinsam
genutzten Hosts (`https://example.com/filex/`): eine Einstellung, `FILEX_BASE_PATH`, und
ein Proxy, der den vollständigen Pfad weitergibt - Beispiele für Caddy, nginx und Helm in
[docs/DEPLOYMENT.md → filex unter einem Unterpfad bereitstellen](docs/DEPLOYMENT.md#serving-filex-under-a-sub-path).

## In Ihre Anwendung einbetten

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

Es muss **kein Stylesheet importiert werden** - das Aussehen reist im Bundle mit und wird
beim Mounten eingefügt, in diesem Schnipsel fehlt also nichts. ⚠ Bei einem Bundler
müssen die optionalen Pakete für die Vorschau (`monaco-editor` und Co.) als extern
markiert werden, was [docs/INTEGRATION.md](docs/INTEGRATION.md) in einer einzigen
`rollupOptions.external`-Zeile zeigt; jeder einzelne dieser Importe ist abgesichert,
sodass die Vorschauen weniger können, statt kaputtzugehen.

### Vanilla JS / beliebiges Framework
```html
<script type="module" src="https://cdn.jsdelivr.net/npm/@brftech/filex/dist/filex.js"></script>
<filex-explorer api-base="http://localhost:5212" sidenav connections ui-profile="simple"></filex-explorer>
```

`sidenav` schaltet den Navigationsbereich ein (er ist standardmäßig eingeschaltet; das
Attribut gibt es, damit eine Host-Seite beide Zustände angeben kann), `connections`
fügt dessen Einträge „So verbinden Sie sich“ und „API-Schlüssel“ hinzu, und
`ui-profile="simple"` schaltet die Bedienelemente für Power-User per Voreinstellung aus.
Alle drei sind gewöhnliche `config`-Schlüssel, also setzen die Vue- und React-Wrapper sie
auf dieselbe Weise - siehe
[docs/INTEGRATION.md](docs/INTEGRATION.md).

Mandantenfähige Host-Anwendungen reichen die API in der Regel serverseitig per Proxy
durch, fügen pro Anfrage einen **beschränkten API-Schlüssel** (`root: tenant-folder`) ein
und entfernen Client-Header - die Sandbox wird vom Backend durchgesetzt, nicht vom Widget.
Ein solcher Schlüssel hat `kind: "app"`, daher blendet der Navigationsbereich die Bereiche
aus, die einer einzelnen Person gehören - API-Schlüssel, Zuletzt verwendet, Favoriten,
Mit mir geteilt -, während „Hochladen“, die Speicher, der Papierkorb und
„So verbinden Sie sich“ bleiben. Siehe
[docs/INTEGRATION.md](docs/INTEGRATION.md) und
[docs/MCP.md](docs/MCP.md#token-kinds---user-vs-app).

⚠ Eine Einbettung, die sich auf das eigene filex-**Sitzungs-Cookie** des Besuchers stützt
(ohne API-Schlüssel) und auf einer Seite eines anderen Ursprungs (Origin) liegt - eine
Schwester-Subdomain eingeschlossen -, liest wie bisher, aber jede Änderung, die sie
sendet, wird abgelehnt (`403 cross_origin_refused`), bis dieser Ursprung in
`FILEX_CORS_ALLOWED_ORIGINS` steht; der Standardwert `*` gewährt das nicht. Ein
Bearer-Token, eine Host-Anwendung, die als Proxy mit einem Schlüssel arbeitet, die
Desktop-App und die installierte Web-App brauchen nichts davon
([docs/CONFIGURATION.md](docs/CONFIGURATION.md#requests-from-other-origins)).

## Desktop-App & CLI

Den Explorer gibt es auch als **Desktop-App für Windows / Linux / macOS** - dieselbe
Komponente, die von der Weboberfläche und den Einbettungen gerendert wird, keine eigene
halbe Kopie:

- **Mehrere Konten gleichzeitig** - eine Kontenleiste mit Servern/Mandanten, die jeweils
  ihr eigenes Branding zeigen.
- **Dateien herausziehen** - Ziehen Sie eine Auswahl auf den Desktop oder in eine andere
  Anwendung: Ordner und Mehrfachauswahlen kommen als einzelne echte Dateien und Ordner an.
  Alles, was bereits auf diesem Computer behalten wird, lässt sich sofort ziehen; der Rest
  wird einmal abgerufen und zwischengespeichert
  ([docs/DESKTOP.md](docs/DESKTOP.md#dragging-files-out)).
- **Auf diesem Computer behalten** - Klicken Sie mit der rechten Maustaste auf einen
  beliebigen Ordner, eine Datei oder einen ganzen Speicher, um das Element auf dem Rechner
  unter einem einzigen filex-Ordner (in den Einstellungen verschiebbar) zu spiegeln; alles
  andere bleibt nur online, und jede Zeile zeigt, was für sie gilt (✓ ◐ ⟳ ☁).
  „Nur online behalten“ verschiebt die lokale Kopie in den Papierkorb oder lässt sie, wo
  sie ist ([docs/DESKTOP.md](docs/DESKTOP.md#keeping-folders-on-this-computer)).
- **Ordner-Sync** - Koppeln Sie einen lokalen Ordner mit einem Serverordner, und solange
  die App im Infobereich läuft, bleiben sie in beiden Richtungen auf demselben Stand, und
  zwar **live**: Was im Browser gespeichert wird, ist in etwa einer Sekunde auf der
  Festplatte, und was lokal gespeichert wird, ebenso schnell auf dem Server (die Engine
  folgt dem Änderungsstrom des Servers und dem Dateisystem, mit einer vollständigen
  Prüfung alle 30 s als Sicherheitsnetz), beide Versionen bleiben erhalten, wenn sich
  beide Seiten gleichzeitig ändern, dazu paralleles Übertragen und Auflisten, ein erster
  Durchlauf, der dort weitermacht, wo er unterbrochen wurde, ein lokaler
  30-Tage-Papierkorb und eine Engine, die sich weigert, aus einem fehlenden Ordner eine
  Massenlöschung zu machen ([docs/SYNC.md](docs/SYNC.md)).
- **Öffnet Office-Dokumente von Ihrer eigenen Festplatte** - Doppelklicken Sie auf eine
  `.docx`/`.xlsx`/`.pptx`-Datei (oder einen beliebigen der zehn Office-Typen), und sie
  öffnet sich in dem Editor, den Ihr Server betreibt, auf einem Rechner ohne installiertes
  Office. Ein Dokument in einem Ordner, den Sie auf diesem Computer behalten, wird selbst
  geöffnet; alles andere wird auf den Server kopiert, bearbeitet und über das Original
  zurückgeschrieben
  ([docs/DESKTOP.md](docs/DESKTOP.md#opening-documents-from-your-computer)).
- **Mount as a drive** („Als Laufwerk einbinden“) - Eine Schaltfläche in den Einstellungen
  bindet den Server über WebDAV als Laufwerk des Betriebssystems ein, und eine weitere hebt
  die Einbindung auf; das eigene Token des Kontos dient als Zugangsdaten und erscheint nie
  auf einer Befehlszeile. Unter Windows erprobt; die Codepfade für macOS und Linux sind
  vorhanden, aber noch nicht verifiziert
  ([docs/DESKTOP.md](docs/DESKTOP.md#mounting-the-server-as-a-drive)).
- **⌘K durchsucht jedes Konto** in der Kontenleiste, gruppiert unter je einer Kennzeichnung
  pro Konto; gesucht, heruntergeladen und herausgezogen wird jeweils mit der eigenen
  Anmeldung des Kontos ([docs/SEARCH.md](docs/SEARCH.md)).
- **Ihre Benachrichtigungen und Ihr Konto im Fenster** - Die obere Leiste endet wie die der
  Web-App: mit der **Glocke** (Zahl der ungelesenen Benachrichtigungen, die neuesten
  Zeilen, *Alle als gelesen markieren*, die vollständige Liste) und dem **Avatar** mit
  *Benutzereinstellungen* - dem eigenen Einstellungsdialog der Web-App, **im Fenster**
  geöffnet - und *Adminbereich* für einen Administrator. Ein Klick auf eine
  Benachrichtigung führt im Fenster in den Ordner mit ausgewählter Datei. Das Abmelden
  bleibt unter *Settings → Accounts* („Konten“ in den Einstellungen) in der App selbst
  ([docs/DESKTOP.md](docs/DESKTOP.md#notifications-and-your-account)).
- **Meldet sich über Ihren Browser an**, sodass sich SSO und MFA genau wie im Web verhalten.
- **Aktualisiert sich selbst** - lädt im Hintergrund herunter, installiert beim Beenden;
  `FILEX_NO_UPDATE=1` schaltet das aus.
- **Läuft ohne Installation**, wenn Sie genau das brauchen: Die **portable** `.exe` für
  Windows, das AppImage für Linux und das `.zip` für macOS laufen alle von dort, wo Sie sie
  ablegen. Die portable Windows-Variante bewahrt alles, was sie hat, in einem einzigen
  `filex-data`-Ordner neben sich auf, sodass nach dem Löschen dieses Ordners nichts von
  Ihnen auf einem Rechner zurückbleibt, der nicht Ihnen gehört - der Preis dafür ist, dass
  sie sich nicht selbst aktualisiert.

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

Der Store-Build (*filex File Manager*) ist die einzige Windows-Variante, die signiert ist -
Microsoft signiert sie -, und der Store hält sie aktuell. winget (`BRFTech.filex-app`)
wird mit jedem Release eingereicht und wartet auf seine erste Prüfung durch die
winget-Moderatoren, sodass `winget install` das Paket noch nicht findet. Installer,
portable `.exe`, AppImage, `.deb`, `.rpm` und `.dmg` sind dem
[neuesten Release](https://github.com/BRF-Tech/filex/releases/latest) angehängt - noch
nicht signiert, rechnen Sie also beim Windows-Installer mit einer SmartScreen-Meldung.
Details: [docs/DESKTOP.md](docs/DESKTOP.md). Die CLI allein:
`brew install brf-tech/filex/filex` ([docs/CLI.md](docs/CLI.md); ihr winget-Paket,
`BRFTech.filex`, ist in derselben Prüfung).

Unter Linux läuft die App nie ohne die Chromium-Sandbox. `.deb` und `.rpm` brauchen
nichts; ab Ubuntu 23.10 braucht ein AppImage einmalig ein AppArmor-Profil, und das Snap
braucht `sudo snap connect filex-app:browser-sandbox`, bis der Snap Store die Verbindung
von selbst herstellt - in beiden Fällen sagt die App das und zeigt den Schritt an
([docs/DESKTOP.md](docs/DESKTOP.md#appimage-on-recent-ubuntu)).

**ARM (arm64)** - was dafür ausgeliefert wird (jedes Release baut all das und führt es vor
der Veröffentlichung auf arm64-Rechnern aus):

| | arm64 |
|---|---|
| Binärdatei für Server + CLI | Linux, macOS und Windows: `filex-<os>-arm64` und die Archive `.tar.gz` / `.zip` |
| Docker-Images (`ghcr.io/brf-tech/filex`, full und slim) | Multi-Arch - `docker pull` wählt arm64 von selbst |
| Desktop-App - Linux | `filex-desktop-arm64.AppImage`, `filex-desktop-arm64.deb`, `filex-desktop-aarch64.rpm` und der Snap Store (`sudo snap install filex-app` wählt arm64) - seit 0.48.1 |
| Desktop-App - Windows on Arm | `filex-desktop-arm64.exe` (Installer) und `filex-desktop-portable-arm64.exe` - seit 0.48.1; die App aktualisiert sich selbst auf den arm64-Build |
| Desktop-App - macOS | nur Apple Silicon (kein Intel-Build) |
| Homebrew | die CLI (`filex`) auf Apple Silicon und unter Linux auf Arm; die Desktop-App (`filex-app`) auf Apple Silicon |

Auf einem Arm-Rechner nennt das Angebot *filex-Desktop-App herunterladen* in der Anwendung
(und sein Gegenstück in den Einstellungen) die arm64-Datei zuerst, und die Download-Liste
auf [filex.sh](https://filex.sh/#downloads) hebt sie hervor, anhand dessen, was der
Browser meldet (die Client Hints von Chromium, das `aarch64` von Firefox). Einem Browser,
der dazu nichts sagt (Safari, Firefox unter Windows), wird die x64-Datei angeboten, mit
der arm64-Datei daneben.

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

## KI-Agenten / MCP

filex bringt unter `/api/ai` eine per API-Schlüssel authentifizierte
Automatisierungsschnittstelle mit (auflisten, lesen, schreiben, verschieben, kopieren,
löschen, suchen, teilen, zippen) und spricht unter `/api/ai/mcp` das
**Model Context Protocol**. Ein Agent führt auch die eigenen Vorgänge des Explorers aus -
Kopieren über Speicher hinweg, App-Aktionen wie **Konvertieren**, die Vorgangswarteschlange,
Papierkorb und Versionsverlauf, 7z/TAR-Archive, seine Links und Dateianforderungen, die
Glocke, Favoriten, Kommentare und die Berechtigungen für ein Element, das ihm gehört -,
und zwar über die eigenen Handler des Explorers, also gelten die Regeln des Explorers. Ein
Admin-Schlüssel erreicht, was der Adminbereich erreicht (Mandanten, Identitätsanbieter,
Anmeldesicherheit, Standard-Apps, Webhooks, Speicher), über dessen eigene Handler.
`/api/ai` und `/api/files` sind in einer
[OpenAPI-3.1-Datei](backend/internal/api/openapi.json) beschrieben:

```bash
claude mcp add filex --transport http https://files.example.com/api/ai/mcp \
  --header "Authorization: Bearer <api-token>"
```

Ein API-Schlüssel trägt Berechtigungen pro Verb (`read`, `write`, `delete`, dazu `mcp` und
`admin`), ist optional **auf einen einzigen Ordner beschränkt**, unterliegt denselben
RBAC-Berechtigungen und Rollen wie die Oberfläche und ist mit Identitäten pro Schlüssel
versehen, sodass Audit-Protokolle, Freigaben und Anwesenheit zeigen, *wer* (welche
Integration) was getan hat. Die Verben gelten an **jeder Schnittstelle, die der Schlüssel
erreicht** - `/api/ai`, die MCP-Tools, die eigenen Routen des Explorers (und damit
`filex client` und eine Einbettung), WebDAV, SFTP, FTPS sowie die aus ihm erstellten
S3-Zugriffsschlüssel und NFS-Exporte. Manches steht einem Schlüssel nie zu: ein Plug-in
installieren - ein Agent **hinterlässt eine Installationsanfrage**, die ein Administrator
im Adminbereich genehmigt - und jemanden zum Administrator machen. Ein Schlüssel muss
mindestens eine Berechtigung nennen - eine leere Liste wird abgelehnt, nie als „alle“
gelesen - und **was er ausgibt, kann nie weiter reichen als der Schlüssel selbst**:
Verlangt jemand über einen nur lesenden oder auf einen Ordner beschränkten Schlüssel einen
API-Schlüssel, einen S3-Zugriffsschlüssel, einen NFS-Export oder einen SSH-Schlüssel mit
mehr Verben, einem Stammordner außerhalb des eigenen oder einer längeren Gültigkeitsdauer,
wird das mit `403 token_ceiling` abgelehnt, samt der Angabe, was zu weit reichte.
Verschiebt ein Agent etwas, **wird nie überschrieben**: Ein Element, dessen Zielname schon
vergeben ist, landet daneben unter einem freien Namen (`report-copy.txt`), genau wie beim
Verschieben in der Oberfläche, und die Antwort nennt den Pfad, unter dem es tatsächlich
gelandet ist. Und er kennt **verschlüsselte Ordner**: Jede Zeile sagt, ob sie verschlüsselt
ist, verschlüsselte Daten werden nie so ausgegeben, als wären sie die Datei
(`409 E2E_ENCRYPTED`), und in einen verschlüsselten Ordner geschriebener Klartext wird
abgelehnt, es sei denn, der Aufrufer sagt ausdrücklich, dass er das will
(`allow_plaintext`) ([docs/MCP.md](docs/MCP.md#encrypted-folders-and-fxe)).

Eine große Datei, die schon auf der Festplatte des Agenten liegt, passt nie durch einen
Tool-Aufruf - ihre Bytes müssten durch den Kontext des Modells wandern. **Upload-Tickets**
lösen das: Ein autorisierter Aufruf legt das Ziel fest und gibt eine kurzlebige, nur einmal
verwendbare URL zurück, die **keine Zugangsdaten** braucht, sodass selbst ein Agent ohne
filex-Schlüssel die Übertragung mit `curl -T bigfile <url>` abschließen kann. Details:
[docs/MCP.md](docs/MCP.md).

## Apps

Ein **Speicher-Plug-in** bringt filex ein Backend bei, von dem es noch nie gehört hat. Eine
**App** bringt filex bei, *was sich mit Dateien tun lässt* - sie unterschreiben, sie
konvertieren, sie an jemanden außerhalb senden - und ist absichtlich eine andere Art von
Plug-in: ein **WebAssembly-Modul, das innerhalb von filex läuft, in einer Sandbox**, die
ihm nichts überlässt, was ihm nicht gewährt wurde. Kein Dateisystem, kein Netzwerk, keine
Umgebung, kein Programm auf Ihrem Server: nur die Host-Funktionen, die das Manifest der
App anfordert, jede dem Administrator in klaren Worten gezeigt, bevor irgendetwas
installiert wird, und nur die Dateien, die die ausführende Person tatsächlich ausgewählt
hat. Die schweren Engines, die eine App vielleicht braucht (ffmpeg, ImageMagick,
Ghostscript, poppler, rsvg), sind die des Servers und werden mit einer Berechtigung pro
Engine angeboten; Office-Dokumente laufen über den von Ihnen angebundenen ONLYOFFICE
Document Server als Office-Engine - filex führt kein LibreOffice aus.

Eine App kann auch **eine eigene Oberfläche** mitbringen - oder aus nichts anderem
bestehen: HTML, CSS und JavaScript, die ihr Autor geschrieben hat, ein Editor oder eine
Vorschau für ein Format. filex liefert sie aus dem von Ihnen genehmigten Paket (per SHA-256
festgelegt) in einem **Sandbox-Frame** aus: ein opaker Ursprung (Origin), der weder
Sitzung noch Cookies noch Seiten von filex lesen kann, eine Inhaltsrichtlinie, die filex
anhand der erteilten Berechtigungen der App schreibt (keine Verbindung, kein
Browserspeicher, keine Formulare, keine Pop-ups), und ein einziger geprüfter
Nachrichtenkanal, über den filex ihr nur die Dateien übergibt, mit denen sie geöffnet
wurde, und in diese zurückschreibt - als neue Version oder als Entwurf. Sie kann ihren
eigenen Dateityp zu **Neues Dokument** hinzufügen, sich im Editor-Tab öffnen und Ihnen
eine Datei zum Behalten übergeben - jedes Mal, wenn Sie es erlauben. ⚠ Browser können
nicht vollständig verhindern, dass eine Seite Daten nach außen sendet (WebRTC ignoriert
eine Inhaltsrichtlinie; in Chrome kann filex es schließen, in Firefox nur aus der Seite
nehmen - ein Sicherheitsgurt, keine Mauer), deshalb sagt die Prüfung bei der Installation
das offen: **Vertrauen Sie einer App mit eigener Oberfläche so weit, wie Sie ihrem Autor
die Dateien anvertrauen, die Sie darin öffnen.**

Was eine App hinzufügt, steht dort, wo alles andere steht: Zeilen im Dateimenü,
Bildschirme, die filex für sie zeichnet, oder ihre eigene Oberfläche, Aufträge in derselben
Warteschlange wie ein Kopiervorgang - mit Fortschritt, **Abbrechen** und einem Ergebnis,
das wie jeder andere Schreibvorgang versioniert, gescannt und indexiert wird -, ein
Abschnitt in den Details einer Datei, ein Startbildschirm unter **Apps** in der Navigation
und, wenn sie jemanden ohne Konto braucht, ein Link, der eine gewöhnliche **Freigabe** ist:
in derselben Liste, unter derselben Richtlinie für PIN-Sperre und Ablaufdatum, von Ihnen
widerrufbar wie jeder andere Link. Eine App, die das anfordert, wird außerdem einmal pro
Stunde geweckt, um ihre eigene geplante Arbeit zu erledigen - eine Unterschriftsanfrage,
die sich bei Ablauf ihrer Frist selbst schließt und die Erinnerungen sendet, die Sie
gewünscht haben.

Nicht jede App führt Code aus. Ein **Sprachpaket** ist ein Manifest mit Texten und sonst
nichts: Es wird allein aus dem Manifest installiert - kein Modul, kein Go, kein Release -,
startet nie eine Laufzeitumgebung und fügt seine Sprache dem Explorer, dem Adminbereich,
den öffentlichen Seiten und dem Text hinzu, den der Server schreibt. **Plug-ins → Apps**
führt es als *Sprachpaket* mit seiner Abdeckung der laufenden Version auf, und alles, was
ihm fehlt, wird auf Englisch angezeigt. Spanisch, Deutsch und Französisch gibt es als
Beispiele, und `BRF-Tech/filex-lang-template` führt einen Übersetzer vom Export bis zur
Installation.

Neben filex erscheinen vier Apps, als öffentliche Repositorys, die Sie installieren, lesen
und forken können. Die ersten beiden sind Module; die letzten beiden sind nur eine
Oberfläche, ohne dass etwas auf dem Server läuft:

| App | Was sie hinzufügt |
|---|---|
| **[E-Signatur](https://github.com/BRF-Tech/filex-sign)** - `BRF-Tech/filex-sign` | **Unterschreiben…**, **Unterschriften anfordern…**, **Unterschreiben / Ausfüllen** und **Prüfen** für ein PDF, und nur für ein PDF (ein Office-Dokument wird zuerst mit **Konvertieren** in eines umgewandelt). Eine Anfrage ist ein kurzer Assistent: wer unterschreibt - Personen auf diesem filex, die darin unterschreiben, und per Name oder E-Mail-Adresse jede andere Person, die einen **privaten Link** bekommt, hinter einer PIN, sofern Sie nichts anderes festlegen - in welcher Reihenfolge, die Felder, die benannt, jedem Unterzeichner zugewiesen und dann auf der Seite platziert werden; wie lange die Anfrage offen bleibt, ob die Datei währenddessen **eingefroren** ist und ob am Ende ein **Audit-Protokoll als PDF** geschrieben wird. Das Ergebnis ist ein PAdES-signiertes PDF, das **zertifiziert und versiegelt** ist: Die erste Unterschrift zertifiziert das Dokument, sodass spätere nur noch ausfüllen und unterschreiben dürfen, und sobald die letzte eingeht, **versiegelt filex selbst die gesamte Datei** mit dem eigenen Siegel der Installation, so gesperrt, dass jede Änderung danach als nicht zulässig gemeldet wird. Der **SHA-256-Hash genau dieser versiegelten Bytes**, der Fingerabdruck des Siegels und die Angabe, wie sich beides prüfen lässt, gehen an die anfordernde Person und an jeden Unterzeichner, intern wie extern, und in das Audit-Protokoll. Auf Wunsch bleibt die unterschriebene Datei **in filex gesperrt**, bis ein Administrator die Sperre aufhebt. **Signaturschlüssel verlassen den Server nie**: Die eigene Zertifizierungsstelle der Instanz (oder eine, die Sie importieren) stellt pro Unterzeichner ein Zertifikat aus, und der Schlüssel, mit dem eine Unterschrift erzeugt wurde, wird Sekunden später vernichtet - der Schlüssel des Siegels ist die einzige Ausnahme, vom Host verwahrt und nie herausgegeben. |
| **[Konvertieren](https://github.com/BRF-Tech/filex-convert)** - `BRF-Tech/filex-convert` | **Konvertieren…** für jede Datei: Bilder, Video, Audio, Dokumente, E-Books, Archive, Daten, Untertitel und Schriftarten. Das Ziel wird über Schaltflächen ausgewählt, die nach Kategorie gruppiert sind, dann folgen nur die Einstellungen, die dafür eine Rolle spielen, dann eine Überprüfung. Die meisten Konvertierungen laufen in reinem Go innerhalb der Sandbox; die übrigen nutzen die Engines des Servers, wenn sie installiert sind, und ein Ziel, das eine fehlende braucht, sagt das, statt stillschweigend wegzubleiben. |
| **[filextext](https://github.com/BRF-Tech/filextext-app)** - `BRF-Tech/filextext-app` | Ein **Ende-zu-Ende-verschlüsselter Text-Arbeitsbereich** in einer einzigen `.fxtxt`-Datei: Seiten und Ordner links, Tabs oben, der BlockSuite-Editor von AFFiNE in der Mitte (Überschriften, Listen, Aufgaben, Code, Tabellen, Bilder, Links zwischen Seiten, Markdown-Import und -Export). Er wird **in Ihrem Browser** verschlüsselt, mit denselben Schlüsseln und demselben Wiederherstellungsschlüssel wie die [verschlüsselten Ordner](docs/E2E-ENCRYPTION.md) von filex; filex speichert verschlüsselte Daten und bekommt weder das Passwort noch ein Wort des Textes je zu sehen. Eine `.fxtxt`-Datei öffnet sich darin anstelle der Vorschau, und **Neues Dokument** erhält den Eintrag *Encrypted workspace (.fxtxt)* (verschlüsselter Arbeitsbereich). |
| **[draw.io](https://github.com/BRF-Tech/filex-drawio)** - `BRF-Tech/filex-drawio` | Der Diagrammeditor **draw.io**, innerhalb von filex: `.drawio`- und `.dio`-Dateien öffnen sich darin anstelle der Vorschau, **Speichern** schreibt eine neue Version, und **Neues Dokument** erhält den Eintrag *draw.io diagram* (draw.io-Diagramm). Die eigenen Dateien von draw.io werden aus dem Paket der App ausgeliefert (per SHA-256 festgelegt); die App erreicht nichts außerhalb davon. |

**Eine App von GitHub installieren** - *Admin → Plug-ins → **Apps** → **App installieren** →
GitHub-Repository*: Geben Sie `BRF-Tech/filex-sign` und das Release-Tag ein. filex liest die
`filex-app.json` des Repositorys, lädt das darin genannte Modul (oder das Paket der
Oberfläche) herunter, lehnt es ab, wenn sein SHA-256-Hash nicht übereinstimmt, und hält dann
bei der **Berechtigungsprüfung** an. Nichts wird installiert, bevor Sie jede Berechtigung
gelesen und *Ich verstehe* angekreuzt haben; die erteilten Berechtigungen sind genau diese
Liste, und eine Aktualisierung, die mehr verlangt, hält erneut bei der Überprüfung an.
`FILEX_PLUGIN_TRUSTED_KEYS` macht signierte Module zur Pflicht (eine Installation von GitHub
bringt keine Signatur mit, laden Sie das Modul auf einer solchen Instanz also stattdessen mit
seiner Signatur hoch). Jeder Download - eine App, ihre Update-Prüfung, ein Speicher-Plug-in -
geht nur an öffentliche Adressen, geprüft nach der DNS-Auflösung und bei jeder
Weiterleitung: Um von einem Server in Ihrem eigenen Netzwerk zu installieren, laden Sie die
Dateien hoch. Im Demo-Modus sind Apps ausgeschaltet. Ein API-Schlüssel - ein Agent, ein
Skript, die CLI - kann keine installieren: Er **hinterlässt eine Anfrage**, filex friert die
Bytes und Berechtigungen ein, die damit installiert würden, und ein Administrator genehmigt
sie unter **Plug-ins → Installationsanfragen**.

**Wer sie nutzen darf.** Eine App kann **eigene Berechtigungen** deklarieren - eine App zum
Unterschreiben knüpft *Unterschriften anfordern* an eine solche, während Sie keine brauchen,
um zu unterschreiben, was Ihnen geschickt wurde - und Sie vergeben sie pro Rolle und pro
Person wie die von filex selbst; eine Aktion, zu der jemand nicht berechtigt ist, steht
nicht in seinem Menü und wird abgelehnt, wenn sie aufgerufen wird
([App-Berechtigungen](docs/APP-PLUGINS.md#app-permissions)).

**Nichts aktualisiert sich von selbst.** Einmal am Tag (und bei **Nach Updates suchen**)
fragt filex die Quelle jeder App - ihre GitHub-Releases, den Branch eines Sprachpakets oder
die Manifest-Adresse, von der sie installiert wurde - nach einer neueren Version, die dieses
filex ausführen kann, und sagt es Ihnen: Diese wartet unter *Update verfügbar* (oder
*Genehmigung nötig*, wenn sie mehr verlangt), bis ein Administrator ihre Änderungen -
Berechtigungen, Modul, Dateien der Oberfläche, Versionshinweise - überprüft und sie genehmigt
hat, und alle verwenden dann diese Version. **Zurück zu *Version*** stellt die von ihr
ersetzte wieder her. Speicher-Plug-ins können auf dieselbe Weise einer Quelle folgen. Eine
App gibt an, mit welchen filex-Versionen sie funktioniert (`"filex": ">=0.47.0"` in ihrem
Manifest), und filex installiert sie außerhalb dieses Bereichs nicht.

**Leitfaden für Betreiber:** [docs/APP-PLUGINS.md](docs/APP-PLUGINS.md) - Apps installieren
und steuern, die Unterschriftsrunde von Anfang bis Ende, der Konverter, geplantes Aufwecken
und was die öffentlichen Links einer App schützt. **Eine App schreiben** (Standard-Go,
`GOOS=wasip1`, mit einem Test-Kit): Beginnen Sie mit dem Vorlagen-Repository
[BRF-Tech/filex-app-template](https://github.com/BRF-Tech/filex-app-template) und
[docs/PLUGIN-KIT.md](docs/PLUGIN-KIT.md); der Schnittstellenvertrag:
[docs/APP-PLUGINS-API.md](docs/APP-PLUGINS-API.md). Die andere Art von Plug-in, ein
Speicher-Backend: [docs/PLUGINS.md](docs/PLUGINS.md).

## Funktionen

- **Mehrere Speicher** - Binden Sie viele Speicher gleichzeitig ein (lokal, S3, FTP, SFTP, WebDAV, SMB/NAS); jeder erscheint als Ordner der obersten Ebene. Jeder hat außerdem eine Adresse, die sich nie ändert: Der Name des Speichers ist das erste Pfadsegment bei WebDAV, SFTP, NFS und der S3-API, eine Umbenennung würde ihm also eine neue Adresse geben - eine Einbindung, die auf seine **uid** verweist, übersteht jede Umbenennung. **In einem kopieren oder ausschneiden und in einem anderen einfügen**: filex streamt den Baum zwischen den beiden Treibern, behält den Zeitstempel jeder Datei bei und entfernt das Original erst, wenn die Kopie geprüft ist. Ein Dienst, der nicht erreichbar ist, wird innerhalb von Sekunden gemeldet, und eine Zeitüberschreitung gibt es nur bei Stille, nie bei einer Übertragung, die weiterläuft (S3, WebDAV, FTP, SFTP und SMB: [docs/STORAGE.md](docs/STORAGE.md#when-the-store-is-down)). Ein Eintrag, zu dem der Speicher keine Auskunft geben konnte (weder „da“ noch „nicht gefunden“), bleibt erhalten, gekennzeichnet mit einem **!** und der eigenen Antwort des Speichers, und mit ihm geschieht nichts - im Explorer, in den REST- und Agenten-APIs, bei Freigaben und in den Editoren (die Dateiprotokolle lesen die Kennzeichnung nicht) -, bis der Speicher wieder antwortet ([PLUGINS.md](docs/PLUGINS.md#an-entry-your-stat-cannot-answer-for)).
- **Dateien auf den Desktop herausziehen** - Ziehen Sie in der Desktop-App eine Auswahl in den Explorer/Finder oder in ein anderes Programm, und sie landet dort als einzelne echte Dateien und Ordner, nicht als Archiv; im Browser lässt sich eine einzelne Datei auf dieselbe Weise herausziehen - auch im Adminbereich, über einen Link für eine einzige Datei, der eine Minute gilt und einmal funktioniert ([docs/DESKTOP.md](docs/DESKTOP.md#dragging-files-out)).
- **Speicher-Plug-ins** - Ein Speicher, von dem filex noch nie gehört hat, ist ein **separates Programm**, das Sie im Adminbereich installieren: Es beschreibt sein eigenes Konfigurationsformular, filex spricht mit ihm ein kleines HTTP/JSON-Protokoll, und sein Treiber verhält sich dann wie jeder integrierte. Die Sprache ist beliebig; ein Go-SDK macht daraus drei Methoden. filex **prüft jede Fähigkeit, die ein Plug-in angibt** - bei der Installation und erneut anhand der Konfiguration, die Sie eingeben, wenn Sie einen darauf aufbauenden Speicher speichern - und lehnt eines ab, das nicht kann, was es behauptet, denn ein halb funktionierender Treiber erzeugt Fehler, die aussehen, als sei filex kaputt. Aktualisierungen ersetzen die Binärdatei an Ort und Stelle und werden rückgängig gemacht, wenn die neue nicht startet; bei jedem Start werden Hash und Signatur der Binärdatei erneut geprüft, und jedes Plug-in führt ein **Protokoll** über seine Starts, seine Fehler und die Einträge, zu denen es keine Auskunft geben konnte (*Aktionen → Protokoll*) ([docs/PLUGINS.md](docs/PLUGINS.md)).
- **Apps** - eine zweite Art von Plug-in: ein **WebAssembly-Modul in einer Sandbox**, eine **eigene Oberfläche in einem Sandbox-Frame** (keine Verbindung, kein Zugriff auf die Sitzung von filex; was ein Browser nicht versprechen kann, steht unter [Apps](#apps)) oder beides - sie ergänzt filex um Aktionen im Dateimenü (*Unterschriften anfordern…*, *Konvertieren…*), Bildschirme, die filex für sie zeichnet, einen Abschnitt in den Details einer Datei, einen Startbildschirm unter **Apps** in der Navigation und Links, die externe Beteiligte ohne Konto öffnen. Installiert wird sie aus einem GitHub-Repository über eine **Berechtigungsprüfung** - die App bekommt genau das, was Sie genehmigt haben, und nichts sonst: kein Dateisystem, kein Netzwerk, kein Programm auf Ihrem Server; die schweren Engines (ffmpeg, ImageMagick, …) sind die des Servers, und Office-Dokumente laufen über das ONLYOFFICE, das Sie anbinden, jeweils als eigene Berechtigung angeboten. Die Bildschirme, die filex zeichnet, folgen, wer auch immer sie geschrieben hat, den Regeln von filex - jede Auswahlmöglichkeit sichtbar statt in einem Dropdown versteckt, nichts hinter „Erweitert“ eingeklappt, eine Frage pro Schritt. Der Link, den eine App an einen externen Unterzeichner schickt, ist eine gewöhnliche **Freigabe**, Sie sehen und widerrufen ihn also in derselben Liste wie alles andere, und er ist nie mehr wert als die Person, die ihn erstellt hat: Ein Auftrag, der über ihn gestartet wird, durchläuft dieselben Prüfungen wie einer, der in filex gestartet wird (eine Aktion, die Sie ausgeschaltet haben, bleibt ausgeschaltet, der Zugriff des Erstellers auf das Dokument wird erneut gelesen), und der Link funktioniert nicht mehr, wenn das Konto des Erstellers deaktiviert wird - bis es wieder aktiviert wird. Eine App kann auch **ihre eigene Oberfläche** mitbringen - HTML und JavaScript, die filex aus dem genehmigten Paket der App in einen Sandbox-Frame ausliefert, dessen Richtlinie keine Verbindung, keinen Browserspeicher und kein Cookie zulässt, und die über einen einzigen geprüften Kanal mit filex sprechen; ein Editor, der auf dem Server nichts braucht (draw.io, filextext), ist eine App ganz ohne Modul ([die eigene Oberfläche einer App](docs/APP-PLUGINS.md#an-apps-own-interface), SDK `@brftech/filex-app-ui`). Eine App, der Sie `schedule` erteilen, wird einmal pro Stunde geweckt, um zu der von ihr gewählten Minute ihre eigene Arbeit zu erledigen, als gewöhnlicher Auftrag in der Warteschlange. Nichts aktualisiert sich von selbst: filex prüft täglich die Quelle jeder App und meldet, wenn eine neuere Version da ist; ein Administrator überprüft, was sie ändert, und genehmigt sie; alle verwenden die genehmigte Version - und **Zurück zu *Version*** macht eine Genehmigung rückgängig. Apps geben an, mit welchen filex-Versionen sie funktionieren. Ein API-Schlüssel installiert nie eine - er hinterlässt eine **Installationsanfrage**, die ein Administrator genehmigt - und eine App kann **eigene Berechtigungen** deklarieren, die Sie pro Rolle und pro Person vergeben ([App-Berechtigungen](docs/APP-PLUGINS.md#app-permissions)). Eine App kann **Vorschaubilder erzeugen** für Dateitypen, für die filex keine erzeugt (sie bekommt die Bytes einer einzigen Datei und nichts sonst), und **Standard-Apps** legt pro Dateityp fest, welche App ihn öffnet, welche sein Vorschaubild erzeugt und in welcher Reihenfolge; jede Person wählt unter den Apps zum Öffnen, die eingeschaltet geblieben sind, wobei *Immer diese App verwenden* in ihrem Konto gespeichert wird - eine einzige Wahl für den Browser, die Desktop-App und eine Einbettung ([Standard-Apps](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail)). Vier gibt es als öffentliche Repositorys: **E-Signatur**, **Konvertieren**, **filextext** und **draw.io** ([Apps](#apps), [docs/APP-PLUGINS.md](docs/APP-PLUGINS.md#the-apps-that-ship-alongside-filex), [eine schreiben](docs/PLUGIN-KIT.md)).
- **E-Signatur** ([`BRF-Tech/filex-sign`](https://github.com/BRF-Tech/filex-sign), eine App) - Unterschreiben Sie ein PDF selbst oder bitten Sie andere darum: Personen auf diesem filex unterschreiben darin, über eine Benachrichtigung, die den richtigen Bildschirm öffnet; alle anderen bekommen einen privaten Link, standardmäßig hinter einer PIN - einer, die filex für Sie aufbewahrt (siehe *Freigaben*). Die Felder werden **zuerst festgelegt** - ein Name, wem das Feld gehört, erforderlich oder nicht, das Format eines Datums - und **danach auf der Seite platziert**, zwei Fragen auf zwei Bildschirmen. Das Dokument kann für alle **eingefroren** werden, Administratoren eingeschlossen, solange es im Umlauf ist; Erinnerungen und die Frist laufen von selbst; die anfordernde Person behält jeden Unterzeichner in den Details der Datei und auf dem Startbildschirm der App im Blick; und das Ergebnis ist ein PAdES-signiertes PDF, das durch seine erste Unterschrift **zertifiziert** und nach seiner letzten **von filex versiegelt** wird, sodass ein PDF-Reader jede spätere Änderung als nicht zulässig meldet - mit dem **SHA-256-Hash der versiegelten Bytes** und dem Fingerabdruck des Siegels, die an die anfordernde Person und jeden Unterzeichner gesendet werden, einem **Audit-Protokoll als PDF**, wenn Sie eines verlangen, einem Beleg für jeden Unterzeichner und einer Option, die fertige Datei gesperrt zu halten, bis ein Administrator die Sperre aufhebt. **Prüfen** berichtet über jedes unterschriebene PDF: jede Unterschrift, die Zertifizierung, das Siegel und ob dies die Datei ist, deren Hash versendet wurde. Signaturschlüssel verlassen den Server nie: Die eigene Zertifizierungsstelle der Instanz oder eine, die Sie importieren, stellt jedem Unterzeichner ein Zertifikat aus, und der Schlüssel, mit dem eine Unterschrift erzeugt wurde, wird Sekunden später vernichtet ([docs/APP-PLUGINS.md](docs/APP-PLUGINS.md#signing-documents-end-to-end)).
- **Konvertieren, als App** ([`BRF-Tech/filex-convert`](https://github.com/BRF-Tech/filex-convert)) - *Konvertieren…* für jede Datei oder Auswahl: Bilder, Video, Audio, Dokumente, E-Books, Archive, Daten, Untertitel und Schriftarten. Das Ziel ist eine Schaltfläche unter seiner Kategorie, dann nur die Einstellungen, die dafür eine Rolle spielen, dann eine Überprüfung; die meisten Wege laufen in reinem Go innerhalb der Sandbox, der Rest über die Engines des Servers, und ein Ziel, das eine fehlende Engine braucht, wird als solches aufgeführt, statt stillschweigend zu fehlen ([docs/APP-PLUGINS.md](docs/APP-PLUGINS.md#converting-files)). (Der ältere iframe-Konverter-Sidecar wurde in 0.48 entfernt.)
- **Protokoll-Gateway** - Derselbe Baum ist erreichbar als **S3** (SigV4; aws-cli, rclone, restic, mc, s3fs), **SFTP** (OpenSSH, WinSCP, FileZilla, sshfs), **FTPS** (explizites TLS, für die Geräte, die nur FTP gelernt haben; geben Sie ihm das automatisch erneuerte Zertifikat Ihres Reverse-Proxys - es wird bei einer Änderung neu eingelesen), **NFSv3** (NAS-Clients im LAN, Mediaplayer) und **WebDAV** - jeweils mit eigenen Zugangsdaten, die Sie einzeln widerrufen können, und alle mit denselben Berechtigungen, demselben Papierkorb und demselben Kontingent wie die Oberfläche ([docs/PROTOCOLS.md](docs/PROTOCOLS.md)).
- **`filex mount`** - Binden Sie einen entfernten filex-Server über gewöhnliches HTTPS in einen Ordner ein: unter Linux ein Ordner, **unter Windows ein Laufwerksbuchstabe** (`filex mount Z:`, benötigt das kostenlose [WinFsp](https://winfsp.dev)). Keine Synchronisierung: Kopiert wird nichts außer einem begrenzten Lesecache, daher öffnet die Einbindung eine von hunderttausend Dateien, ohne den Rest herunterzuladen.
- **Zusammenarbeit in Echtzeit** - Anwesenheitsleiste mit Live-Avataren + Fokus, sofortige Aktualisierungen bei Dateiänderungen über WebSocket, Polling als Ausweichlösung. Ein einzelner Schreibvorgang wird in dem Moment gemeldet, in dem er ankommt; ein Schwall (das Entpacken eines ZIP-Archivs, ein Ordner-Upload, ein NFS-Client, der Block für Block schreibt) wird zu einem Frame pro Zeitfenster zusammengefasst, damit der Ordner live bleibt, ohne die Seite zu überfluten ([docs/REALTIME.md](docs/REALTIME.md)).
- **Eine Dateiliste, die sich wie eine Tabelle verhält** - Ändern Sie die Breite einer Spalte, blenden Sie eine aus, ziehen Sie eine an eine neue Stelle; die Tabelle scrollt seitwärts, statt eine Spalte wegzulassen, wenn der Platz nicht reicht, und die Spalte „Aktionen“ bleibt rechts fixiert. Sortieren Sie nach Name, Typ, Datum oder Größe, auf- oder absteigend, und **das Raster und die Liste folgen derselben Sortierung** - bis zu diesem Release galt „nach Größe sortiert“ nur für eine Ansicht, und ein Wechsel der Ansicht ordnete die Zeilen vor Ihren Augen neu. Ist nach Datum sortiert, gruppieren alle drei Ansichten die Zeilen unter **Heute · Gestern · Diese Woche · Dieser Monat** und danach Monat für Monat, in **Ihrer** Zeitzone, nicht in der des Browsers.
- **Ein Ordner merkt sich, wie Sie ihn verlassen haben** - optional, über die Benutzereinstellungen: die Ansicht und die Sortierung jedes Ordners, den Sie sich tatsächlich eingerichtet haben, **pro Person auf dem Server** gespeichert, sodass sie Ihnen auf einen anderen Rechner und in die Desktop-App folgen und nie zu jemand anderem gelangen, der denselben Ordner ansieht. Standardmäßig ausgeschaltet, dann gilt Ihre letzte Wahl einfach überall ([docs/INTEGRATION.md](docs/INTEGRATION.md)).
- **Wem eine Datei gehört** - Jeder Knoten führt seinen Eigentümer mit, die Dateiliste hat eine Spalte **Eigentümer** und die Filterzeile einen Eintrag **Eigentümer**, und das Kontingent wird dem Eigentümer angerechnet, nicht dem, der die Datei zuletzt angefasst hat.
- **Archive in jedem gängigen Format** - Erstellen, öffnen und entpacken Sie ZIP, 7z, TAR
  und dessen Varianten mit gzip/bzip2/xz (RAR dort, wo das 7-Zip des Servers es mitbringt),
  mit Passwort für ZIP und 7z, als Hintergrundauftrag mit Fortschrittsanzeige. Links und
  Gerätedateien in einem Archiv werden abgelehnt, bevor etwas geschrieben wird, und
  Grenzwerte für Größe und Anzahl der Einträge stoppen eine Archivbombe am Grenzwert
  ([docs/ARCHIVES.md](docs/ARCHIVES.md)). Beigetragen von Alex (@ahjephson).
- **Eine Auswahl mitnehmen** - Wählen Sie mehrere Dateien und Ordner aus, und **Herunterladen** streamt sie als ein einziges Archiv, das erst während der Übertragung entsteht: In Ihren Speicher wird keine temporäre Datei geschrieben, im Tab wird nichts gepuffert, und ein Archiv von 700 MB kostet den Server weniger als ein Megabyte Arbeitsspeicher. **Verschieben nach** und **Kopieren nach** öffnen eine Ordnerauswahl, die alle Speicher umfasst und ein Ziel ablehnt, in das Sie nicht schreiben können - serverseitig, nicht nur im Dialog.
- **Neues Dokument** - Erstellen Sie über das Menü **+ Neu** eine Word-, Excel-, PowerPoint- oder OpenDocument-Datei oder eine Datei in einem beliebigen Text- oder Codeformat: Geben Sie ihr einen Namen - jeden beliebigen, auch `LICENSE`, `Makefile` oder `test.conf` -, wählen Sie, wohin sie kommt, und sie öffnet sich in dem Editor, der für sie zuständig ist. Die Vorlagen sind echte, minimale, gültige Dokumente, die in die Binärdatei einkompiliert sind, daher funktioniert das auch auf einer Installation ohne jede Office-Suite; ein Typ, den diese Installation anschließend nicht öffnen könnte, wird gar nicht erst angeboten, und der Dialog sagt, warum. Ein neues Dokument ist bis zum ersten Speichern ein **Entwurf**: Im Ordner erscheint nichts, bis Sie auf „Speichern“ drücken (ist der Name inzwischen vergeben, wird nachgefragt - `report (2).txt`? - und nie etwas ersetzt), beim Schließen kommt die Frage *Auf dem Datenträger speichern / In Entwürfen behalten / Verwerfen*, und **Entwürfe** im Navigationsbereich bewahrt die auf, mit denen Sie noch nicht fertig sind, für niemanden sonst sichtbar - standardmäßig 50 pro Person, festgelegt auf der Seite „Schutz“ im Adminbereich ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md#drafts-nothing-is-in-the-folder-until-you-save)).
- **RBAC + Berechtigungen pro Element** - Rollen (Administrator, Benutzer, Betrachter und benutzerdefinierte Rollen, jede eine Liste von Berechtigungen - [docs/PERMISSIONS.md](docs/PERMISSIONS.md)), Berechtigungen pro Datei und Ordner mit Vererbung unter **Admin → Ordnerzugriff**, **Gruppen**, die allen, die ihnen angehören, Ordnerzugriff und eine Rolle geben - Mitglieder von Hand hinzugefügt oder im Gleichschritt mit den Gruppen gehalten, die eine Anmeldung mitbringt ([docs/GROUPS.md](docs/GROUPS.md)) -, Einladungen zur Freigabe per E-Mail (SMTP), Suche und Dateilisten unter Berücksichtigung der Berechtigungen. **Mit mir geteilt** beantwortet die umgekehrte Frage aus Sicht des Empfängers - worauf Ihnen andere Zugriff erteilt haben und welche Speicher Sie nur über eine erteilte Berechtigung erreichen.
- **Die Shell** - ein einziges Layout, für den Betreiber wie für den Endbenutzer, im Adminbereich, in der Desktop-App und in jeder Einbettung: eine obere Leiste über die volle Breite mit der Schaltfläche zum Einklappen und dem Produktlogo an ihrem linken Rand, ein einziges **Suchfeld**, dessen Chip ⌘K / Strg+K die Suchanfrage an die Befehlspalette übergibt (das Feld durchsucht diesen Ordner; in der Palette sind „Überall“, gespeicherte Suchen und Befehle zu Hause), ein primäres Menü **+ Neu** (Dateien hochladen · Neuer Ordner · **Neues Dokument** · Dateien anfordern), eine Filterzeile **Typ · Eigentümer · Geändert · Größe** unter der Pfadleiste, **Ordner** und **Dateien** als beschriftete Abschnitte in der Rasteransicht, ein Detailbereich, aufgeteilt in **Details** (mit „Personen mit Zugriff“ und einer Zeile für den Freigabelink) und **Aktivität** (Versionsverlauf und Kommentare), und eine **Speicherplatzanzeige** unter der Navigation. Darstellung, Farbpalette, Sprache, Dichte, die Zeitzone, die Startseite und die Schalter für Benachrichtigungen liegen alle in den **Benutzereinstellungen**, erreichbar über den Avatar - und die Web-App speichert Ihre Darstellung, Farbpalette, Dichte und Sprache in Ihrem **Konto**, nicht im Browser, sodass sie im nächsten schon auf Sie warten; der Editor für Tastenkürzel und *Tour neu starten* stehen im selben Menü. Aus dem Build wird nichts entfernt - eine Einbettung, die keinen Einstellungsdialog hat, behält ein Menü „⋯“, in dem sie weiterhin stehen ([docs/INTEGRATION.md](docs/INTEGRATION.md)).
- **Start, innerhalb der Shell** - die Startansicht für alle, Admins eingeschlossen: Ihre Speicher, was Sie zuletzt geöffnet haben, und Ihre Favoriten, als Karten im Inhaltsbereich, mit demselben Navigationsbereich und derselben Kopfzeile wie bei den Dateien. Der Wechsel zwischen „Start“ und einem Ordner ändert den Inhalt und sonst nichts. Ein Betreiber, der lieber auf der Übersicht im Adminbereich landet, wählt sie in seinen Benutzereinstellungen.
- **Navigationsbereich** - das Menü **+ Neu** als primäre Aktion, die Ziele Start / Meine Dateien / Mit mir geteilt / **Meine Freigaben** / Zuletzt verwendet / Favoriten / **Entwürfe** / Papierkorb, die Speicher, die Sie sehen können - **in Ihrer eigenen Reihenfolge** (ziehen Sie eine Zeile, oder wählen Sie in deren Menü Nach oben / Nach unten / Nach Name sortieren; in Ihrem Konto gespeichert), sonst in der Reihenfolge, die der Administrator auf der Seite „Speicher“ festgelegt hat ([docs/STORAGE.md](docs/STORAGE.md#ordering-storages)) -, ein Abschnitt **Apps**, wenn eine installierte App einen Startbildschirm hat, und **So verbinden Sie sich** + **API-Schlüssel**: die Anleitungen pro Protokoll und die Verwaltung der API-Schlüssel in Selbstbedienung, aus dem Explorer heraus geöffnet, damit die Benutzer einer eingebetteten Kopie die Zugangsdaten selbst erstellen können, die WebDAV/FTPS/`filex mount` verlangen, statt einen Administrator zu fragen. Über die obere Leiste zur Icon-Leiste einklappbar (pro Browser gemerkt), unter 560px eine ausfahrbare Leiste statt einer Spalte. In der Web-App, der Desktop-App und jeder Einbettung standardmäßig eingeschaltet; `uiProfile: 'simple'` schaltet zusätzlich die Tab-Leiste, die geteilte Ansicht, die Galerieansicht und den Bereich „So verbinden Sie sich“ aus, ohne etwas davon aus dem Build zu entfernen ([docs/INTEGRATION.md](docs/INTEGRATION.md)).
- **Freigaben** - öffentliche Links mit PIN, Ablaufdatum und Download-Limit, innerhalb einer vom Admin festgelegten **maximalen Linklaufzeit** (Standard 7 Tage - der Dialog bietet nur an, was der Server auch behält); Ordnerlinks werden als ZIP gestreamt (zwischengespeichert, bis zu einer Größenobergrenze vorgewärmt, nach einer Woche aufgeräumt); Upload-Links zur **Dateianforderung** für eingehende Dateien; ShareX-kompatibler Upload-Endpunkt. **Meine Freigaben** listet die von Ihnen erstellten Links auf - für alle, nicht nur für Administratoren - mit *Link kopieren*, *PIN kopieren* und *Widerrufen*: Die PIN eines Links wird versiegelt neben dem Hash aufbewahrt, der ihn schützt, sodass die Person, die ihn erstellt hat, oder ein Administrator sie wieder auslesen kann, wenn jemand sie noch einmal braucht, und jedes Auslesen wird ins Audit-Protokoll geschrieben. Fünf falsche PINs sperren jeden öffentlichen Link für zehn Minuten. Ein Download-Link, eine Dateianforderung und die Seite einer App sind **ein einziger öffentlicher Bildschirm in Ihrem Branding** - Name, Logo und Farben Ihrer Instanz, eine einzige PIN-Abfrage, eine einzige Regelung zum Ablaufdatum und eine Sprachauswahl ([docs/SHARING.md](docs/SHARING.md)).
- **Desktop-App + Ordner-Sync** - App für Windows/Linux/macOS: bidirektionale Synchronisierung im Infobereich, **selektive Synchronisierung** (Rechtsklick → *Auf diesem Computer behalten*, ein Stammordner pro Konto, der Rest nur online), mehrere Konten gleichzeitig, **öffnet Office-Dokumente von Ihrer eigenen Festplatte** im Editor des Servers, aktualisiert sich selbst (macOS: nicht signierter Build, Updates durch erneutes Herunterladen, bis er signiert ist). Jedes Dokument öffnet sich in **einem eigenen Fenster** (mit dem Namen der Datei als Titel), die Fenster sind **rahmenlos** mit den eigenen Bedienelementen der App (native Ampelknöpfe unter macOS), und **Settings → Open files with** („Dateien öffnen mit“ in den Einstellungen) legt fest, ob ein Einfachklick oder ein Doppelklick öffnet ([docs/DESKTOP.md](docs/DESKTOP.md), [docs/SYNC.md](docs/SYNC.md)).
- **Papierkorb & Versionsverlauf** - Löschungen sind innerhalb einer Aufbewahrungsdauer umkehrbar, Schreibvorgänge hinterlassen Snapshots; beides liegt in dem Speicher, den Sie bereits eingebunden haben ([docs/TRASH-VERSIONING.md](docs/TRASH-VERSIONING.md)).
- **Schutz beim Schreiben** - optionaler ClamAV-Scan jeder geschriebenen Datei - der integrierte Editor eingeschlossen, ebenso Dateien, die nicht über filex kommen, sondern von der Speicher-Synchronisierung auf dem Backend gefunden werden - erreicht wird ClamAV über eine lokale Binärdatei oder über einen clamd-Container im Netzwerk; dazu die Aufbewahrung für Papierkorb und Versionen auf einer einzigen Seite im Adminbereich. Der Schalter, der Modus und die Adresse des Scanners, die Größenobergrenze und das Scanfenster beim Speichern im Editor stehen unter **Einstellungen → Schutz**; die `FILEX_CLAMAV*`-Variablen belegen sie beim ersten Start vor und treten dann zurück (der Pfad zur Binärdatei des Scanners bleibt absichtlich eine reine Umgebungseinstellung - er ist ein Befehl, den dieser Server ausführt) ([docs/PROTECTION.md](docs/PROTECTION.md)).
- **E2E-verschlüsselte Ordner** - clientseitiges WebCrypto; der Server speichert verschlüsselte Daten und erhält nie einen Schlüssel. Ein Ordner hat eine **Stufe**: „Nur Inhalte“ (der Standard - WebDAV, die CLI und die Desktop-Synchronisierung arbeiten weiter mit seinen Namen) oder **Inhalte und Namen** (AES-SIV, sodass der Server keinen lesbaren Namen behält), und sie lässt sich später in den **Verschlüsselungseinstellungen** des Ordners fortsetzbar anheben, wo auch sein Passwort geändert wird. Ein Ordner, den Sie schon haben, wird **an Ort und Stelle verschlüsselt**, Dateien über 200 MB eingeschlossen; **jede einzelne Datei lässt sich für sich allein verschlüsseln** (eine eigenständige `.fxe`-Datei mit eigenem Passwort und eigenem Wiederherstellungsschlüssel); Dateien jeder Größe werden als Stream verschlüsselt; ein entsperrter Ordner wird als **entschlüsseltes ZIP** heruntergeladen, das im Browser entsteht; `filex decrypt` öffnet einen heruntergeladenen Ordner oder eine `.fxe`-Datei auf Ihrem eigenen Rechner, und **`filex encrypt`** macht aus einem Ordner auf der Festplatte einen verschlüsselten Ordner oder verschlüsselt einen Ordner auf dem Server dort, wo er liegt - für Ordner, die für einen Tab zu groß sind, fortsetzbar und mit Schlüsseln, die auf Ihrem Rechner entstehen ([docs/CLI.md](docs/CLI.md#filex-encrypt---make-a-folder-an-encrypted-folder)). Jeder Ordner bekommt einen **Wiederherstellungsschlüssel**, der einmal angezeigt wird, damit ein vergessenes Passwort nicht automatisch verlorene Daten bedeutet; ein Betreiber kann optional die **Schlüsselhinterlegung** aktivieren - bei der Installation, oder nachträglich in einer laufenden Installation eingeführt; von sich aus erreicht sie bestehende Ordner nie, aber deren Eigentümern wird beim Entsperren die Wahl angeboten - und bei ihrer Verwendung wird der Eigentümer des Ordners benachrichtigt ([docs/E2E-ENCRYPTION.md](docs/E2E-ENCRYPTION.md)). **Wer verschlüsseln darf**, entscheidet die Organisation: ein Schalter des Plattformbetreibers pro Mandant, eine Richtlinie des Mandanten (aus, nur Administratoren, alle, deren Rolle es erlaubt, oder **nach Genehmigung durch einen Administrator** - eine Anfrage mit Begründung, genehmigt für eine Person, einen Ordner und eine Art der Verschlüsselung, ein einziges Mal) und die Berechtigung `files.encrypt`, abgefragt an jedem Zugang, über den etwas neu Verschlüsseltes entstehen könnte, Kopien eingeschlossen ([docs/E2E-ENCRYPTION.md](docs/E2E-ENCRYPTION.md#who-may-encrypt)).
- **Native Mandantenfähigkeit** - Anbieter-/Mandantenmodus mit Isolation pro Mandant auf einer einzigen Instanz. Jeder Mandant hat einen **Realm** - seinen Anmeldenamen, bei der Erstellung vergeben und nie geändert -, sodass eine Anmeldung ihren Mandanten über dessen eigene Adresse (die Webseite, den WebDAV-`Host`, den FTPS-Zertifikatsnamen) oder über den Realm nennt: ein Feld **Realm** im Anmeldeformular, `realm/name` über SFTP. Die Kontosuche verlässt den Mandanten nie, und ein auf der Seite der Plattform eingegebener Realm eines Mandanten mit eigener Adresse wird mit einem nur einmal verwendbaren 60-Sekunden-Ticket dorthin **übergeben** ([docs/MULTI-TENANCY.md](docs/MULTI-TENANCY.md), [Realms](docs/MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)). Mandanten verwalten sich selbst unter **Admin → Mandanten** und **Mein Mandant**: an einen oder mehrere Mandanten gebundene Anmeldeanbieter, das eigene OIDC und LDAP eines Mandanten, eine Plattform-Subdomain für jeden Mandanten und eigene Domains, per CNAME nachgewiesen und zertifiziert durch den Proxy, durch filex selbst (ACME) oder mit dem eigenen Zertifikat des Mandanten ([docs/TENANT-ADMIN.md](docs/TENANT-ADMIN.md)).
- **Alles über Treiber austauschbar** - Treiber für Speicher / Authentifizierung / Datenbank / Warteschlange werden per Umgebungsvariable eingeschaltet (`FILEX_AUTH_DRIVERS=local,oidc`, `FILEX_QUEUE_DRIVER=postgres`, …); die Anmeldung über das Betriebssystem (`windows`, `pam`) ist die Ausnahme und wird im Adminbereich eingeschaltet, sobald ihr Test bestanden ist.
- **OIDC, SSO zuerst** - optionale automatische Weiterleitung zu Ihrem IdP mit lokaler Notfall-Anmeldung (`?local=1`), und die Admin-Rolle folgt bei jeder Anmeldung einer IdP-Gruppe.
- **LDAP / Active Directory** - Verzeichniskonten melden sich über dasselbe Passwortformular an wie lokale, und mit demselben Passwort bei WebDAV, SFTP und FTPS (S3 und NFS nehmen die Schlüssel und Exporte, die diese Konten erstellen); Unterstützung für private Zertifizierungsstellen, und `local` bleibt an erster Stelle, damit `admin@local` funktioniert, solange das Verzeichnis nicht erreichbar ist. Die E-Mail eines Kontos ist immer eine Adresse: das Mail-Attribut des Eintrags, sonst ein als `name@domain` eingegebener Name, sonst `name@local` (`name@<realm>.local` im Realm eines Mandanten; die eine Regel, die auch die Anmeldeanbieter für Betriebssystemkonten anwenden); ein Konto, das ein älteres filex unter dem bloßen Namen angelegt hat, wird bei der nächsten Anmeldung **übernommen**, wobei Dateien, Freigaben und Rolle unverändert bleiben ([docs/LDAP.md](docs/LDAP.md)).
- **Replikation + Abgleich** - Fan-out Primärspeicher→Replikat (Spiegeln / Nur anhängen / Überspringen je Pfad-Glob-Regel), Lese-Fallback, geplanter Statusbericht, „Alle beheben“ mit einem Klick.
- **Persistente Vorgangswarteschlange** - neustartsichere Warteschlange in Ihrer eigenen Datenbank (SQLite / Postgres / MySQL) oder in Redis, Worker-Pool mit Wiederholungen + Abbrechen + Übersicht im Adminbereich. Jeder Treiber ordnet nach Priorität, sodass der Virenscan für eine Datei, die jemand gerade hochgeladen hat, vor den zwanzigtausend an die Reihe kommt, die ein erster Import in die Warteschlange gestellt hat. Ohne Angabe folgt der Treiber der Datenbank, statt standardmäßig SQLite zu nehmen - SQLite-Anweisungen an einen Postgres-Server zu richten, ist bei jeder Abfrage ein Syntaxfehler, und kein Auftrag läuft jemals.
- **Dateibaum aus der Datenbank** - Dateilisten kommen aus dem DB-Cache (1-5 ms), nicht vom Speicher-Backend (~100 ms); eine regelmäßige Synchronisierung erfasst Änderungen, die an filex vorbei geschehen, per ETag, wo das Backend eines meldet, und per Größe + Änderungszeit, wo nicht. Das Feld **Vom Scan ausgeschlossene Pfade** eines Speichers (`.*`, `downloads/incomplete/**`, `*.tmp`) hält die Teile eines bestehenden Verzeichnisbaums, für die filex keine Verwendung hat, aus dem Durchlauf, dem Katalog, dem Suchindex und dem Virenscanner heraus - eine Kostenbremse, keine Zugriffskontrolle ([docs/STORAGE.md](docs/STORAGE.md#scan-exclusions)).
- **Lazy-Katalog für große lokale Verzeichnisbäume** - `sync_mode: lazy` überspringt den Durchlauf zu Beginn: Der Ordner, den Sie öffnen, wird sofort direkt vom Datenträger aufgelistet und zuerst katalogisiert, und der Rest wird von einem langsamen Hintergrunddurchlauf katalogisiert, der Personen den Vortritt lässt (oder nur dann, wenn Ordner geöffnet werden). Geöffnete Ordner werden innerhalb eines Budgets überwacht, ein Ordner, den niemand besucht hat, wird nie als gelöscht behandelt, und Suche, Ordnergrößen und Nutzung sagen es klar, wenn sie noch nicht alles abdecken ([docs/STORAGE.md](docs/STORAGE.md#lazy-catalogue), [Konzept](docs/LAZY-CATALOGUE.md)). Idee von Alex ([#45](https://github.com/BRF-Tech/filex/issues/45)).
- **Vorschau & Editoren** - Bild/Video/Audio, PDF, Markdown (geteilter Editor + Vorschau), CSV (die Tabellenkalkulation von ONLYOFFICE, wenn es konfiguriert ist, sonst eine schreibgeschützte Tabelle), Code (Monaco), Office über ONLYOFFICE, Diagramme mit Drawio + Mermaid, 3D-Modelle. Ein Dokument, das ONLYOFFICE nur in einem neueren Format speichern kann (eine `.doc`-Datei, bearbeitet und als DOCX gespeichert), wird unter der richtigen Endung **neben** dem Original abgelegt, das Original wird nie überschrieben, und die Personen, die es bearbeitet haben, erfahren davon ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md#a-save-in-another-format)). Bei ONLYOFFICE führt **Jetzt testen** einen Abruf über denselben Zugang aus, den ein Dokument verwendet, und warnt, wenn der Dokumentserver JWT nicht erzwingt, und nach *Download failed* („Herunterladen ist fehlgeschlagen“) sagt der Editor, welcher der beiden Fehler hinter dieser Meldung steckt ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md#failure-editor-shows-download-failed)). Gibt es für einen Dateityp mehr als eine App oder Vorschau, lässt sich über **Öffnen mit** und **App auswählen…** eine wählen, und *Immer diese App verwenden* wird in Ihrem Konto gespeichert.
- **Benachrichtigungen** - generische JSON-Webhooks (unabhängig von Slack/Discord): beliebig viele Ziele, jedes mit eigenem Signatur-Secret und eigenem Abonnement pro Ereignis, dazu eine Glocke in der App mit Gelesen/Ungelesen und einer Stummschaltungsmatrix pro Benutzer. Die Zahl der ungelesenen Benachrichtigungen steht als **Badge an der Glocke** - genau bis 99, darüber `99+`, und am Dock-Symbol der Desktop-App, wo das System eines hat - eine Zeile ist genau dann anklickbar, wenn sie irgendwohin führt (eine Unterschriftsanfrage öffnet den Unterschriftsbildschirm, keine Benachrichtigungsseite), und **Alle anzeigen** öffnet jede einzelne Ihrer Benachrichtigungen über dem Explorer, für alle und nicht nur für Administratoren. Ein Schreibvorgang, der eine Datei **erstellt**, und einer, der eine **ersetzt**, sind verschiedene Ereignisse (`file.uploaded` / `file.updated`), und diejenigen, die ein Betreiber am ehesten gesondert haben will - ein unter Quarantäne gestellter infizierter Upload, ein fehlgeschlagener Upload, ein mit seinem Wiederherstellungsschlüssel geöffneter verschlüsselter Ordner -, lassen sich einzeln abonnieren ([docs/NOTIFICATIONS.md](docs/NOTIFICATIONS.md)).
- **Suche** - Bleve eingebettet, Volltext + Metadaten, unter Berücksichtigung der Berechtigungen. Bewertung von Dateinamen im Stil von VS Code: Ordner zählen, die Wortreihenfolge nicht (`main code` findet `Code/main.go`), Trennzeichen und Tippfehler werden verziehen (`invoice 2026` findet `invoice_2026.pdf`, `mian.go` findet `main.go`), während Zahlen wörtlich genommen werden (`2026` bedeutet nie `2025`), `tag:`-Filter, exakte Treffer zuerst. Ein ⌘K-Ergebnis lässt sich herunterladen (ein Ordner als ein einziges ZIP) oder dort herausziehen, wo es steht ([docs/SEARCH.md](docs/SEARCH.md)).
- **Vorschaubilder, die man lesen kann**: Ein PDF zeigt seine **erste Seite**, am oberen Rand ausgerichtet, damit der Titel auf der Karte steht; ein Video sein erstes nicht schwarzes Einzelbild (eine Aufblende am Anfang ergab früher ein schwarzes Quadrat, und ein Clip, der kürzer als eine Sekunde war, ergab gar nichts, während in der Zeile trotzdem „bereit“ stand); ein Office-Dokument seine gerenderte erste Seite; und eine Text-, Code- oder CSV-Datei **füllt die Karte mit ihren eigenen ersten Zeilen**, statt die Endung zu wiederholen, die schon in der Zeile steht. Bild, Video (ffmpeg), PDF (ghostscript), Office (das angebundene ONLYOFFICE); abhängig von den vorhandenen Fähigkeiten, und ein Server, dem eine dieser Binärdateien fehlt, sagt das jetzt beim Start in seinem Protokoll, statt stillschweigend farbige Rechtecke zu zeichnen. Ein zwischengespeichertes Vorschaubild wird verworfen, wenn die Datei, zu der es gehört, endgültig gelöscht wird, und ein regelmäßiger Abgleich räumt die verwaisten Bilder weg, die eine ältere Installation angesammelt hat. Ein Vorschaubild **folgt seiner Datei**: Wurde eine Datei außerhalb von filex geändert oder hatte sie nie ein Bild, wird es neu erzeugt, wenn eine Dateiliste oder die Speicher-Synchronisierung sie sieht; **SVG** wird in jeder Installation von einer integrierten Engine gezeichnet (mit Grenzwerten für Größe und Zeit, die ein Administrator festlegt), und **HEIC/AVIF**-Fotos laufen über ImageMagick; transparente Bilder liegen auf einem Schachbrettmuster; ein **Ordner zeigt die Dateien, die zuletzt hinzugekommen sind**, im Raster, in der Galerie und in der Liste zusammen mit dem Ordner dargestellt, und beim Überfahren mit der Maus, was er enthält (ein Administrator kann das ausschalten); Textdateien zeigen ihre ersten Zeilen, Archive ihren Inhalt; eine Datei, deren Werkzeug fehlt, wird benannt, nicht kaschiert; und **Admin → Werkzeuge → Vorschaubilder reparieren** erzeugt die Vorschaubilder einer Datei, eines Ordners oder eines Speichers bei Bedarf neu ([docs/thumbnails.md](docs/thumbnails.md)).
- **Tabs, Designs & Deep Links** - mehrere Ordner nebeneinander geöffnet, helle, dunkle oder automatische Darstellung sowie eine Adressleiste, die dem geöffneten Ordner folgt, sodass ein eingefügter Link dort landet. Die Designgalerie bringt acht Farbpaletten mit, jede eine Belegung der `--fe-*`-Tokens und kein zweites Stylesheet, sodass eine Host-Seite oder eine Einbettung eine davon wählen kann - oder eigene Werte setzen -, ohne CSS zu forken; ein Betreiber kann eigene hinzufügen (siehe *Darstellung*).
- **Darstellung: Ihre Farben, überall** - Die Seite **Darstellung** im Adminbereich stellt benannte Designs zusammen - zwölf Farben für Hell und für Dunkel, einen Eckenradius, einen Font-Stack - mit Vorschau, während Sie tippen, und macht eines davon zum **Standard der Instanz**. Die Textfarbe auf einer farbigen Schaltfläche wird nach Kontrast gewählt und nicht als weiß vorausgesetzt, der Rest der Farbpalette wird auf dem Server abgeleitet, und das Design erreicht die Anmeldeseite und jeden öffentlichen Link - in seinen eigenen Tönen oder in Farben, die Sie diesen beiden Seiten eigens geben -, denn Branding, das an der Anmeldung endet, ist keines: Eine Seite ohne Anmeldung trägt den Standard der Instanz, nie die Farbpalette der Person, die diesen Browser zuletzt benutzt hat, und die eigene Wahl einer angemeldeten Person gewinnt. Designs lassen sich als eine einzige JSON-Datei exportieren und importieren. Ein **eigenes Stylesheet** ist das gefährliche Werkzeug daneben, und es ist jetzt ausgeschaltet, bis Sie es einschalten, wird nie an jemanden ausgeliefert, der nicht angemeldet ist, kann nichts abrufen und kann die Seite nicht erreichen, die es ausschaltet ([docs/INTEGRATION.md](docs/INTEGRATION.md#themes)).
- **Eine Tabelle, überall** - In filex gibt es nur noch eine Tabelle, die des Explorers, und jede andere Liste ist diese Tabelle: die Menüs des Adminbereichs, **Meine Freigaben**, die eigenen Bildschirme einer App. Jede fixiert ihre erste Spalte links und ihre Aktionen rechts, lässt sich auf dieselbe Weise in der Breite ändern, umordnen und sortieren und schließt jede Zeile mit **einem einzigen angehefteten Menü „Aktionen“** ab, das alles enthält, was diese Zeile kann - dasselbe Menü, das sich über das ⋮ des Explorers öffnet, sodass sich eine zweite Tabelle nicht von der ersten entfernen kann. Eine installierte App mit Startbildschirm bekommt eine eigene Zeile unter **Apps** in der Navigation des Adminbereichs.
- **Ein Adminbereich, in dem man sich zurechtfindet** - Die Seiten des Administrators stehen in einem Megamenü in der oberen Leiste: die Übersicht, dann **Dateien & Speicher**, **Personen & Sicherheit** und **System**, jeweils ein Bereich mit benannten Abschnitten und einer kurzen Zeile unter jeder Seite. Jede Seite liegt zwei Klicks entfernt unter der Adresse, die sie immer hatte, einem delegierten Administrator werden nur die Seiten angeboten, die seine Berechtigungen öffnen, Tastatur und Screenreader sind berücksichtigt, und ein Smartphone bekommt dieselben Seiten als Liste in einer ausfahrbaren Leiste ([Adminbereich](docs/ADMIN-PANEL.md)).
- **Symlinks, an der Speichergrenze** - Zeigt ein Link in einem `local`-Speicher auf etwas innerhalb dieses Speichers, wird ihm gefolgt, und er öffnet sich als das, worauf er zeigt; verlässt einer den Speicher, wird er **mit einer Kennzeichnung und dem Grund aufgelistet** und beim Lesen, Schreiben und Löschen abgelehnt - es sei denn, Sie schalten für diesen Speicher *Symlinks folgen, die diesen Ordner verlassen* ein ([docs/STORAGE.md](docs/STORAGE.md#symlinks)).
- **Öffnen, wie es jedes Gerät erwartet** - Mit der **Maus** wählt ein Einfachklick aus, und ein **Doppelklick öffnet** (die Eingabetaste öffnet die Auswahl) - die klassische Geste eines Dateimanagers und eine Einstellung pro Person (`ExplorerConfig.openTrigger`, Standard `'double'`; die Desktop-App bietet sie als **Settings → Open files with** an, und `'single'` stellt das Öffnen per Einfachklick wieder her). Auf einem **Touchscreen** öffnet Tippen immer - Auswählen durch Darüberfahren gibt es nicht. Auf jedem Gerät ist das **Kontrollkästchen** der eine Klick oder das eine Tippen, mit dem man auswählt (Umschalt erweitert den Bereich), und ein Rechtsklick oder langes Drücken öffnet das Menü; Listenzeilen, Rasterkarten und Galeriekacheln tragen es alle.
- **Tastatur, und es steht dabei** - Jedes Verb im Kontextmenü und in der Symbolleiste zeigt die Taste an, die es auslöst, und liest sie aus dem Register, sodass sie einer Neubelegung folgt. Zweiunddreißig Aktionen lassen sich über *Einstellungen für Tastenkürzel* neu belegen (pro Browser gespeichert); die Handvoll Kombinationen, die ein Browser für sich beansprucht, etwa `Ctrl+W`, werden mit einer Begründung abgelehnt, statt als Taste gespeichert zu werden, die nie etwas auslösen würde.
- **Nutzung & Kosten** - filex misst nicht selbst, was Ihr Anbieter abrechnet; es liest den Bericht, den der Anbieter schon schreibt, normalisiert ihn und bepreist ihn anhand einer Tabelle, die Sie bearbeiten können. Die täglichen CSV-Dateien von Backblaze B2 werden über dieselbe S3-API gelesen, die filex schon spricht, also keine neue Abhängigkeit und keine neue Art von Zugangsdaten. Freikontingente sind eigene Felder und keine Konstanten in einer Formel, und die Seite hält die Zeile des Anbieters auf Kontoebene von dessen Zeilen pro Bucket getrennt - wer sie addiert, zählt dieselben Transaktionen doppelt, um genau den Betrag, der niemandem auffällt ([docs/USAGE.md](docs/USAGE.md)).
- **Audit-Protokoll** - jede Änderung mit Akteur, Identität der Integration und Metadaten aufgezeichnet.
- **CLI-Client** - Dieselbe Binärdatei erreicht einen entfernten Server (`filex client`, `filex sync`) ohne serverseitiges Plug-in: Kopieren und Verschieben über Speicher hinweg, der Papierkorb, Versionen, Tags, App-Aktionen, Archive und Ihre Links, jeder Serverauftrag bis zum Ende verfolgt; `filex client login --realm` meldet sich bei einem Mandanten an, `filex encrypt` erstellt verschlüsselte Ordner, und eine gespeicherte Sitzung wird immer nur an die Adresse gesendet, mit der sie gespeichert wurde ([docs/CLI.md](docs/CLI.md)).
- **Aktualisiert sich selbst** - Patch-Versionen installieren sich selbst, Nebenversionen werden zur Aktualisierung mit einem Klick angekündigt; eine Installation, die einem Paketmanager gehört (Homebrew, winget, Snap, ein Distributionspaket), oder ein Container wird über neue Releases und den Befehl informiert, mit dem man sie einspielt, und die Seite im Adminbereich sagt, dass nur angekündigt wird ([docs/UPDATES.md](docs/UPDATES.md)).
- **Eine einzige Binärdatei** - goreleaser-Matrix: linux/macOS/Windows × amd64/arm64. CGO=0, modernc.org/sqlite.
- **i18n** - Englisch + Türkisch ab Werk, **öffentliche Links inbegriffen**: Ein
  Freigabelink, eine PIN-Abfrage, die Seite einer Dateianforderung oder der
  Unterschriftsbildschirm einer App erscheint in der Sprache des Besuchers, und die
  öffentliche Shell **bietet eine Auswahl an**, denn die Browsersprache eines
  Fremden ist eine Vermutung, und wer einen Vertrag liest, sollte sie korrigieren
  können. Die schlichten Seiten ohne JS werten `?lang=` aus, dann
  `Accept-Language`, dann den Server-Standard. **Der Text, den der Server
  schreibt, stammt aus demselben Katalog** - E-Mails, die Formulierungen der
  Benachrichtigungen, die Seiten ohne JavaScript und die Berechtigungsprüfung bei
  einer Installation - jeweils an den Leser gerichtet, den dieser Text schon immer
  hatte, pro Schlüssel mit Rückgriff auf Englisch, und eine Übersetzung, deren
  Platzhalter nicht zum Englischen passen, wird zur Laufzeit nicht verwendet,
  sodass eine E-Mail nie ihren Link oder ihre PIN verliert.
- **Sprachpakete** - Jede andere Sprache ist eine **App, in der nichts läuft**:
  ein Manifest mit Texten, wie jede andere App aus einem GitHub-Repository, per
  Upload oder von einer URL installiert und unter **Plug-ins → Apps**
  aufgelistet, mit seiner Abdeckung der laufenden Version (*Español - 97 %
  übersetzt · der Rest wird auf Englisch angezeigt*). Die Sprache des Pakets
  erscheint in jeder Sprachauswahl - im Einstellungsdialog, in der Kopfzeile des
  Adminbereichs, auf öffentlichen Freigabeseiten - und übersetzt den Explorer,
  den Adminbereich und die öffentlichen Seiten gleichermaßen. Pluralformen folgen
  den **CLDR-Kategorien**, ein Paket schreibt also `zero`, `one`, `two`, `few`,
  `many` und `other`, wo seine Sprache sie hat. Spanisch, Deutsch und Französisch
  gibt es als Beispiele, und ein Vorlagen-Repository sowie
  `scripts/i18n-export.mjs` / `i18n-validate.mjs` führen einen Übersetzer vom
  Export bis zur Installation - der Validator misst ein Paket an den Regeln, an
  die sich die integrierten Sprachen halten, darunter ein einfacher Bindestrich,
  wo ein Text zu einem Gedankenstrich greifen würde
  ([eines schreiben](docs/PLUGIN-KIT.md#writing-a-language-pack)).
- **Layout von rechts nach links** - Für Arabisch, Hebräisch, Persisch, Urdu und
  jede andere von rechts nach links geschriebene Sprache ordnet sich die gesamte
  Oberfläche von rechts nach links an: der Navigationsbereich, die Tabellen, die
  Geometrie beim Ziehen und Ablegen sowie beim Ändern der Spaltenbreite, die
  Menüs und die Richtungssymbole. Was **nicht** gespiegelt werden darf, wird es
  auch nicht - ein PDF-Feldeditor arbeitet im Koordinatenraum des Dokuments, und
  ein Pfad, ein Befehl oder jeder andere Maschinentext wird isoliert, sodass er
  in einem von rechts nach links laufenden Satz von links nach rechts gelesen
  wird, auch in Sätzen, die der Server geschrieben hat. Die Regel wird von einem
  Wächtertest durchgesetzt: Das Layout wird ausschließlich mit logischen
  CSS-Eigenschaften geschrieben ([docs/RTL.md](docs/RTL.md)).
- **Tags, persönlich oder für Ihr Team** - Ein Tag ist entweder **persönlich** -
  es gehört Ihnen allein und wird niemandem sonst je genannt - oder ein
  innerhalb des Mandanten geteiltes **Team**-Tag, das sich nur mit der
  Berechtigung zum Bearbeiten der Datei hinzufügen oder entfernen lässt. Ein Tag
  öffnet alle Dateien, die es tragen, aus allen Ordnern, in denen sie liegen,
  und `tag:` grenzt eine Suche ein. Die Groß- und Kleinschreibung bleibt so, wie
  sie eingegeben wurde.
- **Identitätsanbieter, im Adminbereich verwaltet** - **Admin → Identitätsanbieter**
  steuert jetzt die Anmeldung, statt Einstellungen zu speichern, die nirgends
  gelesen wurden: OIDC, LDAP, der Header-Proxy, das lokale Passwortformular und
  die eigenen Konten des Betriebssystems - **Windows** (lokal oder Domäne,
  `LogonUserW`, nichts zu installieren) und **Linux PAM** - jeweils mit einem
  **Jetzt testen**, das den Anbieter wirklich prüft und sagt, welche Teilstrecke
  es verifiziert hat. Ein Anbieter für Betriebssystemkonten wird nur durch einen
  Test eingeschaltet, der ein echtes Konto angemeldet hat, und dieses Konto wird
  Super-Administrator; aus der Umgebung lässt er sich nicht einschalten. Jeder
  Anbieter folgt **einer einzigen Regel für die erste Anmeldung** - ob er ein
  Konto anlegen darf (`auto_create`, für Windows und PAM standardmäßig ausgeschaltet)
  und für welche Gruppen (`allowed_groups`). Was in der Umgebung oder in
  `config.yaml` gesetzt ist, **gewinnt, und zwar sichtbar**; ein Client-Secret
  oder Bind-Passwort ist nur schreibbar und wird mit `FILEX_SECRET_KEY`
  versiegelt gespeichert, nie zurückgesendet; und der letzte verbliebene Zugang
  lässt sich auf der Seite nicht ausschalten ([docs/SSO.md](docs/SSO.md),
  [docs/LDAP.md](docs/LDAP.md), [docs/OS-LOGIN.md](docs/OS-LOGIN.md)).
- **Anmeldesicherheit** - Falsche Passwörter werden pro Kontokennung (ob das
  Konto existiert oder nicht, damit die Zählung nichts verrät) und pro
  Client-Adresse gezählt, im Webformular ebenso wie bei WebDAV, FTPS und SFTP:
  5 pro Konto und 10 pro Adresse innerhalb von 10 Minuten sperren den Zugang für
  eine Minute, verdoppelt bis auf 15, und eine Sperre lehnt auch das richtige
  Passwort ab. Das Anmeldeformular sagt in der Sprache des Lesers, wie viele
  Versuche noch bleiben. Eine **Liste erlaubter IP-Adressen** ist der Weg zurück
  hinein - kein Konto ist privilegiert, auch das des ersten Administrators nicht
  (nur das gemeinsame Konto einer öffentlichen Demo wird ausschließlich pro
  Adresse gezählt, [docs/DEMO.md](docs/DEMO.md)) - und **Admin →
  Anmeldesicherheit** enthält die Grenzwerte, die Liste, die Sperren mit *Sperre
  aufheben* und das Anmeldeprotokoll: jeden Fehlversuch, jede Sperre, jede
  aufgehobene Sperre und jede Änderung der Einstellungen, jeweils einmal,
  welchen Zugang ein Administrator auch genutzt hat (den Adminbereich, einen
  API-Schlüssel, MCP). Gezählt wird die Adresse der Socket-Gegenstelle, es sei
  denn, diese Gegenstelle ist ein Proxy, dem Sie vertrauen
  (`FILEX_TRUSTED_PROXIES`, standardmäßig `auto`: dieser Rechner und, in einem
  Container, die anderen Container in dessen Netzwerk - nie das Gateway, nie das
  LAN; die Seite nennt eine Gegenstelle, die weitergeleitete Adressen sendet,
  ohne dass ihr vertraut wird, und bietet an, sie hinzuzufügen), sodass ein
  Client seine Adresse nicht selbst wählen kann, indem er `X-Forwarded-For`
  schreibt
  ([docs/CONFIGURATION.md](docs/CONFIGURATION.md#sign-in-attempt-limits)).
- **Änderungen von einer anderen Website werden abgelehnt** - Eine Anfrage, die etwas
  ändert und nur die Sitzung des Besuchers mitbringt (das Cookie oder den Anmelde-Header
  eines vertrauenswürdigen Proxys), muss von den eigenen Seiten von filex, von dessen
  eigener Adresse oder von einem Ursprung (Origin) kommen, der in
  `FILEX_CORS_ALLOWED_ORIGINS` aufgeführt ist; alles andere wird mit
  `403 cross_origin_refused` beantwortet, bevor eine Route läuft. Schlüssel, Freigabe- und
  Upload-Links, Upload-Tickets, S3 und Skripte sind nicht betroffen
  ([docs/CONFIGURATION.md](docs/CONFIGURATION.md#requests-from-other-origins)).

## Architektur

Siehe [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Dokumentation

**Erste Schritte** - [Installation](docs/INSTALLATION.md) ·
[Konfiguration](docs/CONFIGURATION.md) · [Adminbereich](docs/ADMIN-PANEL.md) ·
[Datenbanken](docs/DATABASES.md) · [Releases](docs/RELEASES.md) · [Updates](docs/UPDATES.md) ·
[Demo-Modus](docs/DEMO.md)

**Clients** - [Desktop-App](docs/DESKTOP.md) · [Ordner-Sync](docs/SYNC.md) ·
[CLI](docs/CLI.md) · [Integration / Einbettung](docs/INTEGRATION.md) ·
[KI & MCP](docs/MCP.md)

**Ohne Browser** - [Protokolle (S3 · SFTP · FTPS · NFS · WebDAV ·
`filex mount`)](docs/PROTOCOLS.md) · [WebDAV](docs/WEBDAV.md)

**Apps** - [Apps: installieren, steuern, unterschreiben, konvertieren](docs/APP-PLUGINS.md) ·
[Installationsanfragen](docs/APP-PLUGINS.md#install-requests) ·
[App-Berechtigungen](docs/APP-PLUGINS.md#app-permissions) ·
[Standard-Apps](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail) ·
[Eine App schreiben](docs/PLUGIN-KIT.md) ·
[Schnittstellenvertrag für Apps](docs/APP-PLUGINS-API.md)

**Sprache & Layout** -
[Ein Sprachpaket schreiben](docs/PLUGIN-KIT.md#writing-a-language-pack) ·
[Sprachen von rechts nach links](docs/RTL.md)

**Speicher & Zugriff** - [Speicher](docs/STORAGE.md) · [Speicher-Plug-ins](docs/PLUGINS.md) ·
[Nutzung & Kosten](docs/USAGE.md) · [Uploads & Fortsetzen](docs/UPLOADS.md) ·
[Kontingente](docs/QUOTAS.md) · [SSO (OIDC)](docs/SSO.md) ·
[LDAP & Proxy-Authentifizierung](docs/LDAP.md) ·
[Windows- & Linux-Konten](docs/OS-LOGIN.md) ·
[Begrenzung der Anmeldeversuche & vertrauenswürdige
Proxys](docs/CONFIGURATION.md#sign-in-attempt-limits) ·
[RBAC, Ordnerzugriff & API-Schlüssel](docs/RBAC.md) ·
[Rollen & Berechtigungen pro Benutzer](docs/PERMISSIONS.md) · [Gruppen](docs/GROUPS.md) ·
[Mandantenfähigkeit & Realms](docs/MULTI-TENANCY.md) ·
[Selbstverwaltung der Mandanten](docs/TENANT-ADMIN.md)

**Daten & Funktionen** - [Freigaben & Dateianforderungen](docs/SHARING.md) ·
[ShareX](docs/SHAREX.md) ·
[Papierkorb & Versionierung](docs/TRASH-VERSIONING.md) · [Schutz](docs/PROTECTION.md) ·
[Archive](docs/ARCHIVES.md) ·
[E2E-Verschlüsselung](docs/E2E-ENCRYPTION.md) ·
[Wer verschlüsseln darf](docs/E2E-ENCRYPTION.md#who-may-encrypt) · [Suche](docs/SEARCH.md) ·
[Echtzeit & Anwesenheit](docs/REALTIME.md) ·
[Benachrichtigungen](docs/NOTIFICATIONS.md) · [Vorschaubilder](docs/thumbnails.md) ·
[Replikation](docs/REPLICATION.md) · [Designs & Darstellung](docs/INTEGRATION.md#themes)

**Betreiben & erweitern** - [Bereitstellung](docs/DEPLOYMENT.md) · [Docker](docs/DOCKER.md) ·
[Metriken](docs/METRICS.md) · [Architektur](docs/ARCHITECTURE.md) ·
[Backend-API-Spezifikation](docs/BACKEND.md) ·
[OpenAPI 3.1 (`/api/files`, `/api/ai`)](backend/internal/api/openapi.json) ·
[Komponenten-API](docs/API.md) · [ONLYOFFICE](docs/ONLYOFFICE.md) ·
[CSV in ONLYOFFICE](docs/ONLYOFFICE.md#csv-files) ·
[Anfragen von anderen Ursprüngen](docs/CONFIGURATION.md#requests-from-other-origins)

[Vollständiges Dokumentationsverzeichnis](docs/README.md)

## Entwicklung

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
  Skripte, die `pnpm shots` ausführt, um jeden oben gezeigten Screenshot neu aufzunehmen
- `docker/` - Dockerfiles + compose
- `deploy/` - fertige Compose-Stacks + Helm-Chart (siehe [`deploy/`](deploy/))
- `docs/` - Markdown-Dokumentation
- `docs-site/` - VitePress-Website, veröffentlicht unter [docs.filex.sh](https://docs.filex.sh)

Beiträge sind willkommen - siehe [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md).

## Lizenz

MIT - siehe [LICENSE](LICENSE).

Die Store-Badges in [`docs/badges/`](docs/badges/) sind die eigenen Grafiken der Stores,
werden unverändert verwendet und fallen nicht unter diese Lizenz: Microsoft und das
Microsoft-Store-Badge sind Marken der Microsoft-Unternehmensgruppe; das Snap-Store-Badge
ist © Canonical Ltd., lizenziert unter
[CC BY-ND 2.0 UK](https://creativecommons.org/licenses/by-nd/2.0/uk/).
