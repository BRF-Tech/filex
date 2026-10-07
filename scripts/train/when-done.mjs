#!/usr/bin/env node
// Runs a long command and says when it has ended - to the person and to the
// agent waiting for it - so nobody has to keep looking.
//
//   node scripts/train/when-done.mjs [--title T] [--log FILE] [--env FILE] -- <command> [args...]
//
//   --title T    how the notification names the step (default: the command)
//   --log FILE   also write everything the command printed to FILE
//   --env FILE   settings (default: $FILEX_TRAIN_ENV, else
//                ~/.config/filex-train.env): FILEX_NOTIFY_* for a person,
//                FILEX_WAKE_* for an agent (scripts/train/train.env.example)
//
// The command's output passes through as it comes; the notification carries
// its exit code, how long it took and its last 25 lines. The exit code is the
// command's own. Examples:
//
//   node scripts/train/when-done.mjs --title "pretag 0.53.0" -- pnpm release 0.53.0 --resume
//   node scripts/train/when-done.mjs --title "chain on the build host" -- ssh <host> 'bash <chain>/run.sh'
//
// ⚠ Why this exists (#179): the long steps of a release - the test chain,
// pnpm release's pretag and gate, a GitHub run - took the maintainer's
// session hours of turns spent asking "done yet?". Wrapped in this, the
// session ends its turn and is woken by the step itself (scripts/lib/notify.mjs
// says how).

import { spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { announce } from '../lib/notify.mjs';
import { loadSettings } from '../lib/settings.mjs';
import { findBash } from '../release/engine.mjs';
import { since, usageOf } from './cli.mjs';

const TAIL = 25;

/** The command line, parsed: everything after `--` is the command. */
export function parseArgs(argv) {
  const o = { title: '', log: '', env: '', cmd: [] };
  const dash = argv.indexOf('--');
  const own = dash < 0 ? argv : argv.slice(0, dash);
  o.cmd = dash < 0 ? [] : argv.slice(dash + 1);
  for (let i = 0; i < own.length; i++) {
    const a = own[i];
    const next = () => {
      const v = own[++i];
      if (v === undefined) throw new Error(`${a} needs a value`);
      return v;
    };
    if (a === '--title') o.title = next();
    else if (a === '--log') o.log = next();
    else if (a === '--env') o.env = next();
    else if (a === '-h' || a === '--help') o.help = true;
    else throw new Error(`unknown option ${a} (the command goes after --)`);
  }
  if (!o.help && o.cmd.length === 0) throw new Error('no command: write it after --');
  return o;
}

/** Keeps the last `n` lines of a stream of chunks. */
export class Tail {
  constructor(n = TAIL) {
    this.n = n;
    this.lines = [];
    this.partial = '';
  }

  push(chunk) {
    const parts = (this.partial + String(chunk).replace(/\r\n/g, '\n')).split('\n');
    this.partial = parts.pop() ?? '';
    this.lines.push(...parts);
    if (this.lines.length > this.n) this.lines.splice(0, this.lines.length - this.n);
  }

  text() {
    return [...this.lines, ...(this.partial ? [this.partial] : [])].slice(-this.n).join('\n');
  }
}

/**
 * [bin, args] that start the command. On Windows a command such as `pnpm` is
 * a .cmd file node cannot start without a shell, so it goes through Git Bash
 * with the words as positional parameters (nothing is re-quoted); elsewhere
 * it is started directly.
 */
export function commandArgv(cmd, { win = process.platform === 'win32', bash = null } = {}) {
  if (!win) return [cmd[0], cmd.slice(1)];
  return [bash ?? findBash() ?? 'bash', ['-c', 'exec "$@"', 'when-done', ...cmd]];
}

async function main(argv) {
  let o;
  try {
    o = parseArgs(argv);
  } catch (e) {
    console.error(`when-done: ${e.message}\n\n${usageOf(import.meta.url)}`);
    return 2;
  }
  if (o.help) {
    console.log(usageOf(import.meta.url));
    return 0;
  }
  const settings = loadSettings({ file: o.env, envVar: 'FILEX_TRAIN_ENV', fallback: 'filex-train.env' });
  const title = o.title || o.cmd.join(' ').slice(0, 120);
  const tail = new Tail();
  const logFd = o.log ? fs.openSync(o.log, 'w') : null;
  const t0 = Date.now();
  const [bin, args] = commandArgv(o.cmd);
  const code = await new Promise((resolve) => {
    const child = spawn(bin, args, { stdio: ['inherit', 'pipe', 'pipe'], windowsHide: true });
    const feed = (out) => (chunk) => {
      out.write(chunk);
      tail.push(chunk);
      if (logFd !== null) fs.writeSync(logFd, chunk);
    };
    child.stdout.on('data', feed(process.stdout));
    child.stderr.on('data', feed(process.stderr));
    child.on('error', (e) => {
      tail.push(`could not start ${o.cmd[0]}: ${e.message}\n`);
      resolve(127);
    });
    child.on('close', (c, signal) => resolve(typeof c === 'number' ? c : signal ? 128 : 1));
  });
  if (logFd !== null) fs.closeSync(logFd);
  const took = since(t0);
  const result = code === 0 ? 'ok' : code === 3 ? 'waiting' : 'red';
  const state = code === 0 ? 'done' : code === 3 ? 'waiting for a person (exit 3)' : `FAILED (exit ${code})`;
  await announce(
    settings.env,
    {
      title: `${title}: ${state} in ${took}`,
      message: [`$ ${o.cmd.join(' ')}`, ...(o.log ? [`log: ${path.resolve(o.log)}`] : []), '', tail.text()].join('\n'),
      result,
    },
    { log: (l) => console.error(`when-done: ${l}`) },
  );
  return code;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).then(
    (code) => process.exit(code),
    (e) => {
      console.error(`when-done: ${e?.stack ?? e}`);
      process.exit(1);
    },
  );
}
