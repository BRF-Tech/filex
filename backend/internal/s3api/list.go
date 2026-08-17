package s3api

import (
	"context"
	"encoding/xml"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Listing a bucket. This is the operation every client runs first and most
// often, so it is also where the filtering has to be exactly right.
//
// ⚠⚠ The filter must cover DERIVED entries too. With a delimiter, a listing
// returns CommonPrefixes — folder names — and applying the ACL only to keys
// leaks the names of subtrees the caller cannot open. Knowing that
// `clients/acme-merger/` exists is itself information.
//
// S3 explicitly permits returning FEWER entries than MaxKeys, so filtering a
// page is legal. Terminating the listing early is not: a client that sees
// IsTruncated=false stops asking, and everything after the filtered entry is
// silently invisible.

const (
	defaultMaxKeys = 1000
	// maxMaxKeys is S3's own ceiling. A client asking for more gets 1000 and
	// a continuation token, which is exactly what S3 does.
	maxMaxKeys = 1000
)

// ListBucketResult serves both list versions. The difference between them is
// four fields, not four code paths — ⚠ and V1 is NOT optional: clients that
// still use it (older SDKs, embedded devices, some backup tools) see an empty
// bucket rather than an error if only V2 exists.
type ListBucketResult struct {
	XMLName      xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListBucketResult"`
	Name         string   `xml:"Name"`
	Prefix       string   `xml:"Prefix"`
	Delimiter    string   `xml:"Delimiter,omitempty"`
	MaxKeys      int      `xml:"MaxKeys"`
	IsTruncated  bool     `xml:"IsTruncated"`
	EncodingType string   `xml:"EncodingType,omitempty"`

	// V2 only.
	KeyCount              int    `xml:"KeyCount,omitempty"`
	ContinuationToken     string `xml:"ContinuationToken,omitempty"`
	NextContinuationToken string `xml:"NextContinuationToken,omitempty"`
	StartAfter            string `xml:"StartAfter,omitempty"`

	// V1 only.
	Marker     string `xml:"Marker,omitempty"`
	NextMarker string `xml:"NextMarker,omitempty"`

	Contents       []ObjectEntry  `xml:"Contents"`
	CommonPrefixes []CommonPrefix `xml:"CommonPrefixes"`
}

// ObjectEntry is one key in a listing.
type ObjectEntry struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

// CommonPrefix is a folder, as S3 sees one.
type CommonPrefix struct {
	Prefix string `xml:"Prefix"`
}

// listParams is the request, normalized across both versions.
type listParams struct {
	V2        bool
	Prefix    string
	Delimiter string
	MaxKeys   int
	// After is where the page starts, exclusive. V2 folds continuation-token
	// and start-after into it; V1 uses marker. They mean the same thing, so
	// they are one field — keeping three would mean three chances to honour
	// the wrong one.
	After        string
	Token        string
	StartAfter   string
	Marker       string
	EncodingType string
}

func parseListParams(r *http.Request) listParams {
	q := r.URL.Query()
	p := listParams{
		V2:           q.Get("list-type") == "2",
		Prefix:       q.Get("prefix"),
		Delimiter:    q.Get("delimiter"),
		EncodingType: q.Get("encoding-type"),
		MaxKeys:      defaultMaxKeys,
	}
	if v := q.Get("max-keys"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			p.MaxKeys = n
		}
	}
	if p.MaxKeys > maxMaxKeys {
		p.MaxKeys = maxMaxKeys
	}
	if p.V2 {
		p.Token = q.Get("continuation-token")
		p.StartAfter = q.Get("start-after")
		// ⚠ Precedence matters and is not symmetric: when both are present S3
		// uses the continuation token and ignores start-after. Honouring
		// start-after instead would restart a paged listing from the
		// beginning, and a client walking a large bucket would loop forever.
		p.After = p.Token
		if p.After == "" {
			p.After = p.StartAfter
		}
	} else {
		p.Marker = q.Get("marker")
		p.After = p.Marker
	}
	return p
}

