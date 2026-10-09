package appstore

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/internal/plugin"
)

// ── Which keys are a store's build keys (#215, sec055 S5) ───────────────
//
// A store signs every storage plugin build it lists with its artifact key
// (plugin.VerifyBuild: over the plugin's name, version, platform and
// sha256). An administrator who wants only builds a store reviewed to run
// puts that key in FILEX_PLUGIN_TRUSTED_KEYS - and from then on every build
// the store EVER signed verifies there: an old version the store has since
// withdrawn as much as the current one. Through the store's own install link
// that does not matter (the link is fresh and names the current version);
// through an upload, an address or a second store's link it would.
//
// So filex remembers which keys are a trusted store's artifact keys (read
// from the keys.json it reads anyway when it trusts the store, and every
// time it refreshes that trust) and asks, of a build signed by one of them
// that arrives any other way, whether the store still lists that version
// (StorageBuildGate). Remembered for good: untrusting a store does not make
// its signatures a publisher's.
//
// ⚠ Learning these keys from keys.json needs nobody's approval because they
// only ever NARROW what is accepted: FILEX_PLUGIN_TRUSTED_KEYS alone decides
// which signatures verify at all.

// keyBuildKeysPrefix keeps a store's artifact keys (app_store_state
// `buildkeys:<origin>`).
const keyBuildKeysPrefix = "buildkeys:"

// buildKeys is what filex keeps of a store's artifact keys: their material,
// lower hex.
type buildKeys struct {
	Origin string   `json:"origin"`
	Keys   []string `json:"keys"`
}

// noteBuildKeys remembers the artifact keys origin publishes, adding to what
// was kept (a key is never forgotten).
func (s *Service) noteBuildKeys(ctx context.Context, origin string, ks *KeySet) {
	if ks == nil {
		return
	}
	var have buildKeys
	if _, err := s.getJSON(ctx, keyBuildKeysPrefix+origin, &have); err != nil {
		s.log.Warn("app-store: build keys unreadable", slog.String("store", origin), slog.Any("err", err))
	}
	set := map[string]bool{}
	for _, k := range have.Keys {
		set[k] = true
	}
	changed := false
	for _, k := range ks.Keys {
		if k.Use != UseArtifact {
			continue
		}
		pub, err := k.PublicKey()
		if err != nil {
			continue
		}
		h := hex.EncodeToString(pub)
		if !set[h] {
			set[h] = true
			changed = true
		}
	}
	if !changed {
		return
	}
	out := buildKeys{Origin: origin, Keys: make([]string, 0, len(set))}
	for k := range set {
		out.Keys = append(out.Keys, k)
	}
	sort.Strings(out.Keys)
	if err := s.putJSON(ctx, keyBuildKeysPrefix+origin, out); err != nil {
		s.log.Warn("app-store: build keys not kept", slog.String("store", origin), slog.Any("err", err))
	}
}

// BuildKeyStores answers the stores whose artifact key pub is, as filex
// remembers them (sorted; none for a publisher's key). It reads the rows
// directly, so it answers with the app store off too.
func BuildKeyStores(ctx context.Context, st StateStore, pub ed25519.PublicKey) ([]string, error) {
	if st == nil || len(pub) == 0 {
		return nil, nil
	}
	rows, err := st.ListAppStoreState(ctx, keyBuildKeysPrefix)
	if err != nil {
		return nil, err
	}
	want := hex.EncodeToString(pub)
	out := []string{}
	for key, raw := range rows {
		var bk buildKeys
		if json.Unmarshal([]byte(strings.TrimSpace(raw)), &bk) != nil {
			continue
		}
		origin := bk.Origin
		if origin == "" {
			origin = strings.TrimPrefix(key, keyBuildKeysPrefix)
		}
		for _, k := range bk.Keys {
			if strings.EqualFold(k, want) {
				out = append(out, origin)
				break
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// StorageBuildGate is the plugin.BuildGate of this filex (server.go): a
// build signed by a key that is no store's goes on; one a store signed is
// taken through that store's own link, or - any other way (an upload, an
// address, a source, another store's link) - only when the store still lists
// that version of that plugin and the plugin itself is not revoked. A
// store's signature in the old sha256-only form is never taken: a store
// signs the build's name, version and platform. svc nil (the app store off):
// the store cannot be asked, so its signature is refused outside its link.
func StorageBuildGate(st StateStore, svc *Service) plugin.BuildGate {
	return func(ctx context.Context, via string, c plugin.BuildClaim, key ed25519.PublicKey, legacy bool) error {
		owners, err := BuildKeyStores(ctx, st, key)
		if err != nil {
			return fmt.Errorf("could not tell whether the signing key is a store's: %w", err)
		}
		if len(owners) == 0 {
			return nil
		}
		if legacy {
			return fmt.Errorf("%s %s is signed by the store %s over its sha256 alone; a store's signature names the build's name, version and platform - install it from the store", c.Name, c.Version, owners[0])
		}
		for _, o := range owners {
			if o == via {
				return nil
			}
		}
		if svc == nil {
			return fmt.Errorf("%s %s is signed by the store %s, and with the app store off this server cannot ask it whether that version is still listed - install it from the store", c.Name, c.Version, owners[0])
		}
		var refusals []string
		for _, o := range owners {
			err := svc.storageListed(ctx, o, c.Name, c.Version)
			if err == nil {
				return nil
			}
			refusals = append(refusals, err.Error())
		}
		return fmt.Errorf("%s", strings.Join(refusals, "; "))
	}
}

// storageListed answers nil when origin's current catalog lists version of
// the storage plugin name, not yanked, and the plugin not revoked; otherwise
// why not. A catalog that could not be read fresh (the store unreachable,
// the trust gone) refuses: what is not known to be listed is not taken.
func (s *Service) storageListed(ctx context.Context, origin, name, version string) error {
	c, err := s.Catalog(ctx, origin)
	if err != nil {
		return fmt.Errorf("%s %s is signed by the store %s, which could not be asked whether that version is still listed: %v", name, version, origin, err)
	}
	if c.Stale {
		return fmt.Errorf("%s %s is signed by the store %s, which could not be reached to say whether that version is still listed", name, version, origin)
	}
	switch c.storageStanding(name, version) {
	case standingListed:
		return nil
	case standingYanked:
		return fmt.Errorf("the store %s has withdrawn %s %s", origin, name, version)
	case standingRevoked:
		return fmt.Errorf("the store %s has withdrawn %s", origin, name)
	default:
		return fmt.Errorf("the store %s does not list %s %s", origin, name, version)
	}
}

// What a store's catalog says of one version of a storage plugin.
const (
	standingUnknown = iota
	standingListed
	standingYanked
	standingRevoked
)

// storageVersions is a storage plugin's versions in a catalog: revoked, and
// each version (without a leading "v") → yanked.
type storageVersions struct {
	revoked  bool
	versions map[string]bool
}

// storageStanding says what the catalog lists of version of name.
func (c *Catalog) storageStanding(name, version string) int {
	if c == nil {
		return standingUnknown
	}
	sv, ok := c.storage[name]
	if !ok {
		return standingUnknown
	}
	if sv.revoked {
		return standingRevoked
	}
	yanked, ok := sv.versions[normalVersion(version)]
	switch {
	case !ok:
		return standingUnknown
	case yanked:
		return standingYanked
	default:
		return standingListed
	}
}

// normalVersion is a version as a build signature names it: trimmed, without
// a leading "v".
func normalVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}
