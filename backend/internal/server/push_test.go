package server

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
)

// Web Push as the configuration asks for it (task #191). ⚠ Red on the code
// before it: pushConfig does not exist.

func TestPushConfig_OnByDefaultAndOffWhenSwitchedOff(t *testing.T) {
	cfg := config.Default()
	cfg.SecretKey = "s3cret-for-the-test"
	cfg.PublicURL = "https://files.example.com/filex"
	pc := pushConfig(cfg)
	require.NotNil(t, pc, "push is on out of the box")
	require.True(t, pc.Box.Enabled())
	require.Equal(t, "https://files.example.com", pc.Subject)
	require.NoError(t, pc.Endpoints.Check("https://fcm.googleapis.com/fcm/send/x"))
	require.Error(t, pc.Endpoints.Check("https://intranet.example/x"))

	// No FILEX_SECRET_KEY: still handed over, and the box seals nothing -
	// push then says why it is off instead of storing a key in the clear.
	cfg.SecretKey = ""
	pc = pushConfig(cfg)
	require.NotNil(t, pc)
	require.False(t, pc.Box.Enabled())

	cfg.Notify.Push.Enabled = false
	require.Nil(t, pushConfig(cfg))
}

func TestPushSubject(t *testing.T) {
	require.Equal(t, "mailto:ops@example.com", pushSubject(" mailto:ops@example.com ", "https://x.example"))
	require.Equal(t, "https://files.example.com", pushSubject("", "https://files.example.com/sub/"))
	require.Equal(t, "mailto:filex@localhost", pushSubject("", "http://localhost:5212"))
	require.Equal(t, "mailto:filex@nas.lan", pushSubject("", "http://nas.lan:8080"))
}

func TestPushPolicy_FILEX_PUSH_HOSTS(t *testing.T) {
	p := pushPolicy("push.example.org, Other.Example ")
	require.NoError(t, p.Check("https://fcm.googleapis.com/x"), "the browsers' own stay")
	require.NoError(t, p.Check("https://eu.push.example.org/x"))
	require.NoError(t, p.Check("https://other.example/x"))
	require.Error(t, p.Check("https://third.example/x"))

	anyHost := pushPolicy("*")
	require.NoError(t, anyHost.Check("https://third.example/x"))
	require.Error(t, anyHost.Check("https://10.0.0.1/x"), "never an address")
}
