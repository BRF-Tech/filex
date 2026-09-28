/**
 * Every `.Values.<key>` a Helm template reads is a key values.yaml declares.
 *
 * ⚠ Why: 0.48 removed the iframe converter and its `convert:` block from
 * values.yaml, but templates/NOTES.txt still read `.Values.convert.enabled`.
 * `helm template` then fails on a nil pointer for every install — found only
 * by the release's YAML gate (scripts/release/gates/yaml-helm.sh), which needs
 * the helm binary; this reads the files, so it runs everywhere.
 */
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const CHART = path.resolve(__dirname, '../../../deploy/helm/filex');

function files(dir: string): string[] {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name);
    return e.isDirectory() ? files(p) : [p];
  });
}

describe('the Helm chart', () => {
  it('reads no .Values key that values.yaml does not declare', () => {
    const values = fs.readFileSync(path.join(CHART, 'values.yaml'), 'utf8');
    const declared = new Set([...values.matchAll(/^([A-Za-z][\w-]*):/gm)].map((m) => m[1]));
    const unknown: string[] = [];
    for (const f of files(path.join(CHART, 'templates'))) {
      const text = fs.readFileSync(f, 'utf8');
      for (const m of text.matchAll(/\.Values\.([A-Za-z][\w-]*)/g)) {
        if (!declared.has(m[1])) unknown.push(`${path.relative(CHART, f)}: .Values.${m[1]}`);
      }
    }
    expect([...new Set(unknown)]).toEqual([]);
  });
});
