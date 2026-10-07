/*
 * The ONLYOFFICE frame (task #92): the page the document server's api.js runs
 * in, on another origin than filex's - the document server's own
 * (FILEX_ONLYOFFICE_FRAME_ORIGIN, at /filex-frame/editor, which its proxy
 * sends to filex) or the app-interface origin (FILEX_APP_UI_ORIGIN, at
 * <base>/_appui/_onlyoffice/editor); backend/internal/onlyoffice/frame.go. An
 * origin of its own, so the document server's code never runs where filex's
 * session lives - not the web client's bearer, not filex's storage, not its
 * pages.
 *
 * The page it is framed by (core lib/officeFrame.ts) and this script speak a
 * narrow protocol, `filex-oo` version 1:
 *
 *   1. here -> the page that framed us: {proto, v, type: "hello", session}.
 *      The session is the fragment the page put on our address; nothing else
 *      is said before the page answers.
 *   2. the page -> here, once: {proto, v, type: "open", session, config} with
 *      one MessagePort. Taken only from our parent window, only with our own
 *      session, only once per load. `config` is the editor configuration as
 *      filex's server signed it; this script adds the event handlers and
 *      nothing else, and hands it to DocsAPI.DocEditor.
 *   3. here -> the page, over that port only: "started", "ready",
 *      "state" {dirty}, "error" {channel, code, description}, "failed"
 *      {reason}. The page sends nothing back: closing the editor is taking
 *      this frame away.
 *
 * Which document server: the one filex's server names in data-api (its
 * configuration in force), never one the framing page names, and the page's
 * policy allows no script but this one and that server's.
 *
 * Only `window`, `document` and `location` are read, so a test can run this
 * file against stand-ins for the three (web/tests/components/officeFrame.test.ts).
 */
(function () {
  'use strict';
  var PROTO = 'filex-oo';
  var V = 1;
  var MOUNT = 'filex-oo-editor';
  var api = document.documentElement.getAttribute('data-api') || '';
  var session = String(location.hash || '').replace(/^#/, '');
  var parentWin = window.parent;
  // Opened on its own (no page around it), or with no session of the page's:
  // there is nobody to open a document for.
  if (!api || !parentWin || parentWin === window || !/^[A-Za-z0-9_-]{16,128}$/.test(session)) return;

  var taken = false;
  var port = null;
  var editor = null;

  function send(type, extra) {
    if (!port) return;
    var m = { proto: PROTO, v: V, type: type };
    if (extra) {
      for (var k in extra) {
        if (Object.prototype.hasOwnProperty.call(extra, k)) m[k] = extra[k];
      }
    }
    try {
      port.postMessage(m);
    } catch (e) {
      /* the page went away */
    }
  }

  function problem(channel, ev) {
    var d = ev && ev.data && typeof ev.data === 'object' ? ev.data : {};
    send('error', {
      channel: channel,
      code: typeof d.errorCode === 'number' ? d.errorCode : null,
      description: typeof d.errorDescription === 'string' ? d.errorDescription.slice(0, 1000) : '',
    });
  }

  function start(config) {
    var s = document.createElement('script');
    s.src = api;
    s.async = true;
    s.onerror = function () {
      send('failed', { reason: 'script' });
    };
    s.onload = function () {
      var D = window.DocsAPI;
      if (!D || typeof D.DocEditor !== 'function') {
        send('failed', { reason: 'api' });
        return;
      }
      config.events = {
        onDocumentReady: function () {
          send('ready');
        },
        // ONLYOFFICE's `data: false` means the edits reached the document
        // server, not the file: the page keeps "edited" sticky (#184).
        onDocumentStateChange: function (ev) {
          send('state', { dirty: !!(ev && ev.data) });
        },
        onError: function (ev) {
          problem('onError', ev);
        },
        onWarning: function (ev) {
          problem('onWarning', ev);
        },
      };
      try {
        editor = new D.DocEditor(MOUNT, config);
        send('started');
      } catch (e) {
        editor = null;
        send('failed', { reason: 'api' });
      }
    };
    document.head.appendChild(s);
  }

  window.addEventListener('message', function (ev) {
    if (taken || ev.source !== parentWin) return;
    var m = ev.data;
    if (!m || typeof m !== 'object' || m.proto !== PROTO || m.v !== V || m.type !== 'open') return;
    if (m.session !== session) return;
    var config = m.config;
    if (!config || typeof config !== 'object' || Array.isArray(config)) return;
    if (!ev.ports || ev.ports.length !== 1) return;
    taken = true;
    port = ev.ports[0];
    start(config);
  });

  parentWin.postMessage({ proto: PROTO, v: V, type: 'hello', session: session }, '*');
})();
