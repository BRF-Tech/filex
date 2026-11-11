/**
 * The class names of the public link page (components/public: PublicShell,
 * PublicPinGate, PublicShareBody, PublicRequestBody) that are a PUBLIC
 * contract: an administrator's page.shell template (internal/templates)
 * restyles the card through them, so renaming one silently undoes somebody's
 * design. They are listed, with what each is, in docs/TEMPLATES.md ("The
 * public link page"), and web/tests/lib/publicPageClasses.test.ts is red when
 * one of them is no longer in the components or no longer documented.
 *
 * ⚠ Adding a class here is a promise to keep it; removing one is a breaking
 * change for every edited template that names it - say so in CHANGELOG.
 */
export const PUBLIC_PAGE_CLASSES = [
  'fe-ppage',
  'fe-ppage__brand',
  'fe-ppage__logo',
  'fe-ppage__brandname',
  'fe-ppage__card',
  'fe-ppage__badge',
  'fe-ppage__badge--ok',
  'fe-ppage__badge--err',
  'fe-ppage__head',
  'fe-ppage__title',
  'fe-ppage__subject',
  'fe-ppage__intro',
  'fe-ppage__meta',
  'fe-ppage__slot',
  'fe-ppage__pin',
  'fe-ppage__pinput',
  'fe-ppage__actions',
  'fe-ppage__crumbs',
  'fe-ppage__entry',
  'fe-ppage__files',
  'fe-ppage__file',
  'fe-ppage__state',
  'fe-ppage__state--ok',
  'fe-ppage__state-title',
  'fe-ppage__error',
  'fe-ppage__foot',
  'fe-ppage__footslot',
  'fe-ppage__footline',
  'fe-ppage__footmark',
] as const;

/**
 * The page's state and layout, as attributes on the root (`.fe-ppage`): a
 * template's CSS can tell a PIN gate from an open link
 * (`.fe-ppage[data-state="pin"]`) and the three card widths apart.
 */
export const PUBLIC_PAGE_ATTRIBUTES = ['data-state', 'data-layout', 'data-slot'] as const;
