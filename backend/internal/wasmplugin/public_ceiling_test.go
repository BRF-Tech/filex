package wasmplugin

// The life of a link, as the app is TOLD it and as the link GETS it.
//
// The signing app's wizard offered 14-day links and its review screen said
// "links valid 14 days"; share_create then clamped every one of them to the
// instance's share ceiling (share.max_ttl_days, 7 by default) and nobody was
// told. The ceiling now travels in every call input (CallContext and
// ActionRunInput → share_max_ttl_days), read from the same place the clamp
// reads it, so these tests set the ceiling ONCE and assert both halves.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
)

func TestLinkCeiling_IsTheNumberShareCreateClampsTo(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setting string // "" = never written
		want    int
	}{
		{"the default", "", share.DefaultMaxTTLDays},
		{"an administrator's 3 days", "3", 3},
		{"a generous 60", "60", 60},
		{"no ceiling at all", "0", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil)
			p := h.install(t)
			if tc.setting != "" {
				require.NoError(t, h.wide(t).UpsertSetting(context.Background(), share.SettingKeyMaxTTLDays, tc.setting))
			}
			told := h.reg.linkCeiling(context.Background(), p)
			require.NotNil(t, told, "an app that may open links is told how long they live")
			assert.Equal(t, tc.want, *told)

			// …and a link asked for 14 days lives exactly what the app was told
			// (or the full 14 when the ceiling is higher, or absent).
			h.writeCatalogued(t, "docs/c.txt", "terms")
			s := h.jobScope(t, p, h.actor(t), "docs/c.txt")
			before := time.Now()
			out, err := shareCreate(t, s, map[string]any{"page_id": "signer", "subject": "x", "ttl_days": 14})
			require.NoError(t, err)
			sh, err := h.store.GetShareByToken(context.Background(), out["token"].(string))
			require.NoError(t, err)
			require.NotNil(t, sh.ExpiresAt)
			days := 14
			if *told > 0 && *told < days {
				days = *told
			}
			got := sh.ExpiresAt.Sub(before).Hours() / 24
			assert.InDelta(t, float64(days), got, 0.01,
				"told %d, asked 14, the link lives %.2f days", *told, got)
		})
	}
}

// An app that holds no public_pages grant opens no links and is told nothing
// about them — nil, not a zero that would read as "no ceiling".
func TestLinkCeiling_NotToldWithoutTheGrant(t *testing.T) {
	h := newHarness(t, nil)
	p := h.install(t)
	p.Grants = Grants{}
	assert.Nil(t, h.reg.linkCeiling(context.Background(), p))
}

// ── a link a person ended ──────────────────────────────────────────────

// ⭐ WakeSoon brings the wake-up forward and changes nothing else: the app
// is not told what happened, it is woken, and its tick asks after its links.
// The signing request whose link was revoked from Shares stayed open because
// nothing ever woke the app to ask.
func TestWakeSoon_BringsTheWakeUpForward(t *testing.T) {
	h := newHarness(t, nil)
	p, _ := h.installScheduled(t, map[string]string{"tick_mode": "quiet"}, nil)
	// The armed wake-up sits tickBootGrace out; move it to the hour, the
	// way a running instance has it between wake-ups.
	hour := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	require.NoError(t, h.store.PutAppPluginScheduleItem(context.Background(), &model.AppPluginScheduleItem{
		PluginID: p.Row.ID, Key: model.AppPluginScheduleWakeupKey, DueAt: hour, Status: model.AppPluginScheduleDue,
	}))

	before := time.Now().UTC()
	h.reg.WakeSoon(context.Background(), p.Row.ID, "a link was revoked")
	row := h.row(t, p, model.AppPluginScheduleWakeupKey)
	require.NotNil(t, row)
	assert.Equal(t, model.AppPluginScheduleDue, row.Status)
	assert.WithinDuration(t, before.Add(tickNudgeGrace), row.DueAt, 2*time.Second,
		"the wake-up comes in seconds, not at %s", hour)
	assert.Len(t, h.rows(t, p), 1, "no second row: it is the same wake-up, moved")
}

// Nobody who is not woken already is woken by it.
func TestWakeSoon_OnlyARunningAppWithTheGrant(t *testing.T) {
	t.Run("no schedule grant", func(t *testing.T) {
		h := newHarness(t, nil)
		p := h.install(t)
		h.reg.WakeSoon(context.Background(), p.Row.ID, "x")
		assert.Empty(t, h.rows(t, p))
	})
	t.Run("a link no app opened", func(t *testing.T) {
		h := newHarness(t, nil)
		h.reg.WakeSoon(context.Background(), 0, "x") // must not panic or write
	})
	t.Run("a stopped app", func(t *testing.T) {
		h := newHarness(t, nil)
		p, _ := h.installScheduled(t, map[string]string{"tick_mode": "quiet"}, nil)
		before := h.row(t, p, model.AppPluginScheduleWakeupKey)
		require.NotNil(t, before)
		_, err := h.reg.SetEnabled(context.Background(), p.Row.ID, false)
		require.NoError(t, err)
		h.reg.WakeSoon(context.Background(), p.Row.ID, "x")
		after := h.row(t, p, model.AppPluginScheduleWakeupKey)
		require.NotNil(t, after)
		assert.True(t, before.DueAt.Equal(after.DueAt), "a stopped app's wake-up was moved")
	})
}
