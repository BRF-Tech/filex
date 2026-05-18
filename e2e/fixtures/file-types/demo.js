// filex client snippet — wires the FileExplorer SFC into a div.
import { mountFileExplorer } from '@brftech/filex-core';

const app = mountFileExplorer('#root', {
  api: { baseURL: '/api/files', credentials: 'include' },
  i18n: { locale: 'tr' },
});

window.addEventListener('beforeunload', () => app.unmount());
