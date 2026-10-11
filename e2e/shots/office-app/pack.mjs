// The office example app (this directory): an interface-only app that opens
// Word documents, edits them together with whoever else opens them
// (`co_edit` -> files:co-edit) and opens the files of encrypted folders and
// vaults in the clear (`encrypted_folders` -> files:e2e-plaintext) - what the
// 0.56 pictures show (officeapp.mjs, e2eapp.mjs). It offers .docx, .xlsx and
// .pptx in New document, so a filex without ONLYOFFICE has a way to make every
// kind of office document (#217). Never published.
//
// Also the documents the scenes open: real .docx files (a stored zip with the
// three parts Word needs), dated like every fixture (clock.mjs fixtureTime) -
// written to disk and uploaded by PATH, so a browser upload carries the fixed
// time too (lesson: a buffer handed to setInputFiles is dated the real now).
import { utimesSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { packInterfaceApp } from '../board-app/pack.mjs';
import { fixtureTime } from '../clock.mjs';
import { zipStored } from '../fixtures.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));

/** The office app, packed into `dir` (see board-app/pack.mjs). */
export function packOfficeApp(dir) {
  return packInterfaceApp(HERE, dir);
}

const W = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main';
const esc = (s) => String(s).replace(/[&<>]/g, (ch) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' })[ch]);

/**
 * A .docx at `dest`: `title` as its Title paragraph, then `paragraphs`
 * (a string is body text, `{ heading }` a Heading 1). Dated
 * fixtureTime(`rel`) - the path the scene stores it under.
 */
export function writeDocx(dest, { title, paragraphs = [] }, rel) {
  const p = (text, style) =>
    `<w:p>${style ? `<w:pPr><w:pStyle w:val="${style}"/></w:pPr>` : ''}<w:r><w:t xml:space="preserve">${esc(text)}</w:t></w:r></w:p>`;
  const body = [
    title ? p(title, 'Title') : '',
    ...paragraphs.map((x) => (typeof x === 'string' ? p(x) : p(x.heading, 'Heading1'))),
  ].join('');
  writeFileSync(
    dest,
    zipStored([
      {
        name: '[Content_Types].xml',
        data:
          '<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">' +
          '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/>' +
          '<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>',
      },
      {
        name: '_rels/.rels',
        data:
          '<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">' +
          '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>',
      },
      {
        name: 'word/document.xml',
        data: `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="${W}"><w:body>${body}</w:body></w:document>`,
      },
    ]),
  );
  const at = new Date(fixtureTime(rel));
  utimesSync(dest, at, at);
}

/** The documents the 0.56 pictures open: one team's plain working papers. */
export const DOCS = {
  plan: {
    title: 'Launch plan - Q4 2026',
    paragraphs: [
      'The desktop app ships to the stores in the first week of November, with the web release on the same day.',
      { heading: 'What ships' },
      'Editing office documents in the browser, alone or together, without a document server.',
      'Encrypted folders and vaults that open in the apps you choose.',
      { heading: 'Who does what' },
      'Dana writes the release notes and the store texts; Sam reviews them.',
    ],
  },
  minutes: {
    title: 'Board minutes - September',
    paragraphs: [
      'Present: the four directors and the company secretary.',
      { heading: 'Decisions' },
      'The budget for 2027 is approved as proposed, with the hiring plan moved to the second quarter.',
      'The office move is postponed until the lease is signed.',
    ],
  },
  agreement: {
    title: 'Supplier agreement - draft 3',
    paragraphs: [
      'This agreement is made between Northwind Studio Ltd. and its hosting supplier.',
      { heading: '1. Services' },
      'The supplier runs the servers named in Annex A and keeps them patched within seven days of a security release.',
      { heading: '2. Confidentiality' },
      'Neither party discloses the other party\'s documents to anyone outside this agreement.',
    ],
  },
};
