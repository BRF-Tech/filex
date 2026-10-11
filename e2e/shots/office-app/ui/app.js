// The Office example interface: a Word document (.docx) - its paragraphs, read
// from word/document.xml - drawn as a page you type in, saved back as a .docx.
// With filex 0.56 it also edits the document together with whoever else opens
// it (`co_edit`, granted as files:co-edit): who is in the session, a
// watcher's line for a person who may only read the file, the others' cursors.
// In an encrypted folder or a vault filex hands it the file in the clear
// (`encrypted_folders`, files:e2e-plaintext) and it says so in its bar.
//
// Everything goes through @brftech/filex-app-ui, loaded by index.html as
// dist/filex-app-ui.iife.js (window.FilexAppUI). Written for filex's
// screenshots (e2e/shots/officeapp.mjs, e2eapp.mjs); it is not published.
//
// ⚠ No innerHTML: every word on the page comes from the file or from filex,
// so it is set as text. The page reads the body's data attributes the scenes
// wait for: ready, together (alone / editing / watching), members.

const W = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main';
const COLOURS = ['#2f6ceb', '#d9480f', '#2b8a3e', '#ae3ec9', '#c92a2a', '#0b7285'];

const $ = (id) => document.getElementById(id);

(async () => {
  const page = $('page');
  let fx;
  try {
    fx = await window.FilexAppUI.connect();
  } catch {
    page.replaceChildren(el('p', 'status', 'This page runs inside filex.'));
    return;
  }

  const file = await fx.open();
  const opened = await file.bytes();
  $('name').textContent = file.name;
  if (file.encrypted && file.plaintext) {
    const lock = $('lock');
    lock.textContent = file.encrypted === 'vault' ? 'In a vault · read by your browser' : 'Encrypted folder · decrypted by your browser';
    lock.hidden = false;
  }

  // ── editing together, when filex offers it ──────────────────────────────
  let me = null;
  let base = opened;
  try {
    const hello = await fx.coedit.join();
    me = hello.me;
    // The member that started the session puts the base - exactly the bytes
    // it opened - before anybody else may join; the others read that base.
    if (hello.created) await fx.coedit.putBlob('base', opened.slice(0));
    else base = await fx.coedit.getBlob('base');
  } catch {
    me = null;
  }
  const together = !!me;
  const canEdit = together ? !!me.canEdit : !file.readOnly;

  let doc = await documentOf(base, file.ext);
  let changesSeen = 0;
  let lastSeq = 0;
  const members = new Map();
  const cursors = new Map();

  render();
  page.contentEditable = canEdit ? 'true' : 'false';
  page.spellcheck = false;
  for (const b of document.querySelectorAll('.tools button')) b.disabled = !canEdit;
  if (together && !canEdit) {
    const w = $('watching');
    w.textContent = 'You are watching: every change reaches you as it is made. You may read this document, not change it.';
    w.hidden = false;
  }
  document.body.dataset.together = together ? (canEdit ? 'editing' : 'watching') : 'alone';
  let dirty = false;
  state(together ? sessionWord() : canEdit ? 'Saved' : 'Read only');

  if (together) {
    fx.on('coedit.entry', (e) => onEntry(e));
    fx.on('coedit.cursor', (c) => onCursor(c));
    fx.on('coedit.dropped', () => state('Editing alone'));
    await fx.coedit.subscribe(0);
  }

  fx.onSave(async () => {
    const bytes = docxOf(readPage());
    if (together) await fx.save(bytes, { through: lastSeq });
    else await fx.save(bytes);
    fx.dirty(false);
    dirty = false;
    state(together ? sessionWord() : 'Saved');
  });

  let timer = null;
  page.addEventListener('input', () => {
    fx.dirty(true);
    dirty = true;
    state('Not saved');
    if (!together || !canEdit) return;
    clearTimeout(timer);
    timer = setTimeout(() => void send(), 250);
  });
  document.addEventListener('selectionchange', () => {
    if (!together || !canEdit) return;
    const i = paragraphAt(document.getSelection()?.anchorNode);
    if (i >= 0) fx.coedit.cursor({ p: i });
  });
  for (const b of document.querySelectorAll('.tools button')) {
    b.addEventListener('mousedown', (ev) => ev.preventDefault());
    b.addEventListener('click', () => format(b.dataset.cmd));
  }
  document.body.dataset.ready = '1';

  // ── the session's log ────────────────────────────────────────────────────
  function onEntry(e) {
    // What arrived, for a scene that waits on it to say what it saw.
    (window.__entries ||= []).push(`${e.seq}:${e.kind}`);
    lastSeq = Math.max(lastSeq, e.seq);
    if (e.kind === 'join' && e.member) members.set(e.member.client, e.member);
    else if (e.kind === 'leave') {
      members.delete(e.client);
      cursors.delete(e.client);
    } else if (e.kind === 'changes') {
      changesSeen = e.seq;
      if (e.client !== me.client && e.body && Array.isArray(e.body.paragraphs)) {
        doc = e.body.paragraphs;
        render();
      }
    }
    people();
  }

  function onCursor(c) {
    if (!c || !c.client || c.client === me.client) return;
    cursors.set(c.client, c.cursor);
    marks();
  }

  async function send() {
    try {
      const granted = await fx.coedit.lease('acquire', changesSeen);
      if (!granted) {
        timer = setTimeout(() => void send(), 400);
        return;
      }
      const { seq } = await fx.coedit.append('changes', { paragraphs: readPage() });
      changesSeen = Math.max(changesSeen, seq);
      await fx.coedit.lease('release', changesSeen);
    } catch {
      state('Editing alone');
    }
  }

  // ── drawing ──────────────────────────────────────────────────────────────
  function render() {
    const rows = doc.length ? doc : [{ style: 'p', text: '' }];
    page.replaceChildren(
      ...rows.map((p, i) => {
        const n = el(p.style === 'h1' || p.style === 'h2' ? p.style : 'p', '', p.text);
        if (!p.text) n.append(document.createElement('br'));
        n.dataset.i = String(i);
        return n;
      }),
    );
    marks();
  }

  function people() {
    const list = $('people');
    const shown = [...members.values()].sort((a, b) => a.indexUser - b.indexUser);
    list.replaceChildren(
      ...shown.map((m) => {
        const li = el('li', m.canEdit ? 'who' : 'who watch');
        li.style.setProperty('--c', colourOf(m));
        const you = m.client === me.client ? ' (you)' : '';
        li.append(el('span', 'dot', initials(m.name)), el('span', '', `${m.name || 'Someone'}${you}`), el('span', 'role', m.canEdit ? 'editing' : 'watching'));
        return li;
      }),
    );
    document.body.dataset.members = String(shown.length);
    if (!dirty) state(sessionWord());
    marks();
  }

  /** What the bar says of the session: who may write, and whether anybody else is in. */
  function sessionWord() {
    if (!canEdit) return 'Watching';
    return members.size > 1 ? 'Editing together' : 'Editing';
  }

  function marks() {
    for (const n of page.querySelectorAll('[data-by]')) {
      delete n.dataset.by;
      n.style.removeProperty('--c');
    }
    for (const [client, cursor] of cursors) {
      const m = members.get(client);
      const n = m && cursor && typeof cursor.p === 'number' ? page.children[cursor.p] : null;
      if (!n) continue;
      n.dataset.by = m.name || 'Someone';
      n.style.setProperty('--c', colourOf(m));
    }
  }

  function readPage() {
    return [...page.children].map((n) => ({
      style: n.tagName === 'H1' ? 'h1' : n.tagName === 'H2' ? 'h2' : 'p',
      text: n.textContent || '',
    }));
  }

  function paragraphAt(node) {
    let n = node && node.nodeType === 3 ? node.parentElement : node;
    while (n && n.parentElement !== page) n = n.parentElement;
    return n ? [...page.children].indexOf(n) : -1;
  }

  function format(cmd) {
    if (!canEdit) return;
    if (cmd === 'h1' || cmd === 'p') {
      const i = paragraphAt(document.getSelection()?.anchorNode);
      if (i < 0) return;
      const rows = readPage();
      rows[i].style = cmd;
      doc = rows;
      render();
    } else {
      document.execCommand(cmd);
    }
    page.dispatchEvent(new Event('input'));
  }

  function state(text) {
    $('state').textContent = text;
  }
})().catch((e) => {
  document.body.dataset.error = String((e && (e.message || e.code)) || e);
});

