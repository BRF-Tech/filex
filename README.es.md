<!-- Translated from README.md as of v0.56.0 (the shortened README of #218). The English README is the source: change it first, then carry the change here. -->

<div align="center">

<img src="docs/logo.png" alt="logotipo de filex" width="96">

# filex

**Gestor de archivos autoalojado y uso compartido con interfaz web, en un solo binario de Go - alternativa de código abierto a Nextcloud, Dropbox y Google Drive.**<br>
Explorador de archivos web para almacenamiento local, S3, SFTP, WebDAV, FTP y SMB: nube privada servida como S3, SFTP, NFS y WebDAV.<br>
SSO/LDAP, multiinquilino, carpetas cifradas, componente web integrable, aplicación de escritorio, servidor MCP.

[![Pruebe la demo en vivo](https://img.shields.io/badge/Try_the_live_demo-f59e0b?style=for-the-badge)](https://demo.filex.sh)
[![Inicio rápido](https://img.shields.io/badge/Quick_start-2f6ceb?style=for-the-badge)](#inicio-rápido)
[![Documentación](https://img.shields.io/badge/Documentation-374151?style=for-the-badge)](https://docs.filex.sh)

[![Release](https://img.shields.io/github/v/release/BRF-Tech/filex?color=2f6ceb)](https://github.com/BRF-Tech/filex/releases)
[![License: MIT](https://img.shields.io/github/license/BRF-Tech/filex?color=22c55e)](LICENSE)

[English](README.md) · [Türkçe](README.tr.md) · [Deutsch](README.de.md) · **Español** · [Français](README.fr.md) · [简体中文](README.zh-CN.md)

<sub>Esta es una traducción del [README en inglés](README.md); donde los dos difieran, prevalece el texto en inglés. Se tradujo de forma automática y está pendiente de la revisión de un hablante nativo - se agradecen las correcciones. Los documentos a los que enlaza están en inglés.</sub>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://filex.sh/shots/explorer-grid-dark.ff7a02b49f27.png">
  <img src="https://filex.sh/shots/explorer-grid-light.475b0b61faab.png" alt="explorador de filex - cuadrícula de miniaturas" width="900">
</picture>

</div>

<table>
<tr>
<td width="50%" valign="top">

**🗂️ Todos los almacenamientos, un solo árbol**

Monte lado a lado lo que ya tiene, y copie o corte en un almacenamiento y pegue en otro. Apunte `rclone`, `restic`, WinSCP o FileZilla al mismo árbol.

[Almacenamiento y protocolos →](#almacenamiento-y-protocolos)

</td>
<td width="50%" valign="top">

**🔗 Compartir y proteger**

Enlaces con PIN, caducidad y límite de descargas. Papelera, historial de versiones y carpetas cifradas de extremo a extremo.

[Uso compartido y protección →](#uso-compartido-y-protección)

</td>
</tr>
<tr>
<td width="50%" valign="top">

**🔑 Sus cuentas, sus roles**

OIDC, LDAP / Active Directory o las cuentas de Windows y Linux del servidor. Roles, grupos, multiinquilino.

[Personas y acceso →](#personas-y-acceso)

</td>
<td width="50%" valign="top">

**💻 Una aplicación de escritorio con sincronización de carpetas en vivo**

Windows, Linux y macOS (Apple Silicon): un cambio en cualquiera de los dos lados llega en aproximadamente un segundo.

[Aplicación de escritorio y CLI →](#aplicación-de-escritorio-y-cli)

</td>
</tr>
<tr>
<td width="50%" valign="top">

**🧩 Se integra en su producto**

La misma interfaz como componente de Vue 3, componente de React y componente web `<filex-explorer>`.

[Integrarlo en su aplicación →](#integrarlo-en-su-aplicación)

</td>
<td width="50%" valign="top">

**🤖 Aplicaciones y agentes de IA**

Aplicaciones en un entorno aislado - Firma electrónica, Convertir, draw.io - y un servidor MCP integrado que un agente puede manejar.

[Aplicaciones →](#aplicaciones) · [Agentes de IA / MCP →](#agentes-de-ia--mcp)

</td>
</tr>
</table>

<p align="center"><a href="#por-qué-filex">Por qué filex</a> · <a href="#si-viene-de-nextcloud-dropbox-o-google-drive">Si viene de otra herramienta</a> · <a href="#autoalojamiento-con-compose-o-helm">Autoalojamiento</a> · <a href="#funciones">Funciones</a></p>

<br>

# Inicio rápido

> [!TIP]
> **Mírelo antes de instalarlo:** [demo.filex.sh](https://demo.filex.sh) - inicie sesión con
> `demo@demo.com` / `demo` (rol de administrador, el entorno de pruebas se restablece cada noche).

Ejecute su propia instancia, desde la carpeta que quiere servir:

```bash
docker run -p 5212:5212 \
  -e FILEX_DEFAULT_STORAGE_DRIVER=local -e FILEX_DEFAULT_STORAGE_PATH=/srv/files \
  -v filex-data:/data -v "$PWD:/srv/files" \
  ghcr.io/brf-tech/filex:latest
```

Abra http://localhost:5212/admin - la primera ejecución imprime en la consola las
credenciales de administrador y las instrucciones de integración. Esa URL es la del operador;
las personas a las que usted da una cuenta reciben **http://localhost:5212/drive**, el mismo
gestor de archivos sin el panel que lo rodea.

<details>
<summary><b>Qué monta ese comando y con qué usuario se ejecuta</b></summary>

Con eso se sirve **la carpeta desde la que lo ejecutó** - abra la interfaz y sus archivos ya
están ahí. `/data` es el directorio propio de filex (base de datos SQLite, índice de
búsqueda, caché de miniaturas), por lo que es un volumen con nombre y no la carpeta en la
que usted deja archivos; los dos están separados a propósito. Apunte `$PWD` a otro lugar, o
agregue después más almacenamientos desde el panel de administración - un bucket con varias
carpetas de nivel superior se puede montar de una sola vez como un almacenamiento por
carpeta (*Almacenamientos → Agregar almacenamiento → Montar varias carpetas a la vez*).

El contenedor se ejecuta como **root** de forma predeterminada, así que lo que escribe en
`/data` pertenece a root; defina `PUID`/`PGID` para ejecutarlo con su propio usuario
([docs/DOCKER.md](docs/DOCKER.md#which-user-the-container-runs-as)).

</details>

<details>
<summary><b>¿Sin Docker? Ejecute el binario</b></summary>

Descargue el archivo comprimido para su plataforma de la
[última versión](https://github.com/BRF-Tech/filex/releases/latest), extráigalo y ejecute
el binario ([docs/INSTALLATION.md](docs/INSTALLATION.md#binary)):

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

Para un despliegue de verdad hay stacks de Compose y un chart de Helm:
[Autoalojamiento con Compose o Helm](#autoalojamiento-con-compose-o-helm). La imagen está en
[GitHub Packages](https://github.com/BRF-Tech/filex/pkgs/container/filex).

<br>

# Por qué filex

La mayoría de los gestores de archivos autoalojados son o bien **demasiado pequeños** (un
listado de directorio con subidas) o bien **demasiado grandes** (una suite de trabajo en
grupo que se despliega solo por la pestaña de archivos). filex apunta al espacio que queda
entre ambos. El recorrido de abajo tiene ocho partes, cada una con sus capturas de pantalla
plegadas debajo. Cada función al completo, con sus límites y la guía que la documenta, está
en [docs/FEATURES.md](docs/FEATURES.md).

<br>

## El explorador

**Un gestor de archivos que la gente ya sabe usar.**

- Un panel de navegación con **Inicio · Mis archivos · Compartido conmigo · Mis enlaces · Recientes ·
  Destacados · Borradores · Papelera**; vistas de lista, cuadrícula y galería; pestañas, etiquetas y una paleta ⌘K.
- Los avatares de presencia y los cambios en los archivos llegan en vivo, por WebSocket.
- Cree un tema con sus propios colores y establézcalo como predeterminado: la página de inicio de
  sesión y cada enlace público también lo usan, y un teléfono instala la aplicación web con el
  nombre y el logotipo de su instancia.
- El inglés y el turco vienen integrados, los demás idiomas se instalan como paquetes, y la interfaz
  se dispone de derecha a izquierda para los idiomas que se leen así.

Todas las funciones del explorador, en detalle: [docs/FEATURES.md → The explorer](docs/FEATURES.md#the-explorer).

<p align="center"><img src="https://filex.sh/shots/driveshell/driveshell-hero-1440.6410104e072c.png" alt="La interfaz base de filex" width="860"></p>

<details>
<summary><b>Capturas de pantalla del explorador</b> - la interfaz base, la búsqueda, la navegación, las etiquetas, la papelera, la campana, los temas, los enlaces simbólicos, CSV en ONLYOFFICE</summary>

<table>
<tr>
<td width="50%" valign="top">

![La interfaz base de filex](https://filex.sh/shots/driveshell/driveshell-hero-1440.6410104e072c.png)

<sub>La interfaz base - adonde llega todo el mundo</sub>

</td>
<td width="50%" valign="top">

![Búsqueda en una carpeta](https://filex.sh/shots/driveshell/driveshell-search-1440.2536c373425f.png)

<sub>Buscar en esta carpeta; `⌘K` / `Ctrl K` pasa la consulta a la paleta</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Panel de navegación](https://filex.sh/shots/sidenav/sidenav-expanded-1440.e96ea7e98388.png)

<sub>Panel de navegación - Inicio, Compartido conmigo, Mis enlaces, Recientes, Destacados, Papelera y los almacenamientos a los que puede acceder</sub>

</td>
<td width="50%" valign="top">

![Contraído a una barra](https://filex.sh/shots/sidenav/sidenav-rail-1440.9269e026d29a.png)

<sub>Contraído a la barra de iconos</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Etiquetas personales y de equipo](https://filex.sh/shots/tags/tags-kinds-1440.a4504c43e5cb.png)

<sub>Etiquetas - las suyas, o las de su equipo; una etiqueta abre todos los archivos que la llevan, de todas las carpetas en las que están</sub>

</td>
<td width="50%" valign="top">

![La vista de la papelera](https://filex.sh/shots/sidenav/view-trash-1440.40f8904b147f.png)

<sub>Papelera - qué se eliminó, de dónde vino y cuánto falta para que desaparezca</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![La campana con su insignia de notificaciones sin leer, abierta](https://filex.sh/shots/signing/bell-badge-1440.e9232b261511.png)

<sub>La campana - con el número de notificaciones sin leer encima, y cada fila lleva adonde dice</sub>

</td>
<td width="50%" valign="top">

![La lista completa de notificaciones encima del explorador](https://filex.sh/shots/signing/notifications-list-1440.669ce56b62d7.png)

<sub>Todas sus notificaciones, dentro del explorador - para todos, no solo para los administradores</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![El editor de temas](https://filex.sh/shots/appearance/theme-editor-1440.867a94807e72.png)

<sub>Apariencia - cree un tema con sus propios colores, con vista previa mientras escribe</sub>

</td>
<td width="50%" valign="top">

![El explorador con el tema del operador](https://filex.sh/shots/appearance/themed-explorer-1440.abc278616daa.png)

<sub>Establecido como predeterminado, es lo que lleva el explorador de todos…</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![La página de inicio de sesión con el tema del operador](https://filex.sh/shots/appearance/themed-signin-1440.b891c6431444.png)

<sub>…y la página de inicio de sesión, antes de que nadie haya iniciado sesión</sub>

</td>
<td width="50%" valign="top">

![Un enlace simbólico que sale del almacenamiento, con insignia](https://filex.sh/shots/symlinks/symlink-badge-1440.82a105d4239e.png)

<sub>Un enlace simbólico que filex no seguirá lo dice - en el listado, y con palabras en sus detalles</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Un CSV separado por punto y coma abierto en la hoja de cálculo de ONLYOFFICE](https://filex.sh/shots/csvoffice/csv-view-1440.83237ba55d3d.png)

<sub>Un `.csv` se abre en la hoja de cálculo de ONLYOFFICE cuando hay uno conectado - primero un vistazo, y sin cuadro de diálogo de delimitador: se pasa el propio separador del archivo ([archivos CSV](docs/ONLYOFFICE.md#csv-files))</sub>

</td>
<td width="50%" valign="top">

![El CSV en el editor de ONLYOFFICE, con la nota sobre lo que se conserva al guardar](https://filex.sh/shots/csvoffice/csv-edit-1440.4efc379a293d.png)

<sub>…y su editor, que dice lo que se conserva al guardar como CSV; el archivo vuelve como el mismo tipo de CSV, y las celdas que nadie cambió conservan su texto - `007` sigue siendo `007` ([celdas que nadie cambió](docs/ONLYOFFICE.md#cells-nobody-changed-keep-their-text))</sub>

</td>
</tr>
</table>

</details>

<br>

## Almacenamiento y protocolos

**Todos los almacenamientos en un solo árbol, y ese árbol accesible desde cualquier cosa.**

- Monte lado a lado discos locales, S3, FTP, SFTP, WebDAV y recursos compartidos SMB/NAS, y copie o
  corte en uno y pegue en otro.
- Acceda al mismo árbol como **S3**, **SFTP**, **FTPS**, **NFSv3** y **WebDAV**: apunte a él `rclone`,
  `restic`, `aws s3`, WinSCP o FileZilla. Se aplican los mismos permisos de filex (por persona,
  grupo y carpeta), la misma papelera y la misma cuota que en la interfaz web; los bits POSIX y
  las ACL del disco no se asignan a usuarios de filex.
- `filex mount` conecta un servidor remoto por HTTPS corriente: una carpeta en Linux, una letra de
  unidad en Windows (macOS no es compatible).
- Para un almacenamiento que filex no incluye, instale un complemento desde el panel de
  administración o desde el enlace de una tienda de aplicaciones.

Todas las funciones de almacenamiento y protocolos, en detalle: [docs/FEATURES.md → Storage and protocols](docs/FEATURES.md#storage-and-protocols).

<p align="center"><img src="https://filex.sh/shots/sidenav/connect-1440.45536274e7dc.png" alt="Cómo conectarse" width="860"></p>

<details>
<summary><b>Capturas de pantalla de almacenamiento y protocolos</b> - las guías de conexión, las claves de API y un complemento de almacenamiento</summary>

<table>
<tr>
<td width="50%" valign="top">

![Cómo conectarse](https://filex.sh/shots/sidenav/connect-1440.45536274e7dc.png)

<sub>Cómo conectarse - las guías, generadas a partir de *su* despliegue</sub>

</td>
<td width="50%" valign="top">

![Claves de API](https://filex.sh/shots/sidenav/apikeys-minted-1440.3464cb59b2f8.png)

<sub>Claves de API - cree las suyas, en el explorador o en una integración (la sesión o el token de una persona; una integración servida a través de un proxy con un único token compartido de tipo *app* no recibe esta entrada)</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Guía de conexión](https://filex.sh/shots/connections-guide.3999cf32c3d9.png)

<sub>Acceder a filex desde cualquier cosa - S3, SFTP, FTPS, NFS, WebDAV. Cada comando se genera a partir de *su* despliegue</sub>

</td>
<td width="50%" valign="top">

![Complementos](https://filex.sh/shots/admin-plugins.6bd96bf65745.png)

<sub>Un almacenamiento que filex no incluye - instalado como complemento en **Complementos → Complementos de almacenamiento** y que describe su propio formulario de configuración</sub>

</td>
</tr>
</table>

</details>

<br>

## Uso compartido y protección

**Comparta con un enlace que puede limitar y revocar. Deshaga una eliminación o una sobrescritura equivocadas.**

- Enlaces públicos con PIN, caducidad y límite de descargas, y solicitudes de archivos para lo que
  tiene que llegar. **Mis enlaces** lista cada enlace que usted creó, para copiarlo o revocarlo.
- Comparta una carpeta con una persona o un grupo y aparece en su **Compartido conmigo**. El
  almacenamiento necesita activado el **Control de acceso por elemento (RBAC)**, que no lo está de
  forma predeterminada.
- Las eliminaciones se pueden deshacer dentro de un periodo de retención y las escrituras guardan
  instantáneas, ambas dentro del almacenamiento que usted ya montó.
- Carpetas cifradas de extremo a extremo: se cifran en el navegador, y el servidor nunca recibe una
  clave. Una aplicación que usted permita edita un archivo así en su navegador, que lo descifra
  para la aplicación y cifra lo que esta guarda. Análisis opcional con ClamAV de cada archivo que
  se escribe.

Todas las funciones de uso compartido y protección, en detalle: [docs/FEATURES.md → Sharing and protection](docs/FEATURES.md#sharing-and-protection).

<p align="center"><img src="https://filex.sh/shots/public-share.0e3ba07f7c88.png" alt="Un enlace compartido público, tal como lo ve quien lo recibe" width="860"></p>

<details>
<summary><b>Capturas de pantalla de uso compartido y protección</b> - el cuadro de diálogo Compartir, lo que ve quien lo recibe, Mis enlaces, quién puede cifrar</summary>

<table>
<tr>
<td width="50%" valign="top">

![Cuadro de diálogo Compartir](https://filex.sh/shots/share-modal.87cb16a1a6f0.png)

<sub>Uso compartido - PIN, caducidad, límite de descargas, `curl` de una línea</sub>

</td>
<td width="50%" valign="top">

![Visor de Markdown](https://filex.sh/shots/viewer-markdown.1789ecdcfbc5.png)

<sub>Visor de Markdown</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Un enlace compartido público, tal como lo ve quien lo recibe](https://filex.sh/shots/public-share.0e3ba07f7c88.png)

<sub>…y lo que abre la persona que está al otro lado. filex tiene UNA sola pantalla hacia el exterior - un archivo compartido, una carpeta, una solicitud de archivos, la página de firma de una aplicación y el PIN que va delante de cualquiera de ellos son todos esta página, con el nombre de su instancia</sub>

</td>
<td width="50%" valign="top">

![Mis enlaces con el menú Acciones de una fila abierto](https://filex.sh/shots/signing/my-shares-1440.6f8676cbb555.png)

<sub>Mis enlaces - los enlaces que usted creó, y sus PIN cuando necesite pasarle uno a alguien</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Admin → Enlaces compartidos, con el menú Acciones de una fila abierto](https://filex.sh/shots/signing/admin-table-actions-1440.0fffdc146eb1.png)

<sub>Cada tabla de administración - un único menú **Acciones** fijado por fila, el mismo menú que abre el ⋮ del explorador</sub>

</td>
<td width="50%" valign="top">

![Admin → Cifrado: la política de aprobación y tres solicitudes en espera](https://filex.sh/shots/encryption/admin-encryption-1440.35606d99b816.png)

<sub>Quién puede cifrar - desactivado, solo administradores, todas las personas cuyo rol lo permita, o tras la aprobación de un administrador; las solicitudes en espera, con quién las hizo y por qué ([quién puede cifrar](docs/E2E-ENCRYPTION.md#who-may-encrypt))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![La solicitud de una carpeta cifrada, con su motivo](https://filex.sh/shots/encryption/request-new-folder.e74894fba30f.png)

<sub>…y el lado de la persona: la solicitud que abre el cuadro de diálogo Nueva carpeta - una sola carpeta cifrada nueva, pedida a un administrador con un motivo</sub>

</td>
</tr>
</table>

</details>

<br>

## Personas y acceso

**Las cuentas que la gente ya tiene, y un rol para cada persona.**

- Inicie sesión con una contraseña local, OIDC, LDAP / Active Directory, un proxy de autenticación o
  la cuenta de Windows o Linux del equipo en el que se ejecuta filex.
- Cada persona tiene un solo rol - Administrador, Usuario, Lector o uno propio - formado por permisos
  con nombre. Los grupos llevan acceso a carpetas y un rol, pueden seguir los grupos de su
  directorio, y las excepciones por persona prevalecen sobre el rol.
- Adivinar una contraseña es lento: las contraseñas incorrectas se contabilizan por cuenta y por
  dirección, por igual en el formulario web, WebDAV, FTPS y SFTP.
- Multiinquilino por diseño, tras un solo interruptor: cada inquilino tiene sus propios proveedores
  de inicio de sesión, su propio dominio y certificado, y se gestiona a sí mismo. Desactivado,
  ninguna pantalla muestra un inquilino.

Todas las funciones de personas y acceso, en detalle: [docs/FEATURES.md → People and access](docs/FEATURES.md#people-and-access).

<p align="center"><img src="https://filex.sh/shots/roles/roles-list-1440.6bb0779b66e3.png" alt="Admin → Roles: los roles integrados y dos personalizados" width="860"></p>

<details>
<summary><b>Capturas de pantalla de personas y acceso</b> - roles, grupos, seguridad del inicio de sesión, una cuenta bloqueada, el panel de administración y su menú</summary>

<table>
<tr>
<td width="50%" valign="top">

![Admin → Roles: los roles integrados y dos personalizados](https://filex.sh/shots/roles/roles-list-1440.6bb0779b66e3.png)

<sub>Roles - Administrador, Usuario, Lector y sus propios roles: quién tiene cada uno, qué permite, dónde difiere según la carpeta, sus límites ([docs/PERMISSIONS.md](docs/PERMISSIONS.md))</sub>

</td>
<td width="50%" valign="top">

![Admin → Grupos](https://filex.sh/shots/groups/groups-list-1440.22ff57b029f5.png)

<sub>Grupos - conjuntos de personas con nombre, con acceso a carpetas y un rol; miembros agregados a mano o sincronizados con los grupos que trae un inicio de sesión ([docs/GROUPS.md](docs/GROUPS.md))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Compartir una carpeta con un grupo](https://filex.sh/shots/groups/share-group-1440.31c2a81e1eeb.png)

<sub>Compartir una carpeta con un grupo, junto a personas - Propietario se pide en el cuadro de diálogo, no se concede con un clic</sub>

</td>
<td width="50%" valign="top">

![Admin → Seguridad de acceso](https://filex.sh/shots/loginsecurity/login-security-1440.70f2a2a76257.png)

<sub>Seguridad del inicio de sesión - el límite de intentos y las direcciones permitidas, que nunca se cuentan; los proxies de confianza, los bloqueos y el registro de inicios de sesión siguen en la misma página ([límites de intentos de inicio de sesión](docs/CONFIGURATION.md#sign-in-attempt-limits))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![El formulario de inicio de sesión de una cuenta bloqueada](https://filex.sh/shots/loginsecurity/login-locked-1440.42e1fe6f1937.png)

<sub>…y lo que dice el formulario de inicio de sesión de una cuenta bloqueada, con el tiempo restante del bloqueo en su botón</sub>

</td>
<td width="50%" valign="top">

![Panel principal de administración](https://filex.sh/shots/admin-dashboard.233358b87c3e.png)

<sub>Panel de administración</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Página de inicio de la demo](https://filex.sh/shots/demo-landing.64f23c6f8cb3.png)

<sub>Página de inicio de la demo</sub>

</td>
<td width="50%" valign="top">

![El panel Ajustes del menú de administración, abierto encima de Admin → Ajustes → General](https://filex.sh/shots/megamenu/settings-panel-1440.23a710915f9c.png)

<sub>El menú de administración - todas las páginas en cuatro paneles, **Archivos y almacenamiento**, **Personas**, **Ajustes** (General, Seguridad y datos, Procesamiento de archivos e integraciones) y **Mantenimiento y registros**, con una línea breve debajo de cada una; un teléfono recibe las mismas páginas en un panel deslizante ([docs/ADMIN-PANEL.md](docs/ADMIN-PANEL.md))</sub>

</td>
</tr>
</table>

</details>

<br>

## Aplicación de escritorio y CLI

**Las carpetas de su equipo se mantienen sincronizadas con el servidor, en vivo.**

- Una aplicación para Windows, Linux y macOS (Apple Silicon): el mismo explorador en una ventana, con
  varias cuentas o inquilinos lado a lado.
- Haga clic con el botón derecho en una carpeta → **Mantener en este equipo** y se mantiene
  sincronizada con el servidor en ambos sentidos, en aproximadamente un segundo. Todo lo demás queda
  solo en línea en la ventana.
- Inicia sesión a través de su navegador, así que el SSO y la MFA se comportan igual que en la web,
  y abre los documentos de Office de su propio disco en el editor que ejecuta su servidor.
- Los equipos sin interfaz gráfica tienen el mismo motor: `filex sync` y `filex client`.

**Instálela** desde la Microsoft Store (Windows 10/11) o la Snap Store (Ubuntu y otros Linux con
snapd):

<p>
<a href="https://apps.microsoft.com/detail/9PKXDJLVZWXW"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/badges/ms-store-light.svg"><img src="docs/badges/ms-store-dark.svg" alt="Descargar de Microsoft Store" height="52"></picture></a>&nbsp;&nbsp;&nbsp;
<a href="https://snapcraft.io/filex-app"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/badges/snap-store-white.svg"><img src="docs/badges/snap-store-black.svg" alt="Obténgalo en la Snap Store" height="52"></picture></a>
</p>

o con un gestor de paquetes:

```bash
brew install brf-tech/filex/filex-app       # macOS 13+ (Apple Silicon), Homebrew tap BRF-Tech/homebrew-filex
sudo snap install filex-app                 # the same snap as the badge above
```

<details>
<summary><b>Más sobre la instalación, y la CLI</b> - la versión de la Store, winget, las descargas de cada versión, Linux, arm64, los comandos del cliente</summary>

La versión de la Store (*filex File Manager*) es la única copia para Windows con firma de
código - Microsoft la firma - y la Store la mantiene actualizada. El paquete de winget de
la aplicación de escritorio (`BRFTech.filex-app`) está a la espera de su primera revisión
por parte de los moderadores de winget - hasta que lo acepten, una versión deja el envío
como está - así que
`winget install BRFTech.filex-app` todavía no lo encuentra.
El instalador, el `.exe` portátil, el AppImage, el `.deb`, el `.rpm` y el `.dmg` se
adjuntan a la [última versión](https://github.com/BRF-Tech/filex/releases/latest) -
todavía sin firma de código, así que es de esperar un aviso de SmartScreen con el
instalador de Windows. Detalles: [docs/DESKTOP.md](docs/DESKTOP.md). Solo la CLI:
`brew install brf-tech/filex/filex`, o `winget install BRFTech.filex` en Windows
([docs/CLI.md](docs/CLI.md)).

En Linux el `.deb` y el `.rpm` no necesitan nada; en Ubuntu 23.10 y posteriores un AppImage
necesita, una sola vez, un perfil de AppArmor
([AppImage on recent Ubuntu](docs/DESKTOP.md#appimage-on-recent-ubuntu)), y el Snap se ejecuta
dentro del confinamiento estricto del snap
([The snap and the sandbox](docs/DESKTOP.md#the-snap-and-the-sandbox)). Cada versión se compila
también para arm64: el servidor, las imágenes y la aplicación de escritorio en Linux, Windows on Arm
y Apple Silicon ([docs/INSTALLATION.md → ARM (arm64)](docs/INSTALLATION.md#arm-arm64)). Todo lo que
hace la aplicación: [docs/FEATURES.md → Desktop app, phone and CLI](docs/FEATURES.md#desktop-app-phone-and-cli).

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

</details>

<br>

## Integrarlo en su aplicación

**Un gestor de archivos de verdad dentro de su propio producto.**

La misma interfaz se distribuye como un componente de Vue 3, un componente de React y un componente
web `<filex-explorer>` independiente del framework, respaldada por su propio servidor filex. Un
token restringido la limita a una carpeta por inquilino, y eso lo impone el backend, no el widget.
El componente web ocupa dos líneas:

```html
<script type="module" src="https://cdn.jsdelivr.net/npm/@brftech/filex/dist/filex.js"></script>
<filex-explorer api-base="http://localhost:5212" sidenav connections ui-profile="simple"></filex-explorer>
```

<p align="center"><img src="https://filex.sh/shots/sidenav/embed-webcomponent-1440.b802d5b6ab56.png" alt="Componente web integrado" width="860"></p>

<details>
<summary><b>Vue 3 y React</b> - los otros dos envoltorios, y dónde están los detalles</summary>

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

Estilos, bundlers, los atributos `sidenav` / `connections` / `ui-profile`, los tokens restringidos
para hosts multiinquilino, una cookie de sesión desde otro origen y la impresión desde una
aplicación: [docs/INTEGRATION.md](docs/INTEGRATION.md), resumido en
[docs/FEATURES.md → Embedding](docs/FEATURES.md#embedding). ⚠ Mantenga los paquetes y el
servidor en la misma versión ([docs/API.md](docs/API.md)).

<table>
<tr>
<td width="50%" valign="top">

![Compartido conmigo](https://filex.sh/shots/sidenav/view-shared-1440.faf6f6668c20.png)

<sub>Compartido conmigo - carpetas a las que otras personas le dieron acceso, sin instrucciones de montaje</sub>

</td>
<td width="50%" valign="top">

![Componente web integrado](https://filex.sh/shots/sidenav/embed-webcomponent-1440.b802d5b6ab56.png)

<sub>Integrado en la página de otro producto</sub>

</td>
</tr>
</table>

</details>

<br>

## Aplicaciones

**Cosas nuevas que hacer con los archivos, exactamente con los permisos que usted aprobó.**

Una aplicación es un módulo de WebAssembly que se ejecuta dentro de filex en un entorno aislado, una
interfaz propia en un marco aislado, o ambas cosas. Se instala desde el enlace de una tienda o desde
su dirección de GitHub, usted lee cada permiso que pide, y nada se actualiza solo. Las personas de
su filex pueden explorar una tienda de confianza en su pantalla **Tienda de aplicaciones** y pedirle
una aplicación o un complemento de almacenamiento. Cinco se distribuyen como repositorios públicos:

| Aplicación | Qué agrega |
|---|---|
| [Firma electrónica](https://github.com/BRF-Tech/filex-sign) | Firme un PDF, o pida a otras personas que lo firmen: las personas de este filex firman dentro de él, cualquier otra persona mediante un enlace privado, detrás de un PIN de forma predeterminada |
| [Convertir](https://github.com/BRF-Tech/filex-convert) | **Convertir…** en cualquier archivo o selección: imágenes, video, audio, documentos, libros electrónicos, archivos comprimidos, datos, subtítulos y fuentes |
| [Office editor](https://github.com/BRF-Tech/filex-office-editor) | Documentos de Word, Excel y PowerPoint en el editor de ONLYOFFICE, en el navegador y sin Document Server; sus versiones 0.2.0 y 0.3.0, hechas para filex 0.56, añaden las carpetas cifradas y varias personas a la vez |; su 0.4.0, hecha para filex 0.57, abre todos los tipos que abre un Document Server (`.doc`, `.xls`, `.ppt`, `.csv`...) y trae un motor de ofimática que filex ejecuta en el servidor para las miniaturas y las conversiones de documentos de oficina ([docs/OFFICE-EDITOR.md](docs/OFFICE-EDITOR.md))
| [filextext](https://github.com/BRF-Tech/filextext-app) | Un espacio de trabajo de texto cifrado de extremo a extremo en un único archivo `.fxtxt` |
| [draw.io](https://github.com/BRF-Tech/filex-drawio) | El editor de diagramas draw.io, en archivos `.drawio` y `.dio` |

Una aplicación que usted permita puede editar un archivo **junto con** otras personas - el relé de
filex pone sus cambios en un solo orden sin leerlos, y quien solo puede leer lo sigue en vivo - y
puede abrir un archivo de una **carpeta cifrada**, que su navegador descifra para ella y vuelve a
cifrar antes de guardarlo.

Un paquete de idioma también es una aplicación: el español, el alemán y el francés se distribuyen
como ejemplos.

Instale una en pocos minutos: [docs/APP-PLUGINS.md → Install one](docs/APP-PLUGINS.md#install-one). Todas las funciones de las aplicaciones, en detalle: [docs/FEATURES.md → Apps](docs/FEATURES.md#apps).

<p align="center"><img src="https://filex.sh/shots/signing/sign-place-1440.696a10b65be8.png" alt="Colocación de los recuadros en el documento" width="860"></p>

<details>
<summary><b>Capturas de pantalla de las aplicaciones</b> - una solicitud de firma de principio a fin, instalar una aplicación, el convertidor, una aplicación con interfaz propia, la edición conjunta, el archivo de una carpeta cifrada en una aplicación, un paquete de idioma, Aplicaciones predeterminadas, instalar desde una tienda</summary>

Dana pide a un colega del mismo filex y a un socio externo que firmen un acuerdo. La
aplicación es [Firma electrónica](https://github.com/BRF-Tech/filex-sign); cada pantalla
la genera filex, y el enlace que recibe el socio es un enlace compartido corriente.

<table>
<tr>
<td width="50%" valign="top">

![Definición de los recuadros de una solicitud de firma](https://filex.sh/shots/signing/sign-define-1440.63ea72b13724.png)

<sub>Definir los recuadros - dé un nombre a cada uno y diga de quién es; el documento viene después</sub>

</td>
<td width="50%" valign="top">

![Colocación de los recuadros en el documento](https://filex.sh/shots/signing/sign-place-1440.696a10b65be8.png)

<sub>Colocarlos - elija un recuadro, toque la página donde va</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![La página de PIN del firmante externo](https://filex.sh/shots/signing/sign-outside-pin-1440.2cd8ccb99483.png)

<sub>El enlace del socio - la única pantalla pública de filex, con el nombre de su instancia, detrás de un PIN</sub>

</td>
<td width="50%" valign="top">

![El firmante externo completando sus recuadros](https://filex.sh/shots/signing/sign-outside-fill-1440.9f50aeb7abef.png)

<sub>…y lo que abre: solo los recuadros del propio socio - aquí, un nombre escrito con la fuente que eligió el solicitante (dibujar y subir son las otras dos opciones)</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![El documento bloqueado, con su panel Firmas abierto](https://filex.sh/shots/signing/sign-status-1440.54c60e832ced.png)

<sub>Mientras está en curso - el documento congelado para todos y, en sus detalles, quién ha firmado</sub>

</td>
<td width="50%" valign="top">

![La revisión de permisos del asistente de instalación](https://filex.sh/shots/apps/apps-install-review-1440.cdb1a4ebf8f6.png)

<sub>Instalar una aplicación - cada permiso que pide, en lenguaje claro, antes de que se ejecute nada</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![El detalle de una aplicación instalada](https://filex.sh/shots/apps/apps-detail-1440.b7f6ec845458.png)

<sub>Una aplicación instalada - de dónde vino, su huella digital y cada permiso que tiene, en lenguaje claro (su configuración y sus acciones vienen después, más abajo en la página)</sub>

</td>
<td width="50%" valign="top">

![El asistente del convertidor](https://filex.sh/shots/apps/convert-wizard-1440.231ada006fd6.png)

<sub>El convertidor, otra aplicación - cada destino bajo su categoría, tres pasos</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![La revisión de instalación de una aplicación con interfaz propia](https://filex.sh/shots/apps/app-interface-review-1440.877a89c82359.png)

<sub>Una aplicación que trae su propia interfaz - la revisión muestra la huella digital del paquete, cada dirección fuera de él (una en vivo es un permiso, en amarillo) y lo que un navegador no puede prometer</sub>

</td>
<td width="50%" valign="top">

![La interfaz propia de una aplicación abierta como visor de un archivo](https://filex.sh/shots/apps/app-interface-viewer-1440.519b23618156.png)

<sub>…y esa interfaz abierta en un archivo de su propio tipo, donde estaría la vista previa de filex. Lee y guarda el archivo a través de filex, en un marco aislado (una pequeña aplicación de ejemplo, escrita para estas imágenes)</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Dos personas en un documento: una edita, la otra observa](https://filex.sh/shots/officeapp/coedit-watcher-1280.904b31e20f36.png)

<sub>Edición conjunta - Dana escribe, y Sam, que solo puede leer, ve llegar cada cambio y su cursor a través del relé de filex ([Edición conjunta](docs/APP-PLUGINS-API.md#editing-together-coedit-056); una aplicación de ejemplo escrita para estas imágenes)</sub>

</td>
<td width="50%" valign="top">

![Un archivo de una carpeta cifrada abierto en una aplicación, descifrado por el navegador](https://filex.sh/shots/e2eapp/app-encrypted-folder-1280.856d3586a9e2.png)

<sub>Un archivo de una carpeta cifrada de extremo a extremo en una aplicación que usted permitió - su navegador lo descifra para la aplicación y cifra lo que se guarda, y el servidor sigue guardando solo texto cifrado (la misma aplicación de ejemplo; [Editar en una aplicación](docs/E2E-ENCRYPTION.md#editing-in-an-app))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![La lista Aplicaciones, con un paquete de idioma entre las aplicaciones](https://filex.sh/shots/langpack/apps-list-1440.004f0c819981.png)

<sub>Todas las aplicaciones de la instancia, y un **paquete de idioma** entre ellas - un manifiesto sin nada que se ejecute, que dice cuánto de este filex traduce y cuyo idioma se va con él</sub>

</td>
<td width="50%" valign="top">

![Complementos → Aplicaciones predeterminadas](https://filex.sh/shots/defaultapps/default-apps-1440.0dd62b519f8f.png)

<sub>Aplicaciones predeterminadas - cada tipo de archivo gestionado por algo además de filex: quién lo abre y quién genera su miniatura, en el orden que usted establezca ([Aplicaciones predeterminadas](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Carpetas mostradas con sus archivos más recientes](https://filex.sh/shots/thumbnails/folders-grid-1440.dd7653d6edca.png)

<sub>Vistas previas de carpetas - cada carpeta mostrada con los tres últimos archivos que entraron en ella; los SVG los genera el motor integrado de filex ([docs/thumbnails.md](docs/thumbnails.md#folder-previews))</sub>

</td>
<td width="50%" valign="top">

![La revisión de instalación abierta desde el enlace de una tienda](https://filex.sh/shots/store/store-review-1440.36cf3bbc2a50.png)

<sub>Instalar desde una tienda - la misma revisión, marcada **De la tienda**, con la clave de licencia de la aplicación de pago mostrada por sus primeros caracteres ([Installing from a store](docs/APP-PLUGINS.md#installing-from-a-store))</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![El primer enlace de una tienda: ¿confiar en ella?](https://filex.sh/shots/store/store-trust-1440.8fb07862db1a.png)

<sub>El primer enlace de una tienda - su dirección y las huellas digitales de sus claves, para compararlas antes de confiar en ella ([Tiendas de confianza](docs/APP-PLUGINS.md#trusted-stores))</sub>

</td>
<td width="50%" valign="top">

![Una aplicación de pago retenida, con la franja en una página de administración](https://filex.sh/shots/store/store-license-held-1440.6fd145640e0f.png)

<sub>Una licencia que la tienda revocó - la aplicación retenida, *Sin licencia*, sin quitar nada, y una franja en cada página de administración ([Aplicaciones de pago](docs/APP-PLUGINS.md#paid-apps))</sub>

</td>
</tr>
</table>

</details>

<br>

## Agentes de IA / MCP

**Un agente trabaja en la carpeta que usted le dio, y en ninguna otra.**

- Un **servidor MCP** nativo en `/api/ai/mcp`, y una superficie REST en `/api/ai` descrita en un
  archivo OpenAPI 3.1.
- Una clave de API lleva permisos por verbo y se puede restringir a una sola carpeta, y nada de lo
  que entrega puede ser más amplio que la propia clave.
- Un archivo grande pasa por un **ticket de subida**: una URL de corta duración y de un solo uso, así
  que sus bytes nunca viajan por el contexto del modelo.

```bash
claude mcp add filex --transport http https://files.example.com/api/ai/mcp \
  --header "Authorization: Bearer <api-token>"
```

Lo que puede hacer un agente, lo que una clave nunca puede entregar, las carpetas cifradas y los
tickets de subida: [docs/FEATURES.md → AI agents and MCP](docs/FEATURES.md#ai-agents-and-mcp); la
referencia es [docs/MCP.md](docs/MCP.md).

<br>

# Si viene de Nextcloud, Dropbox o Google Drive

filex es un gestor de archivos, no una suite de trabajo en grupo. Esto es lo que significa
frente a cuatro productos de los que usted puede venir (File Browser entre ellos):

| Si viene de | Con filex |
|---|---|
| **Nextcloud**<br>Una plataforma de colaboración que usted aloja o contrata a un proveedor: archivos y, alrededor de ellos, calendario, contactos, correo, chat, videollamadas y una suite ofimática en línea. Una aplicación PHP detrás de un servidor web, con una base de datos. | Solo la parte de los archivos.<br>Un binario de Go con SQLite dentro (PostgreSQL o MySQL cuando los quiera).<br>Una aplicación de escritorio con sincronización de carpetas en vivo, enlaces compartidos, SSO y LDAP.<br>Los documentos de Office, en solitario o entre varias personas, en la aplicación de editor ofimático o en el ONLYOFFICE que usted conecte.<br>Sin calendario, contactos, correo, chat ni videollamadas. |
| **Dropbox**, **Google Drive**<br>Servicios alojados: sus archivos viven en los servidores del proveedor, bajo su cuota y sus condiciones. | Su propio servidor y el almacenamiento que ya tiene: un disco, un recurso compartido de un NAS, un bucket S3.<br>Lo de todos los días, en su sitio: una interfaz web, enlaces compartidos con PIN y caducidad, solicitudes de archivos, papelera e historial de versiones.<br>Una aplicación de escritorio que mantiene sincronizadas las carpetas que usted elija. |
| **File Browser**<br>Un binario que pone una interfaz web sobre un directorio que usted le indica, con cuentas de usuario (cada una con su propio ámbito y sus interruptores de permisos), reglas de permitir y denegar por ruta y enlaces compartidos con contraseña y caducidad. Su repositorio está archivado, y su [README](https://github.com/filebrowser/filebrowser) dice que no habrá más versiones, correcciones de errores ni correcciones de seguridad. | El mismo arranque con un solo comando.<br>Varios almacenamientos lado a lado.<br>Roles y grupos además del acceso por carpeta; inicio de sesión único con OIDC y LDAP integrados.<br>Papelera y versiones, búsqueda de texto completo.<br>El mismo árbol accesible como S3, SFTP, FTPS, NFSv3 y WebDAV. |

**Lo que filex no tiene**, venga de donde venga:

- **Una aplicación para Android o iOS.** En un teléfono, filex es la aplicación web, que se
  ofrece para instalarse como una
  ([On a phone or a tablet](docs/DESKTOP.md#on-a-phone-or-a-tablet-the-web-app)) pero
  necesita conexión para mostrar los archivos; con ella cerrada, las notificaciones llegan
  como Web Push una vez que usted las activa para el dispositivo.
- **Archivos de marcador de posición en Finder o en el Explorador.** La aplicación de
  escritorio copia las carpetas que usted mantiene en el equipo, y todo lo demás queda en
  línea, en su propia ventana
  ([docs/DESKTOP.md](docs/DESKTOP.md#keeping-folders-on-this-computer)).
- **Un editor ofimático integrado en el binario.** Los documentos de Office se editan - en
  solitario o entre varias personas - con la aplicación de editor ofimático
  que usted instala
  ([`filex-office-editor`](https://github.com/BRF-Tech/filex-office-editor), el editor de
  ONLYOFFICE en el navegador, sin Document Server), o en un ONLYOFFICE Document Server que
  usted conecte; sin ninguno de los dos se abren en una vista previa de solo lectura
  ([docs/ONLYOFFICE.md](docs/ONLYOFFICE.md)).

<details>
<summary><b>Al mudarse</b> - lo que viene con usted y lo que no</summary>

- Donde los archivos ya están en algún lugar que filex puede montar - una carpeta en un
  disco o un recurso compartido de un NAS, un prefijo en un bucket S3, un servidor SFTP, FTP
  o WebDAV - mudarse es un montaje, no una migración. filex no almacena los archivos por sí
  mismo: apunte un almacenamiento a esa carpeta y ahí están, tal cual.
- Los archivos que están en Dropbox o Google Drive tienen que copiarse antes a un lugar así:
  filex no incluye ningún controlador de almacenamiento para ninguno de los dos.
- Solo se mudan los archivos: los enlaces, los permisos y el historial de versiones que
  guardaba el sistema anterior no se importan.
- Lo que filex agrega - la papelera, el historial de versiones, los borradores - está en
  carpetas ocultas en la raíz del almacenamiento, y una carpeta que usted cifra de extremo a
  extremo contiene texto cifrado a partir de entonces
  ([docs/STORAGE.md](docs/STORAGE.md), [docs/TRASH-VERSIONING.md](docs/TRASH-VERSIONING.md)).

</details>

<br>

# Autoalojamiento con Compose o Helm

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
  (Deployment + PVC + Ingress opcional), publicado con cada versión:
  `helm repo add brf-tech https://helm.brf.sh` y luego `helm install filex brf-tech/filex`
  (o `oci://ghcr.io/brf-tech/charts/filex`). Cada servicio adicional de arriba es un interruptor
  `enabled` en `values.yaml` - incluya PostgreSQL / Redis / un servidor S3, o conecte
  OnlyOffice / Drawio externos.

Las instrucciones paso a paso de cada nivel están en
[docs/INSTALLATION.md](docs/INSTALLATION.md).

filex se ejecuta en la raíz de un host propio o bajo una ruta de un host compartido
(`https://example.com/filex/`): un solo ajuste, `FILEX_BASE_PATH`, y un proxy
que pase la ruta completa - ejemplos para Caddy, nginx y Helm en
[docs/DEPLOYMENT.md → Serving filex under a sub-path](docs/DEPLOYMENT.md#serving-filex-under-a-sub-path).

<br>

# Documentación

Las guías se publican como sitio en [docs.filex.sh](https://docs.filex.sh), y el
[índice completo de la documentación](docs/README.md) lista cada página.

Empiece por [Instalación](docs/INSTALLATION.md), [Configuración](docs/CONFIGURATION.md), la
[aplicación de escritorio](docs/DESKTOP.md), los [protocolos](docs/PROTOCOLS.md), las
[aplicaciones](docs/APP-PLUGINS.md) y [ONLYOFFICE](docs/ONLYOFFICE.md).
[Funciones](docs/FEATURES.md) da a cada función una entrada con sus límites y su guía.

<br>

# Funciones

| Área | Lo que obtiene |
|---|---|
| **El explorador** | Un solo diseño en la aplicación de administración, la aplicación de escritorio y cada integración: un panel de navegación, vistas de lista, cuadrícula y galería, pestañas, etiquetas personales y de equipo, una paleta de comandos ⌘K, atajos de teclado reasignables, documentos nuevos desde el menú **+ Nuevo** y temas que compone un operador ([Integración](docs/INTEGRATION.md), [Temas](docs/INTEGRATION.md#themes)) |
| **Almacenamiento** | Discos locales, S3, FTP, SFTP, WebDAV y SMB/NAS montados lado a lado, copiar y mover entre ellos, y un complemento para cualquier otra cosa, desde el panel de administración o una tienda de aplicaciones ([Almacenamiento](docs/STORAGE.md), [Complementos de almacenamiento](docs/PLUGINS.md)) |
| **Sin navegador** | El mismo árbol servido como S3, SFTP, FTPS, NFSv3 y WebDAV - un movimiento o una eliminación allí es tan seguro junto a un escaneo del almacenamiento como uno en la interfaz web - más `filex mount` por HTTPS ([Protocolos](docs/PROTOCOLS.md)) |
| **Inicio de sesión** | OIDC, LDAP / Active Directory, cuentas de Windows y Linux, y límites de intentos de inicio de sesión ([SSO](docs/SSO.md), [LDAP](docs/LDAP.md), [Cuentas del sistema operativo](docs/OS-LOGIN.md)) |
| **Personas y acceso** | Roles y permisos por persona, grupos, acceso por carpeta y multiinquilino nativo ([Permisos](docs/PERMISSIONS.md), [Grupos](docs/GROUPS.md), [Multiinquilino](docs/MULTI-TENANCY.md)) |
| **Uso compartido** | Enlaces públicos con PIN, caducidad y límite de descargas, solicitudes de archivos e invitaciones para compartir por correo electrónico ([Uso compartido](docs/SHARING.md)) |
| **Protección** | Papelera e historial de versiones, análisis opcional con ClamAV, carpetas cifradas de extremo a extremo, replicación a un segundo almacenamiento (con una copia inicial de lo que ya hay) y un registro de auditoría ([Papelera y versiones](docs/TRASH-VERSIONING.md), [Protección](docs/PROTECTION.md), [Cifrado E2E](docs/E2E-ENCRYPTION.md), [Replicación](docs/REPLICATION.md), [Registro de auditoría](docs/BACKEND.md#admin-audit-log)) |
| **Visores y editores** | Imágenes, video, audio, PDF, Markdown, código (Monaco), documentos de Office en la aplicación de editor ofimático o mediante ONLYOFFICE, diagramas de draw.io, diagramas de Mermaid dibujados y editados como texto, modelos 3D, y archivos comprimidos en ZIP, 7z y TAR ([ONLYOFFICE](docs/ONLYOFFICE.md), [Archivos comprimidos](docs/ARCHIVES.md)) |
| **Búsqueda y miniaturas** | Búsqueda de texto completo integrada que respeta los permisos, y miniaturas para imágenes, video, PDF, carpetas y documentos de Office - las de Office las dibuja en el servidor el motor de la app de editor de Office o un ONLYOFFICE conectado ([Búsqueda](docs/SEARCH.md), [Miniaturas](docs/thumbnails.md)) |
| **Tiempo real** | Presencia y actualizaciones de archivos en vivo por WebSocket, y la edición conjunta de un archivo en una aplicación, a través del relé de filex ([Tiempo real](docs/REALTIME.md), [Edición conjunta](docs/APP-PLUGINS-API.md#editing-together-coedit-056)) |
| **Clientes** | Una aplicación de escritorio con sincronización de carpetas, una aplicación web que un teléfono instala con el nombre y el logotipo de su instancia, una CLI, integraciones de Vue / React / componente web, una API REST y un servidor MCP ([Escritorio](docs/DESKTOP.md), [Sincronización](docs/SYNC.md), [CLI](docs/CLI.md), [Integración](docs/INTEGRATION.md), [MCP](docs/MCP.md)) |
| **Aplicaciones e idiomas** | Aplicaciones en un entorno aislado (Firma electrónica, Convertir, Office editor, filextext, draw.io) que imprimen a través de filex, editan un archivo entre varias personas a través de su relé y abren el archivo de una carpeta cifrada solo cuando usted lo permite, descifrado en su navegador; paquetes de idioma y un diseño de derecha a izquierda ([Aplicaciones](docs/APP-PLUGINS.md), [Editar en una aplicación](docs/E2E-ENCRYPTION.md#editing-in-an-app), [RTL](docs/RTL.md)) |
| **Ponerlo en marcha** | Un solo binario o un solo contenedor; SQLite, PostgreSQL o MySQL; un panel de administración con una sola búsqueda para cada página, ajuste y registro; webhooks, una campana en la aplicación y Web Push a teléfonos y navegadores, con un resumen opcional; una vista de uso y costo (Backblaze B2 por ahora); comprobación de actualizaciones, con versiones de parche que se instalan solas una vez que usted lo permite ([Bases de datos](docs/DATABASES.md), [Panel de administración](docs/ADMIN-PANEL.md), [Notificaciones](docs/NOTIFICATIONS.md), [Uso](docs/USAGE.md), [Actualizaciones](docs/UPDATES.md)) |

Cada función como entrada propia - cómo se comporta, sus límites y la página que la documenta:
[docs/FEATURES.md](docs/FEATURES.md).

<br>

# Arquitectura

Véase [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

<details>
<summary><b>Las piezas de un vistazo</b> - controladores, protocolos y paquetes en un solo diagrama</summary>

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

# Cómo se construye filex

filex tiene un solo mantenedor, que lo construye con agentes de programación de IA (Claude
Code). El mantenedor decide qué es filex y cómo funciona: la arquitectura, los modelos de
datos y de seguridad, y cómo se comporta cada función. Los agentes escriben la mayor parte
del código dentro de esa dirección, en lenguajes con los que trabaja el mantenedor. Cerca
del 82 % de los commits del repositorio de desarrollo llevan una línea
`Co-Authored-By: Claude`, así que no se oculta.

Está asistido por IA, pero no es código generado sin revisar:

- Cada cambio pasa por la cadena de pruebas antes de integrarse: pruebas de Go (también con
  el detector de carreras), unos 500 archivos de Vitest, unas 120 especificaciones de
  Playwright en Chromium, Firefox y WebKit, Cypress, y pruebas de base de datos en SQLite,
  PostgreSQL y MySQL. Cada versión se etiqueta solo después de que la matriz completa de
  GitHub y una ejecución de prueba del flujo de publicación pasen sobre el commit exacto.
- Los cambios en el inicio de sesión, los permisos, el uso compartido y el cifrado reciben
  rondas de revisión adicionales, y una prueba en rojo se corrige, no se relaja.
- Los informes de seguridad se tratan abiertamente: véase [SECURITY.md](SECURITY.md) y los
  avisos publicados. Todavía no ha habido una auditoría independiente.
- Los README en alemán, español, francés y chino y los paquetes de idioma alemán, español y
  francés son traducciones automáticas que ningún hablante nativo ha revisado todavía.

# Contribuir

[![CI](https://img.shields.io/github/actions/workflow/status/BRF-Tech/filex/ci.yml?branch=main&label=ci)](https://github.com/BRF-Tech/filex/actions)

Las incidencias y las pull requests son bienvenidas; antes de una pull request de cierto
tamaño, abra una incidencia que diga lo que va a hacer.
[docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) explica el flujo de trabajo, las pruebas que un
cambio tiene que pasar y las reglas de la documentación, y el proyecto sigue el
[Contributor Covenant](CODE_OF_CONDUCT.md). Un problema de seguridad se informa en privado,
como describe [SECURITY.md](SECURITY.md). Lo que cambió en cada versión está en
[CHANGELOG.md](CHANGELOG.md). Cada día a las 10:00, hora de Estambul, se publica una versión
con lo que `main` contiene en ese momento, cuando contiene algo nuevo; una pull request
integrada después del corte sale con la del día siguiente
([Release process](docs/CONTRIBUTING.md#release-process)).

<details>
<summary><b>Desarrollo</b> - compilar desde el código fuente, y qué hay en cada sitio</summary>

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
  `shots/`, los scripts que ejecuta `pnpm shots` para tomar las capturas de pantalla de
  arriba (publicadas en filex.sh, no guardadas en el repositorio)
- `docker/` - Dockerfiles + compose
- `deploy/` - stacks de Compose listos para usar + chart de Helm (véase [`deploy/`](deploy/))
- `docs/` - documentación en Markdown
- `docs-site/` - sitio de VitePress publicado en [docs.filex.sh](https://docs.filex.sh)

</details>

<br>

# Licencia

MIT - véase [LICENSE](LICENSE).

Las insignias de las tiendas que están en [`docs/badges/`](docs/badges/) son material
gráfico de las propias tiendas, usado sin modificar y no cubierto por esa licencia:
Microsoft y la insignia de la Microsoft Store son marcas comerciales del grupo de compañías
de Microsoft; la insignia de la Snap Store es © Canonical Ltd., bajo licencia
[CC BY-ND 2.0 UK](https://creativecommons.org/licenses/by-nd/2.0/uk/).

Nextcloud, Dropbox, Google Drive, File Browser y ONLYOFFICE son nombres de productos de
terceros, que aquí se usan solo para describirlos. filex no está afiliado a ninguno de
ellos, ni patrocinado ni respaldado por ninguno.
