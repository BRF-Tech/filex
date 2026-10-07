// A TCP forwarder for the job containers, which have no socat:
//   node forward.mjs <listen-host>:<port> <target-host>:<port>
// The Document Server reaches the filex under test through it, and the
// docker shim (scripts/chain/shim/docker) points the e2e harness's S3 port
// at the gateway run.mjs started.

import net from 'node:net';

const [listen, target] = process.argv.slice(2);
if (!listen || !target) {
  console.error('usage: node forward.mjs <listen-host>:<port> <target-host>:<port>');
  process.exit(2);
}
const split = (hp) => {
  const i = hp.lastIndexOf(':');
  return [hp.slice(0, i), Number(hp.slice(i + 1))];
};
const [lhost, lport] = split(listen);
const [thost, tport] = split(target);

net
  .createServer((client) => {
    const upstream = net.connect(tport, thost);
    client.pipe(upstream);
    upstream.pipe(client);
    client.on('error', () => upstream.destroy());
    upstream.on('error', () => client.destroy());
  })
  .listen(lport, lhost);
