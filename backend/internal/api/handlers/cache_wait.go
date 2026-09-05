package handlers

/* "Preparing your download…" — the answer a big file on a slow backend gives
   before it gives bytes.

   The requirement, verbatim (translated from Turkish): "if the fs is slow and
   the file is big, we need to do something like telling the user we're building
   a cache just for them and starting the download once the cache is ready."

   Two clients, two shapes, one decision — mirroring what folder-share ZIPs
   have done since they grew a cache (share.go: renderZipWaitPage + ?zip=status):

     * a browser (the explorer opens downloads with window.open, so the
       response IS the page the user is looking at) gets a progress page that
       polls and then starts the download itself;
     * anything programmatic — XHR/fetch, the CLI, the desktop app, curl — gets
       202 with {"state":"preparing","percent":N}. 202 and not 200, so a client
       that ignores the body still cannot mistake it for the file.

   ⚠ A 202 is only ever sent where nothing has been charged for it. Public
   share links reserve a download off the link's cap BEFORE any byte leaves
   (v0.18.0, the day a "3 downloads" link served four) — telling that visitor
   "not yet" would spend one of their downloads on a JSON body. So share
   surfaces never call this; they read through the cache when it is warm and
   stream from the driver when it is not. */

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/filebody"
)

// wantsHTML reports whether this request is a browser navigation rather than a
// programmatic call. A fetch()/XHR sets X-Requested-With or asks for JSON; a
// navigation asks for text/html.
func wantsHTML(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.Header.Get("X-Requested-With") != "" {
		return false
	}
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") {
		return false
	}
	return strings.Contains(accept, "text/html")
}

// writeCachePreparing answers a request for a file whose local copy is still
// being prepared.
func writeCachePreparing(w http.ResponseWriter, r *http.Request, name string, prep *filebody.Prep) {
	if wantsHTML(r) {
		renderCacheWaitPage(w, r, name, prep)
		return
	}
	w.Header().Set("Retry-After", "2")
	writeJSON(w, http.StatusAccepted, map[string]any{
		"state":   "preparing",
		"ready":   false,
		"percent": prep.Percent,
		"size":    prep.Size,
		"name":    name,
	})
}

// writeCacheStatus answers the progress poll. Always 200 and always JSON, even
// once the copy is ready, so the wait page's fetch().json() never chokes — the
// same rule ?zip=status follows.
func writeCacheStatus(w http.ResponseWriter, prep *filebody.Prep) {
	if prep == nil {
		// Nothing is being prepared: either it never qualified or the fetch
		// failed. Either way the answer to "may I ask for the file now" is
		// yes — the download will stream from the driver.
		writeJSON(w, http.StatusOK, map[string]any{"state": "ready", "ready": true, "percent": 100})
		return
	}
	if prep.Ready {
		writeJSON(w, http.StatusOK, map[string]any{"state": "ready", "ready": true, "percent": 100, "size": prep.Size})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"state": "preparing", "ready": false, "percent": prep.Percent, "size": prep.Size,
	})
}

// renderCacheWaitPage is the browser half: a dependency-free progress page
// that polls &cache=status and reloads into the download when it is ready.
func renderCacheWaitPage(w http.ResponseWriter, r *http.Request, name string, prep *filebody.Prep) {
	lang := publicLocale(r, "")
	t := publicT(lang)

	// The poll URL is this request's URL with cache=status added, so the path,
	// the adapter prefix and every other parameter survive verbatim.
	q := r.URL.Query()
	q.Set("cache", "status")
	pollURL := r.URL.Path + "?" + q.Encode()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusAccepted)
	_ = cacheWaitTemplate.Execute(w, map[string]any{
		"Lang":    lang,
		"T":       t,
		"Name":    name,
		"Sub":     fmt.Sprintf(t["cache_sub"], name),
		"Poll":    pollURL,
		"Percent": prep.Percent,
	})
}

var cacheWaitTemplate = template.Must(template.New("cachewait").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.T.cache_title}}</title>
` + publicPageStyle + `
<style>
.track { height: 10px; border-radius: 999px; background: var(--px-line); overflow: hidden; }
.bar { height: 100%; width: 0%; border-radius: 999px; background: linear-gradient(90deg, var(--px-accent), var(--px-accent-hover)); transition: width 0.4s ease; }
.pct { margin-top: 10px; font-size: 0.95rem; font-weight: 600; font-variant-numeric: tabular-nums; }
.hint { margin: 16px 0 0; font-size: 0.8rem; color: var(--px-muted); }
@media (prefers-reduced-motion: reduce) { .bar { transition: none; } }
</style>
</head><body>
<main class="wrap">
<div class="card">
<h1>{{.T.cache_heading}}</h1>
<p class="sub">{{.Sub}}</p>
<div class="track" aria-hidden="true"><div id="bar" class="bar"></div></div>
<div class="pct"><span id="pct">%{{.Percent}}</span></div>
<p class="hint">{{.T.cache_hint}}</p>
</div>
</main>
<script>
(function(){
  var poll = "{{.Poll}}";
  var bar = document.getElementById('bar');
  var pct = document.getElementById('pct');
  function tick(){
    fetch(poll, { credentials: 'same-origin', headers: { 'Accept': 'application/json' } })
      .then(function(r){ return r.json(); })
      .then(function(j){
        var p = typeof j.percent === 'number' ? j.percent : 0;
        bar.style.width = p + '%';
        pct.textContent = '%' + p;
        if (j.ready) { location.reload(); return; }
        setTimeout(tick, 1500);
      })
      .catch(function(){ setTimeout(tick, 3000); });
  }
  setTimeout(tick, 800);
})();
</script>
</body></html>`))
