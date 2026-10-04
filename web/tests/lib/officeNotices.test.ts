// filex 0.51: what an ONLYOFFICE save says to the people who edited the
// document, in their language. The server phrases it from its catalogue
// (srvtext `server.onlyoffice.*`) in every built-in language into the row's
// meta (`title_<lang>`, `body_<lang>`), on the plain file events:
//
//   - `file.uploaded` of the file the edit was saved as, beside a document
//     whose format the document server cannot write (rapor.doc → rapor.docx);
//   - `file.upload_failed` of a save that was not written.
//
// ⚠ Fails on 0.50: those rows were said as "New file: rapor.docx" and "Upload
// failed: list.csv" with the server's English reason.
import { describe, expect, it } from 'vitest';

import { renderNotification, type NotificationLike } from '@brftech/filex-core/src/lib/notificationText';

const savedBeside: NotificationLike = {
  event: 'file.uploaded',
  title: 'file.uploaded',
  body: '/Belgeler/rapor.docx',
  meta: {
    origin: 'onlyoffice',
    node: { storage_id: 3, path: '/Belgeler/rapor.docx', name: 'rapor.docx', size: 1234 },
    saved_beside: '/Belgeler/rapor.doc',
    title_en: 'Your edit was saved as rapor.docx',
    title_tr: 'Düzenlemeniz rapor.docx olarak kaydedildi',
    body_en: 'ONLYOFFICE saved rapor.doc as DOCX, which a .doc file cannot hold, so your edit is in rapor.docx, in the same folder. rapor.doc did not change.',
    body_tr: 'ONLYOFFICE rapor.doc dosyasını DOCX biçiminde kaydetti; bir .doc dosyası bunu tutamadığı için düzenlemeniz aynı klasörde rapor.docx adıyla duruyor. rapor.doc değişmedi.',
  },
};

const refused: NotificationLike = {
  event: 'file.upload_failed',
  title: 'Your edit to list.csv was not saved',
  body: '/list.csv',
  meta: {
    origin: 'onlyoffice',
    node: { storage_id: 3, path: '/list.csv', name: 'list.csv' },
    reason: 'ONLYOFFICE saved it as PDF, which filex does not keep for a .csv file.',
    title_en: 'Your edit to list.csv was not saved',
    title_tr: 'list.csv dosyasındaki düzenlemeniz kaydedilmedi',
    body_en: 'ONLYOFFICE saved it as PDF, which filex does not keep for a .csv file. list.csv did not change.',
    body_tr: 'ONLYOFFICE dosyayı PDF biçiminde kaydetti; filex bunu bir .csv dosyası için tutmaz. list.csv değişmedi.',
  },
};

describe('an ONLYOFFICE save, said to the people who edited it', () => {
  it('saved beside: in the reader’s language', () => {
    expect(renderNotification(savedBeside, 'tr')).toEqual({
      title: 'Düzenlemeniz rapor.docx olarak kaydedildi',
      body: 'ONLYOFFICE rapor.doc dosyasını DOCX biçiminde kaydetti; bir .doc dosyası bunu tutamadığı için düzenlemeniz aynı klasörde rapor.docx adıyla duruyor. rapor.doc değişmedi.',
    });
    expect(renderNotification(savedBeside, 'en').title).toBe('Your edit was saved as rapor.docx');
  });

  it('not written: in the reader’s language, not the server’s English reason', () => {
    expect(renderNotification(refused, 'tr')).toEqual({
      title: 'list.csv dosyasındaki düzenlemeniz kaydedilmedi',
      body: 'ONLYOFFICE dosyayı PDF biçiminde kaydetti; filex bunu bir .csv dosyası için tutmaz. list.csv değişmedi.',
    });
    expect(renderNotification(refused, 'en').body).toBe(
      'ONLYOFFICE saved it as PDF, which filex does not keep for a .csv file. list.csv did not change.',
    );
  });

  it('a language pack’s phrase for the plain event does not stand in for the server’s words', () => {
    const strings = { 'server.notify.file.uploaded.title': 'Neue Datei: {name}' };
    expect(renderNotification(savedBeside, 'en', { strings, lang: 'de' }).title).toBe('Your edit was saved as rapor.docx');
  });

  it('the plain events are said as before', () => {
    const plain = { ...savedBeside, meta: { node: { path: '/a/b.txt', name: 'b.txt' } } };
    expect(renderNotification(plain, 'tr').title).toBe('Yeni dosya: b.txt');
    const failed = { ...refused, title: 'Upload failed', meta: { node: { path: '/a/b.txt', name: 'b.txt' }, reason: 'disk full' } };
    expect(renderNotification(failed, 'en')).toEqual({ title: 'Upload failed: b.txt', body: 'disk full' });
  });
});
