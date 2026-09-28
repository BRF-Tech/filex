// Markup a preview must render inert: the vector list the preview
// sanitizer (packages/core/src/lib/sanitizeHtml.ts) is held to, and the
// check a rendered preview is judged by. Shared by the unit test of the
// sanitizer and the component tests of the surfaces that use it.
//
// Every payload only sets a harmless marker (`window.__fxPreview`); the
// tests assert on the STRUCTURE of what is left, because happy-dom runs no
// inline handler — a structural check is what holds in every environment.

export const VECTORS: Record<string, string> = {
  'img onerror': '<img src="x" onerror="window.__fxPreview=1">',
  'img onerror, slash-separated': '<img/src=x/onerror=window.__fxPreview=1>',
  'svg onload': '<svg onload="window.__fxPreview=1"><circle r="1"></circle></svg>',
  'javascript: link': '<a href="javascript:window.__fxPreview=1">j1</a>',
  'javascript: link, unquoted': '<a href=javascript:window.__fxPreview=1>j2</a>',
  'javascript: link, entity-split': '<a href="jav&#x09;ascript:window.__fxPreview=1">j3</a>',
  'iframe srcdoc': '<iframe srcdoc="<p>framed</p>"></iframe>',
  'iframe srcdoc, unclosed': '<iframe srcdoc="<p>framed</p>">',
  'details ontoggle': '<details open ontoggle="window.__fxPreview=1"><summary>s</summary>t</details>',
  'math mutation': '<math><mtext><table><mglyph><style><!--</style><img title="--&gt;&lt;img src=x onerror=window.__fxPreview=1&gt;">',
  'math mutation, form': '<form><math><mtext></form><form><mglyph><svg><mtext><style><path id="</style><img onerror=window.__fxPreview=1 src>">',
  'data: HTML link': '<a href="data:text/html;base64,PHA+ZGF0YTwvcD4=">d1</a>',
  'data: HTML object': '<object data="data:text/html,<p>o</p>"></object>',
  'data: HTML embed': '<embed src="data:text/html,<p>e</p>">',
  form: '<form action="https://example.invalid/f"><input name="a"><button>go</button></form>',
  'button formaction': '<button formaction="https://example.invalid/f">b</button>',
  'style url()': '<p style="color: red; background: url(https://example.invalid/p.png)">styled</p>',
  'style url(), escaped': '<p style="background-image: \\75 rl(https://example.invalid/e.png)">escaped</p>',
  'style element': '<style>body{background:url(https://example.invalid/s.png)}</style>',
  script: '<script>window.__fxPreview=1</script>',
  base: '<base href="https://example.invalid/">',
  'meta refresh': '<meta http-equiv="refresh" content="0;url=https://example.invalid/">',
  'link stylesheet': '<link rel="stylesheet" href="https://example.invalid/x.css">',
};

/** A README that must still look like one after sanitizing. */
export const BENIGN_MARKDOWN = [
  '# Project title',
  '',
  'Some *emphasis*, **strong**, `inline code` and a [link](https://example.test/docs).',
  '',
  '| Name | Value |',
  '| ---- | ----- |',
  '| a    | 1     |',
  '',
  '```js',
  'const x = 1 < 2;',
  '```',
  '',
  '<img src="https://example.test/logo.png" alt="logo" width="72">',
  '',
  '<details><summary>More</summary>hidden body</details>',
  '',
  '<p style="color: red">red words</p>',
  '',
  'Press <kbd>Ctrl</kbd>+<kbd>S</kbd>.',
].join('\n');

const BAD_ELEMENTS = [
  'script', 'iframe', 'frame', 'object', 'embed', 'form', 'input', 'button', 'textarea', 'select',
  'base', 'meta', 'link', 'style', 'math', 'svg',
];
const URL_ATTRS = ['href', 'src', 'action', 'formaction', 'xlink:href', 'data', 'poster', 'background'];

/**
 * Every way `root` could still run, fetch or submit something — empty when
 * it is inert. `allow` names element kinds the surface legitimately keeps
 * (a diagram keeps `svg` and `style`, a formula keeps `math`).
 */
export function activeContent(root: ParentNode, allow: string[] = []): string[] {
  const found: string[] = [];
  const all = Array.from(root.querySelectorAll('*'));
  for (const el of all) {
    const tag = el.tagName.toLowerCase();
    if (BAD_ELEMENTS.includes(tag) && !allow.includes(tag)) found.push(`<${tag}>`);
    for (const a of Array.from(el.attributes)) {
      const name = a.name.toLowerCase();
      if (name.startsWith('on')) found.push(`${tag}[${name}]`);
      if (name === 'srcdoc') found.push(`${tag}[srcdoc]`);
      if (URL_ATTRS.includes(name)) {
        // eslint-disable-next-line no-control-regex
        const v = a.value.replace(/[\u0000- ]/g, '').toLowerCase();
        if (/^(javascript|vbscript):/.test(v) || /^data:(text|application)\//.test(v)) found.push(`${tag}[${name}=${v.slice(0, 24)}]`);
      }
      if (name === 'style' && (/url\s*\(/i.test(a.value) || a.value.includes('\\'))) found.push(`${tag}[style=${a.value}]`);
    }
  }
  return found;
}
