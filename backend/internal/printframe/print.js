(function () {
  'use strict';
  var parentWin = window.parent;
  if (!parentWin || parentWin === window) return;

  // The print under way: its port, the blob: PDF's frame, the button.
  var job = null;

  // What of the parent's look the button takes: colours, borders, font and
  // padding, each a plain value.
  var LOOK = /^(background-color|color|border-(top|right|bottom|left)-(color|width|style)|border-(top-left|top-right|bottom-right|bottom-left)-radius|font-(family|size|weight|style)|line-height|letter-spacing|padding-(top|right|bottom|left))$/;
  var BAD = /[;{}<>\\!@]|url\(|expression/i;

  function reply(port, msg) {
    try {
      port.postMessage(msg);
    } catch (e) {}
  }

  function drop() {
    if (!job) return;
    var j = job;
    job = null;
    j.done = true;
    if (j.guard) clearTimeout(j.guard);
    try {
      j.port.onmessage = null;
      j.port.close();
    } catch (e) {}
    if (j.url) {
      try {
        URL.revokeObjectURL(j.url);
      } catch (e) {}
    }
    if (j.frame && j.frame.parentNode) j.frame.parentNode.removeChild(j.frame);
    if (j.button && j.button.parentNode) j.button.parentNode.removeChild(j.button);
  }

  function isHead(b) {
    return b.length >= 5 && b[0] === 0x25 && b[1] === 0x50 && b[2] === 0x44 && b[3] === 0x46 && b[4] === 0x2d;
  }

  // Do the bytes start like a PDF (%PDF-)? A Blob is asked for its first
  // five bytes only.
  function head(pdf, then) {
    if (pdf instanceof ArrayBuffer) {
      then(isHead(new Uint8Array(pdf, 0, Math.min(5, pdf.byteLength))));
      return;
    }
    if (typeof Blob !== 'undefined' && pdf instanceof Blob) {
      pdf
        .slice(0, 5)
        .arrayBuffer()
        .then(
          function (b) {
            then(isHead(new Uint8Array(b)));
          },
          function () {
            then(false);
          },
        );
      return;
    }
    then(false);
  }

  function look(b, l) {
    if (!l || typeof l !== 'object') return;
    for (var k in l) {
      if (!Object.prototype.hasOwnProperty.call(l, k)) continue;
      var v = l[k];
      if (!LOOK.test(k) || typeof v !== 'string' || !v || v.length > 200 || BAD.test(v)) continue;
      b.style.setProperty(k, v);
      if (k === 'background-color') b.style.setProperty('outline-color', v);
    }
  }

  function print(j) {
    if (j.done) return;
    j.done = true;
    if (j.guard) clearTimeout(j.guard);
    try {
      j.frame.contentWindow.focus();
      j.frame.contentWindow.print();
      reply(j.port, { ok: true });
    } catch (e) {
      reply(j.port, { ok: false, code: 'failed' });
    }
  }

  function start(port, d) {
    drop();
    var j = { port: port, done: false, armed: false, loaded: false, clicked: false, guard: null, url: '', frame: null, button: null };
    job = j;
    port.onmessage = function (m) {
      var x = m && m.data;
      if (job !== j || !x || typeof x !== 'object') return;
      if (x.type === 'arm') {
        j.armed = true;
        if (j.button && !j.clicked) j.button.disabled = false;
      } else if (x.type === 'cancel') {
        drop();
      }
    };
    head(d.pdf, function (ok) {
      if (job !== j) return;
      if (!ok) {
        reply(port, { ok: false, code: 'invalid' });
        drop();
        return;
      }
      // Always served as a PDF, whatever type the bytes came with.
      j.url = URL.createObjectURL(new Blob([d.pdf], { type: 'application/pdf' }));
      var f = document.createElement('iframe');
      f.className = 'pdf';
      f.setAttribute('title', 'PDF');
      f.setAttribute('tabindex', '-1');
      f.setAttribute('aria-hidden', 'true');
      f.addEventListener('load', function () {
        if (job !== j || j.loaded) return;
        j.loaded = true;
        if (j.clicked) {
          setTimeout(function () {
            print(j);
          }, 100);
        }
      });
      j.frame = f;
      f.src = j.url;
      document.body.appendChild(f);

      var b = document.createElement('button');
      b.type = 'button';
      b.disabled = true;
      b.dir = 'auto';
      b.setAttribute('data-testid', 'print-allow');
      b.textContent = typeof d.label === 'string' && d.label.trim() ? d.label.trim().slice(0, 80) : 'Print';
      look(b, d.look);
      b.addEventListener('click', function (ev) {
        if (job !== j || j.done || j.clicked || !j.armed) return;
        // The person's own click only: a script's click is not trusted, and
        // the browser says whether a person acted on THIS page a moment ago.
        if (!ev || ev.isTrusted !== true) return;
        var ua = navigator.userActivation;
        if (ua && ua.isActive !== true) return;
        j.clicked = true;
        b.disabled = true;
        if (j.loaded) {
          print(j);
          return;
        }
        // A browser without a PDF viewer downloads the PDF instead of
        // drawing it, and the frame never loads.
        j.guard = setTimeout(function () {
          if (job !== j || j.done) return;
          j.done = true;
          reply(port, { ok: false, code: 'failed' });
        }, 15000);
      });
      j.button = b;
      document.body.appendChild(b);
      var r = b.getBoundingClientRect();
      reply(port, { state: 'ask', width: Math.ceil(r.width) + 6, height: Math.ceil(r.height) + 6 });
    });
  }

  window.addEventListener('message', function (ev) {
    if (ev.source !== parentWin) return;
    var d = ev.data;
    if (!d || typeof d !== 'object' || d.type !== 'filex:print' || !ev.ports || !ev.ports[0]) return;
    start(ev.ports[0], d);
  });

  parentWin.postMessage({ type: 'filex:print-ready' }, '*');
})();
