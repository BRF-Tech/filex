// The Board example interface: a .board file (JSON — a title and columns of
// cards) drawn as columns. A click moves a card one column on; filex's Save
// writes the file back. Everything goes through @brftech/filex-app-ui, loaded
// by index.html as dist/filex-app-ui.iife.js (window.FilexAppUI).
//
// ⚠ No innerHTML: every word on the board comes from the file, so it is set
// as text.

(async () => {
  const main = document.getElementById('board');
  const say = (text) => {
    main.replaceChildren(el('p', 'status', text));
  };

  let fx;
  try {
    fx = await window.FilexAppUI.connect();
  } catch {
    say('This page runs inside filex.');
    return;
  }

  const file = await fx.open();
  let board;
  try {
    board = JSON.parse(await file.text());
    if (!Array.isArray(board.columns)) throw new Error('no columns');
  } catch {
    say(`${file.name} is not a board.`);
    return;
  }

  fx.onSave(() => `${JSON.stringify(board, null, 2)}\n`);
  render();

  function render() {
    const cards = board.columns.reduce((n, c) => n + (c.cards?.length ?? 0), 0);
    const head = el('header', 'head');
    head.append(el('h1', '', board.title || file.name), el('span', 'count', `${cards} ${cards === 1 ? 'card' : 'cards'}`));

    const cols = el('div', 'cols');
    board.columns.forEach((col, ci) => {
      const section = el('section', 'col');
      const h = el('h2', '', col.title);
      h.append(el('span', 'n', String(col.cards?.length ?? 0)));
      const list = el('ul', 'cards');
      (col.cards ?? []).forEach((card, ki) => {
        const btn = el('button', 'card');
        btn.type = 'button';
        btn.append(el('span', 'title', card.title));
        if (card.tag) btn.append(el('span', 'tag', card.tag));
        if (ci < board.columns.length - 1) {
          btn.title = `Move to ${board.columns[ci + 1].title}`;
          btn.addEventListener('click', () => move(ci, ki));
        } else {
          btn.disabled = true;
        }
        const li = el('li');
        li.append(btn);
        list.append(li);
      });
      section.append(h, list);
      cols.append(section);
    });
    main.replaceChildren(head, cols);
  }

  function move(ci, ki) {
    const [card] = board.columns[ci].cards.splice(ki, 1);
    (board.columns[ci + 1].cards ??= []).push(card);
    fx.dirty(true);
    render();
  }
})();

function el(tag, cls = '', text = '') {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text) n.textContent = text;
  return n;
}
