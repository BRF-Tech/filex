# Right-to-left languages

filex lays its whole interface out right to left for Arabic, Hebrew,
Persian, Urdu and every other right-to-left language a
[language pack](PLUGIN-KIT.md) adds — the explorer, the admin panel, the
public share, PIN and file-request pages, the signing wizard and the
embeddable components. Nothing has to be switched on: install a pack for a
right-to-left language, pick it, and the screens turn.

This page is for three readers: the **administrator** who wants to know what
changes, the **translator** writing a pack, and the **contributor** writing
interface code.

## What decides the direction

**The server does, from one list.** Whether a language is written right to
left is answered by `wire.IsRTL` (`backend/pkg/pluginkit/wire/langpack.go`):
a list of primary language subtags (`ar`, `fa`, `he`, `ur`, `ps`, `sd`, `ug`,
`yi`, …) and of script subtags that override it (`az-Arab` is right to left,
`ar-Latn` is not). The answer reaches the browser as the `rtl` flag on each
row of the offered-language list (`GET /api/public/branding` → `ui_locales`),
and the interface reads that flag and nothing else — it keeps no second list,
and it does not ask the browser (`Intl.Locale#getTextInfo` is a different
list, and Firefox does not have it). English and Turkish, the two built-in
languages, are left to right; so is any code the server does not offer.

## The rule: `dir` follows the language whose words are on screen

- **A whole page** (the admin panel, the drive, the public pages) carries
  `dir` on `<html>`, and it is *derived from* `<html lang>`: whatever sets the
  page's language sets its direction with it (`syncDocumentDir`,
  `packages/core/src/lib/direction.ts`). The layout turns in the same frame as
  the words.
- **An embedded explorer** (`<FileExplorer>`, the web component, the React
  wrapper) carries `dir` on its own root, from **its own `locale`** — not from
  the host page. An Arabic explorer inside an English page reads right to
  left; an English explorer inside an Arabic page reads left to right. Laying
  English out mirrored because the page around it is Arabic (or the reverse)
  scrambles exactly the words the component is there to show, and the host
  already chose the component's language when it set `locale`.
- **Menus and popovers** that the explorer draws under `<body>` (the context
  menu, the column menu, filter popovers, the tour, the quick-look hint) carry
  `dir` again, because `<body>` is outside the explorer and would hand them the
  host page's direction.

A language whose list has not arrived yet is laid out left to right until it
does; the admin panel shows English words during the same moment, so the words
and the layout turn together.

## What mirrors, and what never does

Mirrored: the order of everything along a line (the navigation panel moves to
the right, the details panel to the left, the Actions column of a table to the
left edge and the Name column to the right edge), text alignment, paddings and
borders, the frozen columns' edges, slide-in animations, switch knobs, and the
icons that **mean a direction** — back and forward arrows, the breadcrumb
separator, a collapsed row's chevron, "go into", undo, sign-out, send, and the
panel icons for the navigation and details panels.

Gestures turn with it: dragging a column's edge grows the column the way the
pointer goes, a column dropped on the right half of its neighbour lands before
it, arrow keys move things the way the arrow points (← is "next" in a
right-to-left interface), and menus open toward the line's end — down-left of
the pointer — and flip at the screen's edge.

**Never mirrored:**

- **Document space.** A PDF page, an image, a drawing, a signature — anything
  with coordinates of its own. A signature box placed at the left of a page is
  at the left of that page in every language; the PDF field editor positions
  boxes in the page's own coordinates and the signer stamps them there.
- **Machine text** — commands, paths, URLs, addresses, keys — reads left to
  right inside a right-to-left page (`pre`, `code`, `kbd`, `samp`), so a
  connection guide's `sftp -P 2022 …` can still be typed back.
- Things that do not point along the line: "open in a new tab" (up and out),
  refresh and rotate (a clock turns the same way in every script), the
  up/down chevrons, a tick, and the play triangle (media plays the same way
  everywhere).

## Mixed-direction text

File names, folder names, paths, people and email addresses are the
*person's* text, not the interface's. Wherever the interface prints one it is
isolated (`<bdi>`), so its own letters decide its direction: `2026
report.pdf` keeps its number in front in an Arabic list, and an Arabic file
name keeps its order in an English one.

Inside a right-to-left sentence the interface also isolates, invisibly, what
must read left to right — a number pair (`3 / 10`, `1.2 MB / 5 GB`), a
`word:value` token such as the search syntax `tag:…` or `root:<storage>://<folder>`,
and an absolute path (`/var/lib/filex/report.pdf`) — and every value
interpolated into a sentence (a file name in "Could not upload «…»"). This
happens where the words are drawn; a translator writes plain text.

