// Independent producer of the store-signature test vectors (Node's crypto,
// no filex code): canonical JSON = keys sorted, no whitespace, strings and
// numbers as JSON.stringify writes them; signature = ed25519 over the
// lower-hex sha256 of those bytes.
import crypto from 'node:crypto';

function canonical(v) {
  if (v === null || typeof v !== 'object') return JSON.stringify(v);
  if (Array.isArray(v)) return '[' + v.map(canonical).join(',') + ']';
  return '{' + Object.keys(v).sort().map((k) => JSON.stringify(k) + ':' + canonical(v[k])).join(',') + '}';
}

function keyFromSeed(seed) {
  const pkcs8 = Buffer.concat([Buffer.from('302e020100300506032b657004220420', 'hex'), seed]);
  const priv = crypto.createPrivateKey({ key: pkcs8, format: 'der', type: 'pkcs8' });
  const pub = crypto.createPublicKey(priv).export({ format: 'der', type: 'spki' }).subarray(-32);
  return { priv, pub };
}

function vector(name, keyId, use, seedText, payload) {
  const seed = crypto.createHash('sha256').update(seedText).digest();
  const { priv, pub } = keyFromSeed(seed);
  const canon = canonical(payload);
  const sha = crypto.createHash('sha256').update(Buffer.from(canon, 'utf8')).digest('hex');
  const sig = crypto.sign(null, Buffer.from(sha, 'utf8'), priv).toString('hex');
  const fingerprint = crypto.createHash('sha256').update(pub).digest('hex');
  return {
    name, key_id: keyId, use, seed_hex: seed.toString('hex'), public_hex: pub.toString('hex'), fingerprint,
    // The answer as a store would send it: the payload in its own order,
    // indented; a filex canonicalises before it verifies.
    envelope: JSON.stringify({ payload, key_id: keyId, signature: sig }, null, 2),
    canonical: canon, sha256_hex: sha, signature_hex: sig,
  };
}

const intent = {
  token_id: 'tok_01HZX3', store: 'https://fapps.brfd.app', app: 'sign', kind: 'app', version: '0.3.0',
  repo: 'BRF-Tech/filex-sign', ref: 'v0.3.0', commit: '214e9e8a0c7d4b5e9f1a2b3c4d5e6f708192a3b4',
  manifest_sha256: '1f0e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0',
  wasm_sha256: '6a3d2e590000000000000000000000000000000000000000000000000000beef',
  permissions: ['files:read', 'files:write', 'http:freetsa.org'], filex_range: '>=0.52.0',
  paid: true, license_key: 'FXL-7Q2M-K9P4-ZZ31', expires_at: '2026-10-04T18:30:00Z',
};
const license = {
  result: 'valid', app: 'sign', licensee: 'Şahin & Ortakları <Ltd.>   “quoted”', seats: 5, seats_used: 2,
  valid_until: '2027-10-04T00:00:00Z', updates_until: '2027-04-04T00:00:00Z', instance_id: 'fx-0123456789abcdef0123456789abcdef',
  checked_at: '2026-10-04T15:00:00Z', next_check_by: '2026-10-05T15:00:00Z', grace_until: '2026-10-18T15:00:00Z',
  note: 'tab\there, newline\nhere, bell\u0007, slash / and back' + String.fromCharCode(92) + 'slash',
};
const out = {
  produced_by: 'node ' + process.version + ' crypto (independent of filex)',
  vectors: [
    vector('install-intent', 'idx-2026-1', 'index', 'filex app store test vector: index key', intent),
    vector('license-answer', 'lic-2026-1', 'license', 'filex app store test vector: license key', license),
  ],
};
process.stdout.write(JSON.stringify(out, null, 2) + '\n');
