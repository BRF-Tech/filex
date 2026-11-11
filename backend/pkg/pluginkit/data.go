package pluginkit

import (
	"errors"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── The app's own data (filex 0.60.0, permission `data`) ───────────────────
//
// A key-value store that belongs to the app: filex keeps it beside itself
// (a file of the app's own, or a database the operator gave apps), never in
// filex's own tables, and erases it when the app is removed or set up again.
// Keys are ordered, so a prefix lists them in order and an app builds its
// own indexes from key patterns (`u/<user>/<when>/<code>`).
//
// Every key is in a scope (wire.DataTenant, wire.DataApp). The tenant scope
// is the call's tenant, which filex decides: the person's on a call made for
// a person, the address's on a route visit. An app cannot name another
// tenant's scope.
//
// ⚠ A key is printable ASCII (no space, nothing past `~`), so every store
// filex keeps it in orders it byte for byte: encode a person's text (hex,
// base64url) before it goes into a key. A value is any UTF-8.
//
// ⚠ Limits: a key at most wire.DataMaxKeyBytes, a value at most
// wire.DataMaxValueBytes, a listing at most wire.DataMaxList keys, and the
// app's whole store at most what the operator allows (too_large past it). A
// route visit may only read and count (DataGet, DataList, DataIncr).

// DataEntry is one key of a listing.
type DataEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Rev   int64  `json:"rev"`
}

// DataPutOptions makes a write conditional.
type DataPutOptions struct {
	// IfAbsent writes only when the key does not exist: the way to claim a
	// code nobody else has (two calls racing for it: one wins, the other is
	// answered busy).
	IfAbsent bool `json:"if_absent,omitempty"`
	// IfRev writes only when the key is at this revision (from DataGet):
	// compare-and-set. 0 asks nothing.
	IfRev int64 `json:"if_rev,omitempty"`
}

// DataGet reads one key: its value, its revision, and whether it exists.
func DataGet(scope, key string) (string, int64, bool, error) {
	var out struct {
		Value string `json:"value"`
		Rev   int64  `json:"rev"`
		Found bool   `json:"found"`
	}
	if err := callJSON(hostDataGet, map[string]string{"scope": scope, "key": key}, &out); err != nil {
		return "", 0, false, err
	}
	return out.Value, out.Rev, out.Found, nil
}

// DataPut writes one key and answers its new revision. A condition that does
// not hold is wire.ErrBusy (IsBusy).
func DataPut(scope, key, value string, opt DataPutOptions) (int64, error) {
	var out struct {
		Rev int64 `json:"rev"`
	}
	in := map[string]any{"scope": scope, "key": key, "value": value, "if_absent": opt.IfAbsent, "if_rev": opt.IfRev}
	if err := callJSON(hostDataPut, in, &out); err != nil {
		return 0, err
	}
	return out.Rev, nil
}

// DataDelete removes one key (ifRev 0: whatever its revision) and answers
// whether it was there.
func DataDelete(scope, key string, ifRev int64) (bool, error) {
	var out struct {
		Deleted bool `json:"deleted"`
	}
	if err := callJSON(hostDataDelete, map[string]any{"scope": scope, "key": key, "if_rev": ifRev}, &out); err != nil {
		return false, err
	}
	return out.Deleted, nil
}

// DataList lists the keys that start with prefix, in order, after `after`
// (the `next` of the previous page; "" from the start), at most limit
// (1..wire.DataMaxList). next is "" on the last page.
func DataList(scope, prefix, after string, limit int) ([]DataEntry, string, error) {
	var out struct {
		Items []DataEntry `json:"items"`
		Next  string      `json:"next"`
	}
	in := map[string]any{"scope": scope, "prefix": prefix, "after": after, "limit": limit}
	if err := callJSON(hostDataList, in, &out); err != nil {
		return nil, "", err
	}
	return out.Items, out.Next, nil
}

// DataIncr adds by to a counter key (one written only by DataIncr) and
// answers its value. On a route visit filex adds the counts up and writes
// them about once a second, so the value answered there may lag by that
// much.
func DataIncr(scope, key string, by int64) (int64, error) {
	var out struct {
		Value int64 `json:"value"`
	}
	if err := callJSON(hostDataIncr, map[string]any{"scope": scope, "key": key, "by": by}, &out); err != nil {
		return 0, err
	}
	return out.Value, nil
}

// LinkLive reports whether one of filex's public links (a share or a file
// request, by wire.ShortenTarget.Link) is still open: not revoked, not
// expired, not deleted, its creator's account still on, and in the call's
// tenant (permission `link_shortener`). A short address whose link ended
// should answer a miss.
func LinkLive(link string) (bool, error) {
	var out struct {
		Live bool `json:"live"`
	}
	if err := callJSON(hostLinkLive, map[string]string{"link": link}, &out); err != nil {
		return false, err
	}
	return out.Live, nil
}

// IsBusy reports a host refusal with wire.ErrBusy: a DataPut condition that
// did not hold, among others.
func IsBusy(err error) bool {
	var he *HostError
	return errors.As(err, &he) && he.Code == wire.ErrBusy
}
