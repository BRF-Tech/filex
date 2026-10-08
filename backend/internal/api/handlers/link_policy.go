package handlers

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/metrics"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/quotastore"
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

// linkCeilingDays is the longest life, in days, a new public link made by the
// request's person may get: the install's ceiling (share.max_ttl_days,
// share.ClampExpiry) or the permission rules binding them
// (ShareLinkMaxDays, applyLinkPolicy above), whichever is shorter. 0 = no
// ceiling at all.
//
// ⚠ Published as `share_link_max_days` (capabilities). The share dialog used
// to read only the install's ceiling, so a person whose rule allowed 7 days
// was offered 30 - and the server cut the link to 7 and said
// `expiry_clamped` after the fact.
func linkCeilingDays(ctx context.Context, store db.Store, resolver *acl.Resolver) int {
	days := 0
	if store != nil {
		days = share.NewService(store).MaxTTLDays(ctx)
	}
	s := linkSettings(ctx, resolver)
	if s.ShareLinkMaxDays != nil && *s.ShareLinkMaxDays > 0 && (days <= 0 || *s.ShareLinkMaxDays < days) {
		days = *s.ShareLinkMaxDays
	}
	return days
}

// uploadLimits keeps one quota.Service per store for the write doors that do
// not carry one (the agent API and everything that writes through it, the
// text editor), so the permission loader behind CheckFileSize keeps its cache
// across requests.
var uploadLimits sync.Map // db.Store → *quota.Service

// checkWriteQuota is the question every single-file write door asks before a
// byte lands (quota.CheckFile, the manager's checkQuota): fileSize against the
// per-file upload limit, and what the write adds against the ceiling of the
// account the bytes are billed to (quotastore.OwnerFrom: an upload ticket's
// minter, otherwise the signed-in user). replacing is the size of the file the
// write replaces, 0 for a new one: an overwrite adds only what it grows by, so
// an account at its ceiling can still save an edit that does not grow it.
//
// Asked by the agent API's write funnel (/api/ai/upload, MCP file_write,
// ShareX, /u/{ticket}: aiOps.WriteStream) and the text editor's save.
func checkWriteQuota(ctx context.Context, store db.Store, fileSize, replacing int64) error {
	if store == nil {
		return nil
	}
	owner := quotastore.OwnerFrom(ctx)
	if owner <= 0 {
		return nil
	}
	q, _ := uploadLimits.LoadOrStore(store, quota.New(store))
	err := q.(*quota.Service).CheckFile(ctx, owner, fileSize, fileSize-replacing)
	if errors.Is(err, quota.ErrQuotaExceeded) {
		metrics.GuardRefusals.WithLabelValues(metrics.GuardQuota).Inc()
	}
	return err
}

// catalogedFileSize is the catalogue's size of the file at rel, 0 when the
// catalogue holds no file there: what a write at rel replaces.
func catalogedFileSize(ctx context.Context, store db.Store, storageID int64, rel string) int64 {
	if store == nil {
		return 0
	}
	clean := protocolsync.NormalizePath(rel)
	n, err := store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, clean))
	if err != nil || n == nil || n.Type != model.NodeTypeFile {
		return 0
	}
	return n.Size
}
