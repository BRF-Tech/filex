package cliclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// What `filex encrypt` needs from the server to encrypt a folder where it is,
// the way the browser does it (docs/E2E-ENCRYPTION.md → "Encrypting a folder
// you already have"). Every route already exists; nothing here encrypts -
// the bytes are encrypted on this machine before they are sent.

type conversionKey struct{}

// ConversionWrite marks every upload made under the returned context as an
// end-to-end CONVERSION write (`e2e_convert=1`): ciphertext replacing the
// plaintext it was made from, inside a folder whose key file says a
// conversion is under way. The server keeps no version of what such a write
// replaces - and only for a write it has checked is one; anything else is an
// ordinary overwrite. The flag rides where the server reads it: the multipart
// form, and the staged commit.
func ConversionWrite(ctx context.Context) context.Context {
	return context.WithValue(ctx, conversionKey{}, true)
}

func isConversionWrite(ctx context.Context) bool {
	v, _ := ctx.Value(conversionKey{}).(bool)
	return v
}

// E2EEscrowKey is the installation's escrow PUBLIC key (base64 SPKI), as
// /api/capabilities publishes it to the browser, or "" when escrow is off.
// A folder made while it is on gets a slot sealed to it, wherever it is made.
func (c *Client) E2EEscrowKey(ctx context.Context) (string, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/api/capabilities", nil, nil)
	if err != nil {
		return "", err
	}
	raw, err := c.doJSON(req)
	if err != nil {
		return "", err
	}
	var caps struct {
		Escrow *struct {
			Enabled   bool   `json:"enabled"`
			PublicKey string `json:"public_key"`
		} `json:"e2e_escrow"`
	}
	if err := json.Unmarshal(raw, &caps); err != nil {
		return "", fmt.Errorf("parse capabilities: %w", err)
	}
	if caps.Escrow == nil || !caps.Escrow.Enabled {
		return "", nil
	}
	if caps.Escrow.PublicKey == "" {
		return "", errors.New("the server says escrow is on but publishes no key")
	}
	return caps.Escrow.PublicKey, nil
}

// E2EAllowed asks the server whether this account may start encrypting at
// remote, as kind: "folder" (encrypt the folder where it is), "new_folder"
// (make a new encrypted folder in it) or "file". answer is "allowed",
// "request" (an administrator's approval is needed first) or "denied";
// reason names the layer that said no (tenant_disabled, policy_off,
// admins_only, permission, approval_required). A server older than the
// encryption policy has no such question (404) and answers "allowed": every
// write it takes is asked on its own anyway.
func (c *Client) E2EAllowed(ctx context.Context, remote, kind string) (answer, reason string, err error) {
	rp, err := ParseRemotePath(remote)
	if err != nil {
		return "", "", err
	}
	raw, err := c.postJSON(ctx, "/api/files/e2e/allowed", map[string]any{
		"items": []map[string]string{{"path": rp.String(), "kind": kind}},
	})
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		return "allowed", "", nil
	}
	if err != nil {
		return "", "", err
	}
	var out struct {
		Encrypt []string `json:"encrypt"`
		Reasons []string `json:"reasons"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", fmt.Errorf("parse the encryption answer: %w", err)
	}
	if len(out.Encrypt) != 1 {
		return "", "", fmt.Errorf("the server answered %d encryption answers for one question", len(out.Encrypt))
	}
	if len(out.Reasons) == 1 {
		reason = out.Reasons[0]
	}
	return out.Encrypt[0], reason, nil
}

// E2ECleanupResult is what POST /api/files/e2e/cleanup removed.
type E2ECleanupResult struct {
	VersionsDeleted   int `json:"versions_deleted"`
	TrashPurged       int `json:"trash_purged"`
	ThumbnailsDropped int `json:"thumbnails_dropped"`
	IndexCleared      int `json:"index_cleared"`
}

// E2ECleanup removes what filex still holds from before a folder was
// encrypted: thumbnails and extracted search text always, and - when asked,
// by the folder's owner or an administrator - older versions and trash
// entries.
func (c *Client) E2ECleanup(ctx context.Context, remote string, versions, trash bool) (*E2ECleanupResult, error) {
	rp, err := ParseRemotePath(remote)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{"path": rp.String(), "versions": versions, "trash": trash})
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/api/files/e2e/cleanup", nil, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	raw, err := c.doJSON(req)
	if err != nil {
		return nil, err
	}
	var out E2ECleanupResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse cleanup: %w", err)
	}
	return &out, nil
}

// ReadSmall downloads a small file whole: a key file, a name sidecar. More
// than max bytes is an error, not a truncation.
func (c *Client) ReadSmall(ctx context.Context, remote string, max int64) ([]byte, error) {
	var buf limitedBuffer
	buf.max = max
	if _, err := c.Download(ctx, remote, &buf); err != nil {
		return nil, err
	}
	return buf.buf.Bytes(), nil
}

// limitedBuffer refuses a write past max.
//
// ⚠ The buffer is a FIELD, not embedded: an embedded bytes.Buffer promotes its
// ReadFrom, io.Copy (inside Download) prefers a destination's ReadFrom to its
// Write, and the limit was never asked - a 2 KiB body went into a 1 KiB
// "small" read whole.
type limitedBuffer struct {
	buf bytes.Buffer
	max int64
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if int64(b.buf.Len()+len(p)) > b.max {
		return 0, fmt.Errorf("more than %d bytes", b.max)
	}
	return b.buf.Write(p)
}

// WriteSmall uploads data as destDir/name (a key file, a name sidecar),
// with the overwrite precondition expect ("" none, "none" must not exist).
func (c *Client) WriteSmall(ctx context.Context, destDir RemotePath, name string, data []byte, expect string) error {
	_, err := c.uploadReader(ctx, destDir, name, bytes.NewReader(data), expect)
	return preconditionErr(err)
}

// Rename gives the item a new name in the same folder.
func (c *Client) Rename(ctx context.Context, item RemotePath, newName string) error {
	_, err := c.rename(ctx, item, newName)
	return err
}
