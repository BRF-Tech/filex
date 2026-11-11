# Mail and page templates

Every mail filex sends and every page it shows somebody who opens a link is a
**template** you can change: *Admin → Settings → **Templates***. filex ships one
for each; the install's administrator can replace any of them for the whole
install, and on a [multi-tenant](MULTI-TENANCY.md) install a tenant's
administrator can replace them for their own tenant. **Back to the default**
removes your text again.

There are two layers, and most installs only need the first:

1. **The theme** - the colours, the logo, the name and the footer line - is
   the [Branding](CONFIGURATION.md#branding-white-label) page's. Every
   template wears it: the mails and the pages pick up the accent colour, the
   logo and the footer text without you touching any HTML.
2. **The templates** - the full HTML and CSS of each mail and page, written in
   [Liquid](https://shopify.github.io/liquid/), with a live preview and a test
   mail. This page is about this layer.

- [What can be changed](#what-can-be-changed)
- [Who edits which templates](#who-edits-which-templates)
- [The editor](#the-editor)
- [Writing a template](#writing-a-template)
- [The public link page](#the-public-link-page)
- [Mails](#mails)
- [Security](#security)
- [API](#api)

## What can be changed

| Template | What it is | Its own variables |
|---|---|---|
| `mail.layout` | The frame of **every** mail: header, logo, footer, the stylesheet. `{{ content }}` is the mail's own part. | `content` |
| `mail.share` | A share link sent by mail, or the link an invitation made. | `item.name`, `item.kind` (`file` / `folder`), `item.size`, `item.size_text`, `link.url`, `link.pin`, `link.pin_withheld`, `link.days_left` |
| `mail.file_request` | A file request sent by mail. | `folder.name`, `link.url`, `link.pin`, `link.pin_withheld`, `link.days_left`, `limits.max_files`, `limits.max_file_mb`, `limits.types` |
| `mail.grant` | An existing account was given access to an item. | `item.path`, `link.url` |
| `mail.account` | A new account, with its first password. | `account.email`, `account.password`, `link.url` |
| `mail.notification` | Any [notification](NOTIFICATIONS.md) that asks for a mail. | `notice.event`, `notice.title`, `notice.lines`, `link.url` |
| `mail.digest` | A notification digest. | `notice.title`, `notice.lines`, `digest.minutes`, `link.url` |
| `mail.smtp_test` | The SMTP **Test** button's mail. | - |
| `mail.cloud_verify` | The [cloud](CONFIGURATION.md) sign-up code. | `tenant.slug`, `verify.token`, `verify.ttl` |
| `mail.app` | A mail an [app](APP-PLUGINS.md) sends: what the app wrote, framed like every other mail. | `app.name`, `message.subject`, `message.body` |
| `page.layout` | The frame of every page below: the document, the base stylesheet, the logo and the footer. | `content` |
| `page.pin` | A link's PIN gate (shares and file requests). | `form.action`, `form.error`, `link.kind` |
| `page.unlocked` | "PIN accepted" before the download starts. | - |
| `page.error` | "This link is not available", and the other error pages. | `error.code`, `error.key`, `error.title`, `error.body` |
| `page.zip_wait` | "Preparing your download" while a folder's ZIP is built. | `folder.name`, `zip.url` |
| `page.folder` | A shared folder, for a browser without JavaScript. | `folder.*`, `entries` (each `name`, `kind`, `url`, `size`, `date`, `thumb`) |
| `page.file_request` | A file request's upload page, for a browser without JavaScript. | `folder.name`, `form.name_max` |
| `page.app` | An app's link, for a browser without JavaScript. | `app.title`, `files` |
| `page.delete_confirm` | An [uploader](UPLOADERS.md)'s deletion link (`/x/…`): which file, one button. | `file.name`, `form.action` |
| `page.delete_done` | The deletion link after the file was deleted. | - |
| `page.cache_wait` | "Preparing your download" for a big file on slow storage. | `file.name`, `file.percent` |
| `page.shell` | The document the public link page is drawn in ([below](#the-public-link-page)). | `app_head`, `app`, `link.kind` |
| `slot.share.title`, `.intro`, `.footer` | Short texts on a share link's page. Empty by default: the page says its own words. | `item.*`, `link.kind`, `link.expires_at`, `link.message` |
| `slot.file_request.title`, `.intro`, `.footer` | The same for a file request. | the same |

Every template also has `lang`, `dir` (`ltr` / `rtl`), `brand.name`,
`brand.logo_url`, `brand.footer_text`, `brand.hide_powered_by`,
`theme.accent`, `theme.accent_hover`, `theme.on_accent`, `product.name`,
`product.url`, `product.link`, `site.name` and `site.url`; a mail has
`mail.subject`, a page `page.title` and `theme.css` (the page's theme and
accent stylesheet). The editor lists every variable of the template you have
open, and puts one in where the cursor is when you click it.

**Not a template:** the pages filex answers an [app's public
address](APP-PLUGINS.md#short-links) with by itself (an address that leads
nowhere, too many tries, unavailable) and the page an app draws there (a
short link's `+` preview). They are filex's own, in the visitor's language,
with nothing loaded from anywhere else: an app's host of its own serves the
app and nothing else of filex. Editing them is not possible yet.

## Who edits which templates

| You are | You edit | Who it reaches |
|---|---|---|
| The install's administrator (single-tenant, or the platform's own tenant) | the **install's** templates | every mail and page of the install, and every tenant that has not replaced the same template |
| A tenant's administrator | **your tenant's** templates | your tenant's mails and the pages of your tenant's links |

A tenant's template wins over the install's, the install's over filex's own.
Nothing in the API names a tenant: the server reads whose templates you edit
from your session, so there is no id to change. Deleting a tenant removes its
templates.

**Which tenant a public page belongs to.** A page of a **link** - its PIN
gate, its folder, its upload page, its ZIP, a deletion link's confirmation,
the public link page - wears the
templates and the branding of the tenant the link was made in, whatever
address it is opened at. A page that belongs to no link, and **every "this
link is not available" page**, wears the address's: a visitor learns nothing
about whose link a token was from the page that says it is not available.
A mail wears the tenant of the link it is about, else the addressee's, else
the sender's.

The public link page (the application) follows the same rule from its first
paint: the document's title, theme colour and icon, the logo, the name and
the colours above the card are the link's tenant's
(`GET /api/public/{s,d}/{token}/branding`), and a token that names no live
link gets the address's.

## The editor

![Admin, Settings, Templates: the mails and pages, the share mail's source in the code editor with a sign-off added, and the preview the server rendered (dark theme)](https://filex.sh/shots/templates/templates-editor-dark-1440.381c86aa34ed.png)

- **The source** is filex's code editor - the one the file preview edits
  code in (Monaco): Liquid and HTML in colour, line numbers, the usual
  keyboard (Tab indents, Ctrl+Z, Ctrl+F). When the engine refuses the draft,
  the line it names is marked in the editor (and scrolled into view), with
  the engine's words on hover and under the preview. A build of the explorer
  without Monaco shows a plain text box instead.
- **HTML / Subject / Plain text** - a mail has three parts. The plain-text
  part is optional: left empty, it is made from the HTML (see
  [Mails](#mails)).
- **Variables** - every variable the template has; click one to insert it.
  The *hooks* line names what the page's own script needs (see
  [Hooks](#hooks)).
- **Preview** - rendered by the server, in the same sandbox the real renders
  use, with sample data, in the language you pick, and shown in a frame that
  runs no script. A mail shows its subject and both parts. What the sanitizer
  had to take out (a `<script>`, an `onclick`) is listed: it will not reach
  anybody.
- **Save** - checks the template first: it must render with the sample data
  **strictly** (a variable that does not exist is an error, with its line), and
  a page must keep its hooks. A refused save says why, in the template
  engine's words. Two administrators saving over each other: the second is told
  the template changed and opens it again.
- **Start from the default** puts filex's own text in the editor;
  **Back to the default** removes your text.
- **Send a test mail to me** sends the draft, with the sample data, to your
  own account's address - and only there.

If a saved template fails later anyway - a time or memory ceiling with real
data, a sentence a language pack no longer has - filex uses its own template
for that mail or page, the template's row shows when and why it failed, and
the server's log says it too. A broken template never costs somebody their
mail or a visitor their page.

![A misspelled variable: the line the engine refused is marked in the editor, and its words are under the preview (dark theme)](https://filex.sh/shots/templates/templates-error-dark-1440.957929a9072d.png)

## Writing a template

Templates are [Liquid](https://shopify.github.io/liquid/) (the
[osteele/liquid](https://github.com/osteele/liquid) dialect, close to
Shopify's): `{{ variable }}`, `{% if %}`, `{% for %}`, `{% assign %}`,
`{% capture %}`, `{% case %}`, `{% raw %}`, `{% comment %}` and the standard
filters (`upcase`, `truncate`, `date`, `default`, `join`, `size`…).

### Escaping

Every `{{ }}` is **HTML-escaped**. A file name, a message, anything a stranger
typed, is shown as text and never becomes markup, whatever the template does
with it. There is no way around it from a template: `safe` does nothing.
`escape`, `escape_once`, `newline_to_br` and `capture` escape exactly once
(their output is not escaped a second time).

### Texts from the language packs

The default templates say nothing of their own: every sentence is one of the
server catalogue's (`server.mail.*`, `server.public.*`), so a
[language pack](PLUGIN-KIT.md) translates them, in the reader's language.

| Filter | What it does |
|---|---|
| `{{ 'mail.greeting' \| t }}` | A sentence, without the `server.` prefix. |
| `{{ 'mail.share.subject_file' \| t: name: item.name }}` | With its `{placeholders}` filled. |
| `{{ 'mail.valid_days' \| t: count: link.days_left }}` | `count` picks the plural form. |
| `{{ 'public.zip_hint' \| t_html: link: a }}` | A sentence with markup in a placeholder: the sentence is escaped, `a` (made by `link_to`) is put in as it is. |
| `{{ label \| link_to: url, 'id' }}` | An `<a>` around the escaped text. An address that is not `http(s)`, `mailto:`, `tel:` or relative becomes `#`. |
| `{{ n \| bytes }}` | A size in the reader's language (`2.4 MB`). |

A sentence that does not exist is an error (the save says which). To change
the words, write your own text instead of `t` - in one language, or in several
with `{% if lang == "tr" %}…{% endif %}`.

`blank` and `empty` are the empty text (`{% if brand.logo_url != blank %}`); a
list is asked with `.size` (`{% if entries.size == 0 %}`).

### Hooks

Some pages run a script of filex's own - the ZIP and download progress, the
upload page. The script is not part of the template (no template can carry a
script, see [Security](#security)); it is added after the template rendered,
and it finds its way by element ids. A page template must keep them, and a
save without them is refused:

| Template | Hooks |
|---|---|
| `page.pin` | a field `name="pin"` in the form |
| `page.delete_confirm` | a field `name="confirm"` in the form (the button's POST deletes only with it) |
| `page.zip_wait` | `#bar`, `#pct`, `#dl` |
| `page.cache_wait` | `#bar`, `#pct` |
| `page.file_request` | `#card`, `#sub`, `#drop`, `#hint`, `#file`, `#nameField`, `#uploaderName`, `#note`, `#files`, `#bar`, `#barfill`, `#msg`, `#send`, `#foot` |
| `page.shell` | `{{ app_head }}` and `{{ app }}`, once each |

### Shared pieces

`{% include 'name' %}` reads only the pieces filex registers with a template;
there is no file system behind it. The layouts (`mail.layout`, `page.layout`)
are the way to share markup across templates.

## The public link page

A browser with JavaScript opening `/s/…` or `/d/…` gets the public link page
(the PIN gate with its bot check, the preview, the folder gallery, the upload,
the language picker). It is an application, and three things about it are
templates:

- **`page.shell`** is the document it is drawn in: put your `<style>` (or a
  stylesheet `<link>`) in the head after `{{ app_head }}`, and markup of your own
  around `{{ app }}` - a header, a footer, a background.
- **The card is restyled by that CSS** through the class names below. They
  are a public contract: they keep their names across releases (a test holds
  them; a rename would be a breaking change in the CHANGELOG).
- **Text slots** - `slot.share.*` and `slot.file_request.*`: a title in place
  of the heading, an introduction under it and a line in the footer, rendered
  on the server in the visitor's language. Like the heading, the title and the
  introduction appear only once the link is open (after the PIN), and the
  item's name is given to them only then.

![A shared link's page with the administrator's introduction above the file and their footer line under it](https://filex.sh/shots/templates/templates-link-page-1440.fb8b605394fb.png)

| Class | What it is |
|---|---|
| `.fe-ppage` | The page (its root). `data-state` is `loading`, `pin`, `ready`, `accepted`, `done`, `error` or `unavailable`; `data-layout` is `gate`, `form` or `wide`. |
| `.fe-ppage__brand` | The logo and the name above the card. |
| `.fe-ppage__logo` | The logo. |
| `.fe-ppage__brandname` | The name. |
| `.fe-ppage__card` | The card. |
| `.fe-ppage__badge` | The round badge the card opens with; `.fe-ppage__badge--ok`, `.fe-ppage__badge--err`. |
| `.fe-ppage__head` | The heading block. |
| `.fe-ppage__title` | The heading. |
| `.fe-ppage__subject` | The line under it (the sender's message). |
| `.fe-ppage__intro` | The introduction slot. |
| `.fe-ppage__meta` | Expiry and visits left. |
| `.fe-ppage__slot` | Every text slot (`data-slot` is `title`, `intro` or `footer`). |
| `.fe-ppage__pin` | The PIN gate's form. |
| `.fe-ppage__pinput` | The PIN field. |
| `.fe-ppage__actions` | A row of buttons (download, open). |
| `.fe-ppage__crumbs` | A folder's path. |
| `.fe-ppage__files` | A shared folder's list. |
| `.fe-ppage__file` | One row of it (`data-dir="1"` for a folder). |
| `.fe-ppage__entry` | The row's link. |
| `.fe-ppage__state` | A state of the page's own ("accepted", "this link is not available"); `.fe-ppage__state--ok`. |
| `.fe-ppage__state-title` | Its heading. |
| `.fe-ppage__error` | A failure, in the server's words. |
| `.fe-ppage__foot` | The footer: the language picker, the footer text, the attribution. |
| `.fe-ppage__footslot` | The footer slot. |
| `.fe-ppage__footline` | The branding's footer text. |
| `.fe-ppage__footmark` | The "Served by" line. |

## Mails

A mail is sent as `multipart/alternative`: a **plain-text** part and the
**HTML** part, both quoted-printable (no line over SMTP's limit), with `Date`,
`Message-ID` and `Content-Language` headers.

- **The CSS is written into the elements.** Several mail programs drop
  `<style>`, so every rule of the template's stylesheets that can apply to an
  element is put in its `style` attribute, by specificity, as a browser would.
  What cannot be - `@media` (dark mode, a narrow screen), `:hover` - stays in a
  `<style>` for the programs that read one; a `<style data-inline="keep">` is
  left as written.
- **The plain text is made from the HTML** unless the template has a
  plain-text body of its own: paragraphs, `- ` list items, a link followed by
  its address. Two attributes say what the HTML means in plain text:
  `data-text="skip"` leaves an element out (an address under a button would be
  said twice), and `data-text="href"` on a link writes its address alone.
- **The language** is the reader's: an account's own, else the one picked for
  the address in the form, else the install's default.
- **Images** in a mail need an absolute `https://` address; a logo given as a
  `data:` URI is shown by few mail programs (the default layout then shows the
  name).

## Security

Templates are written by administrators - on a multi-tenant install, by the
administrator of any tenant - and the pages are opened by strangers, on the
same address as everybody's sessions. So:

- **No template can run a script, for anybody** (the install's administrator
  included). What a template renders passes a sanitizer that removes
  `<script>`, `<iframe>`, `<object>`, `<embed>`, `<base>`, event handlers
  (`on…`), `javascript:` and other script addresses, `<meta http-equiv>`, SVG
  animation, and a form posting elsewhere. Every page is then served with a
  Content-Security-Policy whose `script-src` is a nonce made for that response
  and given to filex's own script alone, `form-action 'self'`, and no frame,
  `<object>` or `<base>` (the public link page: that nonce and
  `'strict-dynamic'`, `base-uri 'self'`, and `object-src 'self'` for a PDF's
  preview). The text slots are sanitized again in the browser before the
  page draws them.
- **An edited template runs in a sandbox.** Liquid has loops and string
  building, and no Go Liquid library bounds them: measured, `{% for i in
  (1..100000000) %}{% endfor %}` ran past every deadline in-process and a string
  doubled thirty times asked for ten gigabytes. Every template an administrator
  saved is rendered in a WebAssembly instance (the same engine compiled to
  `wasip1`, in [wazero](https://wazero.io)): a fresh instance per render, at
  most 64 MiB of memory, 1 s for a page and 3 s for a mail, 1 MiB of output,
  two at a time, no file and no network. A template that reaches a ceiling, or
  fails at all, is replaced by filex's own for that render. filex's own
  templates are part of the program and render in-process.
- **Templates are 128 KiB at most**, and `include` reads only registered
  pieces (the library's default would read files from the server's working
  directory).
- **Tenants are kept apart**: a tenant's templates are read and written with
  its id, set from the session; the cache of rendered pages is keyed by
  template revision and the whole of the data.

The sandbox's module (`backend/internal/templates/sandbox/engine.wasm.gz`) is
built from the repository by a pinned, reproducible Docker build
(`sh backend/internal/templates/sandbox/build.sh`); a test is red when the
sources changed and the module was not rebuilt.

## API

`/api/admin/templates` (an administrator; the scope is the session's):

| Call | What it does |
|---|---|
| `GET /api/admin/templates` | Every template: kind, layout, hooks, variables, whose text is in force (`source`: `tenant`, `instance`, `default`), its revision and last failure. |
| `GET /api/admin/templates/{key}` | One template: the text in force for you, and filex's own. |
| `PUT /api/admin/templates/{key}` | Save `{subject, body, text, revision}` (the revision you opened; `409 template_changed` when somebody saved in between). Refused with `422 template_invalid`, `template_hooks_missing`, `template_too_slow` or `template_stopped` (a ceiling of the sandbox) and the engine's words, `413 template_too_large` over 128 KiB. |
| `DELETE /api/admin/templates/{key}` | Back to the default. |
| `POST /api/admin/templates/{key}/preview` | Render a draft `{subject, body, text, lang}` with sample data. |
| `POST /api/admin/templates/{key}/test` | Send it to your own address. |

The public link page reads its slots from `GET /api/public/s/{token}/slots`
and `GET /api/public/d/{token}/slots` (in the visitor's `Accept-Language`), and
its identity from `GET /api/public/s/{token}/branding` and
`GET /api/public/d/{token}/branding`: the link's tenant's branding, or the
address's for a token that names no live link
([BACKEND.md](BACKEND.md#get-apipublicstokenbranding-get-apipublicdtokenbranding-)).

Saves, resets and test mails are in the [audit log](ADMIN-PANEL.md)
(`template.update`, `template.reset`, `template.test`). The templates are kept
in the `templates` table (migration 00137).
