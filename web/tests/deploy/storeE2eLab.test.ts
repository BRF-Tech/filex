// The Store check without a Windows desktop, path A (task #248): a GitHub
// runner runs desktop/scripts/store-e2e.mjs as a SECOND local administrator,
// in an interactive session of its own, from a single-purpose public
// repository (packaging/store-e2e-lab/ is all of it).
//
// ⚠ Why: the runner's own account is the built-in Administrator (RID 500, a
// full token, no UAC), an MSIX app's Chromium does not start under it, and
// no activation check passed on GitHub from 0.44.2 on. A lab that quietly ran
// as that account again, or let a check only warn, would prove nothing -
// these are the properties that make its green mean something.
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const LAB = path.resolve(__dirname, '../../../packaging/store-e2e-lab');
const ps1 = () => fs.readFileSync(path.join(LAB, 'run-store-e2e-as.ps1'), 'utf8');
const yml = () => fs.readFileSync(path.join(LAB, 'store-e2e.yml'), 'utf8');

describe('the Store e2e lab (#248, path A)', () => {
  it('runs store-e2e as a second account, in its own interactive session, not elevated', () => {
    const s = ps1();
    expect(s).toMatch(/New-LocalUser -Name e2e /);
    expect(s).toMatch(/Add-LocalGroupMember -Group Administrators -Member e2e/);
    expect(s).toMatch(/Add-LocalGroupMember -Group 'Remote Desktop Users' -Member e2e/);
    // An RDP session to itself (the client refuses 127.0.0.1), waited for until it is Active.
    expect(s).toMatch(/'\/v:127\.0\.0\.2', '\/u:e2e'/);
    expect(s).toMatch(/quser .*e2e/);
    expect(s).toMatch(/notmatch '\\sActive\\s'/);
    // In THAT session, as e2e, with the filtered token UAC gives an administrator.
    expect(s).toMatch(/schtasks \/create \/tn filex-store-e2e .* \/ru e2e \/rp \$plain \/it \/f/);
    expect(s).not.toMatch(/\/rl HIGHEST/);
    expect(s).toMatch(/AllowDevelopmentWithoutDevLicense -Value 1/);
  });

  it('lets no check only warn: neither CI nor GITHUB_ACTIONS reaches store-e2e', () => {
    const s = ps1();
    expect(s).toMatch(/'set CI=',\s*'set GITHUB_ACTIONS=',/);
    expect(s).not.toMatch(/STORE_E2E_STRICT=0/);
    // Its exit code is the job's.
    expect(s).toMatch(/exit \$code\s*$/);
  });

  it('never prints the password and keeps it nowhere', () => {
    const s = ps1();
    expect(s).toMatch(/Write-Host "::add-mask::\$plain"/);
    expect(s.indexOf('::add-mask::')).toBeLessThan(s.indexOf('New-LocalUser'));
    for (const line of s.split('\n').filter((l) => /\$plain/.test(l))) {
      expect(line, line).not.toMatch(/Say |Write-Host "(?!::add-mask::)|Set-Content|Out-File/);
    }
  });

  it('checks a released tag of the public repository the way the release tool builds it', () => {
    const y = yml();
    expect(y).toMatch(/^ {6}tag:\n {8}description:/m);
    expect(y).toMatch(/repository: BRF-Tech\/filex\n\s+ref: \$\{\{ inputs\.tag \}\}/);
    // The release tool's Store check: the UI, the Windows binary, the app, its CLI (plan.mjs storeCopyDeferred).
    expect(y).toContain("pnpm -r --filter './packages/*' build && pnpm --filter ./web build && node scripts/sync-embed.mjs");
    expect(y).toContain('node scripts/build-backend.mjs --out bin/filex-release-e2e.exe');
    expect(y).toMatch(/FILEX_CLI_BIN="\$PWD\/\.\.\/bin\/filex-release-e2e\.exe" node scripts\/fetch-cli\.mjs --platform win32/);
    expect(y).toMatch(/\.\/lab\/run-store-e2e-as\.ps1 -Desktop /);
    expect(y).toMatch(/timeout-minutes: \d+/);
    expect(y).toMatch(/if: always\(\)\n\s+uses: actions\/upload-artifact@v4/);
    // It publishes nothing and asks for nothing more than reading.
    expect(y).toMatch(/permissions:\n {2}contents: read\n/);
    expect(y).not.toMatch(/secrets\./);
  });

  it('is set up and judged as CONTRIBUTING says', () => {
    const c = fs.readFileSync(path.resolve(__dirname, '../../../docs/CONTRIBUTING.md'), 'utf8');
    expect(c).toMatch(/### The Store check without a Windows desktop/);
    expect(c).toContain('gh workflow run store-e2e.yml -R BRF-Tech/store-e2e-lab -f tag=');
  });
});
