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
        brand: {
          50: '#eef3ff',
          100: '#dbe6fe',
          200: '#b5cbfb',
          300: '#8fb0f7',
          400: '#5b8cff',
          500: '#467cf5',
          600: '#2f6ceb',
          700: '#2559c9',
          800: '#254aa0',
          900: '#26427c',
          950: '#1f2b47',
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
