package appstore_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/appstore"
	"github.com/brf-tech/filex/backend/internal/appstore/storetest"
	"github.com/brf-tech/filex/backend/internal/netguard"
)

// A store is reached through a guarded client: an address on this machine or
// the private network is refused - as a literal, and after DNS (a
// public-looking name that resolves inward: DNS rebinding) - and a redirect
// is not followed at all.
func TestClient_TheGuardHoldsForStores(t *testing.T) {
	ctx := context.Background()
	st := storetest.New()
	defer st.Close()
	st.AddKey("idx-1", appstore.UseIndex, appstore.KeyActive)

	guarded := appstore.NewClient(netguard.Policy{}, "t")
	if _, err := guarded.Keys(ctx, st.Origin()); code(err) != appstore.CodeBadStore {
		t.Fatalf("a literal loopback address without loopback sources: want %s, got %v", appstore.CodeBadStore, err)
	}
	port := st.Origin()[strings.LastIndex(st.Origin(), ":"):]
	if _, err := guarded.Keys(ctx, "http://localhost"+port); code(err) != appstore.CodeBadStore {
		t.Fatalf("a name that resolves to loopback: want %s (judged after DNS), got %v", appstore.CodeBadStore, err)
	}

	// With loopback admitted (the tests' store), a redirect to the private
	// network or the metadata service is not followed: no redirect is.
	for _, target := range []string{"http://10.0.0.5/v1/keys.json", "http://169.254.169.254/latest/meta-data/"} {
		redir := httptest.NewServer(http.RedirectHandler(target, http.StatusFound))
		c := appstore.NewClient(netguard.Policy{Loopback: true}, "t")
		_, err := c.Keys(ctx, redir.URL)
		redir.Close()
		if code(err) != appstore.CodeBadAnswer {
			t.Fatalf("redirect to %s: want %s (not followed), got %v", target, appstore.CodeBadAnswer, err)
		}
	}

	ok := appstore.NewClient(netguard.Policy{Loopback: true}, "t")
	if _, err := ok.Keys(ctx, st.Origin()); err != nil {
		t.Fatalf("the loopback store with loopback sources: %v", err)
	}
}

// An answer bigger than a store's answer can be is refused, not read on.
func TestClient_AnOversizedAnswerIsRefused(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"keys":[` + strings.Repeat(" ", 128<<10) + `]}`))
	}))
	defer big.Close()
	c := appstore.NewClient(netguard.Policy{Loopback: true}, "t")
	if _, err := c.Keys(context.Background(), big.URL); code(err) != appstore.CodeBadAnswer {
		t.Fatalf("a 128 KiB keys.json: want %s, got %v", appstore.CodeBadAnswer, err)
	}
}