// listObjects answers ListObjectsV2 and ListObjects (V1).
func (h *Handler) listObjects(w http.ResponseWriter, r *http.Request, p *protocolauth.Principal, st *model.Storage) {
	ctx := r.Context()
	params := parseListParams(r)

	drv, err := h.cfg.Resolver(st.ID)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "InternalError", "storage unavailable")
		return
	}
	set, err := p.ACL(ctx, st)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	out := ListBucketResult{
		Name:         st.Name,
		Prefix:       params.Prefix,
		Delimiter:    params.Delimiter,
		MaxKeys:      params.MaxKeys,
		EncodingType: params.EncodingType,
		Contents:     []ObjectEntry{},
		// ⚠ Both slices are non-nil so the XML always carries the elements a
		// client expects to iterate.
		CommonPrefixes: []CommonPrefix{},
	}
	if params.V2 {
		out.ContinuationToken = params.Token
		out.StartAfter = params.StartAfter
	} else {
		out.Marker = params.Marker
	}

	lister := &lister{
		h: h, ctx: ctx, drv: drv, storageID: st.ID, set: set, principal: p, params: params,
	}
	if err := lister.run(); err != nil {
		WriteError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	out.Contents = lister.contents
	out.CommonPrefixes = lister.prefixes
	out.IsTruncated = lister.truncated
	out.KeyCount = len(lister.contents) + len(lister.prefixes)
	if lister.truncated {
		if params.V2 {
			out.NextContinuationToken = lister.next
		} else {
			out.NextMarker = lister.next
		}
	}
	WriteXML(w, r, http.StatusOK, out)
}

// lister walks a storage in S3 key order and fills a page.
type lister struct {
	h         *Handler
	ctx       context.Context
	drv       storage.Driver
	storageID int64
	set       *acl.Set
	principal *protocolauth.Principal
	params    listParams

	contents  []ObjectEntry
	prefixes  []CommonPrefix
	seenPfx   map[string]bool
	truncated bool
	next      string
}

func (l *lister) run() error {
	l.seenPfx = map[string]bool{}
	l.contents = []ObjectEntry{}
	l.prefixes = []CommonPrefix{}
	if l.params.MaxKeys == 0 {
		// A legal request that asks for nothing. S3 answers with an empty,
		// truncated-if-there-is-more listing rather than an error.
		l.truncated = true
		return nil
	}
	// Start the walk at the deepest directory the prefix can guarantee, so a
	// prefix like `projects/2026/inv` does not walk the whole bucket.
	start := prefixDir(l.params.Prefix)
	return l.walk(start)
}

// walk lists one directory and recurses, emitting keys in S3 order.
//
// ⚠⚠ The ordering rule is subtle and getting it wrong produces a listing that
// looks sorted and is not. S3 orders by the FULL key, so a directory must sort
// as if it already carried its trailing slash: with entries `c` (a folder) and
// `c-file.txt`, the keys are `c/…` and `c-file.txt`, and '-' (0x2D) sorts
// before '/' (0x2F) — so the FILE comes first. Sorting the raw names would put
// the folder first and produce keys out of order, which makes a client's
// pagination skip entries.
func (l *lister) walk(dir string) error {
	objs, err := l.drv.List(l.ctx, dir)
	if err != nil {
		if err == storage.ErrNotFound {
			return nil // a prefix that matches nothing is an empty listing
		}
		return err
	}

	sort.Slice(objs, func(i, j int) bool {
		return sortKey(dir, objs[i]) < sortKey(dir, objs[j])
	})

	for i := range objs {
		if l.truncated {
			return nil
		}
		o := objs[i]
		rel := joinKey(dir, o.Name)

		if o.Kind == storage.KindDirectory {
			if err := l.emitDir(rel); err != nil {
				return err
			}
			continue
		}
		l.emitKey(rel, o)
	}
	return nil
}

// emitDir either groups a directory into a CommonPrefix or recurses into it.
func (l *lister) emitDir(rel string) error {
	key := rel + "/"
	// A directory outside the requested prefix is only worth entering if the
	// prefix could still be inside it.
	if !strings.HasPrefix(key, l.params.Prefix) && !strings.HasPrefix(l.params.Prefix, key) {
		return nil
	}
	if !l.descendable(rel) {
		// ⚠ Not descendable means not entered AND not named. Returning the
		// prefix while refusing to list it would still tell the caller the
		// folder is there.
		return nil
	}
	if l.params.Delimiter != "" && strings.HasPrefix(key, l.params.Prefix) {
		// With a delimiter this folder is a CommonPrefix — but only if the
		// delimiter actually appears in the part after the prefix, which for
		// the usual "/" is exactly this case.
		if rest := strings.TrimPrefix(key, l.params.Prefix); strings.Contains(rest, l.params.Delimiter) {
			l.addPrefix(l.params.Prefix + rest[:strings.Index(rest, l.params.Delimiter)+len(l.params.Delimiter)])
			return nil
		}
	}
	return l.walk(rel)
}

func (l *lister) emitKey(rel string, o storage.Object) {
	if !strings.HasPrefix(rel, l.params.Prefix) {
		return
	}
	if l.params.After != "" && rel <= l.params.After {
		return
	}
	if !l.visible(rel) {
		return
	}
	// A key whose remainder contains the delimiter belongs to a CommonPrefix,
	// not to Contents. For delimiter="/" the directory walk already handles
	// it; this covers the arbitrary-delimiter case S3 also allows.
	if l.params.Delimiter != "" {
		rest := strings.TrimPrefix(rel, l.params.Prefix)
		if i := strings.Index(rest, l.params.Delimiter); i >= 0 {
			l.addPrefix(l.params.Prefix + rest[:i+len(l.params.Delimiter)])
			return
		}
	}
	if l.full() {
		l.truncate(rel)
		return
	}
	l.contents = append(l.contents, ObjectEntry{
		Key:          rel,
		LastModified: amzTime(o.Mtime),
		ETag:         l.knownETag(rel, o),
		Size:         o.Size,
		StorageClass: "STANDARD",
	})
}

