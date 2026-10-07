<!-- Translated from README.md as of bf857aad (v0.51.0). The English README is the source: change it first, then carry the change here. -->

<div align="center">

<img src="docs/logo.png" alt="Logo filex" width="96">

# filex - gestionnaire de fichiers auto-hébergé, intégrable partout

[![Release](https://img.shields.io/github/v/release/BRF-Tech/filex?color=2f6ceb)](https://github.com/BRF-Tech/filex/releases)
[![CI](https://img.shields.io/github/actions/workflow/status/BRF-Tech/filex/ci.yml?branch=main&label=ci)](https://github.com/BRF-Tech/filex/actions)
[![License: MIT](https://img.shields.io/github/license/BRF-Tech/filex?color=22c55e)](LICENSE)
[![Container](https://img.shields.io/badge/ghcr.io-brf--tech%2Ffilex-2496ed?logo=docker&logoColor=white)](https://github.com/BRF-Tech/filex/pkgs/container/filex)
[![Live demo](https://img.shields.io/badge/live_demo-demo.filex.sh-f59e0b)](https://demo.filex.sh)

[English](README.md) · [Türkçe](README.tr.md) · [Deutsch](README.de.md) · [Español](README.es.md) · **Français** · [简体中文](README.zh-CN.md)

<sub>Cette page est une traduction du [README anglais](README.md), dans son état de la v0.51.0. En cas de divergence, le texte anglais fait foi. Traduite automatiquement, elle attend la relecture d’un locuteur natif - les corrections sont les bienvenues. Les documents vers lesquels elle renvoie sont en anglais.</sub>

Un seul binaire Go avec une interface web complète, des pilotes de stockage,
d’authentification et de base de données interchangeables, la **collaboration en temps
réel**, **un composant web intégrable**, une **application de bureau dont la
synchronisation des dossiers est en direct** - une modification d’un côté arrive de
l’autre en une seconde environ - un **serveur MCP intégré** pour que les agents
IA le pilotent nativement, et des **applications** : des extensions qui lui apprennent de
nouvelles choses à faire avec les fichiers - un module WebAssembly en bac à sable, une
interface qui leur est propre dans un cadre en bac à sable, ou les deux - à commencer par
la **signature de documents** avec des personnes de votre organisation et de l’extérieur.
Un **pack de langue** est lui aussi une application, si bien que filex peut être traduit
sans attendre une nouvelle version - et il s’affiche **de droite à gauche** pour les
langues qui se lisent dans ce sens. Chacun se connecte avec le compte qu’il a déjà - SSO,
LDAP, ou le **compte Windows ou Linux** de la machine où filex s’exécute - et, sur une
installation multilocataire, **chaque locataire s’administre lui-même** : ses propres
fournisseurs de connexion, son domaine personnalisé et son propre certificat.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://filex.sh/shots/explorer-grid-dark.5ddafe2dac64.png">
  <img src="https://filex.sh/shots/explorer-grid-light.484fb070ca19.png" alt="Explorateur filex - grille de miniatures" width="900">
</picture>

</div>

## Essayer tout de suite

**Démo en direct** : [demo.filex.sh](https://demo.filex.sh) - connectez-vous avec
`demo@demo.com` / `demo` (rôle d’administrateur, bac à sable réinitialisé chaque nuit).
Ou lancez votre propre instance :

```bash
docker run -p 5212:5212 \
  -e FILEX_DEFAULT_STORAGE_DRIVER=local -e FILEX_DEFAULT_STORAGE_PATH=/srv/files \
  -v filex-data:/data -v "$PWD:/srv/files" \
  ghcr.io/brf-tech/filex:latest
```

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

Ouvrez http://localhost:5212/admin - au premier lancement, les identifiants de
l’administrateur et les instructions d’intégration s’affichent dans la console. Cette URL
est celle de l’opérateur ; les personnes à qui vous donnez un compte reçoivent
**http://localhost:5212/drive**, le même gestionnaire de fichiers sans le panneau autour.

Vous préférez une fenêtre à un onglet de navigateur ? L’**application de bureau**
(Windows / Linux / macOS) se connecte à n’importe quel serveur filex et synchronise des
dossiers en arrière-plan - et il en existe, sur chaque plateforme, une copie qui fonctionne
**sans installation** (un `.exe` portable, une AppImage, un `.zip`).
Obtenez-la depuis le [Microsoft Store](https://apps.microsoft.com/detail/9PKXDJLVZWXW), le
[Snap Store](https://snapcraft.io/filex-app) ou la
[dernière version](https://github.com/BRF-Tech/filex/releases/latest) -
[docs/DESKTOP.md](docs/DESKTOP.md).

## Pourquoi filex

La plupart des gestionnaires de fichiers auto-hébergés sont soit **trop petits** (une liste
de répertoire avec téléversement), soit **trop gros** (une suite collaborative que l’on
déploie pour son onglet de fichiers). filex vise l’entre-deux :

- **Un client navigateur pour vos utilisateurs, pas seulement pour vous** - donnez à
  quelqu’un un compte `user` ou `viewer` et `…/drive`, et il dispose du gestionnaire de
  fichiers proprement dit : ses stockages, le téléversement, le partage, la recherche,
  l’éditeur. Pas de panneau d’administration à traverser, pas de frontend séparé à
  déployer. `…/admin` est la porte d’entrée de l’opérateur vers la même application.
- **Une navigation que tout le monde connaît déjà** - un panneau de gauche avec un menu
  **+ Nouveau** bien en vue et **Accueil · Mes fichiers · Partagés avec moi · Mes partages ·
  Récents · Favoris · Brouillons · Corbeille**, ainsi que les stockages auxquels vous avez
  accès ; un stockage que quelqu’un a partagé avec vous y apparaît simplement, à un clic,
  sans instructions de montage. N’importe qui peut réduire ce panneau en barre d’icônes
  depuis la barre supérieure. **Accueil** est une vue *dans* l’application, pas une page à
  côté - vos stockages, ce que vous avez ouvert en dernier et ce que vous avez mis en
  favoris, sous la même barre latérale et le même en-tête que les fichiers. C’est
  l’interface commune à tous : un seul champ de recherche sur toute la largeur de
  l’en-tête avec le rappel de sa palette ⌘K, une rangée de filtres
  Type / Propriétaire / Modifié / Taille, Dossiers et Fichiers en sections titrées,
  Détails et Activité dans le panneau de détails, et une ligne de stockage. Pour ceux qui
  veulent un simple espace de fichiers plutôt qu’un gestionnaire de fichiers,
  `uiProfile: 'simple'` désactive par défaut le reste des éléments d’interface - un seul
  volet, un seul dossier, liste ou grille. Un seul explorateur dans tous les cas : il n’y
  a pas de seconde interface à tenir à jour en parallèle.
- **S’intègre partout** - la même interface est livrée sous forme de composant Vue 3, de
  composant React et de composant web `<filex-explorer>` indépendant de tout framework.
  Placez un vrai gestionnaire de fichiers dans *votre* produit, adossé à votre propre
  serveur filex et verrouillé sur un dossier par locataire. Le panneau de navigation est
  fourni avec - `<filex-explorer sidenav ui-profile="simple">`, c’est tout ce qu’il faut à
  une page hôte qui ne touche jamais au JavaScript.
- **Natif pour les agents IA** - une surface REST (`/api/ai`) bornée par les autorisations
  d’une clé d’API, ainsi qu’un **serveur MCP** natif (`/api/ai/mcp`) ; `/api/ai` et
  `/api/files` sont décrits dans un
  [fichier OpenAPI 3.1](backend/internal/api/openapi.json) qu’un test maintient conforme
  au routeur. Donnez à un agent une clé confinée à un seul dossier et il y travaille avec
  les opérations mêmes de l’explorateur - lister, lire, écrire, copier, convertir,
  partager, la corbeille, les versions, les archives - et rien en dehors.
- **Des applications qui ne peuvent faire que ce que vous avez approuvé** - signer un
  contrat avec un partenaire qui n’a pas de compte, convertir une vidéo, tout ce que
  décrit un manifeste, ajouté sous forme d’**application** : un module WebAssembly qui
  s’exécute dans filex, une interface qui lui est propre et que filex sert dans un cadre
  en bac à sable, ou les deux - avec exactement les autorisations que vous avez lues et
  accordées à l’installation. Le module n’a pas de système de fichiers, pas de réseau,
  aucun programme sur votre serveur ; l’interface ne peut pas lire la session de filex
  et est coupée du réseau par la politique de filex lui-même. Rien ne se met à jour tout
  seul : une nouvelle version attend un administrateur, et la précédente reste à un
  clic. Quatre applications sont livrées sous forme de dépôts publics -
  **Signature électronique**, **Convertir**, **filextext** (un espace de travail texte
  chiffré de bout en bout) et **draw.io** - et vous en installez une depuis son adresse
  GitHub ([Applications](#applications)).
- **Dans votre langue, et dans votre sens de lecture** - l’anglais et le turc sont livrés
  dans le binaire, et toute autre langue est un **pack de langue** : une application qui
  n’exécute rien, installée depuis un dépôt comme n’importe quelle autre, qui traduit
  l’explorateur, le panneau d’administration, les pages publiques qu’ouvre un inconnu
  *et le texte qu’écrit le serveur de filex* - les e-mails, les notifications, les pages
  sans JavaScript derrière un lien. Un pack dit quelle part de cette version il couvre, et
  ce qui lui manque s’affiche en anglais ; les formes du pluriel suivent CLDR, si bien
  qu’une langue a les formes qui sont réellement les siennes. Pour l’arabe, l’hébreu, le
  persan et l’ourdou, l’interface **passe de droite à gauche** - et s’arrête là où
  l’inversion en miroir serait une erreur, dans l’espace du document et dans le texte machine
  ([docs/RTL.md](docs/RTL.md), [écrire un pack](docs/PLUGIN-KIT.md#writing-a-language-pack)).
- **Il porte votre marque, pas la nôtre** - composez un thème à vos couleurs sur l’écran
  **Apparence** et faites-en le thème par défaut : la page de connexion et chaque lien
  public le portent aussi, et une demande de signature venant de votre instance porte
  votre nom, pas celui de filex.
- **Temps réel** - des avatars de présence (une photo de profil définie une fois sur le
  compte, affichée pour chaque client connecté avec votre compte) et des mises à jour en
  direct des fichiers par WebSocket, dans l’interface native *et* dans les contextes
  intégrés (authentification par ticket de courte durée, repli sur l’interrogation
  périodique de l’API). Une tâche par lots est regroupée à l’émission, si bien
  qu’extraire une archive de cinq mille fichiers coûte à un explorateur ouvert un filet
  borné de trames plutôt que cinq mille ([docs/REALTIME.md](docs/REALTIME.md)).
- **Sur votre bureau aussi** - le même explorateur est livré sous forme d’application
  Windows/Linux/macOS qui garde les dossiers locaux synchronisés avec le serveur depuis la
  zone de notification - **en direct**, en une seconde environ, dans les deux sens - se
  met à jour toute seule et accueille plusieurs comptes (ou locataires) côte à côte.
  Faites un clic droit sur un dossier → **Conserver sur cet ordinateur** et il est mis en
  miroir sous un seul dossier filex ; tout le reste demeure en ligne uniquement dans la
  fenêtre. Les machines sans interface graphique disposent du même moteur sous la forme de
  `filex sync` / `filex client`.
- **Parle les protocoles dans les deux sens** - filex peut *se connecter à* des disques
  locaux, S3, FTP, SFTP, WebDAV et des partages SMB/NAS, et l’on peut *y accéder en*
  **S3**, **SFTP**, **FTPS**, **NFSv3** et **WebDAV**. Pointez vers filex `rclone`,
  `restic`, `aws s3`, WinSCP, FileZilla, un scanner qui n’a appris que le FTP ou un
  lecteur multimédia qui n’a appris que le NFS, et ils arrivent dans la même arborescence,
  avec les mêmes autorisations, la même corbeille et le même quota que l’interface web.
  Hors du LAN, il y a aussi **`filex mount`**, qui attache un serveur distant en HTTPS
  ordinaire - un dossier sous Linux, une lettre de lecteur sous Windows
  ([docs/PROTOCOLS.md](docs/PROTOCOLS.md)).
- **Rôles et autorisations par utilisateur** - 29 autorisations nommées (chaque action sur
  les fichiers, chaque type de partage, chaque protocole, les clés d’API, l’application de
  bureau, cinq espaces d’administration), et chacun a un seul rôle : Administrateur,
  Utilisateur, Lecteur ou un rôle personnalisé, qui peut différer dans certains dossiers
  (« pas de suppression, sauf dans Scratch ») et comporter des limites (durée de vie et
  mot de passe des liens, types de fichiers bloqués, taille maximale de fichier,
  authentification à deux facteurs obligatoire). Les exceptions par personne l’emportent
  sur le rôle, un administrateur délégué peut gérer les utilisateurs sans jamais détenir
  plus que ce qu’il accorde, et la même réponse vaut à chaque point d’entrée -
  l’application web, l’API des agents, WebDAV, SFTP, FTPS, S3, NFS et les clés d’API. Une
  application installée peut ajouter ses propres autorisations (« Demander des
  signatures »), accordées de la même façon, et un lien public ne reste ouvert que tant
  que son créateur peut encore le créer ([docs/PERMISSIONS.md](docs/PERMISSIONS.md)).
- **Groupes** - des ensembles nommés de personnes, par locataire : partagez un dossier
  avec un groupe comme avec une personne, et donnez à un groupe un rôle que détient chaque
  membre qui n’a pas de rôle propre (une priorité de rôle départage les groupes). Les
  personnes rejoignent un groupe à la main, ou par les groupes que transmet leur connexion -
  un claim OIDC, le `memberOf` de LDAP, les groupes du système d’exploitation ou un
  en-tête de proxy - et le quittent quand le fournisseur d’identité le dit
  ([docs/GROUPS.md](docs/GROUPS.md)).
- **Connexion avec le compte que chacun a déjà** - un mot de passe local, OIDC, LDAP /
  Active Directory, un proxy d’authentification, ou le **compte Windows ou Linux** de la
  machine où filex s’exécute : le système d’exploitation juge le mot de passe,
  filex ne le stocke jamais, et le fournisseur n’est activé qu’une fois qu’un vrai compte
  s’est connecté par son intermédiaire. Chaque fournisseur suit une seule et même
  règle pour déterminer qui obtient un compte lors d’une première connexion
  ([docs/OS-LOGIN.md](docs/OS-LOGIN.md)).
- **Deviner un mot de passe prend du temps** - les mots de passe erronés sont comptés par
  compte et par adresse, sur le formulaire web comme sur WebDAV, FTPS et SFTP ; un
  verrouillage double jusqu’à 15 minutes, une liste d’adresses IP autorisées est le moyen
  de rentrer, et une adresse client transmise n’est crue que si elle vient d’un proxy de
  confiance - par défaut cette machine et les conteneurs à côté de filex, ou tout autre
  proxy que vous nommez
  ([limites de tentatives de connexion](docs/CONFIGURATION.md#sign-in-attempt-limits)). Une
  modification qu’un autre site envoie avec la session d’un visiteur est refusée
  ([requêtes venant d’autres origines](docs/CONFIGURATION.md#requests-from-other-origins)).
- **Multilocataire par conception** - un stockage par locataire avec un mode
  multilocataire natif, des rôles RBAC + des droits d’accès par élément, des clés d’API
  confinées, des identités par clé pour les journaux d’audit, et des types de clé
  distincts, application et utilisateur, si bien qu’un identifiant d’intégration partagé
  ne peut gérer les clés de personne. Une clé nomme les autorisations qu’elle
  détient - une liste vide est refusée, et non lue comme « tout » - et **aucun identifiant
  qu’elle délivre n’est jamais plus large qu’elle-même** : une clé d’API, une clé S3, un
  export NFS ou une clé SSH créés avec une clé restreinte ne peuvent ni dépasser ses
  verbes, ni sortir de son dossier, ni survivre à son expiration (`403 token_ceiling`). La
  frontière entre locataires est imposée à chaque route qui désigne une ligne, pas
  seulement à celles qui en dressent la liste, et les paramètres communs à toute
  l’instance sont réservés au locataire de la plateforme. Chaque locataire a un **realm**,
  son nom de connexion : les `alex` de deux locataires sont deux personnes, qu’ils se
  connectent à l’adresse propre au locataire, saisissent le realm sur la page de la
  plateforme ou écrivent `realm/alex` en SFTP
  ([realms](docs/MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)). Et un locataire
  s’administre lui-même : son administrateur ajoute l’OIDC ou le LDAP propre au locataire,
  l’opérateur lie des fournisseurs de connexion partagés à un seul locataire ou à
  plusieurs, et le domaine personnalisé d’un locataire est prouvé par un CNAME et servi
  avec un certificat de votre proxy, de filex lui-même (ACME) ou avec le sien
  ([docs/TENANT-ADMIN.md](docs/TENANT-ADMIN.md)).
- **Un déploiement sans histoire** - un seul binaire ou un seul conteneur, sur un hôte
  dédié ou sous un sous-chemin d’un hôte que vous partagez ; SQLite par défaut,
  Postgres/MySQL quand vous les voulez ; chaque pilote se choisit par variable
  d’environnement. À chaque modification, la CI migre les trois moteurs, les compare entre
  eux et y écrit, parce que « pris en charge » a longtemps voulu dire « ça compile »
  ([docs/DATABASES.md](docs/DATABASES.md)).

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

## Captures d’écran

### Applications - la signature d’un document, la première d’entre elles

Dana demande à un collègue du même filex et à un partenaire extérieur de signer un accord.
L’application est [Signature électronique](https://github.com/BRF-Tech/filex-sign) ;
chaque écran est affiché par filex, et le lien que reçoit le partenaire est un
partage ordinaire.

| Définissez les zones - nommez chacune et dites à qui elle appartient ; le document vient ensuite | Placez-les - choisissez une zone, appuyez sur la page à l’endroit où elle doit aller |
|---|---|
| ![Définition des zones d’une demande de signature](https://filex.sh/shots/signing/sign-define-1440.4a966ffefa4f.png) | ![Placement des zones sur le document](https://filex.sh/shots/signing/sign-place-1440.696a10b65be8.png) |

| Le lien du partenaire - le seul écran public de filex, au nom de votre instance, protégé par un PIN | …et ce qu’il ouvre : uniquement ses propres zones - ici un nom saisi au clavier dans la police choisie par le demandeur (dessiné et téléversé sont les deux autres possibilités) |
|---|---|
| ![L’écran de saisie du PIN du signataire externe](https://filex.sh/shots/signing/sign-outside-pin-1440.2cd8ccb99483.png) | ![Le signataire externe remplissant ses zones](https://filex.sh/shots/signing/sign-outside-fill-1440.54632da00ef4.png) |

| Tant que la demande est en cours - le document gelé pour tout le monde et, dans ses détails, qui a signé | Installation d’une application - chaque autorisation qu’elle demande, en termes simples, avant que rien ne s’exécute |
|---|---|
| ![Le document verrouillé, son panneau Signatures ouvert](https://filex.sh/shots/signing/sign-status-1440.518a3bd6651f.png) | ![La vérification des autorisations dans l’assistant d’installation](https://filex.sh/shots/apps/apps-install-review-1440.cdb1a4ebf8f6.png) |

| Une application installée - d’où elle vient, son empreinte, et chaque autorisation qu’elle détient, en termes simples (ses paramètres et ses actions suivent, plus bas dans la page) | Le convertisseur, une autre application - chaque cible sous sa catégorie, trois étapes |
|---|---|
| ![Le détail d’une application installée](https://filex.sh/shots/apps/apps-detail-1440.4e27b40c5ce7.png) | ![L’assistant du convertisseur](https://filex.sh/shots/apps/convert-wizard-1440.231ada006fd6.png) |

| Une application qui apporte sa propre interface - la vérification montre l’empreinte du paquet, chaque adresse hors du paquet (une adresse en direct est une autorisation, en jaune) et ce qu’un navigateur ne peut pas promettre | …et cette interface ouverte sur son propre type de fichier, là où se trouverait l’aperçu de filex. Elle lit et enregistre le fichier en passant par filex, dans un cadre en bac à sable (une petite application d’exemple, écrite pour ces images) |
|---|---|
| ![La vérification à l’installation d’une application dotée de sa propre interface](https://filex.sh/shots/apps/app-interface-review-1440.5e0e3009d2ba.png) | ![L’interface propre à une application, ouverte comme visionneuse d’un fichier](https://filex.sh/shots/apps/app-interface-viewer-1440.519b23618156.png) |

| Toutes les applications de l’instance, et parmi elles un **pack de langue** - un manifeste qui n’exécute rien, qui dit quelle part de ce filex il traduit et dont la langue s’en va avec lui |
|---|
| ![La liste Applications, un pack de langue parmi les applications](https://filex.sh/shots/langpack/apps-list-1440.c0b6a723e725.png) |

### Ce qui est à vous, où que vous soyez

| La cloche - le nombre de notifications non lues affiché dessus, chaque ligne menant là où elle le dit | Toutes vos notifications, dans l’explorateur - pour tout le monde, pas seulement pour les administrateurs |
|---|---|
| ![La cloche ouverte, avec son badge de notifications non lues](https://filex.sh/shots/signing/bell-badge-1440.61396b9ab714.png) | ![La liste complète des notifications par-dessus l’explorateur](https://filex.sh/shots/signing/notifications-list-1440.cb8cc10769dc.png) |

| Mes partages - les liens que vous avez créés, et leurs PIN quand vous devez en transmettre un | Chaque tableau d’administration - un menu **Actions** épinglé par ligne, le même menu qu’ouvre le ⋮ de l’explorateur |
|---|---|
| ![Mes partages, le menu Actions d’une ligne ouvert](https://filex.sh/shots/signing/my-shares-1440.3cdfc7ee8ef3.png) | ![Admin → Partages, le menu Actions d’une ligne ouvert](https://filex.sh/shots/signing/admin-table-actions-1440.401d5cbfacf8.png) |

### Votre marque

| Apparence - composez un thème à vos couleurs, prévisualisé au fil de la saisie | Défini comme thème par défaut, c’est ce que porte l’explorateur de tout le monde… |
|---|---|
| ![L’éditeur de thèmes](https://filex.sh/shots/appearance/theme-editor-1440.7cf3d997f7b1.png) | ![L’explorateur portant le thème de l’opérateur](https://filex.sh/shots/appearance/themed-explorer-1440.dbe464fc39c7.png) |

| …et la page de connexion, avant que quiconque ne se soit connecté | Un lien symbolique que filex ne suit pas le dit - dans la liste, et en toutes lettres dans ses détails |
|---|---|
| ![La page de connexion portant le thème de l’opérateur](https://filex.sh/shots/appearance/themed-signin-1440.84197bec297e.png) | ![Un lien symbolique qui sort du stockage, signalé par un badge](https://filex.sh/shots/symlinks/symlink-badge-1440.f85936372113.png) |

### Le gestionnaire de fichiers

| Partage - PIN, expiration, limite de téléchargements, `curl` en une ligne | Visionneuse Markdown |
|---|---|
| ![Boîte de dialogue de partage](https://filex.sh/shots/share-modal.eaf11836828a.png) | ![Visionneuse Markdown](https://filex.sh/shots/viewer-markdown.1789ecdcfbc5.png) |

| …et ce qu’ouvre la personne à l’autre bout. filex n’a qu’UN écran tourné vers l’extérieur - un fichier partagé, un dossier, une demande de fichiers, la page de signature d’une application et le PIN placé devant n’importe lequel d’entre eux sont tous cette même page, au nom de votre instance |
|---|
| ![Un lien de partage public, tel que le voit son destinataire](https://filex.sh/shots/public-share.0e3ba07f7c88.png) |

| Panneau d’administration | Page d’accueil de la démo |
|---|---|
| ![Tableau de bord d’administration](https://filex.sh/shots/admin-dashboard.7c334e824b3c.png) | ![Page d’accueil de la démo](https://filex.sh/shots/demo-landing.d2b345f6a229.png) |

| Le menu d’administration - toutes les pages en trois panneaux, **Fichiers et stockage**, **Utilisateurs et sécurité** et **Système**, avec une courte ligne sous chacune ; un téléphone affiche les mêmes pages dans un tiroir ([docs/ADMIN-PANEL.md](docs/ADMIN-PANEL.md)) |
|---|
| ![Le panneau Utilisateurs et sécurité du menu d’administration, ouvert par-dessus Admin → Utilisateurs](https://filex.sh/shots/megamenu/people-panel-1440.2efdcb9a685a.png) |

| Rôles - Administrateur, Utilisateur, Lecteur et vos propres rôles : qui détient chacun d’eux, ce qu’il autorise, où il diffère selon le dossier, ses limites ([docs/PERMISSIONS.md](docs/PERMISSIONS.md)) |
|---|
| ![Admin → Rôles : les rôles intégrés et deux rôles personnalisés](https://filex.sh/shots/roles/roles-list-1440.77477a2c7001.png) |

| Groupes - des ensembles nommés de personnes, dotés d’un accès aux dossiers et d’un rôle ; des membres gérés à la main ou gardés synchronisés avec les groupes que transmet une connexion ([docs/GROUPS.md](docs/GROUPS.md)) | Partage d’un dossier avec un groupe, à côté des personnes - le niveau Propriétaire est soumis à confirmation dans la boîte de dialogue, pas accordé d’un clic |
|---|---|
| ![Admin → Groupes](https://filex.sh/shots/groups/groups-list-1440.73224b70e40b.png) | ![Partage d’un dossier avec un groupe](https://filex.sh/shots/groups/share-group-1440.31c2a81e1eeb.png) |

| Sécurité de connexion - la limite de tentatives, les adresses autorisées, les proxys de confiance, les verrouillages et le journal des connexions ([limites de tentatives de connexion](docs/CONFIGURATION.md#sign-in-attempt-limits)) | …et ce que dit le formulaire de connexion d’un compte verrouillé, avec le compte à rebours du verrouillage sur son bouton |
|---|---|
| ![Admin → Sécurité de connexion](https://filex.sh/shots/loginsecurity/login-security-1440.e8cb49e3e6b3.png) | ![Le formulaire de connexion d’un compte verrouillé](https://filex.sh/shots/loginsecurity/login-locked-1440.94d4b8117a23.png) |

| Qui peut chiffrer - désactivé, administrateurs uniquement, toutes les personnes dont le rôle le permet, ou après l’approbation d’un administrateur ; les demandes en attente, avec leur auteur et leur motif ([qui peut chiffrer](docs/E2E-ENCRYPTION.md#who-may-encrypt)) | …et du côté de la personne : la boîte de dialogue Nouveau dossier demande à un administrateur un seul dossier chiffré, avec un motif |
|---|---|
| ![Admin → Chiffrement : la politique d’approbation et trois demandes en attente](https://filex.sh/shots/encryption/admin-encryption-1440.b74163acb93c.png) | ![Demande d’un dossier chiffré depuis la boîte de dialogue Nouveau dossier](https://filex.sh/shots/encryption/request-new-folder.e74894fba30f.png) |

| Applications par défaut - chaque type de fichier pris en charge par autre chose que filex : qui l’ouvre et qui génère sa miniature, dans l’ordre que vous définissez ([Applications par défaut](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail)) | Aperçus de dossiers - chaque dossier illustré par les trois derniers fichiers qui y sont arrivés ; les SVG sont rendus par le moteur intégré de filex ([docs/thumbnails.md](docs/thumbnails.md#folder-previews)) |
|---|---|
| ![Extensions → Applications par défaut](https://filex.sh/shots/defaultapps/default-apps-1440.27d3c64fe457.png) | ![Dossiers illustrés par leurs fichiers les plus récents](https://filex.sh/shots/thumbnails/folders-grid-1440.2b408fb54f7e.png) |

| Un `.csv` s’ouvre dans le tableur d’ONLYOFFICE quand celui-ci est connecté - un coup d’œil d’abord, et pas de boîte de dialogue pour le délimiteur : le séparateur propre au fichier est transmis ([fichiers CSV](docs/ONLYOFFICE.md#csv-files)) | …et son éditeur, qui dit ce que conserve un enregistrement en CSV ; le fichier revient en CSV du même type |
|---|---|
| ![Un CSV à points-virgules ouvert dans le tableur d’ONLYOFFICE](https://filex.sh/shots/csvoffice/csv-view-1440.83237ba55d3d.png) | ![Le CSV dans l’éditeur d’ONLYOFFICE, avec la note sur ce que conserve un enregistrement](https://filex.sh/shots/csvoffice/csv-edit-1440.4efc379a293d.png) |

| L’interface commune - là où tout le monde arrive | Recherche dans ce dossier ; `⌘K` / `Ctrl K` transmet la requête à la palette |
|---|---|
| ![L’interface commune de filex](https://filex.sh/shots/driveshell/driveshell-hero-1440.d8c44de4f498.png) | ![Recherche dans un dossier](https://filex.sh/shots/driveshell/driveshell-search-1440.119e6bd43905.png) |

| Panneau de navigation - Accueil, Partagés avec moi, Mes partages, Récents, Favoris, Corbeille, et les stockages auxquels vous avez accès | Réduit à la barre d’icônes |
|---|---|
| ![Panneau de navigation](https://filex.sh/shots/sidenav/sidenav-expanded-1440.ef235f94c521.png) | ![Réduit à une barre d’icônes](https://filex.sh/shots/sidenav/sidenav-rail-1440.843a2158380d.png) |

| Étiquettes - les vôtres, ou celles de votre équipe ; une étiquette ouvre tous les fichiers qui la portent, quel que soit le dossier où ils se trouvent | Corbeille - ce qui a été supprimé, d’où cela venait, et combien de temps il reste avant que cela ne disparaisse |
|---|---|
| ![Étiquettes personnelles et d’équipe](https://filex.sh/shots/tags/tags-kinds-1440.f363e549a5be.png) | ![La vue Corbeille](https://filex.sh/shots/sidenav/view-trash-1440.c7438b1e66a8.png) |

| Partagés avec moi - les dossiers auxquels d’autres personnes vous ont accordé l’accès, sans instructions de montage | Intégré à la page d’un autre produit |
|---|---|
| ![Partagés avec moi](https://filex.sh/shots/sidenav/view-shared-1440.6f68fd5b581d.png) | ![Composant web intégré](https://filex.sh/shots/sidenav/embed-webcomponent-1440.2dc86ba73804.png) |

| Guide de connexion - les guides, construits à partir de *votre* déploiement | Clés d’API - créez les vôtres, dans l’explorateur ou dans une intégration (la session ou la clé d’une personne ; une intégration relayée par un proxy avec une seule clé partagée de type *app* n’a pas cette entrée) |
|---|---|
| ![Guide de connexion](https://filex.sh/shots/sidenav/connect-1440.ade9043dddde.png) | ![Clés d’API](https://filex.sh/shots/sidenav/apikeys-minted-1440.14eb6d484613.png) |

| Accéder à filex depuis n’importe quoi - S3, SFTP, FTPS, NFS, WebDAV. Chaque commande est construite à partir de *votre* déploiement |
|---|
| ![Guide de connexion](https://filex.sh/shots/connections-guide.cf9a135724c9.png) |

| Un stockage qui n’est pas livré avec filex - installé comme extension dans **Extensions → Extensions de stockage**, il décrit son propre formulaire de configuration |
|---|
| ![Extensions](https://filex.sh/shots/admin-plugins.c25fa69cfc7c.png) |

## Démarrage rapide - binaire

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

## Auto-héberger avec Compose ou Helm

Le `docker run` ci-dessus suffit pour essayer filex. Pour un vrai déploiement,
des stacks prêtes à l’emploi se trouvent dans [`deploy/`](deploy/) :

- **[`deploy/compose/`](deploy/compose/)** - Docker Compose :
  - **minimal** - filex + SQLite + disque local (un service, zéro dépendance).
  - **full** - filex + PostgreSQL + Redis + Caddy (HTTPS automatique), plus des services
    optionnels activables : **ONLYOFFICE**, **Drawio** et un **serveur S3** (Versity S3
    Gateway). Activez ou désactivez chacun avec un profil Compose dans `.env`. La
    conversion, c’est l’[application Convertir](#applications), pas un conteneur annexe.
- **[`deploy/helm/filex/`](deploy/helm/filex/)** - un chart Helm pour Kubernetes
  (Deployment + PVC + Ingress facultatif). Chaque service optionnel ci-dessus est une
  option `enabled` dans `values.yaml` - embarquez PostgreSQL / Redis / un serveur S3, ou
  raccordez un ONLYOFFICE / Drawio externe.

Les instructions pas à pas pour chaque configuration se trouvent dans
[docs/INSTALLATION.md](docs/INSTALLATION.md).

filex fonctionne à la racine d’un hôte dédié ou sous un chemin d’un hôte qu’il partage
(`https://example.com/filex/`) : un seul paramètre, `FILEX_BASE_PATH`, et un proxy
qui transmet le chemin complet - exemples pour Caddy, nginx et Helm dans
[docs/DEPLOYMENT.md → Servir filex sous un sous-chemin](docs/DEPLOYMENT.md#serving-filex-under-a-sub-path).

## Intégrer à votre application

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

Il n’y a **aucune feuille de style à importer** - l’apparence voyage dans le bundle et
s’injecte au montage, il ne manque donc rien à cet extrait. ⚠ Avec un bundler, il
faudra externaliser les paquets optionnels des visionneuses (`monaco-editor` et consorts),
ce que [docs/INTEGRATION.md](docs/INTEGRATION.md) montre en une ligne
`rollupOptions.external` ; chacun de ces imports est protégé, si bien que les visionneuses
se dégradent au lieu de casser.

### JavaScript natif / tout framework
```html
<script type="module" src="https://cdn.jsdelivr.net/npm/@brftech/filex/dist/filex.js"></script>
<filex-explorer api-base="http://localhost:5212" sidenav connections ui-profile="simple"></filex-explorer>
```

`sidenav` active le panneau de navigation (il est activé par défaut ; l’attribut est
là pour qu’une page hôte puisse le dire dans un sens comme dans l’autre), `connections`
y ajoute les entrées « Guide de connexion » et « Clés d’API », et `ui-profile="simple"`
désactive par défaut les éléments d’interface pour utilisateurs avancés. Tous trois sont
des clés `config` ordinaires, les composants Vue et React les définissent donc de la
même façon - voir [docs/INTEGRATION.md](docs/INTEGRATION.md).

Les hôtes multilocataires servent généralement de proxy à l’API côté serveur, injectent
une **clé confinée** (`root: tenant-folder`) à chaque requête et retirent les en-têtes du
client - le cloisonnement est imposé par le backend, pas par le composant. Une telle clé
porte `kind: "app"`, le panneau masque donc les éléments propres à une personne - Clés
d’API, Récents, Favoris, Partagés avec moi - tandis que Téléverser, les stockages, la
Corbeille et « Guide de connexion » restent. Voir
[docs/INTEGRATION.md](docs/INTEGRATION.md) et
[docs/MCP.md](docs/MCP.md#token-kinds---user-vs-app).

⚠ Une intégration qui s’appuie sur le **cookie de session** filex du visiteur lui-même
(sans jeton) depuis une page d’une autre origine - sous-domaine voisin compris - lit
comme avant, mais chaque modification qu’elle envoie est refusée
(`403 cross_origin_refused`) tant que cette origine ne figure pas dans
`FILEX_CORS_ALLOWED_ORIGINS` ; la valeur par défaut `*` ne l’autorise pas. Un jeton
Bearer, un hôte qui sert de proxy avec une clé, l’application de bureau et l’application
web installée n’ont besoin de rien
([docs/CONFIGURATION.md](docs/CONFIGURATION.md#requests-from-other-origins)).

## Application de bureau et CLI

L’explorateur est aussi livré sous forme d’**application de bureau Windows / Linux / macOS** -
le même composant que dans l’interface web et les intégrations, pas une demi-copie à part :

- **Plusieurs comptes à la fois** - une barre de serveurs/locataires, chacun affichant sa
  propre image de marque.
- **Faites glisser des fichiers vers l’extérieur** - faites glisser une sélection sur le
  bureau ou dans une autre application : les dossiers et les sélections multiples arrivent
  sous forme de vrais fichiers et dossiers distincts. Pour tout ce qui est déjà conservé
  sur cet ordinateur, le glisser-déposer est instantané ; le reste est récupéré une fois
  et mis en cache ([docs/DESKTOP.md](docs/DESKTOP.md#dragging-files-out)).
- **Conserver sur cet ordinateur** - faites un clic droit sur n’importe quel dossier,
  fichier ou stockage entier pour en garder un miroir sous un seul dossier filex de la
  machine (modifiable depuis les Paramètres) ; tout le reste demeure en ligne uniquement,
  et chaque ligne dit ce qu’il en est (✓ ◐ ⟳ ☁). « Conserver en ligne uniquement » envoie
  la copie locale à la Corbeille, ou la laisse en place
  ([docs/DESKTOP.md](docs/DESKTOP.md#keeping-folders-on-this-computer)).
- **Synchronisation de dossiers** - associez un dossier local à un dossier du serveur et
  ils restent synchronisés dans les deux sens tant que l’application réside dans la zone
  de notification, **en direct** : un enregistrement dans le navigateur est sur le disque
  en une seconde environ, et un enregistrement local sur le serveur tout aussi vite (le
  moteur suit le flux de modifications du serveur et le système de fichiers, avec une
  vérification complète toutes les 30 s comme filet de sécurité), les deux versions
  conservées quand les deux côtés changent en même temps, des transferts et des listes en
  parallèle, une première exécution qui reprend là où elle a été interrompue, une
  corbeille locale de 30 jours, et un moteur qui refuse de transformer un dossier manquant
  en suppression massive ([docs/SYNC.md](docs/SYNC.md)).
- **Ouvre les documents Office depuis votre propre disque** - double-cliquez sur un
  `.docx`/`.xlsx`/`.pptx` (ou sur n’importe lequel des dix types Office, ou sur un `.csv`) et il s’ouvre
  dans l’éditeur que fait tourner votre serveur, sur une machine où Office n’est pas
  installé. Quand le document se trouve dans un dossier que vous conservez sur cet
  ordinateur, c’est lui-même qui s’ouvre ; tout autre document est copié sur le serveur,
  modifié, puis réécrit par-dessus l’original - ou à côté, quand l’éditeur l’enregistre dans un
  autre format (un ancien `.doc` revient en `.docx`)
  ([docs/DESKTOP.md](docs/DESKTOP.md#opening-documents-from-your-computer)).
- **Monter comme lecteur** - un bouton des Paramètres attache le serveur comme lecteur du
  système d’exploitation via WebDAV, et un autre le détache ; le jeton du compte sert
  d’identifiant et n’apparaît jamais sur une ligne de commande. Testé sous Windows ; les
  variantes macOS et Linux existent mais ne sont pas encore vérifiées
  ([docs/DESKTOP.md](docs/DESKTOP.md#mounting-the-server-as-a-drive)).
- **⌘K cherche dans tous les comptes** de la barre, résultats regroupés sous un badge par
  compte, chacun utilisant sa propre connexion pour la recherche, le téléchargement et le
  glisser-déposer vers l’extérieur ([docs/SEARCH.md](docs/SEARCH.md)).
- **Vos notifications et votre compte dans la fenêtre** - la barre supérieure se termine
  comme celle de l’application web : la **cloche** (nombre de notifications non lues,
  lignes les plus récentes, *Tout marquer comme lu*, liste complète) et l’**avatar** avec
  *Paramètres utilisateur* - la boîte de dialogue de paramètres de l’application web
  elle-même, ouverte **dans la fenêtre** - et *Panneau d’administration* pour un
  administrateur. Un clic sur une notification aboutit dans la fenêtre : le dossier, avec
  le fichier sélectionné. La déconnexion reste dans l’application elle-même, sous *Settings → Accounts*
  (Paramètres → Comptes) ([docs/DESKTOP.md](docs/DESKTOP.md#notifications-and-your-account)).
- **Se connecte par votre navigateur**, donc le SSO et la MFA se comportent exactement
  comme sur le web.
- **Se met à jour toute seule** - télécharge discrètement, installe quand vous quittez
  l’application ; `FILEX_NO_UPDATE=1` désactive ce comportement.
- **Fonctionne sans installation**, si c’est ce qu’il vous faut : le `.exe` **portable**
  pour Windows, l’AppImage Linux et le `.zip` macOS s’exécutent tous depuis l’endroit où
  vous les placez. La copie portable Windows conserve tout ce qu’elle a dans un seul
  dossier `filex-data` à côté d’elle, si bien que supprimer ce dossier ne laisse rien qui
  vous appartienne sur une machine qui ne vous appartient pas - en contrepartie, elle ne
  se met pas à jour toute seule.

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

La version du Store (*filex File Manager*) est la seule copie Windows dont le code est
signé - c’est Microsoft qui la signe - et le Store la tient à jour. winget
(`BRFTech.filex-app`) est soumis avec chaque version et attend son premier examen par les
modérateurs de winget ; `winget install` ne le trouve donc pas encore. Le programme
d’installation, le `.exe` portable, l’AppImage, le `.deb`, le `.rpm` et le `.dmg` sont
joints à la [dernière version](https://github.com/BRF-Tech/filex/releases/latest) - pas
encore signés, attendez-vous donc à une invite SmartScreen avec le programme
d’installation Windows. Détails : [docs/DESKTOP.md](docs/DESKTOP.md). La CLI seule :
`brew install brf-tech/filex/filex` ([docs/CLI.md](docs/CLI.md) ; son paquet winget,
`BRFTech.filex`, attend le même examen).

Sous Linux, le `.deb`, le `.rpm` et l’AppImage ne s’exécutent jamais sans le bac à sable
de Chromium. Le `.deb` et le `.rpm` n’ont besoin de rien ; à partir d’Ubuntu 23.10,
l’AppImage a besoin d’un profil AppArmor à installer une seule fois, et l’application le
dit et montre l’étape ([docs/DESKTOP.md](docs/DESKTOP.md#appimage-on-recent-ubuntu)). Le
Snap s’exécute sans le bac à sable de Chromium, dans le confinement strict du snap, et
n’a besoin de rien non plus ([docs/DESKTOP.md](docs/DESKTOP.md#the-snap-and-the-sandbox)).

**ARM (arm64)** - ce qui est livré pour cette architecture (à chaque version, tout cela
est compilé et exécuté sur des machines arm64 avant publication) :

| | arm64 |
|---|---|
| Binaire serveur + CLI | Linux, macOS et Windows : `filex-<os>-arm64` et les archives `.tar.gz` / `.zip` |
| Images Docker (`ghcr.io/brf-tech/filex`, full et slim) | multi-architecture - `docker pull` choisit arm64 tout seul |
| Application de bureau - Linux | `filex-desktop-arm64.AppImage`, `filex-desktop-arm64.deb`, `filex-desktop-aarch64.rpm`, et le Snap Store (`sudo snap install filex-app` choisit arm64) - depuis la 0.48.1 |
| Application de bureau - Windows on Arm | `filex-desktop-arm64.exe` (programme d’installation) et `filex-desktop-portable-arm64.exe` - depuis la 0.48.1 ; l’application se met à jour toute seule vers la version arm64 |
| Application de bureau - macOS | Apple Silicon uniquement (pas de version Intel) |
| Homebrew | la CLI (`filex`) sur Apple Silicon et sous Linux sur Arm ; l’application de bureau (`filex-app`) sur Apple Silicon |

Sur une machine Arm, la proposition *Obtenez l’application de bureau* de
l’application (et sa copie dans les Paramètres) présente d’abord le fichier arm64,
et la liste de téléchargements sur [filex.sh](https://filex.sh/#downloads) le met
en évidence, d’après ce qu’indique le navigateur (les client hints de Chromium, le
`aarch64` de Firefox). Un navigateur qui ne le dit pas (Safari, Firefox sous
Windows) se voit proposer le fichier x64, avec le fichier arm64 à côté.

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

## Agents IA / MCP

filex est livré avec une surface d’automatisation authentifiée par clé d’API sur `/api/ai`
(liste, lecture, écriture, déplacement, copie, suppression, recherche, partage, zip) et
parle le **Model Context Protocol** sur `/api/ai/mcp`. Un agent exécute aussi les opérations
propres à l’explorateur - la copie entre stockages, les actions d’application telles que
**convertir**, la file d’attente des opérations, la corbeille et l’historique des versions,
les archives 7z/TAR, ses liens et ses demandes de fichiers, la cloche, les favoris, les
commentaires et les autorisations sur un élément dont il est propriétaire - en passant par
les gestionnaires de requêtes de l’explorateur lui-même : les règles sont donc celles de
l’explorateur. Une clé d’administration donne accès à ce que fait le panneau (locataires,
fournisseurs d’identité, sécurité de connexion, Applications par défaut, webhooks,
stockages) par les gestionnaires de requêtes du panneau lui-même. `/api/ai` et `/api/files`
sont décrits dans un [fichier OpenAPI 3.1](backend/internal/api/openapi.json) :

```bash
claude mcp add filex --transport http https://files.example.com/api/ai/mcp \
  --header "Authorization: Bearer <api-token>"
```

Une clé d’API porte des autorisations par verbe (`read`, `write`, `delete`, plus `mcp` et
`admin`), peut être **confinée à un seul dossier**, est soumise aux mêmes droits d’accès et
rôles RBAC que l’interface, et est marquée d’une identité par clé, afin que les journaux
d’audit, les partages et la présence montrent *qui* (quelle intégration) a fait quoi. Les
verbes valent pour **chaque surface à laquelle la clé donne accès** - `/api/ai`, les outils
MCP, les routes propres à l’explorateur (et donc `filex client` et une intégration), WebDAV,
SFTP, FTPS, ainsi que les clés S3 et les exports NFS créés à partir de cette clé. Certains
actes ne sont jamais du ressort d’une clé : installer une extension - un agent **dépose une
demande d’installation** qu’un administrateur approuve dans le panneau - et nommer quelqu’un
administrateur. Une clé doit nommer au moins une autorisation - une liste vide est refusée,
jamais lue comme « toutes » - et **ce qu’elle délivre ne peut jamais être plus large que la
clé elle-même** : si l’on demande, au moyen d’une clé en lecture seule ou confinée à un
dossier, une clé d’API, une clé d’accès S3, un export NFS ou une clé SSH ayant plus de
verbes, une racine hors de la sienne ou une durée de vie plus longue, la demande est refusée
avec `403 token_ceiling`, qui nomme ce qui était trop large. Un **déplacement fait par un
agent n’écrase jamais rien** : un élément qui vise un nom déjà pris arrive à côté, sous un
nom libre (`report-copy.txt`), exactement comme lors d’un déplacement dans l’interface, et
la réponse nomme le chemin où il est réellement arrivé. Et il connaît les **dossiers
chiffrés** : chaque ligne indique si elle est chiffrée, les données chiffrées ne sont jamais
remises comme si elles étaient le fichier (`409 E2E_ENCRYPTED`), et les données en clair
écrites dans un dossier chiffré sont refusées, sauf si l’appelant dit que c’est bien ce
qu’il veut (`allow_plaintext`) ([docs/MCP.md](docs/MCP.md#encrypted-folders-and-fxe)).

Un gros fichier déjà présent sur le disque de l’agent ne tient jamais dans un appel
d’outil - il faudrait que ses octets transitent par le contexte du modèle. Les **tickets de
téléversement** y remédient : un seul appel autorisé fixe la destination et renvoie une URL
de courte durée, à usage unique, qui ne demande **aucun identifiant**, de sorte que même un
agent sans aucune clé filex peut terminer le transfert avec `curl -T bigfile <url>`.
Détails : [docs/MCP.md](docs/MCP.md).

## Applications

Une **extension de stockage** apprend à filex un backend dont il n’a jamais
entendu parler. Une **application** lui apprend une *chose à faire avec des
fichiers* - les signer, les convertir, les envoyer à quelqu’un de l’extérieur - et
c’est à dessein un autre type d’extension : un **module WebAssembly qui s’exécute
dans filex, dans un bac à sable** qui ne lui remet rien d’autre que ce qui lui a
été accordé. Pas de système de fichiers, pas de réseau, pas d’environnement, aucun
programme sur votre serveur : seulement les fonctions hôtes que demande son
manifeste, chacune présentée à l’administrateur en termes simples avant toute
installation, et seulement les fichiers que la personne qui l’a lancée a
réellement sélectionnés. Les moteurs lourds que peut vouloir une application
(ffmpeg, ImageMagick, Ghostscript, poppler, rsvg) sont ceux du serveur, proposés à
raison d’une autorisation par moteur ; les documents Office passent par
l’ONLYOFFICE Document Server que vous connectez, en tant que moteur bureautique -
filex n’exécute aucun LibreOffice.

Une application peut aussi apporter - ou n’être rien d’autre que - **une interface
qui lui est propre** : du HTML, du CSS et du JavaScript écrits par son auteur, un
éditeur ou une visionneuse pour un format. filex la sert depuis le paquet que vous
avez approuvé (épinglé par son SHA-256) dans un **cadre en bac à sable** : une
origine opaque qui ne peut lire ni la session, ni les cookies, ni les pages de
filex, une politique de contenu que filex rédige à partir de ce qui a été accordé
à l’application (aucune connexion réseau, aucun stockage, aucun formulaire, aucune
fenêtre pop-up), et un seul canal de messages contrôlé, par lequel filex ne lui
remet que les fichiers avec lesquels elle a été ouverte et enregistre par-dessus -
une nouvelle version, ou un brouillon. Elle peut ajouter son propre type de
fichier à **Nouveau document**, s’ouvrir dans l’onglet de l’éditeur et vous
remettre un fichier à conserver - chaque fois que vous l’autorisez. ⚠ Les
navigateurs ne peuvent pas entièrement empêcher une page d’envoyer des données
vers l’extérieur (WebRTC ne tient pas compte d’une politique de contenu ; Chrome
laisse filex le fermer, dans Firefox, filex ne peut que le retirer de la page - une
ceinture de sécurité, pas un mur), si bien que la vérification à l’installation le
dit sans détour : **ne faites confiance à une application dotée d’une interface
que dans la mesure où vous confieriez à son auteur les fichiers que vous y
ouvrez.**

Ce qu’une application ajoute se trouve là où se trouve tout le reste : des lignes
dans le menu des fichiers, des écrans que filex affiche pour elle ou sa propre
interface, des tâches dans la même file d’attente qu’une copie - avec la
progression, **Annuler** et un résultat versionné, analysé et indexé comme toute
autre écriture - une section dans les détails d’un fichier, un écran d’accueil
sous **Applications** dans la navigation et, lorsqu’elle a besoin de quelqu’un qui
n’a pas de compte, un lien qui est un **partage** ordinaire : dans la même liste,
soumis à la même politique de verrouillage du PIN et d’expiration, révocable par
vous comme tout autre lien. Une application qui le demande est aussi réveillée une
fois par heure pour faire son propre travail planifié - une demande de signature
qui se clôt d’elle-même à sa date limite et envoie les rappels que vous avez
demandés.

Les applications n’exécutent pas toutes du code. Un **pack de langue** est un
manifeste de chaînes et rien d’autre : il s’installe à partir du seul manifeste -
pas de module, pas de Go, pas de version publiée - ne démarre jamais
d’environnement d’exécution, et ajoute sa langue à l’explorateur, au panneau
d’administration, aux pages publiques et au texte qu’écrit le serveur.
**Extensions → Applications** le liste comme *Pack de langue* avec sa couverture
de la version en cours d’exécution, et tout ce qui lui manque s’affiche en
anglais. L’espagnol, l’allemand et le français sont livrés à titre d’exemple, et
`BRF-Tech/filex-lang-template` guide un traducteur de l’export à l’installation.

Quatre applications sont livrées avec filex, sous forme de dépôts publics que vous
pouvez installer, lire et forker. Les deux premières sont des modules ; les deux
dernières ne sont qu’une interface, sans rien qui s’exécute sur le serveur :

| Application | Ce qu’elle ajoute |
|---|---|
| **[Signature électronique](https://github.com/BRF-Tech/filex-sign)** - `BRF-Tech/filex-sign` | **Signer…**, **Demander des signatures…**, **Signer / Remplir** et **Vérifier** sur un PDF, et uniquement sur un PDF (un document Office est d’abord transformé en PDF avec **Convertir**). Une demande tient en un court assistant : qui signe - des personnes de ce filex, qui y signent, et n’importe qui d’autre, désigné par son nom ou son e-mail, qui reçoit un **lien privé**, protégé par un PIN sauf si vous en décidez autrement - dans quel ordre, les zones nommées et attribuées à chaque signataire, puis placées sur la page ; combien de temps la demande reste ouverte, si le fichier est **gelé** pendant ce temps, et si un **journal d’audit PDF** est écrit à la fin. Le résultat est un PDF signé PAdES, **certifié et scellé** : la première signature certifie le document, si bien que les suivantes ne peuvent que remplir et signer, et, lorsque la dernière arrive, **filex scelle lui-même le fichier entier** avec le sceau propre à l’installation, verrouillé de sorte que toute modification ultérieure est signalée comme non autorisée. Le **SHA-256 de ces octets scellés, et de ceux-là exactement**, l’empreinte du sceau et la manière de les vérifier sont transmis au demandeur et à chaque signataire, interne comme externe, et inscrits dans le journal d’audit. En option, le fichier signé reste **verrouillé dans filex** jusqu’à ce qu’un administrateur lève le verrou. **Les clés de signature ne quittent jamais le serveur** : l’autorité de certification propre à l’instance (ou celle que vous importez) délivre un certificat par signataire, et la clé qui a produit une signature est détruite quelques secondes plus tard - la clé du sceau est la seule exception, détenue par l’hôte et jamais communiquée. |
| **[Convertir](https://github.com/BRF-Tech/filex-convert)** - `BRF-Tech/filex-convert` | **Convertir…** sur n’importe quel fichier : images, vidéo, audio, documents, livres numériques, archives, données, sous-titres et polices. La cible se choisit parmi des boutons regroupés sous leur catégorie ; viennent ensuite les seuls paramètres qui comptent pour elle, puis une vérification. La plupart des conversions s’exécutent en Go pur dans le bac à sable ; les autres passent par les moteurs du serveur lorsqu’ils sont installés, et une cible qui a besoin d’un moteur manquant le dit au lieu d’être absente sans rien dire. |
| **[filextext](https://github.com/BRF-Tech/filextext-app)** - `BRF-Tech/filextext-app` | Un **espace de travail texte chiffré de bout en bout** dans un seul fichier `.fxtxt` : pages et dossiers à gauche, onglets en haut, l’éditeur BlockSuite d’AFFiNE au milieu (titres, listes, tâches, code, tableaux, images, liens entre pages, Markdown en entrée et en sortie). Il est chiffré **dans votre navigateur** avec les mêmes clés et la même clé de récupération que les [dossiers chiffrés](docs/E2E-ENCRYPTION.md) de filex ; filex stocke des données chiffrées et ne voit jamais le mot de passe ni un seul mot du texte. Un `.fxtxt` s’y ouvre à la place de l’aperçu, et *Espace de travail chiffré (.fxtxt)* s’ajoute à **Nouveau document**. |
| **[draw.io](https://github.com/BRF-Tech/filex-drawio)** - `BRF-Tech/filex-drawio` | L’éditeur de diagrammes **draw.io**, dans filex : les fichiers `.drawio` et `.dio` s’y ouvrent à la place de l’aperçu, **Enregistrer** écrit une nouvelle version, et *Diagramme draw.io* s’ajoute à **Nouveau document**. Les fichiers propres à draw.io sont servis depuis le paquet de l’application (épinglé par son SHA-256) ; l’éditeur n’accède à rien en dehors de ce paquet. |

**Installez-en une depuis GitHub** - *Admin → Extensions → **Applications** →
**Installer une application** → Dépôt GitHub* : saisissez `BRF-Tech/filex-sign` et le
tag de version. filex lit le `filex-app.json` du dépôt, télécharge le module (ou le
paquet de l’interface) que ce fichier désigne et le refuse si son SHA-256 ne correspond
pas, puis s’arrête à la **vérification des autorisations**. Rien n’est installé tant que
vous n’avez pas lu chaque autorisation et coché *Je comprends* ; ce qui est accordé à
l’application, c’est exactement cette liste, et une mise à jour qui en demande davantage
s’arrête de nouveau à la vérification. `FILEX_PLUGIN_TRUSTED_KEYS` rend obligatoire la
signature des modules (une installation depuis GitHub ne comporte aucune signature,
donc, sur une telle instance, téléversez plutôt le module avec sa signature). Chaque
téléchargement - une application, sa recherche de mises à jour, une extension de
stockage - ne va que vers des adresses publiques, jugées après la résolution DNS et à
chaque redirection : pour installer depuis un serveur de votre propre réseau, téléversez
les fichiers. Les applications sont désactivées en mode démo. Une clé d’API - un agent,
un script, la CLI - ne peut pas en installer une : elle **dépose une demande**, filex
fige les octets et les autorisations qu’il installerait, et un administrateur l’approuve
dans **Extensions → Demandes d’installation**.

**Qui peut l’utiliser.** Une application peut déclarer **ses propres autorisations** -
une application de signature soumet *Demander des signatures* à l’une d’elles, alors que
signer ce qui vous a été envoyé n’en exige aucune - et vous les accordez par rôle et par
personne, comme celles de filex ; une action dont quelqu’un ne dispose pas ne figure pas
dans son menu, et elle est refusée s’il la demande
([Autorisations des applications](docs/APP-PLUGINS.md#app-permissions)).

**Rien ne se met à jour tout seul.** Une fois par jour (et à chaque clic sur
**Rechercher des mises à jour**), filex interroge l’endroit d’où vient chaque
application - ses versions publiées sur GitHub, la branche d’un pack de langue, ou
l’adresse du manifeste depuis laquelle elle a été installée - pour savoir s’il existe
une version plus récente qui peut s’exécuter sur ce filex, et vous le dit : elle attend
sous *Mise à jour disponible* (ou *Approbation requise*, lorsqu’elle demande davantage)
jusqu’à ce qu’un administrateur ait vérifié ce qu’elle change - autorisations, module,
fichiers de l’interface, notes - et l’ait approuvée, et tout le monde utilise alors
cette version. **Revenir à *version*** remet en place celle qu’elle a remplacée. Les
extensions de stockage peuvent suivre une source de la même façon. Une application
dit avec quelles versions de filex elle fonctionne (`"filex": ">=0.47.0"` dans son
manifeste), et filex ne l’installe pas en dehors de cette plage.

**Guide de l’opérateur** : [docs/APP-PLUGINS.md](docs/APP-PLUGINS.md) - l’installation
et le contrôle des applications, le circuit de signature du début à la fin, le
convertisseur, les réveils planifiés, et ce qui protège les liens publics d’une
application. **En écrire une** (Go standard, `GOOS=wasip1`, avec un kit de test) :
partez du dépôt modèle
[BRF-Tech/filex-app-template](https://github.com/BRF-Tech/filex-app-template) et de
[docs/PLUGIN-KIT.md](docs/PLUGIN-KIT.md) ; le contrat d’échange :
[docs/APP-PLUGINS-API.md](docs/APP-PLUGINS-API.md). L’autre type d’extension, un backend
de stockage : [docs/PLUGINS.md](docs/PLUGINS.md).

## Fonctionnalités

- **Multi-stockage** - montez de nombreux stockages à la fois (local, S3, FTP, SFTP, WebDAV, SMB/NAS) ; chacun apparaît comme un dossier de premier niveau. Chacun porte aussi une adresse qui ne bouge jamais : le nom du stockage est le premier segment du chemin en WebDAV, SFTP, NFS et dans l’API S3, si bien qu’en renommer un changerait son adresse - un montage écrit avec son **uid** survit à tous les renommages. **Copiez ou coupez dans l’un et collez dans l’autre** : filex transfère l’arborescence en flux d’un pilote à l’autre, conserve l’horodatage de chaque fichier et ne supprime l’original qu’une fois la copie vérifiée. Un service de stockage en panne est signalé en quelques secondes, et seul le silence fait expirer le délai, jamais un transfert qui continue d’avancer (S3, WebDAV, FTP, SFTP et SMB : [docs/STORAGE.md](docs/STORAGE.md#when-the-store-is-down)). Une entrée sur laquelle le stockage n’a pas pu se prononcer (ni « présent » ni « introuvable ») est conservée, marquée d’un **!** et de la réponse du stockage lui-même, et l’on n’en fait rien - dans l’explorateur, l’API REST et celle des agents, le partage et les éditeurs (les protocoles de fichiers ne lisent pas la marque) - jusqu’à ce que le stockage réponde de nouveau ([PLUGINS.md](docs/PLUGINS.md#an-entry-your-stat-cannot-answer-for)).
- **Faites glisser des fichiers vers votre bureau** - dans l’application de bureau, faites glisser une sélection dans l’Explorateur de fichiers/le Finder ou un autre programme et elle y arrive sous forme de vrais fichiers et dossiers distincts, pas d’une archive ; dans un navigateur, vous faites glisser un fichier seul vers l’extérieur de la même façon - dans l’application d’administration aussi, par un lien valable pour un seul fichier, qui dure une minute et ne fonctionne qu’une fois ([docs/DESKTOP.md](docs/DESKTOP.md#dragging-files-out)).
- **Extensions de stockage** - un stockage dont filex n’a jamais entendu parler est un **programme distinct** que vous installez depuis le panneau d’administration : il décrit son propre formulaire de configuration, filex lui parle un petit protocole HTTP/JSON, et son pilote se comporte alors comme n’importe quel pilote intégré. N’importe quel langage ; un SDK Go ramène cela à trois méthodes. filex **teste chaque capacité qu’une extension annonce** - à l’installation, puis de nouveau avec la configuration que vous saisissez quand vous enregistrez un stockage qui s’appuie sur elle - et refuse celle qui ne peut pas faire ce qu’elle dit, parce qu’un pilote qui ne fonctionne qu’à moitié produit des pannes qui font croire que filex est cassé. Les mises à jour remplacent le binaire sur place et reviennent à la version précédente si le nouveau ne démarre pas ; chaque démarrage vérifie de nouveau l’empreinte et la signature du binaire, et chaque extension tient un **journal** de ses démarrages, de ses échecs et des entrées sur lesquelles elle n’a pas pu se prononcer (*Actions → Journal*) ([docs/PLUGINS.md](docs/PLUGINS.md)).
- **Applications** - un deuxième type d’extension : un module **WebAssembly en bac à sable**, une **interface qui lui est propre dans un cadre en bac à sable** (aucune connexion réseau, aucun accès à la session de filex ; voir [Applications](#applications) pour ce qu’un navigateur ne peut pas promettre), ou les deux - ajoutant des actions au menu des fichiers (*Demander des signatures…*, *Convertir…*), des écrans que filex affiche pour elle, une section dans les détails d’un fichier, un écran d’accueil sous **Applications** dans la navigation, et des liens qu’un participant externe ouvre sans compte. Installée depuis un dépôt GitHub en passant par une **vérification des autorisations** - l’application obtient exactement ce que vous avez approuvé et rien d’autre : pas de système de fichiers, pas de réseau, aucun programme sur votre serveur ; les moteurs lourds (ffmpeg, ImageMagick, …) sont ceux du serveur et les documents Office passent par l’ONLYOFFICE que vous connectez, le tout proposé une autorisation à la fois. Les écrans que filex affiche obéissent aux règles de filex, quel que soit leur auteur - chaque choix visible plutôt que caché dans une liste déroulante, rien de replié derrière « avancé », une question par étape. Le lien qu’une application envoie à un signataire externe est un **partage** ordinaire, vous le voyez et le révoquez donc dans la même liste que tout le reste, et il ne vaut jamais plus que son créateur : une tâche lancée depuis ce lien passe les mêmes contrôles qu’une tâche lancée dans filex (une action que vous avez désactivée reste désactivée, l’accès du créateur au document est relu), et il cesse de fonctionner quand le compte du créateur est désactivé - jusqu’à ce qu’il soit réactivé. Une application peut aussi apporter **sa propre interface** - du HTML et du JavaScript que filex sert depuis le paquet approuvé de l’application dans un cadre en bac à sable dont la politique n’autorise aucune connexion réseau, aucun stockage et aucun cookie, et qui dialoguent avec filex par un seul canal contrôlé ; un éditeur qui n’a besoin de rien sur le serveur (draw.io, filextext) est une application sans aucun module ([l’interface propre à une application](docs/APP-PLUGINS.md#an-apps-own-interface), SDK `@brftech/filex-app-ui`). Une application à laquelle vous accordez `schedule` est réveillée une fois par heure pour faire son propre travail à la minute qu’elle a choisie, comme une tâche ordinaire dans la file d’attente. Rien ne se met à jour tout seul : filex consulte chaque jour la source de chaque application et dit quand il existe une version plus récente, un administrateur vérifie ce qu’elle change et l’approuve, tout le monde utilise la version approuvée - et **Revenir à *version*** annule une approbation. Les applications disent avec quelles versions de filex elles fonctionnent. Une clé d’API n’en installe jamais - elle dépose une **demande d’installation** qu’un administrateur approuve - et une application peut déclarer **ses propres autorisations**, que vous accordez par rôle et par personne ([Autorisations des applications](docs/APP-PLUGINS.md#app-permissions)). Une application peut **générer les miniatures** de types de fichiers pour lesquels filex n’en génère pas (elle reçoit les octets d’un seul fichier et rien d’autre), et **Applications par défaut** décide, par type de fichier, quelle application l’ouvre et laquelle génère sa miniature, et dans quel ordre ; chaque personne choisit parmi les applications laissées activées pour l’ouverture, et *Toujours utiliser cette application* est conservé sur son compte - un seul choix pour le navigateur, l’application de bureau et une intégration ([Applications par défaut](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail)). Quatre applications sont livrées sous forme de dépôts publics : **Signature électronique**, **Convertir**, **filextext** et **draw.io** ([Applications](#applications), [docs/APP-PLUGINS.md](docs/APP-PLUGINS.md#the-apps-that-ship-alongside-filex), [en écrire une](docs/PLUGIN-KIT.md)).
- **Signature électronique** ([`BRF-Tech/filex-sign`](https://github.com/BRF-Tech/filex-sign), une application) - signez un PDF vous-même, ou demandez à d’autres de le faire : les personnes de ce filex y signent, depuis une notification qui ouvre le bon écran ; n’importe qui d’autre reçoit un lien privé, protégé par défaut par un PIN - un PIN que filex conserve pour vous (voir *Partage*). Les zones sont **définies d’abord** - un nom, à qui la zone appartient, obligatoire ou non, le format d’une date - et **placées ensuite sur la page**, deux questions sur deux écrans. Le document peut être **gelé** pour tout le monde, administrateurs compris, tant que la demande est en cours ; les rappels et la date limite fonctionnent tout seuls ; le demandeur suit chaque signataire dans les détails du fichier et sur l’écran d’accueil de l’application ; et le résultat est un PDF signé PAdES, **certifié** par sa première signature et **scellé par filex** après la dernière, de sorte qu’un lecteur PDF signale toute modification ultérieure comme non autorisée - avec le **SHA-256 des octets scellés** et l’empreinte du sceau envoyés au demandeur et à chaque signataire, un **journal d’audit PDF** quand vous en demandez un, un reçu pour chaque signataire, et une option pour garder le fichier terminé verrouillé jusqu’à ce qu’un administrateur lève le verrou. **Vérifier** établit un rapport sur n’importe quel PDF signé : chaque signature, la certification, le sceau, et s’il s’agit bien du fichier dont l’empreinte a été envoyée. Les clés de signature ne quittent jamais le serveur : l’autorité de certification propre à l’instance, ou celle que vous importez, délivre un certificat à chaque signataire, et la clé qui a produit une signature est détruite quelques secondes plus tard ([docs/APP-PLUGINS.md](docs/APP-PLUGINS.md#signing-documents-end-to-end)).
- **Convertir, sous forme d’application** ([`BRF-Tech/filex-convert`](https://github.com/BRF-Tech/filex-convert)) - *Convertir…* sur n’importe quel fichier ou n’importe quelle sélection : images, vidéo, audio, documents, livres numériques, archives, données, sous-titres et polices. La cible est un bouton sous sa catégorie, puis viennent les seuls paramètres qui comptent pour elle, puis une vérification ; la plupart des conversions s’exécutent en Go pur dans le bac à sable, les autres passent par les moteurs du serveur, et une cible qui a besoin d’un moteur manquant est listée comme telle au lieu d’être absente sans rien dire ([docs/APP-PLUGINS.md](docs/APP-PLUGINS.md#converting-files)). (L’ancien conteneur annexe du convertisseur en iframe a été supprimé dans la version 0.48.)
- **Passerelle de protocoles** - la même arborescence est accessible en **S3** (SigV4 ; aws-cli, rclone, restic, mc, s3fs), **SFTP** (OpenSSH, WinSCP, FileZilla, sshfs), **FTPS** (TLS explicite, pour les équipements qui n’ont appris que le FTP ; donnez-lui le certificat à renouvellement automatique de votre proxy inverse - il est relu dès qu’il change), **NFSv3** (clients NAS du LAN, lecteurs multimédias) et **WebDAV** - chacun avec son propre identifiant, que vous pouvez révoquer séparément, et tous soumis aux mêmes autorisations, à la même corbeille et au même quota que l’interface ([docs/PROTOCOLS.md](docs/PROTOCOLS.md)).
- **`filex mount`** - attachez un serveur filex distant à un dossier en HTTPS ordinaire : un dossier sous Linux, une **lettre de lecteur sous Windows** (`filex mount Z:`, nécessite [WinFsp](https://winfsp.dev), gratuit). Ce n’est pas une synchronisation : rien n’est copié, hormis un cache de lecture borné, si bien que le montage ouvre un fichier sur cent mille sans télécharger le reste.
- **Collaboration en temps réel** - barre de présence avec avatars en direct + focus, mises à jour instantanées par WebSocket à chaque modification de fichier, repli sur l’interrogation périodique. Une écriture isolée est annoncée à l’instant où elle arrive ; une salve (l’extraction d’un zip, le téléversement d’un dossier, un client NFS qui écrit morceau après morceau) est fusionnée en une seule trame par fenêtre de temps, pour que le dossier reste en direct sans inonder la page ([docs/REALTIME.md](docs/REALTIME.md)).
- **Une liste qui se comporte comme un tableau** - redimensionnez une colonne, masquez-en une, faites-en glisser une vers un nouvel emplacement ; le tableau défile horizontalement plutôt que de faire disparaître une colonne quand la place manque, et la colonne des actions reste épinglée à droite. Triez par nom, type, date ou taille, dans un sens ou dans l’autre, et **la grille et la liste obéissent au même tri** - jusqu’à cette version, « trié par taille » ne valait que pour une vue, et changer de vue réordonnait les lignes sous vos yeux. Avec le tri par date, les trois vues regroupent les lignes sous **Aujourd’hui · Hier · Cette semaine · Ce mois-ci** puis mois par mois, dans **votre** fuseau horaire plutôt que dans celui du navigateur.
- **Un dossier se souvient de la façon dont vous l’avez laissé** - facultatif, depuis les paramètres utilisateur : la vue et le tri de chaque dossier que vous avez effectivement configuré, conservés **par personne sur le serveur** pour qu’ils vous suivent sur une autre machine et dans l’application de bureau, et ne déteignent jamais sur quelqu’un d’autre qui regarde le même dossier. Désactivé par défaut, auquel cas votre dernier choix s’applique simplement partout ([docs/INTEGRATION.md](docs/INTEGRATION.md)).
- **À qui appartient un fichier** - chaque nœud enregistre son propriétaire, la liste a une colonne **Propriétaire** et la rangée de filtres une entrée **Propriétaire**, et le quota est imputé au propriétaire plutôt qu’à la dernière personne à avoir touché le fichier.
- **Des archives dans tous les formats courants** - créez, ouvrez et extrayez des archives
  ZIP, 7z, TAR et ses variantes gzip/bzip2/xz (RAR là où le 7-Zip du serveur le prend en
  charge), avec un mot de passe pour ZIP et 7z, en tâche d’arrière-plan avec suivi de la
  progression. Les liens et les périphériques contenus dans une archive sont refusés avant
  toute écriture, et des limites de taille et de nombre d’entrées arrêtent une bombe de
  décompression dès la limite atteinte ([docs/ARCHIVES.md](docs/ARCHIVES.md)).
  Contribution d’Alex (@ahjephson).
- **Emportez une sélection** - sélectionnez plusieurs fichiers et dossiers, et **Télécharger** les envoie en flux sous la forme d’une seule archive, construite à la volée : aucun fichier temporaire n’est écrit dans votre stockage, rien n’est mis en mémoire tampon dans l’onglet, et une archive de 700 Mo coûte au serveur moins d’un mégaoctet de mémoire. **Déplacer vers** et **Copier vers** ouvrent un sélecteur de dossier qui couvre tous les stockages et refuse toute destination où vous ne pouvez pas écrire - côté serveur, pas seulement dans la boîte de dialogue.
- **Nouveau document** - créez un fichier Word, Excel, PowerPoint ou OpenDocument, ou dans tout format de texte ou de code, depuis le menu **+ Nouveau** : nommez-le - n’importe quel nom, `LICENSE`, `Makefile` ou `test.conf` compris - choisissez son emplacement, et il s’ouvre dans l’éditeur qui le prend en charge. Les modèles sont de vrais documents, minimaux et valides, compilés dans le binaire, si bien que cela fonctionne sur une installation sans aucune suite bureautique ; un type que ce déploiement ne pourrait pas ouvrir ensuite n’est même pas proposé, et la boîte de dialogue dit pourquoi. Un nouveau document est un **brouillon** jusqu’à son premier enregistrement : rien n’apparaît dans le dossier jusqu’à ce que vous appuyiez sur Enregistrer (un nom pris entre-temps donne lieu à une question - `report (2).txt` ? - jamais à un remplacement), fermer le document propose *Enregistrer sur le disque / Garder dans Brouillons / Abandonner*, et **Brouillons**, dans le panneau de navigation, garde ceux que vous n’avez pas terminés, que personne d’autre ne voit - 50 par personne par défaut, réglable sur la page d’administration Protection ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md#drafts-nothing-is-in-the-folder-until-you-save)).
- **RBAC + autorisations par élément** - rôles (Administrateur, Utilisateur, Lecteur et rôles personnalisés, chacun étant une liste d’autorisations - [docs/PERMISSIONS.md](docs/PERMISSIONS.md)), droits d’accès par fichier/dossier avec héritage dans **Admin → Accès aux dossiers**, **groupes** qui détiennent un accès aux dossiers et un rôle pour tous ceux qui en font partie - membres ajoutés à la main ou gardés synchronisés avec les groupes que transmet une connexion ([docs/GROUPS.md](docs/GROUPS.md)) - invitations de partage par e-mail (SMTP), recherche et listes qui tiennent compte des droits d’accès. **Partagés avec moi** répond à la question inverse, du côté du destinataire - ce que d’autres personnes vous ont accordé, et les stockages que vous n’atteignez que par un droit d’accès.
- **L’interface commune** - une seule mise en page, pour l’opérateur comme pour l’utilisateur final, dans l’application d’administration, l’application de bureau et chaque intégration : une barre supérieure sur toute la largeur avec le bouton de réduction et le logo du produit sur son bord gauche, un seul **champ de recherche** dont la pastille ⌘K / Ctrl+K transmet la requête à la palette de commandes (le champ cherche dans ce dossier ; c’est dans la palette que se trouvent « partout », les recherches enregistrées et les commandes), un menu principal **+ Nouveau** (téléverser des fichiers · nouveau dossier · **nouveau document** · demander des fichiers), une rangée de filtres **Type · Propriétaire · Modifié · Taille** sous le fil d’Ariane, **Dossiers** et **Fichiers** en sections titrées dans la vue grille, un panneau de détails divisé en **Détails** (avec « Personnes ayant accès » et une ligne pour le lien de partage) et **Activité** (historique des versions et commentaires), et une **ligne de stockage** sous la navigation. Le thème, la palette, la langue, la densité, le fuseau horaire, la page de démarrage et les options de notification se trouvent tous dans les **paramètres utilisateur**, accessibles depuis l’avatar - et l’application web conserve votre thème, votre palette, votre densité et votre langue sur votre **compte**, pas dans le navigateur, si bien qu’ils vous attendent dans le suivant ; l’éditeur de raccourcis clavier et *Relancer la visite guidée* sont dans le même menu. Rien n’est retiré du build - une intégration, qui n’a pas de boîte de dialogue de paramètres, garde un menu « ⋯ » qui les contient encore ([docs/INTEGRATION.md](docs/INTEGRATION.md)).
- **Accueil, dans l’interface commune** - la vue sur laquelle tout le monde arrive, administrateurs compris : vos stockages, ce que vous avez ouvert en dernier et ce que vous avez mis en favoris, sous forme de cartes dans la zone de contenu, avec le même panneau de navigation et le même en-tête que les fichiers. Passer d’Accueil à un dossier, ou l’inverse, change le contenu et rien d’autre. Un opérateur qui préfère arriver sur le tableau de bord d’administration le choisit dans ses paramètres de profil.
- **Panneau de navigation** - le menu **+ Nouveau** comme action principale, les destinations Accueil / Mes fichiers / Partagés avec moi / **Mes partages** / Récents / Favoris / **Brouillons** / Corbeille, les stockages que vous pouvez voir - **dans l’ordre que vous choisissez** (faites glisser une ligne, ou Monter / Descendre / Trier par nom depuis son menu ; conservé sur votre compte), sinon dans l’ordre défini par l’administrateur sur la page Stockages ([docs/STORAGE.md](docs/STORAGE.md#ordering-storages)) - une section **Applications** quand une application installée a un écran d’accueil, et **Guide de connexion** + **Clés d’API** : les guides par protocole et le gestionnaire de clés en libre-service, ouverts depuis l’explorateur lui-même pour que les utilisateurs d’une copie intégrée puissent créer l’identifiant que demandent WebDAV/FTPS/`filex mount` au lieu de s’adresser à un administrateur. Réductible en barre d’icônes (mémorisé par navigateur) depuis la barre supérieure, un tiroir au lieu d’une colonne en dessous de 560 px. Activé par défaut dans l’application web, l’application de bureau et chaque intégration ; `uiProfile: 'simple'` désactive en outre la barre d’onglets, la vue partagée, la vue galerie et la partie « Guide de connexion » sans en retirer aucune du build ([docs/INTEGRATION.md](docs/INTEGRATION.md)).
- **Partage** - liens publics avec PIN, expiration et limite de téléchargements, sans dépasser la **durée de vie maximale des liens** fixée par l’administrateur (7 jours par défaut - la boîte de dialogue ne propose que ce que le serveur conservera) ; les liens de dossier envoient leur contenu en flux sous forme de ZIP (mis en cache, préchauffé jusqu’à un plafond de taille, purgé au bout d’une semaine) ; liens de dépôt par **demande de fichiers** pour les envois entrants ; endpoint de téléversement compatible ShareX. **Mes partages** liste les liens que vous avez créés - pour tout le monde, pas seulement pour les administrateurs - avec *Copier le lien*, *Copier le PIN* et *Révoquer* : le PIN d’un lien est conservé scellé à côté du hachage qui le protège, si bien que son créateur ou un administrateur peut le relire quand quelqu’un en a de nouveau besoin, et chaque lecture est consignée dans le journal d’audit. Cinq PIN erronés ferment n’importe quel lien public pendant dix minutes. Un lien de téléchargement, une demande de fichiers et la page d’une application sont **un seul écran public à vos couleurs** - le nom, le logo et les couleurs de votre instance, un seul contrôle du PIN, une seule logique d’expiration et un sélecteur de langue ([docs/SHARING.md](docs/SHARING.md)).
- **Application de bureau + synchronisation de dossiers** - application Windows/Linux/macOS : synchronisation bidirectionnelle résidant dans la zone de notification, **synchronisation sélective** (clic droit → *Conserver sur cet ordinateur*, un dossier racine par compte, le reste en ligne uniquement), plusieurs comptes à la fois, **ouvre les documents Office depuis votre propre disque** dans l’éditeur du serveur, mise à jour automatique (macOS : version non signée, mises à jour par nouveau téléchargement jusqu’à ce qu’elle soit signée). Chaque document s’ouvre dans **sa propre fenêtre** (avec le nom du fichier pour titre), les fenêtres sont **sans bordure** avec les boutons propres à l’application (les feux tricolores natifs sous macOS), et **Settings → Open files with** (Paramètres → Ouvrir les fichiers par) permet de choisir entre simple clic et double-clic pour ouvrir ([docs/DESKTOP.md](docs/DESKTOP.md), [docs/SYNC.md](docs/SYNC.md)).
- **Corbeille et historique des versions** - les suppressions sont réversibles pendant la durée de rétention, les écritures conservent des instantanés ; les deux résident dans le stockage que vous avez déjà monté ([docs/TRASH-VERSIONING.md](docs/TRASH-VERSIONING.md)).
- **Protection en écriture** - analyse ClamAV facultative de chaque fichier écrit - y compris ceux de l’éditeur intégré, et ceux que la synchronisation du stockage trouve sur le backend sans qu’ils soient passés par filex - assurée par un binaire local ou par un conteneur clamd via le réseau ; s’y ajoute la rétention de la corbeille et des versions, derrière une seule surface d’administration. L’activation, le mode et l’adresse du moteur d’analyse, le plafond de taille et la fenêtre d’analyse de l’éditeur se trouvent dans **Paramètres → Protection** ; les variables `FILEX_CLAMAV*` les initialisent au premier démarrage, puis s’effacent (le chemin du binaire du moteur d’analyse reste réservé à l’environnement, délibérément - c’est une commande que ce serveur exécute) ([docs/PROTECTION.md](docs/PROTECTION.md)).
- **Dossiers chiffrés de bout en bout** - WebCrypto côté client ; le serveur stocke des données chiffrées et ne reçoit jamais de clé. Un dossier a un **niveau** : contenu seulement (le niveau par défaut - WebDAV, la CLI et la synchronisation de l’application de bureau continuent de fonctionner avec ses noms) ou **contenu et noms** (AES-SIV, le serveur ne conserve donc aucun nom lisible), et ce niveau peut être relevé plus tard, avec reprise possible, depuis les **Paramètres de chiffrement** du dossier, où se change aussi son mot de passe. Un dossier que vous avez déjà est **chiffré sur place**, fichiers de plus de 200 Mo compris ; **n’importe quel fichier peut être chiffré isolément** (un `.fxe` autonome, avec son propre mot de passe et sa propre clé de récupération) ; les fichiers de toute taille sont chiffrés en flux ; un dossier déverrouillé se télécharge sous forme de **zip déchiffré** créé dans le navigateur ; `filex decrypt` ouvre un dossier téléchargé ou un `.fxe` sur votre propre machine, et **`filex encrypt`** crée un dossier chiffré à partir d’un dossier sur disque, ou chiffre un dossier sur le serveur là où il se trouve - pour les dossiers trop volumineux pour un onglet, avec reprise possible, les clés étant créées sur votre machine ([docs/CLI.md](docs/CLI.md#filex-encrypt---make-a-folder-an-encrypted-folder)). Chaque dossier reçoit une **clé de récupération**, affichée une seule fois, de sorte qu’un mot de passe oublié n’est pas automatiquement synonyme de données perdues ; un opérateur peut, en option, activer le **séquestre de clés** - à l’installation, ou adopté plus tard sur une installation en service ; il ne s’étend jamais de lui-même aux dossiers existants, mais le choix est proposé à leurs propriétaires au déverrouillage - et son utilisation avertit le propriétaire du dossier ([docs/E2E-ENCRYPTION.md](docs/E2E-ENCRYPTION.md)). **Qui peut chiffrer**, c’est l’organisation qui en décide : une option que l’opérateur de la plateforme active ou désactive pour chaque locataire, une politique du locataire (désactivé, administrateurs uniquement, toutes les personnes dont le rôle le permet, ou **après l’approbation d’un administrateur** - une demande motivée, approuvée pour une seule personne, un seul dossier et un seul type de chiffrement, une seule fois), et l’autorisation `files.encrypt`, exigée à chaque point d’entrée qui pourrait créer un nouvel élément chiffré, copies comprises ([docs/E2E-ENCRYPTION.md](docs/E2E-ENCRYPTION.md#who-may-encrypt)).
- **Mode multilocataire natif** - mode fournisseur/locataire avec isolation par locataire sur une seule instance. Chaque locataire a un **realm** - son nom de connexion, donné à la création et jamais modifié - si bien qu’une connexion désigne son locataire par l’adresse propre à ce locataire (la page web, le `Host` WebDAV, le nom du certificat FTPS) ou par le realm : un champ **Realm** dans le formulaire de connexion, `realm/name` en SFTP. La recherche du compte ne sort jamais du locataire, et un realm saisi sur la page de la plateforme pour un locataire qui a sa propre adresse y est **transféré** avec un ticket à usage unique valable 60 secondes ([docs/MULTI-TENANCY.md](docs/MULTI-TENANCY.md), [realms](docs/MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)). Les locataires s’administrent eux-mêmes dans **Admin → Locataires** et **Mon locataire** : des fournisseurs de connexion liés à un seul locataire ou à plusieurs, l’OIDC et le LDAP propres à un locataire, un sous-domaine de la plateforme pour chaque locataire et des domaines personnalisés prouvés par un CNAME, certifiés par le proxy, par filex lui-même (ACME) ou avec le certificat propre au locataire ([docs/TENANT-ADMIN.md](docs/TENANT-ADMIN.md)).
- **Des pilotes interchangeables pour tout** - pilotes de stockage / d’authentification / de base de données / de file d’attente à activer par variable d’environnement (`FILEX_AUTH_DRIVERS=local,oidc`, `FILEX_QUEUE_DRIVER=postgres`, …) ; la connexion par le système d’exploitation (`windows`, `pam`) fait exception, activée depuis le panneau d’administration une fois son test réussi.
- **Priorité au SSO OIDC** - redirection automatique facultative vers votre IdP avec connexion locale de secours (`?local=1`), et le rôle d’administrateur suit un groupe de l’IdP à chaque connexion.
- **LDAP / Active Directory** - les comptes de l’annuaire se connectent par le même formulaire de mot de passe que les comptes locaux, et avec le même mot de passe en WebDAV, SFTP et FTPS (S3 et NFS utilisent les clés et les exports que ces comptes créent) ; prise en charge des autorités de certification privées, et `local` reste en tête pour que `admin@local` fonctionne pendant que l’annuaire est en panne. L’e-mail d’un compte est toujours une adresse : l’attribut mail de l’entrée, sinon un nom saisi sous la forme `name@domain`, sinon `name@local` (`name@<realm>.local` dans le realm d’un locataire ; la règle unique qu’appliquent aussi les fournisseurs de connexion du système d’exploitation) ; un compte qu’un filex plus ancien a ouvert sous le nom seul est **adopté** à sa prochaine connexion, fichiers, partages et rôle inchangés ([docs/LDAP.md](docs/LDAP.md)).
- **Réplique + réconciliation** - propagation principal→réplique (miroir / ajout seul / ignorer, par règle sur motif glob de chemin), repli en lecture, rapport d’état planifié, « Tout corriger » en un clic.
- **File d’attente d’opérations persistante** - file d’attente à l’épreuve des redémarrages, dans votre propre base de données (SQLite / Postgres / MySQL) ou dans Redis, pool de workers avec nouvelles tentatives + annulation + tableau de bord d’administration. Chaque pilote trie par priorité, de sorte que l’analyse antivirus d’un fichier que quelqu’un vient de téléverser est servie avant les vingt mille qu’un premier import a mises en file d’attente. Non défini, le pilote suit la base de données au lieu de prendre SQLite par défaut - envoyer des instructions SQLite à un serveur Postgres donne une erreur de syntaxe à chaque interrogation, et aucune tâche ne s’exécute jamais.
- **Arborescence de fichiers adossée à la base de données** - les listes viennent du cache de la base de données (1-5 ms), pas du backend de stockage (~100 ms) ; une synchronisation périodique détecte les modifications faites en dehors de filex, par etag là où le backend en fournit un et par taille + date de modification là où il n’en fournit pas. Les **Chemins exclus de l’analyse** d’un stockage (`.*`, `downloads/incomplete/**`, `*.tmp`) tiennent à l’écart du parcours, du catalogue, de l’index de recherche et de l’antivirus les parties d’une arborescence existante dont filex n’a pas l’usage - un contrôle des coûts, pas un contrôle d’accès ([docs/STORAGE.md](docs/STORAGE.md#scan-exclusions)).
- **Catalogue différé pour les grandes arborescences locales** - `sync_mode: lazy` se passe du parcours préalable : le dossier que vous ouvrez est listé aussitôt, directement depuis le disque, et catalogué en premier, et le reste l’est par une passe lente en arrière-plan qui cède la priorité aux personnes (ou seulement à mesure que les dossiers sont ouverts). Les dossiers ouverts sont surveillés dans la limite d’un budget, un dossier que personne n’a visité n’est jamais traité comme supprimé, et la recherche, la taille des dossiers et l’espace utilisé disent clairement quand ils ne couvrent pas encore tout ([docs/STORAGE.md](docs/STORAGE.md#lazy-catalogue), [conception](docs/LAZY-CATALOGUE.md)). Idée d’Alex ([#45](https://github.com/BRF-Tech/filex/issues/45)).
- **Visionneuses et éditeurs** - image/vidéo/audio, PDF, Markdown (vue partagée éditeur + aperçu), CSV (le tableur d’ONLYOFFICE quand il est configuré, un tableau en lecture seule sinon), code (Monaco), Office via ONLYOFFICE, diagrammes Drawio + Mermaid, modèles 3D. Un document qu’ONLYOFFICE ne peut enregistrer que dans un format plus récent (un `.doc` modifié, enregistré en DOCX) est conservé **à côté** de l’original, sous la bonne extension, sans jamais l’écraser, et les personnes qui l’ont modifié en sont informées ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md#a-save-in-another-format)). Le bouton **Tester maintenant** d’ONLYOFFICE fait sa requête par le même point d’entrée qu’un document et avertit quand le serveur de documents n’impose pas JWT, et, après *Échec du téléchargement*, l’éditeur dit laquelle des deux pannes que recouvre ce message était en cause ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md#failure-editor-shows-download-failed)). Lorsqu’il existe plus d’une application ou visionneuse pour un type de fichier, **Ouvrir avec** et **Choisir une application…** permettent d’en sélectionner une, et *Toujours utiliser cette application* est conservé sur votre compte.
- **Notifications** - webhooks JSON génériques (indépendants de Slack/Discord) : autant de cibles que voulu, chacune avec son propre secret de signature et son propre abonnement par événement, ainsi qu’une cloche dans l’application avec lu/non lu et une matrice de mise en sourdine par utilisateur. Le nombre de notifications non lues est un **badge sur la cloche** - exact jusqu’à 99, `99+` au-delà, et sur l’icône de l’application de bureau dans le dock, là où le système en a un - une ligne est cliquable précisément lorsqu’elle mène quelque part (une demande de signature ouvre l’écran de signature, pas une page de notifications), et **Tout afficher** ouvre la totalité de vos notifications par-dessus l’explorateur, pour tout le monde et pas seulement pour les administrateurs. Une écriture qui **crée** un fichier et une écriture qui en **remplace** un sont des événements distincts (`file.uploaded` / `file.updated`), et ceux qu’un opérateur tient le plus à recevoir à part - un téléversement infecté mis en quarantaine, un téléversement échoué, un dossier chiffré ouvert avec sa clé de récupération - peuvent faire l’objet d’un abonnement individuel ([docs/NOTIFICATIONS.md](docs/NOTIFICATIONS.md)).
- **Recherche** - Bleve intégré, plein texte + métadonnées, qui respecte les autorisations. Classement des noms de fichiers à la manière de VS Code : les dossiers comptent, pas l’ordre des mots (`main code` trouve `Code/main.go`), séparateurs et fautes de frappe pardonnés (`invoice 2026` trouve `invoice_2026.pdf`, `mian.go` trouve `main.go`) tandis que les nombres sont pris à la lettre (`2026` ne veut jamais dire `2025`), filtres `tag:`, correspondances exactes classées en premier. Un résultat ⌘K peut être téléchargé (un dossier, en un seul zip) ou glissé vers l’extérieur depuis l’endroit où il se trouve ([docs/SEARCH.md](docs/SEARCH.md)).
- **Des miniatures lisibles** : un PDF montre sa **première page**, ancrée en haut pour que le titre figure dans la carte ; une vidéo, sa première image qui n’est pas noire (une ouverture en fondu produisait auparavant un carré noir, et un clip de moins d’une seconde ne produisait rien du tout alors que la ligne disait encore « prêt ») ; un document Office, le rendu de sa première page ; et un fichier texte, de code ou CSV **remplit la carte de ses premières lignes** plutôt que de répéter l’extension que la ligne affiche déjà. image, vidéo (ffmpeg), PDF (ghostscript), Office (l’ONLYOFFICE connecté) ; les capacités sont prises en compte, et un serveur auquel il manque l’un de ces binaires le dit désormais dans son journal au démarrage au lieu de générer en silence des rectangles colorés. Une miniature en cache est libérée quand le fichier auquel elle appartient est supprimé définitivement, et une réconciliation périodique récupère l’espace occupé par les orphelines qu’une installation plus ancienne a accumulées. Une miniature **suit son fichier** : celle d’un fichier modifié en dehors de filex, ou d’un fichier qui n’a jamais eu d’image, est régénérée quand une liste ou la synchronisation le voit ; le **SVG** est rendu par un moteur intégré sur chaque installation (avec des limites de taille et de temps que fixe un administrateur), et les photos **HEIC/AVIF** passent par ImageMagick ; les images transparentes sont posées sur un damier ; un **dossier montre les derniers fichiers qui y sont arrivés**, affichés avec le dossier dans la grille, la galerie et la liste, et, au survol, ce qu’il contient (un administrateur peut désactiver cela) ; les fichiers texte montrent leurs premières lignes et les archives ce qu’elles contiennent ; un fichier dont l’outil manque est nommé, pas masqué ; et **Admin → Outils → Réparation des miniatures** régénère à la demande les miniatures d’un fichier, d’un dossier ou d’un stockage ([docs/thumbnails.md](docs/thumbnails.md)).
- **Onglets, thèmes et liens profonds** - plusieurs dossiers ouverts côte à côte, thème clair/sombre/automatique, et une barre d’adresse qui suit le dossier ouvert, pour qu’un lien collé y mène. Huit palettes sont livrées dans la galerie de thèmes, chacune étant un jeu de valeurs pour les variables `--fe-*` plutôt qu’une seconde feuille de style, si bien qu’une page hôte ou une intégration peut en choisir une - ou définir ses propres valeurs - sans dupliquer le moindre CSS ; un opérateur peut ajouter les siennes (voir *Apparence*).
- **Apparence : vos couleurs, partout** - l’écran **Apparence** du panneau d’administration compose des thèmes nommés - douze couleurs pour le mode clair et pour le mode sombre, un rayon des angles, une pile de polices - prévisualisés au fil de la saisie, et fait de l’un d’eux le **thème par défaut de l’instance**. Le texte d’un bouton coloré est choisi par contraste au lieu d’être supposé blanc, le reste de la palette est dérivé côté serveur, et le thème s’étend à la page de connexion et à chaque lien public - dans ses propres tons, ou dans des couleurs que vous donnez en propre à ces deux pages - parce qu’une image de marque qui s’arrête à la connexion n’en est pas une : une page vue sans être connecté porte le thème par défaut de l’instance, jamais la palette de la dernière personne à avoir utilisé ce navigateur, et, pour une personne connectée, c’est son propre choix qui l’emporte. Les thèmes s’exportent et s’importent sous la forme d’un seul fichier JSON. La **feuille de style personnalisée** est l’outil dangereux juste à côté, et elle est désormais désactivée tant que vous ne l’activez pas, n’est jamais servie à quiconque n’est pas connecté, ne peut rien aller chercher et ne peut pas atteindre l’écran qui la désactive ([docs/INTEGRATION.md](docs/INTEGRATION.md#themes)).
- **Un seul tableau, partout** - il ne reste qu’un tableau dans filex, celui de l’explorateur, et toute autre liste est ce tableau : les menus du panneau d’administration, **Mes partages**, les écrans propres à une application. Chaque liste fige sa première colonne à gauche et ses actions à droite, se redimensionne, se réordonne et se trie de la même façon, et termine chaque ligne par **un seul menu Actions épinglé** qui contient tout ce que cette ligne permet de faire - le même menu qu’ouvre le ⋮ de l’explorateur, si bien qu’un second tableau ne peut pas diverger du premier. Une application installée dotée d’un écran d’accueil obtient sa propre ligne sous **Applications** dans la navigation du panneau.
- **Un panneau d’administration où vous vous repérez** - les pages de l’administrateur sont rangées dans un méga-menu de la barre supérieure : le tableau de bord, puis **Fichiers et stockage**, **Utilisateurs et sécurité** et **Système**, chacun formant un panneau de sections nommées, avec une courte ligne sous chaque page. Chaque page est à deux clics, à l’adresse qu’elle a toujours eue, un administrateur délégué ne se voit proposer que les pages que ses autorisations ouvrent, le clavier et les lecteurs d’écran sont pris en charge, et un téléphone affiche les mêmes pages sous forme de liste dans un tiroir ([Panneau d’administration](docs/ADMIN-PANEL.md)).
- **Liens symboliques, à la frontière du stockage** - un lien situé dans un stockage `local` et qui pointe à l’intérieur de celui-ci est suivi et s’ouvre comme sa cible ; un lien qui sort du stockage est **listé avec un badge et la raison**, et refusé en lecture, en écriture et en suppression - sauf si vous activez *Suivre les liens symboliques qui sortent de ce dossier* pour ce stockage ([docs/STORAGE.md](docs/STORAGE.md#symlinks)).
- **Ouvrir selon l’usage de chaque appareil** - avec une **souris**, un simple clic sélectionne et un **double-clic ouvre** (Entrée ouvre la sélection) - le geste classique des gestionnaires de fichiers, et une préférence propre à chaque personne (`ExplorerConfig.openTrigger`, `'double'` par défaut ; l’application de bureau la propose sous **Settings → Open files with**, et `'single'` rétablit l’ouverture en un clic). Sur un **écran tactile**, un appui ouvre toujours - il n’y a pas de sélection au survol. Sur tous les appareils, la **case à cocher** est le seul clic ou appui qui sélectionne (Maj étend la plage) et un clic droit ou un appui long ouvre le menu ; les lignes de liste, les cartes de grille et les vignettes de galerie la portent toutes.
- **Au clavier, et c’est indiqué** - chaque action du menu contextuel et de la barre d’outils affiche la touche qui la déclenche, lue dans le registre, si bien qu’elle suit toute réaffectation. Trente-deux actions sont réaffectables depuis *Paramètres des raccourcis* (enregistrés par navigateur) ; les quelques combinaisons qu’un navigateur se réserve, comme `Ctrl+W`, sont refusées avec un motif au lieu d’être enregistrées comme une touche qui ne déclencherait jamais rien.
- **Utilisation et coût** - filex ne mesure pas la facture de votre fournisseur ; il lit le rapport que le fournisseur écrit déjà, le normalise et en calcule le prix d’après un tableau que vous pouvez modifier. Les CSV quotidiens de Backblaze B2 sont lus via la même API S3 que filex parle déjà, donc pas de nouvelle dépendance ni de nouveau type d’identifiant. Les offres gratuites sont des champs à part entière plutôt que des constantes dans une formule, et la page garde la ligne au niveau du compte du fournisseur séparée de ses lignes par bucket - les additionner revient à compter deux fois les mêmes transactions, d’un montant que, justement, personne ne remarque ([docs/USAGE.md](docs/USAGE.md)).
- **Journal d’audit** - chaque modification enregistrée avec son auteur, l’identité d’intégration et les métadonnées.
- **Client CLI** - le même binaire accède à un serveur distant (`filex client`, `filex sync`) sans extension côté serveur : la copie et le déplacement entre stockages, la corbeille, les versions, les étiquettes, les actions d’application, les archives et vos liens, chaque tâche du serveur étant suivie jusqu’à son terme ; `filex client login --realm` se connecte à un locataire, `filex encrypt` crée des dossiers chiffrés, et une session enregistrée n’est jamais envoyée qu’à l’adresse avec laquelle elle a été enregistrée ([docs/CLI.md](docs/CLI.md)).
- **Mise à jour automatique** - les versions mineures sont annoncées pour une mise à jour en un clic, et les correctifs s’installent tout seuls une fois que vous l’autorisez (`AUTO_UPGRADE=true` ; par défaut, filex ne fait que vérifier et vous prévenir) ; une installation gérée par un gestionnaire de paquets (Homebrew, winget, Snap, un paquet de distribution), ou un conteneur, se voit signaler les nouvelles versions et la commande pour les obtenir, et la page d’administration dit qu’elle ne fera que les annoncer ([docs/UPDATES.md](docs/UPDATES.md)).
- **Binaire unique** - matrice goreleaser : linux/macOS/Windows × amd64/arm64. CGO=0, modernc.org/sqlite.
- **i18n** - anglais + turc de série, **liens publics compris** : un lien de partage,
  un écran de saisie du PIN, une page de demande de fichiers ou l’écran de signature
  d’une application s’affiche dans la langue du visiteur, et l’interface des pages
  publiques **propose un sélecteur**, parce que la langue du navigateur d’un inconnu
  n’est qu’une supposition et que la personne qui lit un contrat doit pouvoir la
  corriger. Les pages simples sans JS consultent `?lang=`, puis `Accept-Language`,
  puis la valeur par défaut du serveur. **Le texte qu’écrit le serveur vient du même
  catalogue** - les e-mails, les phrases de notification, les pages sans JavaScript et
  la vérification des autorisations d’une installation - chacun adressé au lecteur
  qui a toujours été le sien, avec repli sur l’anglais clé par clé, et une traduction
  dont les espaces réservés ne correspondent pas à ceux de l’anglais n’est pas utilisée
  à l’exécution, si bien qu’un e-mail ne perd jamais son lien ni son PIN.
- **Packs de langue** - toute autre langue est une **application qui n’exécute rien** :
  un manifeste de chaînes, installé depuis un dépôt GitHub, par téléversement ou depuis
  une URL, comme n’importe quelle autre application, listé sous
  **Extensions → Applications** avec sa couverture de la version en cours d’exécution
  (*Español - 97 % traduit · le reste s’affiche en anglais*). Sa langue s’ajoute à
  chaque sélecteur - la boîte de dialogue des paramètres, l’en-tête d’administration,
  les pages de partage publiques - et traduit aussi bien l’explorateur que le panneau
  d’administration et les pages publiques. Les formes du pluriel suivent les
  **catégories CLDR**, si bien qu’un pack écrit `zero`, `one`, `two`, `few`, `many` et
  `other` là où sa langue les possède. L’espagnol, l’allemand et le français sont
  livrés à titre d’exemple, et un dépôt modèle ainsi que `scripts/i18n-export.mjs` /
  `i18n-validate.mjs` guident un traducteur de l’export à l’installation - le
  validateur soumet un pack aux règles que respectent les langues intégrées, parmi
  lesquelles un trait d’union simple là où un texte voudrait un tiret long
  ([en écrire un](docs/PLUGIN-KIT.md#writing-a-language-pack)).
- **Mise en page de droite à gauche** - en arabe, en hébreu, en persan, en
  ourdou et dans toute autre langue de droite à gauche, l’interface entière
  s’affiche de droite à gauche : le panneau de navigation, les tableaux, la
  géométrie du glisser-déposer et du redimensionnement des colonnes, les menus
  et les icônes de direction. Ce qui ne doit **pas** s’inverser en miroir ne
  s’inverse pas - un éditeur de champs PDF travaille dans l’espace du document,
  et un chemin, une commande ou tout autre texte machine est isolé pour se lire
  de gauche à droite dans une phrase de droite à gauche, y compris dans les
  phrases qu’a écrites le serveur. La règle est imposée par un test garde-fou :
  la mise en page n’est écrite qu’avec des propriétés CSS logiques
  ([docs/RTL.md](docs/RTL.md)).
- **Étiquettes, personnelles ou d’équipe** - une étiquette est soit
  **personnelle** - à vous seul, jamais révélée à personne d’autre - soit une
  étiquette **d’équipe** partagée au sein du locataire, dont l’ajout ou le
  retrait demande l’autorisation de modifier le fichier. Une étiquette ouvre
  tous les fichiers qui la portent, quel que soit le dossier où ils se trouvent,
  et `tag:` restreint une recherche. Les majuscules sont conservées telles
  qu’elles ont été saisies.
- **Fournisseurs d’identité, gérés depuis le panneau** -
  **Admin → Fournisseurs d’identité** pilote désormais la connexion au lieu de stocker
  des paramètres que rien ne lisait : OIDC, LDAP, l’en-tête de proxy inverse, le
  formulaire de mot de passe local et les comptes du système d’exploitation
  lui-même - **Windows** (local ou de domaine, `LogonUserW`, rien à installer)
  et **Linux PAM** - chacun avec un bouton **Tester maintenant** qui le teste
  réellement et dit quelle étape il a vérifiée. Un fournisseur du système
  d’exploitation n’est activé que par un test qui a connecté un vrai compte, et
  ce compte devient super-administrateur ; ce fournisseur ne peut pas être
  activé depuis l’environnement. Chaque fournisseur suit **une seule règle de
  première connexion** - s’il peut ou non créer un compte (`auto_create`,
  désactivé par défaut pour Windows et PAM) et pour quels groupes
  (`allowed_groups`). Ce qui est défini dans l’environnement ou dans
  `config.yaml` **l’emporte, et cela se voit** ; un secret client ou un mot de
  passe de liaison est en écriture seule et scellé au repos avec
  `FILEX_SECRET_KEY`, jamais renvoyé ; et le dernier moyen d’entrer ne peut pas
  être désactivé depuis la page ([docs/SSO.md](docs/SSO.md),
  [docs/LDAP.md](docs/LDAP.md), [docs/OS-LOGIN.md](docs/OS-LOGIN.md)).
- **Sécurité de connexion** - les mots de passe erronés sont comptés par
  identifiant de compte (que le compte existe ou non, le décompte ne révèle donc
  rien) et par adresse client, sur le formulaire web comme sur WebDAV, FTPS et
  SFTP : 5 par compte et 10 par adresse en 10 minutes ferment la porte pendant
  une minute, durée qui double jusqu’à 15, et un verrouillage refuse même le bon
  mot de passe. Le formulaire de connexion dit combien de tentatives il reste,
  dans la langue du lecteur. Une **liste d’adresses IP autorisées** est le moyen
  de rentrer - aucun compte n’est privilégié, celui du premier administrateur
  compris (seul le compte partagé d’une démo publique n’est compté que par
  adresse, [docs/DEMO.md](docs/DEMO.md)) - et **Admin → Sécurité de connexion**
  regroupe les limites, la liste, les verrouillages avec *Lever le
  verrouillage*, et le journal des connexions : chaque tentative échouée, chaque
  verrouillage, chaque levée de verrouillage et chaque modification des
  paramètres, une fois chacun, quel que soit le point d’entrée utilisé par un
  administrateur (le panneau, une clé d’API, MCP). L’adresse comptée est celle
  du pair du socket, sauf si ce pair est un proxy auquel vous faites confiance
  (`FILEX_TRUSTED_PROXIES`, par défaut `auto` : cette machine et, dans un
  conteneur, les autres conteneurs de son réseau - jamais la passerelle, jamais
  le LAN ; la page nomme un pair qui, sans être de confiance, envoie des
  adresses transmises, et propose de l’ajouter), si bien qu’un client ne peut
  pas choisir sa propre adresse en écrivant `X-Forwarded-For`
  ([docs/CONFIGURATION.md](docs/CONFIGURATION.md#sign-in-attempt-limits)).
- **Les modifications venues d’un autre site sont refusées** - une requête qui modifie
  quelque chose et ne porte que la session du visiteur (le cookie, ou l’en-tête de
  connexion d’un proxy de confiance) doit venir des pages de filex lui-même, de sa propre
  adresse ou d’une origine listée dans `FILEX_CORS_ALLOWED_ORIGINS` ; tout le reste reçoit
  pour réponse `403 cross_origin_refused` avant qu’une route ne s’exécute. Les clés, les
  liens de partage et de dépôt, les tickets de téléversement, S3 et les scripts ne sont
  pas concernés
  ([docs/CONFIGURATION.md](docs/CONFIGURATION.md#requests-from-other-origins)).

## Architecture

Voir [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Documentation

**Premiers pas** - [Installation](docs/INSTALLATION.md) ·
[Configuration](docs/CONFIGURATION.md) · [Panneau d’administration](docs/ADMIN-PANEL.md) ·
[Bases de données](docs/DATABASES.md) · [Versions](docs/RELEASES.md) ·
[Mises à jour](docs/UPDATES.md) · [Mode démo](docs/DEMO.md)

**Clients** - [Application de bureau](docs/DESKTOP.md) ·
[Synchronisation de dossiers](docs/SYNC.md) · [CLI](docs/CLI.md) ·
[Intégration / composant intégré](docs/INTEGRATION.md) · [IA et MCP](docs/MCP.md)

**Sans navigateur** - [Protocoles (S3 · SFTP · FTPS · NFS · WebDAV ·
`filex mount`)](docs/PROTOCOLS.md) · [WebDAV](docs/WEBDAV.md)

**Applications** -
[Applications : installer, contrôler, signer, convertir](docs/APP-PLUGINS.md) ·
[Demandes d’installation](docs/APP-PLUGINS.md#install-requests) ·
[Autorisations des applications](docs/APP-PLUGINS.md#app-permissions) · [Applications par
défaut](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail) ·
[Écrire une application](docs/PLUGIN-KIT.md) ·
[Contrat d’échange des applications](docs/APP-PLUGINS-API.md)

**Langue et mise en page** -
[Écrire un pack de langue](docs/PLUGIN-KIT.md#writing-a-language-pack) ·
[Langues de droite à gauche](docs/RTL.md)

**Stockage et accès** - [Stockage](docs/STORAGE.md) ·
[Extensions de stockage](docs/PLUGINS.md) · [Utilisation et coût](docs/USAGE.md) ·
[Téléversements et reprise](docs/UPLOADS.md) · [Quotas](docs/QUOTAS.md) ·
[SSO (OIDC)](docs/SSO.md) · [LDAP et authentification par proxy](docs/LDAP.md) ·
[Comptes Windows et Linux](docs/OS-LOGIN.md) · [Limites de tentatives de connexion et
proxys de confiance](docs/CONFIGURATION.md#sign-in-attempt-limits) ·
[RBAC, accès aux dossiers et clés d’API](docs/RBAC.md) ·
[Rôles et autorisations par utilisateur](docs/PERMISSIONS.md) · [Groupes](docs/GROUPS.md) ·
[Mode multilocataire et realms](docs/MULTI-TENANCY.md) ·
[Libre-service des locataires](docs/TENANT-ADMIN.md)

**Données et fonctionnalités** - [Partage et demandes de fichiers](docs/SHARING.md) ·
[ShareX](docs/SHAREX.md) ·
[Corbeille et gestion des versions](docs/TRASH-VERSIONING.md) ·
[Protection](docs/PROTECTION.md) · [Archives](docs/ARCHIVES.md) ·
[Chiffrement de bout en bout](docs/E2E-ENCRYPTION.md) ·
[Qui peut chiffrer](docs/E2E-ENCRYPTION.md#who-may-encrypt) · [Recherche](docs/SEARCH.md) ·
[Temps réel et présence](docs/REALTIME.md) ·
[Notifications](docs/NOTIFICATIONS.md) · [Miniatures](docs/thumbnails.md) ·
[Réplication](docs/REPLICATION.md) · [Thèmes et apparence](docs/INTEGRATION.md#themes)

**Exploiter et étendre** - [Déploiement](docs/DEPLOYMENT.md) · [Docker](docs/DOCKER.md) ·
[Métriques](docs/METRICS.md) · [Architecture](docs/ARCHITECTURE.md) ·
[Spécification de l’API du backend](docs/BACKEND.md) ·
[OpenAPI 3.1 (`/api/files`, `/api/ai`)](backend/internal/api/openapi.json) ·
[API du composant](docs/API.md) · [ONLYOFFICE](docs/ONLYOFFICE.md) ·
[CSV dans ONLYOFFICE](docs/ONLYOFFICE.md#csv-files) ·
[Requêtes venant d’autres origines](docs/CONFIGURATION.md#requests-from-other-origins)

[Index complet de la documentation](docs/README.md)

## Développement

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
  les scripts que lance `pnpm shots` pour refaire toutes les captures d’écran ci-dessus
- `docker/` - Dockerfiles + compose
- `deploy/` - stacks Compose prêtes à l’emploi + chart Helm (voir [`deploy/`](deploy/))
- `docs/` - documentation en Markdown
- `docs-site/` - site VitePress publié sur [docs.filex.sh](https://docs.filex.sh)

Les contributions sont les bienvenues - voir [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md).

## Licence

MIT - voir [LICENSE](LICENSE).

Les badges des boutiques, dans [`docs/badges/`](docs/badges/), sont les visuels de ces
boutiques elles-mêmes, utilisés sans modification et non couverts par cette licence :
Microsoft et le badge Microsoft Store sont des marques commerciales du groupe de sociétés
Microsoft ; le badge Snap Store est © Canonical Ltd., sous licence
[CC BY-ND 2.0 UK](https://creativecommons.org/licenses/by-nd/2.0/uk/).
