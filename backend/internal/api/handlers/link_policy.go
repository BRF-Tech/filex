package handlers

import (
	"context"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/share"
)

// Permission-rule settings for public links (package perm): the most
// restrictive of every rule that binds the creator.
//
// Both are applied by adjusting the link rather than refusing it, the way the
// install-wide cap already works (share.ClampExpiry): a link asked for with no
// expiry, or past the cap, gets the cap; a link asked for without a password
// gets a generated PIN, returned to the creator exactly like the "password"
// checkbox's. Every client keeps working, and no link leaves the rule.

// linkSettings returns the rule settings binding the request's user; zero
// settings when there is no resolver or no user.
func linkSettings(ctx context.Context, resolver *acl.Resolver) model.PermRuleSettings {
	if resolver == nil {
		return model.PermRuleSettings{}
	}
	res, err := resolver.Perms(ctx, auth.UserFrom(ctx))
	if err != nil || res == nil {
		return model.PermRuleSettings{}
	}
	return res.Settings
}

// applyLinkPolicy narrows opts to s and returns the PIN it generated, if any.
func applyLinkPolicy(opts *share.CreateOpts, s model.PermRuleSettings, now time.Time) (generatedPIN string) {
	if s.ShareLinkMaxDays != nil {
		limit := now.Add(time.Duration(*s.ShareLinkMaxDays) * 24 * time.Hour)
		if opts.ExpiresAt == nil || opts.ExpiresAt.After(limit) {
			opts.ExpiresAt = &limit
		}
	}
	if s.ShareLinkPasswordRequired && opts.PIN == "" {
		opts.PIN = randomPIN(8)
		generatedPIN = opts.PIN
	}
	return generatedPIN
}

// uploadLimits keeps one quota.Service per store for the write doors that do
// not carry one (the agent API, the text editor, the chunked upload), so the
// permission loader behind CheckFileSize keeps its cache across requests.
var uploadLimits sync.Map // db.Store → *quota.Service

// checkUploadSize holds size against the per-file upload limit of the
// permission rules binding the request's user (quota.ErrFileTooLarge).
func checkUploadSize(ctx context.Context, store db.Store, size int64) error {
	u := auth.UserFrom(ctx)
	if store == nil || u == nil {
		return nil
	}
	q, _ := uploadLimits.LoadOrStore(store, quota.New(store))
	return q.(*quota.Service).CheckFileSize(ctx, u.ID, size)
}
