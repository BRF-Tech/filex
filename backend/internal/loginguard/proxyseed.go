// Package loginguard — proxyseed.go
//
// FILEX_TRUSTED_PROXIES is a SEED since 0.57 (task #258, the owner's
// decision of 2026-10-15), like every other setting a variable gives a first
// value to (package dbsetting):
//
//	a start where `login.trusted_proxies` holds nothing  → the configuration's
//	    list (FILEX_TRUSTED_PROXIES over config.yaml trusted_proxies) is
//	    written into it, as typed
//	from then on                                          → the stored list
//	    wins; the variable is not read again, and a start whose variable says
//	    something else logs it once
//	`filex settings reseed login.trusted_proxies`         → writes the
//	    configuration's list over the stored one, on purpose
//
// Until 0.57 the variable was read at every start, as the fallback under an
// empty setting. An install upgraded with the variable set and no row (the
// usual case: the list was never edited on the page) gets that very list
// written at its first 0.57 start, so nothing it trusts changes - `auto` and
// a list such as "auto, <the proxy's address>" included.
//
// ⚠ The fallback under an empty row stays (Guard.TrustedProxySource): a
// start that could not write the seed, or a list removed on the page during
// this run, still trusts what the configuration says rather than falling to
// `auto` until the next start re-seeds it. "Remove the saved list" therefore
// means what it always meant: back to the configuration's list.
package loginguard

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/internal/dbsetting"
)

// ProxyEnvVar is the variable that seeds the trusted-proxy list.
const ProxyEnvVar = "FILEX_TRUSTED_PROXIES"

// ProxyConfigSource names where the configuration's list came from: the
// variable when it is set, else the config.yaml key.
func ProxyConfigSource() string {
	if strings.TrimSpace(os.Getenv(ProxyEnvVar)) != "" {
		return ProxyEnvVar
	}
	return "config.yaml trusted_proxies"
}

// proxyEntries is a list's entries in one spelling: split on the separators
// the list takes, lowercased, sorted.
func proxyEntries(text string) []string {
	out := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\r' || r == '\t'
	})
	sort.Strings(out)
	return out
}

// SameProxyList reports whether two lists name the same entries, whatever
// their order, case and separators: "auto, 192.0.2.10" and
// "192.0.2.10,AUTO" are one list.
func SameProxyList(a, b string) bool {
	x, y := proxyEntries(a), proxyEntries(b)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// SeedTrustedProxies writes configured (the configuration's list: cfg.
// TrustedProxies) into `login.trusted_proxies` on a start where the row holds
// nothing, and logs, once, a configured list the stored one overrules. Call it
// at start, before Guard.Load, so the memory starts with the seeded list. It
// answers whether the stored list overrules the configured one.
//
// A row the store cannot read is left alone and its error returned: the
// fallback under an empty setting keeps the configuration's list in force.
func SeedTrustedProxies(ctx context.Context, st dbsetting.Store, configured string) (bool, error) {
	configured = strings.TrimSpace(configured)
	if st == nil || configured == "" {
		return false, nil
	}
	cur, err := st.GetSetting(ctx, KeyTrustedProxy)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("loginguard: read %s: %w", KeyTrustedProxy, err)
	}
	if strings.TrimSpace(cur) != "" {
		if SameProxyList(cur, configured) {
			return false, nil
		}
		dbsetting.WarnStoredWins(dbsetting.Drift{Key: KeyTrustedProxy, Source: ProxyConfigSource()})
		return true, nil
	}
	return false, TrustedProxiesSetting.SeedValueFrom(ctx, st, configured, ProxyConfigSource())
}

// ReseedTrustedProxies writes configured over the stored list - `filex
// settings reseed login.trusted_proxies`. A configuration with no list
// leaves the stored one as it is: the list typed on the page (a CDN's
// ranges, a proxy on another machine) is not dropped to `auto` because a
// variable is absent. A running server puts the new list in force at its
// next settings read (within a minute). It answers whether it wrote.
func ReseedTrustedProxies(ctx context.Context, st dbsetting.Store, configured string) (bool, error) {
	configured = strings.TrimSpace(configured)
	if st == nil || configured == "" {
		return false, nil
	}
	v, err := TrustedProxiesSetting.Canonical(configured)
	if err != nil {
		return false, fmt.Errorf("loginguard: %s: %w", ProxyConfigSource(), err)
	}
	if err := st.UpsertSetting(ctx, KeyTrustedProxy, v); err != nil {
		return false, fmt.Errorf("loginguard: reseed %s: %w", KeyTrustedProxy, err)
	}
	return true, nil
}
