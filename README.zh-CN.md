<!-- Translated from README.md as of v0.56.0 (the shortened README of #218). The English README is the source: change it first, then carry the change here. -->

<div align="center">

<img src="docs/logo.png" alt="filex 标志" width="96">

# filex

**带 Web 界面的自托管文件管理与共享，只需一个 Go 二进制文件；Nextcloud、Dropbox 和 Google Drive 的开源替代品。**<br>
基于 Web 的文件浏览器，支持本地、S3、SFTP、WebDAV、FTP 和 SMB 存储：一个私有云，可以当作 S3、SFTP、NFS、WebDAV 来访问。<br>
SSO/LDAP、多租户、加密文件夹、可嵌入的 Web 组件、桌面应用、MCP 服务器。

[![试用在线演示](https://img.shields.io/badge/Try_the_live_demo-f59e0b?style=for-the-badge)](https://demo.filex.sh)
[![快速开始](https://img.shields.io/badge/Quick_start-2f6ceb?style=for-the-badge)](#快速开始)
[![文档](https://img.shields.io/badge/Documentation-374151?style=for-the-badge)](https://docs.filex.sh)

[![Release](https://img.shields.io/github/v/release/BRF-Tech/filex?color=2f6ceb)](https://github.com/BRF-Tech/filex/releases)
[![License: MIT](https://img.shields.io/github/license/BRF-Tech/filex?color=22c55e)](LICENSE)

[English](README.md) · [Türkçe](README.tr.md) · [Deutsch](README.de.md) · [Español](README.es.md) · [Français](README.fr.md) · **简体中文**

<sub>本文译自[英文 README](README.md)（0.56 发行前精简后的版本）；两者不一致时，以英文版为准。本文为机器翻译，尚待母语者审校，欢迎指正。文中链接指向的文档均为英文。</sub>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://filex.sh/shots/explorer-grid-dark.ff7a02b49f27.png">
  <img src="https://filex.sh/shots/explorer-grid-light.475b0b61faab.png" alt="filex 资源管理器 - 缩略图网格" width="900">
</picture>

</div>

<table>
<tr>
<td width="50%" valign="top">

**🗂️ 所有存储，一棵文件树**

把你已有的存储并排挂载，在一个存储中复制或剪切，在另一个存储中粘贴。把 `rclone`、`restic`、WinSCP 或 FileZilla 指向同一棵文件树。

[存储与协议 →](#存储与协议)

</td>
<td width="50%" valign="top">

**🔗 共享与防护**

带 PIN、过期时间和下载次数上限的链接。回收站、版本历史和端到端加密文件夹。

[共享与防护 →](#共享与防护)

</td>
</tr>
<tr>
<td width="50%" valign="top">

**🔑 你的账户，你的角色**

OIDC、LDAP / Active Directory，或者服务器上的 Windows 和 Linux 账户。角色、群组、多租户。

[人员与访问 →](#人员与访问)

</td>
<td width="50%" valign="top">

**💻 文件夹实时同步的桌面应用**

Windows、Linux 和 macOS（Apple Silicon）：任何一边的修改大约一秒就到另一边。

[桌面应用与 CLI →](#桌面应用与-cli)

</td>
</tr>
<tr>
<td width="50%" valign="top">

**🧩 嵌入你的产品**

同一套界面，以 Vue 3 组件、React 组件和 `<filex-explorer>` Web 组件的形式提供。

[嵌入到你的应用中 →](#嵌入到你的应用中)

</td>
<td width="50%" valign="top">

**🤖 应用与 AI 智能体**

沙箱化的应用（e-Signature、Convert、draw.io），以及智能体可以驱动的内置 MCP 服务器。

[应用 →](#应用) · [AI 智能体 / MCP →](#ai-智能体--mcp)

</td>
</tr>
</table>

<p align="center"><a href="#为什么选择-filex">为什么选择 filex</a> · <a href="#如果你来自-nextclouddropbox-或-google-drive">从其他工具迁来</a> · <a href="#使用-compose-或-helm-自托管">自托管</a> · <a href="#功能">功能</a></p>

<br>

# 快速开始

> [!TIP]
> **安装之前先看看**：[demo.filex.sh](https://demo.filex.sh)，用
> `demo@demo.com` / `demo` 登录（管理员角色，演示环境每晚重置）。

运行你自己的实例，在你想要提供的文件夹里执行：

```bash
docker run -p 5212:5212 \
  -e FILEX_DEFAULT_STORAGE_DRIVER=local -e FILEX_DEFAULT_STORAGE_PATH=/srv/files \
  -v filex-data:/data -v "$PWD:/srv/files" \
  ghcr.io/brf-tech/filex:latest
```

打开 http://localhost:5212/admin 即可，首次运行时会把管理员凭据和嵌入说明打印到控制台。那是运维人员的地址；你给谁开了账户，谁拿到的就是 **http://localhost:5212/drive** 这个地址：同一个文件管理器，只是外面没有那层管理面板。

<details>
<summary><b>这条命令挂载了什么，以什么用户身份运行</b></summary>

这条命令提供的就是**你运行它时所在的文件夹**：打开界面，你的文件已经在里面了。`/data` 是 filex 自己的目录（SQLite 数据库、搜索索引、缩略图缓存），所以它是一个命名卷，而不是你放文件的那个文件夹；两者是有意分开的。把 `$PWD` 指向别的位置，或者之后在管理面板里添加更多存储；有多个顶层文件夹的存储桶，可以一次挂载完，每个文件夹一个存储：*Storages → Add storage → Mount several folders at once*（存储 > 添加存储 > 一次挂载多个文件夹）。

容器默认以 **root** 身份运行，所以它写入 `/data` 的内容归 root 所有；设置 `PUID`/`PGID`，让它以你自己的身份运行（[docs/DOCKER.md](docs/DOCKER.md#which-user-the-container-runs-as)）。

</details>

<details>
<summary><b>没有 Docker？运行二进制文件</b></summary>

从[最新发行版](https://github.com/BRF-Tech/filex/releases/latest)下载对应你平台的压缩包，解压后运行二进制文件（[docs/INSTALLATION.md](docs/INSTALLATION.md#binary)）：

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

要正式部署，有现成的 Compose 部署方案和 Helm chart：[使用 Compose 或 Helm 自托管](#使用-compose-或-helm-自托管)。镜像发布在 [GitHub Packages](https://github.com/BRF-Tech/filex/pkgs/container/filex) 上。

<br>

# 为什么选择 filex

大多数自托管文件管理器要么**太小**（只是带上传功能的目录列表），要么**太大**（为了其中的文件标签页而部署一整套协作办公套件）。filex 瞄准的正是两者之间的空当。下面的导览分为八个部分，每个部分的截图都折叠在它下面。每项功能的完整说明，连同它的限制和介绍它的指南，都在 [docs/FEATURES.md](docs/FEATURES.md) 中。

<br>

## 资源管理器

**一个大家早已会用的文件管理器。**

- 导航面板里有 **Home · My files · Shared with me · My shares · Recent · Starred · Drafts · Trash**（主页、我的文件、与我共享、我的共享、最近、已加星标、草稿、回收站）；列表、网格和画廊视图；标签页、标签和 ⌘K 命令面板。
- 在线头像和文件变更通过 WebSocket 实时送达。
- 用你自己的颜色组合出一个主题，并把它设为默认主题：登录页和每个公开链接也会套用它；手机安装的 Web 应用也会带上你的实例的名称和徽标。
- 英语和土耳其语是内置的，其他语言以语言包的形式安装；对于从右到左书写的语言，界面会转为从右到左。

资源管理器的全部功能：[docs/FEATURES.md → The explorer](docs/FEATURES.md#the-explorer)。

<p align="center"><img src="https://filex.sh/shots/driveshell/driveshell-hero-1440.6410104e072c.png" alt="filex 外壳" width="860"></p>

<details>
<summary><b>资源管理器截图</b> - 外壳、搜索、导航、标签、回收站、铃铛、主题、符号链接、ONLYOFFICE 中的 CSV</summary>

<table>
<tr>
<td width="50%" valign="top">

![filex 外壳](https://filex.sh/shots/driveshell/driveshell-hero-1440.6410104e072c.png)

<sub>外壳：每个人进入后看到的界面</sub>

</td>
<td width="50%" valign="top">

![在文件夹中搜索](https://filex.sh/shots/driveshell/driveshell-search-1440.2536c373425f.png)

<sub>在这个文件夹中搜索；`⌘K` / `Ctrl K` 把查询交给命令面板</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![导航面板](https://filex.sh/shots/sidenav/sidenav-expanded-1440.e96ea7e98388.png)

<sub>导航面板：Home（主页）、Shared with me（与我共享）、My shares（我的共享）、Recent（最近）、Starred（已加星标）、Trash（回收站），以及你能访问的存储</sub>

</td>
<td width="50%" valign="top">

![折叠为图标栏](https://filex.sh/shots/sidenav/sidenav-rail-1440.9269e026d29a.png)

<sub>折叠为图标栏</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![个人标签和团队标签](https://filex.sh/shots/tags/tags-kinds-1440.a4504c43e5cb.png)

<sub>标签：你自己的，或者你团队的；一个标签会打开带这个标签的每个文件，无论这些文件在哪个文件夹里</sub>

</td>
<td width="50%" valign="top">

![回收站视图](https://filex.sh/shots/sidenav/view-trash-1440.40f8904b147f.png)

<sub>Trash（回收站）：删除了什么、原本在哪里，以及离清除还剩多久</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![带未读角标的铃铛，已展开](https://filex.sh/shots/signing/bell-badge-1440.e9232b261511.png)

<sub>铃铛：未读数就在铃铛上，每一行都通向它所说的地方</sub>

</td>
<td width="50%" valign="top">

![覆盖在资源管理器之上的完整通知列表](https://filex.sh/shots/signing/notifications-list-1440.669ce56b62d7.png)

<sub>你的全部通知，都在资源管理器里：每个人都可以用，而不只是管理员</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![主题编辑器](https://filex.sh/shots/appearance/theme-editor-1440.867a94807e72.png)

<sub>Appearance（外观）：用你自己的颜色组合出一个主题，边输入边预览</sub>

</td>
<td width="50%" valign="top">

![套用了运维人员主题的资源管理器](https://filex.sh/shots/appearance/themed-explorer-1440.abc278616daa.png)

<sub>设为默认之后，每个人的资源管理器套用的就是它…</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![套用了运维人员主题的登录页](https://filex.sh/shots/appearance/themed-signin-1440.b891c6431444.png)

<sub>…登录页也是，这时还没有任何人登录</sub>

</td>
<td width="50%" valign="top">

![指向存储之外的符号链接，带有标记](https://filex.sh/shots/symlinks/symlink-badge-1440.82a105d4239e.png)

<sub>filex 不会跟随的符号链接会明确说明：既在列表里，也在它的详细信息里用文字写明</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![以分号分隔的 CSV，在 ONLYOFFICE 的电子表格中打开](https://filex.sh/shots/csvoffice/csv-view-1440.83237ba55d3d.png)

<sub>连接了 ONLYOFFICE 时，`.csv` 在 ONLYOFFICE 的电子表格中打开，先是查看，而且没有分隔符对话框：文件自己的分隔符会一并传过去（[CSV 文件](docs/ONLYOFFICE.md#csv-files)）</sub>

</td>
<td width="50%" valign="top">

![ONLYOFFICE 编辑器中的 CSV，附有保存时保留什么的说明](https://filex.sh/shots/csvoffice/csv-edit-1440.4efc379a293d.png)

<sub>…以及它的编辑器，其中会说明保存为 CSV 时保留什么；文件写回去时仍是同一种 CSV，没人改过的单元格保留原来的文本：`007` 还是 `007`（[没人改过的单元格](docs/ONLYOFFICE.md#cells-nobody-changed-keep-their-text)）</sub>

</td>
</tr>
</table>

</details>

<br>

## 存储与协议

**所有存储都在一棵文件树里，这棵树用什么都能访问。**

- 把本地磁盘、S3、FTP、SFTP、WebDAV 和 SMB/NAS 共享并排挂载，在一个中复制或剪切，在另一个中粘贴。
- 把同一棵文件树当作 **S3**、**SFTP**、**FTPS**、**NFSv3** 和 **WebDAV** 来访问：把 `rclone`、`restic`、`aws s3`、WinSCP 或 FileZilla 指向它。同样的 filex 权限（按人、组和文件夹）、回收站和配额与 Web 界面中一样生效；磁盘上的 POSIX 权限位和 ACL 不会映射到 filex 用户。
- `filex mount` 通过普通的 HTTPS 挂载远程服务器：在 Linux 上是一个文件夹，在 Windows 上是一个盘符（不支持 macOS）。
- 对于 filex 没有内置的存储，从管理面板或应用商店的链接安装一个插件。

存储与协议的全部功能：[docs/FEATURES.md → Storage and protocols](docs/FEATURES.md#storage-and-protocols)。

<p align="center"><img src="https://filex.sh/shots/sidenav/connect-1440.45536274e7dc.png" alt="How to connect（如何连接）" width="860"></p>

<details>
<summary><b>存储与协议截图</b> - 连接指南、API 密钥和一个存储插件</summary>

<table>
<tr>
<td width="50%" valign="top">

![How to connect](https://filex.sh/shots/sidenav/connect-1440.45536274e7dc.png)

<sub>How to connect（如何连接）：各份指南，都根据*你的*部署生成</sub>

</td>
<td width="50%" valign="top">

![API keys](https://filex.sh/shots/sidenav/apikeys-minted-1440.3464cb59b2f8.png)

<sub>API keys（API 密钥）：在资源管理器或嵌入端里签发你自己的密钥（凭某个人的会话或令牌；用一个共享的*应用*令牌做代理的嵌入端没有这个条目）</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![连接指南](https://filex.sh/shots/connections-guide.3999cf32c3d9.png)

<sub>用什么都能访问 filex：S3、SFTP、FTPS、NFS、WebDAV。每条命令都根据*你的*部署生成</sub>

</td>
<td width="50%" valign="top">

![Plugins](https://filex.sh/shots/admin-plugins.6bd96bf65745.png)

<sub>filex 未内置的一个存储：在 **Plugins → Storage plugins**（插件 > 存储插件）中以插件形式安装，配置表单由插件自己描述</sub>

</td>
</tr>
</table>

</details>

<br>

## 共享与防护

**用一个可以设限、可以撤销的链接共享。删错了、覆盖错了，都能找回来。**

- 带 PIN、过期时间和下载次数上限的公开链接，以及用来接收文件的文件请求。**My shares**（我的共享）列出你创建的每个链接，可以复制，也可以撤销。
- 把一个文件夹与某个人或某个群组共享，它就出现在对方的 **Shared with me**（与我共享）下。存储需要开启 **Per-item access control (RBAC)**（按项目访问控制），这一项默认是关闭的。
- 删除后在保留期限内可以恢复，写入会保留快照，两者都存放在你已经挂载的存储里。
- 端到端加密文件夹：在浏览器中加密，服务器从不接收密钥。你允许的应用可以在你的浏览器里编辑这样的文件：浏览器为应用解密文件，并把应用保存的内容重新加密。可选对每个写入的文件进行 ClamAV 扫描。

共享与防护的全部功能：[docs/FEATURES.md → Sharing and protection](docs/FEATURES.md#sharing-and-protection)。

<p align="center"><img src="https://filex.sh/shots/public-share.0e3ba07f7c88.png" alt="接收者看到的公开共享链接" width="860"></p>

<details>
<summary><b>共享与防护截图</b> - 共享对话框、接收者看到的内容、My shares、谁可以加密</summary>

<table>
<tr>
<td width="50%" valign="top">

![共享对话框](https://filex.sh/shots/share-modal.87cb16a1a6f0.png)

<sub>共享：PIN、过期时间、下载次数上限、单行 `curl` 命令</sub>

</td>
<td width="50%" valign="top">

![Markdown 查看器](https://filex.sh/shots/viewer-markdown.1789ecdcfbc5.png)

<sub>Markdown 查看器</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![接收者看到的公开共享链接](https://filex.sh/shots/public-share.0e3ba07f7c88.png)

<sub>…以及另一端的人打开的内容。filex 只有唯一一个对外页面：一个共享的文件、一个文件夹、一个文件请求、应用的签署页面，以及挡在其中任何一个前面的 PIN，全都是这个页面，都以你的实例的名义出现</sub>

</td>
<td width="50%" valign="top">

![My shares，其中一行的 Actions 菜单已打开](https://filex.sh/shots/signing/my-shares-1440.6f8676cbb555.png)

<sub>My shares（我的共享）：你创建的链接，以及这些链接的 PIN，需要转告别人时用得上</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Admin → Shares，其中一行的 Actions 菜单已打开](https://filex.sh/shots/signing/admin-table-actions-1440.0fffdc146eb1.png)

<sub>管理面板里的每张表格：每行一个固定的 **Actions**（操作）菜单，与资源管理器的 ⋮ 打开的是同一个菜单</sub>

</td>
<td width="50%" valign="top">

![Admin → Encryption：策略设为须经批准，三个请求等待处理](https://filex.sh/shots/encryption/admin-encryption-1440.35606d99b816.png)

<sub>谁可以加密：关闭、仅限管理员、角色允许的每个人，或者经管理员批准之后；还有等待处理的请求，注明是谁提出的、为什么提出（[谁可以加密](docs/E2E-ENCRYPTION.md#who-may-encrypt)）</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![加密文件夹请求及其理由](https://filex.sh/shots/encryption/request-new-folder.e74894fba30f.png)

<sub>…以及提出请求的人这一侧：New folder（新建文件夹）对话框打开的请求 - 向管理员申请一个新的加密文件夹，并附上理由</sub>

</td>
</tr>
</table>

</details>

<br>

## 人员与访问

**大家已有的账户，每人一个角色。**

- 用本地密码、OIDC、LDAP / Active Directory、认证代理，或者运行 filex 的那台机器上的 Windows 或 Linux 账户登录。
- 每个人都有一个角色：Administrator（管理员）、User（用户）、Viewer（查看者），或者你自己定义的角色，由具名权限构成。群组带有文件夹访问权限和一个角色，可以跟随你目录中的群组；针对个人的例外优先于角色。
- 猜密码快不起来：输错的密码按账户、按地址分别计数，Web 表单、WebDAV、FTPS 和 SFTP 都一样。
- 从设计上就是多租户，由一个开关控制：每个租户有自己的登录提供方、自己的域名和证书，并且自己管理自己。关闭时，任何界面都不显示租户。

人员与访问的全部功能：[docs/FEATURES.md → People and access](docs/FEATURES.md#people-and-access)。

<p align="center"><img src="https://filex.sh/shots/roles/roles-list-1440.6bb0779b66e3.png" alt="Admin → Roles：内置角色和两个自定义角色" width="860"></p>

<details>
<summary><b>人员与访问截图</b> - 角色、群组、登录安全、被锁定的账户、管理面板及其菜单</summary>

<table>
<tr>
<td width="50%" valign="top">

![Admin → Roles：内置角色和两个自定义角色](https://filex.sh/shots/roles/roles-list-1440.6bb0779b66e3.png)

<sub>Roles（角色）：Administrator（管理员）、User（用户）、Viewer（查看者）以及你自己的角色，每个角色由谁持有、允许什么、在哪些文件夹上有所不同、有哪些限制（[docs/PERMISSIONS.md](docs/PERMISSIONS.md)）</sub>

</td>
<td width="50%" valign="top">

![Admin → Groups](https://filex.sh/shots/groups/groups-list-1440.22ff57b029f5.png)

<sub>Groups（群组）：具名的一组人，带有文件夹访问权限和一个角色；成员手动添加，或者与登录带来的群组保持一致（[docs/GROUPS.md](docs/GROUPS.md)）</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![与群组共享文件夹](https://filex.sh/shots/groups/share-group-1440.31c2a81e1eeb.png)

<sub>与群组共享文件夹，群组和人并列：Owner（所有者）要经对话框询问后才授予，而不是点一下就授予</sub>

</td>
<td width="50%" valign="top">

![Admin → Sign-in security](https://filex.sh/shots/loginsecurity/login-security-1440.70f2a2a76257.png)

<sub>Sign-in security（登录安全）：尝试次数限制，以及从不计数的允许地址；受信任的代理、锁定和登录记录在同一页面的下方（[登录尝试次数限制](docs/CONFIGURATION.md#sign-in-attempt-limits)）</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![账户被锁定时的登录表单](https://filex.sh/shots/loginsecurity/login-locked-1440.42e1fe6f1937.png)

<sub>…以及账户被锁定时登录表单显示的内容，锁定的倒计时就在按钮上</sub>

</td>
<td width="50%" valign="top">

![管理仪表盘](https://filex.sh/shots/admin-dashboard.233358b87c3e.png)

<sub>管理面板</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![演示首页](https://filex.sh/shots/demo-landing.64f23c6f8cb3.png)

<sub>演示首页</sub>

</td>
<td width="50%" valign="top">

![管理菜单的 Settings 面板已打开，覆盖在 Admin → Settings → General 之上](https://filex.sh/shots/megamenu/settings-panel-1440.23a710915f9c.png)

<sub>管理菜单：所有页面分在 **Files & storage**（文件与存储）、**People**（人员）、**Settings**（设置：General、Security & data、File processing & integrations）和 **Maintenance & records**（维护与记录）四个菜单面板里，每个页面下面都有一行简短说明；在手机上，同一批页面放在抽屉里（[docs/ADMIN-PANEL.md](docs/ADMIN-PANEL.md)）</sub>

</td>
</tr>
</table>

</details>

<br>

## 桌面应用与 CLI

**你电脑上的文件夹与服务器实时保持一致。**

- 一个 Windows、Linux 和 macOS（Apple Silicon）应用：同一个资源管理器放在一个窗口里，多个账户或租户可以并排共存。
- 右键点击一个文件夹 → **Keep on this computer**（保留在此电脑上），它就与服务器双向保持一致，修改大约一秒就到另一边。其余的一切在窗口中保持仅在线。
- 它通过你的浏览器登录，所以 SSO 和 MFA 的表现与在 Web 上一样；它还能在你的服务器运行的编辑器中打开你本地磁盘上的 Office 文档。
- 没有图形界面的机器用的是同一个引擎：`filex sync` 和 `filex client`。

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

<details>
<summary><b>关于安装和 CLI 的更多内容</b> - Store 版本、winget、发行版下载、Linux、arm64、客户端命令</summary>

Store 版本（*filex File Manager*）是唯一经过代码签名的 Windows 版本（由 Microsoft 签名），并由 Store 负责更新。桌面应用的 winget 包（`BRFTech.filex-app`）正在等待 winget 审核人员的首次审核（在被接受之前，发行版不会改动这份提交），所以 `winget install BRFTech.filex-app` 目前还找不到它。安装程序、便携版 `.exe`、AppImage、`.deb`、`.rpm` 和 `.dmg` 都附在[最新发行版](https://github.com/BRF-Tech/filex/releases/latest)上，尚未经过代码签名，所以运行 Windows 安装程序时会出现 SmartScreen 提示。详见 [docs/DESKTOP.md](docs/DESKTOP.md)。单独安装 CLI：`brew install brf-tech/filex/filex`，在 Windows 上则用 `winget install BRFTech.filex`（[docs/CLI.md](docs/CLI.md)）。

在 Linux 上，`.deb` 和 `.rpm` 什么都不需要；在 Ubuntu 23.10 及更高版本上，AppImage 需要一个一次性设置的 AppArmor 配置文件（[AppImage on recent Ubuntu](docs/DESKTOP.md#appimage-on-recent-ubuntu)），Snap 则运行在 snap 的严格隔离（strict confinement）之中（[The snap and the sandbox](docs/DESKTOP.md#the-snap-and-the-sandbox)）。每个发行版也都会为 arm64 构建：服务器、镜像，以及 Linux、Windows on Arm 和 Apple Silicon 上的桌面应用（[docs/INSTALLATION.md → ARM (arm64)](docs/INSTALLATION.md#arm-arm64)）。这个应用能做的一切：[docs/FEATURES.md → Desktop app, phone and CLI](docs/FEATURES.md#desktop-app-phone-and-cli)。

同一个二进制文件也是一个客户端，供服务器、脚本和没有图形界面的机器使用：

```bash
filex client login --url https://files.example.com
filex client upload build/report.pdf docs://ci-artifacts/
filex client mv docs://ci-artifacts/report.pdf archive://2026/   # across storages, waits for the job
filex client run convert convert docs://data/table.csv --param target=xlsx

filex sync add ~/Documents/work docs://work   # the engine the desktop app uses
filex sync run --watch 30s
```

参见 [docs/CLI.md](docs/CLI.md) 和 [docs/SYNC.md](docs/SYNC.md)。

</details>

<br>

## 嵌入到你的应用中

**在你自己的产品里放一个真正的文件管理器。**

同一套界面发布为 Vue 3 组件、React 组件，以及与框架无关的 `<filex-explorer>` Web 组件，背后是你自己的 filex 服务器。受限令牌把它锁定在每个租户各自的文件夹内，这道限制由后端强制执行，而不是由前端组件执行。Web 组件只要两行：

```html
<script type="module" src="https://cdn.jsdelivr.net/npm/@brftech/filex/dist/filex.js"></script>
<filex-explorer api-base="http://localhost:5212" sidenav connections ui-profile="simple"></filex-explorer>
```

<p align="center"><img src="https://filex.sh/shots/sidenav/embed-webcomponent-1440.b802d5b6ab56.png" alt="嵌入的 Web 组件" width="860"></p>

<details>
<summary><b>Vue 3 和 React</b> - 另外两种封装，以及详细说明在哪里</summary>

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

样式、打包工具、`sidenav` / `connections` / `ui-profile` 属性、多租户宿主应用使用的受限令牌、来自其他源的会话 Cookie，以及从应用中打印：见 [docs/INTEGRATION.md](docs/INTEGRATION.md)，概要见 [docs/FEATURES.md → Embedding](docs/FEATURES.md#embedding)。⚠ 包和服务器要保持同一版本（[docs/API.md](docs/API.md)）。

<table>
<tr>
<td width="50%" valign="top">

![Shared with me](https://filex.sh/shots/sidenav/view-shared-1440.faf6f6668c20.png)

<sub>Shared with me（与我共享）：其他人授予你访问权限的文件夹，无需挂载说明</sub>

</td>
<td width="50%" valign="top">

![嵌入的 Web 组件](https://filex.sh/shots/sidenav/embed-webcomponent-1440.b802d5b6ab56.png)

<sub>嵌入在另一个产品的页面中</sub>

</td>
</tr>
</table>

</details>

<br>

## 应用

**对文件做新的事情，权限正好是你批准的那些。**

应用是在 filex 内部、在沙箱中运行的 WebAssembly 模块，或者是放在沙箱 iframe 里的自带界面，也可以两者兼有。你从商店链接或它的 GitHub 地址安装应用，读过它请求的每一项权限，而且没有什么会自动更新。你的 filex 上的用户可以在 **App store**（应用商店）界面上浏览一个受信任的商店，并向你申请一个应用或一个存储插件。有五个应用以公开仓库的形式发布：

| 应用 | 添加的内容 |
|---|---|
| [e-Signature](https://github.com/BRF-Tech/filex-sign) | 签署一份 PDF，或者请别人签署：这个 filex 上的人在 filex 内部签署，其他任何人通过私密链接签署，默认设有 PIN |
| [Convert](https://github.com/BRF-Tech/filex-convert) | 任何文件或所选内容上的 **Convert…**（转换）：图片、视频、音频、文档、电子书、压缩包、数据、字幕和字体 |
| [Office editor](https://github.com/BRF-Tech/filex-office-editor) | 在浏览器中用 ONLYOFFICE 的编辑器处理 Word、Excel 和 PowerPoint 文档，不需要 Document Server；它为 filex 0.56 制作的 0.2.0 和 0.3.0 版本增加了加密文件夹和多人同时编辑 |；它为 filex 0.57 制作的 0.4.0 版本能打开 Document Server 能打开的所有类型（`.doc`、`.xls`、`.ppt`、`.csv`……），并带来一个办公引擎，filex 在服务器上用它生成办公文档缩略图并进行转换（[docs/OFFICE-EDITOR.md](docs/OFFICE-EDITOR.md)）
| [filextext](https://github.com/BRF-Tech/filextext-app) | 装在单个 `.fxtxt` 文件里的端到端加密的文本工作区 |
| [draw.io](https://github.com/BRF-Tech/filex-drawio) | draw.io 图表编辑器，用于 `.drawio` 和 `.dio` 文件 |

你允许的应用可以和其他人**一起**编辑一个文件：filex 的中继在不读取内容的情况下把大家的修改排成同一个顺序，只能阅读的人可以实时旁观；应用也可以打开**加密文件夹**中的文件，由你的浏览器为它解密，并在保存前重新加密。

语言包也是一种应用：西班牙语、德语和法语作为示例提供。

几分钟就能装好一个：[docs/APP-PLUGINS.md → Install one](docs/APP-PLUGINS.md#install-one)。应用的全部功能：[docs/FEATURES.md → Apps](docs/FEATURES.md#apps)。

<p align="center"><img src="https://filex.sh/shots/signing/sign-place-1440.696a10b65be8.png" alt="在文档上放置填写框" width="860"></p>

<details>
<summary><b>应用截图</b> - 一次签名请求从头到尾、安装应用、转换器、自带界面的应用、多人一起编辑、在应用中打开的加密文件夹文件、语言包、默认应用、从商店安装</summary>

Dana 请同一个 filex 上的一位同事和 filex 之外的一位合作伙伴签署一份协议。所用的应用是 [e-Signature](https://github.com/BRF-Tech/filex-sign)；每个界面都由 filex 绘制，合作伙伴收到的链接是一个普通的共享。

<table>
<tr>
<td width="50%" valign="top">

![定义签名请求的填写框](https://filex.sh/shots/signing/sign-define-1440.63ea72b13724.png)

<sub>定义填写框：给每个填写框命名，并说明它是谁的；下一步是文档</sub>

</td>
<td width="50%" valign="top">

![在文档上放置填写框](https://filex.sh/shots/signing/sign-place-1440.696a10b65be8.png)

<sub>放置填写框：选一个填写框，在页面上轻点要放的位置</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![外部签署人的 PIN 验证页](https://filex.sh/shots/signing/sign-outside-pin-1440.2cd8ccb99483.png)

<sub>合作伙伴的链接：filex 仅有的一个公开页面，以你的实例的名义出现，凭 PIN 才能打开</sub>

</td>
<td width="50%" valign="top">

![外部签署人正在填写自己的填写框](https://filex.sh/shots/signing/sign-outside-fill-1440.9f50aeb7abef.png)

<sub>…以及链接打开的内容：只有对方自己的填写框，这里是一个用发起人选定的字体键入的姓名（另外两种是绘制和上传）</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![文档已锁定，Signatures 面板已打开](https://filex.sh/shots/signing/sign-status-1440.54c60e832ced.png)

<sub>等待签署期间：文档对每个人都是冻结的，谁已签署，在文档的详细信息里可以看到</sub>

</td>
<td width="50%" valign="top">

![安装向导的权限审核](https://filex.sh/shots/apps/apps-install-review-1440.cdb1a4ebf8f6.png)

<sub>安装应用：在任何东西运行之前，先用直白的文字列出它请求的每一项权限</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![已安装应用的详情](https://filex.sh/shots/apps/apps-detail-1440.b7f6ec845458.png)

<sub>已安装的应用：它从哪里来、指纹是什么，以及它持有的每一项权限，用直白的文字写明（它的设置和操作接在后面，在页面更靠下的地方）</sub>

</td>
<td width="50%" valign="top">

![转换器的向导](https://filex.sh/shots/apps/convert-wizard-1440.231ada006fd6.png)

<sub>转换器，另一个应用：每种目标格式都列在各自的类别下，共三步</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![自带界面的应用的安装审核](https://filex.sh/shots/apps/app-interface-review-1440.877a89c82359.png)

<sub>自带界面的应用：审核时会显示应用包的指纹、应用包之外的每个地址（实时加载的地址是一项权限，以黄色显示），以及浏览器无法保证的事</sub>

</td>
<td width="50%" valign="top">

![应用自带的界面作为文件查看器打开](https://filex.sh/shots/apps/app-interface-viewer-1440.519b23618156.png)

<sub>…以及这个界面，打开的是它自己的文件类型，就在 filex 预览本来所在的位置。它在沙箱 iframe 中通过 filex 读取和保存文件（一个小型示例应用，为这些截图而写）</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![一个文档里的两个人：一个在编辑，另一个在旁观](https://filex.sh/shots/officeapp/coedit-watcher-1280.904b31e20f36.png)

<sub>多人一起编辑：Dana 在输入，只能阅读的 Sam 通过 filex 的中继看到每一处修改和她的光标（[一起编辑](docs/APP-PLUGINS-API.md#editing-together-coedit-056)；一个为这些截图而写的示例应用）</sub>

</td>
<td width="50%" valign="top">

![加密文件夹中的一个文件在应用中打开，由浏览器解密](https://filex.sh/shots/e2eapp/app-encrypted-folder-1280.856d3586a9e2.png)

<sub>端到端加密文件夹中的一个文件，在你允许的应用中打开：你的浏览器为应用解密它，并加密保存的内容，服务器上仍然只有密文（同一个示例应用；[在应用中编辑](docs/E2E-ENCRYPTION.md#editing-in-an-app)）</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![Apps 列表，其中有一个语言包](https://filex.sh/shots/langpack/apps-list-1440.004f0c819981.png)

<sub>实例上的每个应用，其中有一个**语言包**：一份没有任何可运行内容的清单，会说明自己翻译了这个 filex 的多少内容；语言包一走，它的语言也跟着走</sub>

</td>
<td width="50%" valign="top">

![Plugins → Default apps](https://filex.sh/shots/defaultapps/default-apps-1440.0dd62b519f8f.png)

<sub>Default apps（默认应用）：每一种由 filex 之外的东西处理的文件，谁来打开它、谁来绘制它的缩略图，按你设定的顺序（[默认应用](docs/APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail)）</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![用最新文件绘制的文件夹](https://filex.sh/shots/thumbnails/folders-grid-1440.dd7653d6edca.png)

<sub>文件夹预览：每个文件夹用最近进入其中的三个文件绘制；这些 SVG 由 filex 的内置引擎绘制（[docs/thumbnails.md](docs/thumbnails.md#folder-previews)）</sub>

</td>
<td width="50%" valign="top">

![从商店链接打开的安装审核](https://filex.sh/shots/store/store-review-1440.36cf3bbc2a50.png)

<sub>从商店安装：同样的审核，标为 **From store**（来自商店），付费应用的许可证密钥只显示开头几个字符（[Installing from a store](docs/APP-PLUGINS.md#installing-from-a-store)）</sub>

</td>
</tr>
<tr>
<td width="50%" valign="top">

![来自商店的第一个链接：要信任它吗？](https://filex.sh/shots/store/store-trust-1440.8fb07862db1a.png)

<sub>来自某个商店的第一个链接：它的地址和它的密钥指纹，信任之前先比对（[受信任的商店](docs/APP-PLUGINS.md#trusted-stores)）</sub>

</td>
<td width="50%" valign="top">

![被搁置的付费应用，管理页面上的横幅](https://filex.sh/shots/store/store-license-held-1440.6fd145640e0f.png)

<sub>商店吊销了的许可证：应用被搁置，显示 *Unlicensed*（未授权），什么都没删除，每个管理页面上都有一条横幅（[付费应用](docs/APP-PLUGINS.md#paid-apps)）</sub>

</td>
</tr>
</table>

</details>

<br>

## AI 智能体 / MCP

**智能体只在你交给它的文件夹里工作，别处一概不碰。**

- 位于 `/api/ai/mcp` 的原生 **MCP 服务器**，以及位于 `/api/ai`、由一份 OpenAPI 3.1 文件描述的 REST 接口。
- API 密钥的权限按动词划分，可以限定在单个文件夹内，而且它发放出去的任何东西，权限都不会超出密钥自身。
- 大文件走**上传票据**：一个短时有效、只能用一次的 URL，所以文件的字节永远不必经过模型的上下文。

```bash
claude mcp add filex --transport http https://files.example.com/api/ai/mcp \
  --header "Authorization: Bearer <api-token>"
```

智能体能做什么、密钥永远发放不了什么、加密文件夹和上传票据：[docs/FEATURES.md → AI agents and MCP](docs/FEATURES.md#ai-agents-and-mcp)；参考文档是 [docs/MCP.md](docs/MCP.md)。

<br>

# 如果你来自 Nextcloud、Dropbox 或 Google Drive

filex 是文件管理器，不是协作办公套件。对照你可能来自的四种产品（其中包括 File Browser），这意味着：

| 来自 | 用 filex |
|---|---|
| **Nextcloud**<br>一个自己托管或从服务商处获得的协作平台：文件，以及围绕文件的日历、联系人、邮件、聊天、视频通话和在线办公。一个运行在 Web 服务器后面、带数据库的 PHP 应用。 | 单独的文件部分。<br>一个内置 SQLite 的 Go 二进制文件（想用时可换 PostgreSQL 或 MySQL）。<br>带实时文件夹同步的桌面应用、共享链接、SSO 和 LDAP。<br>办公文档可以独自或多人一起编辑，在办公编辑器应用或你连接的 ONLYOFFICE 中处理。<br>没有日历、联系人、邮件、聊天和视频通话。 |
| **Dropbox**、**Google Drive**<br>托管服务：你的文件放在服务商的服务器上，受它的配额和条款约束。 | 你自己的服务器和你已有的存储：一块磁盘、一个 NAS 共享、一个 S3 存储桶。<br>日常所需一应俱全：Web 界面、带 PIN 和过期时间的共享链接、文件请求、回收站和版本历史。<br>一个让你选定的文件夹保持同步的桌面应用。 |
| **File Browser**<br>一个二进制文件，给你指定的一个目录加上 Web 界面，带用户账户（各有自己的范围和权限开关）、按路径的允许和拒绝规则，以及带密码和过期时间的共享链接。它的仓库已归档，它的 [README](https://github.com/filebrowser/filebrowser) 说明不会再有新的发行版、错误修复或安全修复。 | 同样一条命令就能启动。<br>多个存储并列。<br>在按文件夹访问之上有角色和群组；内置 OIDC 单点登录和 LDAP。<br>回收站和版本、全文搜索。<br>同一棵文件树可以当作 S3、SFTP、FTPS、NFSv3 和 WebDAV 访问。 |

**filex 没有的东西**，无论你来自哪一种：

- **Android 或 iOS 应用。** 在手机上，filex 就是 Web 应用，它会提议像应用一样安装（[On a phone or a tablet](docs/DESKTOP.md#on-a-phone-or-a-tablet-the-web-app)），但需要联网才能显示文件；Web 应用关闭时，只要你为这台设备开启了 Web Push，通知就会以 Web Push 送达。
- **访达或资源管理器中的占位文件。** 桌面应用复制的是你保留在电脑上的文件夹，其余一切都在它自己的窗口里保持在线（[docs/DESKTOP.md](docs/DESKTOP.md#keeping-folders-on-this-computer)）。
- **内置在二进制文件里的办公编辑器。** 办公文档由你安装的办公编辑器应用编辑（[`filex-office-editor`](https://github.com/BRF-Tech/filex-office-editor)，浏览器里的 ONLYOFFICE 编辑器，不需要 Document Server），可以独自编辑，也可以多人一起编辑；或者在你连接的 ONLYOFFICE Document Server 中编辑；两者都没有时，只能以只读预览打开（[docs/ONLYOFFICE.md](docs/ONLYOFFICE.md)）。

<details>
<summary><b>迁入</b> - 什么会随你过来，什么不会</summary>

- 文件如果已经在 filex 能挂载的地方（磁盘或 NAS 共享上的文件夹、S3 存储桶里的前缀、SFTP、FTP 或 WebDAV 服务器），迁入就是一次挂载，而不是一次迁移。filex 不自己存放文件：把一个存储指向那个文件夹，文件就原样在那里了。
- 放在 Dropbox 或 Google Drive 里的文件要先复制到这样的地方：filex 没有这两者的存储驱动。
- 迁过来的只有文件：旧系统保存的链接、权限和版本历史不会导入。
- filex 添加的东西（回收站、版本历史、草稿）放在存储根目录下的隐藏文件夹里，你做了端到端加密的文件夹从那以后存的是密文（[docs/STORAGE.md](docs/STORAGE.md)、[docs/TRASH-VERSIONING.md](docs/TRASH-VERSIONING.md)）。

</details>

<br>

# 使用 Compose 或 Helm 自托管

上面的 `docker run` 足够用来试用 filex。要正式部署，[`deploy/`](deploy/) 中有现成的部署方案：

- **[`deploy/compose/`](deploy/compose/)**：Docker Compose：
  - **minimal**：filex + SQLite + 本地磁盘（一个服务，零依赖）。
  - **full**：filex + PostgreSQL + Redis + Caddy（自动 HTTPS），外加可开关的附加组件：**OnlyOffice**、**Drawio** 和一个 **S3 服务器**（Versity S3 Gateway）。用 `.env` 中的 Compose profile 逐个开启或关闭。转换由 [Convert 应用](#应用)负责，而不是边车容器。
- **[`deploy/helm/filex/`](deploy/helm/filex/)**：用于 Kubernetes 的 Helm chart（Deployment + PVC + 可选的 Ingress），随每个版本发布：`helm repo add brf-tech https://helm.brf.sh`，然后 `helm install filex brf-tech/filex`（或 `oci://ghcr.io/brf-tech/charts/filex`）。上面的每个附加组件都对应 `values.yaml` 中的一个 `enabled` 开关：一并部署 PostgreSQL / Redis / S3 服务器，或者接入外部的 OnlyOffice / Drawio。

每种部署方式的分步说明见 [docs/INSTALLATION.md](docs/INSTALLATION.md)。

filex 可以运行在独立的主机的根路径上，也可以运行在共用主机的某个路径下（`https://example.com/filex/`）：需要 `FILEX_BASE_PATH` 这一项设置，外加一个传递完整路径的代理。Caddy、nginx 和 Helm 的示例见 [docs/DEPLOYMENT.md → Serving filex under a sub-path](docs/DEPLOYMENT.md#serving-filex-under-a-sub-path)。

<br>

# 文档

指南以网站的形式发布在 [docs.filex.sh](https://docs.filex.sh)，[完整文档索引](docs/README.md)列出了每一页。

从[安装](docs/INSTALLATION.md)、[配置](docs/CONFIGURATION.md)、[桌面应用](docs/DESKTOP.md)、[协议](docs/PROTOCOLS.md)、[应用](docs/APP-PLUGINS.md)和 [ONLYOFFICE](docs/ONLYOFFICE.md) 开始。[功能](docs/FEATURES.md)一页为每项功能单独列出一条，附上它的限制和介绍它的指南。

<br>

# 功能

| 领域 | 你得到什么 |
|---|---|
| **资源管理器** | 管理端、桌面应用和每个嵌入端都用同一套布局：导航面板，列表、网格和画廊视图，标签页，个人标签和团队标签，⌘K 命令面板，可重新映射的键盘快捷键，从 **+ New**（新建）菜单创建的新文档，以及运维人员组合出的主题（[集成](docs/INTEGRATION.md)、[主题](docs/INTEGRATION.md#themes)） |
| **存储** | 本地磁盘、S3、FTP、SFTP、WebDAV 和 SMB/NAS 并排挂载，可以在它们之间复制和移动；其他任何存储都可以用插件接入，从管理面板或应用商店安装（[存储](docs/STORAGE.md)、[存储插件](docs/PLUGINS.md)） |
| **不用浏览器** | 同一棵文件树以 S3、SFTP、FTPS、NFSv3 和 WebDAV 的形式提供访问（在那里移动或删除，与存储扫描并行时和在 Web 界面里一样安全），另有基于 HTTPS 的 `filex mount`（[协议](docs/PROTOCOLS.md)） |
| **登录** | OIDC、LDAP / Active Directory、Windows 和 Linux 账户，以及登录尝试次数限制（[SSO](docs/SSO.md)、[LDAP](docs/LDAP.md)、[操作系统账户](docs/OS-LOGIN.md)） |
| **人员与访问** | 角色和按人设置的权限、群组、按文件夹的访问权限，以及原生多租户（[权限](docs/PERMISSIONS.md)、[群组](docs/GROUPS.md)、[多租户](docs/MULTI-TENANCY.md)） |
| **共享** | 带 PIN、过期时间和下载次数上限的公开链接，文件请求，以及通过电子邮件发送的共享邀请（[共享](docs/SHARING.md)） |
| **防护** | 回收站和版本历史、可选的 ClamAV 扫描、端到端加密文件夹、复制到第二个存储（已有的文件会先做一次初始复制），以及审计日志（[回收站与版本管理](docs/TRASH-VERSIONING.md)、[防护](docs/PROTECTION.md)、[端到端加密](docs/E2E-ENCRYPTION.md)、[复制](docs/REPLICATION.md)、[审计日志](docs/BACKEND.md#admin-audit-log)） |
| **查看器与编辑器** | 图片、视频、音频、PDF、Markdown、代码（Monaco）、在办公编辑器应用中或通过 ONLYOFFICE 处理的 Office 文档、draw.io 图表、可绘制也可作为文本编辑的 Mermaid 图表、3D 模型，以及 ZIP、7z 和 TAR 压缩包（[ONLYOFFICE](docs/ONLYOFFICE.md)、[压缩包](docs/ARCHIVES.md)） |
| **搜索与缩略图** | 遵循权限的内嵌全文搜索，以及图片、视频、PDF、文件夹和 Office 文档的缩略图；Office 文档的缩略图由服务器上的 Office 编辑器应用引擎或已连接的 ONLYOFFICE 绘制（[搜索](docs/SEARCH.md)、[缩略图](docs/thumbnails.md)） |
| **实时** | 基于 WebSocket 的在线状态和实时文件更新，以及通过 filex 的中继在应用中多人一起编辑文件（[实时](docs/REALTIME.md)、[一起编辑](docs/APP-PLUGINS-API.md#editing-together-coedit-056)） |
| **客户端** | 带文件夹同步的桌面应用、可以在手机上以你的实例名称和徽标安装的 Web 应用、CLI、Vue / React / Web 组件嵌入、REST API 和 MCP 服务器（[桌面应用](docs/DESKTOP.md)、[同步](docs/SYNC.md)、[CLI](docs/CLI.md)、[集成](docs/INTEGRATION.md)、[MCP](docs/MCP.md)） |
| **应用与语言** | 沙箱化的应用（e-Signature、Convert、Office editor、filextext、draw.io），通过 filex 打印，通过它的中继多人一起编辑文件，只有在你允许时才会打开加密文件夹中的文件，并在你的浏览器中解密；语言包和从右到左布局（[应用](docs/APP-PLUGINS.md)、[在应用中编辑](docs/E2E-ENCRYPTION.md#editing-in-an-app)、[RTL](docs/RTL.md)） |
| **运行** | 一个二进制文件或一个容器；SQLite、PostgreSQL 或 MySQL；一个管理面板，用一个搜索框找到每个页面、设置和记录；webhook、界面内的铃铛，以及推送到手机和浏览器的 Web Push，可选摘要；用量与费用视图（目前支持 Backblaze B2）；更新检查，补丁版本在你允许后自行安装（[数据库](docs/DATABASES.md)、[管理面板](docs/ADMIN-PANEL.md)、[通知](docs/NOTIFICATIONS.md)、[用量](docs/USAGE.md)、[更新](docs/UPDATES.md)） |

每项功能单独成条，说明它如何工作、有哪些限制，以及介绍它的页面：[docs/FEATURES.md](docs/FEATURES.md)。

<br>

# 架构

参见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。

<details>
<summary><b>各部分一览</b> - 一张图看懂驱动、协议和包</summary>

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

# filex 是怎样开发的

filex 只有一位维护者，他借助 AI 编程智能体（Claude Code）开发 filex。filex 是什么、如何工作，由维护者决定：架构、数据模型和安全模型，以及每项功能的行为。智能体在这个方向之内编写大部分代码，用的是维护者使用的语言。开发仓库中约 82% 的提交带有 `Co-Authored-By: Claude` 这一行，所以这件事并没有隐藏。

它是 AI 辅助开发的，而不是未经审查的生成代码：

- 每项变更在合入之前都要走完测试链：Go 测试（也在竞态检测器下运行）、约 500 个 Vitest 文件、约 120 个在 Chromium、Firefox 和 WebKit 上运行的 Playwright 规格、Cypress，以及在 SQLite、PostgreSQL 和 MySQL 上运行的数据库测试。每个发行版都只有在 GitHub 的完整矩阵和发布工作流的一次试运行在那个确切的提交上通过之后，才会打标签。
- 涉及登录、权限、共享和加密的变更会额外经过几轮审查；测试变红时，修的是问题，而不是放宽测试。
- 安全报告公开处理：参见 [SECURITY.md](SECURITY.md) 和已发布的安全公告。目前还没有过独立审计。
- 德语、西班牙语、法语和中文的 README，以及德语、西班牙语和法语语言包，都是机器翻译，还没有经过母语人士审阅。

# 参与贡献

[![CI](https://img.shields.io/github/actions/workflow/status/BRF-Tech/filex/ci.yml?branch=main&label=ci)](https://github.com/BRF-Tech/filex/actions)

欢迎提交 issue 和 pull request；提交较大的 pull request 之前，请先开一个 issue，说明你打算做什么。[docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) 介绍了工作流程、变更必须通过的测试和文档规则，项目遵循 [Contributor Covenant](CODE_OF_CONDUCT.md)。安全问题请按 [SECURITY.md](SECURITY.md) 所述私下报告。每个发行版的变更见 [CHANGELOG.md](CHANGELOG.md)。每天伊斯坦布尔时间 10:00，只要 `main` 有新内容，就以当时的 `main` 切出一个发行版；在那之后合并的 pull request 随第二天的发行版发布（[Release process](docs/CONTRIBUTING.md#release-process)）。

<details>
<summary><b>开发</b> - 从源代码构建，以及各部分放在哪里</summary>

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
- `e2e/`：Playwright 测试套件（Web、嵌入端、打包后的桌面应用）+ `shots/`，即 `pnpm shots` 运行的脚本，用来截取上面的截图（截图发布在 filex.sh 上，不保存在仓库里）
- `docker/`：Dockerfile + compose
- `deploy/`：现成的 Compose 部署方案 + Helm chart（见 [`deploy/`](deploy/)）
- `docs/`：Markdown 文档
- `docs-site/`：发布在 [docs.filex.sh](https://docs.filex.sh) 的 VitePress 站点

</details>

<br>

# 许可证

MIT，参见 [LICENSE](LICENSE)。

[`docs/badges/`](docs/badges/) 中的商店徽章是各商店自己的图稿，未经修改直接使用，且不在该许可证的涵盖范围内：Microsoft 和 Microsoft Store 徽章是 Microsoft 集团公司的商标；Snap Store 徽章 © Canonical Ltd.，采用 [CC BY-ND 2.0 UK](https://creativecommons.org/licenses/by-nd/2.0/uk/) 许可协议。

Nextcloud、Dropbox、Google Drive、File Browser 和 ONLYOFFICE 是其他方的产品名称，这里只用于描述这些产品。filex 与它们均无关联，也未获得它们的赞助或认可。
