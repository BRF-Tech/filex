package e2edecrypt

import (
	"bytes"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/e2e"
)

// Writing a key file (create.go): what CreateFolder makes, the readers of
// this package open by every door the browser's folders open by - and the
// rewrites keep what they do not understand.

const createPW = "a folder password, long enough"

func TestCreateFolder_OpensByPasswordAndByRecoveryKey(t *testing.T) {
	made, err := CreateFolder(createPW, CreateOptions{})
	require.NoError(t, err)
	m, err := ParseMarker(made.Marker)
	require.NoError(t, err)
	require.Equal(t, 2, m.V, "level 1 is a v2 key file, which filex 0.31 and later open")
	require.Equal(t, "wrapped", m.Fmk)
	require.Equal(t, MinIterations, m.Iter)
	require.NotNil(t, m.Rk)
	require.False(t, m.HasNames())

	byPw, err := m.UnlockPassword(createPW)
	require.NoError(t, err)
	require.Equal(t, made.Keys.FMK, byPw.FMK)

	byRk, err := m.UnlockRecoveryKey(made.RecoveryKey)
	require.NoError(t, err)
	require.Equal(t, made.Keys.FMK, byRk.FMK)
	require.Regexp(t, `^([0-9A-HJKMNP-TV-Z]{4}-){7}[0-9A-HJKMNP-TV-Z]{4}$`, made.RecoveryKey)

	_, err = m.UnlockPassword(createPW + "!")
	require.ErrorIs(t, err, ErrWrongPassword)

	// A file written under the new folder's key opens with the unlocked one.
	ct, err := EncryptContent(made.Keys.FMK, []byte("merhaba"), nil)
	require.NoError(t, err)
	got, err := DecryptContent(byRk.FMK, ct)
	require.NoError(t, err)
	require.Equal(t, "merhaba", string(got))
}

func TestCreateFolder_NeverFewerIterationsThanTheBrowser(t *testing.T) {
	made, err := CreateFolder(createPW, CreateOptions{Iterations: 1000})
	require.NoError(t, err)
	m, err := ParseMarker(made.Marker)
	require.NoError(t, err)
	require.Equal(t, MinIterations, m.Iter)
}

func TestCreateFolder_ThePasswordRuleIsTheDialogs(t *testing.T) {
	_, err := CreateFolder("seven77", CreateOptions{})
	require.Error(t, err, "7 characters")
	// The dialog counts UTF-16 code units: four emoji are eight of them.
	require.Empty(t, PasswordProblem("😀😀😀😀"))
	require.Empty(t, PasswordProblem("şifreğüç"))
	require.NotEmpty(t, PasswordProblem("şifreğü"))
}

func TestCreateFolder_Level2HasANameKeyThatOpensAgain(t *testing.T) {
	made, err := CreateFolder(createPW, CreateOptions{EncryptNames: true})
	require.NoError(t, err)
	m, err := ParseMarker(made.Marker)
	require.NoError(t, err)
	require.Equal(t, 3, m.V)
	require.Equal(t, []string{"names"}, m.Req)
	require.True(t, m.HasNames())
	require.False(t, m.Names.Pending)
	require.Equal(t, NamesLongDefault, m.Names.Long)
	require.NotNil(t, made.Keys.Names)

	k, err := m.UnlockPassword(createPW)
	require.NoError(t, err)
	require.NotNil(t, k.Names)
	require.Equal(t, made.Keys.Names.RootID, k.Names.RootID)
	enc, err := made.Keys.Names.EncryptName("Bütçe 2027.xlsx", made.Keys.Names.RootID, nil)
	require.NoError(t, err)
	plain, state := k.Names.DecryptStoredName(enc.Stored, k.Names.RootID, nil)
	require.Equal(t, NameDecrypted, state)
	require.Equal(t, "Bütçe 2027.xlsx", plain)
}

func TestCreateFolder_EscrowSlotOpensWithThePrivateHalf(t *testing.T) {
	pub, priv, err := e2e.GenerateEscrowKeyPair(2048)
	require.NoError(t, err)
	made, err := CreateFolder(createPW, CreateOptions{EscrowPublicKey: pub})
	require.NoError(t, err)

	var raw struct {
		Esc *struct {
			KID  string `json:"kid"`
			Alg  string `json:"alg"`
			Blob string `json:"blob"`
		} `json:"esc"`
	}
	require.NoError(t, json.Unmarshal(made.Marker, &raw))
	require.NotNil(t, raw.Esc)
	der, err := base64.StdEncoding.DecodeString(pub)
	require.NoError(t, err)
	require.Equal(t, e2e.EscrowKeyID(der), raw.Esc.KID)
	require.Equal(t, "RSA-OAEP-256", raw.Esc.Alg)

	pk8, err := base64.StdEncoding.DecodeString(priv)
	require.NoError(t, err)
	anyKey, err := x509.ParsePKCS8PrivateKey(pk8)
	require.NoError(t, err)
	blob, err := base64.StdEncoding.DecodeString(raw.Esc.Blob)
	require.NoError(t, err)
	fmk, err := rsa.DecryptOAEP(sha256.New(), nil, anyKey.(*rsa.PrivateKey), blob, nil)
	require.NoError(t, err)
	require.Equal(t, made.Keys.FMK, fmk)

	// No escrow key given: no slot, and nothing an escrow key could open.
	none, err := CreateFolder(createPW, CreateOptions{})
	require.NoError(t, err)
	require.NotContains(t, string(none.Marker), `"esc"`)
}

