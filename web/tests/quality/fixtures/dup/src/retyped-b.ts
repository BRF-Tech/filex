// Fixture: the RE-TYPED copy of retyped-a.ts. Same control flow, no shared
// identifier, no shared string. See retyped-a.ts for why these files exist.

interface KeyRecord {
  fingerprint: string;
  revoked: boolean;
  title: string;
}

export function summariseKeys(records: KeyRecord[], cap: number): string[] {
  const live: string[] = [];
  const dead: string[] = [];
  for (const record of records) {
    if (!record.title) continue;
    if (record.revoked) {
      dead.push(`${record.title} [${record.fingerprint}]`);
      continue;
    }
    live.push(`${record.title} [${record.fingerprint}]`);
    if (live.length >= cap) break;
  }
  if (live.length === 0 && dead.length === 0) return ['no keys'];
  const banner = `${live.length} usable, ${dead.length} revoked`;
  return [banner, ...live, ...dead];
}
