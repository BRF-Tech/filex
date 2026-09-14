// Measures the shell's OWN controls against the product's tokens.
//
// The question this answers is not "does it look off" but "by how much, and
// against which token" — a review that says "the button is a bit short" gets
// argued with; one that says "30px where --fe-h-md is 34px" does not.
//
// Run: node scripts/diag-chrome-metrics.mjs   (same env as look-chrome.mjs)

import { launchApp, signIn, sleep } from './lib/harness.mjs';

const { app } = await launchApp();
try {
  const { win } = await signIn(app);
  await sleep(5000);
  await win.evaluate(() => {
    const skip = [...document.querySelectorAll('button')].find((b) =>
      /Turu atla|Skip|Atla/i.test(b.textContent ?? ''));
    skip?.click();
  });
  await sleep(800);
  // Open the app's own settings so its controls are on screen and measurable.
  await win.evaluate(() => {
    const btns = [...document.querySelectorAll('#rail .rail-btn')];
    btns[btns.length - 1].click();
  });
  await sleep(900);

  const out = await win.evaluate(() => {
    const cs = getComputedStyle(document.documentElement);
    const tok = (n) => cs.getPropertyValue(n).trim();
    const px = (el) => (el ? Math.round(el.getBoundingClientRect().height * 10) / 10 : null);
    const f = (el) => (el ? getComputedStyle(el).fontSize : null);
    const fam = (el) => (el ? getComputedStyle(el).fontFamily.split(',')[0] : null);
    const bg = (el) => (el ? getComputedStyle(el).backgroundColor : null);

    const settings = document.querySelector('#settings');
    // ⚠ Not just `button`: the head's close is an .icon-btn at --fe-h-sm (28px)
    // and the language strip's segments live inside .segmented. Asking for the
    // first button reported 28px and read as "the buttons are too short".
    const btn = settings?.querySelector(
      'button:not(.icon-btn):not(.link):not(.switch):not(.segmented button)');
    const primary = settings?.querySelector('button.primary');
    const card = settings?.querySelector('.card');
    const close = settings?.querySelector('.icon-btn');
    const h1 = settings?.querySelector('h1');
    const h2 = settings?.querySelector('h2');

    // A control from the explorer, for comparison — the same screen, the token
    // heights actually applied.
    const feBtn = document.querySelector('.fe-btn');
    const feInput = document.querySelector('.fe-drivesearch input, .fe-toolbar input');

    return {
      tokens: {
        'h-sm': tok('--fe-h-sm'), 'h-md': tok('--fe-h-md'), 'h-lg': tok('--fe-h-lg'),
        'text-xs': tok('--fe-text-xs'), 'text-sm': tok('--fe-text-sm'), 'text-md': tok('--fe-text-md'),
        font: tok('--fe-font').split(',')[0],
        bg: tok('--fe-bg'), 'bg-elev': tok('--fe-bg-elev'), border: tok('--fe-border'),
        primary: tok('--fe-primary'), text: tok('--fe-text'), muted: tok('--fe-text-muted'),
        danger: tok('--fe-danger'), radius: tok('--fe-radius'), 'radius-lg': tok('--fe-radius-lg'),
      },
      shellVars: {
        bg: cs.getPropertyValue('--bg').trim(),
        bg2: cs.getPropertyValue('--bg-2').trim(),
        line: cs.getPropertyValue('--line').trim(),
        fg: cs.getPropertyValue('--fg').trim(),
        muted: cs.getPropertyValue('--muted').trim(),
        accent: cs.getPropertyValue('--accent').trim(),
        danger: cs.getPropertyValue('--danger').trim(),
      },
      shell: {
        bodyFont: getComputedStyle(document.body).fontSize,
        bodyFamily: getComputedStyle(document.body).fontFamily.split(',')[0],
        button: { h: px(btn), font: f(btn), family: fam(btn) },
        primary: { h: px(primary), bg: bg(primary) },
        close: { h: px(close), w: close ? Math.round(close.getBoundingClientRect().width) : null },
        card: { radius: card ? getComputedStyle(card).borderRadius : null, bg: bg(card) },
        h1: { font: f(h1) }, h2: { font: f(h2) },
        railBg: bg(document.querySelector('#rail')),
        railWidth: cs.getPropertyValue('--rail').trim(),
        settingsBg: bg(settings),
      },
      explorer: {
        feBtnH: px(feBtn),
        feBtnFont: f(feBtn),
        feInputH: px(feInput),
        rootDarkClass: document.documentElement.className,
        colorScheme: cs.colorScheme,
      },
      prefs: {
        thememode: localStorage.getItem('filex.thememode'),
        palette: localStorage.getItem('filex.palette'),
        density: localStorage.getItem('filex.density'),
        locale: localStorage.getItem('filex.locale'),
        timezone: localStorage.getItem('filex.timezone'),
      },
    };
  });
  console.log(JSON.stringify(out, null, 2));
} catch (e) {
  console.log('ERR', e.message);
} finally {
  await app.close().catch(() => {});
}