func TestConversion_StartAndFinishKeepWhatTheyDoNotKnow(t *testing.T) {
	made, err := CreateFolder(createPW, CreateOptions{})
	require.NoError(t, err)
	var f map[string]any
	require.NoError(t, json.Unmarshal(made.Marker, &f))
	f["esc_declined"] = "2026-09-01T00:00:00.000Z"
	f["x_future"] = map[string]any{"kept": true}
	base, err := json.Marshal(f)
	require.NoError(t, err)

	when := time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)
	started, err := StartConversion(base, when, &Cleanup{Versions: true, Trash: false})
	require.NoError(t, err)
	m, err := ParseMarker(started)
	require.NoError(t, err)
	require.Equal(t, 3, m.V)
	require.Equal(t, []string{"conv"}, m.Req)
	require.True(t, m.ConvPending)
	require.Equal(t, &Cleanup{Versions: true, Trash: false}, ConversionCleanup(started))
	require.Contains(t, string(started), `"started":"2026-10-01T09:30:00.000Z"`)
	require.Contains(t, string(started), `"x_future":{"kept":true}`)
	require.Contains(t, string(started), `"esc_declined":"2026-09-01T00:00:00.000Z"`)
	// The password still opens it while it runs.
	_, err = m.UnlockPassword(createPW)
	require.NoError(t, err)

	// Starting twice does not require "conv" twice.
	again, err := StartConversion(started, when, nil)
	require.NoError(t, err)
	m2, err := ParseMarker(again)
	require.NoError(t, err)
	require.Equal(t, []string{"conv"}, m2.Req)

	done, err := FinishConversion(started)
	require.NoError(t, err)
	md, err := ParseMarker(done)
	require.NoError(t, err)
	require.Equal(t, 2, md.V, "back to v2: every filex since 0.31 opens it again")
	require.Empty(t, md.Req)
	require.False(t, md.ConvPending)
	require.NotContains(t, string(done), `"conv"`)
	require.NotContains(t, string(done), `"req"`)
	require.Contains(t, string(done), `"x_future":{"kept":true}`)
}

func TestConversion_AtLevel2KeepsNamesRequired(t *testing.T) {
	made, err := CreateFolder(createPW, CreateOptions{})
	require.NoError(t, err)
	named, nk, err := EnableNames(made.Marker, made.Keys.FMK, nil)
	require.NoError(t, err)
	require.NotNil(t, nk)
	mn, err := ParseMarker(named)
	require.NoError(t, err)
	require.Equal(t, 3, mn.V)
	require.Equal(t, []string{"names"}, mn.Req)
	require.True(t, mn.Names.Pending, "a folder whose entries still have plaintext names")

	started, err := StartConversion(named, time.Now(), nil)
	require.NoError(t, err)
	ms, err := ParseMarker(started)
	require.NoError(t, err)
	require.Equal(t, []string{"names", "conv"}, ms.Req)

	done, err := FinishConversion(started)
	require.NoError(t, err)
	md, err := ParseMarker(done)
	require.NoError(t, err)
	require.Equal(t, 3, md.V)
	require.Equal(t, []string{"names"}, md.Req)

	finished, err := FinishNames(done)
	require.NoError(t, err)
	mf, err := ParseMarker(finished)
	require.NoError(t, err)
	require.False(t, mf.Names.Pending)
	k, err := mf.UnlockPassword(createPW)
	require.NoError(t, err)
	require.Equal(t, nk.RootID, k.Names.RootID)

	_, _, err = EnableNames(finished, made.Keys.FMK, nil)
	require.Error(t, err, "only a v2 folder can switch to encrypted names")
}

func TestRewrites_RefuseAKeyFileTheyCannotRead(t *testing.T) {
	_, err := StartConversion([]byte("not json"), time.Now(), nil)
	require.ErrorIs(t, err, ErrNotMarker)
	_, err = FinishConversion([]byte(`{"v":3,"req":["vault"],"salt":"AAAA","iter":1,"verify":"x","fmk":"wrapped","fmk_pw":"x"}`))
	require.Error(t, err, "an unknown required feature is not rewritten")
}

// The key file Go writes reads back with the browser's field names: what
// the browser's parseMarker needs is there, spelled the same.
func TestCreateFolder_TheJSONTheBrowserReads(t *testing.T) {
	made, err := CreateFolder(createPW, CreateOptions{EncryptNames: true})
	require.NoError(t, err)
	var f map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(made.Marker, &f))
	for _, k := range []string{"v", "salt", "iter", "verify", "fmk", "fmk_pw", "rk", "req", "names"} {
		require.Contains(t, f, k)
	}
	var names map[string]any
	require.NoError(t, json.Unmarshal(f["names"], &names))
	for _, k := range []string{"alg", "enc", "long", "key", "root_id"} {
		require.Contains(t, names, k)
	}
	require.NotContains(t, names, "pending")
	salt, err := base64.StdEncoding.DecodeString(jsonString(t, f["salt"]))
	require.NoError(t, err)
	require.Len(t, salt, 16)
	require.False(t, bytes.Contains(made.Marker, []byte("\n")))
}

func jsonString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	require.NoError(t, json.Unmarshal(raw, &s))
	return s
}
