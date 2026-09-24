/**
 * How much room an outward-facing body needs.
 *
 * ⚠ The public page has ONE look and three widths — the three the Go-rendered
 * pages had until v0.42.2 (`.card` 400px centred, the drop card 520px, and
 * `.card--folder` 880px). Which one a link gets is decided HERE, from the
 * kind the server answered with, so the page that renders a link and the
 * preview an operator is shown on the Corporate identity page cannot come to
 * different conclusions — and so that no body carries a layout of its own
 * (the whole reason the three pages drifted apart the first time).
 *
 * ⚠ Every state that is not `ready` is drawn as a `gate` whatever this says:
 * a PIN box, "this link is not available" and "thank you" are short, and the
 * shell forces it (`PublicShell.layout`).
 */
export type PublicLayout = 'gate' | 'form' | 'wide';

/**
 * `app` gets the wide card (a wizard with a document in it) and so does a
 * folder that has a LISTING to show; a file request gets the middle one,
 * because it is a form with labels; a single document — and anything
 * unrecognised — gets the narrow centred card, which is the one the
 * reference screen is.
 *
 * ⚠ `list` is why a folder is not simply wide. The server does not send a
 * listing for every folder share yet, and an 880px card holding a heading
 * and two buttons is a worse page than a 520px one holding the same thing.
 * It widens the moment entries arrive, which is what the old `.card--folder`
 * was for.
 */
export function publicLayoutFor(
  kind: string | null | undefined,
  opts: { list?: boolean } = {},
): PublicLayout {
  if (kind === 'app') return 'wide';
  if (kind === 'folder') return opts.list ? 'wide' : 'form';
  if (kind === 'drop') return 'form';
  return 'gate';
}
