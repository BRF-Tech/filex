package storage

import (
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Off is the default, and off must change nothing: an http.Transport treats a
// nil TLSClientConfig and an empty one differently.
func TestApplyTLSVerify_OffLeavesTheConfigurationAlone(t *testing.T) {
	assert.Nil(t, ApplyTLSVerify(nil, false))
	base := &tls.Config{ServerName: "s3.example.com"}
	assert.Same(t, base, ApplyTLSVerify(base, false))
	assert.False(t, base.InsecureSkipVerify)
}

func TestApplyTLSVerify_OnAcceptsAnyCertificateOnACopy(t *testing.T) {
	base := &tls.Config{ServerName: "s3.example.com", MinVersion: tls.VersionTLS13}

	got := ApplyTLSVerify(base, true)

	require.NotSame(t, base, got)
	assert.True(t, got.InsecureSkipVerify)
	assert.Equal(t, "s3.example.com", got.ServerName, "the server still has to be told its name")
	assert.Equal(t, uint16(tls.VersionTLS13), got.MinVersion)
	assert.False(t, base.InsecureSkipVerify, "base may be shared")
}

func TestApplyTLSVerify_OnWithoutABaseStaysOnModernTLS(t *testing.T) {
	got := ApplyTLSVerify(nil, true)

	require.NotNil(t, got)
	assert.True(t, got.InsecureSkipVerify)
	assert.Equal(t, uint16(tls.VersionTLS12), got.MinVersion)
}

// The switch's own words are the form's: every driver that has it declares the
// same field, and it says what it exposes.
func TestSkipTLSVerifyField_CarriesTheWarning(t *testing.T) {
	f := SkipTLSVerifyField()

	assert.Equal(t, SkipTLSVerifyKey, f.Key)
	assert.Equal(t, FieldBool, f.Type)
	assert.True(t, f.Advanced)
	assert.Equal(t, false, f.Default)
	require.NotNil(t, f.SecurityOff)
	assert.NotEmpty(t, f.SecurityOff.Risk)
	assert.NotEmpty(t, f.SecurityOff.RiskI18nKey)
	assert.False(t, f.SecurityOff.WhenOff, "it has to be read before the switch is turned on")
}
