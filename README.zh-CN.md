<!-- Translated from README.md as of bf857aad (v0.51.0). The English README is the source: change it first, then carry the change here. -->

<div align="center">

<img src="docs/logo.png" alt="filex 标志" width="96">

# filex - 可随处嵌入的自托管文件管理器

[![Release](https://img.shields.io/github/v/release/BRF-Tech/filex?color=2f6ceb)](https://github.com/BRF-Tech/filex/releases)
[![CI](https://img.shields.io/github/actions/workflow/status/BRF-Tech/filex/ci.yml?branch=main&label=ci)](https://github.com/BRF-Tech/filex/actions)
[![License: MIT](https://img.shields.io/github/license/BRF-Tech/filex?color=22c55e)](LICENSE)
[![Container](https://img.shields.io/badge/ghcr.io-brf--tech%2Ffilex-2496ed?logo=docker&logoColor=white)](https://github.com/BRF-Tech/filex/pkgs/container/filex)
[![Live demo](https://img.shields.io/badge/live_demo-demo.filex.sh-f59e0b)](https://demo.filex.sh)

[English](README.md) · [Türkçe](README.tr.md) · [Deutsch](README.de.md) · [Español](README.es.md) · [Français](README.fr.md) · **简体中文**

<sub>本文译自 v0.51.0 版的[英文 README](README.md)；两者不一致时，以英文版为准。本文为机器翻译，尚待母语者审校，欢迎指正。文中链接指向的文档均为英文。</sub>

filex 是单个 Go 二进制文件，自带功能完整的 Web 界面、可插拔的存储、认证和数据库驱动、**实时协作**、**可嵌入的 Web 组件**、**文件夹同步实时进行的桌面应用**（任何一边的修改大约一秒就到另一边）、让 AI 智能体可以原生驱动它的**内置 MCP 服务器**，以及**应用**：教会 filex 对文件做新事情的插件，可以是沙箱化的 WebAssembly 模块，可以是放在沙箱 iframe 里的自带界面，也可以两者兼有。第一个应用是与组织内外的人一起**签署文档**。**语言包**也是一种应用，所以翻译 filex 不必等新的发行版；对于从右到左书写的语言，filex 还会按**从右到左**排布界面。大家用已有的账户登录：SSO、LDAP，或者运行 filex 的那台机器上的 **Windows 或 Linux 账户**；在多租户实例上，**每个租户自己管理自己**：有自己的登录提供方，有自己的域名和证书。

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://filex.sh/shots/explorer-grid-dark.5ddafe2dac64.png">
  <img src="https://filex.sh/shots/explorer-grid-light.484fb070ca19.png" alt="filex 资源管理器 - 缩略图网格" width="900">
</picture>

</div>

## 立即试用

**在线演示**：[demo.filex.sh](https://demo.filex.sh)，用 `demo@demo.com` / `demo` 登录（管理员角色，演示环境每晚重置）。或者运行你自己的实例：

```bash
docker run -p 5212:5212 \
  -e FILEX_DEFAULT_STORAGE_DRIVER=local -e FILEX_DEFAULT_STORAGE_PATH=/srv/files \
  -v filex-data:/data -v "$PWD:/srv/files" \
  ghcr.io/brf-tech/filex:latest
```

这条命令提供的就是**你运行它时所在的文件夹**：打开界面，你的文件已经在里面了。`/data` 是 filex 自己的目录（SQLite 数据库、搜索索引、缩略图缓存），所以它是一个命名卷，而不是你放文件的那个文件夹；两者是有意分开的。把 `$PWD` 指向别的位置，或者之后在管理面板里添加更多存储；有多个顶层文件夹的存储桶，可以一次挂载完，每个文件夹一个存储：*Storages → Add storage → Mount several folders at once*（存储 > 添加存储 > 一次挂载多个文件夹）。

容器默认以 **root** 身份运行，所以它写入 `/data` 的内容归 root 所有；设置 `PUID`/`PGID`，让它以你自己的身份运行（[docs/DOCKER.md](docs/DOCKER.md#which-user-the-container-runs-as)）。

打开 http://localhost:5212/admin 即可，首次运行时会把管理员凭据和嵌入说明打印到控制台。那是运维人员的地址；你给谁开了账户，谁拿到的就是 **http://localhost:5212/drive** 这个地址：同一个文件管理器，只是外面没有那层管理面板。

比起浏览器标签页，更想要一个独立窗口？**桌面应用**（Windows / Linux / macOS）可登录任意 filex 服务器，并在后台同步文件夹；而且每个平台上都有一个**无需安装**就能运行的版本（便携版 `.exe`、AppImage、`.zip`）。从 [Microsoft Store](https://apps.microsoft.com/detail/9PKXDJLVZWXW)、[Snap Store](https://snapcraft.io/filex-app) 或[最新发行版](https://github.com/BRF-Tech/filex/releases/latest)获取，详见 [docs/DESKTOP.md](docs/DESKTOP.md)。

## 为什么选择 filex

大多数自托管文件管理器要么**太小**（只是带上传功能的目录列表），要么**太大**（为了其中的文件标签页而部署一整套协作办公套件）。filex 瞄准的正是两者之间的空当：

- **给你的用户用的浏览器客户端，而不只是给你自己**：把一个 `user` 或 `viewer` 账户和 `…/drive` 交给某个人，对方得到的就是文件管理器本身：自己的存储、上传、共享、搜索、编辑器。不必穿过管理面板，也不必另外部署前端。`…/admin` 是运维人员进入同一个应用的入口。
- **大家早已熟悉的导航**：一个左侧面板，上面有醒目的 **+ New**（新建）菜单和 **Home · My files · Shared with me · My shares · Recent · Starred · Drafts · Trash**（主页、我的文件、与我共享、我的共享、最近、已加星标、草稿、回收站），以及你能访问的存储；有人共享给你的存储会直接出现在那里，点一下即可，无需挂载说明。任何人都可以从顶栏把它折叠为图标栏。**Home** 是应用*内部*的一个视图，而不是应用旁边的一个页面：你的存储、你最近打开的内容和你加了星标的内容，与文件共用同一个侧边栏和同一个顶栏。这就是每个人都会得到的外壳：横跨顶栏的单个搜索框，带有 ⌘K 命令面板提示；一个 Type / Owner / Modified / Size（类型、所有者、修改时间、大小）筛选栏；Folders（文件夹）和 Files（文件）两个带标题的分区；信息面板中的 Details（详细信息）和 Activity（活动）；还有一个存储用量行。对于想要的是网盘而不是文件管理器的人，`uiProfile: 'simple'` 会预先关闭其余界面元素：一栏、一个文件夹、列表或网格。无论哪种情况都是同一个资源管理器：没有需要保持一致的第二套界面。
- **随处可嵌入**：同一套界面发布为 Vue 3 组件、React 组件，以及与框架无关的 `<filex-explorer>` Web 组件。把一个真正的文件管理器放进*你的*产品里，背后是你自己的 filex 服务器，并锁定在每个租户各自的文件夹内。导航面板也一并提供：`<filex-explorer sidenav ui-profile="simple">`，对一个完全不碰 JavaScript 的宿主页面来说，要做的启用配置就这么多。
- **原生支持 AI 智能体**：一个受 API 密钥权限约束的 REST 接口（`/api/ai`），外加一个原生的 **MCP 服务器**（`/api/ai/mcp`）；`/api/ai` 和 `/api/files` 由一份 [OpenAPI 3.1 文件](backend/internal/api/openapi.json)描述，并有一项测试保证这份文件与路由一致。把一个限定在某个文件夹内的令牌交给智能体，它就在那个文件夹里用资源管理器自己的操作工作（列出、读取、写入、复制、转换、共享、回收站、版本、压缩包），文件夹之外的东西一概碰不到。
- **只能做你批准过的事的应用**：与没有账户的合作伙伴签署合同、转换视频，凡是清单所描述的事情，都作为**应用**添加进来：可以是在 filex 内部运行的 WebAssembly 模块，可以是 filex 放在沙箱 iframe 里提供的自带界面，也可以两者兼有，权限不多不少，正是你在安装时读过并授予的那些。模块拿不到文件系统，拿不到网络，也拿不到你服务器上的任何程序；界面读不到 filex 的会话，filex 自己的策略还切断了它与网络的联系。没有什么会自动更新：新版本要等管理员处理，上一个版本点一下就能换回来。有四个应用以公开仓库的形式发布：**e-Signature**、**Convert**、**filextext**（一个端到端加密的文本工作区）和 **draw.io**；要装哪一个，就从它的 GitHub 地址安装（[应用](#应用)）。
- **用你的语言，按你的方向**：英语和土耳其语内置在二进制文件中，其他任何语言都是一个**语言包**：一个没有任何可运行内容的应用，像其他应用一样从仓库安装，翻译的是资源管理器、管理面板、陌生人打开的公开页面，*以及 filex 服务器写出的文字*：邮件、通知、链接背后不用 JavaScript 的页面。语言包会说明自己覆盖了这个版本的多少内容，缺少的部分以英语显示；复数形式遵循 CLDR，所以一种语言实际有哪些形式，得到的就是哪些形式。对于阿拉伯语、希伯来语、波斯语和乌尔都语，界面会**转为从右到左**，并在不该镜像翻转的地方停下：文档空间里和机器文本里（[docs/RTL.md](docs/RTL.md)、[编写语言包](docs/PLUGIN-KIT.md#writing-a-language-pack)）。每个人只有一种语言：账户的语言就是 Web 应用、资源管理器和桌面应用界面上的语言，在其中任何一处选定一种语言，各处都随之改变；ONLYOFFICE 编辑器也以这种语言打开，或者以管理员为所有人选定的语言打开（[编辑器的语言](docs/ONLYOFFICE.md#the-editors-language)）。
- **呈现的是你的品牌，不是我们的**：在 **Appearance**（外观）界面上用你自己的颜色组合出一个主题，并把它设为默认主题：登录页和每个公开链接也会套用它，从你的实例发出的签名请求带的是你的名字，而不是 filex 的。
- **实时**：在线头像（头像在账户上设置一次，每个以你的身份登录的客户端都显示为这个头像）和基于 WebSocket 的实时文件更新，在原生界面中，*也*在嵌入场景中（短时效票据认证、回退到 API 轮询）。批量任务的更新在发出时会合并，所以解压一个含五千个文件的压缩包，对一个打开着的资源管理器来说只需数量有上限的少量帧，而不是五千帧（[docs/REALTIME.md](docs/REALTIME.md)）。
- **桌面上也有**：同一个资源管理器以 Windows/Linux/macOS 应用的形式提供，这个应用在系统托盘里让本地文件夹与服务器保持一致（**实时**，大约一秒，双向），会自动更新，还能让多个账户（或租户）并排共存。右键点击一个文件夹 → **Keep on this computer**（保留在此电脑上），它就镜像到一个 filex 文件夹下；其余的一切在窗口中保持仅在线。没有图形界面的机器通过 `filex sync` / `filex client` 用上同一个引擎。
- **协议双向都通**：filex 可以*连接到*本地磁盘、S3、FTP、SFTP、WebDAV 和 SMB/NAS 共享，也可以*被当作* **S3**、**SFTP**、**FTPS**、**NFSv3** 和 **WebDAV** 来访问。把 `rclone`、`restic`、`aws s3`、WinSCP、FileZilla、只会 FTP 的扫描仪，或者只会 NFS 的媒体播放器指向 filex，它们就和 Web 界面一样，落在同一棵文件树上，用同样的权限、同一个回收站和同样的配额。LAN 之外还有 **`filex mount`**，它通过普通的 HTTPS 挂载远程服务器：在 Linux 上是一个文件夹，在 Windows 上是一个盘符（[docs/PROTOCOLS.md](docs/PROTOCOLS.md)）。
- **角色与按用户设置的权限**：29 项具名权限（每种文件操作、每种共享方式、每种协议、API 密钥、桌面应用、五个管理区域），每个人都有一个角色：Administrator（管理员）、User（用户）、Viewer（查看者）或自定义角色；角色在某些文件夹中可以不同（“不能删除，Scratch 文件夹除外”），还可以带有限制（链接有效期和密码、禁止的文件类型、文件大小上限、强制双因素认证）。针对个人的例外优先于角色；委派管理员可以管理用户，但发放出去的权限永远不会超过自己持有的权限；无论从哪个入口进来，得到的答复都一样：Web 应用、智能体 API、WebDAV、SFTP、FTPS、S3、NFS 和 API 密钥。已安装的应用可以添加自己的权限，例如“Request signatures”（请求签名），发放方式相同；公开链接只有在创建者仍有权创建它的期间才保持有效（[docs/PERMISSIONS.md](docs/PERMISSIONS.md)）。
- **群组**：具名的一组人，按租户划分：像与某个人共享那样与群组共享文件夹，也可以给群组一个角色，群组里没有自己角色的每个人都持有这个角色（群组之间由角色优先级决定）。人可以手动添加进来，也可以通过登录带来的群组加入（OIDC 声明、LDAP `memberOf`、操作系统的群组或代理请求头），身份提供方说该离开时就离开（[docs/GROUPS.md](docs/GROUPS.md)）。
- **用大家已有的账户登录**：本地密码、OIDC、LDAP / Active Directory、认证代理，或者运行 filex 的那台机器上的 **Windows 或 Linux 账户**：密码由操作系统判定，filex 从不保存，而且这个提供方只有在一个真实账户用它登录过之后才会开启。首次登录时谁能获得账户，每个提供方都遵循同一条规则（[docs/OS-LOGIN.md](docs/OS-LOGIN.md)）。
- **猜密码快不起来**：输错的密码按账户、按地址分别计数，Web 表单、WebDAV、FTPS 和 SFTP 都一样；锁定时间每次加倍，最长 15 分钟；IP 允许列表是重新进来的途径；转发来的客户端地址，只有出自受信任的代理才采信：默认是这台机器和 filex 旁边的容器，其他的由你指明（[登录尝试次数限制](docs/CONFIGURATION.md#sign-in-attempt-limits)）。其他站点带着访客的会话发来的更改会被拒绝（[来自其他源的请求](docs/CONFIGURATION.md#requests-from-other-origins)）。
- **从设计上就是多租户**：每个租户各自的存储与原生多租户模式、RBAC 角色 + 按项目授权、受限的 API 令牌、每个令牌各自用于审计跟踪的身份，还区分令牌类型（应用令牌与用户令牌），所以一个共享的嵌入端凭据无法管理任何人的密钥。令牌会指明自己持有的权限（空列表会被拒绝，而不是被当作“全部”），而且**它签发的任何凭据，权限都不会超出它自身**：通过范围较窄的令牌签发的 API 密钥、S3 密钥、NFS 导出或 SSH 密钥，不能超出它的动词，不能离开它的文件夹，也不能比它活得更久（`403 token_ceiling`）。租户边界在每一条指明某一行数据的路由上都会强制执行，而不只是在列出数据的路由上；实例范围的设置仅限超级租户。每个租户有一个 **realm**（领域），也就是它的登录名：两个租户里的 `alex` 是两个人，无论他们是在租户自己的地址上登录，在平台的页面上输入 realm，还是通过 SFTP 写成 `realm/alex`（[realm](docs/MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)）。而且租户自己管理自己：租户的管理员添加租户自己的 OIDC 或 LDAP，运维人员把共享的登录提供方绑定到一个或多个租户，租户的自有域名通过 CNAME 验证，提供服务所用的证书来自你的代理、filex 自身（ACME），或者是租户自己的证书（[docs/TENANT-ADMIN.md](docs/TENANT-ADMIN.md)）。
- **部署起来平淡无奇**：一个二进制文件或一个容器，运行在独立的主机上，或者运行在你共用的主机的某个子路径下；默认用 SQLite，想用时就用 Postgres/MySQL；每个驱动都用环境变量切换。每次变更，三种引擎全部由 CI 执行迁移、相互比对并写入数据，因为过去所谓“支持”，意思只是“能编译通过”（[docs/DATABASES.md](docs/DATABASES.md)）。

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

## 截图

### 应用 - 第一个应用：签署文档

Dana 请同一个 filex 上的一位同事和 filex 之外的一位合作伙伴签署一份协议。所用的应用是 [e-Signature](https://github.com/BRF-Tech/filex-sign)；每个界面都由 filex 绘制，合作伙伴收到的链接是一个普通的共享。

| 定义填写框：给每个填写框命名，并说明它是谁的；下一步是文档 | 放置填写框：选一个填写框，在页面上轻点要放的位置 |
|---|---|
| ![定义签名请求的填写框](https://filex.sh/shots/signing/sign-define-1440.63ea72b13724.png) | ![在文档上放置填写框](https://filex.sh/shots/signing/sign-place-1440.696a10b65be8.png) |

| 合作伙伴的链接：filex 仅有的一个公开页面，以你的实例的名义出现，凭 PIN 才能打开 | …以及链接打开的内容：只有对方自己的填写框，这里是一个用发起人选定的字体键入的姓名（另外两种是绘制和上传） |
|---|---|
| ![外部签署人的 PIN 验证页](https://filex.sh/shots/signing/sign-outside-pin-1440.2cd8ccb99483.png) | ![外部签署人正在填写自己的填写框](https://filex.sh/shots/signing/sign-outside-fill-1440.9f50aeb7abef.png) |

| 等待签署期间：文档对每个人都是冻结的，谁已签署，在文档的详细信息里可以看到 | 安装应用：在任何东西运行之前，先用直白的文字列出它请求的每一项权限 |
|---|---|
| ![文档已锁定，Signatures 面板已打开](https://filex.sh/shots/signing/sign-status-1440.b5bfdbd379c3.png) | ![安装向导的权限审核](https://filex.sh/shots/apps/apps-install-review-1440.cdb1a4ebf8f6.png) |

| 已安装的应用：它从哪里来、指纹是什么，以及它持有的每一项权限，用直白的文字写明（它的设置和操作接在后面，在页面更靠下的地方） | 转换器，另一个应用：每种目标格式都列在各自的类别下，共三步 |
|---|---|
| ![已安装应用的详情](https://filex.sh/shots/apps/apps-detail-1440.4e27b40c5ce7.png) | ![转换器的向导](https://filex.sh/shots/apps/convert-wizard-1440.231ada006fd6.png) |

| 自带界面的应用：审核时会显示应用包的指纹、应用包之外的每个地址（实时加载的地址是一项权限，以黄色显示），以及浏览器无法保证的事 | …以及这个界面，打开的是它自己的文件类型，就在 filex 预览本来所在的位置。它在沙箱 iframe 中通过 filex 读取和保存文件（一个小型示例应用，为这些截图而写） |
|---|---|
| ![自带界面的应用的安装审核](https://filex.sh/shots/apps/app-interface-review-1440.5e0e3009d2ba.png) | ![应用自带的界面作为文件查看器打开](https://filex.sh/shots/apps/app-interface-viewer-1440.519b23618156.png) |

| 实例上的每个应用，其中有一个**语言包**：一份没有任何可运行内容的清单，会说明自己翻译了这个 filex 的多少内容；语言包一走，它的语言也跟着走 |
|---|
| ![Apps 列表，其中有一个语言包](https://filex.sh/shots/langpack/apps-list-1440.50e11eedf3d4.png) |

### 你自己的内容，随处可达

| 铃铛：未读数就在铃铛上，每一行都通向它所说的地方 | 你的全部通知，都在资源管理器里：每个人都可以用，而不只是管理员 |
|---|---|
| ![带未读角标的铃铛，已展开](https://filex.sh/shots/signing/bell-badge-1440.61396b9ab714.png) | ![覆盖在资源管理器之上的完整通知列表](https://filex.sh/shots/signing/notifications-list-1440.3176161f2110.png) |

| My shares（我的共享）：你创建的链接，以及这些链接的 PIN，需要转告别人时用得上 | 管理面板里的每张表格：每行一个固定的 **Actions**（操作）菜单，与资源管理器的 ⋮ 打开的是同一个菜单 |
|---|---|
| ![My shares，其中一行的 Actions 菜单已打开](https://filex.sh/shots/signing/my-shares-1440.6f8676cbb555.png) | ![Admin → Shares，其中一行的 Actions 菜单已打开](https://filex.sh/shots/signing/admin-table-actions-1440.ad8c2e623c72.png) |

### 你的品牌

| Appearance（外观）：用你自己的颜色组合出一个主题，边输入边预览 | 设为默认之后，每个人的资源管理器套用的就是它… |
|---|---|
| ![主题编辑器](https://filex.sh/shots/appearance/theme-editor-1440.7cf3d997f7b1.png) | ![套用了运维人员主题的资源管理器](https://filex.sh/shots/appearance/themed-explorer-1440.dbe464fc39c7.png) |

| …登录页也是，这时还没有任何人登录 | filex 不会跟随的符号链接会明确说明：既在列表里，也在它的详细信息里用文字写明 |
|---|---|
| ![套用了运维人员主题的登录页](https://filex.sh/shots/appearance/themed-signin-1440.1c420dacc12e.png) | ![指向存储之外的符号链接，带有标记](https://filex.sh/shots/symlinks/symlink-badge-1440.0d128385d287.png) |

### 文件管理器

| 共享：PIN、过期时间、下载次数上限、单行 `curl` 命令 | Markdown 查看器 |
|---|---|
| ![共享对话框](https://filex.sh/shots/share-modal.c8a399c36424.png) | ![Markdown 查看器](https://filex.sh/shots/viewer-markdown.1789ecdcfbc5.png) |

| …以及另一端的人打开的内容。filex 只有唯一一个对外页面：一个共享的文件、一个文件夹、一个文件请求、应用的签署页面，以及挡在其中任何一个前面的 PIN，全都是这个页面，都以你的实例的名义出现 |
|---|
| ![接收者看到的公开共享链接](https://filex.sh/shots/public-share.0e3ba07f7c88.png) |

| 管理面板 | 演示首页 |
|---|---|
| ![管理仪表盘](https://filex.sh/shots/admin-dashboard.d94a065baab6.png) | ![演示首页](https://filex.sh/shots/demo-landing.d2b345f6a229.png) |

| 管理菜单：所有页面分在 **Files & storage**（文件与存储）、**People & security**（人员与安全）和 **System**（系统）三个菜单面板里，每个页面下面都有一行简短说明；在手机上，同一批页面放在抽屉里（[docs/ADMIN-PANEL.md](docs/ADMIN-PANEL.md)） |
|---|
| ![管理菜单的 People & security 面板已打开，覆盖在 Admin → Users 之上](https://filex.sh/shots/megamenu/people-panel-1440.2efdcb9a685a.png) |

| Roles（角色）：Administrator（管理员）、User（用户）、Viewer（查看者）以及你自己的角色，每个角色由谁持有、允许什么、在哪些文件夹上有所不同、有哪些限制（[docs/PERMISSIONS.md](docs/PERMISSIONS.md)） |
|---|
| ![Admin → Roles：内置角色和两个自定义角色](https://filex.sh/shots/roles/roles-list-1440.77477a2c7001.png) |

| Groups（群组）：具名的一组人，带有文件夹访问权限和一个角色；成员手动添加，或者与登录带来的群组保持一致（[docs/GROUPS.md](docs/GROUPS.md)） | 与群组共享文件夹，群组和人并列：Owner（所有者）要经对话框询问后才授予，而不是点一下就授予 |
|---|---|
| ![Admin → Groups](https://filex.sh/shots/groups/groups-list-1440.73224b70e40b.png) | ![与群组共享文件夹](https://filex.sh/shots/groups/share-group-1440.31c2a81e1eeb.png) |

| Sign-in security（登录安全）：尝试次数限制、允许的地址、受信任的代理、锁定，以及登录记录（[登录尝试次数限制](docs/CONFIGURATION.md#sign-in-attempt-limits)） | …以及账户被锁定时登录表单显示的内容，锁定的倒计时就在按钮上 |
|---|---|
| ![Admin → Sign-in security](https://filex.sh/shots/loginsecurity/login-security-1440.5a98c09e6f76.png) | ![账户被锁定时的登录表单](https://filex.sh/shots/loginsecurity/login-locked-1440.386b07b4543a.png) |

| 谁可以加密：关闭、仅限管理员、角色允许的每个人，或者经管理员批准之后；还有等待处理的请求，注明是谁提出的、为什么提出（[谁可以加密](docs/E2E-ENCRYPTION.md#who-may-encrypt)） | …以及提出请求的人这一侧：New folder（新建文件夹）对话框向管理员请求一个加密文件夹，并附上理由 |
|---|---|
| ![Admin → Encryption：策略设为须经批准，三个请求等待处理](https://filex.sh/shots/encryption/admin-encryption-1440.b74163acb93c.png) | ![在 New folder 对话框中请求一个加密文件夹](https://filex.sh/shots/encryption/request-new-folder.e74894fba30f.png) |

| Default apps（默认应用）：每一种由 filex 之外的东西处理的文件，谁来打开它、谁来绘制它的缩略图，按你设定的顺序（[默认应用](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail)） | 文件夹预览：每个文件夹用最近进入其中的三个文件绘制；这些 SVG 由 filex 的内置引擎绘制（[docs/thumbnails.md](docs/thumbnails.md#folder-previews)） |
|---|---|
| ![Plugins → Default apps](https://filex.sh/shots/defaultapps/default-apps-1440.27d3c64fe457.png) | ![用最新文件绘制的文件夹](https://filex.sh/shots/thumbnails/folders-grid-1440.2b408fb54f7e.png) |

| 连接了 ONLYOFFICE 时，`.csv` 在 ONLYOFFICE 的电子表格中打开，先是查看，而且没有分隔符对话框：文件自己的分隔符会一并传过去（[CSV 文件](docs/ONLYOFFICE.md#csv-files)） | …以及它的编辑器，其中会说明保存为 CSV 时保留什么；文件写回去时仍是同一种 CSV |
|---|---|
| ![以分号分隔的 CSV，在 ONLYOFFICE 的电子表格中打开](https://filex.sh/shots/csvoffice/csv-view-1440.83237ba55d3d.png) | ![ONLYOFFICE 编辑器中的 CSV，附有保存时保留什么的说明](https://filex.sh/shots/csvoffice/csv-edit-1440.4efc379a293d.png) |

| 外壳：每个人进入后看到的界面 | 在这个文件夹中搜索；`⌘K` / `Ctrl K` 把查询交给命令面板 |
|---|---|
| ![filex 外壳](https://filex.sh/shots/driveshell/driveshell-hero-1440.16742e249165.png) | ![在文件夹中搜索](https://filex.sh/shots/driveshell/driveshell-search-1440.119e6bd43905.png) |

| 导航面板：Home（主页）、Shared with me（与我共享）、My shares、Recent（最近）、Starred（已加星标）、Trash（回收站），以及你能访问的存储 | 折叠为图标栏 |
|---|---|
| ![导航面板](https://filex.sh/shots/sidenav/sidenav-expanded-1440.461d5aaaff2a.png) | ![折叠为图标栏](https://filex.sh/shots/sidenav/sidenav-rail-1440.843a2158380d.png) |

| 标签：你自己的，或者你团队的；一个标签会打开带这个标签的每个文件，无论这些文件在哪个文件夹里 | Trash：删除了什么、原本在哪里，以及离清除还剩多久 |
|---|---|
| ![个人标签和团队标签](https://filex.sh/shots/tags/tags-kinds-1440.a9f9fff4d4fd.png) | ![回收站视图](https://filex.sh/shots/sidenav/view-trash-1440.ab3cfb3b01cf.png) |

| Shared with me：其他人授予你访问权限的文件夹，无需挂载说明 | 嵌入在另一个产品的页面中 |
|---|---|
| ![Shared with me](https://filex.sh/shots/sidenav/view-shared-1440.475ec2d8b49f.png) | ![嵌入的 Web 组件](https://filex.sh/shots/sidenav/embed-webcomponent-1440.c8c25d893d24.png) |

| How to connect（如何连接）：各份指南，都根据*你的*部署生成 | API keys（API 密钥）：在资源管理器或嵌入端里签发你自己的密钥（凭某个人的会话或令牌；用一个共享的*应用*令牌做代理的嵌入端没有这个条目） |
|---|---|
| ![How to connect](https://filex.sh/shots/sidenav/connect-1440.7327ccaf3ae3.png) | ![API keys](https://filex.sh/shots/sidenav/apikeys-minted-1440.bae083a0679a.png) |

| 用什么都能访问 filex：S3、SFTP、FTPS、NFS、WebDAV。每条命令都根据*你的*部署生成 |
|---|
| ![连接指南](https://filex.sh/shots/connections-guide.cf9a135724c9.png) |

| filex 未内置的一个存储：在 **Plugins → Storage plugins**（插件 > 存储插件）中以插件形式安装，配置表单由插件自己描述 |
|---|
| ![Plugins](https://filex.sh/shots/admin-plugins.c25fa69cfc7c.png) |

## 快速开始 - 二进制文件

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

## 使用 Compose 或 Helm 自托管

上面的 `docker run` 足够用来试用 filex。要正式部署，[`deploy/`](deploy/) 中有现成的部署方案：

- **[`deploy/compose/`](deploy/compose/)**：Docker Compose：
  - **minimal**：filex + SQLite + 本地磁盘（一个服务，零依赖）。
  - **full**：filex + PostgreSQL + Redis + Caddy（自动 HTTPS），外加可开关的附加组件：**OnlyOffice**、**Drawio** 和一个 **S3 服务器**（Versity S3 Gateway）。用 `.env` 中的 Compose profile 逐个开启或关闭。转换由 [Convert 应用](#应用)负责，而不是边车容器。
- **[`deploy/helm/filex/`](deploy/helm/filex/)**：用于 Kubernetes 的 Helm chart（Deployment + PVC + 可选的 Ingress）。上面的每个附加组件都对应 `values.yaml` 中的一个 `enabled` 开关：一并部署 PostgreSQL / Redis / S3 服务器，或者接入外部的 OnlyOffice / Drawio。

每种部署方式的分步说明见 [docs/INSTALLATION.md](docs/INSTALLATION.md)。

filex 可以运行在独立的主机的根路径上，也可以运行在共用主机的某个路径下（`https://example.com/filex/`）：需要 `FILEX_BASE_PATH` 这一项设置，外加一个传递完整路径的代理。Caddy、nginx 和 Helm 的示例见 [docs/DEPLOYMENT.md → Serving filex under a sub-path](docs/DEPLOYMENT.md#serving-filex-under-a-sub-path)。

## 嵌入到你的应用中

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

**没有需要导入的样式表**：外观已包含在打包产物里，挂载时注入，所以这段代码片段什么都不缺。⚠ 打包工具需要把可选的查看器包（`monaco-editor` 之类）设为外部依赖，[docs/INTEGRATION.md](docs/INTEGRATION.md) 用一行 `rollupOptions.external` 演示了写法；这些导入每一处都加了保护，所以查看器会降级，而不是坏掉。

### 原生 JS / 任意框架
```html
<script type="module" src="https://cdn.jsdelivr.net/npm/@brftech/filex/dist/filex.js"></script>
<filex-explorer api-base="http://localhost:5212" sidenav connections ui-profile="simple"></filex-explorer>
```

`sidenav` 打开导航面板（导航面板默认就是打开的；有这个属性，是为了让宿主页面无论开还是关都能明确写出来），`connections` 给导航面板加上“How to connect”（如何连接）和“API keys”（API 密钥）条目，`ui-profile="simple"` 则预先关闭面向高级用户的界面元素。这三项都是普通的 `config` 键，所以 Vue 和 React 封装用同样的方式设置它们，参见 [docs/INTEGRATION.md](docs/INTEGRATION.md)。

多租户宿主应用通常在服务器端代理 API，为每个请求注入一个**受限令牌**（`root: tenant-folder`），并剥离客户端请求头：这道限制由后端强制执行，而不是由前端组件执行。这样的令牌是 `kind: "app"`，所以导航面板会隐藏属于某一个人的入口：API keys、Recent（最近）、Starred（已加星标）、Shared with me（与我共享）；而 Upload（上传）、各个存储、Trash（回收站）和“How to connect”仍然保留。参见 [docs/INTEGRATION.md](docs/INTEGRATION.md) 和 [docs/MCP.md](docs/MCP.md#token-kinds---user-vs-app)。

⚠ 如果嵌入端所在的页面属于其他源（同级子域名也算在内），并且依靠的是访客自己的 filex **会话 Cookie**（没有令牌），那么读取照旧，但在那个源列入 `FILEX_CORS_ALLOWED_ORIGINS` 之前，它发出的每一项更改都会被拒绝（`403 cross_origin_refused`）；默认值 `*` 并不授予这项许可。Bearer 令牌、用密钥做代理的宿主应用、桌面应用和已安装的 Web 应用则什么都不需要（[docs/CONFIGURATION.md](docs/CONFIGURATION.md#requests-from-other-origins)）。

⚠ 包和服务器要保持同一版本：**`@brftech/filex` 0.54 需要 filex 0.54 服务器**。哪些文件可以打开编辑、输入的上限以及版本行，都来自服务器的 capabilities，包里不留可以回退的副本（[docs/API.md](docs/API.md)）。

## 桌面应用与 CLI

资源管理器还以 **Windows / Linux / macOS 桌面应用**的形式提供，用的就是 Web 界面和嵌入端渲染的那个组件，而不是另做的一个半成品副本：

- **同时使用多个账户**：一条服务器/租户图标栏，各自显示自己的品牌。
- **把文件拖出去**：把所选内容拖到桌面上或拖进其他程序，文件夹和多选内容拖出后就是一个个独立的真实文件和文件夹。已经保留在此电脑上的内容立即就能拖出；其余内容获取一次后就缓存下来（[docs/DESKTOP.md](docs/DESKTOP.md#dragging-files-out)）。
- **Keep on this computer**（保留在此电脑上）：右键点击任意文件夹、文件或整个存储，即可把它镜像到机器上的一个 filex 文件夹下（该文件夹可在 Settings（设置）中移动）；其余内容都保持仅在线，每一行都会标明自己是哪一种（✓ ◐ ⟳ ☁）。“Keep online only”（仅在线保留）会把本地副本移入回收站，或者留在原处（[docs/DESKTOP.md](docs/DESKTOP.md#keeping-folders-on-this-computer)）。
- **文件夹同步**：把一个本地文件夹和一个服务器文件夹配对，只要应用待在系统托盘里，两者就双向保持一致，而且是**实时**的：在浏览器里保存的内容大约一秒就落到磁盘上，本地保存的内容到服务器也一样快（引擎监听服务器的变更流和文件系统，并以每 30 s 一次的完整检查兜底）；两边同时修改时两个版本都保留；传输和获取列表都并行进行；首次运行中断后会从中断处继续；本地回收站保留 30 天；引擎不会把一个不见了的文件夹当成一次批量删除来执行（[docs/SYNC.md](docs/SYNC.md)）。
- **直接打开你本地磁盘上的 Office 文档**：双击一个 `.docx`/`.xlsx`/`.pptx` 文件（或十种 Office 类型中的任意一种，或一个 `.csv` 文件），它就在你的服务器运行的编辑器里打开，这台机器上不必安装 Office。文档如果位于你保留在此电脑上的文件夹里，打开的就是它本身；其余文档则先复制上去，编辑后再写回，覆盖原文件；如果编辑器以另一种格式保存（旧的 `.doc` 会以 `.docx` 返回），则写在原文件旁边（[docs/DESKTOP.md](docs/DESKTOP.md#opening-documents-from-your-computer)）。
- **挂载为驱动器**：Settings 中的一个按钮通过 WebDAV 把服务器挂载为操作系统的驱动器，另一个按钮把它卸载；凭据就是账户自己的令牌，绝不会出现在命令行中。已在 Windows 上实测；macOS 和 Linux 的代码路径已经有了，但尚未验证（[docs/DESKTOP.md](docs/DESKTOP.md#mounting-the-server-as-a-drive)）。
- **⌘K 搜索所有账户**，即图标栏上的每一个账户，结果按账户分组，每个账户一个标记，搜索、下载和拖出都用各账户自己的登录（[docs/SEARCH.md](docs/SEARCH.md)）。
- **通知和账户都在窗口里**：顶栏的末尾和 Web 应用的一样，是**铃铛**（未读数、最新的几条、*Mark all read*（全部标为已读）、完整列表）和**头像**，头像下有 *User settings*（用户设置），也就是 Web 应用自己的设置对话框，**在窗口内**打开；管理员还有 *Admin panel*（管理面板）。点击一条通知，就会在窗口里进入对应的文件夹，并选中该文件。退出登录仍在应用自己的 *Settings → Accounts*（设置 > 账户）中（[docs/DESKTOP.md](docs/DESKTOP.md#notifications-and-your-account)）。
- **说你账户的语言**：窗口、托盘菜单、通知以及每个文件夹下同步引擎的消息，用的都是当前账户的语言；*Settings → Language*（设置 > 语言）改的是账户的语言，所以 Web 应用也随之改变（[docs/DESKTOP.md](docs/DESKTOP.md#language)）。
- **通过浏览器登录**，所以 SSO 和 MFA 的表现与在 Web 上完全一样。
- **自动更新**：悄悄下载，退出时安装；`FILEX_NO_UPDATE=1` 可关闭自动更新。
- **无需安装即可运行**，如果你需要的就是这个：Windows **便携版** `.exe`、Linux AppImage 和 macOS `.zip` 放在哪里，就能从哪里运行。Windows 便携版把所有东西都放在自己旁边的一个 `filex-data` 文件夹里，所以删掉那个文件夹，就不会在不属于你的机器上留下你的任何东西；代价是它不会自动更新。

从 Microsoft Store（Windows 10/11）或 Snap Store（Ubuntu 以及其他带 snapd 的 Linux）**安装**：

<p>
<a href="https://apps.microsoft.com/detail/9PKXDJLVZWXW"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/badges/ms-store-light.svg"><img src="docs/badges/ms-store-dark.svg" alt="从 Microsoft Store 下载" height="52"></picture></a>&nbsp;&nbsp;&nbsp;
<a href="https://snapcraft.io/filex-app"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/badges/snap-store-white.svg"><img src="docs/badges/snap-store-black.svg" alt="从 Snap Store 获取" height="52"></picture></a>
</p>

或者用包管理器：

```bash
brew install brf-tech/filex/filex-app       # macOS 13+ (Apple Silicon), Homebrew tap BRF-Tech/homebrew-filex
sudo snap install filex-app                 # the same snap as the badge above
```

Store 版本（*filex File Manager*）是唯一经过代码签名的 Windows 版本（由 Microsoft 签名），并由 Store 负责更新。桌面应用的 winget 包（`BRFTech.filex-app`）随每个发行版提交，正在等待 winget 审核人员的首次审核，所以 `winget install BRFTech.filex-app` 目前还找不到它。安装程序、便携版 `.exe`、AppImage、`.deb`、`.rpm` 和 `.dmg` 都附在[最新发行版](https://github.com/BRF-Tech/filex/releases/latest)上，尚未经过代码签名，所以运行 Windows 安装程序时会出现 SmartScreen 提示。详见 [docs/DESKTOP.md](docs/DESKTOP.md)。单独安装 CLI：`brew install brf-tech/filex/filex`，在 Windows 上则用 `winget install BRFTech.filex`（[docs/CLI.md](docs/CLI.md)）。

在 Linux 上，`.deb`、`.rpm` 和 AppImage 绝不会脱离 Chromium 的沙箱运行。`.deb` 和 `.rpm` 什么都不需要；在 Ubuntu 23.10 及更高版本上，AppImage 需要一个一次性设置的 AppArmor 配置文件，应用会明确说明并给出操作步骤（[docs/DESKTOP.md](docs/DESKTOP.md#appimage-on-recent-ubuntu)）。Snap 不使用 Chromium 的沙箱运行，而是运行在 snap 的严格隔离（strict confinement）之中，同样什么都不需要（[docs/DESKTOP.md](docs/DESKTOP.md#the-snap-and-the-sandbox)）。

**ARM (arm64)**：提供以下内容（每个发行版都会构建这些产物，并在发布前先在 arm64 机器上运行）：

| | arm64 |
|---|---|
| 服务器 + CLI 二进制文件 | Linux、macOS 和 Windows：`filex-<os>-arm64` 以及 `.tar.gz` / `.zip` 压缩包 |
| Docker 镜像（`ghcr.io/brf-tech/filex`，full 和 slim） | 多架构，`docker pull` 会自行选择 arm64 |
| 桌面应用 - Linux | `filex-desktop-arm64.AppImage`、`filex-desktop-arm64.deb`、`filex-desktop-aarch64.rpm`，以及 Snap Store（`sudo snap install filex-app` 会选择 arm64），自 0.48.1 起 |
| 桌面应用 - Windows on Arm | `filex-desktop-arm64.exe`（安装程序）和 `filex-desktop-portable-arm64.exe`，自 0.48.1 起；应用会自动更新到 arm64 版本 |
| 桌面应用 - macOS | 仅 Apple Silicon（没有 Intel 版本） |
| Homebrew | CLI（`filex`）支持 Apple Silicon 和 Arm 上的 Linux；桌面应用（`filex-app`）支持 Apple Silicon |
| winget | CLI（`BRFTech.filex`）支持 Arm 上的 Windows，winget 会自行选择 arm64 版本 |

在 Arm 机器上，应用的 *Get the desktop app*（获取桌面应用）提示，以及它在 Settings 中的那一份，都会把 arm64 文件排在最前，[filex.sh](https://filex.sh/#downloads) 上的下载列表也会突出显示它，依据是浏览器报告的信息（Chromium 的客户端提示、Firefox 的 `aarch64`）。不报告这一信息的浏览器（Safari、Windows 上的 Firefox）得到的是 x64 文件，arm64 文件就在旁边。

同一个二进制文件也是一个客户端，供服务器、脚本和没有图形界面的机器使用：

```bash
filex client login --url https://files.example.com
filex client upload build/report.pdf docs://ci-artifacts/
filex client mv docs://ci-artifacts/report.pdf archive://2026/   # across storages, waits for the job
filex client run convert convert docs://data/table.csv --param target=xlsx

filex sync add ~/Documents/work docs://work   # the engine the desktop app uses
filex sync run --watch 30s
```

参见 [docs/CLI.md](docs/CLI.md) 和 [docs/SYNC.md](docs/SYNC.md)。驱动这个引擎的程序读取 `filex sync run --json`：每行一个 JSON 事件，每个事件都带一个稳定的代码，以及引擎用账户语言写出的句子；桌面应用显示的正是这些句子（[事件流](docs/SYNC.md#the-event-stream---json)）。

## AI 智能体 / MCP

filex 在 `/api/ai` 提供一个用令牌认证的自动化接口（列出、读取、写入、移动、复制、删除、搜索、共享、打包为 zip），并在 `/api/ai/mcp` 支持 **Model Context Protocol**。智能体还能执行资源管理器自己的操作（跨存储复制、**转换**这类应用操作、操作队列、回收站和版本历史、7z/TAR 压缩包、它自己的链接和文件请求、铃铛、星标、评论，以及它所拥有项目的权限），走的是资源管理器自己的处理程序，所以规则就是资源管理器的规则。管理员密钥能做管理面板所做的事（租户、身份提供方、登录安全、Default apps（默认应用）、webhook、存储），走的是管理面板自己的处理程序。`/api/ai` 和 `/api/files` 由一份 [OpenAPI 3.1 文件](backend/internal/api/openapi.json)描述：

```bash
claude mcp add filex --transport http https://files.example.com/api/ai/mcp \
  --header "Authorization: Bearer <api-token>"
```

API 密钥的权限按动词划分（`read`、`write`、`delete`，另有 `mcp` 和 `admin`），密钥还可以**限定在单个文件夹内**，受界面所用的同一套 RBAC 授权和角色约束，并带有每个密钥各自的身份，所以审计日志、共享和在线状态都会显示是*谁*（哪个集成）做了什么。这些动词在**密钥能到达的每一个入口**上都生效：`/api/ai`、MCP 工具、资源管理器自己的路由（因此也包括 `filex client` 和嵌入端）、WebDAV、SFTP、FTPS，以及由它签发的 S3 密钥和 NFS 导出。有些事密钥永远做不了：安装插件（智能体会**留下一个安装请求**，由管理员在管理面板中批准），以及让某个人成为管理员。密钥必须至少指明一项权限（空列表会被拒绝，绝不会被当作“全部”），而且**它发放出去的东西，权限永远不会超出密钥自身**：通过只读密钥或限定在文件夹内的密钥申请 API 令牌、S3 访问密钥、NFS 导出或 SSH 密钥时，如果要的是更多的动词、自身根目录之外的根目录，或者更长的有效期，就会被拒绝，返回的 `403 token_ceiling` 会指明是哪一项过宽。智能体的**移动从不覆盖**：如果项目要用的名称已被占用，它会落在旁边，改用一个未被占用的名称（`report-copy.txt`），与界面里的移动完全一样，而且答复会指明它实际落到的路径。这个接口也认得**加密文件夹**：每一行都会说明是否已加密，密文绝不会当作文件本身交出去（`409 E2E_ENCRYPTED`），向加密文件夹写入明文会被拒绝，除非调用方表明确实要这样做（`allow_plaintext`）（[docs/MCP.md](docs/MCP.md#encrypted-folders-and-fxe)）。

已经在智能体磁盘上的大文件，永远塞不进一次工具调用：那样它的字节就得从模型的上下文里走一遍。**上传票据**解决这个问题：一次经过授权的调用固定好目标位置，并返回一个短时有效、只能用一次、**无需凭据**的 URL，所以就连没有 filex 令牌的智能体，也能用 `curl -T bigfile <url>` 完成传输。详见 [docs/MCP.md](docs/MCP.md)。

filex 拒绝时，在每一个入口上都用同一种方式说明：一个供程序据以分支的稳定 `error` 代码，以及 `message` 中服务器用读者的语言写出的句子，可以原样转述（[docs/API-ERRORS.md](docs/API-ERRORS.md)）。智能体创建的链接在答复里附带自己的下载命令，即由服务器写出的 `curl` 和 PowerShell 命令行（[docs/SHARING.md](docs/SHARING.md)）。

## 应用

**存储插件**教会 filex 一种它从没听说过的后端。**应用**则教给它一件*可以对文件做的事*（签署、转换、发给外部的人），而且它有意被设计成另一种插件：一个**在 filex 内部、在沙箱中运行的 WebAssembly 模块**，凡是没有授予它的东西，沙箱一概不给。没有文件系统，没有网络，没有环境变量，没有你服务器上的任何程序：只有它的清单要求的宿主函数，每一个都会在安装任何东西之前用直白的文字展示给管理员；也只有运行它的人实际选中的文件。应用可能想用的重量级引擎（ffmpeg、ImageMagick、Ghostscript、poppler、rsvg）是服务器自己的，按引擎提供，每个引擎一项权限；办公文档则交给你连接的 ONLYOFFICE Document Server，由它充当办公引擎：filex 不运行 LibreOffice。

应用还可以有（或者只有）**自带的界面**：它的作者编写的 HTML、CSS 和 JavaScript，一个面向某种格式的编辑器或查看器。filex 从你批准过的包（以其 SHA-256 固定）中提供这个界面，放在一个**沙箱 iframe** 里：一个读不到 filex 的会话、Cookie 和页面的不透明源，一份 filex 根据应用的授权写出的内容策略（没有连接、没有存储、没有表单、没有弹出窗口），以及一条经过校验的消息通道，filex 通过这条通道只把用这个界面打开的文件交给它，并把内容存回这些文件：存为一个新版本，或者一份草稿。这个界面可以把自己的文件类型加进 **New document**（新建文档），可以在编辑器标签页中打开，也可以把一个文件交给你留存，每一次都要你允许。⚠ 浏览器无法完全阻止页面向外发送数据（WebRTC 不理会内容策略；Chrome 允许 filex 把它关掉，在 Firefox 中 filex 只能把它从页面里拿掉：是安全带，不是墙），所以安装审核会明确说明：**对于带界面的应用，你愿意把在其中打开的文件托付给它的作者到什么程度，就信任它到什么程度。**

其他一切在哪里，应用添加的东西就在哪里：文件菜单里的行、filex 为它绘制的界面或它自带的界面、和复制操作排在同一个队列里的任务（有进度，有 **Cancel**（取消），结果和其他任何写入一样保留版本、经过病毒扫描并编入索引）、文件详细信息中的一个区块、导航里 **Apps**（应用）下面的一个主界面，以及在它需要某个没有账户的人时用的链接，这个链接就是一个普通的**共享**：在同一个列表里，受同样的 PIN 锁定和过期策略约束，和其他每个链接一样可以由你撤销。提出要求的应用还会每小时被唤醒一次，去做自己的定时工作：签名请求到截止时间自行关闭，并发出你要求的提醒。

并非每个应用都运行代码。**语言包**是一份字符串清单，别无其他：它只凭这份清单就能安装（没有模块，没有 Go，没有发行版），从不启动运行时，并把自己的语言加到资源管理器、管理面板、公开页面和服务器写出的文字里。**Plugins → Apps**（插件 > 应用）把它列为 *Language pack*（语言包），并标出它对当前运行版本的覆盖率，缺少的内容都以英语显示。西班牙语、德语和法语作为示例提供，`BRF-Tech/filex-lang-template` 则带着译者从导出一路走到安装。

有四个应用随 filex 一同提供，都是公开仓库，你可以安装、阅读和分叉。前两个是模块；后两个只有界面，没有任何在服务器上运行的内容：

| 应用 | 添加的内容 |
|---|---|
| **[e-Signature](https://github.com/BRF-Tech/filex-sign)** - `BRF-Tech/filex-sign` | PDF 上的 **Sign…**（签署）、**Request signatures…**（请求签名）、**Sign / Fill**（签署 / 填写）和 **Verify**（验证），而且仅限 PDF（办公文档先用 **Convert** 转成 PDF）。一次请求就是一个简短的向导：谁来签署（这个 filex 上的人在 filex 内部签署；其他任何人按姓名或电子邮件指定，会收到一个**私密链接**，除非你另行设定，否则要凭 PIN 才能打开），按什么顺序签署，为填写框命名并分配给每位签署人，再把填写框放置到页面上；请求开放多久，期间文件是否**冻结**，以及结束时是否生成一份**审计跟踪 PDF**。得到的是一份带 PAdES 签名的 PDF，而且**已认证并盖章**：第一个签名对文档进行认证，因此后续的签名只能填写和签署；最后一个签名完成时，**filex 自己为整个文件盖章**，用的是实例自己的印章，并加以锁定，这样此后的任何更改都会被报告为不允许。**正是这些盖章后的字节的 SHA-256**、印章的指纹以及核对方法，会发给发起人以及内部和外部的每一位签署人，并写入审计跟踪。还可以选择让已签署的文件**在 filex 中保持锁定**，直到管理员解除锁定。**签名密钥从不离开服务器**：实例自己的证书颁发机构（或你导入的证书颁发机构）为每位签署人颁发一张证书，生成签名的密钥几秒后即销毁；印章的密钥是唯一的例外，由宿主持有，从不交出。 |
| **[Convert](https://github.com/BRF-Tech/filex-convert)** - `BRF-Tech/filex-convert` | 任何文件上的 **Convert…**（转换）：图片、视频、音频、文档、电子书、压缩包、数据、字幕和字体。先从按类别分组的按钮中选定目标格式，接着只列出与该目标格式相关的设置，最后确认。大多数转换在沙箱内用纯 Go 运行；其余的使用服务器上的引擎，前提是这些引擎已经安装；某个目标格式所需的引擎缺失时，该目标格式会明确说明，而不是悄悄消失。 |
| **[filextext](https://github.com/BRF-Tech/filextext-app)** - `BRF-Tech/filextext-app` | 装在单个 `.fxtxt` 文件里的**端到端加密的文本工作区**：左侧是页面和文件夹，顶部是标签页，中间是 AFFiNE 的 BlockSuite 编辑器（标题、列表、待办事项、代码、表格、图片、页面之间的链接、Markdown 导入和导出）。工作区**在你的浏览器中**加密，用的密钥和恢复密钥与 filex 的[加密文件夹](docs/E2E-ENCRYPTION.md)相同；filex 存储的是密文，从不会看到密码，也看不到正文的任何一个字。`.fxtxt` 文件在这个工作区里打开，代替预览；**New document**（新建文档）中会多出 *Encrypted workspace (.fxtxt)*（加密工作区）。 |
| **[draw.io](https://github.com/BRF-Tech/filex-drawio)** - `BRF-Tech/filex-drawio` | **draw.io** 图表编辑器，就在 filex 内：`.drawio` 和 `.dio` 文件在这个编辑器里打开，代替预览；**Save**（保存）会写入一个新版本；**New document** 中会多出 *draw.io diagram*（draw.io 图表）。draw.io 自己的文件从应用的包（以其 SHA-256 固定）中提供；它不访问包之外的任何东西。 |

**从 GitHub 安装**：在 *Admin → Plugins → **Apps** → **Install an app** → GitHub repository*（管理 > 插件 > 应用 > 安装应用 > GitHub 仓库）中输入 `BRF-Tech/filex-sign` 和发行版标签。filex 读取仓库的 `filex-app.json`，下载其中指明的模块（或界面的包），SHA-256 不匹配就拒绝，然后停在**权限审核**这一步。在你读完每一项权限并勾选 *I understand*（我已了解）之前，什么都不会安装；授权不多不少，就是这份列表，要求更多权限的升级会再次停在审核这一步。`FILEX_PLUGIN_TRUSTED_KEYS` 强制要求使用已签名的模块（从 GitHub 安装不带签名，所以在这样的实例上，改为连同签名一起上传模块）。每一次下载（应用、应用的更新检查、存储插件）都只访问公网地址，在 DNS 解析之后判定，每次重定向时都重新判定：要从你自己网络里的服务器安装，就上传文件。在演示模式下，应用是关闭的。API 密钥（智能体、脚本、CLI）不能安装应用：它会**留下一个请求**，filex 冻结将要安装的字节和权限，再由管理员在 **Plugins → Install requests**（插件 > 安装请求）下批准。

**谁可以使用。** 应用可以声明**自己的权限**（一个签署应用规定 *Request signatures*（请求签名）需要一项这样的权限，而签署发给你的内容则不需要任何权限），你按角色、按人发放这些权限，和 filex 自己的权限一样；某个人无权执行的操作不会出现在这个人的菜单里，即使请求执行也会被拒绝（[应用权限](docs/APP-PLUGINS.md#app-permissions)）。

**没有什么会自动更新。** filex 每天一次（点击 **Check for updates**（检查更新）时也会）向每个应用的来源（应用的 GitHub 发行版、语言包的分支，或者安装时所用的清单地址）询问有没有这个 filex 能运行的较新版本，并告诉你：新版本在 *Update available*（有可用更新）下等待（如果它要求更多权限，则在 *Needs approval*（需要批准）下），直到管理员审核过它改动的内容（权限、模块、界面文件、说明）并批准；此后每个人都使用这个版本。**Back to *version***（回到某个版本）会把新版本替换掉的那个版本换回来。存储插件也可以用同样的方式跟随一个来源。应用会说明自己适用于哪些 filex 版本（应用清单中的 `"filex": ">=0.47.0"`），超出这个范围，filex 不会安装它。

**运维指南**：[docs/APP-PLUGINS.md](docs/APP-PLUGINS.md)，涵盖安装和管理应用、一轮签署的全过程、转换器、定时唤醒，以及应用的公开链接由什么保护。**编写应用**（标准 Go，`GOOS=wasip1`，附带测试工具包）：从模板仓库 [BRF-Tech/filex-app-template](https://github.com/BRF-Tech/filex-app-template) 和 [docs/PLUGIN-KIT.md](docs/PLUGIN-KIT.md) 开始；通信契约：[docs/APP-PLUGINS-API.md](docs/APP-PLUGINS-API.md)。另一种插件，即存储后端：[docs/PLUGINS.md](docs/PLUGINS.md)。

## 功能

- **多存储**：同时挂载多个存储（本地磁盘、S3、FTP、SFTP、WebDAV、SMB/NAS）；每个存储显示为一个顶层文件夹。每个存储还有一个永远不变的地址：存储的名称是 WebDAV、SFTP、NFS 和 S3 API 上的第一个路径段，所以给存储改名，就等于给它换了地址；而按 **uid** 写的挂载配置经得起每一次改名。**在一个存储中复制或剪切，在另一个存储中粘贴**：filex 在两个驱动之间流式传输文件树，保留每个文件的时间戳，并且只在副本校验通过后才删除原件。存储一旦不可用，几秒内就会报出来；只有毫无动静才算超时，仍在推进的传输绝不会超时（S3、WebDAV、FTP、SFTP 和 SMB：[docs/STORAGE.md](docs/STORAGE.md#when-the-store-is-down)）。存储无法给出答复的条目（既不是“在这里”，也不是“未找到”）会保留下来，标上 **!** 和存储自己的答复；在资源管理器、REST API 和智能体 API、共享以及编辑器中（文件协议不读取这个标记），都不会对它做任何操作，直到存储重新给出答复（[PLUGINS.md](docs/PLUGINS.md#an-entry-your-stat-cannot-answer-for)）。
- **把文件拖到桌面**：在桌面应用中，把所选内容拖进文件资源管理器/访达或其他程序，落下的是一个个独立的真实文件和文件夹，而不是一个压缩包；在浏览器中，单个文件也能以同样的方式拖出，在管理端也可以，靠的是一个只对应单个文件、有效一分钟、只能用一次的链接（[docs/DESKTOP.md](docs/DESKTOP.md#dragging-files-out)）。
- **存储插件**：filex 从没听说过的存储，就是一个你从管理面板安装的**独立程序**：它描述自己的配置表单，filex 用一个小型 HTTP/JSON 协议与它通信，此后它的驱动就和任何内置驱动没有两样。用哪种语言写都行；用 Go SDK 的话，只要三个方法。filex 会**探测插件声称的每一项能力**（安装时探测一次，你保存一个使用该插件的存储时，再针对你填写的配置探测一次），并拒绝说到做不到的插件，因为半能用的驱动造成的故障，看起来会像是 filex 坏了。升级会就地替换二进制文件，新的启动不起来就回滚；每次启动都会重新校验二进制文件的哈希值和签名，每个插件还保留一份记录自身启动、故障和未能给出答复的条目的**日志**，在 *Actions → Log*（操作 > 日志）中查看（[docs/PLUGINS.md](docs/PLUGINS.md)）。
- **应用**：第二种插件，可以是**沙箱化的 WebAssembly** 模块，可以是**放在沙箱 iframe 里的自带界面**（不能建立任何连接，也访问不到 filex 的会话；浏览器无法保证什么，见[应用](#应用)），也可以两者兼有；应用向文件菜单添加 *Request signatures…*（请求签名）、*Convert…*（转换）这样的操作，还添加 filex 为它绘制的界面、文件详细信息中的一个区块、导航中 **Apps**（应用）下的一个主界面，以及外部参与者无需账户就能打开的链接。应用从 GitHub 仓库安装，安装时要经过**权限审核**，得到的正好是你批准的那些，别的一概没有：没有文件系统，没有网络，没有你服务器上的任何程序；重量级引擎（ffmpeg、ImageMagick、…）用的是服务器自己的，办公文档经由你连接的 ONLYOFFICE 处理，都是一项权限一项权限地提供。filex 绘制的界面不管是谁写的，都遵守 filex 的规则：每个选项都摆在明处，而不是收在下拉菜单里，没有什么藏在“高级”后面，每一步只问一个问题。应用发给外部签署人的链接是一个普通的**共享**，所以它和别的都列在同一个列表里，你在那里查看和撤销它；它的权限永远不会超过它的创建者：从这个链接发起的任务，要过的检查和在 filex 内部发起的任务一样（你关闭的操作依然关闭，创建者对文档的访问权限会重新读取一遍）；创建者的账户一停用，链接就失效，直到账户重新启用。应用也可以**自带界面**：HTML 和 JavaScript 由 filex 从应用已获批准的包中提供，放进一个沙箱 iframe，这个 iframe 的策略不允许任何连接、任何存储和任何 Cookie，界面只通过一条经过校验的通道与 filex 通信；在服务器上什么都不需要的编辑器（draw.io、filextext）就是一个根本没有模块的应用（[应用自带的界面](docs/APP-PLUGINS.md#an-apps-own-interface)，SDK `@brftech/filex-app-ui`）。你授予了 `schedule` 的应用每小时被唤醒一次，在它自己选定的那一分钟做自己的工作，作为队列里的一个普通任务运行。没有什么会自动更新：filex 每天检查每个应用的来源，有新版本时会明确说明；管理员审核新版本改了什么，然后批准，大家用的都是获批的那个版本，而 **Back to *version***（回到某个版本）可以撤销一次批准。应用会说明自己适用于哪些 filex 版本。API 密钥绝不会安装应用，而是留下一个由管理员批准的**安装请求**；应用还可以声明**自己的权限**，由你按角色、按人发放（[应用权限](docs/APP-PLUGINS.md#app-permissions)）。应用可以为 filex 自己不绘制的文件类型**绘制缩略图**（交到它手里的只有一个文件的字节，别无其他），而 **Default apps**（默认应用）按文件类型决定由哪个应用打开、由哪个应用绘制缩略图，以及按什么顺序；每个人从仍然开启的、能打开这类文件的应用里挑选，*Always use this app*（始终使用此应用）记在各自的账户上：浏览器、桌面应用和嵌入端共用一个选择（[默认应用](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail)）。有四个应用以公开仓库的形式发布：**e-Signature**、**Convert**、**filextext** 和 **draw.io**（[应用](#应用)、[docs/APP-PLUGINS.md](docs/APP-PLUGINS.md#the-apps-that-ship-alongside-filex)、[编写一个](docs/PLUGIN-KIT.md)）。
- **e-Signature**（[`BRF-Tech/filex-sign`](https://github.com/BRF-Tech/filex-sign)，一个应用）：自己签署一份 PDF，或者请别人签署。这个 filex 上的人在 filex 内部签署，从一条会打开对应界面的通知进入；其他任何人拿到的是一个私密链接，默认设有 PIN，这个 PIN 由 filex 替你保管（见*共享*）。填写框**先定义**（名称、归谁、是否必填、日期的格式），**之后再放置到页面上**，两个问题，分在两个界面上。文档在等待签署期间可以对每个人**冻结**，管理员也不例外；提醒和截止时间都自动处理；发起人在文件的详细信息里和应用的主界面上跟进每一位签署人；最终得到一份带 PAdES 签名的 PDF，由第一个签名**认证**，并在最后一个签名之后**由 filex 盖章**，所以此后所做的任何更改，阅读器都会报告为不允许的更改。**盖章后的字节的 SHA-256** 和印章的指纹会发给发起人和每一位签署人，你索取时还有一份**审计跟踪 PDF**，每位签署人都有一份回执，另有一个选项可以让签署完成的文件保持锁定，直到管理员解除锁定。**Verify**（验证）会对任何已签名的 PDF 给出报告：每一个签名、认证、印章，以及这是不是当初发出的哈希值所对应的那个文件。签名密钥从不离开服务器：实例自己的证书颁发机构，或者你导入的证书颁发机构，为每位签署人颁发一张证书，而生成签名的密钥几秒后即销毁（[docs/APP-PLUGINS.md](docs/APP-PLUGINS.md#signing-documents-end-to-end)）。
- **Convert，以应用的形式**（[`BRF-Tech/filex-convert`](https://github.com/BRF-Tech/filex-convert)）：对任何文件或所选内容都可以用 *Convert…*：图片、视频、音频、文档、电子书、压缩包、数据、字幕和字体。目标格式是它所属类别下的一个按钮，接着只有它用得上的设置，最后确认；大多数转换路径在沙箱内用纯 Go 运行，其余的走服务器的引擎，所需引擎缺失的目标格式会如实列出，而不是悄悄消失（[docs/APP-PLUGINS.md](docs/APP-PLUGINS.md#converting-files)）。（旧的 iframe 转换器边车容器已在 0.48 中移除。）
- **协议网关**：同一棵文件树可以被当作 **S3**（SigV4；aws-cli、rclone、restic、mc、s3fs）、**SFTP**（OpenSSH、WinSCP、FileZilla、sshfs）、**FTPS**（显式 TLS，给只会 FTP 的设备用；把你反向代理的自动续期证书交给它，证书一变就会重新读取）、**NFSv3**（LAN 上的 NAS 客户端、媒体播放器）和 **WebDAV** 来访问，每种协议都有自己的凭据，可以单独撤销，而且全都和界面一样，用同样的权限、同一个回收站和同样的配额（[docs/PROTOCOLS.md](docs/PROTOCOLS.md)）。
- **`filex mount`**：通过普通的 HTTPS 把远程 filex 服务器挂载到一个文件夹上：在 Linux 上是一个文件夹，**在 Windows 上是一个盘符**（`filex mount Z:`，需要免费的 [WinFsp](https://winfsp.dev)）。这不是同步：除了一个有上限的读缓存，什么都不复制，所以它能从十万个文件中打开一个，而不必下载其余的。
- **实时协作**：带实时头像 + 焦点的在线状态栏，文件变更通过 WebSocket 即时更新，并可回退到轮询。单次写入一落地就会通告；突发的一连串写入（解压一个 zip、上传一个文件夹、NFS 客户端一块接一块地写）会合并成每个时间窗口一帧，这样文件夹既保持实时，又不会淹没页面（[docs/REALTIME.md](docs/REALTIME.md)）。
- **像表格一样工作的列表**：调整列宽、隐藏某一列、把某一列拖到新位置；空间不够时，表格会横向滚动，而不是丢掉一列，操作列始终固定在右侧。按名称、类型、日期或大小排序，升序降序均可，而且**网格和列表遵循同一种排序**：在这个发行版之前，“按大小排序”只对其中一种视图成立，一切换视图，行的顺序就在你眼前变了。按日期排序时，三种视图都把行归到 **Today · Yesterday · This week · This month**（今天、昨天、本周、本月）之下，接着逐月分组，按的是**你的**时区，而不是浏览器的时区。
- **文件夹记得你离开时的样子**：可选，在用户设置中开启。凡是你实际设置过的文件夹，视图模式和排序都**按人保存在服务器上**，所以会跟着你到另一台机器、到桌面应用，也绝不会串到其他查看同一文件夹的人那里。默认关闭，这时你最后一次的选择就在所有地方生效（[docs/INTEGRATION.md](docs/INTEGRATION.md)）。
- **文件归谁所有**：每个节点都记着自己的所有者，列表里有 **Owner**（所有者）列，筛选栏里有 **Owner** 条目，配额计在所有者名下，而不是计在最后动过这个文件的人名下。
- **支持所有常见格式的压缩包**：创建、打开和解压 ZIP、7z、TAR 及其 gzip/bzip2/xz 形式（服务器的 7-Zip 支持时还包括 RAR），ZIP 和 7z 可设密码，作为带进度的后台任务运行。压缩包内的链接和设备文件，在写入任何内容之前就会被拒绝；大小和条目数的限制会把压缩包炸弹拦在上限处（[docs/ARCHIVES.md](docs/ARCHIVES.md)）。由 Alex（@ahjephson）贡献。
- **把所选内容一并带走**：选中多个文件和文件夹，**Download**（下载）就把它们打包成一个压缩包，边生成边流式传输：不往你的存储里写临时文件，标签页里不缓冲任何内容，一个 700 MB 的压缩包占用服务器不到一兆字节的内存。**Move to**（移动到）和 **Copy to**（复制到）会打开一个横跨所有存储的文件夹选择器，并拒绝你不能写入的目标位置：这是在服务器端执行的，而不只是在对话框里。
- **新建文档**：从 **+ New**（新建）菜单创建 Word、Excel、PowerPoint 或 OpenDocument 文件，也可以是任意文本或代码格式的文件：起个名字（任何名字都行，`LICENSE`、`Makefile` 或 `test.conf` 也可以），选择存放位置，文件就在负责处理它的编辑器中打开。模板是真实、最小、有效的文档，编译在二进制文件里，所以在完全没有办公套件的实例上也能用；这个部署事后打不开的类型，一开始就不会提供，对话框会说明原因。新文档在首次保存之前是**草稿**：在你按下 Save（保存）之前，文件夹里什么都不会出现（这期间名字若被占用，会询问是否改用 `report (2).txt`，绝不替换），关闭它时会询问 *Save to disk / Keep in Drafts / Discard*（保存到磁盘、保留在草稿中、放弃），导航面板里的 **Drafts**（草稿）留着你还没做完的草稿，其他人都看不到。默认每人 50 个，在管理面板的 Protection（防护）页面上设置（[docs/ONLYOFFICE.md](docs/ONLYOFFICE.md#drafts-nothing-is-in-the-folder-until-you-save)）。
- **RBAC + 项目级权限**：角色（Administrator（管理员）、User（用户）、Viewer（查看者）和自定义角色，每个角色都是一份权限列表：[docs/PERMISSIONS.md](docs/PERMISSIONS.md)）；带继承的按文件/文件夹授权，位于 **Admin → Folder access**（管理 > 文件夹访问权限）下；**群组**，为其中每个人持有文件夹访问权限和一个角色，成员手动添加，或者与登录带来的群组保持一致（[docs/GROUPS.md](docs/GROUPS.md)）；通过电子邮件（SMTP）发送的共享邀请；遵循授权的搜索和列表。**Shared with me**（与我共享）从接收者这一侧回答反过来的问题：其他人授予了你什么，以及哪些存储你只能通过授权访问。
- **外壳**：一套布局，运维人员和最终用户共用，管理端、桌面应用和每个嵌入端都是这一套：一条占满整个宽度的顶栏，左端是折叠控件和产品标志；一个**搜索框**，它的 ⌘K / Ctrl+K 小按钮把查询交给命令面板（搜索框搜的是当前文件夹；“所有位置”、已保存的搜索和命令都在命令面板里）；一个作为主要操作的 **+ New** 菜单（上传文件 · 新建文件夹 · **新建文档** · 请求文件）；面包屑下方的 **Type · Owner · Modified · Size**（类型、所有者、修改时间、大小）筛选栏；网格视图中的 **Folders**（文件夹）和 **Files**（文件）两个带标题的分区；一个信息面板，分为 **Details**（详细信息），其中有“People with access”（有权访问的人）和一行共享链接，以及 **Activity**（活动），其中有版本历史和评论；还有导航下方的**存储用量行**。主题、配色、语言、密度、时区、起始页和通知开关都在**用户设置**里，从头像进入，而且 Web 应用把你的主题、配色、密度和语言保存在你的**账户**上，而不是浏览器里，所以换到下一个浏览器时，它们已经在那里等你；快捷键编辑器和 *Restart the tour*（重新开始导览）也在同一个菜单里。构建产物里什么都没有移除：嵌入端没有设置对话框，但留着一个“⋯”菜单，这些东西仍然在里面（[docs/INTEGRATION.md](docs/INTEGRATION.md)）。
- **主页，就在外壳之内**：每个人进入后首先看到的视图，管理员也不例外：你的存储、你最近打开的内容和你加了星标的内容，以卡片的形式显示在内容区，用的是和看文件时同样的导航面板、同样的顶栏。在 Home（主页）和文件夹之间切换，变的只有内容区，别的都不变。运维人员若更愿意进入后看到管理仪表盘，就在自己的个人资料设置中选它。
- **导航面板**：作为主要操作的 **+ New** 菜单；目的地 Home / My files / Shared with me / **My shares** / Recent / Starred / **Drafts** / Trash（主页、我的文件、与我共享、我的共享、最近、已加星标、草稿、回收站）；你能看到的存储，**按你自己的顺序**排列（拖动某一行，或者从该行的菜单里选 Move up / Move down / Sort by name（上移、下移、按名称排序）；顺序保存在你的账户上），否则按管理员在 Storages（存储）页面上设定的顺序排列（[docs/STORAGE.md](docs/STORAGE.md#ordering-storages)）；某个已安装的应用有主界面时出现的 **Apps**（应用）分区；以及 **How to connect**（如何连接）和 **API keys**（API 密钥）：各协议的指南和自助式令牌管理器，从资源管理器内部打开，这样嵌入端的用户就能自己签发 WebDAV/FTPS/`filex mount` 所需的凭据，而不必去找管理员。可从顶栏折叠为图标栏（按浏览器记住），宽度低于 560px 时是抽屉，而不是一列。在 Web 应用、桌面应用和每个嵌入端中默认开启；`uiProfile: 'simple'` 还会关闭标签页栏、分栏、画廊视图模式和“How to connect”入口，但不会把其中任何一项从构建产物中移除（[docs/INTEGRATION.md](docs/INTEGRATION.md)）。
- **共享**：带 PIN、过期时间和下载次数上限的公开链接，受管理员设定的**链接最长有效期**约束（默认 7 天：对话框只提供服务器会保留的选项）；文件夹链接以 ZIP 流式传输（有缓存，大小上限以内的会预热，一周后清理）；**文件请求**上传链接，用来接收他人上传进来的文件；兼容 ShareX 的上传端点。**My shares** 列出你创建的链接（每个人都可以用，而不只是管理员），并带有 *Copy link*（复制链接）、*Copy PIN*（复制 PIN）和 *Revoke*（撤销）：链接的 PIN 加密封存在守护这个链接的哈希值旁边，所以有人再次需要 PIN 时，创建者或管理员可以把它读回来，每一次读取都会写入审计日志。输错五次 PIN，任何公开链接都会关闭十分钟。下载链接、文件请求和应用的页面是**同一个带品牌的公开页面**：你的实例的名称、标志和颜色，一个 PIN 验证页，一套过期规则和一个语言选择器（[docs/SHARING.md](docs/SHARING.md)）。列出的每个链接都标明自己的状态（有效、已过期、已用完或已撤销），新链接附带自己的 `curl` 和 PowerShell 下载命令。从 Share（共享）对话框用电子邮件发出的链接，邮件由服务器根据链接本身写出，用每位收件人各自的语言，而且绝不包含链接的 PIN（[通过电子邮件发送链接](docs/SHARING.md#emailing-a-link)）。
- **桌面应用 + 文件夹同步**：Windows/Linux/macOS 应用，常驻系统托盘做双向同步，支持**选择性同步**（右键点击 → *Keep on this computer*（保留在此电脑上），每个账户一个根文件夹，其余仅在线），可同时使用多个账户，在服务器的编辑器中**打开你本地磁盘上的 Office 文档**，自动更新（macOS：未签名的构建版本，签名之前靠重新下载来更新），使用当前账户的语言。每个文档在**自己的窗口**中打开（窗口标题是文件名），窗口是**无边框**的，用的是应用自己的控件（macOS 上是原生的红绿灯按钮），**Settings → Open files with**（设置 > 打开文件的方式）用来选择单击还是双击打开（[docs/DESKTOP.md](docs/DESKTOP.md)、[docs/SYNC.md](docs/SYNC.md)）。
- **回收站与版本历史**：删除后在保留期限内可以恢复，写入会保留快照；两者都存放在你已经挂载的存储里（[docs/TRASH-VERSIONING.md](docs/TRASH-VERSIONING.md)）。回收站可以分页浏览其中的全部内容，*Empty trash*（清空回收站）会先显示服务器统计的、将要删除的确切数量和大小。
- **写入防护**：可选的 ClamAV 病毒扫描，覆盖每一个写入的文件（内置编辑器写入的也算，存储同步在后端发现的、并非经由 filex 进来的文件也算），扫描器通过本地二进制文件或网络上的 clamd 容器调用；再加上回收站/版本保留，都归在同一个管理入口之下。开关、扫描器的模式和地址、大小上限以及针对编辑器保存的扫描时间窗口，都放在 **Settings → Protection**（设置 > 防护）中；`FILEX_CLAMAV*` 变量在首次启动时为它们填入初始值，之后就退到一边（扫描器的二进制文件路径有意只留在环境变量里：它是这台服务器要执行的命令）（[docs/PROTECTION.md](docs/PROTECTION.md)）。
- **端到端加密文件夹**：在客户端用 WebCrypto 加密；服务器存储的是密文，从不接收密钥。文件夹有一个**级别**：仅内容（默认级别：WebDAV、CLI 和桌面同步仍照常使用其中的名称）或**内容和名称**（AES-SIV，因此服务器不保留任何可读的名称）；之后可以在文件夹的 **Encryption settings**（加密设置）中提高级别，过程可续传，文件夹的密码也在那里修改。第三个级别，即**保险库**（vault），连文件树的形状也一并隐藏：服务器只存储大小相同的数据包和一份加密索引，同一时间只有一个人在服务器持有的锁下写入；它已经实现，默认关闭（`FILEX_E2E_VAULT`），Web 应用、桌面应用、`filex decrypt` 和 `filex vault mount` 都能打开它（[保险库的格式](docs/E2E-VAULT-FORMAT.md)）。你已有的文件夹会**就地加密**，超过 200 MB 的文件也不例外；**任何单个文件都可以单独加密**（成为一个自包含的 `.fxe`，带有自己的密码和恢复密钥）；任意大小的文件都采用流式加密；已解锁的文件夹下载下来是一个在浏览器中生成的**解密后的 zip**；`filex decrypt` 在你自己的机器上打开下载下来的文件夹或 `.fxe`，**`filex encrypt`** 则把磁盘上的文件夹做成加密文件夹，或者把服务器上的文件夹就地加密：适用于大到标签页处理不了的文件夹，可续传，密钥在你的机器上生成（[docs/CLI.md](docs/CLI.md#filex-encrypt---make-a-folder-an-encrypted-folder)）。每个文件夹都有一个**恢复密钥**，只显示一次，这样忘记密码并不必然意味着数据丢失；运维人员可以选择启用**密钥托管**（在安装时启用，或之后在运行中的实例上采用；它不会自行作用于已有的文件夹，但解锁时会让这些文件夹的所有者自己选择），动用密钥托管时会通知文件夹的所有者（[docs/E2E-ENCRYPTION.md](docs/E2E-ENCRYPTION.md)）。**谁可以加密**由组织自己决定：平台运维人员按租户设置的开关，租户策略（关闭、仅限管理员、角色允许的每个人，或者**经管理员批准之后**：一个带理由的请求，获批后只对一个人、一个文件夹和一种加密类型有效，而且只能用一次），以及 `files.encrypt` 权限，每一个可能产生新的加密内容的入口都会检查这项权限，复制也不例外（[docs/E2E-ENCRYPTION.md](docs/E2E-ENCRYPTION.md#who-may-encrypt)）。
- **原生多租户**：平台方/租户模式，在一个实例上按租户隔离。每个租户有一个 **realm**（领域），也就是它的登录名，创建时给定，之后不再更改；所以登录时靠租户自己的地址（网页、WebDAV 的 `Host`、FTPS 证书名称）指明所属租户，或者靠 realm：登录表单上的 **Realm** 字段，SFTP 上的 `realm/name`。账户查找从不越出租户；在平台的页面上输入的 realm，如果所属租户有自己的地址，就凭一张一次性、60 秒有效的票据**移交**到那个地址（[docs/MULTI-TENANCY.md](docs/MULTI-TENANCY.md)、[realm](docs/MULTI-TENANCY.md#realms-which-tenant-a-sign-in-is-for)）。租户在 **Admin → Tenants**（管理 > 租户）和 **My tenant**（我的租户）中自己管理自己：绑定到一个或多个租户的登录提供方、租户自己的 OIDC 和 LDAP、每个租户一个平台子域名，以及经 CNAME 验证的自有域名，证书由代理颁发、由 filex 自己颁发（ACME），或者使用租户自己的证书（[docs/TENANT-ADMIN.md](docs/TENANT-ADMIN.md)）。
- **一切驱动皆可插拔**：存储、认证、数据库、队列驱动通过环境变量按需启用（`FILEX_AUTH_DRIVERS=local,oidc`、`FILEX_QUEUE_DRIVER=postgres`、…）；操作系统登录（`windows`、`pam`）是例外，要等它的测试通过后，再从管理面板中开启。
- **OIDC SSO 优先**：可选择自动重定向到你的 IdP，同时保留应急用的本地登录（`?local=1`）；管理员角色在每次登录时都以一个 IdP 群组为准。
- **LDAP / Active Directory**：目录账户和本地账户在同一个密码表单上登录，在 WebDAV、SFTP 和 FTPS 上也用同一个密码（S3 和 NFS 用的是账户自己签发的密钥和导出）；支持私有 CA，而且 `local` 始终排在最前，所以目录不可用时 `admin@local` 仍能登录。账户的电子邮件始终是一个地址：先取条目的 mail 属性，没有就取按 `name@domain` 形式输入的名称，再不然就是 `name@local`（在租户的 realm 中是 `name@<realm>.local`；操作系统提供方用的也是这一条规则）；旧版 filex 以裸名称创建的账户，会在下次登录时**接管**过来，文件、共享和角色都不变（[docs/LDAP.md](docs/LDAP.md)）。
- **副本 + 对账**：主存储→副本分发（镜像 / 仅追加 / 跳过，按路径 glob 规则设定）、读取回退、定时状态报告、一键“Fix all”（全部修复）。
- **持久化操作队列**：重启后不丢失的队列，放在你自己的数据库（SQLite / Postgres / MySQL）或 Redis 中；工作池带重试 + 取消 + 管理仪表盘。每个驱动都按优先级给任务排序，所以某个人刚上传了一个文件，它的病毒扫描会先于首次导入排进队列的那两万个得到处理。未设置时，驱动跟随数据库，而不是默认用 SQLite：把 SQLite 语句发到 Postgres 服务器上，每次轮询都是语法错误，一个任务都不会运行。
- **由数据库支撑的文件树**：列表来自数据库缓存（1-5 ms），而不是存储后端（~100 ms）；定期同步会发现不经 filex 发生的更改，后端报告 etag 时按 etag 比较，不报告时按大小 + 修改时间比较。存储的 **Paths to exclude from scanning**（扫描时排除的路径），例如 `.*`、`downloads/incomplete/**`、`*.tmp`，会把现有文件树中 filex 用不上的部分挡在遍历、编目、搜索索引和病毒扫描器之外：这是成本控制手段，不是访问控制手段（[docs/STORAGE.md](docs/STORAGE.md#scan-exclusions)）。
- **面向大型本地文件树的延迟编目**：`sync_mode: lazy` 跳过一开始的遍历：你打开的文件夹会立刻从磁盘直接列出，并最先编目；其余部分由一个会给人让路的慢速后台过程编目（或者只在有人打开文件夹时才编目）。打开过的文件夹在预算范围内受监视，没人访问过的文件夹绝不会被当作已删除；搜索、文件夹大小和用量在尚未覆盖全部内容时会明确说明（[docs/STORAGE.md](docs/STORAGE.md#lazy-catalogue)、[设计文档](docs/LAZY-CATALOGUE.md)）。想法来自 Alex（[#45](https://github.com/BRF-Tech/filex/issues/45)）。
- **查看器与编辑器**：图片/视频/音频、PDF、Markdown（分栏编辑器 + 预览）、CSV（配置了 ONLYOFFICE 时用它的电子表格，否则用只读表格）、代码（Monaco）、通过 OnlyOffice 支持的 Office 文档、Drawio + Mermaid 图表、3D 模型。ONLYOFFICE 只能保存为较新格式的文档（`.doc` 编辑后保存为 DOCX），会用正确的扩展名存放在原文件**旁边**，绝不覆盖原文件，编辑过这份文档的人会收到通知（[docs/ONLYOFFICE.md](docs/ONLYOFFICE.md#a-save-in-another-format)）。OnlyOffice 的 **Test now**（立即测试）会经由文档所用的同一个入口获取一遍，并在文档服务器没有强制使用 JWT 时发出警告；出现 *Download failed*（下载失败）之后，编辑器会说明这条消息背后是两种失败中的哪一种（[docs/ONLYOFFICE.md](docs/ONLYOFFICE.md#failure-editor-shows-download-failed)）。编辑器以每个人自己的语言打开，或者以管理员在 **External services → ONLYOFFICE**（外部服务 > ONLYOFFICE）中为所有人选定的语言打开（`FILEX_ONLYOFFICE_LANG`）（[编辑器的语言](docs/ONLYOFFICE.md#the-editors-language)）。某类文件有不止一个应用或查看器时，用 **Open with**（打开方式）和 **Choose an app…**（选择应用）选定一个，*Always use this app*（始终使用此应用）记在你的账户上。
- **通知**：通用 JSON webhook（不绑定 Slack/Discord），目标数量不限，每个目标有自己的签名密钥，并各自按事件订阅；另有界面内的铃铛，带已读/未读状态和按用户设置的静音矩阵。未读数是**铃铛上的角标**（99 以内显示确切数字，超过则显示 `99+`；系统有程序坞图标的话，桌面应用的程序坞图标上也有）；一行通知能否点击，只看它有没有去处（签名请求打开的是签署界面，而不是通知页面）；**View all**（查看全部）在资源管理器之上打开你的每一条通知，每个人都可以用，而不只是管理员。**创建**文件的写入和**替换**文件的写入是不同的事件（`file.uploaded` / `file.updated`）；运维人员最想单独收到的那几种事件（受感染的上传被隔离、上传失败、加密文件夹用恢复密钥打开）可以逐个订阅（[docs/NOTIFICATIONS.md](docs/NOTIFICATIONS.md)）。**每条通知都由服务器写出**：铃铛、桌面应用的提示、推送和电子邮件显示的是同一句话，用读者账户的语言；webhook 用为它设定的语言，另外还收到未经翻译的消息，供自行翻译的接收方使用（[通知写的是什么](docs/NOTIFICATIONS.md#what-a-notification-says)）。**Web Push**（在用户设置中的 *Push notifications on this device*（在此设备上推送通知））在 filex 关闭时也把通知送到手机或浏览器（在 iPhone 或 iPad 上，需要把 Web 应用添加到主屏幕，iOS 16.4 或更高版本）：种类、静音和摘要都与铃铛相同（[Web Push](docs/NOTIFICATIONS.md#web-push)）。
- **搜索**：内嵌 Bleve，全文 + 元数据，遵循权限。VS Code 风格的文件名评分：文件夹算数，词序不算（`main code` 能找到 `Code/main.go`），分隔符和拼写错误不计较（`invoice 2026` 能找到 `invoice_2026.pdf`，`mian.go` 能找到 `main.go`），数字则按字面匹配（`2026` 绝不会当成 `2025`），`tag:` 筛选，精确匹配排在最前。搜索在服务器上按类型、MIME 类型、日期、大小、文件夹和所有者缩小范围，然后才计入数量上限，答复会说明命中的总数（[缩小搜索范围](docs/SEARCH.md#narrowing-a-search)）。⌘K 的结果可以下载（文件夹打包成一个 zip），也可以就地拖出（[docs/SEARCH.md](docs/SEARCH.md)）。
- **看得出内容的缩略图**：PDF 显示**第一页**，按顶部对齐，所以标题就在卡片里；视频显示第一个不是黑色的帧（过去，以淡入开场的视频会生成一个黑色方块，不到一秒的片段则什么都生成不出来，那一行却仍然显示“就绪”）；Office 文档显示渲染出来的第一页；文本、代码或 CSV 文件则**用自己的开头几行填满卡片**，而不是重复那一行已经显示的扩展名。图片、视频（ffmpeg）、PDF（ghostscript）、Office（已连接的 OnlyOffice）；会感知各项能力是否可用，服务器如果缺少其中某个二进制文件，现在会在启动时的日志里明确说明，而不是悄悄画一些彩色方块。文件被彻底删除时，它的缓存缩略图随之释放；定期运行的对账任务会回收旧实例积累下来的孤立缩略图。缩略图**跟着自己的文件走**：在 filex 之外改动过的文件，或者一直没有图片的文件，会在列表或同步遇到它时重新绘制缩略图；**SVG** 在每个实例上都由内置引擎绘制（大小和时间限制由管理员设置），**HEIC/AVIF** 照片则通过 ImageMagick 绘制；透明图片衬在棋盘格上；**文件夹显示最近进入其中的文件**，在网格、画廊和列表中与文件夹画在一起，悬停时显示文件夹里有什么（管理员可以关闭这项功能）；文本文件显示开头几行，压缩包显示其中的内容；缺少处理工具的文件会被指明，而不是被遮掩过去；**Admin → Tools → Thumbnail repair**（管理 > 工具 > 缩略图修复）按需重新绘制一个文件、一个文件夹或一个存储的缩略图（[docs/thumbnails.md](docs/thumbnails.md)）。
- **标签页、主题与深层链接**：多个文件夹并排打开，浅色/深色/自动主题，地址栏跟随当前打开的文件夹，所以粘贴的链接会直达那个文件夹。主题库自带八套配色，每一套都是 `--fe-*` 变量的一份映射，而不是第二份样式表，所以宿主页面或嵌入端可以选用其中一套，或者设置自己的值，而不必分叉任何 CSS；运维人员可以添加自己的配色（见*外观*）。
- **外观：处处都是你的颜色**：管理面板的 **Appearance**（外观）界面用来组合具名主题（浅色和深色各十二种颜色、一个圆角半径、一个字体栈），边输入边预览，并把其中一个设为**实例默认主题**。彩色按钮上的文字颜色按对比度选定，而不是假定为白色；配色的其余部分由服务器推导；主题会延伸到登录页和每个公开链接（用主题自己的色调，或者用你单独给这两个页面指定的颜色），因为到登录页就止步的品牌化，算不上品牌化：未登录的页面套用实例默认主题，绝不会套用上一个用过那个浏览器的人的配色，已登录的人则以自己的选择为准。主题以一个 JSON 文件导出和导入。**自定义样式表**是它旁边那件危险的工具：现在它在你打开之前一直关闭，绝不会提供给任何未登录的人，无法获取任何内容，也触及不到用来关闭它的那个界面（[docs/INTEGRATION.md](docs/INTEGRATION.md#themes)）。
- **一张表格，处处通用**：filex 里只剩一张表格，就是资源管理器的那张，其他每个列表都是这一张：管理面板的各个菜单、**My shares**（我的共享）、应用自己的界面。每个列表都把第一列冻结在左侧，把操作冻结在右侧，调整大小、调整顺序和排序的方式都一样，每一行都以**一个固定的 Actions（操作）菜单**收尾，菜单里是这一行能做的全部操作，也就是资源管理器的 ⋮ 打开的那个菜单，所以第二张表格不可能偏离第一张。带主界面的已安装应用，在管理面板导航的 **Apps**（应用）下有自己的一行。
- **找得到路的管理面板**：管理员的页面都放在顶栏的一个大型菜单里：先是仪表盘，然后是 **Files & storage**（文件与存储）、**People & security**（人员与安全）和 **System**（系统），各是一个由具名分区组成的菜单面板，每个页面下面都有一行简短说明。每个页面点两下就到，地址还是它一直以来的那个；委派管理员只会看到自己的权限能打开的页面；键盘和屏幕阅读器都支持；在手机上，同一批页面以列表的形式放在抽屉里（[管理面板](docs/ADMIN-PANEL.md)）。
- **符号链接，止于存储边界**：`local` 存储里的链接如果指向该存储内部，filex 会跟随它，并按它指向的内容打开；指向存储之外的链接会**带着标记和原因列出**，读取、写入和删除都会被拒绝，除非你为那个存储打开 *Follow symlinks that leave this folder*（跟随指向此文件夹之外的符号链接）这一选项（[docs/STORAGE.md](docs/STORAGE.md#symlinks)）。
- **按每种设备习惯的方式打开**：用**鼠标**时，单击选中，**双击打开**（Enter 打开所选内容），这是文件管理器的经典操作方式，也是一项按查看者设置的偏好（`ExplorerConfig.openTrigger`，默认为 `'double'`；桌面应用把它显示为 **Settings → Open files with**（设置 > 打开文件的方式），`'single'` 则恢复单击打开）。在**触摸屏**上，轻点始终是打开：不存在悬停即选中。在每种设备上，点击或轻点一下**复选框**就是选中（Shift 扩展范围），右键点击或长按则打开菜单；列表行、网格卡片和画廊图块都带有复选框。
- **支持键盘，而且标在明处**：右键菜单和工具栏里的每一项操作都会标出执行它的按键，按键从快捷键注册表读取，所以重新映射后也会跟着变。三十二个操作可以在 *Shortcut settings*（快捷键设置）中重新映射（按浏览器保存）；浏览器留给自己用的少数几个组合键，比如 `Ctrl+W`，会被拒绝并说明原因，而不是存成一个永远不会触发的按键。
- **用量与费用**：filex 不自行计量你的服务商账单；它读取服务商本来就会生成的报告，把报告规范化，再用一张你可以编辑的表格计价。Backblaze B2 的每日 CSV 通过 filex 已经在用的同一套 S3 API 读取，所以没有新的依赖，也没有新的凭据类型。免费额度是独立的字段，而不是公式里的常量；页面把服务商账户级的那一行与各存储桶的行分开：把它们相加会把同一批事务算两遍，多出来的数目恰好小到没人察觉（[docs/USAGE.md](docs/USAGE.md)）。
- **审计日志**：每一次变更都有记录，带有执行者、集成身份和元数据。
- **CLI 客户端**：同一个二进制文件无需任何服务器端插件就能连接远程服务器（`filex client`、`filex sync`）：跨存储复制和移动、回收站、版本、标签、应用操作、压缩包和你的链接，每个服务器任务都会一直跟踪到结束；`filex client login --realm` 登录到某个租户，`filex encrypt` 创建加密文件夹，`filex vault` 挂载和整理保险库，`filex sync run --json` 把引擎的事件交给程序，遭到拒绝时打印服务器自己的句子，已保存的会话只会发送到保存它时所用的那个地址（[docs/CLI.md](docs/CLI.md)）。
- **自动更新**：次要版本会发出通知，可一键升级；补丁版本在您允许后才会自行安装（`AUTO_UPGRADE=true`；默认情况下 filex 只检查并通知您）；对于由包管理器管理的实例（Homebrew、winget、Snap、操作系统发行版的软件包）或容器，filex 会告知有新的发行版以及获取新发行版的命令，管理页面会明确说明它只做通知（[docs/UPDATES.md](docs/UPDATES.md)）。
- **单一二进制文件**：goreleaser 矩阵：linux/macOS/Windows × amd64/arm64。CGO=0，modernc.org/sqlite。
- **i18n**：英语 + 土耳其语开箱即用，**公开链接也包括在内**：共享链接、PIN 验证页、文件请求页面或应用的签署界面都以访客的语言呈现，公开页面的外壳会**提供一个选择器**，因为陌生人浏览器的语言只是一种猜测，而正在读合同的人应当能够改正它。不用 JS 的简单页面先看 `?lang=`，再看 `Accept-Language`，最后用服务器默认值。**服务器写出的文字来自同一份字符串表**（邮件、通知用语、不用 JavaScript 的页面和安装时的权限审核），每段文字面向的仍是它一直以来的读者，每个键各自回退到英语；占位符与英语不一致的译文，运行时不会采用，所以邮件绝不会丢失其中的链接或 PIN。已登录的人只有一种语言，即账户的语言：界面和每个渠道上的通知都跟随它，ONLYOFFICE 编辑器也跟随它，除非管理员固定了一种语言。
- **语言包**：其他任何语言都是一个**没有任何可运行内容的应用**：一份字符串清单，和其他任何应用一样从 GitHub 仓库、上传的文件或 URL 安装，列在 **Plugins → Apps**（插件 > 应用）下，并标出它对当前运行版本的覆盖率（*Español - 97% translated · the rest shows in English*，意思是“西班牙语，已翻译百分之九十七，其余以英语显示”）。它的语言会加入每一个选择器（设置对话框、管理面板的顶栏、公开共享页面），并把资源管理器、管理面板和公开页面一并翻译。复数形式遵循 **CLDR 类别**，所以 `zero`、`one`、`two`、`few`、`many` 和 `other` 当中，自己的语言有哪些，语言包就写哪些。西班牙语、德语和法语作为示例提供，一个模板仓库加上 `scripts/i18n-export.mjs` / `i18n-validate.mjs` 会带着译者从导出一路走到安装。校验器要求语言包遵守内置语言所遵守的规则，其中包括：文本想用长破折号的地方，一律用普通连字符（[编写一个](docs/PLUGIN-KIT.md#writing-a-language-pack)）。
- **从右到左布局**：阿拉伯语、希伯来语、波斯语、乌尔都语以及其他任何从右到左书写的语言，都会让整个界面从右到左排布：导航面板、表格、拖放和调整列宽时的几何计算、菜单以及带方向的图标。**不**该镜像翻转的内容就不翻转：PDF 字段编辑器在文档空间中工作；路径、命令或其他任何机器文本都会隔离开来，这样在从右到左的句子里仍然从左到右读，服务器写出的句子也不例外。这条规则由一项守卫测试强制执行：布局只用 CSS 逻辑属性编写（[docs/RTL.md](docs/RTL.md)）。
- **标签：个人的，或团队的**：标签要么是**个人**标签（只属于你，名称绝不会透露给其他任何人），要么是在租户内共享的**团队**标签，添加或移除团队标签需要对文件有编辑权限。一个标签会打开带这个标签的每个文件，无论这些文件在哪个文件夹里，而 `tag:` 能缩小搜索范围。大小写按输入时的原样保留。
- **身份提供方，在面板中管理**：**Admin → Identity providers**（管理 > 身份提供方）现在真正驱动登录，而不再只是保存一些无人读取的设置：OIDC、LDAP、请求头代理、本地密码表单以及操作系统自己的账户（**Windows**（本地账户或域账户，`LogonUserW`，无需安装任何东西）和 **Linux PAM**），每一种都带有 **Test now**（立即测试），它会真正去探测，并说明验证了哪一个环节。操作系统提供方只有通过一次让真实账户成功登录的测试才能开启，这个账户会成为超级管理员；这种提供方无法通过环境变量开启。每个提供方都遵循**同一条首次登录规则**：能否创建账户（`auto_create`，对 Windows 和 PAM 默认关闭），以及为哪些群组创建（`allowed_groups`）。在环境变量或 `config.yaml` 中设置的值**优先，而且看得见**；客户端密钥或绑定密码只能写入，用 `FILEX_SECRET_KEY` 加密封存，绝不回传；最后一条登录途径无法在该页面上关闭（[docs/SSO.md](docs/SSO.md)、[docs/LDAP.md](docs/LDAP.md)、[docs/OS-LOGIN.md](docs/OS-LOGIN.md)）。
- **登录安全**：输错的密码按账户标识符计数（不论这个账户是否存在，因此计数不会泄露任何信息），也按客户端地址计数，Web 表单、WebDAV、FTPS 和 SFTP 都一样：10 分钟内每个账户满 5 次、每个地址满 10 次，就把门关上一分钟，之后每次加倍，最长 15 分钟；锁定期间，连正确的密码也不接受。登录表单会用读者的语言说明还能再试几次。**IP 允许列表**是重新进来的途径：没有哪个账户享有特权，第一个管理员也不例外（只有公开演示的共享账户仅按地址计数，[docs/DEMO.md](docs/DEMO.md)）；**Admin → Sign-in security**（管理 > 登录安全）里有次数限制、允许列表、带 *Lift the lock*（解除锁定）的各项锁定，以及登录记录：每一次错误尝试、锁定、解除和设置更改，各记一次，不论管理员走的是哪个入口（管理面板、API 密钥、MCP）。计数所用的地址是套接字的对端地址，除非这个对端是你信任的代理（`FILEX_TRUSTED_PROXIES`，默认为 `auto`：这台机器，在容器中运行时还包括同一网络上的其他容器，绝不包括网关，也绝不包括 LAN；页面会指明发送了转发地址却未受信任的对端，并提议把这个对端加进去），所以客户端无法靠写入 `X-Forwarded-For` 来自选地址（[docs/CONFIGURATION.md](docs/CONFIGURATION.md#sign-in-attempt-limits)）。
- **来自其他站点的更改会被拒绝**：一个请求如果会做出更改，并且只携带访客的会话（Cookie，或受信任的代理的登录请求头），就必须来自 filex 自己的页面、filex 自己的地址，或者 `FILEX_CORS_ALLOWED_ORIGINS` 中列出的源；其余的一律在路由运行之前以 `403 cross_origin_refused` 作答。密钥、共享链接和上传链接、上传票据、S3 以及脚本不受影响（[docs/CONFIGURATION.md](docs/CONFIGURATION.md#requests-from-other-origins)）。
- **服务器的措辞，每一种拒绝同一个形状**：拒绝时答复一个稳定的 `error` 代码，以及服务器用读者的语言写出的句子（`message`）；资源管理器、管理面板、桌面应用、CLI 和智能体显示的都是同样的措辞（[docs/API-ERRORS.md](docs/API-ERRORS.md)）。客户端过去各自保留一份副本的规则（哪些文件可以打开编辑、输入的上限、在这个实例上不可能发生的通知事件），现在由服务器发布，所以没有哪个界面会作出不同的判断（[服务器发布的规则](docs/BACKEND.md#rules-the-server-publishes)）。

## 架构

参见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。

## 文档

**入门**：[安装](docs/INSTALLATION.md) · [配置](docs/CONFIGURATION.md) · [管理面板](docs/ADMIN-PANEL.md) · [数据库](docs/DATABASES.md) · [发行版](docs/RELEASES.md) · [更新](docs/UPDATES.md) · [演示模式](docs/DEMO.md)

**客户端**：[桌面应用](docs/DESKTOP.md) · [文件夹同步](docs/SYNC.md) · [CLI](docs/CLI.md) · [同步引擎的事件流](docs/SYNC.md#the-event-stream---json) · [集成 / 嵌入](docs/INTEGRATION.md) · [AI 与 MCP](docs/MCP.md)

**不用浏览器**：[协议（S3 · SFTP · FTPS · NFS · WebDAV · `filex mount`）](docs/PROTOCOLS.md) · [WebDAV](docs/WEBDAV.md)

**应用**：[应用：安装、管理、签署、转换](docs/APP-PLUGINS.md) · [安装请求](docs/APP-PLUGINS.md#install-requests) · [应用权限](docs/APP-PLUGINS.md#app-permissions) · [默认应用](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail) · [编写应用](docs/PLUGIN-KIT.md) · [应用通信契约](docs/APP-PLUGINS-API.md)

**语言与布局**：[编写语言包](docs/PLUGIN-KIT.md#writing-a-language-pack) · [从右到左的语言](docs/RTL.md)

**存储与访问**：[存储](docs/STORAGE.md) · [存储插件](docs/PLUGINS.md) · [用量与费用](docs/USAGE.md) · [上传与续传](docs/UPLOADS.md) · [配额](docs/QUOTAS.md) · [SSO（OIDC）](docs/SSO.md) · [LDAP 与代理认证](docs/LDAP.md) · [Windows 与 Linux 账户](docs/OS-LOGIN.md) · [登录尝试次数限制与受信任的代理](docs/CONFIGURATION.md#sign-in-attempt-limits) · [RBAC、文件夹访问权限与 API 令牌](docs/RBAC.md) · [角色与按用户设置的权限](docs/PERMISSIONS.md) · [群组](docs/GROUPS.md) · [多租户与 realm](docs/MULTI-TENANCY.md) · [租户自助管理](docs/TENANT-ADMIN.md)

**数据与功能**：[共享与文件请求](docs/SHARING.md) · [ShareX](docs/SHAREX.md) · [回收站与版本管理](docs/TRASH-VERSIONING.md) · [防护](docs/PROTECTION.md) · [压缩包](docs/ARCHIVES.md) · [端到端加密](docs/E2E-ENCRYPTION.md) · [谁可以加密](docs/E2E-ENCRYPTION.md#who-may-encrypt) · [保险库（第 3 级）](docs/E2E-VAULT-FORMAT.md) · [编辑加密的办公文档（设计）](docs/E2E-OFFICE.md) · [搜索](docs/SEARCH.md) · [实时与在线状态](docs/REALTIME.md) · [通知](docs/NOTIFICATIONS.md) · [Web Push](docs/NOTIFICATIONS.md#web-push) · [缩略图](docs/thumbnails.md) · [副本复制](docs/REPLICATION.md) · [主题与外观](docs/INTEGRATION.md#themes)

**运维与扩展**：[部署](docs/DEPLOYMENT.md) · [Docker](docs/DOCKER.md) · [指标](docs/METRICS.md) · [架构](docs/ARCHITECTURE.md) · [后端 API 规范](docs/BACKEND.md) · [API 错误](docs/API-ERRORS.md) · [OpenAPI 3.1（`/api/files`、`/api/ai`）](backend/internal/api/openapi.json) · [组件 API](docs/API.md) · [OnlyOffice](docs/ONLYOFFICE.md) · [编辑器的语言](docs/ONLYOFFICE.md#the-editors-language) · [ONLYOFFICE 中的 CSV](docs/ONLYOFFICE.md#csv-files) · [来自其他源的请求](docs/CONFIGURATION.md#requests-from-other-origins)

[完整文档索引](docs/README.md)

## filex 是怎样开发的

filex 只有一位维护者，他借助 AI 编程智能体（Claude Code）开发 filex。filex 是什么、如何工作，由维护者决定：架构、数据模型和安全模型，以及每项功能的行为。智能体在这个方向之内编写大部分代码，用的是维护者使用的语言。开发仓库中约 82% 的提交带有 `Co-Authored-By: Claude` 这一行，所以这件事并没有隐藏。

它是 AI 辅助开发的，而不是未经审查的生成代码：

- 每项变更在合入之前都要走完测试链：Go 测试（也在竞态检测器下运行）、约 500 个 Vitest 文件、约 120 个在 Chromium、Firefox 和 WebKit 上运行的 Playwright 规格、Cypress，以及在 SQLite、PostgreSQL 和 MySQL 上运行的数据库测试。每个发行版都只有在 GitHub 的完整矩阵和发布工作流的一次试运行在那个确切的提交上通过之后，才会打标签。
- 涉及登录、权限、共享和加密的变更会额外经过几轮审查；测试变红时，修的是问题，而不是放宽测试。
- 安全报告公开处理：参见 [SECURITY.md](SECURITY.md) 和已发布的安全公告。目前还没有过独立审计。
- 德语、西班牙语、法语和中文的 README，以及德语、西班牙语和法语语言包，都是机器翻译，还没有经过母语人士审阅。

## 开发

```bash
git clone https://github.com/BRF-Tech/filex.git
cd filex
pnpm install
pnpm run build:all    # builds packages, web, then Go binary
./bin/filex serve
```

子目录：
- `backend/`：Go HTTP 服务（cmd/filex、internal/*、db/queries、db/migrations）
- `packages/core`：`@brftech/filex-core`（Vue 3 SFC，以此为准）
- `packages/webcomponent`：`@brftech/filex`（Web 组件封装）
- `packages/react`：`@brftech/filex-react`（基于 @lit/react 的 React 适配器）
- `web/`：Vue 3 管理界面（通过 `go:embed` 嵌入到 Go 二进制文件中）
- `desktop/`：Electron 应用（打包后的主进程、系统托盘里的同步、自动更新）
- `demo/`：各框架的独立 HTML 演示
- `e2e/`：Playwright 测试套件（Web、嵌入端、打包后的桌面应用）+ `shots/`，即 `pnpm shots` 运行的脚本，用来重新截取上面的每一张截图
- `docker/`：Dockerfile + compose
- `deploy/`：现成的 Compose 部署方案 + Helm chart（见 [`deploy/`](deploy/)）
- `docs/`：Markdown 文档
- `docs-site/`：发布在 [docs.filex.sh](https://docs.filex.sh) 的 VitePress 站点

欢迎贡献，参见 [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md)。

## 许可证

MIT，参见 [LICENSE](LICENSE)。

[`docs/badges/`](docs/badges/) 中的商店徽章是各商店自己的图稿，未经修改直接使用，且不在该许可证的涵盖范围内：Microsoft 和 Microsoft Store 徽章是 Microsoft 集团公司的商标；Snap Store 徽章 © Canonical Ltd.，采用 [CC BY-ND 2.0 UK](https://creativecommons.org/licenses/by-nd/2.0/uk/) 许可协议。
