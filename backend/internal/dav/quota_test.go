package dav

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/quota"
)

// ⚠⚠ /dav enforced NO quota at all until 2026-08-16, while every other write
// surface did — manager, AI, ShareX, S3, SFTP, FTPS, NFS. A user at their limit
// could keep writing indefinitely by mapping a drive, and because the bytes are
// counted AFTER the write, the number in the admin panel simply climbed past
// the ceiling. The gap was invisible: every /dav test passed, because none of
// them had a quota set.
func TestQuotaIsEnforcedBeforeTheBytesLand(t *testing.T) {
	ha := newHarness(t)
	q := quota.New(ha.store)
	ha.h.cfg.Quota = q

	st := ha.addStorage(t, "main", false, false)
	admin, err := ha.store.GetUserByEmail(context.Background(), ha.adminEmail)
	require.NoError(t, err)
	require.NoError(t, q.SetQuota(context.Background(), admin.ID, 64))

	body := strings.Repeat("x", 4096)
	res := ha.req(t, http.MethodPut, Prefix+"/main/too-big.bin", ha.adminEmail, ha.adminPass, body, nil)
	defer res.Body.Close()

	// ⚠ 507 Insufficient Storage (RFC 4331 §5), refused from Content-Length
	// before a single byte of the body was read. x/net/webdav turns a Close
	// error into 405 — which tells a client to stop trying the METHOD, not that
	// it is out of room — so the check has to happen before the library sees it.
	if res.StatusCode != http.StatusInsufficientStorage {
		t.Fatalf("PUT over quota = %d, want 507", res.StatusCode)
	}

	// And the bytes must not be on the driver either.
	drv, err := ha.resolver(st.ID)
	require.NoError(t, err)
	if _, err := drv.Stat(context.Background(), "too-big.bin"); err == nil {
		t.Fatal("the file landed on the driver despite the quota refusal")
	}

	// A write that FITS still works — the gate must not be a blanket refusal.
	small := ha.req(t, http.MethodPut, Prefix+"/main/small.txt", ha.adminEmail, ha.adminPass, "ok", nil)
	defer small.Body.Close()
	if small.StatusCode != http.StatusCreated {
		t.Fatalf("PUT within quota = %d, want 201", small.StatusCode)
	}
}

// The chunked case: a PUT with no Content-Length cannot be pre-checked, so it
// is caught at Close instead. The status is a poor one (405, the library's) but
// a wrong status is not the same kind of mistake as writing past the limit.
func TestQuotaAlsoCatchesAPutWithNoContentLength(t *testing.T) {
	ha := newHarness(t)
	q := quota.New(ha.store)
	ha.h.cfg.Quota = q
	ha.h.cfg.ACL = acl.New(ha.store)

	st := ha.addStorage(t, "main", false, false)
	admin, err := ha.store.GetUserByEmail(context.Background(), ha.adminEmail)
	require.NoError(t, err)
	require.NoError(t, q.SetQuota(context.Background(), admin.ID, 64))

	req, err := http.NewRequest(http.MethodPut, ha.srv.URL+Prefix+"/main/chunked.bin",
		iotest{strings.NewReader(strings.Repeat("y", 4096))})
	require.NoError(t, err)
	req.SetBasicAuth(ha.adminEmail, ha.adminPass)
	// ⚠ -1 is what makes net/http send it chunked, which is the whole point.
	req.ContentLength = -1
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	if res.StatusCode < 400 {
		t.Fatalf("chunked PUT over quota = %d, want a failure", res.StatusCode)
	}
	drv, err := ha.resolver(st.ID)
	require.NoError(t, err)
	if _, err := drv.Stat(context.Background(), "chunked.bin"); err == nil {
		t.Fatal("a chunked write past the quota landed on the driver")
	}
}

// iotest hides the Len() method net/http uses to infer a Content-Length from a
// *strings.Reader, so the request really is sent chunked.
type iotest struct{ r *strings.Reader }

func (t iotest) Read(p []byte) (int, error) { return t.r.Read(p) }
