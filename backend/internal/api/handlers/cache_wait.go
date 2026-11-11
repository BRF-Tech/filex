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
     * a programmatic client that ASKS for it — `X-Filex-Accept-Prepare: 1` —
       gets 202 with {"state":"preparing","percent":N} and Retry-After, and
       polls ?cache=status itself;
     * every other programmatic client — XHR/fetch, the CLI, the desktop app,
       curl — gets the file, streamed, and no preparation is started for it.

   ⚠ That last line used to say those clients got the 202 too, "and not 200, so
   a client that ignores the body still cannot mistake it for the file". filex's
   own sync client did exactly that: any 2xx was the file, so it wrote the JSON
   to disk under the file's name, and the next run uploaded it over the real
   file (v0.20–v0.42; 45 files of 70–290 MB lost on one deployment). A status
   report is only safe to send to a caller that said it can read one.

   ⚠ A 202 is only ever sent where nothing has been charged for it. Public
   share links reserve a download off the link's cap BEFORE any byte leaves
   (v0.18.0, the day a "3 downloads" link served four) — telling that visitor
   "not yet" would spend one of their downloads on a JSON body. So share
   surfaces never call this; they read through the cache when it is warm and
   stream from the driver when it is not. */

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/leafname"
	"github.com/brf-tech/filex/backend/internal/templates"
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

// PrepareOptInHeader is how a programmatic client says it can use the
// "preparing" answer (202 + JSON + Retry-After) instead of a slow stream.
const PrepareOptInHeader = "X-Filex-Accept-Prepare"

// acceptsPrepare reports whether this programmatic request opted in to the
// "preparing" answer. Nothing else counts: `Accept: application/json` is what
// axios and friends send on EVERY request, so it cannot mean "I understand
// that a download may answer with a status report".
func acceptsPrepare(r *http.Request) bool {
	if r == nil {
		return false
	}
	v := strings.TrimSpace(r.Header.Get(PrepareOptInHeader))
	return v == "1" || strings.EqualFold(v, "true")
}

// writeCachePreparing answers a request for a file whose local copy is still
// being prepared. Only for callers that can use it: see vfStream.
func writeCachePreparing(w http.ResponseWriter, r *http.Request, rend *templates.Renderer, name string, prep *filebody.Prep) {
	if wantsHTML(r) {
		renderCacheWaitPage(w, r, rend, name, prep)
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

// renderCacheWaitPage is the browser half (page.cache_wait): a
// dependency-free progress page that polls &cache=status and reloads into the
// download when it is ready. It is a signed-in person's page, so it wears
// their tenant's template.
func renderCacheWaitPage(w http.ResponseWriter, r *http.Request, rend *templates.Renderer, name string, prep *filebody.Prep) {
	lang := publicLocale(r, "")
	t := publicT(lang)
	// The poll URL is this request's URL with cache=status added, so the path,
	// the adapter prefix and every other parameter survive verbatim.
	q := r.URL.Query()
	q.Set("cache", "status")
	poll, _ := json.Marshal(r.URL.Path + "?" + q.Encode())
	w.Header().Set("Cache-Control", "no-store")
	writePublicPage(w, r, rend, templates.Target{TenantID: mailTenant(r.Context(), nil), Host: r.Host, Lang: lang},
		"page.cache_wait", http.StatusAccepted, t["cache_title"], map[string]any{
			"file": map[string]any{"name": leafname.Visible(name), "percent": prep.Percent},
		}, &pageScript{Script: strings.Replace(cacheWaitScript, "__POLL__", string(poll), 1)})
}

// cacheWaitScript is page.cache_wait's own script (its hooks: #bar, #pct).
const cacheWaitScript = `(function(){
  var poll = __POLL__;
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
`
