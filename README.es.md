<!-- Translated from README.md as of c1e0ba60 (v0.50.0). The English README is the source: change it first, then carry the change here. -->

<div align="center">

<img src="docs/logo.png" alt="logotipo de filex" width="96">

# filex - gestor de archivos autoalojado que se integra en cualquier lugar

[![Release](https://img.shields.io/github/v/release/BRF-Tech/filex?color=2f6ceb)](https://github.com/BRF-Tech/filex/releases)
[![CI](https://img.shields.io/github/actions/workflow/status/BRF-Tech/filex/ci.yml?branch=main&label=ci)](https://github.com/BRF-Tech/filex/actions)
[![License: MIT](https://img.shields.io/github/license/BRF-Tech/filex?color=22c55e)](LICENSE)
[![Container](https://img.shields.io/badge/ghcr.io-brf--tech%2Ffilex-2496ed?logo=docker&logoColor=white)](https://github.com/BRF-Tech/filex/pkgs/container/filex)
[![Live demo](https://img.shields.io/badge/live_demo-demo.filex.sh-f59e0b)](https://demo.filex.sh)

[English](README.md) · [Türkçe](README.tr.md) · [Deutsch](README.de.md) · **Español** · [Français](README.fr.md) · [简体中文](README.zh-CN.md)

<sub>Esta es una traducción del [README en inglés](README.md) correspondiente a la v0.50.0; donde los dos difieran, prevalece el texto en inglés. Se tradujo de forma automática y está pendiente de la revisión de un hablante nativo - se agradecen las correcciones. Los documentos a los que enlaza están en inglés.</sub>

Un único binario de Go con una interfaz web completa, controladores intercambiables de
almacenamiento, autenticación y base de datos, **colaboración en tiempo real**, **un
componente web integrable**, una **aplicación de escritorio cuya sincronización de carpetas
es en vivo** - un cambio en cualquiera de los dos lados llega en aproximadamente un segundo -
un **servidor MCP integrado** para que los agentes de IA lo manejen de forma nativa, y
**aplicaciones**: complementos que le enseñan cosas nuevas que hacer con los archivos - un
módulo de WebAssembly en un entorno aislado, una interfaz propia en un marco aislado, o ambas
cosas - empezando por la **firma de documentos** con personas de dentro y de fuera de su
organización. Un **paquete de idioma** también es una aplicación, así que filex se puede
traducir sin esperar a una nueva versión - y se dispone **de derecha a izquierda** para los
idiomas que se leen así. Las personas inician sesión con las cuentas que ya tienen - SSO,
LDAP o la **cuenta de Windows o Linux** del equipo en el que se ejecuta filex - y en una
instalación multiinquilino, **cada inquilino se gestiona a sí mismo**: sus propios
proveedores de inicio de sesión, su propio dominio y certificado.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/v0.50.0/explorer-grid-dark.png">
  <img src="docs/screenshots/v0.50.0/explorer-grid-light.png" alt="explorador de filex - cuadrícula de miniaturas" width="900">
</picture>

</div>

## Pruébelo ahora

**Demo en vivo:** [demo.filex.sh](https://demo.filex.sh) - inicie sesión con
`demo@demo.com` / `demo` (rol de administrador, el entorno de pruebas se restablece cada
noche). O ejecute su propia instancia:

```bash
docker run -p 5212:5212 \
  -e FILEX_DEFAULT_STORAGE_DRIVER=local -e FILEX_DEFAULT_STORAGE_PATH=/srv/files \
  -v filex-data:/data -v "$PWD:/srv/files" \
  ghcr.io/brf-tech/filex:latest
```

Con eso se sirve **la carpeta desde la que lo ejecutó** - abra la interfaz y sus archivos ya
están ahí. `/data` es el directorio propio de filex (base de datos SQLite, índice de
búsqueda, caché de miniaturas), por lo que es un volumen con nombre y no la carpeta en la
que usted deja archivos; los dos están separados a propósito. Apunte `$PWD` a otro lugar, o
agregue después más almacenamientos desde el panel de administración - un bucket con varias
carpetas de nivel superior se puede montar de una sola vez como un almacenamiento por
carpeta (*Almacenamientos → Agregar → Montar varias carpetas a la vez*).

El contenedor se ejecuta como **root** de forma predeterminada, así que lo que escribe en
`/data` pertenece a root; defina `PUID`/`PGID` para ejecutarlo con su propio usuario
([docs/DOCKER.md](docs/DOCKER.md#which-user-the-container-runs-as)).

Abra http://localhost:5212/admin - la primera ejecución imprime en la consola las
credenciales de administrador y las instrucciones de integración. Esa URL es la del operador;
las personas a las que usted da una cuenta reciben **http://localhost:5212/drive**, el mismo
gestor de archivos sin el panel que lo rodea.

¿Prefiere una ventana a una pestaña del navegador? La **aplicación de escritorio**
(Windows / Linux / macOS) inicia sesión en cualquier servidor filex y sincroniza carpetas en
segundo plano - y en cada plataforma hay una copia que funciona **sin instalarse**
(un `.exe` portátil, un AppImage, un `.zip`).
Obténgala de la [Microsoft Store](https://apps.microsoft.com/detail/9PKXDJLVZWXW), de la
[Snap Store](https://snapcraft.io/filex-app) o de la
[última versión](https://github.com/BRF-Tech/filex/releases/latest) -
[docs/DESKTOP.md](docs/DESKTOP.md).

## Por qué filex

La mayoría de los gestores de archivos autoalojados son o bien **demasiado pequeños** (un
listado de directorio con subidas) o bien **demasiado grandes** (una suite de trabajo en
grupo que se despliega solo por la pestaña de archivos). filex apunta al espacio que queda
entre ambos:

- **Un cliente de navegador para sus usuarios, no solo para usted** - entregue a alguien una
  cuenta `user` o `viewer` y `…/drive`, y esa persona obtiene el gestor de archivos en sí:
  sus almacenamientos, las subidas, el uso compartido, la búsqueda, el editor. Sin panel de
  administración que recorrer, sin un frontend aparte que desplegar. `…/admin` es la vía de
  acceso del operador a la misma aplicación.
- **Una navegación que la gente ya conoce** - un panel izquierdo con un menú **+ Nuevo** bien
  visible e **Inicio · Mis archivos · Compartido conmigo · Mis enlaces · Recientes ·
  Destacados · Borradores · Papelera**, además de los almacenamientos a los que usted puede
  acceder; un almacenamiento que alguien compartió con usted simplemente aparece allí, a un
  clic, sin instrucciones de montaje. Cualquiera puede contraerlo a una barra de iconos desde
  la barra superior. **Inicio** es una vista *dentro* de la aplicación, no una página junto a
  ella - sus almacenamientos, lo último que abrió y lo que destacó, bajo la misma barra
  lateral y la misma cabecera que los archivos. Esta es la interfaz base que todo el mundo
  recibe: un único campo de búsqueda a lo ancho de la cabecera con su indicación de la paleta
  ⌘K, una fila de filtros Tipo / Propietario / Modificado / Tamaño, Carpetas y Archivos como
  secciones rotuladas, Detalles y Actividad en el panel de detalles y una línea de
  almacenamiento. Para quien quiere un simple disco de archivos y no un gestor de archivos,
  `uiProfile: 'simple'` deja desactivado de antemano el resto de los controles de la
  interfaz - un solo panel, una sola carpeta, lista o cuadrícula. Un solo explorador en todos
  los casos: no hay una segunda interfaz que mantener a la par.
- **Se integra en cualquier lugar** - la misma interfaz se distribuye como un componente de
  Vue 3, un componente de React y un componente web `<filex-explorer>` independiente del
  framework. Ponga un gestor de archivos de verdad dentro de *su* producto, respaldado por su
  propio servidor filex y restringido a una carpeta por inquilino. El panel de navegación
  viene incluido - `<filex-explorer sidenav ui-profile="simple">` es todo lo que hace falta
  para activarlo en una página anfitriona que nunca toca JavaScript.
- **Nativo para agentes de IA** - una superficie REST (`/api/ai`) limitada por los permisos de
  una clave de API, más un **servidor MCP** nativo (`/api/ai/mcp`); `/api/ai` y `/api/files`
  se describen en un [archivo OpenAPI 3.1](backend/internal/api/openapi.json) que una prueba
  mantiene fiel al enrutador. Entregue a un agente un token restringido a una sola carpeta y
  este trabaja allí con las propias operaciones del explorador - listar, leer, escribir,
  copiar, convertir, compartir, la papelera, las versiones, los archivos comprimidos - y nada
  fuera de ella.
- **Aplicaciones que solo pueden hacer lo que usted aprobó** - firmar un contrato con un socio
  que no tiene cuenta, convertir un video, cualquier cosa que un manifiesto describa, todo se
  agrega como una **aplicación**: un módulo de WebAssembly que se ejecuta dentro de filex,
  una interfaz propia que filex sirve en un marco aislado, o ambas cosas - exactamente con
  los permisos que usted leyó y concedió al instalar. El módulo no recibe ningún sistema de
  archivos, ninguna red, ningún programa en su servidor; la interfaz no puede leer la sesión
  de filex y queda sin acceso a la red por la propia política de filex. Nada se actualiza
  solo: una versión nueva espera a un administrador y la anterior está a un clic. Cuatro se
  distribuyen como repositorios públicos - **Firma electrónica**, **Convertir**,
  **filextext** (un espacio de trabajo de texto cifrado de extremo a extremo) y
  **draw.io** - y cada una se instala desde su dirección de GitHub
  ([Aplicaciones](#aplicaciones)).
- **En su idioma, y en su sentido de lectura** - el inglés y el turco vienen en el binario, y
  cualquier otro es un **paquete de idioma**: una aplicación sin nada que se ejecute,
  instalada desde un repositorio como cualquier otra, que traduce el explorador, el panel de
  administración, las páginas públicas que abre un desconocido *y el texto que escribe el
  servidor de filex* - el correo, las notificaciones, las páginas sin JavaScript que hay
  detrás de un enlace. Un paquete dice cuánto cubre de esta versión, y lo que le falte se
  muestra en inglés; las formas del plural siguen CLDR, así que un idioma recibe las formas
  que realmente tiene. Para el árabe, el hebreo, el persa y el urdu, la interfaz
  **se dispone de derecha a izquierda** - y se detiene donde reflejar sería un error, en el
  espacio del documento y en el texto de máquina
  ([docs/RTL.md](docs/RTL.md), [escribir un paquete](docs/PLUGIN-KIT.md#writing-a-language-pack)).
- **Lleva su marca, no la nuestra** - cree un tema con sus propios colores en la pantalla
  **Apariencia** y establézcalo como predeterminado: la página de inicio de sesión y cada
  enlace público también lo llevan, y una solicitud de firma de su instancia lleva el nombre
  de usted, no el de filex.
- **Tiempo real** - avatares de presencia (una foto de perfil que se establece una sola vez en
  la cuenta y se muestra para cada cliente con la sesión iniciada como usted) y
  actualizaciones de archivos en vivo por WebSocket, en la interfaz nativa *y* en las
  integraciones (autenticación con tickets de corta duración, sondeo de la API como
  alternativa). Una tarea por lotes se condensa a la salida, así que extraer un archivo
  comprimido de cinco mil archivos le cuesta a un explorador abierto un goteo acotado de
  tramas en lugar de cinco mil
  ([docs/REALTIME.md](docs/REALTIME.md)).
- **También en su escritorio** - el mismo explorador se distribuye como una aplicación para
  Windows/Linux/macOS que mantiene las carpetas locales sincronizadas con el servidor desde
  la bandeja del sistema - **en vivo**, en aproximadamente un segundo, en ambos sentidos -
  se actualiza sola y alberga varias cuentas (o inquilinos) lado a lado. Haga clic con el
  botón derecho en una carpeta → **Mantener en este equipo** y se refleja bajo una sola
  carpeta de filex; todo lo demás queda solo en línea en la ventana. Los equipos sin interfaz
  gráfica tienen el mismo motor en forma de `filex sync` / `filex client`.
- **Habla los protocolos en ambos sentidos** - filex puede *conectarse a* discos locales, S3,
  FTP, SFTP, WebDAV y recursos compartidos SMB/NAS, y se puede *acceder a él como* **S3**,
  **SFTP**, **FTPS**, **NFSv3** y **WebDAV**. Apunte a filex `rclone`, `restic`, `aws s3`,
  WinSCP, FileZilla, un escáner que solo aprendió FTP o un reproductor multimedia que solo
  aprendió NFS, y van a parar al mismo árbol, con los mismos permisos, la misma papelera y la
  misma cuota que la interfaz web. Fuera de la LAN también está **`filex mount`**, que
  conecta un servidor remoto por HTTPS corriente - una carpeta en Linux, una letra de unidad
  en Windows
  ([docs/PROTOCOLS.md](docs/PROTOCOLS.md)).
- **Roles y permisos por usuario** - 28 permisos con nombre (cada acción sobre archivos, cada
  tipo de uso compartido, cada protocolo, las claves de API, la aplicación de escritorio,
  cinco áreas de administración), y cada persona tiene un solo rol: Administrador, Usuario,
  Lector o un rol personalizado, que puede variar en algunas carpetas ("sin eliminar, salvo
  en Scratch") y llevar límites (vigencia y contraseña del enlace, tipos de archivo
  bloqueados, tamaño máximo de archivo, autenticación de dos factores obligatoria). Las
  excepciones por persona prevalecen sobre el rol, un administrador delegado puede
  administrar usuarios sin tener nunca más de lo que concede y la respuesta es la misma en
  todas las vías de acceso - la aplicación web, la API de agentes, WebDAV, SFTP, FTPS, S3,
  NFS y las claves de API. Una aplicación instalada puede agregar permisos propios
  ("Solicitar firmas"), que se conceden de la misma manera, y un enlace público sigue abierto
  solo mientras quien lo creó aún pueda crearlo
  ([docs/PERMISSIONS.md](docs/PERMISSIONS.md)).
- **Grupos** - conjuntos de personas con nombre, por inquilino: comparta una carpeta con un
  grupo igual que con una persona, y dé a un grupo un rol que tiene toda persona del grupo
  que no tenga un rol propio (una prioridad de rol decide entre grupos). Las personas se unen
  a mano, o a través de los grupos que trae su inicio de sesión - un claim de OIDC,
  `memberOf` de LDAP, los grupos del sistema operativo o un encabezado de proxy - y salen
  cuando el proveedor de identidad lo dice
  ([docs/GROUPS.md](docs/GROUPS.md)).
- **Iniciar sesión con la cuenta que cada persona ya tiene** - una contraseña local, OIDC,
  LDAP / Active Directory, un proxy de autenticación o la **cuenta de Windows o Linux** del
  equipo en el que se ejecuta filex: el sistema operativo juzga la contraseña, filex nunca la
  almacena y el proveedor solo se activa después de que una cuenta real haya iniciado sesión
  con él. Todos los proveedores siguen una sola regla sobre quién obtiene una cuenta en el
  primer inicio de sesión
  ([docs/OS-LOGIN.md](docs/OS-LOGIN.md)).
- **Adivinar una contraseña es lento** - las contraseñas incorrectas se contabilizan por
  cuenta y por dirección, por igual en el formulario web, WebDAV, FTPS y SFTP; un bloqueo se
  duplica hasta llegar a 15 minutos, una lista de IP permitidas es la forma de volver a
  entrar y una dirección de cliente reenviada solo se da por buena si viene de un proxy de
  confianza - de forma predeterminada este equipo y los contenedores que están junto a
  filex, cualquier otro que usted indique
  ([límites de intentos de inicio de sesión](docs/CONFIGURATION.md#sign-in-attempt-limits)).
  Se rechaza un cambio que otro sitio envía con la sesión de un visitante
  ([solicitudes desde otros orígenes](docs/CONFIGURATION.md#requests-from-other-origins)).
- **Multiinquilino por diseño** - almacenamiento por inquilino con modo multiinquilino
  nativo, roles RBAC + permisos por elemento, claves de API restringidas, identidades por
  token para los registros de auditoría y tokens de tipo app y de tipo user para que una
  credencial compartida de una integración no pueda administrar las claves de nadie. Un
  token nombra los permisos que tiene - una lista vacía se rechaza en lugar de leerse como
  "todo" - y **ninguna credencial que emita es nunca más amplia que el propio token**: una
  clave de API, una clave S3, una exportación NFS o una clave SSH creadas mediante un
  token restringido no pueden exceder sus verbos, salir de su carpeta ni durar más que su
  caducidad (`403 token_ceiling`). El límite del inquilino se impone en cada ruta que
  nombra una fila, no solo en aquellas que las enumeran, y los ajustes de toda la instancia
  quedan reservados al superinquilino. Cada inquilino tiene un **realm**, su nombre de
  inicio de sesión: el `alex` de un inquilino y el de otro son dos personas, ya sea que
  inicien sesión en la dirección propia del inquilino, escriban el realm en la página de
  la plataforma o escriban `realm/alex` por SFTP
  ([realms](docs/MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)). Y un inquilino
  se gestiona a sí mismo: su administrador agrega el OIDC o el LDAP propios del inquilino,
  el operador vincula proveedores de inicio de sesión compartidos a un inquilino o a
  varios, y el dominio propio de un inquilino se verifica con un CNAME y se sirve con un
  certificado del proxy de usted, del propio filex (ACME) o del propio inquilino
  ([docs/TENANT-ADMIN.md](docs/TENANT-ADMIN.md)).
- **Tan sencillo de desplegar que aburre** - un solo binario o un solo contenedor, en un
  host propio o bajo una subruta de uno que usted comparte; SQLite de forma
  predeterminada, Postgres/MySQL cuando los quiera; cada controlador se cambia con
  variables de entorno. La CI migra los tres motores, los compara entre sí y escribe en
  ellos con cada cambio, porque "compatible" antes quería decir "compila"
  ([docs/DATABASES.md](docs/DATABASES.md)).

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

## Capturas de pantalla

### Aplicaciones - firmar un documento, la primera de ellas

Dana pide a un colega del mismo filex y a un socio externo que firmen un acuerdo. La
aplicación es [Firma electrónica](https://github.com/BRF-Tech/filex-sign); cada pantalla
la genera filex, y el enlace que recibe el socio es un enlace compartido corriente.

| Definir los recuadros - dé un nombre a cada uno y diga de quién es; el documento viene después | Colocarlos - elija un recuadro, toque la página donde va |
|---|---|
| ![Definición de los recuadros de una solicitud de firma](docs/screenshots/v0.50.0/signing/sign-define-1440.png) | ![Colocación de los recuadros en el documento](docs/screenshots/v0.50.0/signing/sign-place-1440.png) |

| El enlace del socio - la única pantalla pública de filex, con el nombre de su instancia, detrás de un PIN | …y lo que abre: solo los recuadros del propio socio - aquí, un nombre escrito con la fuente que eligió el solicitante (dibujar y subir son las otras dos opciones) |
|---|---|
| ![La página de PIN del firmante externo](docs/screenshots/v0.50.0/signing/sign-outside-pin-1440.png) | ![El firmante externo completando sus recuadros](docs/screenshots/v0.50.0/signing/sign-outside-fill-1440.png) |

| Mientras está en curso - el documento congelado para todos y, en sus detalles, quién ha firmado | Instalar una aplicación - cada permiso que pide, en lenguaje claro, antes de que se ejecute nada |
|---|---|
| ![El documento bloqueado, con su panel Firmas abierto](docs/screenshots/v0.50.0/signing/sign-status-1440.png) | ![La revisión de permisos del asistente de instalación](docs/screenshots/v0.50.0/apps/apps-install-review-1440.png) |

| Una aplicación instalada - de dónde vino, su huella digital y cada permiso que tiene, en lenguaje claro (su configuración y sus acciones vienen después, más abajo en la página) | El convertidor, otra aplicación - cada destino bajo su categoría, tres pasos |
|---|---|
| ![El detalle de una aplicación instalada](docs/screenshots/v0.50.0/apps/apps-detail-1440.png) | ![El asistente del convertidor](docs/screenshots/v0.50.0/apps/convert-wizard-1440.png) |

| Una aplicación que trae su propia interfaz - la revisión muestra la huella digital del paquete, cada dirección fuera de él (una en vivo es un permiso, en amarillo) y lo que un navegador no puede prometer | …y esa interfaz abierta en un archivo de su propio tipo, donde estaría la vista previa de filex. Lee y guarda el archivo a través de filex, en un marco aislado (una pequeña aplicación de ejemplo, escrita para estas imágenes) |
|---|---|
| ![La revisión de instalación de una aplicación con interfaz propia](docs/screenshots/v0.50.0/apps/app-interface-review-1440.png) | ![La interfaz propia de una aplicación abierta como visor de un archivo](docs/screenshots/v0.50.0/apps/app-interface-viewer-1440.png) |

| Todas las aplicaciones de la instancia, y un **paquete de idioma** entre ellas - un manifiesto sin nada que se ejecute, que dice cuánto de este filex traduce y cuyo idioma se va con él |
|---|
| ![La lista Aplicaciones, con un paquete de idioma entre las aplicaciones](docs/screenshots/v0.50.0/langpack/apps-list-1440.png) |

### Lo suyo, esté donde esté

| La campana - con el número de notificaciones sin leer encima, y cada fila lleva adonde dice | Todas sus notificaciones, dentro del explorador - para todos, no solo para los administradores |
|---|---|
| ![La campana con su insignia de notificaciones sin leer, abierta](docs/screenshots/v0.50.0/signing/bell-badge-1440.png) | ![La lista completa de notificaciones encima del explorador](docs/screenshots/v0.50.0/signing/notifications-list-1440.png) |

| Mis enlaces - los enlaces que usted creó, y sus PIN cuando necesite pasarle uno a alguien | Cada tabla de administración - un único menú **Acciones** fijado por fila, el mismo menú que abre el ⋮ del explorador |
|---|---|
| ![Mis enlaces con el menú Acciones de una fila abierto](docs/screenshots/v0.50.0/signing/my-shares-1440.png) | ![Admin → Enlaces compartidos, con el menú Acciones de una fila abierto](docs/screenshots/v0.50.0/signing/admin-table-actions-1440.png) |

### Su marca

| Apariencia - cree un tema con sus propios colores, con vista previa mientras escribe | Establecido como predeterminado, es lo que lleva el explorador de todos… |
|---|---|
| ![El editor de temas](docs/screenshots/v0.50.0/appearance/theme-editor-1440.png) | ![El explorador con el tema del operador](docs/screenshots/v0.50.0/appearance/themed-explorer-1440.png) |

| …y la página de inicio de sesión, antes de que nadie haya iniciado sesión | Un enlace simbólico que filex no seguirá lo dice - en el listado, y con palabras en sus detalles |
|---|---|
| ![La página de inicio de sesión con el tema del operador](docs/screenshots/v0.50.0/appearance/themed-signin-1440.png) | ![Un enlace simbólico que sale del almacenamiento, con insignia](docs/screenshots/v0.50.0/symlinks/symlink-badge-1440.png) |

### El gestor de archivos

| Uso compartido - PIN, caducidad, límite de descargas, `curl` de una línea | Visor de Markdown |
|---|---|
| ![Cuadro de diálogo Compartir](docs/screenshots/v0.50.0/share-modal.png) | ![Visor de Markdown](docs/screenshots/v0.50.0/viewer-markdown.png) |

| …y lo que abre la persona que está al otro lado. filex tiene UNA sola pantalla hacia el exterior - un archivo compartido, una carpeta, una solicitud de archivos, la página de firma de una aplicación y el PIN que va delante de cualquiera de ellos son todos esta página, con el nombre de su instancia |
|---|
| ![Un enlace compartido público, tal como lo ve quien lo recibe](docs/screenshots/v0.50.0/public-share.png) |

| Panel de administración | Página de inicio de la demo |
|---|---|
| ![Panel principal de administración](docs/screenshots/v0.50.0/admin-dashboard.png) | ![Página de inicio de la demo](docs/screenshots/v0.50.0/demo-landing.png) |

| Roles - Administrador, Usuario, Lector y sus propios roles: quién tiene cada uno, qué permite, dónde difiere según la carpeta, sus límites ([docs/PERMISSIONS.md](docs/PERMISSIONS.md)) |
|---|
| ![Admin → Roles: los roles integrados y dos personalizados](docs/screenshots/v0.50.0/roles/roles-list-1440.png) |

| Grupos - conjuntos de personas con nombre, con acceso a carpetas y un rol; miembros agregados a mano o sincronizados con los grupos que trae un inicio de sesión ([docs/GROUPS.md](docs/GROUPS.md)) | Compartir una carpeta con un grupo, junto a personas - Propietario se pide en el cuadro de diálogo, no se concede con un clic |
|---|---|
| ![Admin → Grupos](docs/screenshots/v0.50.0/groups/groups-list-1440.png) | ![Compartir una carpeta con un grupo](docs/screenshots/v0.50.0/groups/share-group-1440.png) |

| Seguridad del inicio de sesión - el límite de intentos, las direcciones permitidas, los proxies de confianza, los bloqueos y el registro de inicios de sesión ([límites de intentos de inicio de sesión](docs/CONFIGURATION.md#sign-in-attempt-limits)) | …y lo que dice el formulario de inicio de sesión de una cuenta bloqueada, con el tiempo restante del bloqueo en su botón |
|---|---|
| ![Admin → Seguridad de acceso](docs/screenshots/v0.50.0/loginsecurity/login-security-1440.png) | ![El formulario de inicio de sesión de una cuenta bloqueada](docs/screenshots/v0.50.0/loginsecurity/login-locked-1440.png) |

| Aplicaciones predeterminadas - cada tipo de archivo gestionado por algo además de filex: quién lo abre y quién genera su miniatura, en el orden que usted establezca ([Aplicaciones predeterminadas](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail)) | Vistas previas de carpetas - cada carpeta mostrada con los tres últimos archivos que entraron en ella; los SVG los genera el motor integrado de filex ([docs/thumbnails.md](docs/thumbnails.md#folder-previews)) |
|---|---|
| ![Complementos → Aplicaciones predeterminadas](docs/screenshots/v0.50.0/defaultapps/default-apps-1440.png) | ![Carpetas mostradas con sus archivos más recientes](docs/screenshots/v0.50.0/thumbnails/folders-grid-1440.png) |

| La interfaz base - adonde llega todo el mundo | Buscar en esta carpeta; `⌘K` / `Ctrl K` pasa la consulta a la paleta |
|---|---|
| ![La interfaz base de filex](docs/screenshots/v0.50.0/driveshell/driveshell-hero-1440.png) | ![Búsqueda en una carpeta](docs/screenshots/v0.50.0/driveshell/driveshell-search-1440.png) |

| Panel de navegación - Inicio, Compartido conmigo, Mis enlaces, Recientes, Destacados, Papelera y los almacenamientos a los que puede acceder | Contraído a la barra de iconos |
|---|---|
| ![Panel de navegación](docs/screenshots/v0.50.0/sidenav/sidenav-expanded-1440.png) | ![Contraído a una barra](docs/screenshots/v0.50.0/sidenav/sidenav-rail-1440.png) |

| Etiquetas - las suyas, o las de su equipo; una etiqueta abre todos los archivos que la llevan, de todas las carpetas en las que están | Papelera - qué se eliminó, de dónde vino y cuánto falta para que desaparezca |
|---|---|
| ![Etiquetas personales y de equipo](docs/screenshots/v0.50.0/tags/tags-kinds-1440.png) | ![La vista de la papelera](docs/screenshots/v0.50.0/sidenav/view-trash-1440.png) |

| Compartido conmigo - carpetas a las que otras personas le dieron acceso, sin instrucciones de montaje | Integrado en la página de otro producto |
|---|---|
| ![Compartido conmigo](docs/screenshots/v0.50.0/sidenav/view-shared-1440.png) | ![Componente web integrado](docs/screenshots/v0.50.0/sidenav/embed-webcomponent-1440.png) |

| Cómo conectarse - las guías, generadas a partir de *su* despliegue | Claves de API - cree las suyas, en el explorador o en una integración (la sesión o el token de una persona; una integración servida a través de un proxy con un único token compartido de tipo *app* no recibe esta entrada) |
|---|---|
| ![Cómo conectarse](docs/screenshots/v0.50.0/sidenav/connect-1440.png) | ![Claves de API](docs/screenshots/v0.50.0/sidenav/apikeys-minted-1440.png) |

| Acceder a filex desde cualquier cosa - S3, SFTP, FTPS, NFS, WebDAV. Cada comando se genera a partir de *su* despliegue |
|---|
| ![Guía de conexión](docs/screenshots/v0.50.0/connections-guide.png) |

| Un almacenamiento que filex no incluye - instalado como complemento en **Complementos → Complementos de almacenamiento** y que describe su propio formulario de configuración |
|---|
| ![Complementos](docs/screenshots/v0.50.0/admin-plugins.png) |

## Inicio rápido - binario

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
    Password: kT9_x4Pq2Nm-BvLs
  Saved to:  ~/.filex/.first-run.txt (mode 0600, shown ONCE)
  Change at: /admin/dashboard?settings=1
═══════════════════════════════════════════════════════════════
```

## Autoalojamiento con Compose o Helm

El `docker run` de arriba basta para probar filex. Para un despliegue de verdad,
hay stacks listos para usar en [`deploy/`](deploy/):

- **[`deploy/compose/`](deploy/compose/)** - Docker Compose:
  - **minimal** - filex + SQLite + disco local (un servicio, cero dependencias).
  - **full** - filex + PostgreSQL + Redis + Caddy (HTTPS automático), más servicios
    adicionales que se pueden activar y desactivar: **OnlyOffice**, **Drawio** y
    un **servidor S3** (Versity S3 Gateway). Active o desactive cada uno con un perfil de
    Compose en `.env`. La conversión la hace la [aplicación Convertir](#aplicaciones), no un
    contenedor auxiliar.
- **[`deploy/helm/filex/`](deploy/helm/filex/)** - un chart de Helm para Kubernetes
  (Deployment + PVC + Ingress opcional). Cada servicio adicional de arriba es un interruptor
  `enabled` en `values.yaml` - incluya PostgreSQL / Redis / un servidor S3, o conecte
  OnlyOffice / Drawio externos.

Las instrucciones paso a paso de cada nivel están en
[docs/INSTALLATION.md](docs/INSTALLATION.md).

filex se ejecuta en la raíz de un host propio o bajo una ruta de un host compartido
(`https://example.com/filex/`): un solo ajuste, `FILEX_BASE_PATH`, y un proxy
que pase la ruta completa - ejemplos para Caddy, nginx y Helm en
[docs/DEPLOYMENT.md → Servir filex bajo una subruta](docs/DEPLOYMENT.md#serving-filex-under-a-sub-path).

## Integrarlo en su aplicación

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

No hay **ninguna hoja de estilos que importar** - el aspecto viaja dentro del bundle y se
inyecta al montar el componente, así que a ese fragmento no le falta nada. ⚠ Un bundler
necesitará que los paquetes opcionales de los visores se declaren como externos
(`monaco-editor` y compañía), algo que [docs/INTEGRATION.md](docs/INTEGRATION.md) muestra en
una sola línea de `rollupOptions.external`; cada una de esas importaciones está protegida,
así los visores se degradan en lugar de romperse.

### Vanilla JS / cualquier framework
```html
<script type="module" src="https://cdn.jsdelivr.net/npm/@brftech/filex/dist/filex.js"></script>
<filex-explorer api-base="http://localhost:5212" sidenav connections ui-profile="simple"></filex-explorer>
```

`sidenav` activa el panel de navegación (está activado de forma predeterminada; el atributo
existe para que una página anfitriona pueda declararlo en un sentido u otro),
`connections` agrega sus entradas "Cómo conectarse" y "Claves de API", y
`ui-profile="simple"` deja desactivados de antemano los controles para usuarios avanzados.
Los tres son claves corrientes de `config`, así que los envoltorios de Vue y React los
establecen de la misma manera - véase
[docs/INTEGRATION.md](docs/INTEGRATION.md).

Los hosts multiinquilino suelen hacer de proxy de la API en el servidor,
inyectar un **token restringido** (`root: tenant-folder`) por solicitud y quitar los
encabezados del cliente - el aislamiento lo impone el backend, no el widget. Un token así es
`kind: "app"`, de modo que el panel oculta las superficies que pertenecen a una sola
persona - Claves de API, Recientes, Destacados, Compartido conmigo - mientras que Subir, los
almacenamientos, Papelera y "Cómo conectarse" se mantienen. Véanse
[docs/INTEGRATION.md](docs/INTEGRATION.md) y
[docs/MCP.md](docs/MCP.md#token-kinds---user-vs-app).

⚠ Una integración que se apoya en la **cookie de sesión** de filex del propio visitante (sin
token) desde una página de otro origen - incluido un subdominio hermano - lee como antes,
pero se rechaza cada cambio que envía (`403 cross_origin_refused`) hasta que ese origen
figure en `FILEX_CORS_ALLOWED_ORIGINS`; el valor predeterminado `*` no lo concede. Un token
Bearer, un host que hace de proxy con una clave, la aplicación de escritorio y la aplicación
web instalada no necesitan nada
([docs/CONFIGURATION.md](docs/CONFIGURATION.md#requests-from-other-origins)).

## Aplicación de escritorio y CLI

El explorador también se distribuye como **aplicación de escritorio para Windows / Linux /
macOS** - el mismo componente que muestran la interfaz web y las integraciones, no una
media copia aparte:

- **Varias cuentas a la vez** - una barra de servidores/inquilinos en la que cada uno
  muestra su propia marca.
- **Arrastrar archivos hacia fuera** - arrastre una selección al escritorio o a otra
  aplicación: las carpetas y las selecciones múltiples llegan como archivos y carpetas
  reales, cada uno por separado. Todo lo que ya se mantiene en este equipo se arrastra al
  instante; el resto se descarga una sola vez y se guarda en caché
  ([docs/DESKTOP.md](docs/DESKTOP.md#dragging-files-out)).
- **Mantener en este equipo** - haga clic con el botón derecho en cualquier carpeta,
  archivo o almacenamiento entero para reflejarlo bajo una sola carpeta de filex en el
  equipo (se puede mover desde Configuración); todo lo demás queda solo en línea, y
  cada fila dice cuál es su caso (✓ ◐ ⟳ ☁). "Mantener solo en línea" envía la copia
  local a la Papelera, o la deja donde está
  ([docs/DESKTOP.md](docs/DESKTOP.md#keeping-folders-on-this-computer)).
- **Sincronización de carpetas** - empareje una carpeta local con una del servidor y se
  mantienen sincronizadas en ambos sentidos mientras la aplicación permanece en la bandeja
  del sistema, **en vivo**: lo que se guarda en el navegador está en el disco en
  aproximadamente un segundo y lo que se guarda localmente está en el servidor igual de
  rápido (el motor sigue tanto el flujo de cambios del servidor como el sistema de
  archivos, con una comprobación completa cada 30 s como red de seguridad), ambas
  versiones conservadas cuando los dos lados cambian a la vez, transferencias y listados
  en paralelo, una primera ejecución que se reanuda donde se interrumpió, papelera local
  de 30 días y un motor que se niega a convertir una carpeta que falta en una eliminación
  masiva ([docs/SYNC.md](docs/SYNC.md)).
- **Abre documentos de Office desde su propio disco** - haga doble clic en un
  `.docx`/`.xlsx`/`.pptx` (o en cualquiera de los diez tipos de Office) y se abre en el
  editor que ejecuta su servidor, en un equipo sin Office instalado. Un documento dentro
  de una carpeta que usted mantiene en este equipo se abre tal cual; todo lo demás se
  copia al servidor, se edita y se vuelve a escribir sobre el original
  ([docs/DESKTOP.md](docs/DESKTOP.md#opening-documents-from-your-computer)).
- **Montar como unidad** - un botón en Configuración conecta el servidor como unidad del
  sistema operativo mediante WebDAV, y otro lo desconecta; el propio token de la cuenta es
  la credencial y nunca aparece en una línea de comandos. Comprobado en Windows; las
  variantes para macOS y Linux están ahí, pero aún sin verificar
  ([docs/DESKTOP.md](docs/DESKTOP.md#mounting-the-server-as-a-drive)).
- **⌘K busca en todas las cuentas** de la barra, con los resultados agrupados bajo una
  insignia por cuenta, y en cada una se busca, se descarga y se arrastra hacia fuera con
  su propio inicio de sesión ([docs/SEARCH.md](docs/SEARCH.md)).
- **Sus notificaciones y su cuenta en la ventana** - la barra superior termina como la de
  la aplicación web: la **campana** (número de notificaciones sin leer, las filas más
  recientes, *Marcar todas como leídas*, la lista completa) y el **avatar** con
  *Configuración de usuario* - el propio cuadro de diálogo de configuración de la
  aplicación web, abierto **dentro de la ventana** - y *Panel de administración* para un
  administrador. Un clic en una notificación abre en la ventana la carpeta con el archivo
  seleccionado. Cerrar sesión sigue en la propia *Configuración → Cuentas* de la
  aplicación ([docs/DESKTOP.md](docs/DESKTOP.md#notifications-and-your-account)).
- **Inicia sesión a través de su navegador**, así que el SSO y la MFA se comportan
  exactamente igual que en la web.
- **Se actualiza sola** - descarga en silencio, instala al salir; `FILEX_NO_UPDATE=1` lo
  desactiva.
- **Funciona sin instalarse**, si eso es lo que necesita: el `.exe` **portátil** de
  Windows, el AppImage de Linux y el `.zip` de macOS funcionan todos desde donde los
  ponga. La copia portátil de Windows guarda todo lo que tiene en una sola carpeta
  `filex-data` junto a sí misma, así que eliminar esa carpeta no deja nada suyo en un
  equipo que no es suyo - a cambio, no se actualiza sola.

**Instálela** desde la Microsoft Store (Windows 10/11) o la Snap Store (Ubuntu y otros
Linux con snapd):

<p>
<a href="https://apps.microsoft.com/detail/9PKXDJLVZWXW"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/badges/ms-store-light.svg"><img src="docs/badges/ms-store-dark.svg" alt="Descargar de Microsoft Store" height="52"></picture></a>&nbsp;&nbsp;&nbsp;
<a href="https://snapcraft.io/filex-app"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/badges/snap-store-white.svg"><img src="docs/badges/snap-store-black.svg" alt="Obténgalo en la Snap Store" height="52"></picture></a>
</p>

o con un gestor de paquetes:

```bash
brew install brf-tech/filex/filex-app       # macOS 13+ (Apple Silicon), Homebrew tap BRF-Tech/homebrew-filex
sudo snap install filex-app                 # the same snap as the badge above
```

La versión de la Store (*filex File Manager*) es la única copia para Windows con firma de
código - Microsoft la firma - y la Store la mantiene actualizada. winget
(`BRFTech.filex-app`) se envía con cada versión y está a la espera de su primera revisión
por parte de los moderadores de winget, así que `winget install` todavía no lo encuentra.
El instalador, el `.exe` portátil, el AppImage, el `.deb`, el `.rpm` y el `.dmg` se
adjuntan a la [última versión](https://github.com/BRF-Tech/filex/releases/latest) -
todavía sin firma de código, así que es de esperar un aviso de SmartScreen con el
instalador de Windows. Detalles: [docs/DESKTOP.md](docs/DESKTOP.md). Solo la CLI:
`brew install brf-tech/filex/filex` ([docs/CLI.md](docs/CLI.md); su paquete de winget,
`BRFTech.filex`, está en la misma revisión).

En Linux la aplicación nunca se ejecuta sin el entorno aislado de Chromium. El `.deb` y el
`.rpm` no necesitan nada; en Ubuntu 23.10 y posteriores un AppImage necesita, una sola
vez, un perfil de AppArmor, y el Snap necesita
`sudo snap connect filex-app:browser-sandbox` hasta que la Snap Store lo conecte por sí
sola - en ambos casos la aplicación lo dice y muestra el paso
([docs/DESKTOP.md](docs/DESKTOP.md#appimage-on-recent-ubuntu)).

**ARM (arm64)** - lo que se distribuye para esta arquitectura (cada versión compila todo
esto y lo ejecuta en equipos arm64 antes de publicarse):

| | arm64 |
|---|---|
| Binario de servidor + CLI | Linux, macOS y Windows: `filex-<os>-arm64` y los archivos comprimidos `.tar.gz` / `.zip` |
| Imágenes de Docker (`ghcr.io/brf-tech/filex`, full y slim) | multiarquitectura - `docker pull` elige arm64 por sí solo |
| Aplicación de escritorio - Linux | `filex-desktop-arm64.AppImage`, `filex-desktop-arm64.deb`, `filex-desktop-aarch64.rpm` y la Snap Store (`sudo snap install filex-app` elige arm64) - desde la 0.48.1 |
| Aplicación de escritorio - Windows on Arm | `filex-desktop-arm64.exe` (instalador) y `filex-desktop-portable-arm64.exe` - desde la 0.48.1; la aplicación se actualiza sola a la compilación arm64 |
| Aplicación de escritorio - macOS | solo Apple Silicon (sin compilación para Intel) |
| Homebrew | la CLI (`filex`) en Apple Silicon y en Linux sobre Arm; la aplicación de escritorio (`filex-app`) en Apple Silicon |

En un equipo Arm, la oferta *Obtenga la aplicación de escritorio de filex* que muestra la
aplicación (y su copia en Configuración) pone primero el archivo arm64 y la lista de
descargas de [filex.sh](https://filex.sh/#downloads) lo resalta, según lo que indica el
navegador (los client hints de Chromium, el `aarch64` de Firefox). A un navegador que no
lo dice (Safari, Firefox en Windows) se le ofrece el archivo x64 con el arm64 al lado.

El mismo binario es también un cliente para servidores, scripts y equipos sin interfaz
gráfica:

```bash
filex client login --url https://files.example.com
filex client upload build/report.pdf docs://ci-artifacts/
filex client mv docs://ci-artifacts/report.pdf archive://2026/   # across storages, waits for the job
filex client run convert convert docs://data/table.csv --param target=xlsx

filex sync add ~/Documents/work docs://work   # the engine the desktop app uses
filex sync run --watch 30s
```

Véanse [docs/CLI.md](docs/CLI.md) y [docs/SYNC.md](docs/SYNC.md).

## Agentes de IA / MCP

filex incluye una superficie de automatización autenticada por token en `/api/ai` (listar,
leer, escribir, mover, copiar, eliminar, buscar, compartir, comprimir) y habla
**Model Context Protocol** en `/api/ai/mcp`. Un agente también ejecuta las propias
operaciones del explorador - la copia entre almacenamientos, las acciones de aplicaciones
como **convertir**, la cola de operaciones, la papelera y el historial de versiones, los
archivos comprimidos 7z/TAR, sus enlaces y solicitudes de archivos, la campana, los
destacados, los comentarios y los permisos sobre un elemento que le pertenece - a través de
los propios gestores del explorador, así que las reglas son las del explorador. Una clave de
administrador llega a lo que hace el panel (inquilinos, proveedores de identidad, seguridad
del inicio de sesión, Aplicaciones predeterminadas, webhooks, almacenamientos) a través de
los propios gestores del panel. `/api/ai` y `/api/files` se describen en un
[archivo OpenAPI 3.1](backend/internal/api/openapi.json):

```bash
claude mcp add filex --transport http https://files.example.com/api/ai/mcp \
  --header "Authorization: Bearer <api-token>"
```

Una clave de API lleva permisos por verbo (`read`, `write`, `delete`, más `mcp` y `admin`),
puede estar **restringida a una sola carpeta**, está sujeta a los mismos permisos concedidos
y roles de RBAC que la interfaz y va marcada con identidades por clave para que los
registros de auditoría, los enlaces compartidos y la presencia muestren *quién* (qué
integración) hizo qué. Los verbos rigen en **todas las superficies a las que llega la
clave** - `/api/ai`, las herramientas MCP, las propias rutas del explorador (y por tanto
`filex client` y una integración), WebDAV, SFTP, FTPS, así como las claves S3 y las
exportaciones NFS creadas a partir de ella. Algunos actos nunca corresponden a una clave:
instalar un complemento - un agente **deja una solicitud de instalación** que un
administrador aprueba en el panel - y hacer administrador a alguien. Una clave debe nombrar
al menos un permiso - una lista vacía se rechaza, nunca se lee como "todos" - y **lo que
entrega nunca puede ser más amplio que la propia clave**: si a través de una clave de solo
lectura o restringida a una carpeta se pide una clave de API, una clave de acceso S3, una
exportación NFS o una clave SSH con más verbos, con una raíz fuera de la suya o con una
vigencia más larga, la solicitud se rechaza con `403 token_ceiling`, que nombra lo que era
demasiado amplio. Con un agente, **un movimiento nunca sobrescribe**: un elemento que va
hacia un nombre ya ocupado queda a su lado con uno libre (`report-copy.txt`), exactamente
igual que en un movimiento hecho en la interfaz, y la respuesta nombra la ruta en la que
realmente quedó. Y conoce las **carpetas cifradas**: cada fila dice si está cifrada, el
texto cifrado nunca se entrega como si fuera el archivo (`409 E2E_ENCRYPTED`) y el texto sin
cifrar que se escribe en una carpeta cifrada se rechaza salvo que quien llama declare que lo
hace a propósito (`allow_plaintext`)
([docs/MCP.md](docs/MCP.md#encrypted-folders-and-fxe)).

Un archivo grande que ya está en el disco del agente nunca cabe en una llamada a una
herramienta - sus bytes tendrían que viajar por el contexto del modelo. **Los tickets de
subida** lo resuelven: una llamada autorizada fija el destino y devuelve una URL de corta
duración y de un solo uso que no necesita **ninguna credencial**, de modo que incluso un
agente sin token de filex puede terminar la transferencia con `curl -T bigfile <url>`.
Detalles: [docs/MCP.md](docs/MCP.md).

## Aplicaciones

Un **complemento de almacenamiento** le enseña a filex un backend del que nunca ha oído
hablar. Una **aplicación** le enseña una *cosa que hacer con los archivos* - firmarlos,
convertirlos, enviarlos a alguien de fuera - y es, a propósito, otra clase de complemento:
un **módulo de WebAssembly que se ejecuta dentro de filex, en un entorno aislado** que no le
entrega nada que no se le haya concedido. Sin sistema de archivos, sin red, sin entorno, sin
ningún programa en su servidor: solo las funciones del host que pide su manifiesto, cada una
mostrada al administrador en lenguaje claro antes de que se instale nada, y solo los
archivos que realmente seleccionó la persona que la ejecutó. Los motores pesados que una
aplicación pueda querer (ffmpeg, ImageMagick, Ghostscript, poppler, rsvg) son los del propio
servidor, ofrecidos con un permiso por motor; los documentos de Office pasan por el
ONLYOFFICE Document Server que usted conecte, como motor ofimático - filex no ejecuta
LibreOffice.

Una aplicación también puede traer - o no ser nada más que - **una interfaz propia**: HTML,
CSS y JavaScript que escribió su autor, un editor o un visor para un formato. filex la sirve
desde el paquete que usted aprobó (fijado por su SHA-256) en un **marco aislado**: un origen
opaco que no puede leer la sesión, las cookies ni las páginas de filex, una política de
contenido que filex escribe a partir de los permisos concedidos a la aplicación (sin ninguna
conexión, sin almacenamiento, sin formularios, sin ventanas emergentes) y un único canal de
mensajes verificado a través del cual filex le entrega solo los archivos con los que se abrió
y guarda sobre ellos - una versión nueva, o un borrador. Puede agregar su propio tipo de
archivo a **Nuevo documento**, abrirse en la pestaña del editor y entregarle a usted un
archivo para que lo conserve - cada vez que usted lo permita. ⚠ Los navegadores no pueden
impedir del todo que una página envíe datos hacia fuera (WebRTC ignora una política de
contenido; Chrome permite a filex cerrarlo, en Firefox filex solo puede quitarlo de la
página - un cinturón de seguridad, no un muro), así que la revisión de instalación lo dice
con claridad: **confíe en una aplicación con interfaz tanto como confiaría a su autor los
archivos que abre en ella.**

Lo que agrega una aplicación vive donde vive todo lo demás: filas en el menú de archivos,
pantallas que filex genera para ella o su propia interfaz, tareas en la misma cola que una
copia - con progreso, **Cancelar** y un resultado que se versiona, se analiza y se indexa
como cualquier otra escritura - una sección en los detalles de un archivo, una pantalla de
inicio bajo **Aplicaciones** en la navegación y, cuando necesita a alguien sin cuenta, un
enlace que es un **enlace compartido** corriente: en la misma lista, bajo la misma política
de bloqueo por PIN y de caducidad, revocable por usted como cualquier otro enlace. A una
aplicación que lo pide, filex también la despierta una vez por hora para que haga su propio
trabajo programado - una solicitud de firma que se cierra sola en su fecha límite y envía
los recordatorios que usted pidió.

No todas las aplicaciones ejecutan código. Un **paquete de idioma** es un manifiesto de
cadenas y nada más: se instala únicamente a partir del manifiesto - sin módulo, sin Go, sin
versión publicada - nunca inicia un entorno de ejecución y agrega su idioma al explorador,
al panel de administración, a las páginas públicas y al texto que escribe el servidor.
**Complementos → Aplicaciones** lo muestra como *Paquete de idioma* con su cobertura de la
versión en ejecución, y lo que le falte se muestra en inglés. El español, el alemán y el
francés se distribuyen como ejemplos, y `BRF-Tech/filex-lang-template` guía a un traductor
desde la exportación hasta la instalación.

Cuatro aplicaciones se distribuyen junto con filex, como repositorios públicos que usted
puede instalar, leer y bifurcar. Las dos primeras son módulos; las dos últimas son solo una
interfaz, sin nada que se ejecute en el servidor:

| Aplicación | Qué agrega |
|---|---|
| **[Firma electrónica](https://github.com/BRF-Tech/filex-sign)** - `BRF-Tech/filex-sign` | **Firmar…**, **Solicitar firmas…**, **Firmar / Completar** y **Verificar** en un PDF, y solo en un PDF (un documento de Office se convierte primero en uno con **Convertir**). Una solicitud es un asistente breve: quién firma - personas de este filex, que firman dentro de él, y cualquier otra persona, por nombre o correo electrónico, que recibe un **enlace privado**, detrás de un PIN salvo que usted indique lo contrario - en qué orden, los recuadros con nombre, asignados a cada firmante y colocados después en la página; cuánto tiempo permanece abierta, si el archivo queda **congelado** mientras tanto y si al final se escribe un **PDF de registro de auditoría**. El resultado es un PDF firmado con PAdES que queda **certificado y sellado**: la primera firma certifica el documento para que las posteriores solo puedan completar y firmar, y cuando llega la última, **el propio filex sella el archivo completo** con el sello propio de la instalación, bloqueado de modo que cualquier cambio posterior se señala como no permitido. El **SHA-256 de exactamente esos bytes sellados**, la huella digital del sello y la forma de comprobarlos van al solicitante y a todos los firmantes, de dentro y de fuera, y al registro de auditoría. Opcionalmente, el archivo firmado queda **bloqueado en filex** hasta que un administrador levante el bloqueo. **Las claves de firma nunca salen del servidor**: la autoridad de certificación propia de la instancia (o una que usted importe) emite un certificado por firmante, y la clave con la que se hizo una firma se destruye segundos después - la clave del sello es la única excepción, que queda en poder del host y nunca se entrega. |
| **[Convertir](https://github.com/BRF-Tech/filex-convert)** - `BRF-Tech/filex-convert` | **Convertir…** en cualquier archivo: imágenes, video, audio, documentos, libros electrónicos, archivos comprimidos, datos, subtítulos y fuentes. El destino se elige entre botones agrupados por categoría, luego solo los ajustes que importan para él, luego una revisión. La mayoría de las conversiones se ejecutan en Go puro dentro del entorno aislado; el resto usa los motores del servidor cuando están instalados, y un destino que necesita un motor que falta lo dice en lugar de omitirse en silencio. |
| **[filextext](https://github.com/BRF-Tech/filextext-app)** - `BRF-Tech/filextext-app` | Un **espacio de trabajo de texto cifrado de extremo a extremo** en un único archivo `.fxtxt`: páginas y carpetas a la izquierda, pestañas arriba, el editor BlockSuite de AFFiNE en el centro (encabezados, listas, tareas, código, tablas, imágenes, enlaces entre páginas, Markdown de entrada y de salida). Se cifra **en su navegador** con las mismas claves y la misma clave de recuperación que las [carpetas cifradas](docs/E2E-ENCRYPTION.md) de filex; filex almacena texto cifrado y nunca ve la contraseña ni una palabra del texto. Un `.fxtxt` se abre en él en lugar de la vista previa, y **Nuevo documento** incorpora *Espacio de trabajo cifrado (.fxtxt)*. |
| **[draw.io](https://github.com/BRF-Tech/filex-drawio)** - `BRF-Tech/filex-drawio` | El editor de diagramas **draw.io**, dentro de filex: los archivos `.drawio` y `.dio` se abren en él en lugar de la vista previa, **Guardar** escribe una nueva versión y **Nuevo documento** incorpora *Diagrama de draw.io*. Los archivos propios de draw.io se sirven desde el paquete de la aplicación (fijado por su SHA-256); no accede a nada fuera de él. |

**Instale una desde GitHub** - *Admin → Complementos → **Aplicaciones** →
**Instalar una aplicación** → Repositorio de GitHub*: escriba `BRF-Tech/filex-sign` y la
etiqueta de la versión. filex lee el `filex-app.json` del repositorio, descarga el módulo (o
el paquete de la interfaz) que ese archivo indica y lo rechaza a menos que su SHA-256
coincida, y después se detiene en la **revisión de permisos**. No se instala nada hasta que
haya leído cada permiso y marcado *Entiendo*; lo concedido es exactamente esa lista, y una
actualización que pide más vuelve a detenerse en la revisión. `FILEX_PLUGIN_TRUSTED_KEYS`
hace obligatorios los módulos firmados (una instalación desde GitHub no lleva firma, así que
en una instancia de ese tipo suba en su lugar el módulo con su firma). Cada descarga - una
aplicación, su comprobación de actualizaciones, un complemento de almacenamiento - va solo a
direcciones públicas, evaluadas después del DNS y en cada redirección: para instalar desde un
servidor de su propia red, suba los archivos. Las aplicaciones están desactivadas en el modo
demo. Una clave de API - un agente, un script, la CLI - no puede instalar ninguna:
**deja una solicitud**, filex fija los bytes y los permisos que instalaría, y un
administrador la aprueba en **Complementos → Solicitudes de instalación**.

**Quién puede usarla.** Una aplicación puede declarar **permisos propios** - una aplicación
de firma exige uno para *Solicitar firmas*, mientras que firmar lo que le enviaron no
necesita ninguno - y usted los concede por rol y por persona, como los del propio filex; una
acción que alguien no tiene concedida no aparece en su menú y se rechaza si se pide
([Permisos de las aplicaciones](docs/APP-PLUGINS.md#app-permissions)).

**Nada se actualiza solo.** Una vez al día (y con **Buscar actualizaciones**) filex pregunta
al origen de cada aplicación - sus versiones publicadas en GitHub, la rama de un paquete de
idioma o la dirección del manifiesto desde la que se instaló - si hay una versión más reciente
que este filex pueda ejecutar, y le avisa: la versión espera bajo *Actualización disponible*
(o *Requiere aprobación*, cuando pide más) hasta que un administrador haya revisado lo que
cambia - permisos, módulo, archivos de la interfaz, notas - y la haya aprobado, y entonces
todos usan esa versión. **Volver a *versión*** restaura la que reemplazó. Los complementos de
almacenamiento pueden seguir un origen de la misma manera. Una aplicación dice con qué
versiones de filex funciona (`"filex": ">=0.47.0"` en su manifiesto), y filex no la instala
fuera de ese rango.

**Guía del operador:** [docs/APP-PLUGINS.md](docs/APP-PLUGINS.md) - instalar y controlar
aplicaciones, la ronda de firma de principio a fin, el convertidor, las activaciones
programadas y lo que protege los enlaces públicos de una aplicación. **Escribir una** (Go
estándar, `GOOS=wasip1`, con un kit de pruebas): parta del repositorio de plantilla
[BRF-Tech/filex-app-template](https://github.com/BRF-Tech/filex-app-template) y de
[docs/PLUGIN-KIT.md](docs/PLUGIN-KIT.md); el contrato de comunicación:
[docs/APP-PLUGINS-API.md](docs/APP-PLUGINS-API.md). La otra clase de complemento, un backend
de almacenamiento: [docs/PLUGINS.md](docs/PLUGINS.md).

## Funciones

- **Multialmacenamiento** - monte muchos almacenamientos a la vez (local, S3, FTP, SFTP, WebDAV, SMB/NAS); cada uno aparece como una carpeta de nivel superior. Cada uno lleva además una dirección que nunca se mueve: el nombre del almacenamiento es el primer segmento de la ruta en WebDAV, SFTP, NFS y la API S3, así que cambiarle el nombre a uno cambiaría su dirección - un montaje definido con su **uid** sobrevive a cualquier cambio de nombre. **Copie o corte en uno y pegue en otro**: filex transmite el árbol entre los dos controladores, conserva la marca de tiempo de cada archivo y solo elimina el original una vez verificada la copia. Un almacenamiento caído se notifica en segundos, y solo al silencio se le agota el tiempo de espera, nunca a una transferencia que sigue avanzando (S3, WebDAV, FTP, SFTP y SMB: [docs/STORAGE.md](docs/STORAGE.md#when-the-store-is-down)). Una entrada sobre la que el almacenamiento no pudo responder (ni "aquí" ni "no encontrado") se conserva, marcada con un **!** y con la respuesta del propio almacenamiento, y no se hace nada con ella - en el explorador, las API REST y de agentes, el uso compartido y los editores (los protocolos de archivos no leen la marca) - hasta que el almacenamiento vuelva a responder ([PLUGINS.md](docs/PLUGINS.md#an-entry-your-stat-cannot-answer-for)).
- **Arrastrar archivos a su escritorio** - en la aplicación de escritorio, arrastre una selección al Explorador de Windows, a Finder o a otro programa y llega como archivos y carpetas reales, cada uno por separado, no como un archivo comprimido; en un navegador, un solo archivo se arrastra hacia fuera de la misma manera - también en la aplicación de administración, mediante un enlace de un solo archivo que dura un minuto y funciona una sola vez ([docs/DESKTOP.md](docs/DESKTOP.md#dragging-files-out)).
- **Complementos de almacenamiento** - un almacenamiento del que filex nunca ha oído hablar es un **programa aparte** que usted instala desde el panel de administración: describe su propio formulario de configuración, filex habla con él en un pequeño protocolo HTTP/JSON y su controlador se comporta entonces como cualquiera de los integrados. Cualquier lenguaje; un SDK de Go lo reduce a tres métodos. filex **pone a prueba cada capacidad que un complemento declara** - al instalarlo, y de nuevo contra la configuración que usted escribe cuando guarda un almacenamiento que lo usa - y rechaza el que no puede hacer lo que dice, porque un controlador que funciona a medias produce fallos que parecen averías de filex. Las actualizaciones reemplazan el binario donde está y vuelven al binario anterior si el nuevo no arranca; en cada inicio se comprueban de nuevo el hash y la firma del binario, y cada complemento lleva un **registro** de sus inicios, sus fallos y las entradas sobre las que no pudo responder (*Acciones → Registro*) ([docs/PLUGINS.md](docs/PLUGINS.md)).
- **Aplicaciones** - una segunda clase de complemento: un módulo de **WebAssembly en un entorno aislado**, una **interfaz propia en un marco aislado** (sin ninguna conexión, sin acceso a la sesión de filex; véase en [Aplicaciones](#aplicaciones) lo que un navegador no puede prometer), o ambas cosas - que agrega acciones al menú de archivos (*Solicitar firmas…*, *Convertir…*), pantallas que filex genera para ella, una sección en los detalles de un archivo, una pantalla de inicio bajo **Aplicaciones** en la navegación y enlaces que un participante externo abre sin tener cuenta. Se instala desde un repositorio de GitHub mediante una **revisión de permisos** - la aplicación obtiene exactamente lo que usted aprobó y nada más: sin sistema de archivos, sin red, sin ningún programa en su servidor; los motores pesados (ffmpeg, ImageMagick, …) son los del propio servidor y los documentos de Office pasan por el ONLYOFFICE que usted conecte, unos y otro ofrecidos permiso por permiso. Las pantallas que filex genera obedecen a las reglas de filex, las haya escrito quien las haya escrito - cada opción a la vista en lugar de escondida en una lista desplegable, nada oculto tras "avanzado", una pregunta por paso. El enlace que una aplicación envía a un firmante externo es un **enlace compartido** corriente, así que usted lo ve y lo revoca en la misma lista que todo lo demás, y nunca vale más que quien lo creó: una tarea iniciada desde él pasa las mismas comprobaciones que una iniciada dentro de filex (una acción que usted desactivó sigue desactivada, se vuelve a leer el acceso que quien lo creó tiene al documento), y deja de funcionar cuando se desactiva la cuenta de quien lo creó - hasta que se vuelva a activar. Una aplicación también puede traer **su propia interfaz** - HTML y JavaScript que filex sirve desde el paquete aprobado de la aplicación en un marco aislado cuya política no permite ninguna conexión, ningún almacenamiento ni ninguna cookie, y que hablan con filex por un único canal verificado; un editor que no necesita nada en el servidor (draw.io, filextext) es una aplicación sin módulo alguno ([la interfaz propia de una aplicación](docs/APP-PLUGINS.md#an-apps-own-interface), SDK `@brftech/filex-app-ui`). A una aplicación a la que usted concede `schedule`, filex la despierta una vez por hora para que haga su propio trabajo en el minuto que ella eligió, como una tarea corriente en la cola. Nada se actualiza solo: filex comprueba a diario el origen de cada aplicación y avisa cuando hay una versión más reciente, un administrador revisa lo que cambia y la aprueba, todos usan la versión aprobada - y **Volver a *versión*** deshace una aprobación. Las aplicaciones dicen con qué versiones de filex funcionan. Una clave de API nunca instala ninguna - deja una **solicitud de instalación** que un administrador aprueba - y una aplicación puede declarar **permisos propios** que usted concede por rol y por persona ([Permisos de las aplicaciones](docs/APP-PLUGINS.md#app-permissions)). Una aplicación puede **generar miniaturas** de tipos para los que filex no lo hace (se le entregan los bytes de un solo archivo y nada más), y **Aplicaciones predeterminadas** decide, por tipo de archivo, qué aplicación lo abre y cuál genera su miniatura, en qué orden; cada persona elige entre las aplicaciones que lo abren y siguen activadas, con *Usar siempre esta aplicación* guardado en su cuenta - una sola elección para el navegador, la aplicación de escritorio y una integración ([Aplicaciones predeterminadas](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail)). Cuatro se distribuyen como repositorios públicos: **Firma electrónica**, **Convertir**, **filextext** y **draw.io** ([Aplicaciones](#aplicaciones), [docs/APP-PLUGINS.md](docs/APP-PLUGINS.md#the-apps-that-ship-alongside-filex), [escribir una](docs/PLUGIN-KIT.md)).
- **Firma electrónica** ([`BRF-Tech/filex-sign`](https://github.com/BRF-Tech/filex-sign), una aplicación) - firme usted un PDF, o pida a otras personas que lo firmen: las personas de este filex firman dentro de él, desde una notificación que abre la pantalla correcta; cualquier otra persona recibe un enlace privado, detrás de un PIN de forma predeterminada - uno que filex guarda para usted (véase *Uso compartido*). Los recuadros **se definen primero** - un nombre, de quién es, obligatorio o no, el formato de una fecha - y **se colocan en la página después**, dos preguntas en dos pantallas. El documento puede quedar **congelado** para todos, administradores incluidos, mientras está en curso; los recordatorios y la fecha límite funcionan por sí solos; el solicitante sigue a cada firmante en los detalles del archivo y en la pantalla de inicio de la aplicación; y el resultado es un PDF firmado con PAdES que queda **certificado** por su primera firma y **sellado por filex** tras la última, así un lector de PDF señala como no permitido cualquier cambio hecho después - con el **SHA-256 de los bytes sellados** y la huella digital del sello enviados al solicitante y a cada firmante, un **PDF de registro de auditoría** cuando usted lo pida, un recibo para cada firmante y una opción para mantener bloqueado el archivo terminado hasta que un administrador levante el bloqueo. **Verificar** informa sobre cualquier PDF firmado: cada firma, la certificación, el sello y si este es el archivo cuyo hash se envió. Las claves de firma nunca salen del servidor: la autoridad de certificación propia de la instancia, o una que usted importe, emite un certificado para cada firmante, y la clave con la que se hizo una firma se destruye segundos después ([docs/APP-PLUGINS.md](docs/APP-PLUGINS.md#signing-documents-end-to-end)).
- **Convertir, como aplicación** ([`BRF-Tech/filex-convert`](https://github.com/BRF-Tech/filex-convert)) - *Convertir…* en cualquier archivo o selección: imágenes, video, audio, documentos, libros electrónicos, archivos comprimidos, datos, subtítulos y fuentes. El destino es un botón bajo su categoría, luego solo los ajustes que importan para él, luego una revisión; la mayoría de las rutas se ejecutan en Go puro dentro del entorno aislado, el resto mediante los motores del servidor, y un destino que necesita un motor que falta se muestra como tal en lugar de omitirse en silencio ([docs/APP-PLUGINS.md](docs/APP-PLUGINS.md#converting-files)). (El antiguo contenedor auxiliar del convertidor en iframe se quitó en la 0.48.)
- **Pasarela de protocolos** - al mismo árbol se puede acceder como **S3** (SigV4; aws-cli, rclone, restic, mc, s3fs), **SFTP** (OpenSSH, WinSCP, FileZilla, sshfs), **FTPS** (TLS explícito, para los equipos que solo aprendieron FTP; entréguele el certificado de renovación automática de su proxy inverso - se vuelve a leer cuando cambia), **NFSv3** (clientes NAS de la LAN, reproductores multimedia) y **WebDAV** - cada uno con su propia credencial, que usted puede revocar por separado, y todos ellos detrás de los mismos permisos, la misma papelera y la misma cuota que la interfaz ([docs/PROTOCOLS.md](docs/PROTOCOLS.md)).
- **`filex mount`** - conecte un servidor filex remoto a una carpeta por HTTPS corriente: una carpeta en Linux, una **letra de unidad en Windows** (`filex mount Z:`, necesita [WinFsp](https://winfsp.dev), que es gratuito). No es una sincronización: no se copia nada salvo una caché de lectura acotada, así que abre un archivo entre cien mil sin descargar el resto.
- **Colaboración en tiempo real** - barra de presencia con avatares en vivo + foco, actualizaciones instantáneas de los cambios en archivos por WebSocket, sondeo como alternativa. Una sola escritura se anuncia en el momento en que llega; una ráfaga (la extracción de un zip, la subida de una carpeta, un cliente NFS que escribe fragmento tras fragmento) se fusiona en una trama por ventana de tiempo, así la carpeta sigue en vivo sin inundar la página ([docs/REALTIME.md](docs/REALTIME.md)).
- **Un listado que se comporta como una tabla** - cambie el tamaño de una columna, oculte una, arrastre otra a un lugar nuevo; la tabla se desplaza hacia los lados en lugar de quitar una columna cuando se queda sin espacio, y la columna de acciones permanece fijada a la derecha. Ordene por nombre, tipo, fecha o tamaño, en cualquiera de las dos direcciones, y **la cuadrícula y la lista obedecen al mismo orden** - hasta esta versión "ordenado por tamaño" era un hecho sobre una sola vista, y cambiar de vista reordenaba las filas ante sus ojos. Ordenadas por fecha, las tres vistas agrupan las filas bajo **Hoy · Ayer · Esta semana · Este mes** y después mes a mes, en **su** zona horaria y no en la del navegador.
- **Cada carpeta recuerda cómo la dejó** - opcional, desde la configuración de usuario: el modo de vista y el orden de cada carpeta que sí configuró se guardan **por persona en el servidor** para que le acompañen a otro equipo y a la aplicación de escritorio, y nunca se filtran a ninguna otra persona que mire la misma carpeta. Desactivado de forma predeterminada, en cuyo caso su última elección simplemente se aplica en todas partes ([docs/INTEGRATION.md](docs/INTEGRATION.md)).
- **A quién pertenece un archivo** - cada nodo lleva su propietario, el listado tiene una columna **Propietario** y la fila de filtros una entrada **Propietario**, y la cuota se carga al propietario y no a quien tocó el archivo por última vez.
- **Archivos comprimidos en todos los formatos habituales** - cree, abra y extraiga ZIP, 7z,
  TAR y sus variantes gzip/bzip2/xz (RAR donde el 7-Zip del servidor lo admite), con
  contraseña para ZIP y 7z, como tarea en segundo plano con progreso. Los enlaces y los
  dispositivos dentro de un archivo comprimido se rechazan antes de que se escriba nada, y
  los límites de tamaño y de entradas detienen una bomba de compresión en el límite
  ([docs/ARCHIVES.md](docs/ARCHIVES.md)). Contribución de Alex (@ahjephson).
- **Llevarse una selección** - elija varios archivos y carpetas y **Descargar** los transmite como un solo archivo comprimido, creado sobre la marcha: no se escribe ningún archivo temporal en su almacenamiento, nada se almacena en búfer en la pestaña y un archivo comprimido de 700 MB le cuesta al servidor menos de un megabyte de memoria. **Mover a** y **Copiar a** abren un selector de carpetas que abarca todos los almacenamientos y rechaza un destino en el que usted no puede escribir - en el servidor, no solo en el cuadro de diálogo.
- **Nuevo documento** - cree un archivo de Word, Excel, PowerPoint u OpenDocument, o de cualquier formato de texto o de código, desde el menú **+ Nuevo**: póngale nombre - cualquier nombre, `LICENSE`, `Makefile` o `test.conf` incluidos - elija dónde va, y se abre en el editor que lo gestiona. Las plantillas son documentos reales, mínimos y válidos, compilados en el binario, así que esto funciona en una instalación sin ninguna suite ofimática; un tipo que este despliegue luego no podría abrir ni siquiera se ofrece, y el cuadro de diálogo dice por qué. Un nuevo documento es un **borrador** hasta que se guarda por primera vez: no aparece nada en la carpeta hasta que presione Guardar (si entretanto el nombre quedó ocupado, se pregunta - ¿`report (2).txt`? - nunca se reemplaza), al cerrarlo se pregunta *Guardar en disco / Conservar en Borradores / Descartar*, y **Borradores** en el panel de navegación conserva los que aún no ha terminado, que nadie más ve - 50 por persona de forma predeterminada, valor que se define en la página Protección del panel de administración ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md#drafts-nothing-is-in-the-folder-until-you-save)).
- **RBAC + permisos por elemento** - roles (Administrador, Usuario, Lector y roles personalizados, cada uno de ellos una lista de permisos - [docs/PERMISSIONS.md](docs/PERMISSIONS.md)), acceso por archivo/carpeta con herencia en **Admin → Acceso a carpetas**, **grupos** que tienen acceso a carpetas y un rol para todos los que están en ellos - miembros agregados a mano o sincronizados con los grupos que trae un inicio de sesión ([docs/GROUPS.md](docs/GROUPS.md)) - invitaciones para compartir por correo electrónico (SMTP), búsqueda y listados que tienen en cuenta el acceso concedido. **Compartido conmigo** responde a la pregunta inversa desde el lado del destinatario - aquello a lo que otras personas le dieron acceso, y a qué almacenamientos llega solo a través de un permiso concedido.
- **La interfaz base** - un solo diseño, para el operador y para el usuario final por igual, en la aplicación de administración, la aplicación de escritorio y cada integración: una barra superior que ocupa todo el ancho, con el control para contraer y la marca del producto en su extremo izquierdo, un único **campo de búsqueda** cuyo indicador ⌘K / Ctrl+K pasa la consulta a la paleta de comandos (el campo busca en esta carpeta; la paleta es donde viven "en todas partes", las búsquedas guardadas y los comandos), un menú principal **+ Nuevo** (subir archivos · nueva carpeta · **nuevo documento** · solicitar archivos), una fila de filtros **Tipo · Propietario · Modificado · Tamaño** bajo la ruta de navegación, **Carpetas** y **Archivos** como secciones rotuladas en la vista de cuadrícula, un panel de detalles dividido en **Detalles** (con "Personas con acceso" y una fila de enlace compartido) y **Actividad** (historial de versiones y comentarios), y una **línea de almacenamiento** bajo la navegación. El tema, la paleta, el idioma, la densidad, la zona horaria, la página de inicio y los interruptores de notificaciones viven todos en la **configuración de usuario**, a la que se llega desde el avatar - y la aplicación web le guarda el tema, la paleta, la densidad y el idioma en su **cuenta**, no en el navegador, así que le esperan en el siguiente; el editor de atajos de teclado y *Reiniciar el recorrido* están en el mismo menú. No se quita nada de la compilación - una integración, que no tiene cuadro de diálogo de configuración, conserva un menú "⋯" que aún los contiene ([docs/INTEGRATION.md](docs/INTEGRATION.md)).
- **Inicio, dentro de la interfaz base** - la vista inicial para todo el mundo, administradores incluidos: sus almacenamientos, lo último que abrió y lo que destacó, como tarjetas en el área de contenido con el mismo panel de navegación y la misma cabecera que los archivos. Moverse entre Inicio y una carpeta cambia el contenido y nada más. Un operador que prefiera llegar al panel principal de administración lo elige en la configuración de su perfil.
- **Panel de navegación** - el menú **+ Nuevo** como acción principal, los destinos Inicio / Mis archivos / Compartido conmigo / **Mis enlaces** / Recientes / Destacados / **Borradores** / Papelera, los almacenamientos que puede ver - **en su propio orden** (arrastre una fila, o use Subir / Bajar / Ordenar por nombre desde el menú de la fila; se guarda en su cuenta), o, en su defecto, en el orden que el administrador definió en la página Almacenamientos ([docs/STORAGE.md](docs/STORAGE.md#ordering-storages)) - una sección **Aplicaciones** cuando una aplicación instalada tiene pantalla de inicio, y **Cómo conectarse** + **Claves de API**: las guías por protocolo y el gestor de tokens de autoservicio, que se abren desde dentro del explorador para que los usuarios de una copia integrada puedan crear la credencial que piden WebDAV/FTPS/`filex mount` en lugar de pedírsela a un administrador. Se puede contraer a una barra de iconos (se recuerda por navegador) desde la barra superior, y es un panel deslizante en lugar de una columna por debajo de 560px. Activado de forma predeterminada en la aplicación web, la aplicación de escritorio y cada integración; `uiProfile: 'simple'` desactiva además la barra de pestañas, la vista dividida, el modo de vista de galería y la superficie "Cómo conectarse" sin quitar ninguno de ellos de la compilación ([docs/INTEGRATION.md](docs/INTEGRATION.md)).
- **Uso compartido** - enlaces públicos con PIN, caducidad y límite de descargas, bajo una **vigencia máxima del enlace** que define el administrador (7 días de forma predeterminada - el cuadro de diálogo solo ofrece lo que el servidor va a conservar); los enlaces de carpeta se transmiten como ZIP (almacenado en caché, precalentado hasta un límite de tamaño, purgado al cabo de una semana); enlaces de **solicitud de archivos** para recibir subidas; endpoint de subida compatible con ShareX. **Mis enlaces** muestra los enlaces que usted creó - para todos, no solo para los administradores - con *Copiar enlace*, *Copiar PIN* y *Revocar*: el PIN de un enlace se conserva sellado junto al hash que lo protege, así que quien lo creó o un administrador puede volver a leerlo cuando alguien lo necesite de nuevo, y cada lectura se escribe en el registro de auditoría. Cinco PIN incorrectos cierran cualquier enlace público durante diez minutos. Un enlace de descarga, una solicitud de archivos y la página de una aplicación son **una sola pantalla pública con su marca** - el nombre, el logotipo y los colores de su instancia, una sola página de PIN, una sola forma de tratar la caducidad y un selector de idioma ([docs/SHARING.md](docs/SHARING.md)).
- **Aplicación de escritorio + sincronización de carpetas** - aplicación para Windows/Linux/macOS: sincronización bidireccional que reside en la bandeja del sistema, **sincronización selectiva** (clic con el botón derecho → *Mantener en este equipo*, una carpeta raíz por cuenta, el resto solo en línea), varias cuentas a la vez, **abre documentos de Office desde su propio disco** en el editor del servidor, se actualiza sola (macOS: compilación sin firmar, se actualiza volviendo a descargarla hasta que esté firmada). Cada documento se abre en **su propia ventana** (con el nombre del archivo como título), las ventanas van **sin marco** con los controles propios de la aplicación (botones de semáforo nativos en macOS), y **Configuración → Abrir archivos con** elige entre un clic y doble clic para abrir ([docs/DESKTOP.md](docs/DESKTOP.md), [docs/SYNC.md](docs/SYNC.md)).
- **Papelera e historial de versiones** - las eliminaciones son reversibles dentro de un periodo de retención, las escrituras conservan instantáneas; ambas cosas viven en el almacenamiento que ya montó ([docs/TRASH-VERSIONING.md](docs/TRASH-VERSIONING.md)).
- **Protección al escribir** - análisis opcional de cada archivo que se escribe - incluidos los del editor integrado, y los archivos que la sincronización del almacenamiento encuentra en el backend y no a través de filex - con ClamAV, al que se accede mediante un binario local o un contenedor clamd por la red; más la retención de la papelera y de las versiones tras una sola superficie de administración. El interruptor, el modo y la dirección del analizador, el límite de tamaño y la ventana de análisis al guardar en el editor están en **Configuración → Protección**; las variables `FILEX_CLAMAV*` les dan su valor inicial en el primer arranque y después se apartan (la ruta del binario del analizador sigue definiéndose solo en el entorno, deliberadamente - es un comando que este servidor ejecuta) ([docs/PROTECTION.md](docs/PROTECTION.md)).
- **Carpetas cifradas E2E** - WebCrypto en el cliente; el servidor almacena texto cifrado y nunca recibe una clave. Una carpeta tiene un **nivel**: solo el contenido (el predeterminado - WebDAV, la CLI y la sincronización de escritorio siguen funcionando con los nombres de la carpeta) o **contenido y nombres** (AES-SIV, así que el servidor no conserva ningún nombre legible), y el nivel se puede elevar más adelante, de forma reanudable, desde la **Configuración de cifrado** de la carpeta, donde también se cambia su contraseña. Una carpeta que ya tiene **se cifra donde está**, incluidos los archivos de más de 200 MB; **cualquier archivo se puede cifrar por separado** (un `.fxe` autocontenido con su propia contraseña y clave de recuperación); los archivos de cualquier tamaño se cifran como un flujo; una carpeta desbloqueada se descarga como un **zip descifrado** creado en el navegador; `filex decrypt` abre una carpeta descargada o un `.fxe` en su propio equipo, y **`filex encrypt`** crea una carpeta cifrada a partir de una que está en disco, o cifra una carpeta del servidor donde está - para carpetas demasiado grandes para una pestaña, reanudable, con las claves creadas en su equipo ([docs/CLI.md](docs/CLI.md#filex-encrypt---make-a-folder-an-encrypted-folder)). Cada carpeta recibe una **clave de recuperación**, que se muestra una sola vez, así una contraseña olvidada no significa automáticamente datos perdidos; un operador puede activar opcionalmente la **custodia de claves** - al instalar, o adoptándola más adelante en una instalación en ejecución; nunca alcanza por sí sola a las carpetas existentes, pero a sus propietarios se les ofrece la opción al desbloquearlas - y su uso se notifica al propietario de la carpeta ([docs/E2E-ENCRYPTION.md](docs/E2E-ENCRYPTION.md)).
- **Multiinquilino nativo** - modo proveedor/inquilino con aislamiento por inquilino en una sola instancia. Cada inquilino tiene un **realm** - su nombre de inicio de sesión, que se asigna al crearlo y nunca cambia - así que un inicio de sesión indica su inquilino por la dirección propia del inquilino (la página web, el `Host` de WebDAV, el nombre del certificado FTPS) o por el realm: un campo **Realm** en el formulario de inicio de sesión, `realm/name` por SFTP. La búsqueda de la cuenta nunca sale del inquilino, y un realm escrito en la página de la plataforma para un inquilino con dirección propia se **transfiere** allí con un ticket de un solo uso, válido 60 segundos ([docs/MULTI-TENANCY.md](docs/MULTI-TENANCY.md), [realms](docs/MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)). Los inquilinos se gestionan a sí mismos en **Admin → Inquilinos** y **Mi inquilino**: proveedores de inicio de sesión vinculados a un inquilino o a varios, OIDC y LDAP propios de un inquilino, un subdominio de la plataforma para cada inquilino y dominios propios verificados mediante un CNAME y certificados por el proxy, por el propio filex (ACME) o con el certificado propio del inquilino ([docs/TENANT-ADMIN.md](docs/TENANT-ADMIN.md)).
- **Todo con controladores intercambiables** - controladores de almacenamiento, autenticación, base de datos y cola que hay que activar mediante variables de entorno (`FILEX_AUTH_DRIVERS=local,oidc`, `FILEX_QUEUE_DRIVER=postgres`, …); el inicio de sesión del sistema operativo (`windows`, `pam`) es la excepción: se activa desde el panel de administración una vez superada su prueba.
- **OIDC con SSO primero** - redirección automática opcional a su IdP con inicio de sesión local de emergencia (`?local=1`), y el rol de administrador sigue a un grupo del IdP en cada inicio de sesión.
- **LDAP / Active Directory** - las cuentas del directorio inician sesión en el mismo formulario de contraseña que las locales, y con la misma contraseña en WebDAV, SFTP y FTPS (S3 y NFS aceptan las claves y exportaciones que esas cuentas crean); compatibilidad con CA privadas, y `local` sigue en primer lugar para que `admin@local` funcione mientras el directorio esté caído. El correo electrónico de una cuenta siempre es una dirección: el atributo de correo de la entrada, en su defecto un nombre escrito como `name@domain`, en su defecto `name@local` (`name@<realm>.local` en el realm de un inquilino; la única regla, que también usan los proveedores del sistema operativo); una cuenta que un filex anterior creó con el nombre a secas se **adopta** en su siguiente inicio de sesión, con archivos, enlaces compartidos y rol sin cambios ([docs/LDAP.md](docs/LDAP.md)).
- **Réplica + conciliación** - propagación primario→réplica (espejo / solo anexar / omitir por regla de glob de ruta), lectura de reserva, informe de estado programado, "Corregir todo" con un clic.
- **Cola de operaciones persistente** - cola a prueba de reinicios en su propia base de datos (SQLite / Postgres / MySQL) o en Redis, grupo de workers con reintentos + cancelación + panel principal de administración. Cada controlador ordena por prioridad, así que el análisis antivirus de un archivo que alguien acaba de subir se atiende antes que los veinte mil que puso en cola una primera importación. Si no se define, el controlador sigue a la base de datos en lugar de usar SQLite de forma predeterminada - dirigir sentencias de SQLite a un servidor Postgres es un error de sintaxis en cada sondeo y nunca se ejecuta ninguna tarea.
- **Árbol de archivos respaldado por la base de datos** - los listados salen de la caché de la base de datos (1-5 ms), no del backend de almacenamiento (~100 ms); una sincronización periódica detecta los cambios hechos fuera de filex, por etag donde el backend indica uno y por tamaño + fecha de modificación donde no. Las **Rutas excluidas del escaneo** de un almacenamiento (`.*`, `downloads/incomplete/**`, `*.tmp`) mantienen fuera del escaneo, del catálogo, del índice de búsqueda y del analizador antivirus las partes de un árbol existente que a filex no le sirven - un control de costos, no un control de acceso ([docs/STORAGE.md](docs/STORAGE.md#scan-exclusions)).
- **Catálogo diferido para árboles locales grandes** - `sync_mode: lazy` omite el escaneo inicial: la carpeta que usted abre se lista de inmediato, directamente desde el disco, y se cataloga primero, y el resto lo cataloga una pasada lenta en segundo plano que cede el paso a las personas (o solo a medida que se abren las carpetas). Las carpetas abiertas se vigilan dentro de un presupuesto, una carpeta que nadie visitó no se trata nunca como eliminada, y la búsqueda, el tamaño de las carpetas y el espacio usado dicen claramente cuándo todavía no lo abarcan todo ([docs/STORAGE.md](docs/STORAGE.md#lazy-catalogue), [diseño](docs/LAZY-CATALOGUE.md)). Idea de Alex ([#45](https://github.com/BRF-Tech/filex/issues/45)).
- **Visores y editores** - imagen/video/audio, PDF, Markdown (editor dividido + vista previa), CSV, código (Monaco), Office mediante OnlyOffice, diagramas de Drawio + Mermaid, modelos 3D. El **Probar ahora** de OnlyOffice hace la descarga por la misma vía que usa un documento y avisa cuando el servidor de documentos no exige JWT, y después de *Download failed* el editor dice cuál de los dos fallos que hay detrás de ese mensaje se produjo ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md#failure-editor-shows-download-failed)). Cuando hay más de una aplicación o visor para un tipo de archivo, **Abrir con** y **Elegir una aplicación…** permiten elegir uno, y *Usar siempre esta aplicación* se guarda en su cuenta.
- **Notificaciones** - webhooks JSON genéricos (independientes de Slack/Discord): cualquier número de destinos, cada uno con su propio secreto de firma y su propia suscripción por evento, más una campana en la aplicación con notificaciones leídas y sin leer, y una matriz de silenciado por usuario. El número de notificaciones sin leer es una **insignia en la campana** - exacto hasta 99, `99+` por encima, y en el icono del dock de la aplicación de escritorio donde el sistema tiene uno - una fila admite clic exactamente cuando tiene adónde llevar (una solicitud de firma abre la pantalla de firma, no una página de notificaciones), y **Ver todas** abre todas sus notificaciones encima del explorador, para todo el mundo y no solo para los administradores. Una escritura que **crea** un archivo y una que **reemplaza** otro son eventos distintos (`file.uploaded` / `file.updated`), y los que un operador más quiere tener por separado - una subida infectada puesta en cuarentena, una subida fallida, una carpeta cifrada abierta con su clave de recuperación - admiten suscripción individual ([docs/NOTIFICATIONS.md](docs/NOTIFICATIONS.md)).
- **Búsqueda** - Bleve integrado, texto completo + metadatos, respeta los permisos. Puntuación de nombres de archivo al estilo de VS Code: las carpetas cuentan y el orden de las palabras no (`main code` encuentra `Code/main.go`), se perdonan los separadores y los errores tipográficos (`invoice 2026` encuentra `invoice_2026.pdf`, `mian.go` encuentra `main.go`) mientras que los números se comparan literalmente (`2026` nunca significa `2025`), filtros `tag:`, coincidencias exactas primero. Un resultado de ⌘K se puede descargar (una carpeta como un solo zip) o arrastrar hacia fuera desde donde está ([docs/SEARCH.md](docs/SEARCH.md)).
- **Miniaturas que se pueden leer**: un PDF muestra su **primera página**, anclada por arriba para que el título quede en la tarjeta; un video, su primer fotograma que no sea negro (antes, uno que empezaba con un fundido producía un cuadrado negro, y un clip de menos de un segundo no producía nada en absoluto mientras la fila seguía diciendo "listo"); un documento de Office, su primera página renderizada; y un archivo de texto, de código o CSV **llena la tarjeta con sus propias primeras líneas** en lugar de repetir la extensión que la fila ya muestra. imagen, video (ffmpeg), PDF (ghostscript), Office (el OnlyOffice conectado); con detección de capacidades, y un servidor al que le falta uno de esos binarios ahora lo dice en su registro al arrancar en lugar de generar en silencio rectángulos de colores. Una miniatura en caché se libera cuando el archivo al que pertenece se elimina definitivamente, y un conciliador periódico recupera el espacio de las huérfanas que acumuló una instalación más antigua. Una miniatura **sigue a su archivo**: la de uno modificado fuera de filex, o la de uno que nunca tuvo imagen, se genera de nuevo cuando un listado o la sincronización lo ve; la de un **SVG** la genera un motor integrado en todas las instalaciones (con límites de tamaño y de tiempo que establece un administrador), y las de las fotos **HEIC/AVIF** se generan con ImageMagick; las imágenes transparentes se muestran sobre un fondo de cuadros; una **carpeta muestra los últimos archivos que entraron en ella**, representados con la carpeta en la cuadrícula, la galería y la lista, y lo que contiene al dejar el puntero encima (un administrador puede desactivarlo); los archivos de texto muestran sus primeras líneas y los archivos comprimidos, lo que contienen; un archivo cuya herramienta falta se nombra, no se disimula; y **Admin → Herramientas → Reparación de miniaturas** vuelve a generar bajo demanda las miniaturas de un archivo, una carpeta o un almacenamiento ([docs/thumbnails.md](docs/thumbnails.md)).
- **Pestañas, temas y enlaces profundos** - varias carpetas abiertas lado a lado, tema claro/oscuro/automático y una barra de direcciones que sigue a la carpeta abierta para que un enlace pegado lleve hasta ella. La galería de temas trae ocho paletas, cada una un mapa de las variables `--fe-*` en lugar de una segunda hoja de estilos, así que una página anfitriona o una integración puede elegir una - o establecer sus propios valores - sin bifurcar ningún CSS; un operador puede agregar las suyas (véase *Apariencia*).
- **Apariencia: sus colores, en todas partes** - la pantalla **Apariencia** del panel de administración crea temas con nombre - doce colores para el modo claro y para el oscuro, un radio de las esquinas, una lista de fuentes - con vista previa mientras usted escribe, y convierte uno en el **predeterminado de la instancia**. El texto de un botón de color se elige por contraste en lugar de suponer que es blanco, el resto de la paleta se deriva en el servidor, y el tema llega a la página de inicio de sesión y a todos los enlaces públicos - en los tonos del propio tema, o en colores propios que usted da a esas dos páginas - porque una marca que se queda en el inicio de sesión no es marca: una página sin sesión iniciada lleva el predeterminado de la instancia, nunca la paleta de quien usó ese navegador por última vez, y prevalece la elección propia de una persona con la sesión iniciada. Los temas se exportan y se importan como un único archivo JSON. Una **hoja de estilos personalizada** es la herramienta peligrosa que está a su lado, y ahora está desactivada hasta que usted la active, nunca se sirve a quien no tenga la sesión iniciada, no puede cargar nada y no puede llegar a la pantalla que la desactiva ([docs/INTEGRATION.md](docs/INTEGRATION.md#themes)).
- **Una sola tabla, en todas partes** - en filex queda una sola tabla, la del explorador, y cualquier otra lista es esa misma tabla: los menús del panel de administración, **Mis enlaces**, las pantallas propias de una aplicación. Cada una inmoviliza su primera columna a la izquierda y sus acciones a la derecha, cambia de tamaño, se reordena y se ordena de la misma manera, y termina cada fila en **un único menú Acciones fijado** que contiene todo lo que esa fila puede hacer - el mismo menú que abre el ⋮ del explorador, así que una segunda tabla no puede desviarse de la primera. Una aplicación instalada con pantalla de inicio obtiene su propia fila bajo **Aplicaciones** en la navegación del panel.
- **Enlaces simbólicos, en el límite del almacenamiento** - un enlace dentro de un almacenamiento `local` que apunta dentro de él se sigue y se abre como aquello a lo que apunta; uno que sale del almacenamiento **se muestra con una insignia y el motivo**, y se rechaza su lectura, escritura y eliminación - salvo que active *Seguir los enlaces simbólicos que salen de esta carpeta* para ese almacenamiento ([docs/STORAGE.md](docs/STORAGE.md#symlinks)).
- **Abrir como espera cada dispositivo** - con el **puntero**, un clic selecciona y un **doble clic abre** (Enter abre la selección) - el gesto clásico de un gestor de archivos, y una preferencia por persona (`ExplorerConfig.openTrigger`, valor predeterminado `'double'`; la aplicación de escritorio la ofrece como **Configuración → Abrir archivos con**, y `'single'` restablece la apertura con un solo clic). En una **pantalla táctil**, un toque siempre abre - no existe la selección al pasar el puntero. En todos los dispositivos, la **casilla** es el único clic o toque que selecciona (Shift amplía el rango) y un clic con el botón derecho o un toque prolongado abre el menú; las filas de la lista, las tarjetas de la cuadrícula y los mosaicos de la galería la llevan todos.
- **Teclado, y lo dice** - cada verbo del menú contextual y de la barra de herramientas muestra la tecla que lo ejecuta, leída del registro, de modo que sigue a cualquier reasignación. Treinta y dos acciones se pueden reasignar desde *Configuración de atajos* (se guardan por navegador); las pocas combinaciones que un navegador se queda para sí, como `Ctrl+W`, se rechazan con un motivo en lugar de guardarse como una tecla que nunca se activaría.
- **Uso y costo** - filex no mide la factura de su proveedor; lee el informe que el proveedor ya escribe, lo normaliza y lo valora con una tabla que usted puede editar. Los CSV diarios de Backblaze B2 se leen a través de la misma API S3 que filex ya habla, así que no hay ninguna dependencia nueva ni ningún tipo de credencial nuevo. Las cuotas gratuitas son campos propios en lugar de constantes en una fórmula, y la página mantiene la fila de nivel de cuenta del proveedor separada de sus filas por bucket - sumarlas cuenta dos veces las mismas transacciones, justo por el importe que nadie nota ([docs/USAGE.md](docs/USAGE.md)).
- **Registro de auditoría** - cada modificación queda registrada con autor, identidad de integración y metadatos.
- **Cliente CLI** - el mismo binario llega a un servidor remoto (`filex client`, `filex sync`) sin ningún complemento del lado del servidor: copiar y mover entre almacenamientos, la papelera, las versiones, las etiquetas, las acciones de las aplicaciones, los archivos comprimidos y sus propios enlaces, y cada tarea del servidor se sigue hasta el final; `filex client login --realm` inicia sesión en un inquilino, `filex encrypt` crea carpetas cifradas, y una sesión guardada nunca se envía más que a la dirección con la que se guardó ([docs/CLI.md](docs/CLI.md)).
- **Actualización automática** - las versiones de parche se instalan solas, las menores se anuncian para actualizar con un clic; a una instalación que pertenece a un gestor de paquetes (Homebrew, winget, Snap, un paquete de la distribución) o a un contenedor se le informa de las versiones nuevas y del comando para obtenerlas, y la página de administración dice que solo las anunciará ([docs/UPDATES.md](docs/UPDATES.md)).
- **Binario único** - matriz de goreleaser: linux/macOS/Windows × amd64/arm64. CGO=0, modernc.org/sqlite.
- **i18n** - inglés + turco de fábrica, **enlaces públicos incluidos**: un enlace
  compartido, una página de PIN, una página de solicitud de archivos o la pantalla de
  firma de una aplicación se muestra en el idioma del visitante, y la interfaz pública
  **ofrece un selector**, porque el idioma del navegador de un desconocido es una
  suposición y la persona que lee un contrato debería poder corregirlo. Las páginas
  simples sin JS resuelven `?lang=`, luego `Accept-Language`, luego el valor
  predeterminado del servidor. **El texto que escribe el servidor sale del mismo
  catálogo** - el correo, las frases de las notificaciones, las páginas sin JavaScript
  y la revisión de permisos de una instalación - cada uno dirigido a su lector de
  siempre, con el inglés como reserva clave por clave, y una traducción cuyos
  marcadores de posición no coinciden con los del inglés no se usa en tiempo de
  ejecución, así un correo nunca pierde su enlace ni su PIN.
- **Paquetes de idioma** - cualquier otro idioma es una **aplicación sin nada que se
  ejecute**: un manifiesto de cadenas que se instala desde un repositorio de GitHub, una
  subida o una URL como cualquier otra aplicación y aparece en **Complementos →
  Aplicaciones** con su cobertura de la versión en ejecución (*Español - 97% traducido ·
  el resto se muestra en inglés*). Ese idioma se suma a todos los selectores - el cuadro
  de diálogo de configuración, la cabecera del panel de administración, las páginas
  públicas de enlaces compartidos - y traduce por igual el explorador, el panel de
  administración y las páginas públicas. Las formas del plural siguen las **categorías
  CLDR**, así que un paquete escribe `zero`, `one`, `two`, `few`, `many` y `other` donde
  su idioma las tiene. El español, el alemán y el francés se distribuyen como ejemplos, y
  un repositorio de plantilla más `scripts/i18n-export.mjs` / `i18n-validate.mjs` guían a
  un traductor desde la exportación hasta la instalación - el validador exige a un
  paquete las reglas que cumplen los idiomas integrados, entre ellas, un guion simple
  allí donde un texto recurriría a una raya
  ([escribir uno](docs/PLUGIN-KIT.md#writing-a-language-pack)).
- **Diseño de derecha a izquierda** - el árabe, el hebreo, el persa, el urdu y cualquier
  otro idioma de derecha a izquierda disponen toda la interfaz de derecha a izquierda: el
  panel de navegación, las tablas, la geometría de arrastrar y soltar y la de cambiar el
  tamaño de las columnas, los menús y los iconos direccionales. Lo que **no** debe
  reflejarse no se refleja - un editor de campos de PDF trabaja en el espacio del
  documento, y una ruta, un comando o cualquier otro texto de máquina se aísla para que se
  lea de izquierda a derecha dentro de una frase de derecha a izquierda, incluidas las
  frases que escribió el servidor. La regla la impone una prueba de salvaguarda: el diseño
  se escribe solo con propiedades lógicas de CSS
  ([docs/RTL.md](docs/RTL.md)).
- **Etiquetas, personales o de su equipo** - una etiqueta es o bien **personal** - solo
  suya, nunca se nombra ante nadie más - o bien una etiqueta **de equipo** compartida
  dentro del inquilino, que exige permiso de edición sobre el archivo para agregarla o
  quitarla. Una etiqueta abre todos los archivos que la llevan, de todas las carpetas en
  las que están, y `tag:` acota una búsqueda. Las mayúsculas se conservan tal como se
  escriben.
- **Proveedores de identidad, administrados desde el panel** -
  **Admin → Proveedores de acceso** ahora gobierna el inicio de sesión, en lugar de guardar
  ajustes que nada leía: OIDC, LDAP, el proxy de encabezados, el formulario de contraseña
  local y las cuentas del propio sistema operativo - **Windows** (locales o de dominio,
  `LogonUserW`, nada que instalar) y **Linux PAM** - cada uno con un **Probar ahora** que
  lo pone a prueba de verdad y dice qué tramo verificó. Un proveedor del sistema operativo
  solo se activa mediante una prueba que haya iniciado sesión con una cuenta real, y esa
  cuenta se convierte en superadministrador; no se puede activar desde el entorno. Cada
  proveedor sigue **una sola regla del primer inicio de sesión** - si puede crear una
  cuenta (`auto_create`, desactivado de forma predeterminada para Windows y PAM) y para
  qué grupos (`allowed_groups`). Lo que se define en el entorno o en `config.yaml`
  **prevalece, y se ve**; un secreto de cliente o una contraseña de bind son de solo
  escritura y quedan sellados en reposo con `FILEX_SECRET_KEY`, nunca se devuelven; y la
  última vía de acceso no se puede desactivar desde la página ([docs/SSO.md](docs/SSO.md),
  [docs/LDAP.md](docs/LDAP.md), [docs/OS-LOGIN.md](docs/OS-LOGIN.md)).
- **Seguridad del inicio de sesión** - las contraseñas incorrectas se contabilizan por
  identificador de cuenta (exista o no la cuenta, así el recuento no revela nada) y por
  dirección de cliente, por igual en el formulario web, WebDAV, FTPS y SFTP: 5 por cuenta
  y 10 por dirección en 10 minutos cierran el paso durante un minuto, que se duplica
  hasta 15, y un bloqueo rechaza incluso la contraseña correcta. El formulario de inicio
  de sesión dice el número de intentos restantes, en el idioma de quien lo lee. Una
  **lista de IP permitidas** es la forma de volver a entrar - ninguna cuenta es
  privilegiada, incluida la del primer administrador (solo la cuenta compartida de una
  demo pública se contabiliza únicamente por dirección, [docs/DEMO.md](docs/DEMO.md)) - y
  **Admin → Seguridad de acceso** reúne los límites, la lista, los bloqueos con
  *Levantar el bloqueo* y el registro de inicios de sesión: cada intento fallido, bloqueo,
  levantamiento y cambio de configuración, una vez cada uno, sea cual sea la vía de acceso
  que usó un administrador (el panel, una clave de API, MCP). La dirección que se contabiliza
  es la del equipo al otro lado del socket, salvo que ese equipo sea un proxy en el que
  usted confíe (`FILEX_TRUSTED_PROXIES`, de forma predeterminada `auto`: este equipo y, en
  un contenedor, los demás contenedores de la misma red - nunca la puerta de enlace, nunca
  la LAN; la página nombra un equipo que envía direcciones reenviadas sin ser de confianza
  y ofrece agregarlo), así un cliente no puede elegir su propia dirección escribiendo
  `X-Forwarded-For`
  ([docs/CONFIGURATION.md](docs/CONFIGURATION.md#sign-in-attempt-limits)).
- **Los cambios desde otro sitio se rechazan** - una solicitud que cambie algo y solo lleve
  la sesión del visitante (la cookie, o el encabezado de inicio de sesión de un proxy de
  confianza) debe venir de las propias páginas de filex, de su propia dirección o de un
  origen que figure en `FILEX_CORS_ALLOWED_ORIGINS`; a todo lo demás se le responde
  `403 cross_origin_refused` antes de que se ejecute ninguna ruta. Las claves, los enlaces
  compartidos y de subida, los tickets de subida, S3 y los scripts no se ven afectados
  ([docs/CONFIGURATION.md](docs/CONFIGURATION.md#requests-from-other-origins)).

## Arquitectura

Véase [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Documentación

**Primeros pasos** - [Instalación](docs/INSTALLATION.md) ·
[Configuración](docs/CONFIGURATION.md) · [Bases de datos](docs/DATABASES.md) ·
[Versiones](docs/RELEASES.md) · [Actualizaciones](docs/UPDATES.md) ·
[Modo demo](docs/DEMO.md)

**Clientes** - [Aplicación de escritorio](docs/DESKTOP.md) ·
[Sincronización de carpetas](docs/SYNC.md) · [CLI](docs/CLI.md) ·
[Integración / inserción](docs/INTEGRATION.md) · [IA y MCP](docs/MCP.md)

**Sin navegador** - [Protocolos (S3 · SFTP · FTPS · NFS · WebDAV ·
`filex mount`)](docs/PROTOCOLS.md) · [WebDAV](docs/WEBDAV.md)

**Aplicaciones** -
[Aplicaciones: instalar, controlar, firmar, convertir](docs/APP-PLUGINS.md) ·
[Solicitudes de instalación](docs/APP-PLUGINS.md#install-requests) ·
[Permisos de las aplicaciones](docs/APP-PLUGINS.md#app-permissions) · [Aplicaciones
predeterminadas](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail) ·
[Escribir una aplicación](docs/PLUGIN-KIT.md) ·
[Contrato de comunicación de las aplicaciones](docs/APP-PLUGINS-API.md)

**Idioma y diseño** -
[Escribir un paquete de idioma](docs/PLUGIN-KIT.md#writing-a-language-pack) ·
[Idiomas de derecha a izquierda](docs/RTL.md)

**Almacenamiento y acceso** - [Almacenamiento](docs/STORAGE.md) ·
[Complementos de almacenamiento](docs/PLUGINS.md) ·
[Uso y costo](docs/USAGE.md) · [Subidas y reanudación](docs/UPLOADS.md) ·
[Cuotas](docs/QUOTAS.md) · [SSO (OIDC)](docs/SSO.md) ·
[LDAP y autenticación por proxy](docs/LDAP.md) ·
[Cuentas de Windows y Linux](docs/OS-LOGIN.md) ·
[Límites de intentos de inicio de sesión y proxies de
confianza](docs/CONFIGURATION.md#sign-in-attempt-limits) ·
[RBAC, acceso a carpetas y claves de API](docs/RBAC.md) ·
[Roles y permisos por usuario](docs/PERMISSIONS.md) · [Grupos](docs/GROUPS.md) ·
[Multiinquilino y realms](docs/MULTI-TENANCY.md) ·
[Autoservicio del inquilino](docs/TENANT-ADMIN.md)

**Datos y funciones** - [Uso compartido y solicitudes de archivos](docs/SHARING.md) ·
[ShareX](docs/SHAREX.md) ·
[Papelera y control de versiones](docs/TRASH-VERSIONING.md) ·
[Protección](docs/PROTECTION.md) · [Archivos comprimidos](docs/ARCHIVES.md) ·
[Cifrado E2E](docs/E2E-ENCRYPTION.md) · [Búsqueda](docs/SEARCH.md) ·
[Tiempo real y presencia](docs/REALTIME.md) ·
[Notificaciones](docs/NOTIFICATIONS.md) · [Miniaturas](docs/thumbnails.md) ·
[Replicación](docs/REPLICATION.md) · [Temas y apariencia](docs/INTEGRATION.md#themes)

**Operar y ampliar** - [Despliegue](docs/DEPLOYMENT.md) · [Docker](docs/DOCKER.md) ·
[Métricas](docs/METRICS.md) · [Arquitectura](docs/ARCHITECTURE.md) ·
[Especificación de la API del backend](docs/BACKEND.md) ·
[OpenAPI 3.1 (`/api/files`, `/api/ai`)](backend/internal/api/openapi.json) ·
[API del componente](docs/API.md) · [OnlyOffice](docs/ONLYOFFICE.md) ·
[Solicitudes desde otros orígenes](docs/CONFIGURATION.md#requests-from-other-origins)

[Índice completo de la documentación](docs/README.md)

## Desarrollo

```bash
git clone https://github.com/BRF-Tech/filex.git
cd filex
pnpm install
pnpm run build:all    # builds packages, web, then Go binary
./bin/filex serve
```

Subdirectorios:
- `backend/` - servicio HTTP en Go (cmd/filex, internal/*, db/queries, db/migrations)
- `packages/core` - `@brftech/filex-core` (SFC de Vue 3, fuente de verdad)
- `packages/webcomponent` - `@brftech/filex` (envoltorio de componente web)
- `packages/react` - `@brftech/filex-react` (adaptador de React mediante @lit/react)
- `web/` - interfaz de administración en Vue 3 (integrada en el binario de Go mediante `go:embed`)
- `desktop/` - aplicación de Electron (proceso principal en un bundle, sincronización desde
  la bandeja del sistema, actualización automática)
- `demo/` - demos HTML independientes para cada framework
- `e2e/` - suites de Playwright (web, integraciones, aplicación de escritorio empaquetada) +
  `shots/`, los scripts que ejecuta `pnpm shots` para volver a tomar todas las capturas de
  pantalla de arriba
- `docker/` - Dockerfiles + compose
- `deploy/` - stacks de Compose listos para usar + chart de Helm (véase [`deploy/`](deploy/))
- `docs/` - documentación en Markdown
- `docs-site/` - sitio de VitePress publicado en [docs.filex.sh](https://docs.filex.sh)

Las contribuciones son bienvenidas - véase [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md).

## Licencia

MIT - véase [LICENSE](LICENSE).

Las insignias de las tiendas que están en [`docs/badges/`](docs/badges/) son material
gráfico de las propias tiendas, usado sin modificar y no cubierto por esa licencia:
Microsoft y la insignia de la Microsoft Store son marcas comerciales del grupo de compañías
de Microsoft; la insignia de la Snap Store es © Canonical Ltd., bajo licencia
[CC BY-ND 2.0 UK](https://creativecommons.org/licenses/by-nd/2.0/uk/).
