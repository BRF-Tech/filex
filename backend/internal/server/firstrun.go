// Package server contains the HTTP server lifecycle, the first-run
// detector that bootstraps an admin user, and the boot banner.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// FirstRunCredentials is what FirstRun returns when bootstrapping. Empty
// AdminEmail means no first-run was needed (a user already exists).
type FirstRunCredentials struct {
	AdminEmail    string
	AdminPassword string // plaintext — only ever shown ONCE
	WroteFile     string // path of ~/.filex/.first-run.txt
}

// FirstRun checks whether the users table is empty. If so, it creates an
// admin user with a fresh random password, writes it to a 0600 file at
// {dataDir}/.first-run.txt, and stores the timestamp in the settings table.
//
// Returns zero-value FirstRunCredentials when no bootstrap was performed.
func FirstRun(ctx context.Context, store db.Store, dataDir string) (FirstRunCredentials, error) {
	count, err := store.CountUsers(ctx)
	if err != nil {
		return FirstRunCredentials{}, fmt.Errorf("firstrun: count users: %w", err)
	}
	if count > 0 {
		return FirstRunCredentials{}, nil
	}
	pw, err := generatePassword(16)
	if err != nil {
		return FirstRunCredentials{}, err
	}
	hash, err := local.HashPassword(pw)
	if err != nil {
		return FirstRunCredentials{}, err
	}
	email := "admin@local"
	if _, err := store.CreateUser(ctx, email, hash, model.RoleAdmin, "en", "UTC"); err != nil {
		return FirstRunCredentials{}, fmt.Errorf("firstrun: create user: %w", err)
	}
	_ = store.UpsertSetting(ctx, "first_run_at", time.Now().UTC().Format(time.RFC3339))

	if err := os.MkdirAll(dataDir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return FirstRunCredentials{}, fmt.Errorf("firstrun: mkdir datadir: %w", err)
	}
	path := filepath.Join(dataDir, ".first-run.txt")
	body := fmt.Sprintf("filex first-run credentials\nWritten: %s\nEmail:    %s\nPassword: %s\n\nThis file is shown ONCE — change the password at /admin/profile.\n",
		time.Now().UTC().Format(time.RFC3339), email, pw)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return FirstRunCredentials{}, fmt.Errorf("firstrun: write file: %w", err)
	}
	return FirstRunCredentials{
		AdminEmail:    email,
		AdminPassword: pw,
		WroteFile:     path,
	}, nil
}

// generatePassword returns a cryptographically-strong random ASCII string.
//
// Charset is alphanumeric + a small set of punctuation safe for shell
// pasting. Default length 16 ≈ 96 bits of entropy.
func generatePassword(n int) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ" + "abcdefghjkmnpqrstuvwxyz" +
		"23456789" + "_-+!@%"
	b := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range b {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = alphabet[idx.Int64()]
	}
	return string(b), nil
}

// RandomHex returns 2*n hex characters of cryptographically random data.
func RandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