// knownETag returns the digest for a listed key WITHOUT computing one.
//
// ⚠ The backend's own value first, then the digest filex computed when it wrote
// the object — but never a fresh MD5. A listing walks thousands of entries, and
// hashing each would turn `aws s3 ls` on a large bucket into minutes of disk
// reads; GetObject and HeadObject are where the on-demand computation belongs,
// because they touch one object at a time.
//
// ⚠ Empty is the honest answer when neither is available: a client that sees no
// ETag skips verification, while a client that sees a wrong one reports the
// object as corrupt (see etag.go).
func (l *lister) knownETag(rel string, o storage.Object) string {
	if o.Etag != "" {
		return quoteETag(o.Etag)
	}
	if v, ok := l.h.etags.get(etagKey(l.storageID, rel, o.Size, o.Mtime)); ok {
		return quoteETag(v)
	}
	return ""
}

func (l *lister) addPrefix(p string) {
	if l.seenPfx[p] {
		return
	}
	if l.params.After != "" && p <= l.params.After {
		return
	}
	if l.full() {
		l.truncate(p)
		return
	}
	l.seenPfx[p] = true
	l.prefixes = append(l.prefixes, CommonPrefix{Prefix: p})
}

func (l *lister) full() bool {
	return len(l.contents)+len(l.prefixes) >= l.params.MaxKeys
}

// truncate marks the page full. The token is the LAST entry actually
// returned, so the next page resumes after it — using the entry we just
// refused would skip it.
func (l *lister) truncate(_ string) {
	l.truncated = true
	if n := len(l.contents); n > 0 {
		l.next = l.contents[n-1].Key
	}
	if n := len(l.prefixes); n > 0 && l.prefixes[n-1].Prefix > l.next {
		l.next = l.prefixes[n-1].Prefix
	}
}

// visible reports whether a KEY may appear in Contents: strictly inside the
// confinement, and granted.
//
// Both checks, because a confined credential and an ungranted subtree are
// different restrictions — either one alone leaves the other open.
func (l *lister) visible(rel string) bool {
	return l.inConfine(rel) && l.set.CanSee(rel)
}

// descendable reports whether a DIRECTORY may be entered or named.
//
// ⚠ It is deliberately weaker than visible: an ANCESTOR of the confinement
// root must be traversable, or a key confined to `projects/acme` could never
// reach its own subtree — the walk would refuse to enter `projects` and the
// caller would see an empty bucket. This is the same ancestor-traversal rule
// acl.Set.CanSee already implements for grants (acl.go:174-193), and it is not
// a leak: the ancestor's name is part of the caller's own confinement path,
// so it tells them nothing they did not supply.
func (l *lister) descendable(rel string) bool {
	if !l.inConfine(rel) && !l.isConfineAncestor(rel) {
		return false
	}
	return l.set.CanSee(rel)
}

// inConfine reports whether a path is at or under the credential's root.
func (l *lister) inConfine(rel string) bool {
	c := l.principal.Confine
	if c == nil || c.Rel == "" {
		return true
	}
	return rel == c.Rel || strings.HasPrefix(rel, c.Rel+"/")
}

// isConfineAncestor reports whether a path CONTAINS the confinement root.
func (l *lister) isConfineAncestor(rel string) bool {
	c := l.principal.Confine
	if c == nil || c.Rel == "" {
		return false
	}
	return rel == "" || strings.HasPrefix(c.Rel, rel+"/")
}

// prefixDir returns the directory part of a prefix, so the walk can start
// deeper than the bucket root.
func prefixDir(prefix string) string {
	i := strings.LastIndex(prefix, "/")
	if i < 0 {
		return ""
	}
	return prefix[:i]
}

func joinKey(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// sortKey renders an entry the way it will appear as a key, so directories
// sort with their trailing slash. See walk's comment for why this matters.
func sortKey(dir string, o storage.Object) string {
	k := joinKey(dir, o.Name)
	if o.Kind == storage.KindDirectory {
		k += "/"
	}
	return k
}

// quoteETag wraps an ETag in the quotes S3 clients expect. An unquoted ETag is
// compared literally by some clients and never matches.
func quoteETag(e string) string {
	if e == "" {
		return ""
	}
	if strings.HasPrefix(e, `"`) {
		return e
	}
	return `"` + e + `"`
}
