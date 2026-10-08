package wasmplugin

// The apps' clock (FILEX_APP_CLOCK, appclock.go): the screenshots start their
// filex with it so that what an app writes into its own words - "Requested on
// Oct 7", "frozen until Oct 14", "Signed 2026-10-07 01:00 UTC" - is dated on
// the scenes' clock (2026-09-15 10:30 UTC), like every other date in the
// picture. ⚠ 0.53.0: three README pictures of the signing app carried the
// real day beside fields dated September 15.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

var sceneNow = time.Date(2026, 9, 15, 10, 30, 0, 0, time.UTC)

func TestAppClockFromEnv(t *testing.T) {
	t.Run("unset is the real clock", func(t *testing.T) {
		t.Setenv(EnvAppClock, "")
		c, err := AppClockFromEnv()
		require.NoError(t, err)
		assert.False(t, c.On())
		assert.WithinDuration(t, time.Now(), c.Now(), time.Second)
	})
	t.Run("an RFC 3339 instant starts the apps there, and the clock runs", func(t *testing.T) {
		// The scenes write it with JavaScript's toISOString, milliseconds and all.
		for _, v := range []string{"2026-09-15T10:30:00Z", "2026-09-15T10:30:00.000Z", "2026-09-15T13:30:00+03:00"} {
			t.Setenv(EnvAppClock, v)
			c, err := AppClockFromEnv()
			require.NoError(t, err, v)
			assert.True(t, c.On(), v)
			assert.WithinDuration(t, sceneNow, c.Now(), 5*time.Second, v)
		}
	})
	t.Run("anything else is an error, and the real clock", func(t *testing.T) {
		t.Setenv(EnvAppClock, "September 15")
		c, err := AppClockFromEnv()
		assert.Error(t, err)
		assert.False(t, c.On())
	})
}

func TestAppClockReply_MovesTheHostsTimesOnly(t *testing.T) {
	realNow := time.Date(2026, 10, 8, 2, 0, 0, 0, time.UTC)
	c := AppClockAt(sceneNow, realNow)
	until := realNow.Add(7 * 24 * time.Hour)
	appState := json.RawMessage(`{"created_at":"2026-09-15T10:31:00Z"}`)

	got := c.reply(map[string]any{
		"ok":         true,
		"until":      &until,
		"not_after":  realNow.AddDate(0, 0, 30),
		"expires_at": (*time.Time)(nil),
		"never":      time.Time{},
		"note":       "2026-10-08T02:00:00Z",
		"state":      appState,
		"page":       map[string]any{"expires_at": until},
		"items":      []map[string]any{{"at": realNow}},
		"list":       []any{realNow, "x"},
	}).(map[string]any)

	assert.Equal(t, sceneNow.Add(7*24*time.Hour), *got["until"].(*time.Time), "a lock's end, as the app will print it")
	assert.Equal(t, sceneNow.AddDate(0, 0, 30), got["not_after"], "a certificate's not-after")
	assert.Nil(t, got["expires_at"].(*time.Time), "no expiry stays no expiry")
	assert.True(t, got["never"].(time.Time).IsZero(), "the zero time means none, wherever it is sent")
	assert.Equal(t, "2026-10-08T02:00:00Z", got["note"], "a string is the host's words, not a time to move")
	assert.Equal(t, appState, got["state"], "the app's own state is on its clock already: moving it again would put it twice as far")
	assert.Equal(t, sceneNow.Add(7*24*time.Hour), got["page"].(map[string]any)["expires_at"])
	assert.Equal(t, sceneNow, got["items"].([]map[string]any)[0]["at"])
	assert.Equal(t, []any{sceneNow, "x"}, got["list"])
	assert.Equal(t, realNow.Add(7*24*time.Hour), until, "the host's own value is not changed in place")

	off := AppClock{}
	in := map[string]any{"until": &until}
	assert.Equal(t, in, off.reply(in), "the real clock moves nothing")
}

func TestAppClockTick_WindowOnTheAppsClockAndBack(t *testing.T) {
	realNow := time.Date(2026, 10, 8, 2, 37, 0, 0, time.UTC)
	c := AppClockAt(sceneNow, realNow)
	in := c.tickInput(wire.TickInput{Now: realNow, WindowStart: realNow, WindowEnd: NextTickBoundary(realNow), MaxItems: 3})
	assert.Equal(t, sceneNow, in.Now)
	assert.Equal(t, sceneNow, in.WindowStart)
	assert.Equal(t, sceneNow.Add(23*time.Minute), in.WindowEnd)
	assert.Equal(t, 3, in.MaxItems)

	items := []wire.ScheduleItem{{Key: "remind:a", DueAt: sceneNow.Add(10 * time.Minute)}, {Key: "none"}}
	c.dueFromApp(items)
	assert.Equal(t, realNow.Add(10*time.Minute), items[0].DueAt, "the schedule keeps the real time")
	assert.True(t, items[1].DueAt.IsZero(), "a missing due time stays missing, so storeItems still refuses it")
}

func TestNew_ReadsTheAppsClockAndHandsItToTheRuntime(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()

	t.Setenv(EnvAppClock, "2026-09-15T10:30:00Z")
	reg, err := New(Options{Store: store, Dir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { reg.Close(ctx) })
	assert.True(t, reg.appClock().On())
	assert.WithinDuration(t, sceneNow, reg.appClock().Now(), 30*time.Second)
	assert.Equal(t, reg.appClock(), reg.rt.clock, "the guests' realtime clock is the same clock")

	t.Setenv(EnvAppClock, "")
	plain, err := New(Options{Store: store, Dir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { plain.Close(ctx) })
	assert.False(t, plain.appClock().On(), "unset, the apps read the real time")
	assert.False(t, plain.rt.clock.On())

	var nilReg *Registry
	assert.False(t, nilReg.appClock().On())
}
