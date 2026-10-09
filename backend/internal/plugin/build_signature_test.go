package plugin_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/plugin"
)

// sec055 S5 and S12: a storage plugin build's signature names the build -
// its plugin, version, platform and sha256 - and is checked as the build the
// install path says it is; a build a store signed passes the build gate; an
// old (sha256-only) signature is still taken during 0.55, said to be old; an
// upgrade that lands held keeps the previous binary until the new one comes
// up.

// signedAs is a trusted key on a manager whose platform is linux/amd64.
func signedAs(pub ed25519.PublicKey) func(*plugin.Options) {
	return func(o *plugin.Options) {
		o.TrustedKeys = []string{hex.EncodeToString(pub)}
		o.Platform = "linux/amd64"
	}
}

// signBuild signs a build's text (plugin.BuildClaim.Payload).
func signBuild(priv ed25519.PrivateKey, name, version, platform, sha string) string {
	return hex.EncodeToString(ed25519.Sign(priv, plugin.BuildClaim{Name: name, Version: version, Platform: platform, SHA256: sha}.Payload()))
}

func TestInstallBuild_TheStoreSignatureIsCheckedAsTheLinksBuild(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	content := []byte("#!/bin/sh\nexit 0\n")
	sum := shaOf(content)
	sig := signBuild(priv, "storefs", "1.3.0", "linux/amd64", sum)
	link := plugin.StoreLink{Store: "https://apps.example", Version: "1.3.0"}

	t.Run("the link's own build is taken, and its version kept beside the signature", func(t *testing.T) {
		m, base := buildServer(t, content, signedAs(pub))
		st, err := m.InstallBuild(context.Background(), "storefs", "", plugin.FeedBinary{URL: base + "/build", SHA256: sum}, link, []string{sig})
		require.NoError(t, err)
		require.False(t, st.LegacySignature, "a signature over the build's text is not the old form")
		b, err := os.ReadFile(filepath.Join(m.Dir(), "storefs", st.Binary+".sig"))
		require.NoError(t, err)
		require.Equal(t, sig+"\nversion 1.3.0\n", string(b))
	})

	t.Run("a link for another version: the signature does not stand for it", func(t *testing.T) {
		m, base := buildServer(t, content, signedAs(pub))
		_, err := m.InstallBuild(context.Background(), "storefs", "", plugin.FeedBinary{URL: base + "/build", SHA256: sum},
			plugin.StoreLink{Store: link.Store, Version: "1.2.0"}, []string{sig})
		var rej plugin.RejectedError
		require.True(t, errors.As(err, &rej), "want a refusal, got %v", err)
		_, statErr := os.Stat(filepath.Join(m.Dir(), "storefs"))
		require.True(t, os.IsNotExist(statErr), "a refused build left files behind")
	})

	t.Run("installed under another name: the signature does not stand for it", func(t *testing.T) {
		m, base := buildServer(t, content, signedAs(pub))
		_, err := m.InstallBuild(context.Background(), "otherfs", "", plugin.FeedBinary{URL: base + "/build", SHA256: sum}, link, []string{sig})
		var rej plugin.RejectedError
		require.True(t, errors.As(err, &rej), "want a refusal, got %v", err)
	})

	t.Run("another platform's server: the signature does not stand for it", func(t *testing.T) {
		m, base := buildServer(t, content, func(o *plugin.Options) {
			o.TrustedKeys = []string{hex.EncodeToString(pub)}
			o.Platform = "linux/arm64"
		})
		_, err := m.InstallBuild(context.Background(), "storefs", "", plugin.FeedBinary{URL: base + "/build", SHA256: sum}, link, []string{sig})
		var rej plugin.RejectedError
		require.True(t, errors.As(err, &rej), "want a refusal, got %v", err)
	})
}

func TestInstall_AnOldFormSignatureIsTakenWithAWarning(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	content := []byte("#!/bin/sh\nexit 0\n")
	sum := shaOf(content)
	old := hex.EncodeToString(ed25519.Sign(priv, []byte(sum)))
	m, base := buildServer(t, content, signedAs(pub))
	ctx := context.Background()

	st, err := m.InstallFromURLVersion(ctx, "oldfs", base+"/build", sum, old, "")
	require.NoError(t, err, "the old form is still taken during 0.55")
	require.True(t, st.LegacySignature, "the plugin's row does not say its signature is the old form")
	lines, _, err := m.Logs(ctx, st.ID, 0)
	require.NoError(t, err)
	found := false
	for _, l := range lines {
		if l.Level == "warn" && strings.Contains(l.Msg, "sha256 alone") {
			found = true
		}
	}
	require.True(t, found, "the plugin's log does not warn about the old form: %+v", lines)

	got, err := m.Get(ctx, st.ID)
	require.NoError(t, err)
	require.True(t, got.LegacySignature, "the warning is gone at the next read")
}

