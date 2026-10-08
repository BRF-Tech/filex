package model

import (
	"testing"
	"time"
)

// #210 (B6): where a listed link stands is the server's word. "My shares"
// compared expires_at with the browser's clock, so a link whose downloads were
// used up read as active while every visitor was turned away.
func TestShare_StateAt(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(7*24*time.Hour)
	one, three := 1, 3

	cases := []struct {
		name string
		sh   *Share
		want string
	}{
		{"no expiry, no cap", &Share{}, ShareStateActive},
		{"expiry ahead", &Share{ExpiresAt: &future}, ShareStateActive},
		{"expiry passed", &Share{ExpiresAt: &past}, ShareStateExpired},
		{"downloads used up, date still ahead", &Share{ExpiresAt: &future, MaxDownloads: &three, DownloadCount: 3}, ShareStateExhausted},
		{"downloads left", &Share{ExpiresAt: &future, MaxDownloads: &three, DownloadCount: 2}, ShareStateActive},
		{"an app page's visits used up", &Share{PluginID: 1, PageID: "p", MaxDownloads: &one, VisitCount: 1}, ShareStateExhausted},
		{"a file request's uploads used up", &Share{Kind: ShareKindDrop, MaxUploads: &three, UploadCount: 3}, ShareStateExhausted},
		// Revoking sets the expiry to the same moment: revoked, not expired.
		{"revoked", &Share{ExpiresAt: &past, RevokedAt: &past}, ShareStateRevoked},
		{"revoked beats used up", &Share{RevokedAt: &past, MaxDownloads: &one, DownloadCount: 1}, ShareStateRevoked},
		{"nil", nil, ShareStateExpired},
	}
	for _, c := range cases {
		if got := c.sh.StateAt(now); got != c.want {
			t.Errorf("%s: StateAt = %q, want %q", c.name, got, c.want)
		}
	}
}

// The state agrees with IsExpired wherever IsExpired can see (it cannot see
// RevokedAt, which only the listings fill).
func TestShare_StateAtAgreesWithIsExpired(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	two := 2
	for _, sh := range []*Share{
		{},
		{ExpiresAt: &past},
		{MaxDownloads: &two, DownloadCount: 2},
		{MaxUploads: &two, UploadCount: 1},
	} {
		live := sh.StateAt(now) == ShareStateActive
		if live == sh.IsExpired(now) {
			t.Errorf("%+v: state %q but IsExpired %v", sh, sh.StateAt(now), sh.IsExpired(now))
		}
	}
}
