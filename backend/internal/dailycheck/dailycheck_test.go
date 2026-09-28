package dailycheck

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type settings map[string]string

func (s settings) GetSetting(_ context.Context, key string) (string, error) {
	v, ok := s[key]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func TestBeat_WaitsADayAfterTheLastRunAndNeverLessThanTheFirstWait(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	s := settings{}
	b := Beat{Store: s, Key: "k", First: 2 * time.Minute, Interval: 24 * time.Hour, Now: func() time.Time { return now }}
	assert.Equal(t, 2*time.Minute, b.Wait(context.Background()), "never run: the first wait")
	s["k"] = now.Add(-1 * time.Hour).Format(time.RFC3339)
	assert.Equal(t, 23*time.Hour, b.Wait(context.Background()), "a day after the last run")
	s["k"] = now.Add(-30 * time.Hour).Format(time.RFC3339)
	assert.Equal(t, 2*time.Minute, b.Wait(context.Background()), "overdue: not at once, the first wait")
	s["k"] = "garbage"
	assert.True(t, b.Last(context.Background()).IsZero())
}

func TestBeat_RunsTheCheckAndStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runs, fails := 0, 0
	b := Beat{Store: settings{}, Key: "k", First: time.Millisecond, Interval: time.Hour}
	done := make(chan struct{})
	go func() {
		b.Run(ctx, func(context.Context) error {
			runs++
			if runs == 3 {
				cancel()
			}
			return errors.New("offline")
		}, func(error) { fails++ })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the beat did not stop with its context")
	}
	assert.Equal(t, 3, runs)
	assert.Equal(t, 2, fails, "a failure after the context ended is not reported")
}