func TestStart_TheStoredSignatureIsCheckedAsTheBuildItNames(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	content := []byte("#!/bin/sh\nexit 0\n")
	sum := shaOf(content)
	m, base := buildServer(t, content, signedAs(pub))
	ctx := context.Background()

	st, err := m.InstallFromURLVersion(ctx, "pinfs", base+"/build", sum, signBuild(priv, "pinfs", "1.3.0", "linux/amd64", sum), "1.3.0")
	require.NoError(t, err)
	require.False(t, st.LegacySignature)

	// The version kept beside the signature is what the start checks the
	// signature as: changed by hand, the signature no longer stands.
	sigFile := filepath.Join(m.Dir(), "pinfs", st.Binary+".sig")
	b, err := os.ReadFile(sigFile)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(sigFile, bytes.Replace(b, []byte("version 1.3.0"), []byte("version 1.2.0"), 1), 0o600))
	_, err = m.Restart(ctx, st.ID)
	require.NoError(t, err)
	got := waitState(t, m, st.ID, plugin.StateRefused)
	require.Contains(t, got.StateError, "stored signature does not verify")
}

func TestBuildGate_IsAskedWithTheStoreThatBroughtTheBuild(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	content := []byte("#!/bin/sh\nexit 0\n")
	sum := shaOf(content)
	sig := signBuild(priv, "gatefs", "1.3.0", "linux/amd64", sum)
	m, base := buildServer(t, content, signedAs(pub))
	ctx := context.Background()
	var asked []string
	m.SetBuildGate(func(_ context.Context, via string, c plugin.BuildClaim, key ed25519.PublicKey, legacy bool) error {
		asked = append(asked, via+"|"+c.Name+"|"+c.Version+"|"+c.Platform)
		if !bytes.Equal(key, pub) || legacy {
			return errors.New("not the key the build was signed with")
		}
		if via != "https://apps.example" {
			return errors.New("signed by a store: install it from the store")
		}
		return nil
	})

	_, err = m.InstallFromURLVersion(ctx, "gatefs", base+"/build", sum, sig, "1.3.0")
	var rej plugin.RejectedError
	require.True(t, errors.As(err, &rej), "a store's build taken by address: %v", err)
	require.Contains(t, err.Error(), "install it from the store")
	require.True(t, errors.Is(err, plugin.ErrStoreBuild), "the gate's refusal is not marked as the gate's: %v", err)
	_, statErr := os.Stat(filepath.Join(m.Dir(), "gatefs"))
	require.True(t, os.IsNotExist(statErr), "a refused build left files behind")

	_, err = m.InstallBuild(ctx, "gatefs", "", plugin.FeedBinary{URL: base + "/build", SHA256: sum},
		plugin.StoreLink{Store: "https://apps.example", Version: "1.3.0"}, []string{sig})
	require.NoError(t, err, "the store's own link")
	require.Contains(t, asked, "https://apps.example|gatefs|1.3.0|linux/amd64")
	require.Contains(t, asked, "|gatefs|1.3.0|linux/amd64")
}

// S12: held is not a success. An upgrade that lands while the license holds
// the plugin keeps the previous binary until the new one first comes up; when
// the license holds and the new one does not come up, the previous one is
// put back.
func TestUpgrade_AHeldUpgradeKeepsThePreviousBinaryUntilItComesUp(t *testing.T) {
	m, _, _ := newManager(t)
	ctx := context.Background()
	oldBin := []byte("#!/bin/sh\nexit 1\n")
	newBin := []byte("#!/bin/sh\nexit 2\n")
	st, err := m.InstallBinary(ctx, "heldup", "heldup", bytes.NewReader(oldBin), "")
	require.NoError(t, err)
	m.SetLicenseHold("heldup", "license: unverified")
	waitState(t, m, st.ID, plugin.StateHeld)

	up, err := m.Upgrade(ctx, st.ID, "heldup", bytes.NewReader(newBin), "")
	require.NoError(t, err)
	require.Equal(t, plugin.StateHeld, up.State)
	require.Equal(t, shaOf(newBin), up.SHA256)
	bin := filepath.Join(m.Dir(), "heldup", up.Binary)
	_, err = os.Stat(bin + ".previous")
	require.NoError(t, err, "the previous binary was dropped while the new one has not run once")

	// The license holds now; the new binary is no plugin and does not come
	// up - the previous one is put back.
	m.SetLicenseHold("heldup", "")
	deadline := time.Now().Add(60 * time.Second)
	for {
		got, err := m.Get(ctx, st.ID)
		require.NoError(t, err)
		if got.SHA256 == shaOf(oldBin) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the previous binary was not put back: sha %s state %s (%s)", got.SHA256, got.State, got.StateError)
		}
		time.Sleep(100 * time.Millisecond)
	}
	b, err := os.ReadFile(bin)
	require.NoError(t, err)
	require.Equal(t, oldBin, b)
	_, err = os.Stat(bin + ".previous")
	require.True(t, os.IsNotExist(err), "the backup is still there after the roll-back")
}
