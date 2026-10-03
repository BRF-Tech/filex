package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// An operating-system provider (windows, pam) is switched on from Admin →
// Identity providers only, where a real sign-in with a test account checks its
// setup first. Named in FILEX_AUTH_DRIVERS (or the config file) it used to be
// one more "unknown auth driver" line, which reads like a typo; now the log
// says where it is switched on instead, the boot goes on, and every other
// provider in the list starts as it would have.
func TestEnvAuthEntries_OperatingSystemProvidersAreRefusedWithTheirOwnSentence(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	_, store := testutil.NewTestDB(t)
	cfg := config.Config{}
	cfg.Auth.Drivers = []string{"windows", "local", " PAM ", "banana"}
	cfg.Auth.DriversFrom = "FILEX_AUTH_DRIVERS"

	entries, err := envAuthEntries(context.Background(), cfg, store, "")
	require.NoError(t, err, "an operating-system provider in the environment does not stop the boot")
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
		require.True(t, e.Live(), "%s starts", e.Name)
	}
	require.Equal(t, []string{"local"}, names, "the other providers are built; windows and pam are not")

	type line struct {
		Level string `json:"level"`
		Msg   string `json:"msg"`
		Name  string `json:"name"`
		From  string `json:"from"`
	}
	var refused []string
	unknown := 0
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		var l line
		require.NoError(t, json.Unmarshal(sc.Bytes(), &l), sc.Text())
		switch l.Msg {
		case osDriverFromEnv:
			require.Equal(t, "WARN", l.Level)
			require.Equal(t, "FILEX_AUTH_DRIVERS", l.From, "the log names where the entry was written")
			refused = append(refused, l.Name)
		case "unknown auth driver":
			unknown++
			require.Equal(t, "banana", l.Name, "only a name nobody knows is called unknown")
		}
	}
	require.Equal(t, []string{"windows", "pam"}, refused)
	require.Equal(t, 1, unknown)
	require.Contains(t, osDriverFromEnv, "Admin → Identity providers")
}
