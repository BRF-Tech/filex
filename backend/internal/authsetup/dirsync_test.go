package authsetup_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/authsetup"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// syncDriver is a directory provider whose sync waits on release.
type syncDriver struct {
	release chan struct{}
	runs    int
}

func (*syncDriver) Name() string                                    { return "ldap" }
func (*syncDriver) Init(context.Context, map[string]any) error      { return nil }
func (*syncDriver) Capabilities() auth.Capabilities                 { return auth.Capabilities{SignIn: true} }
func (*syncDriver) Authenticate(*http.Request) (*model.User, error) { return nil, auth.ErrUnauthorized }
func (*syncDriver) SyncInterval() time.Duration                     { return time.Hour }
func (d *syncDriver) SyncDirectory(context.Context) (*auth.DirectorySyncReport, error) {
	<-d.release
	d.runs++
	return &auth.DirectorySyncReport{StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(), Found: 3, Created: 1}, nil
}

// One run per provider at a time; its report is kept, with who started it.
func TestDirectorySync_OneRunAtATimeAndTheReportIsKept(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	d := &syncDriver{release: make(chan struct{})}
	l := live(t, store, nil, authsetup.NewEnvEntry("ldap", "FILEX_AUTH_DRIVERS", map[string]any{}, d, nil, true))
	ctx := context.Background()

	require.True(t, l.CanSync("ldap"))
	assert.False(t, l.CanSync("local"), "a provider with no directory cannot sync")
	assert.Equal(t, time.Hour, l.SyncInterval("ldap"))
	last, err := l.LastSync(ctx, "ldap")
	require.NoError(t, err)
	assert.Nil(t, last)

	require.NoError(t, l.StartSync(ctx, "ldap", "manual"))
	assert.True(t, l.Syncing("ldap"))
	assert.ErrorIs(t, l.StartSync(ctx, "ldap", "manual"), authsetup.ErrSyncRunning)
	_, err = l.SyncNow(ctx, "ldap", "schedule")
	assert.True(t, errors.Is(err, authsetup.ErrSyncRunning))

	close(d.release)
	require.Eventually(t, func() bool { return !l.Syncing("ldap") }, 5*time.Second, 10*time.Millisecond)
	last, err = l.LastSync(ctx, "ldap")
	require.NoError(t, err)
	require.NotNil(t, last)
	assert.Equal(t, "manual", last.Trigger)
	assert.Equal(t, "ldap", last.Provider)
	assert.Equal(t, 3, last.Found)
	assert.Equal(t, 1, d.runs)

	_, err = l.SyncNow(ctx, "local", "manual")
	assert.ErrorIs(t, err, authsetup.ErrNoDirectorySync)
}
