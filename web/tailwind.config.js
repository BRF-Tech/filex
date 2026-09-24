/**
 * A brand rung that is a TOKEN rather than a hex.
 *
 * ⚠⚠ Why a function and not just `'var(--fe-primary)'`: Tailwind calls a
 * colour function twice over. For the plain utility (`bg-brand-600`) it hands
 * in `opacityValue: 'var(--tw-bg-opacity)'`, and for the slash modifier
 * (`bg-brand-500/10`) it hands in the literal `'0.1'`. A string value answers
 * the first case and SILENTLY DROPS the second — every `/10` tint in the
 * panel would come out fully opaque, which is a slab of primary where a hint
 * was meant. The literal case is mixed toward transparent instead; the
 * variable case takes the token as it is, because a `--tw-*-opacity` that is
 * always 1 has nothing to say.
 */
const tok = (expr) => ({ opacityValue }) => {
  if (opacityValue == null || opacityValue === '1' || String(opacityValue).includes('var(')) {
    return expr;
  }
  const pct = Number(opacityValue) * 100;
  return Number.isFinite(pct)
    ? `color-mix(in srgb, ${expr} ${pct}%, transparent)`
    : expr;
};

/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,ts,tsx,js,jsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        // gorunum:v1 — the admin shell's brand scale. Until now this was
        // Tailwind `indigo`, value for value, which is why every nav item,
        // link, badge and primary button in the panel was violet while the
        // product's own primary (`--fe-primary`, #2f6ceb) had already moved to
        // blue. One block, and the whole panel follows: no component was
        // edited to recolour the shell.
        //
        // How the rungs were derived — anchors first, ramp second:
        //   50  #eef3ff  = `--fe-bg-selected` (light). The panel's active-row
        //                  tint is then literally the explorer's selected-row
        //                  tint; the two surfaces stop disagreeing.
        //   300 #8fb0f7  = the reference build's primary focus ring.
        //   400 #5b8cff  = `--fe-primary` (dark). `text-brand-400` is the
        //                  panel's link colour in dark mode, so it is the
        //                  primary itself rather than a lookalike.
        //   600 #2f6ceb  = `--fe-primary` (light) — the product blue, and the
        //                  rung the primary Button and every link use.
        //   700 #2559c9  = `--fe-primary-hover` (light).
        //   100, 200, 500 interpolated in OKLab between their neighbours, so
        //                  the lightness ramp stays even.
        //   800/900/950   carry Tailwind blue's own 700->950 lightness steps
        //                  forward from our 700, so the deep end stays a ramp
        //                  and not three shades of one navy.
        //
        // ⚠ 100 is solved for, not picked: text-brand-700 has to clear 4.5 on
        // it (the avatar chip) AND `hover:bg-brand-100` over `bg-brand-50` has
        // to be a step you can see. `--fe-primary-soft` (#e6ecfa) passes the
        // first and fails the second (1.065 step, against indigo's 1.102);
        // #dbe6fe measures 5.00 and 1.128.
        //
        // Measured, every pairing the shell actually renders, light and dark
        // (the numbers are in the sweep that came with this change): nothing
        // drops below its floor — 4.5 for text, 3.0 for the icon tiles and
        // status dots, which are WCAG 1.4.11 non-text. The ratios DO fall
        // (link on white 6.29 -> 4.70) because the product blue is lighter
        // than indigo-600; that is the same trade the sign-in page and
        // `themeContrast.test.ts` already accepted for `--fe-primary`.
        // ⚠⚠ gorunum:v4 — THE RAMP IS THE PALETTE NOW, not a hex ladder.
        //
        // The hexes above are the STOCK palette's, and they are still what
        // these rungs resolve to when nobody has picked anything else: the
        // tokens named here carry exactly those values in
        // `packages/core/src/styles/variables.css` (`--fe-primary` #2f6ceb,
        // `--fe-primary-hover` #2559c9, `--fe-primary-soft` #e6ecfa). What
        // changes is that they now MOVE. A hex cannot follow the palette the
        // person picked — that was the whole of the owner's second complaint,
        // 2026-09-20: "admin panel seçili renk paletinden etkilenmiyor,
        // etkilenmeli" — and every one of them was frozen blue while the
        // explorer beside it had gone amber.
        //
        // How the rungs map, and why each one:
        //   50 / 100  `--fe-primary-soft` — the tint the palette itself names
        //             for "this is the selected thing". 100 was solved for as
        //             a separate hex (#dbe6fe) so `hover:bg-brand-100` over
        //             `bg-brand-50` was a visible step; the panel no longer
        //             uses that pair anywhere (the nav's active row is a
        //             token rule in main.css), so one tint for both.
        //   200       a stronger tint, mixed from the palette's own primary
        //             into its own ground — the separation, not a fixed hex.
        //   300       `--fe-primary-ink`, which exists precisely because the
        //             primary itself measures 3.97:1 on the soft tint and
        //             every palette names an ink that clears 4.5 on its own.
        //   400/500/600  the primary. They differ in stock Tailwind; here
        //             `text-brand-600` (light) and `dark:text-brand-400`
        //             (dark) are two spellings of "the product's blue", and
        //             the token is already the right one for the mode.
        //   700       `--fe-primary-hover`.
        //   800-950   mixed toward the palette's BACKGROUND, which is the
        //             direction the deep end is used in: every occurrence in
        //             the panel is a `dark:` ground (`dark:bg-brand-900/40`),
        //             and in dark mode `--fe-bg` is the dark one.
        //
        // ⚠ `tok()` above is what keeps `/10`, `/20`, `/30` working; read its
        // note before changing a value here to a plain string.
        brand: {
          50: tok('var(--fe-primary-soft)'),
          100: tok('var(--fe-primary-soft)'),
          200: tok('color-mix(in srgb, var(--fe-primary) 35%, var(--fe-bg))'),
          300: tok('var(--fe-primary-ink)'),
          400: tok('var(--fe-primary)'),
          500: tok('var(--fe-primary)'),
          600: tok('var(--fe-primary)'),
          700: tok('var(--fe-primary-hover)'),
          800: tok('color-mix(in srgb, var(--fe-primary) 55%, var(--fe-bg))'),
          900: tok('color-mix(in srgb, var(--fe-primary) 35%, var(--fe-bg))'),
          950: tok('color-mix(in srgb, var(--fe-primary) 20%, var(--fe-bg))'),
        },
      },
      fontFamily: {
        sans: [
          'Inter',
          'ui-sans-serif',
          'system-ui',
          '-apple-system',
          'Segoe UI',
          'Roboto',
          'Helvetica Neue',
          'Arial',
          'sans-serif',
        ],
        mono: [
          'JetBrains Mono',
          'ui-monospace',
          'SFMono-Regular',
          'Menlo',
          'Monaco',
          'Consolas',
          'monospace',
        ],
      },
      keyframes: {
        'fade-in': {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' },
        },
        'slide-up': {
          '0%': { opacity: '0', transform: 'translateY(8px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
        'slide-down': {
          '0%': { opacity: '0', transform: 'translateY(-8px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
        'scale-in': {
          '0%': { opacity: '0', transform: 'scale(0.95)' },
          '100%': { opacity: '1', transform: 'scale(1)' },
        },
      },
      animation: {
        'fade-in': 'fade-in 150ms ease-out',
        'slide-up': 'slide-up 200ms ease-out',
        'slide-down': 'slide-down 200ms ease-out',
        'scale-in': 'scale-in 150ms ease-out',
      },
    },
  },
  plugins: [],
};
