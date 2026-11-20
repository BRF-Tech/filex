package model

import "testing"

// TestShare_Opens is the one rule of which link comes in at which public
// door (GHSA-rcrp-r83c-g8hv): a file request writes and is never read, every
// other link is read and never written through.
func TestShare_Opens(t *testing.T) {
	cases := []struct {
		name         string
		share        *Share
		read, upload bool
	}{
		{"a download link", &Share{Kind: ShareKindDownload}, true, false},
		{"a download link minted before kinds (empty kind)", &Share{}, true, false},
		{"an app's page", &Share{Kind: ShareKindDownload, PluginID: 7, PageID: "sign"}, true, false},
		{"a file request", &Share{Kind: ShareKindDrop}, false, true},
		{"a file request with a PIN", &Share{Kind: ShareKindDrop, PinHash: "x"}, false, true},
		{"no link at all", nil, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.share.Opens(DoorRead); got != c.read {
				t.Errorf("read door: got %v, want %v", got, c.read)
			}
			if got := c.share.Opens(DoorUpload); got != c.upload {
				t.Errorf("upload door: got %v, want %v", got, c.upload)
			}
			if c.share.Opens(LinkDoor(0)) || c.share.Opens(LinkDoor(99)) {
				t.Error("an unknown door opened")
			}
		})
	}
}