function el(tag, cls = '', text = '') {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text) n.textContent = text;
  return n;
}

function initials(name) {
  const parts = String(name || '?').trim().split(/ +/);
  return ((parts[0]?.[0] || '?') + (parts.length > 1 ? parts[parts.length - 1][0] : '')).toUpperCase();
}

function colourOf(m) {
  return COLOURS[Math.abs(Number(m.indexUser) || 0) % COLOURS.length];
}

// ── .docx: read word/document.xml, write a minimal document back ───────────

async function documentOf(bytes, ext) {
  if (ext !== 'docx' || !bytes || bytes.byteLength === 0) return [];
  const xml = await zipEntry(bytes, 'word/document.xml');
  if (!xml) return [];
  const d = new DOMParser().parseFromString(xml, 'application/xml');
  return [...d.getElementsByTagNameNS(W, 'p')].map((p) => {
    const st = p.getElementsByTagNameNS(W, 'pStyle')[0];
    const style = st ? st.getAttributeNS(W, 'val') || '' : '';
    const text = [...p.getElementsByTagNameNS(W, 't')].map((t) => t.textContent).join('');
    return { style: /^(title|heading1)$/i.test(style) ? 'h1' : /^heading/i.test(style) ? 'h2' : 'p', text };
  });
}

/** One entry of a zip, as text: stored or deflated. */
async function zipEntry(bytes, want) {
  const u8 = new Uint8Array(bytes);
  const dv = new DataView(u8.buffer, u8.byteOffset, u8.byteLength);
  let end = -1;
  for (let i = u8.length - 22; i >= Math.max(0, u8.length - 65557); i--) {
    if (dv.getUint32(i, true) === 0x06054b50) {
      end = i;
      break;
    }
  }
  if (end < 0) return null;
  const count = dv.getUint16(end + 10, true);
  let at = dv.getUint32(end + 16, true);
  const dec = new TextDecoder();
  for (let k = 0; k < count; k++) {
    if (dv.getUint32(at, true) !== 0x02014b50) return null;
    const method = dv.getUint16(at + 10, true);
    const size = dv.getUint32(at + 20, true);
    const nameLen = dv.getUint16(at + 28, true);
    const extraLen = dv.getUint16(at + 30, true);
    const commentLen = dv.getUint16(at + 32, true);
    const local = dv.getUint32(at + 42, true);
    if (dec.decode(u8.subarray(at + 46, at + 46 + nameLen)) === want) {
      const start = local + 30 + dv.getUint16(local + 26, true) + dv.getUint16(local + 28, true);
      const raw = u8.slice(start, start + size);
      if (method === 0) return dec.decode(raw);
      if (method !== 8) return null;
      return new Response(new Blob([raw]).stream().pipeThrough(new DecompressionStream('deflate-raw'))).text();
    }
    at += 46 + nameLen + extraLen + commentLen;
  }
  return null;
}

