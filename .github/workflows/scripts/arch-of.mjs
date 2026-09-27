// Which CPU a binary is built for, read from its own header.
//
//   node arch-of.mjs <file> [<file>...] [--expect arm64|amd64]
//
// prints `<arch>  <file>` per file (amd64, arm64, 386 or arm; a macOS
// universal binary prints its slices joined with `+`) and, with --expect,
// exits 1 when any file is not that architecture.
//
// ⚠ Why: every desktop package carries the filex CLI as its sync engine
// (resources/bin), built by desktop/scripts/fetch-cli.mjs for GOARCH — which
// follows the HOST unless told otherwise. An arm64 installer built on an x64
// runner with the x64 CLI inside installs, opens, and then fails at the first
// sync, on the user's machine. A file name says nothing about that; the ELF,
// PE or Mach-O header does. No `file(1)` on Windows runners, so this reads the
// header itself and runs the same everywhere.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const ALIASES = { x64: 'amd64', x86_64: 'amd64', amd64: 'amd64', arm64: 'arm64', aarch64: 'arm64' };

/** 'amd64' | 'arm64' | '386' | 'arm' | 'a+b' (universal) | 'unknown (…)' */
export function archOf(file) {
  const fd = fs.openSync(file, 'r');
  try {
    const head = Buffer.alloc(4096);
    const n = fs.readSync(fd, head, 0, head.length, 0);
    const b = head.subarray(0, n);
    // ELF: e_machine at 18, in the byte order EI_DATA (offset 5) names.
    if (b.length >= 20 && b[0] === 0x7f && b[1] === 0x45 && b[2] === 0x4c && b[3] === 0x46) {
      const m = b[5] === 2 ? b.readUInt16BE(18) : b.readUInt16LE(18);
      return { 0x3e: 'amd64', 0xb7: 'arm64', 0x03: '386', 0x28: 'arm' }[m] ?? `unknown (ELF machine 0x${m.toString(16)})`;
    }
    // PE: "MZ", e_lfanew at 0x3c, "PE\0\0", then the COFF Machine field.
    if (b.length >= 0x40 && b[0] === 0x4d && b[1] === 0x5a) {
      const pe = b.readUInt32LE(0x3c);
      const hdr = Buffer.alloc(6);
      fs.readSync(fd, hdr, 0, 6, pe);
      if (hdr.toString('latin1', 0, 4) !== 'PE\0\0') return 'unknown (MZ without a PE header)';
      const m = hdr.readUInt16LE(4);
      return { 0x8664: 'amd64', 0xaa64: 'arm64', 0x14c: '386', 0x1c4: 'arm' }[m] ?? `unknown (PE machine 0x${m.toString(16)})`;
    }
    const cpu = (t) => ({ 0x01000007: 'amd64', 0x0100000c: 'arm64', 7: '386', 12: 'arm' })[t] ?? `unknown (Mach-O cpu 0x${t.toString(16)})`;
    // Mach-O 64-bit, little endian (every macOS binary since 10.6).
    if (b.length >= 8 && b.readUInt32LE(0) === 0xfeedfacf) return cpu(b.readUInt32LE(4));
    // Universal ("fat") binary: big endian header, one entry per slice.
    if (b.length >= 8 && b.readUInt32BE(0) === 0xcafebabe) {
      const count = b.readUInt32BE(4);
      const slices = [];
      for (let i = 0; i < count && 8 + i * 20 + 4 <= b.length; i++) slices.push(cpu(b.readUInt32BE(8 + i * 20)));
      return slices.join('+');
    }
    return 'unknown (not an ELF, PE or Mach-O file)';
  } finally {
    fs.closeSync(fd);
  }
}

/** amd64/arm64 for any of the spellings the toolchains use (x64, x86_64, aarch64…). */
export function normalizeArch(a) {
  const v = ALIASES[String(a).toLowerCase()];
  if (!v) throw new Error(`unknown architecture "${a}" (use amd64 or arm64)`);
  return v;
}

function main(argv) {
  const files = [];
  let expect = null;
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === '--expect') expect = normalizeArch(argv[++i]);
    else files.push(argv[i]);
  }
  if (files.length === 0) {
    console.error('usage: node arch-of.mjs <file>... [--expect arm64|amd64]');
    return 2;
  }
  let bad = 0;
  for (const f of files) {
    if (!fs.existsSync(f)) {
      console.error(`missing: ${f}`);
      bad++;
      continue;
    }
    const a = archOf(f);
    const ok = !expect || a === expect;
    console.log(`${ok ? '' : 'WRONG '}${a}  ${f}`);
    if (!ok) bad++;
  }
  if (bad && expect) console.error(`${bad} file(s) are not ${expect}`);
  return bad ? 1 : 0;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exit(main(process.argv.slice(2)));
}
