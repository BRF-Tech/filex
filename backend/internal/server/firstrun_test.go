package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// TestGeneratePassword preserves the legacy lightweight check.
func TestGeneratePassword(t *testing.T) {
	pw, err := generatePassword(16)
	require.NoError(t, err)
	require.Len(t, pw, 16)
	assert.False(t, strings.ContainsAny(pw, " \t\n\r"))
}

// TestGeneratePassword_DiversityAcrossSamples — entropy heuristic. Generate
// many passwords and verify the corpus contains at least one digit + one
// letter, which is what we promise on the boot banner.
func TestGeneratePassword_DiversityAcrossSamples(t *testing.T) {
	const N = 50
	letters := false
	digits := false
	for i := 0; i < N; i++ {
		pw, err := generatePassword(16)
		require.NoError(t, err)
		require.Len(t, pw, 16)
		for _, c := range pw {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				letters = true
			}
			if c >= '0' && c <= '9' {
				digits = true
			}
		}
	}
	assert.True(t, letters, "across %d samples should hit a letter", N)
	assert.True(t, digits, "across %d samples should hit a digit", N)
}

// TestRandomHex sanity.
func TestRandomHex(t *testing.T) {
	a, _ := RandomHex(8)
	b, _ := RandomHex(8)
	assert.NotEqual(t, a, b)
	assert.Len(t, a, 16)
}

// TestFirstRun_BootstrapsAdmin — fresh DB → admin user created, file
// written, settings row recorded.
func TestFirstRun_BootstrapsAdmin(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	dataDir := t.TempDir()

	creds, err := FirstRun(context.Background(), store, dataDir, "", "")
	require.NoError(t, err)
	require.NotEmpty(t, creds.AdminEmail)
	require.NotEmpty(t, creds.AdminPassword)
	require.NotEmpty(t, creds.WroteFile)

	// Generated password is 16 chars.
	assert.GreaterOrEqual(t, len(creds.AdminPassword), 16)
	assert.Equal(t, filepath.Join(dataDir, ".first-run.txt"), creds.WroteFile)

	// File exists.
	_, err = os.Stat(creds.WroteFile)
	require.NoError(t, err)

	// Admin user is reachable via the store.
	cnt, err := store.CountUsers(context.Background())
	require.NoError(t, err)
	assert.EqualValues(t, 1, cnt)

	user, err := store.GetUserByEmail(context.Background(), creds.AdminEmail)
	require.NoError(t, err)
	assert.Equal(t, model.RoleAdmin, user.Role)

	// Bcrypt hash should compare clean against the plaintext we got back.
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(creds.AdminPassword)))

	// Settings row recorded.
	v, err := store.GetSetting(context.Background(), "first_run_at")
	require.NoError(t, err)
	assert.NotEmpty(t, v)
}

// TestFirstRun_NoOpWhenUsersPresent — already-bootstrapped DB → no-op.
func TestFirstRun_NoOpWhenUsersPresent(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	dataDir := t.TempDir()

	// Pre-seed an admin so FirstRun finds CountUsers > 0.
	hash, _ := local.HashPassword("seeded")
	_, err := store.CreateUser(context.Background(), "boss@test", hash, model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)

	creds, err := FirstRun(context.Background(), store, dataDir, "", "")
	require.NoError(t, err)
	assert.Empty(t, creds.AdminEmail, "no creds returned when users already exist")
	assert.Empty(t, creds.AdminPassword)
	assert.Empty(t, creds.WroteFile)

	// File MUST NOT have been written.
	_, err = os.Stat(filepath.Join(dataDir, ".first-run.txt"))
	assert.True(t, os.IsNotExist(err), "first-run.txt should not exist on second-run")
}

// TestFirstRun_PresetAdminFromEnv — supplying admin email+password (from
// FILEX_ADMIN_*) uses them verbatim, marks Preset, and does NOT spill the
// creds to a file.
func TestFirstRun_PresetAdminFromEnv(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	dataDir := t.TempDir()

	creds, err := FirstRun(context.Background(), store, dataDir, "boss@example.com", "s3cret-preset-pw")
	require.NoError(t, err)
	assert.Equal(t, "boss@example.com", creds.AdminEmail)
	assert.True(t, creds.Preset)
	assert.Empty(t, creds.AdminPassword, "preset password is not echoed back")
	assert.Empty(t, creds.WroteFile, "preset admin writes no file")

	_, err = os.Stat(filepath.Join(dataDir, ".first-run.txt"))
	assert.True(t, os.IsNotExist(err), "preset admin should not write first-run.txt")

	user, err := store.GetUserByEmail(context.Background(), "boss@example.com")
	require.NoError(t, err)
	assert.Equal(t, model.RoleAdmin, user.Role)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("s3cret-preset-pw")))
}
