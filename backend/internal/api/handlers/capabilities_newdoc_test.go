package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// #217: the New document dialog's "Office documents need a document server"
// line was the client's own sentence. The server now says it, in the
// reader's language, one sentence per service, and the "set it up under
// External services" half only to a caller who can.
func TestCapabilities_NewdocWithheld(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)

	withheldOf := func(t *testing.T, c *http.Client, lang string) map[string]string {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/files/capabilities", nil)
		require.NoError(t, err)
		req.Header.Set("Accept-Language", lang)
		res, err := c.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		var out struct {
			Withheld map[string]string `json:"newdoc_withheld"`
		}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
		return out.Withheld
	}

	t.Run("anonymous, English: the two services, no admin line", func(t *testing.T) {
		w := withheldOf(t, &http.Client{}, "en")
		require.Equal(t, "Office documents need an office engine, the Office editor app or ONLYOFFICE, and neither is set up here.", w["onlyoffice"])
		require.Equal(t, "Diagrams need draw.io, which is not set up here.", w["drawio"])
		_, hasAdmin := w["admin"]
		require.False(t, hasAdmin, "only a caller who can set a service up is told where")
	})

	t.Run("in the reader's language", func(t *testing.T) {
		w := withheldOf(t, &http.Client{}, "tr-TR,tr;q=0.9")
		require.Equal(t, "Ofis belgeleri için bir ofis motoru (Ofis düzenleyici uygulaması ya da ONLYOFFICE) gerekir; burada ikisi de kurulu değil.", w["onlyoffice"])
	})

	t.Run("an administrator is told where to set them up", func(t *testing.T) {
		email, pw := testutil.SeedAdmin(t, store)
		testutil.LoginAs(t, srv, client, email, pw)
		w := withheldOf(t, client, "en")
		require.Equal(t, "Set them up in the admin panel under Apps or External services.", w["admin"])
	})
}
