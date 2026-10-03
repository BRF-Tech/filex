// lib/codeRuns — a server sentence that marks what is to be typed between
// backticks, cut into runs the page draws as text and <code> (#128: the fix
// of a failed operating-system sign-in step, with its command).
import { describe, expect, it } from 'vitest';

import { codeRuns } from '@/lib/codeRuns';

describe('codeRuns', () => {
  it('cuts the marked runs out as code, the rest as text', () => {
    expect(codeRuns('Install it: `sudo apt install pamtester` - then test again.')).toEqual([
      { text: 'Install it: ', code: false },
      { text: 'sudo apt install pamtester', code: true },
      { text: ' - then test again.', code: false },
    ]);
  });

  it('a sentence that starts or ends with code has no empty runs', () => {
    expect(codeRuns('`{pamtester}` exists: `sudo chmod 755 /usr/bin/pamtester`')).toEqual([
      { text: '{pamtester}', code: true },
      { text: ' exists: ', code: false },
      { text: 'sudo chmod 755 /usr/bin/pamtester', code: true },
    ]);
  });

  it('a sentence without marks is one text run; an empty one is none', () => {
    expect(codeRuns('The PAM sign-in works on Linux only.')).toEqual([{ text: 'The PAM sign-in works on Linux only.', code: false }]);
    expect(codeRuns('')).toEqual([]);
  });

  it('a backtick without its pair is text — it never swallows the rest of the sentence', () => {
    expect(codeRuns('one ` and `two` more')).toEqual([{ text: 'one ` and `two` more', code: false }]);
  });
});
