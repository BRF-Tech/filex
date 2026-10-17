<!-- Translated from README.md as of v0.56.0 (the shortened README of #218). The English README is the source: change it first, then carry the change here. -->

<div align="center">

<img src="docs/logo.png" alt="Logo filex" width="96">

# filex

**Gestionnaire et partage de fichiers auto-hébergé, avec une interface web, en un seul binaire Go - une alternative open source à Nextcloud, Dropbox et Google Drive.**<br>
Explorateur de fichiers web pour les stockages locaux, S3, SFTP, WebDAV, FTP et SMB : un cloud privé servi en S3, SFTP, NFS et WebDAV.<br>
SSO/LDAP, multilocataire, dossiers chiffrés, composant web intégrable, application de bureau, serveur MCP.

[![Essayer la démo en direct](https://img.shields.io/badge/Try_the_live_demo-f59e0b?style=for-the-badge)](https://demo.filex.sh)
[![Démarrage rapide](https://img.shields.io/badge/Quick_start-2f6ceb?style=for-the-badge)](#démarrage-rapide)
[![Documentation](https://img.shields.io/badge/Documentation-374151?style=for-the-badge)](https://docs.filex.sh)

[![Release](https://img.shields.io/github/v/release/BRF-Tech/filex?color=2f6ceb)](https://github.com/BRF-Tech/filex/releases)
[![License: MIT](https://img.shields.io/github/license/BRF-Tech/filex?color=22c55e)](LICENSE)

[English](README.md) · [Türkçe](README.tr.md) · [Deutsch](README.de.md) · [Español](README.es.md) · **Français** · [简体中文](README.zh-CN.md)

<sub>Cette page est une traduction du [README anglais](README.md), dans son état de la v0.56.0. En cas de divergence, le texte anglais fait foi. Traduite automatiquement, elle attend la relecture d’un locuteur natif - les corrections sont les bienvenues. Les documents vers lesquels elle renvoie sont en anglais.</sub>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://filex.sh/shots/explorer-grid-dark.ff7a02b49f27.png">
  <img src="https://filex.sh/shots/explorer-grid-light.475b0b61faab.png" alt="Explorateur filex - grille de miniatures" width="900">
</picture>

</div>

<table>
<tr>
<td width="50%" valign="top">

**🗂️ Tous les stockages, une seule arborescence**

Montez côte à côte ce que vous avez déjà, et copiez ou coupez dans un stockage pour coller dans un autre. Pointez `rclone`, `restic`, WinSCP ou FileZilla vers la même arborescence.

[Stockage et protocoles →](#stockage-et-protocoles)

</td>
<td width="50%" valign="top">

**🔗 Partager et protéger**

Des liens avec PIN, expiration et limite de téléchargements. Corbeille, historique des versions et dossiers chiffrés de bout en bout.

[Partage et protection →](#partage-et-protection)

</td>
</tr>
<tr>
<td width="50%" valign="top">

**🔑 Vos comptes, vos rôles**

OIDC, LDAP / Active Directory ou les comptes Windows et Linux du serveur. Rôles, groupes, multilocataire.

[Personnes et accès →](#personnes-et-accès)

</td>
<td width="50%" valign="top">

**💻 Une application de bureau à synchronisation de dossiers en direct**

Windows, Linux et macOS (Apple Silicon) : une modification d’un côté arrive de l’autre en une seconde environ.

[Application de bureau et CLI →](#application-de-bureau-et-cli)

</td>
</tr>
<tr>
<td width="50%" valign="top">

**🧩 S’intègre à votre produit**

La même interface en composant Vue 3, en composant React et en composant web `<filex-explorer>`.

[Intégrer à votre application →](#intégrer-à-votre-application)

</td>
<td width="50%" valign="top">

**🤖 Applications et agents IA**

Des applications en bac à sable - Signature électronique, Convertir, draw.io - et un serveur MCP intégré qu’un agent peut piloter.

[Applications →](#applications) · [Agents IA / MCP →](#agents-ia--mcp)

</td>
</tr>
</table>

<p align="center"><a href="#pourquoi-filex">Pourquoi filex</a> · <a href="#si-vous-venez-de-nextcloud-de-dropbox-ou-de-google-drive">Si vous venez d’un autre outil</a> · <a href="#auto-héberger-avec-compose-ou-helm">Auto-hébergement</a> · <a href="#fonctionnalités">Fonctionnalités</a></p>

<br>

# Démarrage rapide

> [!TIP]
> **Regardez avant d’installer :** [demo.filex.sh](https://demo.filex.sh) - connectez-vous avec
> `demo@demo.com` / `demo` (rôle d’administrateur, bac à sable réinitialisé chaque nuit).

Lancez votre propre instance, depuis le dossier que vous voulez servir :

```bash
docker run -p 5212:5212 \
  -e FILEX_DEFAULT_STORAGE_DRIVER=local -e FILEX_DEFAULT_STORAGE_PATH=/srv/files \
  -v filex-data:/data -v "$PWD:/srv/files" \
  ghcr.io/brf-tech/filex:latest
```

Ouvrez http://localhost:5212/admin - au premier lancement, les identifiants de
l’administrateur et les instructions d’intégration s’affichent dans la console. Cette URL
est celle de l’opérateur ; les personnes à qui vous donnez un compte reçoivent
**http://localhost:5212/drive**, le même gestionnaire de fichiers sans le panneau autour.

<details>
<summary><b>Ce que monte cette commande, et sous quel utilisateur elle s’exécute</b></summary>

Cette commande sert **le dossier dans lequel vous l’avez lancée** - ouvrez l’interface et
vos fichiers y sont déjà. `/data` est le répertoire propre à filex (base de données
SQLite, index de recherche, cache des miniatures), raison pour laquelle c’est un volume
nommé et non le dossier où vous déposez vos fichiers ; les deux sont séparés à dessein.
Pointez `$PWD` ailleurs, ou ajoutez d’autres stockages plus tard depuis le panneau
d’administration - un bucket qui contient plusieurs dossiers de premier niveau peut être
monté en une seule fois, à raison d’un stockage par dossier
(*Stockages → Ajouter un stockage → Monter plusieurs dossiers à la fois*).

Le conteneur s’exécute par défaut en tant que **root**, et ce qu’il écrit dans `/data`
appartient donc à root ; définissez `PUID`/`PGID` pour l’exécuter sous votre propre compte
([docs/DOCKER.md](docs/DOCKER.md#which-user-the-container-runs-as)).

</details>

<details>
<summary><b>Pas de Docker ? Lancez le binaire</b></summary>

Téléchargez l’archive de votre plateforme depuis la
[dernière version](https://github.com/BRF-Tech/filex/releases/latest), extrayez-la et
lancez le binaire ([docs/INSTALLATION.md](docs/INSTALLATION.md#binary)) :

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

Pour un vrai déploiement, il existe des stacks Compose et un chart Helm :
[Auto-héberger avec Compose ou Helm](#auto-héberger-avec-compose-ou-helm). L’image est sur
[GitHub Packages](https://github.com/BRF-Tech/filex/pkgs/container/filex).

<br>

# Pourquoi filex

La plupart des gestionnaires de fichiers auto-hébergés sont soit **trop petits** (une liste
de répertoire avec téléversement), soit **trop gros** (une suite collaborative que l’on
déploie pour son onglet de fichiers). filex vise l’entre-deux. La visite ci-dessous compte
huit parties, chacune avec ses captures d’écran repliées en dessous. Chaque fonctionnalité
en entier, avec ses limites et le guide qui la documente, se trouve dans
[docs/FEATURES.md](docs/FEATURES.md).

<br>

## L’explorateur

**Un gestionnaire de fichiers que tout le monde sait déjà utiliser.**

- Un panneau de navigation avec **Accueil · Mes fichiers · Partagés avec moi · Mes partages ·
  Récents · Favoris · Brouillons · Corbeille** ; des vues liste, grille et galerie ; des
  onglets, des étiquettes et une palette ⌘K.
- Les avatars de présence et les modifications de fichiers arrivent en direct, par WebSocket.
- Composez un thème à vos couleurs et faites-en le thème par défaut : la page de connexion et
  chaque lien public le portent aussi, et un téléphone installe l’application web sous le nom
  et le logo de votre instance.
- L’anglais et le turc sont intégrés, les autres langues s’installent sous forme de packs, et
  l’interface passe de droite à gauche pour les langues qui se lisent dans ce sens.

Toutes les fonctionnalités de l’explorateur en détail : [docs/FEATURES.md → The explorer](docs/FEATURES.md#the-explorer).

<p align="center"><img src="https://filex.sh/shots/driveshell/driveshell-hero-1440.6410104e072c.png" alt="L’interface commune de filex" width="860"></p>

<details>
<summary><b>Captures d’écran de l’explorateur</b> - l’interface commune, la recherche, la navigation, les étiquettes, la corbeille, la cloche, les thèmes, les liens symboliques, le CSV dans ONLYOFFICE</summary>

<table>
<tr>
<td width="50%" valign="top">

![L’interface commune de filex](https://filex.sh/shots/driveshell/driveshell-hero-1440.6410104e072c.png)

<sub>L’interface commune - là où tout le monde arrive</sub>

</td>
<td width="50%" valign="top">

![Recherche dans un dossier](https://filex.sh/shots/driveshell/driveshell-search-1440.2536c373425f.png)

<sub>Recherche dans ce dossier ; `⌘K` / `Ctrl K` transmet la requête à la palette</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Panneau de navigation](https://filex.sh/shots/sidenav/sidenav-expanded-1440.e96ea7e98388.png)

<sub>Panneau de navigation - Accueil, Partagés avec moi, Mes partages, Récents, Favoris, Corbeille, et les stockages auxquels vous avez accès</sub>

</td>
<td width="50%" valign="top">

![Réduit à une barre d’icônes](https://filex.sh/shots/sidenav/sidenav-rail-1440.9269e026d29a.png)

<sub>Réduit à la barre d’icônes</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Étiquettes personnelles et d’équipe](https://filex.sh/shots/tags/tags-kinds-1440.a4504c43e5cb.png)

<sub>Étiquettes - les vôtres, ou celles de votre équipe ; une étiquette ouvre tous les fichiers qui la portent, quel que soit le dossier où ils se trouvent</sub>

</td>
<td width="50%" valign="top">

![La vue Corbeille](https://filex.sh/shots/sidenav/view-trash-1440.40f8904b147f.png)

<sub>Corbeille - ce qui a été supprimé, d’où cela venait, et combien de temps il reste avant que cela ne disparaisse</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![La cloche ouverte, avec son badge de notifications non lues](https://filex.sh/shots/signing/bell-badge-1440.e9232b261511.png)

<sub>La cloche - le nombre de notifications non lues affiché dessus, chaque ligne menant là où elle le dit</sub>

</td>
<td width="50%" valign="top">

![La liste complète des notifications par-dessus l’explorateur](https://filex.sh/shots/signing/notifications-list-1440.669ce56b62d7.png)

<sub>Toutes vos notifications, dans l’explorateur - pour tout le monde, pas seulement pour les administrateurs</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![L’éditeur de thèmes](https://filex.sh/shots/appearance/theme-editor-1440.867a94807e72.png)

<sub>Apparence - composez un thème à vos couleurs, prévisualisé au fil de la saisie</sub>

</td>
<td width="50%" valign="top">

![L’explorateur portant le thème de l’opérateur](https://filex.sh/shots/appearance/themed-explorer-1440.abc278616daa.png)

<sub>Défini comme thème par défaut, c’est ce que porte l’explorateur de tout le monde…</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![La page de connexion portant le thème de l’opérateur](https://filex.sh/shots/appearance/themed-signin-1440.b891c6431444.png)

<sub>…et la page de connexion, avant que quiconque ne se soit connecté</sub>

</td>
<td width="50%" valign="top">

![Un lien symbolique qui sort du stockage, signalé par un badge](https://filex.sh/shots/symlinks/symlink-badge-1440.82a105d4239e.png)

<sub>Un lien symbolique que filex ne suit pas le dit - dans la liste, et en toutes lettres dans ses détails</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Un CSV à points-virgules ouvert dans le tableur d’ONLYOFFICE](https://filex.sh/shots/csvoffice/csv-view-1440.83237ba55d3d.png)

<sub>Un `.csv` s’ouvre dans le tableur d’ONLYOFFICE quand celui-ci est connecté - un coup d’œil d’abord, et pas de boîte de dialogue pour le délimiteur : le séparateur propre au fichier est transmis ([fichiers CSV](docs/ONLYOFFICE.md#csv-files))</sub>

</td>
<td width="50%" valign="top">

![Le CSV dans l’éditeur d’ONLYOFFICE, avec la note sur ce que conserve un enregistrement](https://filex.sh/shots/csvoffice/csv-edit-1440.4efc379a293d.png)

<sub>…et son éditeur, qui dit ce que conserve un enregistrement en CSV ; le fichier revient en CSV du même type, et les cellules que personne n’a modifiées gardent leur texte - `007` reste `007` ([cellules que personne n’a modifiées](docs/ONLYOFFICE.md#cells-nobody-changed-keep-their-text))</sub>

</td>
</tr>
</table>

</details>

<br>

## Stockage et protocoles

**Tous les stockages dans une seule arborescence, et cette arborescence accessible depuis n’importe quoi.**

- Montez côte à côte des disques locaux, S3, FTP, SFTP, WebDAV et des partages SMB/NAS, et
  copiez ou coupez dans l’un pour coller dans un autre.
- Accédez à la même arborescence en **S3**, **SFTP**, **FTPS**, **NFSv3** et **WebDAV** :
  pointez `rclone`, `restic`, `aws s3`, WinSCP ou FileZilla vers elle. Les mêmes autorisations
  filex (par personne, groupe et dossier), la même corbeille et le même quota s’appliquent
  comme dans l’interface web ; les bits POSIX et les ACL du disque ne sont pas associés
  aux utilisateurs filex.
- `filex mount` attache un serveur distant en HTTPS ordinaire : un dossier sous Linux, une
  lettre de lecteur sous Windows (macOS n’est pas pris en charge).
- Pour un stockage que filex ne livre pas, installez une extension depuis le panneau
  d’administration ou depuis le lien d’une boutique d’applications.

Toutes les fonctionnalités de stockage et de protocoles en détail : [docs/FEATURES.md → Storage and protocols](docs/FEATURES.md#storage-and-protocols).

<p align="center"><img src="https://filex.sh/shots/sidenav/connect-1440.45536274e7dc.png" alt="Guide de connexion" width="860"></p>

<details>
<summary><b>Captures d’écran du stockage et des protocoles</b> - les guides de connexion, les clés d’API et une extension de stockage</summary>

<table>
<tr>
<td width="50%" valign="top">

![Guide de connexion](https://filex.sh/shots/sidenav/connect-1440.45536274e7dc.png)

<sub>Guide de connexion - les guides, construits à partir de *votre* déploiement</sub>

</td>
<td width="50%" valign="top">

![Clés d’API](https://filex.sh/shots/sidenav/apikeys-minted-1440.3464cb59b2f8.png)

<sub>Clés d’API - créez les vôtres, dans l’explorateur ou dans une intégration (la session ou la clé d’une personne ; une intégration relayée par un proxy avec une seule clé partagée de type *app* n’a pas cette entrée)</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Guide de connexion](https://filex.sh/shots/connections-guide.3999cf32c3d9.png)

<sub>Accéder à filex depuis n’importe quoi - S3, SFTP, FTPS, NFS, WebDAV. Chaque commande est construite à partir de *votre* déploiement</sub>

</td>
<td width="50%" valign="top">

![Extensions](https://filex.sh/shots/admin-plugins.6bd96bf65745.png)

<sub>Un stockage qui n’est pas livré avec filex - installé comme extension dans **Extensions → Extensions de stockage**, il décrit son propre formulaire de configuration</sub>

</td>
</tr>
</table>

</details>

<br>

## Partage et protection

**Partagez avec un lien que vous pouvez limiter et révoquer. Revenez sur une mauvaise suppression ou un mauvais écrasement.**

- Des liens publics avec PIN, expiration et limite de téléchargements, et des demandes de
  fichiers pour ce qui doit arriver. **Mes partages** liste chaque lien que vous avez créé,
  pour le copier ou le révoquer.
- Partagez un dossier avec une personne ou un groupe et il apparaît dans ses **Partagés avec
  moi**. Le stockage doit avoir **Contrôle d’accès par élément (RBAC)** activé, ce qui n’est
  pas le cas par défaut.
- Les suppressions sont réversibles pendant une durée de conservation et les écritures
  gardent des instantanés, les deux à l’intérieur du stockage que vous avez déjà monté.
- Dossiers chiffrés de bout en bout : chiffrés dans le navigateur, et le serveur ne reçoit
  jamais de clé. Une application que vous autorisez modifie un tel fichier dans votre
  navigateur, qui le déchiffre pour elle et chiffre ce qu’elle enregistre. Analyse ClamAV
  facultative de chaque fichier écrit.

Toutes les fonctionnalités de partage et de protection en détail : [docs/FEATURES.md → Sharing and protection](docs/FEATURES.md#sharing-and-protection).

<p align="center"><img src="https://filex.sh/shots/public-share.0e3ba07f7c88.png" alt="Un lien de partage public, tel que le voit son destinataire" width="860"></p>

<details>
<summary><b>Captures d’écran du partage et de la protection</b> - la boîte de dialogue de partage, ce que voit le destinataire, Mes partages, qui peut chiffrer</summary>

<table>
<tr>
<td width="50%" valign="top">

![Boîte de dialogue de partage](https://filex.sh/shots/share-modal.87cb16a1a6f0.png)

<sub>Partage - PIN, expiration, limite de téléchargements, `curl` en une ligne</sub>

</td>
<td width="50%" valign="top">

![Visionneuse Markdown](https://filex.sh/shots/viewer-markdown.1789ecdcfbc5.png)

<sub>Visionneuse Markdown</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Un lien de partage public, tel que le voit son destinataire](https://filex.sh/shots/public-share.0e3ba07f7c88.png)

<sub>…et ce qu’ouvre la personne à l’autre bout. filex n’a qu’UN écran tourné vers l’extérieur - un fichier partagé, un dossier, une demande de fichiers, la page de signature d’une application et le PIN placé devant n’importe lequel d’entre eux sont tous cette même page, au nom de votre instance</sub>

</td>
<td width="50%" valign="top">

![Mes partages, le menu Actions d’une ligne ouvert](https://filex.sh/shots/signing/my-shares-1440.6f8676cbb555.png)

<sub>Mes partages - les liens que vous avez créés, et leurs PIN quand vous devez en transmettre un</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Admin → Partages, le menu Actions d’une ligne ouvert](https://filex.sh/shots/signing/admin-table-actions-1440.0fffdc146eb1.png)

<sub>Chaque tableau d’administration - un menu **Actions** épinglé par ligne, le même menu qu’ouvre le ⋮ de l’explorateur</sub>

</td>
<td width="50%" valign="top">

![Admin → Chiffrement : la politique d’approbation et trois demandes en attente](https://filex.sh/shots/encryption/admin-encryption-1440.35606d99b816.png)

<sub>Qui peut chiffrer - désactivé, administrateurs uniquement, toutes les personnes dont le rôle le permet, ou après l’approbation d’un administrateur ; les demandes en attente, avec leur auteur et leur motif ([qui peut chiffrer](docs/E2E-ENCRYPTION.md#who-may-encrypt))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![La demande d’un dossier chiffré, avec son motif](https://filex.sh/shots/encryption/request-new-folder.e74894fba30f.png)

<sub>…et du côté de la personne : la demande qu’ouvre la boîte de dialogue Nouveau dossier - un seul nouveau dossier chiffré, demandé à un administrateur avec un motif</sub>

</td>
</tr>
</table>

</details>

<br>

## Personnes et accès

**Les comptes que les gens ont déjà, et un rôle chacun.**

- Connexion avec un mot de passe local, OIDC, LDAP / Active Directory, un proxy
  d’authentification, ou le compte Windows ou Linux de la machine où filex s’exécute.
- Chacun a un seul rôle - Administrateur, Utilisateur, Lecteur ou l’un des vôtres - construit
  à partir d’autorisations nommées. Les groupes portent un accès aux dossiers et un rôle,
  peuvent suivre les groupes de votre annuaire, et les exceptions par personne l’emportent
  sur le rôle.
- Deviner un mot de passe prend du temps : les mots de passe erronés sont comptés par compte
  et par adresse, sur le formulaire web comme sur WebDAV, FTPS et SFTP.
- Multilocataire par conception, derrière un seul interrupteur : chaque locataire a ses
  propres fournisseurs de connexion, son propre domaine et son propre certificat, et
  s’administre lui-même. Désactivé, aucun écran ne montre de locataire.

Toutes les fonctionnalités pour les personnes et l’accès en détail : [docs/FEATURES.md → People and access](docs/FEATURES.md#people-and-access).

<p align="center"><img src="https://filex.sh/shots/roles/roles-list-1440.6bb0779b66e3.png" alt="Admin → Rôles : les rôles intégrés et deux rôles personnalisés" width="860"></p>

<details>
<summary><b>Captures d’écran des personnes et de l’accès</b> - les rôles, les groupes, la sécurité de connexion, un compte verrouillé, le panneau d’administration et son menu</summary>

<table>
<tr>
<td width="50%" valign="top">

![Admin → Rôles : les rôles intégrés et deux rôles personnalisés](https://filex.sh/shots/roles/roles-list-1440.6bb0779b66e3.png)

<sub>Rôles - Administrateur, Utilisateur, Lecteur et vos propres rôles : qui détient chacun d’eux, ce qu’il autorise, où il diffère selon le dossier, ses limites ([docs/PERMISSIONS.md](docs/PERMISSIONS.md))</sub>

</td>
<td width="50%" valign="top">

![Admin → Groupes](https://filex.sh/shots/groups/groups-list-1440.22ff57b029f5.png)

<sub>Groupes - des ensembles nommés de personnes, dotés d’un accès aux dossiers et d’un rôle ; des membres gérés à la main ou gardés synchronisés avec les groupes que transmet une connexion ([docs/GROUPS.md](docs/GROUPS.md))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Partage d’un dossier avec un groupe](https://filex.sh/shots/groups/share-group-1440.31c2a81e1eeb.png)

<sub>Partage d’un dossier avec un groupe, à côté des personnes - le niveau Propriétaire est soumis à confirmation dans la boîte de dialogue, pas accordé d’un clic</sub>

</td>
<td width="50%" valign="top">

![Admin → Sécurité de connexion](https://filex.sh/shots/loginsecurity/login-security-1440.70f2a2a76257.png)

<sub>Sécurité de connexion - la limite de tentatives et les adresses autorisées, jamais comptées ; les proxys de confiance, les verrouillages et le journal des connexions suivent sur la même page ([limites de tentatives de connexion](docs/CONFIGURATION.md#sign-in-attempt-limits))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Le formulaire de connexion d’un compte verrouillé](https://filex.sh/shots/loginsecurity/login-locked-1440.42e1fe6f1937.png)

<sub>…et ce que dit le formulaire de connexion d’un compte verrouillé, avec le compte à rebours du verrouillage sur son bouton</sub>

</td>
<td width="50%" valign="top">

![Tableau de bord d’administration](https://filex.sh/shots/admin-dashboard.233358b87c3e.png)

<sub>Panneau d’administration</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Page d’accueil de la démo](https://filex.sh/shots/demo-landing.64f23c6f8cb3.png)

<sub>Page d’accueil de la démo</sub>

</td>
<td width="50%" valign="top">

![Le panneau Paramètres du menu d’administration, ouvert par-dessus Admin → Paramètres → Général](https://filex.sh/shots/megamenu/settings-panel-1440.23a710915f9c.png)

<sub>Le menu d’administration - toutes les pages en quatre panneaux, **Fichiers et stockage**, **Utilisateurs**, **Paramètres** (Général, Sécurité et données, Traitement des fichiers et intégrations) et **Maintenance et journaux**, avec une courte ligne sous chacune ; un téléphone affiche les mêmes pages dans un tiroir ([docs/ADMIN-PANEL.md](docs/ADMIN-PANEL.md))</sub>

</td>
</tr>
</table>

</details>

<br>

## Application de bureau et CLI

**Les dossiers de votre ordinateur restent synchronisés avec le serveur, en direct.**

- Une application Windows, Linux et macOS (Apple Silicon) : le même explorateur dans une
  fenêtre, avec plusieurs comptes ou locataires côte à côte.
- Clic droit sur un dossier → **Conserver sur cet ordinateur**, et il reste synchronisé avec
  le serveur dans les deux sens, en une seconde environ. Tout le reste demeure en ligne
  uniquement dans la fenêtre.
- Elle se connecte par votre navigateur, donc le SSO et la MFA se comportent comme sur le
  web, et elle ouvre les documents Office de votre propre disque dans l’éditeur que fait
  tourner votre serveur.
- Les machines sans interface graphique disposent du même moteur : `filex sync` et
  `filex client`.

**Installez-la** depuis le Microsoft Store (Windows 10/11) ou le Snap Store (Ubuntu et les
autres distributions Linux avec snapd) :

<p>
<a href="https://apps.microsoft.com/detail/9PKXDJLVZWXW"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/badges/ms-store-light.svg"><img src="docs/badges/ms-store-dark.svg" alt="Télécharger depuis le Microsoft Store" height="52"></picture></a>&nbsp;&nbsp;&nbsp;
<a href="https://snapcraft.io/filex-app"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/badges/snap-store-white.svg"><img src="docs/badges/snap-store-black.svg" alt="Disponible sur le Snap Store" height="52"></picture></a>
</p>

ou avec un gestionnaire de paquets :

```bash
brew install brf-tech/filex/filex-app       # macOS 13+ (Apple Silicon), Homebrew tap BRF-Tech/homebrew-filex
sudo snap install filex-app                 # the same snap as the badge above
```

<details>
<summary><b>Plus de détails sur l’installation, et la CLI</b> - la version du Store, winget, les fichiers joints à la version, Linux, arm64, les commandes du client</summary>

La version du Store (*filex File Manager*) est la seule copie Windows dont le code est
signé - c’est Microsoft qui la signe - et le Store la tient à jour. Le paquet winget de
l’application de bureau (`BRFTech.filex-app`) attend son premier examen par les modérateurs
de winget - tant qu’il n’est pas accepté, une version laisse la soumission telle quelle ; `winget install BRFTech.filex-app` ne le
trouve donc pas encore. Le programme
d’installation, le `.exe` portable, l’AppImage, le `.deb`, le `.rpm` et le `.dmg` sont
joints à la [dernière version](https://github.com/BRF-Tech/filex/releases/latest) - pas
encore signés, attendez-vous donc à une invite SmartScreen avec le programme
d’installation Windows. Détails : [docs/DESKTOP.md](docs/DESKTOP.md). La CLI seule :
`brew install brf-tech/filex/filex`, ou `winget install BRFTech.filex` sous Windows
([docs/CLI.md](docs/CLI.md)).

Sous Linux, le `.deb` et le `.rpm` n’ont besoin de rien ; à partir d’Ubuntu 23.10,
l’AppImage a besoin d’un profil AppArmor à installer une seule fois
([AppImage on recent Ubuntu](docs/DESKTOP.md#appimage-on-recent-ubuntu)), et le Snap
s’exécute dans le confinement strict du snap
([The snap and the sandbox](docs/DESKTOP.md#the-snap-and-the-sandbox)). Chaque version est
aussi compilée pour arm64 : le serveur, les images, et l’application de bureau sous Linux,
Windows on Arm et Apple Silicon
([docs/INSTALLATION.md → ARM (arm64)](docs/INSTALLATION.md#arm-arm64)). Tout ce que fait
l’application : [docs/FEATURES.md → Desktop app, phone and CLI](docs/FEATURES.md#desktop-app-phone-and-cli).

Le même binaire est aussi un client pour les serveurs, les scripts et les machines sans
interface graphique :

```bash
filex client login --url https://files.example.com
filex client upload build/report.pdf docs://ci-artifacts/
filex client mv docs://ci-artifacts/report.pdf archive://2026/   # across storages, waits for the job
filex client run convert convert docs://data/table.csv --param target=xlsx

filex sync add ~/Documents/work docs://work   # the engine the desktop app uses
filex sync run --watch 30s
```

Voir [docs/CLI.md](docs/CLI.md) et [docs/SYNC.md](docs/SYNC.md).

</details>

<br>

## Intégrer à votre application

**Un vrai gestionnaire de fichiers dans votre propre produit.**

La même interface est livrée sous forme de composant Vue 3, de composant React et de
composant web `<filex-explorer>` indépendant de tout framework, adossée à votre propre
serveur filex. Une clé confinée la verrouille sur un dossier par locataire, et c’est le
backend qui l’impose, pas le composant. Le composant web tient en deux lignes :

```html
<script type="module" src="https://cdn.jsdelivr.net/npm/@brftech/filex/dist/filex.js"></script>
<filex-explorer api-base="http://localhost:5212" sidenav connections ui-profile="simple"></filex-explorer>
```

<p align="center"><img src="https://filex.sh/shots/sidenav/embed-webcomponent-1440.b802d5b6ab56.png" alt="Composant web intégré" width="860"></p>

<details>
<summary><b>Vue 3 et React</b> - les deux autres composants, et où trouver les détails</summary>

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

Les styles, les bundlers, les attributs `sidenav` / `connections` / `ui-profile`, les clés
confinées pour les hôtes multilocataires, un cookie de session venant d’une autre origine
et l’impression depuis une application : [docs/INTEGRATION.md](docs/INTEGRATION.md), résumé
dans [docs/FEATURES.md → Embedding](docs/FEATURES.md#embedding). ⚠ Gardez les paquets et le
serveur à la même version ([docs/API.md](docs/API.md)).

<table>
<tr>
<td width="50%" valign="top">

![Partagés avec moi](https://filex.sh/shots/sidenav/view-shared-1440.faf6f6668c20.png)

<sub>Partagés avec moi - les dossiers auxquels d’autres personnes vous ont accordé l’accès, sans instructions de montage</sub>

</td>
<td width="50%" valign="top">

![Composant web intégré](https://filex.sh/shots/sidenav/embed-webcomponent-1440.b802d5b6ab56.png)

<sub>Intégré à la page d’un autre produit</sub>

</td>
</tr>
</table>

</details>

<br>

## Applications

**De nouvelles choses à faire avec les fichiers, avec exactement les autorisations que vous avez approuvées.**

Une application est un module WebAssembly qui s’exécute dans filex, dans un bac à sable, une
interface qui lui est propre dans un cadre en bac à sable, ou les deux. Vous l’installez
depuis le lien d’une boutique ou depuis son adresse GitHub, vous lisez chaque autorisation
qu’elle demande, et rien ne se met à jour tout seul. Les personnes de votre filex peuvent
parcourir une boutique de confiance sur son écran **Boutique d’applications** et vous
demander une application ou une extension de stockage. Cinq sont livrées sous forme de
dépôts publics :

| Application | Ce qu’elle ajoute |
|---|---|
| [Signature électronique](https://github.com/BRF-Tech/filex-sign) | Signez un PDF, ou demandez à d’autres de le faire : les personnes de ce filex y signent, n’importe qui d’autre par un lien privé, protégé par défaut par un PIN |
| [Convertir](https://github.com/BRF-Tech/filex-convert) | **Convertir…** sur n’importe quel fichier ou n’importe quelle sélection : images, vidéo, audio, documents, livres numériques, archives, données, sous-titres et polices |
| [Office editor](https://github.com/BRF-Tech/filex-office-editor) | Les documents Word, Excel et PowerPoint dans l’éditeur d’ONLYOFFICE, dans le navigateur et sans Document Server ; ses versions 0.2.0 et 0.3.0, faites pour filex 0.56, ajoutent les dossiers chiffrés et l’édition à plusieurs en même temps |
| [filextext](https://github.com/BRF-Tech/filextext-app) | Un espace de travail texte chiffré de bout en bout dans un seul fichier `.fxtxt` |
| [draw.io](https://github.com/BRF-Tech/filex-drawio) | L’éditeur de diagrammes draw.io, sur les fichiers `.drawio` et `.dio` |

Une application que vous autorisez peut modifier un fichier **à plusieurs** - le relais de
filex met les modifications de chacun dans un seul ordre sans les lire, et qui ne peut que lire
suit en direct - et ouvrir un fichier d’un **dossier chiffré**, que votre navigateur déchiffre
pour elle et chiffre de nouveau avant qu’il soit enregistré.

Un pack de langue est aussi une application : l’espagnol, l’allemand et le français sont
livrés à titre d’exemple.

Installez-en une en quelques minutes : [docs/APP-PLUGINS.md → Install one](docs/APP-PLUGINS.md#install-one). Toutes les fonctionnalités des applications en détail : [docs/FEATURES.md → Apps](docs/FEATURES.md#apps).

<p align="center"><img src="https://filex.sh/shots/signing/sign-place-1440.696a10b65be8.png" alt="Placement des zones sur le document" width="860"></p>

<details>
<summary><b>Captures d’écran des applications</b> - une demande de signature du début à la fin, l’installation d’une application, le convertisseur, une application dotée de sa propre interface, la modification à plusieurs, le fichier d’un dossier chiffré dans une application, un pack de langue, les Applications par défaut, l’installation depuis une boutique</summary>

Dana demande à un collègue du même filex et à un partenaire extérieur de signer un accord.
L’application est [Signature électronique](https://github.com/BRF-Tech/filex-sign) ;
chaque écran est affiché par filex, et le lien que reçoit le partenaire est un
partage ordinaire.

<table>
<tr>
<td width="50%" valign="top">

![Définition des zones d’une demande de signature](https://filex.sh/shots/signing/sign-define-1440.63ea72b13724.png)

<sub>Définissez les zones - nommez chacune et dites à qui elle appartient ; le document vient ensuite</sub>

</td>
<td width="50%" valign="top">

![Placement des zones sur le document](https://filex.sh/shots/signing/sign-place-1440.696a10b65be8.png)

<sub>Placez-les - choisissez une zone, appuyez sur la page à l’endroit où elle doit aller</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![L’écran de saisie du PIN du signataire externe](https://filex.sh/shots/signing/sign-outside-pin-1440.2cd8ccb99483.png)

<sub>Le lien du partenaire - le seul écran public de filex, au nom de votre instance, protégé par un PIN</sub>

</td>
<td width="50%" valign="top">

![Le signataire externe remplissant ses zones](https://filex.sh/shots/signing/sign-outside-fill-1440.9f50aeb7abef.png)

<sub>…et ce qu’il ouvre : uniquement ses propres zones - ici un nom saisi au clavier dans la police choisie par le demandeur (dessiné et téléversé sont les deux autres possibilités)</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Le document verrouillé, son panneau Signatures ouvert](https://filex.sh/shots/signing/sign-status-1440.54c60e832ced.png)

<sub>Tant que la demande est en cours - le document gelé pour tout le monde et, dans ses détails, qui a signé</sub>

</td>
<td width="50%" valign="top">

![La vérification des autorisations dans l’assistant d’installation](https://filex.sh/shots/apps/apps-install-review-1440.cdb1a4ebf8f6.png)

<sub>Installation d’une application - chaque autorisation qu’elle demande, en termes simples, avant que rien ne s’exécute</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Le détail d’une application installée](https://filex.sh/shots/apps/apps-detail-1440.b7f6ec845458.png)

<sub>Une application installée - d’où elle vient, son empreinte, et chaque autorisation qu’elle détient, en termes simples (ses paramètres et ses actions suivent, plus bas dans la page)</sub>

</td>
<td width="50%" valign="top">

![L’assistant du convertisseur](https://filex.sh/shots/apps/convert-wizard-1440.231ada006fd6.png)

<sub>Le convertisseur, une autre application - chaque cible sous sa catégorie, trois étapes</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![La vérification à l’installation d’une application dotée de sa propre interface](https://filex.sh/shots/apps/app-interface-review-1440.877a89c82359.png)

<sub>Une application qui apporte sa propre interface - la vérification montre l’empreinte du paquet, chaque adresse hors du paquet (une adresse en direct est une autorisation, en jaune) et ce qu’un navigateur ne peut pas promettre</sub>

</td>
<td width="50%" valign="top">

![L’interface propre à une application, ouverte comme visionneuse d’un fichier](https://filex.sh/shots/apps/app-interface-viewer-1440.519b23618156.png)

<sub>…et cette interface ouverte sur son propre type de fichier, là où se trouverait l’aperçu de filex. Elle lit et enregistre le fichier en passant par filex, dans un cadre en bac à sable (une petite application d’exemple, écrite pour ces images)</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Deux personnes dans un document : l’une modifie, l’autre suit](https://filex.sh/shots/officeapp/coedit-watcher-1280.904b31e20f36.png)

<sub>Modifier à plusieurs - Dana tape, et Sam, qui ne peut que lire, voit arriver chaque modification et son curseur par le relais de filex ([Modifier à plusieurs](docs/APP-PLUGINS-API.md#editing-together-coedit-056) ; une application d’exemple écrite pour ces images)</sub>

</td>
<td width="50%" valign="top">

![Un fichier d’un dossier chiffré ouvert dans une application, déchiffré par le navigateur](https://filex.sh/shots/e2eapp/app-encrypted-folder-1280.856d3586a9e2.png)

<sub>Un fichier d’un dossier chiffré de bout en bout dans une application que vous avez autorisée - votre navigateur le déchiffre pour l’application et chiffre ce qu’elle enregistre, et le serveur ne garde toujours que du texte chiffré (la même application d’exemple ; [Modifier dans une application](docs/E2E-ENCRYPTION.md#editing-in-an-app))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![La liste Applications, un pack de langue parmi les applications](https://filex.sh/shots/langpack/apps-list-1440.004f0c819981.png)

<sub>Toutes les applications de l’instance, et parmi elles un **pack de langue** - un manifeste qui n’exécute rien, qui dit quelle part de ce filex il traduit et dont la langue s’en va avec lui</sub>

</td>
<td width="50%" valign="top">

![Extensions → Applications par défaut](https://filex.sh/shots/defaultapps/default-apps-1440.0dd62b519f8f.png)

<sub>Applications par défaut - chaque type de fichier pris en charge par autre chose que filex : qui l’ouvre et qui génère sa miniature, dans l’ordre que vous définissez ([Applications par défaut](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Dossiers illustrés par leurs fichiers les plus récents](https://filex.sh/shots/thumbnails/folders-grid-1440.dd7653d6edca.png)

<sub>Aperçus de dossiers - chaque dossier illustré par les trois derniers fichiers qui y sont arrivés ; les SVG sont rendus par le moteur intégré de filex ([docs/thumbnails.md](docs/thumbnails.md#folder-previews))</sub>

</td>
<td width="50%" valign="top">

![La vérification à l’installation ouverte depuis le lien d’une boutique](https://filex.sh/shots/store/store-review-1440.36cf3bbc2a50.png)

<sub>Installation depuis une boutique - la même vérification, marquée **De la boutique**, la clé de licence de l’application payante montrée par ses premiers caractères ([Installing from a store](docs/APP-PLUGINS.md#installing-from-a-store))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Le premier lien d’une boutique : lui faire confiance ?](https://filex.sh/shots/store/store-trust-1440.8fb07862db1a.png)

<sub>Le premier lien d’une boutique - son adresse et les empreintes de ses clés, à comparer avant de lui faire confiance ([Boutiques de confiance](docs/APP-PLUGINS.md#trusted-stores))</sub>

</td>
<td width="50%" valign="top">

![Une application payante retenue, le bandeau sur une page d’administration](https://filex.sh/shots/store/store-license-held-1440.6fd145640e0f.png)

<sub>Une licence révoquée par la boutique - l’application retenue, *Sans licence*, rien de supprimé, et un bandeau sur chaque page d’administration ([Applications payantes](docs/APP-PLUGINS.md#paid-apps))</sub>

</td>
</tr>
</table>

</details>

<br>

## Agents IA / MCP

**Un agent travaille dans le dossier que vous lui avez donné, et nulle part ailleurs.**

- Un **serveur MCP** natif sur `/api/ai/mcp`, et une surface REST sur `/api/ai` décrite dans
  un fichier OpenAPI 3.1.
- Une clé d’API porte des autorisations par verbe et peut être confinée à un seul dossier, et
  rien de ce qu’elle délivre ne peut être plus large que la clé elle-même.
- Un gros fichier passe par un **ticket de téléversement** : une URL de courte durée, à usage
  unique, si bien que ses octets ne transitent jamais par le contexte du modèle.

```bash
claude mcp add filex --transport http https://files.example.com/api/ai/mcp \
  --header "Authorization: Bearer <api-token>"
```

Ce qu’un agent peut faire, ce qu’une clé ne peut jamais délivrer, les dossiers chiffrés et
les tickets de téléversement : [docs/FEATURES.md → AI agents and MCP](docs/FEATURES.md#ai-agents-and-mcp) ;
la référence est [docs/MCP.md](docs/MCP.md).

<br>

# Si vous venez de Nextcloud, de Dropbox ou de Google Drive

filex est un gestionnaire de fichiers, pas une suite collaborative. Voici ce que cela veut
dire face à quatre produits dont vous venez peut-être (File Browser parmi eux) :

| Si vous venez de | Avec filex |
|---|---|
| **Nextcloud**<br>Une plateforme collaborative que vous hébergez vous-même ou prenez chez un fournisseur : des fichiers et, autour, un agenda, des contacts, la messagerie, le chat, la visioconférence et une suite bureautique en ligne. Une application PHP derrière un serveur web, avec une base de données. | La partie fichiers, seule.<br>Un binaire Go avec SQLite intégré (PostgreSQL ou MySQL quand vous les voulez).<br>Une application de bureau avec synchronisation des dossiers en direct, des liens de partage, le SSO et LDAP.<br>Les documents Office, seul ou à plusieurs, dans l’application d’édition bureautique ou dans l’ONLYOFFICE que vous connectez.<br>Pas d’agenda, de contacts, de messagerie, de chat ni de visioconférence. |
| **Dropbox**, **Google Drive**<br>Des services hébergés : vos fichiers vivent sur les serveurs du fournisseur, sous son quota et ses conditions. | Votre propre serveur et le stockage que vous avez déjà : un disque, un partage NAS, un bucket S3.<br>Le quotidien est là : une interface web, des liens de partage avec PIN et expiration, des demandes de fichiers, la corbeille et l’historique des versions.<br>Une application de bureau qui garde synchronisés les dossiers que vous choisissez. |
| **File Browser**<br>Un binaire qui pose une interface web sur un répertoire que vous lui indiquez, avec des comptes utilisateurs (chacun avec sa portée et ses options d’autorisation), des règles d’autorisation et de refus par chemin, et des liens de partage avec mot de passe et expiration. Son dépôt est archivé, et son [README](https://github.com/filebrowser/filebrowser) annonce qu’il n’y aura plus de versions, de corrections de bugs ni de correctifs de sécurité. | Le même démarrage en une commande.<br>Plusieurs stockages côte à côte.<br>Des rôles et des groupes en plus de l’accès par dossier ; l’authentification unique OIDC et LDAP intégrés.<br>Corbeille et versions, recherche plein texte.<br>La même arborescence accessible en S3, SFTP, FTPS, NFSv3 et WebDAV. |

**Ce que filex n’a pas**, d’où que vous veniez :

- **Une application Android ou iOS.** Sur un téléphone, filex est l’application web, qui
  propose de s’installer comme une application
  ([On a phone or a tablet](docs/DESKTOP.md#on-a-phone-or-a-tablet-the-web-app)) mais a
  besoin d’une connexion pour afficher les fichiers ; quand elle est fermée, les
  notifications arrivent par Web Push une fois que vous l’avez activé pour l’appareil.
- **Des fichiers fantômes (placeholders) dans le Finder ou l’Explorateur.** L’application
  de bureau copie les dossiers que vous conservez sur l’ordinateur, et tout le reste reste
  en ligne, dans sa propre fenêtre
  ([docs/DESKTOP.md](docs/DESKTOP.md#keeping-folders-on-this-computer)).
- **Un éditeur bureautique intégré au binaire.** Les documents Office se modifient - seul
  ou à plusieurs - dans l’application d’édition bureautique que vous
  installez ([`filex-office-editor`](https://github.com/BRF-Tech/filex-office-editor),
  l’éditeur d’ONLYOFFICE dans le navigateur, sans Document Server), ou dans un ONLYOFFICE
  Document Server que vous connectez ; sans l’un ni l’autre, ils s’ouvrent dans un aperçu
  en lecture seule ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md)).

<details>
<summary><b>S’installer</b> - ce qui vous suit, et ce qui ne vous suit pas</summary>

- Là où les fichiers se trouvent déjà quelque part que filex peut monter - un dossier sur
  un disque ou un partage NAS, un préfixe dans un bucket S3, un serveur SFTP, FTP ou
  WebDAV - s’installer, c’est un montage, pas une migration. filex ne stocke pas les
  fichiers lui-même : faites pointer un stockage vers ce dossier et ils sont là, tels
  quels.
- Les fichiers conservés dans Dropbox ou Google Drive doivent d’abord être copiés vers un
  tel endroit : filex ne livre de pilote de stockage pour aucun des deux.
- Seuls les fichiers déménagent : les liens, les autorisations et l’historique des
  versions que gardait l’ancien système ne sont pas importés.
- Ce que filex ajoute - la corbeille, l’historique des versions, les brouillons - se
  trouve dans des dossiers cachés à la racine du stockage, et un dossier que vous chiffrez
  de bout en bout ne contient plus que des données chiffrées à partir de ce moment
  ([docs/STORAGE.md](docs/STORAGE.md), [docs/TRASH-VERSIONING.md](docs/TRASH-VERSIONING.md)).

</details>

<br>

# Auto-héberger avec Compose ou Helm

Le `docker run` ci-dessus suffit pour essayer filex. Pour un vrai déploiement,
des stacks prêtes à l’emploi se trouvent dans [`deploy/`](deploy/) :

- **[`deploy/compose/`](deploy/compose/)** - Docker Compose :
  - **minimal** - filex + SQLite + disque local (un service, zéro dépendance).
  - **full** - filex + PostgreSQL + Redis + Caddy (HTTPS automatique), plus des services
    optionnels activables : **OnlyOffice**, **Drawio** et un **serveur S3** (Versity S3
    Gateway). Activez ou désactivez chacun avec un profil Compose dans `.env`. La
    conversion, c’est l’[application Convertir](#applications), pas un conteneur annexe.
- **[`deploy/helm/filex/`](deploy/helm/filex/)** - un chart Helm pour Kubernetes
  (Deployment + PVC + Ingress facultatif), publié à chaque version :
  `helm repo add brf-tech https://helm.brf.sh`, puis `helm install filex brf-tech/filex`
  (ou `oci://ghcr.io/brf-tech/charts/filex`). Chaque service optionnel ci-dessus est une
  option `enabled` dans `values.yaml` - embarquez PostgreSQL / Redis / un serveur S3, ou
  raccordez un OnlyOffice / Drawio externe.

Les instructions pas à pas pour chaque configuration se trouvent dans
[docs/INSTALLATION.md](docs/INSTALLATION.md).

filex fonctionne à la racine d’un hôte dédié ou sous un chemin d’un hôte qu’il partage
(`https://example.com/filex/`) : un seul paramètre, `FILEX_BASE_PATH`, et un proxy
qui transmet le chemin complet - exemples pour Caddy, nginx et Helm dans
[docs/DEPLOYMENT.md → Serving filex under a sub-path](docs/DEPLOYMENT.md#serving-filex-under-a-sub-path).

<br>

# Documentation

Les guides sont publiés sous forme de site sur [docs.filex.sh](https://docs.filex.sh), et
l’[index complet de la documentation](docs/README.md) liste chaque page.

Commencez par l’[installation](docs/INSTALLATION.md), la [configuration](docs/CONFIGURATION.md),
l’[application de bureau](docs/DESKTOP.md), les [protocoles](docs/PROTOCOLS.md), les
[applications](docs/APP-PLUGINS.md) et [ONLYOFFICE](docs/ONLYOFFICE.md).
[Fonctionnalités](docs/FEATURES.md) donne à chaque fonctionnalité une entrée, avec ses
limites et son guide.

<br>

# Fonctionnalités

| Domaine | Ce que vous obtenez |
|---|---|
| **L’explorateur** | Une même mise en page dans l’application d’administration, l’application de bureau et chaque intégration : un panneau de navigation, des vues liste, grille et galerie, des onglets, des étiquettes personnelles et d’équipe, une palette de commandes ⌘K, des raccourcis clavier reconfigurables, de nouveaux documents depuis le menu **+ Nouveau** et des thèmes composés par l’opérateur ([Intégration](docs/INTEGRATION.md), [Thèmes](docs/INTEGRATION.md#themes)) |
| **Stockage** | Disques locaux, S3, FTP, SFTP, WebDAV et SMB/NAS montés côte à côte, la copie et le déplacement de l’un à l’autre, et une extension pour tout le reste, depuis le panneau d’administration ou une boutique d’applications ([Stockage](docs/STORAGE.md), [Extensions de stockage](docs/PLUGINS.md)) |
| **Sans navigateur** | La même arborescence servie en S3, SFTP, FTPS, NFSv3 et WebDAV - un déplacement ou une suppression faits par là sont aussi sûrs, à côté d’une analyse du stockage, que dans l’interface web - plus `filex mount` en HTTPS ([Protocoles](docs/PROTOCOLS.md)) |
| **Connexion** | OIDC, LDAP / Active Directory, comptes Windows et Linux, et limites de tentatives de connexion ([SSO](docs/SSO.md), [LDAP](docs/LDAP.md), [Comptes du système](docs/OS-LOGIN.md)) |
| **Personnes et accès** | Rôles et autorisations par personne, groupes, accès par dossier et multilocataire natif ([Autorisations](docs/PERMISSIONS.md), [Groupes](docs/GROUPS.md), [Multilocataire](docs/MULTI-TENANCY.md)) |
| **Partage** | Liens publics avec PIN, expiration et limite de téléchargements, demandes de fichiers et invitations au partage par e-mail ([Partage](docs/SHARING.md)) |
| **Protection** | Corbeille et historique des versions, analyse ClamAV facultative, dossiers chiffrés de bout en bout, réplication vers un second stockage (avec une copie initiale de ce qui s’y trouve déjà) et un journal d’audit ([Corbeille et versions](docs/TRASH-VERSIONING.md), [Protection](docs/PROTECTION.md), [Chiffrement de bout en bout](docs/E2E-ENCRYPTION.md), [Réplication](docs/REPLICATION.md), [Journal d’audit](docs/BACKEND.md#admin-audit-log)) |
| **Visionneuses et éditeurs** | Images, vidéo, audio, PDF, Markdown, code (Monaco), documents Office dans l’application d’édition bureautique ou avec ONLYOFFICE, diagrammes draw.io, diagrammes Mermaid dessinés et modifiables en texte, modèles 3D, et archives ZIP, 7z et TAR ([ONLYOFFICE](docs/ONLYOFFICE.md), [Archives](docs/ARCHIVES.md)) |
| **Recherche et miniatures** | Une recherche plein texte intégrée qui respecte les autorisations, et des miniatures pour les images, la vidéo, les PDF, les dossiers et les documents Office - celles des documents Office, dessinées sur le serveur par le moteur de l’app d’édition Office ou par un ONLYOFFICE connecté ([Recherche](docs/SEARCH.md), [Miniatures](docs/thumbnails.md)) |
| **Temps réel** | Présence et mises à jour des fichiers en direct par WebSocket, et la modification à plusieurs d’un fichier dans une application, par le relais de filex ([Temps réel](docs/REALTIME.md), [Modifier à plusieurs](docs/APP-PLUGINS-API.md#editing-together-coedit-056)) |
| **Clients** | Une application de bureau avec synchronisation de dossiers, une application web qu’un téléphone installe sous le nom et le logo de votre instance, une CLI, des intégrations Vue / React / composant web, une API REST et un serveur MCP ([Bureau](docs/DESKTOP.md), [Synchronisation](docs/SYNC.md), [CLI](docs/CLI.md), [Intégration](docs/INTEGRATION.md), [MCP](docs/MCP.md)) |
| **Applications et langues** | Des applications en bac à sable (Signature électronique, Convertir, Office editor, filextext, draw.io) qui impriment en passant par filex, modifient un fichier à plusieurs par son relais et n’ouvrent le fichier d’un dossier chiffré que si vous l’autorisez, déchiffré dans votre navigateur ; des packs de langue et une mise en page de droite à gauche ([Applications](docs/APP-PLUGINS.md), [Modifier dans une application](docs/E2E-ENCRYPTION.md#editing-in-an-app), [RTL](docs/RTL.md)) |
| **Exploitation** | Un seul binaire ou un seul conteneur ; SQLite, PostgreSQL ou MySQL ; un panneau d’administration avec une seule recherche pour chaque page, chaque paramètre et chaque enregistrement ; des webhooks, une cloche dans l’application et Web Push vers les téléphones et les navigateurs, avec un récapitulatif facultatif ; une vue de l’utilisation et des coûts (Backblaze B2 pour l’instant) ; des vérifications de mises à jour, avec des versions correctives qui s’installent toutes seules une fois que vous l’autorisez ([Bases de données](docs/DATABASES.md), [Panneau d’administration](docs/ADMIN-PANEL.md), [Notifications](docs/NOTIFICATIONS.md), [Utilisation](docs/USAGE.md), [Mises à jour](docs/UPDATES.md)) |

Chaque fonctionnalité sous forme d’entrée propre - son comportement, ses limites et la page
qui la documente : [docs/FEATURES.md](docs/FEATURES.md).

<br>

# Architecture

Voir [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

<details>
<summary><b>Les éléments d’un coup d’œil</b> - pilotes, protocoles et paquets dans un seul schéma</summary>

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

# Comment filex est construit

filex a un seul mainteneur, qui le construit avec des agents de programmation IA (Claude
Code). Le mainteneur décide de ce qu’est filex et de son fonctionnement : l’architecture,
les modèles de données et de sécurité, et le comportement de chaque fonctionnalité. Les
agents écrivent l’essentiel du code dans cette direction, dans des langages avec lesquels
le mainteneur travaille. Environ 82 % des commits du dépôt de développement portent une
ligne `Co-Authored-By: Claude` ; ce n’est donc pas caché.

C’est du code assisté par IA, pas du code généré sans relecture :

- Chaque modification passe par la chaîne de tests avant d’être intégrée : tests Go (y
  compris sous le détecteur de concurrence), environ 500 fichiers Vitest, environ 120
  spécifications Playwright sous Chromium, Firefox et WebKit, Cypress, et des tests de base
  de données sur SQLite, PostgreSQL et MySQL. Chaque version n’est étiquetée qu’après que la
  matrice complète de GitHub et une exécution à blanc du workflow de publication ont réussi
  sur le commit exact.
- Les modifications touchant à la connexion, aux autorisations, au partage et au
  chiffrement font l’objet de passes de relecture supplémentaires, et un test rouge est
  corrigé, pas assoupli.
- Les signalements de sécurité sont traités ouvertement : voir [SECURITY.md](SECURITY.md)
  et les avis publiés. Il n’y a pas encore eu d’audit indépendant.
- Les README en allemand, espagnol, français et chinois ainsi que les packs de langue
  allemand, espagnol et français sont des traductions automatiques qu’aucun locuteur natif
  n’a encore relues.

# Contribuer

[![CI](https://img.shields.io/github/actions/workflow/status/BRF-Tech/filex/ci.yml?branch=main&label=ci)](https://github.com/BRF-Tech/filex/actions)

Les tickets et les pull requests sont les bienvenus ; avant une pull request d’une certaine
taille, ouvrez un ticket qui dit ce que vous vous apprêtez à faire.
[docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) décrit le processus de travail, les tests qu’une
modification doit passer et les règles de la documentation, et le projet suit le
[Contributor Covenant](CODE_OF_CONDUCT.md). Un problème de sécurité se signale en privé,
comme le décrit [SECURITY.md](SECURITY.md). Ce qui a changé dans chaque version se trouve
dans [CHANGELOG.md](CHANGELOG.md). Une version est publiée chaque jour à 10 h, heure
d’Istanbul, à partir de ce que contient alors `main`, quand il contient du nouveau ; une
pull request fusionnée après cette heure sort avec la version du lendemain
([Release process](docs/CONTRIBUTING.md#release-process)).

<details>
<summary><b>Développement</b> - compiler depuis les sources, et ce qui se trouve où</summary>

```bash
git clone https://github.com/BRF-Tech/filex.git
cd filex
pnpm install
pnpm run build:all    # builds packages, web, then Go binary
./bin/filex serve
```

Sous-répertoires :
- `backend/` - service HTTP en Go (cmd/filex, internal/*, db/queries, db/migrations)
- `packages/core` - `@brftech/filex-core` (SFC Vue 3, source de vérité)
- `packages/webcomponent` - `@brftech/filex` (composant web)
- `packages/react` - `@brftech/filex-react` (adaptateur React via @lit/react)
- `web/` - interface d’administration Vue 3 (intégrée au binaire Go via `go:embed`)
- `desktop/` - application Electron (processus principal en bundle, synchronisation depuis
  la zone de notification, mise à jour automatique)
- `demo/` - démos HTML autonomes pour chaque framework
- `e2e/` - suites Playwright (web, intégrations, application de bureau empaquetée) + `shots/`,
  les scripts que lance `pnpm shots` pour prendre les captures d’écran ci-dessus (publiées
  sur filex.sh, pas conservées dans le dépôt)
- `docker/` - Dockerfiles + compose
- `deploy/` - stacks Compose prêtes à l’emploi + chart Helm (voir [`deploy/`](deploy/))
- `docs/` - documentation en Markdown
- `docs-site/` - site VitePress publié sur [docs.filex.sh](https://docs.filex.sh)

</details>

<br>

# Licence

MIT - voir [LICENSE](LICENSE).

Les badges des boutiques, dans [`docs/badges/`](docs/badges/), sont les visuels de ces
boutiques elles-mêmes, utilisés sans modification et non couverts par cette licence :
Microsoft et le badge Microsoft Store sont des marques commerciales du groupe de sociétés
Microsoft ; le badge Snap Store est © Canonical Ltd., sous licence
[CC BY-ND 2.0 UK](https://creativecommons.org/licenses/by-nd/2.0/uk/).

Nextcloud, Dropbox, Google Drive, File Browser et ONLYOFFICE sont les noms de produits de
tiers, cités ici uniquement pour les décrire. filex n’est affilié à aucun d’eux, et aucun
d’eux ne le parraine ni ne le cautionne.
