#!/usr/bin/env node
// The nightly's morning report (task #175; since #361 it reads the night
// back from GitHub): ONE notification for the night, started by its own timer
// (scripts/chain/systemd/filex-nightly-report.timer, 07:00) whatever the night
// did - a night that left no record is reported too, which is how a timer
// that never fired gets noticed.
//
//   node scripts/chain/report.mjs --env FILE             compose it and send it
//   node scripts/chain/report.mjs --env FILE --dry-run   compose it and print it; send nothing
//
// What it says (scripts/chain/nightly-lib.mjs composeReport): what the night
// pushed (or why it pushed nothing), the commits since the last nightly, and
// GitHub's verdict on it - ci.yml's `All tests (full)` and every part that
// was not green, with its page, the night it last passed and the commits
// since (nightly.mjs followCi, ciVerdict) - then the nightly image, GitHub's
// desktop packages and the snaps, put on filex.sh/nightly when built
// (nightly.mjs followDesktop, #266). A night still going is waited for, at
// most NIGHTLY_REPORT_WAIT_MIN minutes. A final verdict is written into the
// night's record (history.jsonl), for the nights after it to compare with;
// --dry-run writes nothing and publishes no page.
//
// It posts to CHAIN_NOTIFY_URL (JSON: group, source, severity, title,
// message) with the Bearer key in CHAIN_NOTIFY_KEY_FILE, which it never logs,
// and keeps what it sent in <CHAIN_ROOT>/nightly/report.txt.
//
// Exit: 0 sent (or printed), 1 the post failed, 2 a setup error.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { mergeEnv } from './env.mjs';
import { composeReport, nightlySettings } from './nightly-lib.mjs';
import { alive, followCi, followDesktop, followImage, readHistory, readJson, recordCi } from './nightly.mjs';

const USAGE = `usage: node scripts/chain/report.mjs --env FILE [--dry-run] [--wait-min N]

  --env FILE     the nightly's settings (default: $NIGHTLY_ENV)
  --dry-run      print the report; send nothing, write nothing
  --wait-min N   wait at most N minutes for a night still going
                 (default: NIGHTLY_REPORT_WAIT_MIN, 30)

Exit: 0 sent, 1 the post failed, 2 a setup error.`;

export function parseReportArgs(argv) {
  const args = {};
  for (let i = 0; i < argv.length; i += 1) {
    const a = argv[i];
    const val = () => {
      const v = argv[i + 1];
      if (v === undefined || v.startsWith('--')) throw new Error(`${a} needs a value`);
      i += 1;
      return v;
    };
    if (a === '--env') args.env = val();
    else if (a === '--dry-run') args.dryRun = true;
    else if (a === '--wait-min') {
      args.waitMin = Number(val());
      if (!Number.isFinite(args.waitMin) || args.waitMin < 0) throw new Error('--wait-min needs a number of minutes');
    } else if (a === '--help' || a === '-h') args.help = true;
    else throw new Error(`unknown option ${a}`);
  }
  return args;
}

/** The notification body Notify's send API takes. */
export function notifyBody(report, { group, source }) {
  return { group, source, severity: report.severity, title: report.title, message: report.message };
}

/** git log from..to in the nightly checkout, newest first; throws when the range is not there. */
function commitsIn(src) {
  return (from, to) => {
    const r = spawnSync('git', ['-c', 'safe.directory=*', '-C', src, 'log', '--no-decorate', '--format=%H%x09%s', `${from}..${to}`], {
      encoding: 'utf8',
      maxBuffer: 16 * 1024 * 1024,
    });
    if (r.status !== 0) throw new Error(r.stderr);
    return r.stdout
      .split('\n')
      .filter(Boolean)
      .map((l) => {
        const tab = l.indexOf('\t');
        return { sha: l.slice(0, tab), subject: l.slice(tab + 1) };
      });
  };
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function main() {
  let args;
  try {
    args = parseReportArgs(process.argv.slice(2));
  } catch (e) {
    console.error(`${e.message}\n\n${USAGE}`);
    return 2;
  }
  if (args.help) {
    console.log(USAGE);
    return 0;
  }
  const envArg = args.env || process.env.NIGHTLY_ENV || '';
  let cfg;
  try {
    cfg = nightlySettings(mergeEnv(envArg ? fs.readFileSync(path.resolve(envArg), 'utf8') : '', process.env));
  } catch (e) {
    console.error(e.message);
    return 2;
  }
  const tonightFile = path.join(cfg.state, 'tonight.json');
  const waitMin = args.waitMin ?? cfg.report.waitMin;
  const until = Date.now() + waitMin * 60_000;
  let tonight = readJson(tonightFile);
  while (tonight && tonight.phase !== 'done' && alive(tonight.pid) && Date.now() < until) {
    await sleep(60_000);
    tonight = readJson(tonightFile);
  }
  const push = (readJson(path.join(cfg.state, 'state.json')) ?? {}).last_push ?? null;
  let ci = null;
  if (push?.revision) {
    try {
      ci = await followCi(cfg, push.revision);
    } catch (e) {
      ci = { state: 'unknown', why: e.message };
    }
    if (!args.dryRun) recordCi(cfg.state, push.revision, ci);
  }
  let follow = null;
  try {
    follow = await followDesktop(cfg, { dry: !!args.dryRun, ci });
  } catch (e) {
    follow = { state: 'unknown', why: `the follow-up of GitHub's desktop packages failed: ${e.message}` };
  }
  let image = null;
  try {
    image = followImage(cfg, push);
  } catch (e) {
    image = { state: 'unknown', why: e.message };
  }
  const report = composeReport({
    tonight: readJson(tonightFile) ?? tonight,
    history: readHistory(cfg.state),
    ci,
    push,
    follow,
    image,
    imageRef: `${cfg.image.repo}:nightly`,
    commits: commitsIn(cfg.src),
    running: tonight ? alive(tonight.pid) : false,
    nowMs: Date.now(),
    tz: cfg.tz,
    maxAgeH: cfg.report.maxAgeH,
  });
  const text = `${report.title}\n\n${report.message}\n`;
  console.log(`[${report.severity}] ${text}`);
  if (args.dryRun) return 0;
  try {
    fs.mkdirSync(cfg.state, { recursive: true });
    fs.writeFileSync(path.join(cfg.state, 'report.txt'), `${new Date().toISOString()} [${report.severity}]\n${text}`);
  } catch (e) {
    console.error(`could not keep the report in ${cfg.state}: ${e.message}`);
  }
  const { url, keyFile } = cfg.notify;
  if (!url) {
    console.error('CHAIN_NOTIFY_URL is not set: the report was not sent');
    return 2;
  }
  let key = '';
  try {
    key = fs.readFileSync(keyFile, 'utf8').trim();
  } catch {
    /* said below */
  }
  if (!key) {
    console.error(`no key in CHAIN_NOTIFY_KEY_FILE (${keyFile || 'unset'}): the report was not sent`);
    return 2;
  }
  try {
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'content-type': 'application/json', authorization: `Bearer ${key}` },
      body: JSON.stringify(notifyBody(report, cfg.notify)),
      signal: AbortSignal.timeout(20_000),
    });
    console.log(`notify: HTTP ${res.status}`);
    return res.ok ? 0 : 1;
  } catch (e) {
    console.error(`notify failed: ${e.message}`);
    return 1;
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().then(
    (code) => process.exit(code),
    (e) => {
      console.error(e.stack || e.message);
      process.exit(2);
    },
  );
}