const CRC = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  return t;
})();

function crc32(u8) {
  let c = 0xffffffff;
  for (const b of u8) c = CRC[(c ^ b) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

const xmlText = (s) => String(s).replace(/[&<>]/g, (ch) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' })[ch]);

/** The paragraphs as a .docx: a stored zip of the three parts Word needs. */
function docxOf(paragraphs) {
  const body = paragraphs
    .map((p) => {
      const style = p.style === 'h1' ? '<w:pPr><w:pStyle w:val="Title"/></w:pPr>' : p.style === 'h2' ? '<w:pPr><w:pStyle w:val="Heading1"/></w:pPr>' : '';
      return `<w:p>${style}<w:r><w:t xml:space="preserve">${xmlText(p.text)}</w:t></w:r></w:p>`;
    })
    .join('');
  const files = [
    ['[Content_Types].xml', '<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>'],
    ['_rels/.rels', '<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>'],
    ['word/document.xml', `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="${W}"><w:body>${body}</w:body></w:document>`],
  ];
  const enc = new TextEncoder();
  const parts = [];
  const central = [];
  let offset = 0;
  for (const [name, text] of files) {
    const n = enc.encode(name);
    const data = enc.encode(text);
    const crc = crc32(data);
    const local = new DataView(new ArrayBuffer(30));
    local.setUint32(0, 0x04034b50, true);
    local.setUint16(4, 20, true);
    local.setUint16(6, 0x0800, true);
    local.setUint32(14, crc, true);
    local.setUint32(18, data.length, true);
    local.setUint32(22, data.length, true);
    local.setUint16(26, n.length, true);
    const dir = new DataView(new ArrayBuffer(46));
    dir.setUint32(0, 0x02014b50, true);
    dir.setUint16(4, 20, true);
    dir.setUint16(6, 20, true);
    dir.setUint16(8, 0x0800, true);
    dir.setUint32(16, crc, true);
    dir.setUint32(20, data.length, true);
    dir.setUint32(24, data.length, true);
    dir.setUint16(28, n.length, true);
    dir.setUint32(42, offset, true);
    parts.push(new Uint8Array(local.buffer), n, data);
    central.push(new Uint8Array(dir.buffer), n);
    offset += 30 + n.length + data.length;
  }
  const size = central.reduce((s, u) => s + u.length, 0);
  const end = new DataView(new ArrayBuffer(22));
  end.setUint32(0, 0x06054b50, true);
  end.setUint16(8, files.length, true);
  end.setUint16(10, files.length, true);
  end.setUint32(12, size, true);
  end.setUint32(16, offset, true);
  const all = [...parts, ...central, new Uint8Array(end.buffer)];
  const out = new Uint8Array(all.reduce((s, u) => s + u.length, 0));
  let at = 0;
  for (const u of all) {
    out.set(u, at);
    at += u.length;
  }
  return out.buffer;
}
