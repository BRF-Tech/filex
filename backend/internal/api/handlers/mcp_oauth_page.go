package handlers

import (
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/basepath"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// The pages the authorization endpoint shows in the browser: the consent
// page and, when the request cannot go on, a page that says why. They are
// written by the server, so every sentence is srvtext's (server.mcp_oauth.*),
// in the reader's language. The machine endpoints (register, token, revoke)
// answer OAuth's JSON errors, which no person reads.

type oauthPageKind string

const (
	oauthPageInvalid     oauthPageKind = "invalid"
	oauthPageExpired     oauthPageKind = "expired"
	oauthPageDenied      oauthPageKind = "denied"
	oauthPageScope       oauthPageKind = "scope"
	oauthPageBusy        oauthPageKind = "busy"
	oauthPageUnavailable oauthPageKind = "unavailable"
)

// oauthPageData is one page, in reading order: who asks (or why not), the
// note on the name, what allowing it gives, where the browser goes, the
// buttons, and how to end it later.
type oauthPageData struct {
	Lang, Dir, Title string
	Paragraphs       []string
	Note             string
	Can              string
	Scopes           []string
	Return           string
	Detail           string
	Form             *oauthConsentForm
	After            string
}

type oauthConsentForm struct {
	Action, Request, CSRF, Allow, Deny string
}

var oauthPageTmpl = template.Must(template.New("mcp-oauth").Parse(`<!doctype html>
<html lang="{{.Lang}}" dir="{{.Dir}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>{{.Title}}</title>
<style>html{color-scheme:light dark;font-family:system-ui,sans-serif;line-height:1.5}body{max-width:36rem;margin:3rem auto;padding:1rem}button{font:inherit;padding:.75rem 1rem;min-height:3rem;margin:.5rem .5rem 0 0}button:focus-visible{outline:3px solid Highlight;outline-offset:3px}code,p,li{overflow-wrap:anywhere}.note{opacity:.8;font-size:.9em}</style></head>
<body><main><h1>{{.Title}}</h1>
{{range .Paragraphs}}<p>{{.}}</p>{{end}}
{{if .Note}}<p class="note">{{.Note}}</p>{{end}}
{{if .Can}}<p>{{.Can}}</p>{{end}}
{{if .Scopes}}<ul>{{range .Scopes}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{if .Return}}<p>{{.Return}}</p>{{end}}
{{if .Detail}}<p><code>{{.Detail}}</code></p>{{end}}
{{with .Form}}<form action="{{.Action}}" method="post"><input type="hidden" name="request" value="{{.Request}}"><input type="hidden" name="csrf" value="{{.CSRF}}">
<button type="submit" name="decision" value="deny">{{.Deny}}</button><button type="submit" name="decision" value="approve">{{.Allow}}</button></form>{{end}}
{{if .After}}<p class="note">{{.After}}</p>{{end}}
</main></body></html>`))

// writePage sends one of these pages. formTo is the origin the consent form
// sends the browser on to (its callback's): the form posts here and the
// answer is a redirect there, and a browser holds a form's redirect to
// form-action as well, so it is named beside 'self'. "" for every page
// without a form.
func writePage(w http.ResponseWriter, status int, d oauthPageData, formTo string) {
	csp := "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"
	if formTo != "" {
		csp += " " + formTo
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = oauthPageTmpl.Execute(w, d)
}

func oauthText(lang, key string, vars srvtext.Vars) string {
	return srvtext.Text(lang, "server.mcp_oauth."+key, vars)
}

// page says why an authorization cannot go on. detail is the technical
// reason (English, for the app's developer), when there is one to give.
func (h *MCPOAuth) page(w http.ResponseWriter, r *http.Request, status int, kind oauthPageKind, detail string) {
	lang := requestLang(r)
	writePage(w, status, oauthPageData{Lang: lang, Dir: pageDir(lang), Title: oauthText(lang, "error_title", nil),
		Paragraphs: []string{oauthText(lang, "error."+string(kind), nil)}, Detail: detail}, "")
}

// tenantPage refuses an account whose tenant may not hold grants of the
// request's address, and names the account's own MCP address instead.
func (h *MCPOAuth) tenantPage(w http.ResponseWriter, r *http.Request, u *model.User, req *mcpOAuthRequest) {
	lang := requestLang(r)
	own := h.tenants.Fallback()
	if u != nil && u.ProviderID != nil {
		own = h.tenants.ForProvider(r.Context(), *u.ProviderID)
	}
	site := oauthSite{Issuer: req.Issuer}
	writePage(w, http.StatusForbidden, oauthPageData{Lang: lang, Dir: pageDir(lang), Title: oauthText(lang, "error_title", nil),
		Paragraphs: []string{oauthText(lang, "error.tenant", srvtext.Vars{"address": site.host(), "mcp_url": strings.TrimRight(own, "/") + auth.MCPResourcePath})}}, "")
}

// consentPage asks the signed-in person to allow or deny req: who asks (the
// name the client gave itself, said to be its own), at which address, as
// which account, for what, and where the browser goes next.
func (h *MCPOAuth) consentPage(w http.ResponseWriter, r *http.Request, u *model.User, req *mcpOAuthRequest, id, csrf string) {
	lang := requestLang(r)
	name := req.ClientName
	if name == "" {
		name = oauthText(lang, "unnamed", nil)
	}
	back, _ := url.Parse(req.RedirectURI)
	site := oauthSite{Issuer: req.Issuer}
	d := oauthPageData{Lang: lang, Dir: pageDir(lang), Title: oauthText(lang, "title", nil),
		Paragraphs: []string{oauthText(lang, "asks", srvtext.Vars{"client": name, "address": site.host(), "account": u.Email})},
		Note:       oauthText(lang, "self_named", nil),
		Can:        oauthText(lang, "can", nil),
		Scopes:     oauthScopeLines(lang, req.Scope),
		Return:     oauthText(lang, "return", srvtext.Vars{"host": back.Host}),
		Form: &oauthConsentForm{Action: basepath.Path(r.Context(), mcpOAuthPath+"/authorize"), Request: id, CSRF: csrf,
			Allow: oauthText(lang, "allow", nil), Deny: oauthText(lang, "deny", nil)},
	}
	d.After = oauthText(lang, "revoke", nil)
	writePage(w, http.StatusOK, d, back.Scheme+"://"+back.Host)
}

// oauthScopeLines says what each granted scope lets the client do.
func oauthScopeLines(lang, scope string) []string {
	var out []string
	for _, s := range strings.Split(scope, ",") {
		switch {
		case s == "":
		case s == "read" || s == "write" || s == "delete" || s == "mcp":
			out = append(out, oauthText(lang, "scope."+s, nil))
		case strings.HasPrefix(s, apitoken.ScopeRootPrefix):
			out = append(out, oauthText(lang, "scope.root", srvtext.Vars{"folder": strings.TrimPrefix(s, apitoken.ScopeRootPrefix)}))
		default:
			out = append(out, s)
		}
	}
	return out
}
