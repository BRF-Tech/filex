#!/usr/bin/env node
// The build host's half of the autonomous release round (#165): started by a
// systemd timer (scripts/train/systemd/filex-release-auto*.timer), it runs
// scripts/train/auto.mjs inside the release workspace - a container on this
// host - and tells a person when that workspace is not running, instead of a
// round that never happens and says nothing.
//
//   node auto-host.mjs run|report --env /etc/filex-release-auto.env [--dry-run]
//
// Settings (the file, KEY=VALUE; installed by scripts/train/install-auto.sh):
//   FILEX_AUTO_CONTAINER  the release workspace's container
//   FILEX_AUTO_USER       the user inside it (default: the container's own)
//   FILEX_AUTO_CHECKOUT   the private checkout inside it (default: ~/filex)
//   FILEX_AUTO_WRAP       the command the round runs under, which hands it the
//                         narrow GitHub token (default: with-gh-token; empty:
//                         none)
//   FILEX_AUTO_STATE_DIR  where this host notes since when the workspace has
//                         been down (default: /var/lib/filex-auto)
//   FILEX_NOTIFY_*        who is told (scripts/lib/notify.mjs)
//
// Exit: the round's own (scripts/train/auto.mjs); 2 the workspace is not
// running, or a setting is missing.
//
// ⚠ Why: in 2026-10 a scheduled job elsewhere on this infrastructure skipped
// every run for three days because the workspace it ran in had stopped, and
// nothing said so. A release workspace that is down is said at once, and once
// a day while it stays down; the first round after it is back says that too.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { announce } from '../lib/notify.mjs';
import { loadSettings } from '../lib/settings.mjs';
import { shq } from '../release/engine.mjs';

const DAY_MS = 24 * 60 * 60 * 1000;

/**
 * The `docker exec` that runs one auto.mjs command in the workspace:
 * { bin, args }, or { error } when a setting is missing.
 */
export function hostCommand(env, cmd) {
  if (!['run', 'report'].includes(cmd)) return { error: `no such command "${cmd}": run or report` };
  const container = env.FILEX_AUTO_CONTAINER;
  if (!container) return { error: 'FILEX_AUTO_CONTAINER is not set: the release workspace container' };
  const checkout = env.FILEX_AUTO_CHECKOUT ? shq(env.FILEX_AUTO_CHECKOUT) : '"$HOME/filex"';
  const wrap = env.FILEX_AUTO_WRAP === undefined ? 'with-gh-token' : env.FILEX_AUTO_WRAP;
  // A login shell: the workspace's PATH (the wrapper, node) is its profile's.
  const script = `cd ${checkout} && exec ${wrap ? `${wrap} ` : ''}node scripts/train/auto.mjs ${cmd}`;
  return { bin: 'docker', args: ['exec', ...(env.FILEX_AUTO_USER ? ['-u', env.FILEX_AUTO_USER] : []), container, 'bash', '-lc', script] };
}

/**
 * Whether to tell a person about the workspace now: when it went down, once
 * a day while it stays down, and when it is back. `mark` is what this host
 * noted ({ since, told } or null), `running` what docker says.
 * Returns { tell: 'down' | 'back' | null, mark: the note to keep (null: none) }.
 */
export function downVerdict(mark, running, now = Date.now()) {
  if (running) return { tell: mark ? 'back' : null, mark: null };
  if (!mark) return { tell: 'down', mark: { since: new Date(now).toISOString(), told: new Date(now).toISOString() } };
  if (now - Date.parse(mark.told) >= DAY_MS) return { tell: 'down', mark: { ...mark, told: new Date(now).toISOString() } };
  return { tell: null, mark };
}

function running(container) {
  const r = spawnSync('docker', ['inspect', '-f', '{{.State.Running}}', container], { encoding: 'utf8' });
  return r.status === 0 && r.stdout.trim() === 'true';
}

async function main(argv) {
  const cmd = argv[0] ?? '';
  const envAt = argv.indexOf('--env');
  const file = envAt >= 0 ? argv[envAt + 1] : '';
  const dry = argv.includes('--dry-run');
  const settings = loadSettings({ file });
  const env = settings.env;
  const how = hostCommand(env, cmd);
  if (how.error) {
    console.error(`auto-host: ${how.error}`);
    return 2;
  }
  const stateDir = env.FILEX_AUTO_STATE_DIR || '/var/lib/filex-auto';
  const markFile = path.join(stateDir, 'workspace-down.json');
  let mark = null;
  try {
    mark = JSON.parse(fs.readFileSync(markFile, 'utf8'));
  } catch {
    mark = null;
  }
  const up = running(env.FILEX_AUTO_CONTAINER);
  const v = downVerdict(mark, up);
  const say = (title, message, result) =>
    dry ? console.log(`would tell: ${title}`) : announce(env, { title, message, result }, { log: (l) => console.error(`auto-host: ${l}`) });
  if (v.tell === 'down') {
    await say(
      'filex release round: the release workspace is not running',
      `${env.FILEX_AUTO_CONTAINER} has been down since ${v.mark.since}: no release round runs (${cmd}). Start the workspace; the next timer runs the round.`,
      'red',
    );
  } else if (v.tell === 'back') {
    await say('filex release round: the release workspace is back', `${env.FILEX_AUTO_CONTAINER} runs again (down since ${mark.since}).`, 'ok');
  }
  if (!dry) {
    fs.mkdirSync(stateDir, { recursive: true });
    if (v.mark) fs.writeFileSync(markFile, `${JSON.stringify(v.mark)}\n`);
    else fs.rmSync(markFile, { force: true });
  }
  if (!up) {
    console.error(`auto-host: ${env.FILEX_AUTO_CONTAINER} is not running`);
    return 2;
  }
  if (dry) {
    console.log(`would run: ${how.bin} ${how.args.join(' ')}`);
    return 0;
  }
  const r = spawnSync(how.bin, how.args, { stdio: 'inherit' });
  return typeof r.status === 'number' ? r.status : 1;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).then(
    (code) => process.exit(code),
    (e) => {
      console.error(`auto-host: ${e?.stack ?? e}`);
      process.exit(2);
    },
  );
}
