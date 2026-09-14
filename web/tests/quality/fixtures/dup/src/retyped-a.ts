// Fixture: the ORIGINAL of a RE-TYPED pair.
//
// retyped-b.ts is the same logic with every identifier, string and number
// changed — the shape a block takes when it is copied and then adapted to a
// second resource, which is how most of this repo's real duplicates look
// (nfsexports.SetState vs sshkeys.SetState share 522 tokens and not one name).
// The verbatim pass must NOT match these two; the renamed pass must.

interface ExportRow {
  id: string;
  disabled: boolean;
  label: string;
}

export function summariseExports(rows: ExportRow[], limit: number): string[] {
  const active: string[] = [];
  const parked: string[] = [];
  for (const row of rows) {
    if (!row.label) continue;
    if (row.disabled) {
      parked.push(`${row.label} (${row.id})`);
      continue;
    }
    active.push(`${row.label} (${row.id})`);
    if (active.length >= limit) break;
  }
  if (active.length === 0 && parked.length === 0) return ['no exports'];
  const head = `${active.length} active, ${parked.length} disabled`;
  return [head, ...active, ...parked];
}
