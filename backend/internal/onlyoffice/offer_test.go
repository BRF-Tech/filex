package onlyoffice

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func offerService() *Service {
	return New(nil, nil, "http://ds.test", "offer-secret", "https://filex.test", 0)
}

func offeredQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "https://filex.test"+FetchPath+"?") {
		t.Fatalf("the address is not filex's fetch door: %s", raw)
	}
	return u.Query()
}

// A file on offer is the document server's to download for one conversion:
// by a token that is not its path, signed with the purpose, until it is
// withdrawn.
func TestOfferFile_OneConversionsDownload(t *testing.T) {
	s := offerService()
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "in.docx")
	if err := os.WriteFile(p, []byte("DOCX"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, withdraw, err := s.OfferFile(ctx, p, "in.docx")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "in.docx") || strings.Contains(raw, url.QueryEscape(p)) {
		t.Fatalf("the address names the file: %s", raw)
	}
	q := offeredQuery(t, raw)
	if q.Get("p") != PurposeConvert || q.Get("o") == "" || q.Get("n") != "" {
		t.Fatalf("query %v", q)
	}
	exp, _ := strconv.ParseInt(q.Get("exp"), 10, 64)
	if until := time.Until(time.Unix(exp, 0)); until > PurposeFetchTTL+time.Minute || until < PurposeFetchTTL-time.Minute {
		t.Fatalf("lives %v, want about %v", until, PurposeFetchTTL)
	}

	f, name, err := s.OpenOffered(ctx, q.Get("o"), exp, q.Get("p"), q.Get("sig"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "DOCX" || name != "in.docx" {
		t.Fatalf("served %q as %q", b, name)
	}

	for _, c := range []struct {
		token, purpose, sig string
		exp                 int64
		want                error
	}{
		{q.Get("o"), PurposeConvert, q.Get("sig") + "x", exp, ErrBadSignature},
		{q.Get("o") + "x", PurposeConvert, q.Get("sig"), exp, ErrBadSignature},
		{q.Get("o"), PurposeThumb, q.Get("sig"), exp, ErrUnknownPurpose},
		{q.Get("o"), PurposeConvert, q.Get("sig"), exp + 1, ErrBadSignature},
		{q.Get("o"), PurposeConvert, q.Get("sig"), time.Now().Add(-time.Minute).Unix(), ErrSignatureExpired},
	} {
		if _, _, err := s.OpenOffered(ctx, c.token, c.exp, c.purpose, c.sig); !errors.Is(err, c.want) {
			t.Errorf("%+v: %v, want %v", c, err, c.want)
		}
	}

	withdraw()
	if _, _, err := s.OpenOffered(ctx, q.Get("o"), exp, q.Get("p"), q.Get("sig")); !errors.Is(err, ErrOfferGone) {
		t.Fatalf("withdrawn: %v", err)
	}

	// Another secret in force (the administrator changed it): the old
	// address is refused.
	raw2, withdraw2, _ := s.OfferFile(ctx, p, "in.docx")
	defer withdraw2()
	q2 := offeredQuery(t, raw2)
	exp2, _ := strconv.ParseInt(q2.Get("exp"), 10, 64)
	s.JWTSecret = "another-secret"
	if _, _, err := s.OpenOffered(ctx, q2.Get("o"), exp2, q2.Get("p"), q2.Get("sig")); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("a changed secret: %v", err)
	}
}

func TestOfferFile_NotConfigured(t *testing.T) {
	s := New(nil, nil, "", "", "https://filex.test", 0)
	if _, _, err := s.OfferFile(context.Background(), "x", "x"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("%v", err)
	}
}
