// Rasterises the filex SVG into build/icon.png for electron-builder.
//
// Why Electron and not a raster library: this repo has no image toolchain, and
// pulling one in (sharp + its native binaries) to draw one square would be a
// heavy dependency for a build asset. Electron is already here and ships the
// same renderer that draws the icon in the app, so the PNG matches what users
// see. electron-builder derives .ico and .icns from this single 1024px PNG.
//
// Run: electron scripts/make-icon.mjs   (or `pnpm icon`)
import { app, BrowserWindow } from 'electron';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const SVG = path.resolve(__dirname, '../../web/public/icons/icon.svg');
const OUT = path.resolve(__dirname, '../build/icon.png');
const ICONS_DIR = path.resolve(__dirname, '../build/icons');
const SIZE = 1024;

// Linux gets an explicit size set. Handing electron-builder a single PNG is
// supposed to work, but it could not read this one's dimensions and emitted
// `usr/share/icons/hicolor/0x0/apps/filex.png` — a directory no icon theme
// will ever look in, so the app had no menu icon at all. With build/icons/
// present the size comes from the FILE NAME, so there is nothing left to
// misdetect.
const LINUX_SIZES = [16, 32, 48, 64, 128, 256, 512];

app.commandLine.appendSwitch('disable-gpu');

app.whenReady().then(async () => {
  const svg = fs.readFileSync(SVG, 'utf8');
  const win = new BrowserWindow({
    width: SIZE,
    height: SIZE,
    show: false,
    // No frame/background so the capture is exactly the artwork.
    transparent: true,
    frame: false,
    webPreferences: { offscreen: true },
  });

  // The SVG is 512×512; scale it to the icon size rather than re-authoring it,
  // so the app icon and the PWA icon can never drift apart.
  const html = `<!doctype html><meta charset="utf-8">
    <style>html,body{margin:0;padding:0;background:transparent}
    svg{width:${SIZE}px;height:${SIZE}px;display:block}</style>${svg}`;
  await win.loadURL('data:text/html;charset=utf-8,' + encodeURIComponent(html));
  // One frame is not always painted yet on the first tick.
  await new Promise((r) => setTimeout(r, 400));

  const image = await win.webContents.capturePage();
  fs.mkdirSync(path.dirname(OUT), { recursive: true });
  fs.writeFileSync(OUT, image.toPNG());

  const { width, height } = image.getSize();
  console.log(`wrote ${path.relative(process.cwd(), OUT)} (${width}×${height})`);

  fs.mkdirSync(ICONS_DIR, { recursive: true });
  for (const s of LINUX_SIZES) {
    const f = path.join(ICONS_DIR, `${s}x${s}.png`);
    fs.writeFileSync(f, image.resize({ width: s, height: s, quality: 'best' }).toPNG());
  }
  console.log(`wrote ${LINUX_SIZES.length} sizes to build/icons/ (${LINUX_SIZES.join(', ')})`);

  win.destroy();
  app.exit(width >= 512 && height >= 512 ? 0 : 1);
});
