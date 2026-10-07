package storage

// A storage's own folder on the replica, and filex's own folders kept off it
// (#186, the maintainers' decision, 2026-10-06).

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplicatedFolder_EveryReplicaPathIsInsideIt(t *testing.T) {
	ctx := context.Background()
	primary := newFakeDriver("p")
	replica := newFakeDriver("r")
	rec := &fakeRecorder{}
	rd := NewReplicated(primary, replica, DefaultRules(), rec, &fakeNotifier{}).WithFolder("arsiv")
	assert.Equal(t, "arsiv", rd.Folder())

	require.NoError(t, rd.Write(ctx, "a.txt", strings.NewReader("one"), 3))
	require.NoError(t, rd.Write(ctx, "/docs/b.txt", strings.NewReader("two"), 3))
	rd.Stop()
	assert.Equal(t, "one", string(replica.files["/arsiv/a.txt"]), "a write landed outside the storage's folder")
	assert.Equal(t, "two", string(replica.files["/arsiv/docs/b.txt"]))
	_, atRoot := replica.files["a.txt"]
	assert.False(t, atRoot, "a write landed in the target's root")
	assert.Contains(t, rec.resolved, "a.txt:write", "the failure rows keep the storage's own path")

	rd2 := NewReplicated(primary, replica, DefaultRules(), rec, &fakeNotifier{}).WithFolder("arsiv")
	require.NoError(t, rd2.Delete(ctx, "a.txt"))
	rd2.Stop()
	_, still := replica.files["/arsiv/a.txt"]
	assert.False(t, still, "a delete missed the storage's folder")

	// The read fallback looks there too, and answers with the caller's path.
	replica.files["/arsiv/c.txt"] = []byte("backup")
	replica.stats["/arsiv/c.txt"] = Object{Path: "/arsiv/c.txt", Size: 6}
	primary.readErr = errors.New("connection refused")
	primary.statErr = errors.New("connection refused")
	rd3 := NewReplicated(primary, replica, DefaultRules(), rec, &fakeNotifier{}).WithFolder("arsiv")
	defer rd3.Stop()
	rc, err := rd3.Read(ctx, "c.txt")
	require.NoError(t, err)
	b, _ := io.ReadAll(rc)
	rc.Close()
	assert.Equal(t, "backup", string(b))
	o, err := rd3.Stat(ctx, "c.txt")
	require.NoError(t, err)
	assert.Equal(t, "c.txt", o.Path, "the fallback answered with the replica's own path")
}

func TestReplicatedInternal_FilexsOwnFoldersAreNeverSent(t *testing.T) {
	ctx := context.Background()
	primary := newFakeDriver("p")
	replica := newFakeDriver("r")
	// A user rule that would mirror everything cannot reach them.
	rules := NewRulesEngine(func() ([]RuleSpec, ReplicaMode) {
		return []RuleSpec{{ID: 1, Pattern: "**", Mode: ModeMirror, Priority: 1, Enabled: true}}, ModeMirror
	})
	rd := NewReplicated(primary, replica, rules, &fakeRecorder{}, &fakeNotifier{})
	for _, p := range []string{".thumbs/1.jpg", "/.filex-open/s-rapor.docx", ".filex-drafts/7/k/a.docx", ".versions/12/1", "docs/.filex-trash/x.txt"} {
		require.NoError(t, rd.Write(ctx, p, strings.NewReader("x"), 1))
		assert.Equal(t, ModeSkip, rd.Mode(p), p)
		assert.Equal(t, ModeSkip, rules.Match(p), "the rule engine itself puts %s out of reach", p)
	}
	rd.Stop()
	assert.Equal(t, int32(0), replica.writeCount.Load(), "filex's own folders reached the replica")
}

func TestReplicatedInternal_TheTrashIsADeleteAndARestoreIsAWrite(t *testing.T) {
	ctx := context.Background()
	primary := newFakeDriver("p")
	replica := newFakeDriver("r")
	primary.files["belge.txt"] = []byte("mine")
	primary.stats["belge.txt"] = Object{Path: "belge.txt", Size: 4, Kind: KindFile}
	replica.files["belge.txt"] = []byte("mine")
	rd := NewReplicated(primary, replica, DefaultRules(), &fakeRecorder{}, &fakeNotifier{})

	require.NoError(t, rd.Move(ctx, "belge.txt", ".filex-trash/9/belge.txt"))
	rd.Stop()
	_, still := replica.files["belge.txt"]
	assert.False(t, still, "a file moved to the trash stayed on the backup")
	_, inTrash := replica.files[".filex-trash/9/belge.txt"]
	assert.False(t, inTrash, "the trash itself reached the backup")

	rd2 := NewReplicated(primary, replica, DefaultRules(), &fakeRecorder{}, &fakeNotifier{})
	require.NoError(t, rd2.Move(ctx, ".filex-trash/9/belge.txt", "belge.txt"))
	rd2.Stop()
	assert.Equal(t, "mine", string(replica.files["belge.txt"]), "a file restored from the trash never came back to the backup")

	// A version restored over a file (a copy out of .versions) reaches it too.
	primary.files[".versions/1/1"] = []byte("old")
	rd3 := NewReplicated(primary, replica, DefaultRules(), &fakeRecorder{}, &fakeNotifier{})
	require.NoError(t, rd3.Copy(ctx, ".versions/1/1", "belge.txt"))
	rd3.Stop()
	assert.Equal(t, "old", string(replica.files["belge.txt"]))

	// append_only keeps a trashed file on the backup.
	keep := NewRulesEngine(func() ([]RuleSpec, ReplicaMode) {
		return []RuleSpec{{ID: 1, Pattern: "arsiv/**", Mode: ModeAppendOnly, Priority: 1, Enabled: true}}, ModeMirror
	})
	primary.files["arsiv/x.txt"] = []byte("x")
	replica.files["arsiv/x.txt"] = []byte("x")
	rd4 := NewReplicated(primary, replica, keep, &fakeRecorder{}, &fakeNotifier{})
	require.NoError(t, rd4.Move(ctx, "arsiv/x.txt", ".filex-trash/2/x.txt"))
	rd4.Stop()
	_, kept := replica.files["arsiv/x.txt"]
	assert.True(t, kept, "append_only lost a trashed file on the backup")
}
