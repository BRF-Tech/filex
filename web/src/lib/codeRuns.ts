/**
 * A sentence written by the SERVER that marks what is to be typed — a
 * command, a path, a line for a config file — between backticks
 * (`server.auth_provider.pam_hint_*`: "Install it - Debian/Ubuntu: `sudo apt
 * install pamtester` - …"), cut into its runs, so a page draws the marked ones
 * as <code> the reader can select and copy whole.
 *
 * ⚠ The mark is the server's, not a guess: nothing here looks for things that
 * resemble a command. A backtick without its pair is text (the sentence is
 * shown as it came), never an open code run that swallows the rest.
 */
export interface TextRun {
  text: string;
  code: boolean;
}

export function codeRuns(sentence: string): TextRun[] {
  const parts = sentence.split('`');
  // An odd number of backticks leaves an even number of parts.
  if (parts.length % 2 === 0) return sentence ? [{ text: sentence, code: false }] : [];
  return parts.map((text, i) => ({ text, code: i % 2 === 1 })).filter((r) => r.text !== '');
}
