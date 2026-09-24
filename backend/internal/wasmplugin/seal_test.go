package wasmplugin

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/pathkey"
)

func sealCall(t *testing.T, s *Scope, fn func(context.Context, *Scope, json.RawMessage) (any, error), req map[string]any) (map[string]any, error) {
	t.Helper()
	b, err := json.Marshal(req)
	require.NoError(t, err)
	out, err := fn(context.Background(), s, b)
	if err != nil {
		return nil, err
	}
	m, _ := out.(map[string]any)
	return m, nil
}

// ⚠⚠ The owner, 2026-09-22: a completed request is sealed by filex ITSELF.
// The seal is one key per tenant and app, kept by the host: asked for twice
// it is the same key, it names the installation and the app (never a
// person), it signs like any other key here, and the app can neither
// destroy it nor use it from a screen.
func TestSeal_TheInstallationsOwnKey(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeFile(t, "docs/x.txt", "x")
	s := h.jobScope(t, p, nil, "docs/x.txt")

	first, err := sealCall(t, s, hfCertIssue, map[string]any{"purpose": "platform"})
	require.NoError(t, err)
	again, err := sealCall(t, s, hfCertIssue, map[string]any{"purpose": "platform"})
	require.NoError(t, err)
	assert.Equal(t, first["key_ref"], again["key_ref"], "the same seal on every call")

	cert := parseCert(t, first["cert_pem"].(string))
	ca := parseCert(t, first["chain_pem"].(string))
	assert.Equal(t, "filex document seal", cert.Subject.CommonName)
	assert.Equal(t, []string{"echo"}, cert.Subject.OrganizationalUnit, "the seal says which app sealed")
	assert.Contains(t, cert.UnknownExtKeyUsage[0].String(), "1.3.6.1.5.5.7.3.36", "document-signing EKU, like a signer's")
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	_, err = cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
	require.NoError(t, err, "issued by the tenant's authority")

	sum := sha256.Sum256([]byte("the final bytes"))
	signed, err := sealCall(t, s, hfHostSign, map[string]any{"key_ref": first["key_ref"], "hash": "sha256", "digest_b64": base64.StdEncoding.EncodeToString(sum[:])})
	require.NoError(t, err)
	sig, _ := base64.StdEncoding.DecodeString(signed["signature_b64"].(string))
	assert.True(t, ecdsa.VerifyASN1(cert.PublicKey.(*ecdsa.PublicKey), sum[:], sig))

	_, err = sealCall(t, s, hfKeyDestroy, map[string]any{"key_ref": first["key_ref"]})
	require.Error(t, err, "the app cannot destroy the seal")
	assert.Equal(t, "invalid", asHostError(err).Code)
	_, err = sealCall(t, s, hfHostSign, map[string]any{"key_ref": first["key_ref"], "hash": "sha256", "digest_b64": base64.StdEncoding.EncodeToString(sum[:])})
	require.NoError(t, err, "…and it still signs afterwards")

	// A screen never seals: a visitor must not be able to put the
	// installation's name under anything.
	view, err := newScope(p, h.reg, NewJobID(), h.st.ID, h.drv, nil, "en", false)
	require.NoError(t, err)
	t.Cleanup(view.Close)
	_, err = sealCall(t, view, hfCertIssue, map[string]any{"purpose": "platform"})
	require.Error(t, err)
	_, err = sealCall(t, view, hfHostSign, map[string]any{"key_ref": first["key_ref"], "hash": "sha256", "digest_b64": base64.StdEncoding.EncodeToString(sum[:])})
	require.Error(t, err)
	assert.Equal(t, "permission_denied", asHostError(err).Code)

	// A rotated authority means a new seal from the new authority; the old
	// key is gone, its certificate lives on in what it sealed.
	require.NoError(t, h.reg.RotateCA(context.Background(), 0))
	renewed, err := sealCall(t, s, hfCertIssue, map[string]any{"purpose": "platform"})
	require.NoError(t, err)
	assert.NotEqual(t, first["key_ref"], renewed["key_ref"])
	assert.NotEqual(t, first["chain_pem"], renewed["chain_pem"])
	_, err = sealCall(t, s, hfHostSign, map[string]any{"key_ref": first["key_ref"], "hash": "sha256", "digest_b64": base64.StdEncoding.EncodeToString(sum[:])})
	require.Error(t, err, "the retired seal key signs nothing any more")
}

// A lock can hold until it is lifted, and a lock asked for on the job's OWN
// output is taken when that output is committed — the signing app's "lock
// the signed file when every signature is in", on a file that does not
// exist yet while the job runs.
func TestFileLock_UntilLiftedAndOnTheJobsOwnOutput(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	h.writeFile(t, "docs/in.txt", "x")
	s := h.jobScope(t, p, nil, "docs/in.txt")

	out, err := sealCall(t, s, hfFileLock, map[string]any{"ref": "in:0", "ttl_days": -1, "reason": "signed"})
	require.NoError(t, err)
	assert.Nil(t, out["until"], "no end")
	l, err := h.reg.opts.Store.GetAppPluginLock(context.Background(), h.st.ID, pathkey.Hash(h.st.ID, "/docs/in.txt"))
	require.NoError(t, err)
	require.NotNil(t, l)
	assert.Nil(t, l.Until)
	assert.True(t, l.Live(time.Now().AddDate(50, 0, 0)), "still live in fifty years")

	_, err = sealCall(t, s, hfFileLock, map[string]any{"ref": "in:0", "ttl_days": -2})
	require.Error(t, err)

	// An output: promised now, taken at commit.
	created, err := sealCall(t, s, hfFileCreate, map[string]any{"name": "signed.txt"})
	require.NoError(t, err)
	frame := make([]byte, 8)
	binary.LittleEndian.PutUint64(frame, uint64(created["handle"].(uint64)))
	_, err = hfFileWrite(context.Background(), s, append(frame, []byte("signed")...))
	require.NoError(t, err)
	_, err = sealCall(t, s, hfFileClose, map[string]any{"handle": created["handle"]})
	require.NoError(t, err)
	ref := created["ref"].(string)
	promised, err := sealCall(t, s, hfFileLock, map[string]any{"ref": ref, "ttl_days": -1, "reason": "signed"})
	require.NoError(t, err)
	assert.Equal(t, true, promised["promised"])
	ph := pathkey.Hash(h.st.ID, "/docs/signed.txt")
	before, _ := h.reg.opts.Store.GetAppPluginLock(context.Background(), h.st.ID, ph)
	assert.Nil(t, before, "nothing is locked before the output exists")

	h.reg.keepPromisedLock(context.Background(), s, p, h.st.ID, ref, "docs/signed.txt")
	after, err := h.reg.opts.Store.GetAppPluginLock(context.Background(), h.st.ID, ph)
	require.NoError(t, err)
	require.NotNil(t, after, "taken when the output landed")
	assert.Nil(t, after.Until)
	assert.Equal(t, "docs/signed.txt", after.Rel)
}
