package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func at(h, m int) time.Time {
	return time.Date(2026, 9, 22, h, m, 0, 0, time.Local)
}

func TestParseSyncWindow(t *testing.T) {
	w, err := parseSyncWindow("")
	require.NoError(t, err)
	require.Nil(t, w, "no window means any time")

	w, err = parseSyncWindow("22:00-07:00")
	require.NoError(t, err)
	require.Equal(t, "22:00-07:00", w.String())

	for _, bad := range []string{"22-07", "25:00-07:00", "22:00-07:60", "22:00", "07:00-07:00", "a:b-c:d"} {
		_, err := parseSyncWindow(bad)
		require.Error(t, err, bad)
	}
}

func TestSyncWindowSameDay(t *testing.T) {
	w, _ := parseSyncWindow("09:30-17:00")
	require.False(t, w.contains(at(9, 29)))
	require.True(t, w.contains(at(9, 30)))
	require.True(t, w.contains(at(16, 59)))
	require.False(t, w.contains(at(17, 0)), "the end is exclusive")
	require.Equal(t, at(17, 0), w.closesAfter(at(12, 0)))
	require.Equal(t, at(9, 30), w.opensAfter(at(8, 0)))
	require.Equal(t, at(9, 30).AddDate(0, 0, 1), w.opensAfter(at(18, 0)))
}

func TestSyncWindowOverMidnight(t *testing.T) {
	w, _ := parseSyncWindow("22:00-07:00")
	require.True(t, w.contains(at(23, 0)))
	require.True(t, w.contains(at(0, 0)))
	require.True(t, w.contains(at(6, 59)))
	require.False(t, w.contains(at(7, 0)))
	require.False(t, w.contains(at(12, 0)))
	require.Equal(t, at(7, 0).AddDate(0, 0, 1), w.closesAfter(at(23, 0)), "opened tonight, closes tomorrow morning")
	require.Equal(t, at(7, 0), w.closesAfter(at(3, 0)), "opened last night, closes this morning")
	require.Equal(t, at(22, 0), w.opensAfter(at(12, 0)))
}
