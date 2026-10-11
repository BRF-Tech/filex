// The Helm chart is installed from the BRF-Tech Helm repository, and the
// install guide has to say so in words that survive the export.
//
// ⚠ The export rewrites the project's own domain to example.com everywhere
// except the addresses it keeps (KEEP in scripts/export-public.sh). The Helm
// repository is one of them: rewritten, the public guide would tell people to
// `helm repo add` a host that does not exist, and every check that reads the
// private tree would still pass.
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '../../..');
const hasExporter = existsSync(path.join(REPO, 'scripts', 'export-public.sh'));
const read = (...p: string[]) => readFileSync(path.join(REPO, ...p), 'utf8');

const REPO_ADD = 'helm repo add brf-tech https://helm.brf.sh';
const OCI = 'oci://ghcr.io/brf-tech/charts/filex';

type Rules = { keep: string[]; replace: [string, string][]; infra: string[] };

describe('the Helm install instructions', () => {
  it('the install guide adds the repository, installs from it and names the OCI chart', () => {
    const guide = read('docs', 'INSTALLATION.md');
    expect(guide).toContain(REPO_ADD);
    expect(guide).toContain('helm install filex brf-tech/filex');
    expect(guide).toContain(OCI);
  });

  it('the README and the deploy index point at the same repository', () => {
    for (const f of [['README.md'], ['deploy', 'README.md']]) {
      const text = read(...f);
      expect(text, f.join('/')).toContain('https://helm.brf.sh');
      expect(text, f.join('/')).toContain('brf-tech/filex');
    }
  });

  it.skipIf(!hasExporter)('survive the export rewrite unchanged', async () => {
    const { exportRules, neutralize } = (await import('../../../docs-site/scripts/neutralize.mjs')) as {
      exportRules: () => Rules | null;
      neutralize: (text: string, rules: Rules | null) => string;
    };
    const rules = exportRules()!;
    const published = neutralize(read('docs', 'INSTALLATION.md'), rules);
    expect(published).toContain(REPO_ADD);
    expect(published).toContain(OCI);
    // And the rewrite still reaches the rest of the domain: keeping the Helm
    // repository must not keep its neighbours.
    const neighbour = ['https://fm', 'brf', 'sh'].join('.');
    expect(neutralize(neighbour, rules)).not.toContain(['brf', 'sh'].join('.'));
  });
});
