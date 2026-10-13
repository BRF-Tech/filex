package dav

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"strconv"

	"golang.org/x/net/webdav"

	"github.com/brf-tech/filex/backend/internal/space"
)

// RFC 4331's two quota properties: what Windows reads for the free space of a
// mapped WebDAV drive, and what rclone, Cyberduck and davfs2 show.
//
// x/net/webdav knows neither. It does answer a named property from a handle's
// dead properties (webdav.DeadPropsHolder), so a collection handle that holds
// these two answers them, with the figures every other surface gives
// (internal/space).
//
// ⚠ Only for a PROPFIND that NAMES them. A plain listing (allprop, or the
// properties a file manager asks for) never gets the holder, so it never pays
// for a measurement and never carries the quota: RFC 4331 §3 says the two
// SHOULD NOT be returned by allprop, and a depth-1 listing of a large folder
// would otherwise measure once per subfolder.
var (
	quotaAvailable = xml.Name{Space: "DAV:", Local: "quota-available-bytes"}
	quotaUsed      = xml.Name{Space: "DAV:", Local: "quota-used-bytes"}
)

// propfindPeek is how much of a PROPFIND body is read to see what it asks for.
// A propfind naming a dozen properties is well under 1 KiB.
const propfindPeek = 64 << 10

type quotaAskedKey struct{}

// noteQuotaAsked marks r when it is a PROPFIND whose body names a quota
// property. The body is put back in front of the rest for the library.
func noteQuotaAsked(r *http.Request) *http.Request {
	if r.Method != "PROPFIND" || r.Body == nil || r.Body == http.NoBody {
		return r
	}
	head, err := io.ReadAll(io.LimitReader(r.Body, propfindPeek))
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), r.Body), r.Body}
	if err != nil {
		return r
	}
	if !bytes.Contains(head, []byte(quotaAvailable.Local)) && !bytes.Contains(head, []byte(quotaUsed.Local)) {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), quotaAskedKey{}, true))
}

// withQuota hands out d as a quota holder when the request asked for the quota.
func withQuota(ctx context.Context, d *dirFile, figures func() space.Figures) webdav.File {
	if asked, _ := ctx.Value(quotaAskedKey{}).(bool); !asked {
		return d
	}
	return &quotaDir{dirFile: d, figures: figures}
}

// quotaDir is a collection handle that answers the quota properties.
type quotaDir struct {
	*dirFile
	figures func() space.Figures
}

// DeadProps implements webdav.DeadPropsHolder. A location nothing could
// measure answers the stand-in, never zero available: Windows reads that as a
// full drive and refuses to copy.
func (q *quotaDir) DeadProps() (map[xml.Name]webdav.Property, error) {
	f := q.figures().OrStandIn()
	return map[xml.Name]webdav.Property{
		quotaAvailable: {XMLName: quotaAvailable, InnerXML: []byte(strconv.FormatUint(f.Free, 10))},
		quotaUsed:      {XMLName: quotaUsed, InnerXML: []byte(strconv.FormatUint(f.Used, 10))},
	}, nil
}

// Patch refuses every change, which is what x/net/webdav answers for a handle
// with no dead properties: a quota is not something a client sets.
func (q *quotaDir) Patch(patches []webdav.Proppatch) ([]webdav.Propstat, error) {
	refused := webdav.Propstat{Status: http.StatusForbidden}
	for _, p := range patches {
		for _, prop := range p.Props {
			refused.Props = append(refused.Props, webdav.Property{XMLName: prop.XMLName})
		}
	}
	return []webdav.Propstat{refused}, nil
}
