/**
 * remoteImages — the person's "Load remote images in previews" setting.
 *
 * ⚠⚠ Why a picture waits at all: drawing a picture is a request to the server
 * it lives on, and that server learns that this file was opened, when, and
 * from which IP address. A shared Markdown file with a one-pixel picture in it
 * is a read receipt its reader never agreed to send. So a file's preview draws
 * a placeholder where such a picture is (lib/sanitizeHtml, RemoteImages), a
 * click asks before loading it (composables/useRemoteImages), and this
 * setting - off unless the person turns it on in User settings - lets previews
 * load them without asking. Thumbnails never load them, whatever it says: the
 * server draws a Markdown file's thumbnail from its text, and no thumbnail in
 * the browser draws markup.
 *
 * Kept on the ACCOUNT with the other preferences (lib/prefs, `remoteImages`),
 * so it follows the person from one browser to the next. ⚠ With nobody signed
 * in (a public link) it is off whatever this browser holds: `hasSession()`.
 */
import { currentPrefs, hasSession, prefsConfigured, savePref } from './prefs';

/** Fired on `window` when the setting changes here (a tab does not hear its own `storage` event). */
export const REMOTE_IMAGES_EVENT = 'filex:remote-images';

/** Previews load pictures from other servers without asking. */
export function remoteImagesOn(): boolean {
  return hasSession() && currentPrefs().remoteImages === 'on';
}

/**
 * The setting can be changed by this person here: an account document is
 * wired and somebody is signed in. The question a held picture asks points
 * at the setting only then.
 */
export function remoteImagesSettable(): boolean {
  return prefsConfigured() && hasSession();
}

/** The person turned the setting on or off. */
export function setRemoteImages(on: boolean): void {
  savePref('remoteImages', on ? 'on' : '');
  try {
    window.dispatchEvent(new CustomEvent(REMOTE_IMAGES_EVENT, { detail: on }));
  } catch {
    /* no window (a test) or an engine without the CustomEvent constructor */
  }
}
