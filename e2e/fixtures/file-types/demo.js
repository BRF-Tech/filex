/**
 * filex client snippet — wires the FileExplorer SFC into a div.
 *
 * Reactivity:
 * - openPageBase enables the new-tab "Aç" action
 * - i18n.locale flips the UI between tr / en
 * - api.credentials='include' so the cookie auth piggybacks on the
 *   embedding host's session
 */
import { mountFileExplorer } from '@brftech/filex-core';

const app = mountFileExplorer('#root', {
  api: {
    baseURL: '/api/files',
    credentials: 'include',
  },
  i18n: { locale: 'tr' },
  openPageBase: '/admin/files/edit',
});

window.addEventListener('beforeunload', () => app.unmount());

document.querySelector('#refresh')?.addEventListener('click', () => {
  app.reload();
});

// Resize the explorer to fit a custom dashboard tile.
const observer = new ResizeObserver(([entry]) => {
  app.setHeight(entry.contentRect.height);
});
observer.observe(document.querySelector('#root'));
