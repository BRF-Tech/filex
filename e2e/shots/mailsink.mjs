// A mail server for a scene's filex: it answers SMTP and keeps every message.
//
// ⚠ Why a scene wants one. filex offers "send by email" only when its mail is
// set up AND verified (mailer.Ready: a real SMTP handshake). Without it an
// administrator's share dialog draws the field greyed with "Email is not set
// up - set it up in the admin panel under Settings (Email / SMTP)", which is
// true of a fresh install and not what a picture of the share dialog is about
// (share-modal.png carried it in every README from 0.52.0 to 0.53.0).
//
// ⚠ Plain SMTP, no TLS, no AUTH - the same sink e2e/tests/113 runs: filex's
// mailer with `smtp.tls = none` and no username says EHLO, NOOP (the verify),
// MAIL, RCPT, DATA, QUIT. It listens on loopback, so it serves a filex on this
// machine only: a server in a container or VM cannot reach it, and
// configureMail then fails rather than leaving a half-set-up instance.

import { createServer } from 'node:net';

/**
 * Starts the sink on a free loopback port: `{ port, mails, close }`, where
 * `mails` collects `{ to, data }` per message.
 */
export function startMailSink() {
  const mails = [];
  const server = createServer((sock) => {
    sock.setEncoding('utf8');
    let rcpt = [];
    let inData = false;
    let buf = '';
    let data = '';
    sock.write('220 shots-sink ESMTP\r\n');
    sock.on('data', (chunk) => {
      buf += chunk;
      let i;
      while ((i = buf.indexOf('\r\n')) >= 0) {
        const line = buf.slice(0, i);
        buf = buf.slice(i + 2);
        if (inData) {
          if (line === '.') {
            inData = false;
            mails.push({ to: rcpt, data });
            rcpt = [];
            data = '';
            sock.write('250 queued\r\n');
          } else {
            data += (line.startsWith('..') ? line.slice(1) : line) + '\n';
          }
          continue;
        }
        const verb = line.slice(0, 4).toUpperCase();
        if (verb === 'EHLO' || verb === 'HELO') sock.write('250 shots-sink\r\n');
        else if (verb === 'RCPT') {
          rcpt.push(line.replace(/^RCPT TO:\s*<?([^>]*)>?.*$/i, '$1'));
          sock.write('250 ok\r\n');
        } else if (verb === 'DATA') {
          inData = true;
          sock.write('354 go ahead\r\n');
        } else if (verb === 'QUIT') {
          sock.write('221 bye\r\n');
          sock.end();
        } else sock.write('250 ok\r\n');
      }
    });
    sock.on('error', () => {});
  });
  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      resolve({
        port: server.address().port,
        mails,
        close: () => new Promise((done) => server.close(() => done())),
      });
    });
  });
}

/**
 * Points filex's mail at the sink and has filex verify it, the way an
 * administrator's "Send test" does. `call(path, init)` is a signed-in
 * administrator's fetch. Throws when the verification fails: the picture would
 * show "Email is not set up" again, which is the thing this is for.
 */
export async function configureMail(call, port) {
  const set = await call('/api/admin/settings', {
    method: 'PATCH',
    body: JSON.stringify({
      'smtp.host': '127.0.0.1',
      'smtp.port': String(port),
      'smtp.tls': 'none',
      'smtp.from': 'files@example.com',
      'smtp.username': '',
    }),
  });
  if (!set.ok) throw new Error(`mail settings: ${set.status} ${await set.text()}`);
  const test = await call('/api/admin/settings/smtp-test', { method: 'POST', body: '{}' });
  const body = await test.json().catch(() => ({}));
  if (!test.ok || !body.ok) {
    throw new Error(`the mail sink on :${port} was not verified: ${test.status} ${body.error ?? JSON.stringify(body)}`);
  }
}
