/**
 * Markdown as markup that is safe to draw: the ONE pipeline every surface that
 * shows somebody's Markdown uses - the explorer's preview, and an app's release
 * notes on the admin panel's update review (#122).
 *
 * markdown-it with inline HTML (the GitHub/GitLab contract: `<img>`, tables,
 * `<details>` work), then the preview sanitizer's `document` policy
 * (lib/sanitizeHtml.ts, DOMPurify): nothing that runs or submits survives.
 *
 * ⚠ markdown-it is an optional peer of this package, loaded when first needed.
 * When it cannot be loaded the answer is `null` - the caller shows the text as
 * text, never the raw string as markup.
 */
import { sanitizeHtml } from './sanitizeHtml';

type MarkdownIt = { render(src: string): string };
type MarkdownItCtor = new (opts: Record<string, unknown>) => MarkdownIt;

let renderer: Promise<MarkdownIt | null> | null = null;

function load(): Promise<MarkdownIt | null> {
  if (!renderer) {
    renderer = (import(/* @vite-ignore */ 'markdown-it') as Promise<unknown>)
      .then((mod) => {
        const m = mod as { default?: MarkdownItCtor } & MarkdownItCtor;
        const Md = (m.default ?? m) as MarkdownItCtor;
        return new Md({ html: true, linkify: true, breaks: true, typographer: true });
      })
      .catch(() => {
        // Not installed (an embed without the peer): ask again next time, it
        // may be a transient chunk-load failure.
        renderer = null;
        return null;
      });
  }
  return renderer;
}

/** `text` rendered as Markdown and sanitized; null when markdown-it is unavailable. */
export async function markdownToSafeHtml(text: string): Promise<string | null> {
  const md = await load();
  if (!md) return null;
  return sanitizeHtml(md.render(text), 'document');
}
