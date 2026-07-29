package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Manifest is the release index an install polls. It is deliberately OUR
// document rather than a straight read of the git tags, because two of its
// fields cannot be derived from a tag:
//
//   - AutoOK lets a bad release be pulled back from automatic distribution
//     without deleting the tag — a kill switch. Once a version is out, the only
//     other lever is asking every operator to act.
//   - Migrations marks releases that change the schema. Patch releases must not
//     carry migrations (see docs/UPDATES.md); the flag makes that checkable
//     instead of a promise, and the policy engine refuses to auto-apply a patch
//     that sets it.
//
// The document is served as static JSON, so it can live anywhere: a CDN, a
// GitHub release asset, or an air-gapped mirror (FILEX_UPDATE_MANIFEST_URL).
type Manifest struct {
	// Channel this document describes ("stable", "beta"…). Informational.
	Channel string `json:"channel"`
	// Releases, newest first is NOT required — Latest sorts them.
	Releases []Release `json:"releases"`
}

// Release is one published version.
type Release struct {
	Version string `json:"version"` // "v0.7.6"
	Date    string `json:"date"`    // ISO-8601 date, display only
	// AutoOK false = never apply automatically (kill switch), even for a patch.
	// Absent is treated as TRUE by the JSON default, so the field must be
	// written explicitly by the release tooling; MarshalJSON on the producer
	// side always emits it.
	AutoOK bool `json:"auto_ok"`
	// Migrations true = this release changes the database schema.
	Migrations bool `json:"migrations"`
	// MinVersion, when set, is the oldest version that may upgrade DIRECTLY to
	// this one. Older installs must step through it. Empty = no floor.
	MinVersion string `json:"min_version,omitempty"`
	// Notes is a short human summary; NotesURL points at the full changelog.
	Notes    string `json:"notes,omitempty"`
	NotesURL string `json:"notes_url,omitempty"`
	// Severity ("security" | "normal") — a security release is surfaced even
	// when the policy would otherwise stay quiet.
	Severity string `json:"severity,omitempty"`
	// Assets are the downloadable binaries for a self-applying install, each
	// with its SHA-256. The digest lives HERE rather than in a separate
	// checksums file so there is one authenticated document to trust: the
	// manifest arrives over TLS from a host we control, and everything else is
	// verified against it.
	Assets []Asset `json:"assets,omitempty"`
	// Image is the container reference for this release, shown in the upgrade
	// instructions a container install receives.
	Image string `json:"image,omitempty"`
}

// Asset is one downloadable build artifact.
type Asset struct {
	OS     string `json:"os"`     // "linux", "darwin", "windows"
	Arch   string `json:"arch"`   // "amd64", "arm64"
	URL    string `json:"url"`    // absolute https URL of a .tar.gz/.zip
	SHA256 string `json:"sha256"` // lowercase hex digest of the archive
	// Binary is the path of the filex executable INSIDE the archive. Empty
	// defaults to "filex" (goreleaser's layout).
	Binary string `json:"binary,omitempty"`
}

// AssetFor returns the asset matching an os/arch pair.
func (r Release) AssetFor(goos, goarch string) (Asset, bool) {
	for _, a := range r.Assets {
		if strings.EqualFold(a.OS, goos) && strings.EqualFold(a.Arch, goarch) {
			return a, true
		}
	}
	return Asset{}, false
}

// IsSecurity reports whether this release is flagged as a security fix.
func (r Release) IsSecurity() bool { return strings.EqualFold(r.Severity, "security") }

// DefaultManifestURL is where filex looks unless FILEX_UPDATE_MANIFEST_URL
// overrides it. Static JSON, no auth, no telemetry sent — the request carries
// only the User-Agent below.
const DefaultManifestURL = "https://filex.sh/updates/stable.json"

// userAgent identifies the querying install so the endpoint's access log can
// show adoption. It carries the version and nothing else: no host, no license,
// no instance id. Deliberate — an update check must not become a beacon.
func userAgent(current string) string { return "filex-updater/" + current }

// Fetch downloads and parses the manifest. Failures are ordinary: an install
// with no outbound network must not log errors every hour, so callers treat a
// fetch error as "unknown", not as a fault.
func Fetch(ctx context.Context, client *http.Client, url, currentVersion string) (*Manifest, error) {
	if url == "" {
		url = DefaultManifestURL
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent(currentVersion))
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update manifest: HTTP %d", resp.StatusCode)
	}
	// Cap the read: a manifest is kilobytes, and an unbounded read from a
	// remote document is an easy memory sink.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("update manifest: %w", err)
	}
	return &m, nil
}

// Latest returns the newest non-pre-release in the manifest, or false when the
// document is empty. Ordering is computed here rather than trusted from the
// document, so a hand-edited manifest cannot mis-order releases.
func (m *Manifest) Latest() (Release, bool) {
	if m == nil || len(m.Releases) == 0 {
		return Release{}, false
	}
	type parsed struct {
		rel Release
		ver Version
	}
	var all []parsed
	for _, r := range m.Releases {
		v, err := ParseVersion(r.Version)
		if err != nil || v.IsPre() {
			continue
		}
		all = append(all, parsed{r, v})
	}
	if len(all) == 0 {
		return Release{}, false
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ver.Compare(all[j].ver) > 0 })
	return all[0].rel, true
}

// Between returns the releases strictly newer than from, up to and including
// to, oldest first. Used to answer "what am I about to skip over?" — the
// upgrade dialog shows it, and the policy engine scans it for migrations.
func (m *Manifest) Between(from, to Version) []Release {
	if m == nil {
		return nil
	}
	type parsed struct {
		rel Release
		ver Version
	}
	var out []parsed
	for _, r := range m.Releases {
		v, err := ParseVersion(r.Version)
		if err != nil || v.IsPre() {
			continue
		}
		if from.Compare(v) < 0 && v.Compare(to) <= 0 {
			out = append(out, parsed{r, v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ver.Compare(out[j].ver) < 0 })
	rels := make([]Release, 0, len(out))
	for _, p := range out {
		rels = append(rels, p.rel)
	}
	return rels
}