**And text filex did not write goes through the same gate.** A sentence the
SERVER composed (`server.*` — it reaches the browser as the `message` of a
refusal), an installed app's own words, a notification built out of a row:
none of it passes through `useLocale().t` or the admin panel's
post-translation hook, and it is the text most likely to name a path, a URL or
a command. `lib/direction` **`foreignText(locale, text)`** is the one function
for it — `lib/errorWords` calls it for every failure it says (the reader's
direction rides on the translator, `T.foreign`), `api/client.ts` for the
message of a refusal, and `useNotificationText` for the bell and the browser
notification.

> ⚠ Measured in the Arabic panel (v0.43.0): `server.token.scope_unknown` names
> `root:<storage>://<folder>`, and without isolation the closing `>` took the
> paragraph's direction, was drawn at the far left of the run and mirrored into
> `<` — the line read `…<root:<storage>://<folder`. The catalogue's own copy of
> the same sentence was correct, which is what made it read as a broken pack.
> A pack cannot fix it: bidi controls are forbidden in a translation and the
> validator refuses them. `e2e/tests/126-rtl-server-text.spec.ts` measures the
> drawn fragments; `web/tests/ui/rtlServerText.test.ts` holds each path to the
> one function.

Lists typed into one field (recipients, extensions, tags, identities) accept
the Arabic comma `،`, the ideographic `、` and the fullwidth `，` as well as
`,`, and the Arabic `؛` wherever `;` separates.

## For translators

- Write plain text. Do not add `U+200E`/`U+200F` marks or `dir` markup to
  strings — the interface isolates names, number pairs, `word:value` tokens
  and paths itself, in the text the server writes as well as on screen.
- Keep placeholders (`{name}`, `{n}`) as they are; put names in your
  language's quotation marks (`«{name}»`) as you would in prose.
- A pair written with the word for "of" (`{n} من {m}`) reads naturally; a pair
  kept as `{n} / {m}` is drawn left to right.
- Arrows in instructions ("Connections → API keys") are text: use the arrow
  that points the way your reader reads (`←`).

## For contributors

The interface is written in **logical** directions, which turn by themselves
under `dir="rtl"` and are the very same pixels under `dir="ltr"`:

| Instead of | write |
|---|---|
| `margin-left`, `padding-right`, `border-left` | `margin-inline-start`, `padding-inline-end`, `border-inline-start` |
| `left:` / `right:` | `inset-inline-start:` / `inset-inline-end:` |
| `text-align: left` / `right` | `start` / `end` |
| `float: left` | `float: inline-start` |
| `border-top-left-radius` | `border-start-start-radius` |
| `margin: 0 8px 0 4px` | `margin-block: 0; margin-inline: 4px 8px` |
| `ml-2 pr-4 left-0 text-right rounded-l border-r` | `ms-2 pe-4 start-0 text-end rounded-s border-e` |
| `translateX(8px)`, `box-shadow: -1px 0 …` | `calc(8px * var(--filex-dir-x, 1))` (set on every `[dir]`, top of `base.css`) |
| `-translate-x-full`, `origin-top-right` | add the `rtl:` partner (`rtl:translate-x-full`, `rtl:origin-top-left`) |

- **Gestures** use the helpers in `packages/core/src/lib/direction.ts` —
  `dirOfElement`, `inlineSign`, `inlineKeyStep`, `inlineStartX` /
  `inlineEndX`, `openAlongInline`, `clampAlongInline` — never a component's
  own `dir === 'rtl' ? … : …`. `scrollLeft` is negative in a right-to-left
  scroller; read its magnitude.
- **Teleported surfaces** bind `:dir="dir"` from `useLocale()`.
- **User text** goes in `<bdi>`.
- **A directional icon** is declared where the glyph is drawn:
  `lib/actionIcons.ts` (`DIRECTIONAL`), the icon list in `base.css`, or the
  lucide list in `web/src/styles/main.css`.
- **Physical is right** only for document space, a symmetric centring
  (`left: 50%` + `translateX(-50%)`), and a viewport coordinate a script
  computed with the helpers above. Mark a CSS line
  `/* rtl-physical: <why> */`; list a template or script line in `ALLOW` in
  `web/tests/quality/rtlLogical.test.ts`, with the reason.

`web/tests/quality/rtlLogical.test.ts` reads the sources and fails on a
physical direction anywhere else; `web/tests/ui/rtlGeometry.test.ts` pins the
gestures in both directions and `web/tests/lib/direction.test.ts` the
helpers.
