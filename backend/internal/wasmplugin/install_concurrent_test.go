package wasmplugin

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// meetingStore holds the first two name lookups of an install until both
// have arrived (or a while has passed): two installs of one name that have
// both found the name free, as two requests at once do.
type meetingStore struct {
	db.Store
	n       atomic.Int32
	arrived sync.WaitGroup
}

func (m *meetingStore) GetAppPluginByName(ctx context.Context, name string) (*model.AppPlugin, error) {
	if m.n.Add(1) <= 2 {
		m.arrived.Done()
		done := make(chan struct{})
		go func() { m.arrived.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
	return m.Store.GetAppPluginByName(ctx, name)
}

// Two installs of one name at once (two administrators, or a store link and
// a repository install): one wins, the other is refused - and the loser's
// clean-up does not remove the winner's files (it used to: both wrote the
// app's directory, the loser's row was refused by the unique name, and its
// os.RemoveAll took the directory the winner's row points at).
func TestInstall_TwoInstallsOfOneNameLeaveTheWinnersFiles(t *testing.T) {
	ms := &meetingStore{}
	ms.arrived.Add(2)
	reg, o := newPackRegistry(t, func(o *Options) {
		ms.Store = o.Store
		o.Store = ms
	})
	packs := [][]byte{
		packManifest(t, "lang-eo", map[string]map[string]string{"eo": {"common.cancel": "Nuligi"}}),
		packManifest(t, "lang-eo", map[string]map[string]string{"eo": {"common.cancel": "Rezigni"}}),
	}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range packs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = reg.Install(context.Background(), &InstallInput{Manifest: packs[i], Source: "upload", Lang: "en"})
		}(i)
	}
	wg.Wait()
	winner := -1
	for i, err := range errs {
		if err == nil {
			assert.Equal(t, -1, winner, "both installs of one name succeeded")
			winner = i
		}
	}
	require.NotEqual(t, -1, winner, "neither install succeeded: %v", errs)
	loser := errs[1-winner]
	var ie *InstallError
	if assert.ErrorAs(t, loser, &ie, "the loser: %v", loser) {
		assert.Equal(t, ErrCodeNameTaken, ie.Code, "the loser: %v", loser)
	}
	p, ok := reg.ByName("lang-eo")
	require.True(t, ok)
	assert.Equal(t, string(packs[winner]), p.Row.ManifestJSON)
	onDisk, err := os.ReadFile(filepath.Join(o.Dir, "lang-eo", "filex-app.json"))
	require.NoError(t, err, "the winner's files are gone (the loser's clean-up removed them)")
	assert.Equal(t, string(packs[winner]), string(onDisk), "the files on disk are not the winner's")
}
