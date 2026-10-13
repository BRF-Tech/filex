package webdav

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/brf-tech/filex/backend/internal/storage"
)

var _ storage.SpaceReporter = (*Driver)(nil)

// quotaServer answers every PROPFIND with the two quota properties given (""
// leaves one out, the way a server that does not know it does).
func quotaServer(t *testing.T, avail, used string) (*fakeDAV, *atomic.Value) {
	t.Helper()
	asked := &atomic.Value{}
	asked.Store("")
	fs := newFakeDAV(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		b, _ := io.ReadAll(r.Body)
		asked.Store(string(b))
		var props strings.Builder
		if avail != "" {
			props.WriteString("<D:quota-available-bytes>" + avail + "</D:quota-available-bytes>")
		}
		if used != "" {
			props.WriteString("<D:quota-used-bytes>" + used + "</D:quota-used-bytes>")
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="utf-8"?><D:multistatus xmlns:D="DAV:">`+
			`<D:response><D:href>/dav/fx/</D:href><D:propstat><D:prop>`+props.String()+
			`</D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`)
	})
	return fs, asked
}

func TestSpace_ReadsTheRFC4331Quota(t *testing.T) {
	fs, asked := quotaServer(t, "7000", "3000")
	d := davDriver(t, fs.srv.URL, nil)

	sp, err := d.Space(context.Background(), "")
	if err != nil {
		t.Fatalf("Space: %v", err)
	}
	if sp.Total != 10000 || sp.Used != 3000 || sp.Free != 7000 {
		t.Errorf("got %+v, want total 10000 used 3000 free 7000", sp)
	}
	if body := asked.Load().(string); !strings.Contains(body, "quota-available-bytes") {
		t.Errorf("the PROPFIND did not ask for the quota: %s", body)
	}
	if sp.Volume == "" {
		t.Error("no volume: two storages on one account would be added up twice")
	}
}

// Nextcloud answers -3 for an account with no quota. That is "cannot tell",
// never a disk with minus three bytes left.
func TestSpace_ANegativeMarkerCannotTell(t *testing.T) {
	fs, _ := quotaServer(t, "-3", "52428800")
	d := davDriver(t, fs.srv.URL, nil)
	if _, err := d.Space(context.Background(), ""); !errors.Is(err, storage.ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
}

func TestSpace_AServerWithoutQuotaCannotTell(t *testing.T) {
	fs, _ := quotaServer(t, "", "")
	d := davDriver(t, fs.srv.URL, nil)
	if _, err := d.Space(context.Background(), ""); !errors.Is(err, storage.ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
}
