package api

import (
	"context"
	"strings"

	"github.com/brf-tech/filex/backend/internal/db"
)

// templateTenantForHost is the tenant an address belongs to, for the
// templates of a page that belongs to no link: a customer tenant's provider
// id, 0 for the platform's own address or an address nobody claims.
func templateTenantForHost(store db.Store) func(context.Context, string) int64 {
	return func(ctx context.Context, host string) int64 {
		host = strings.ToLower(strings.TrimSpace(host))
		if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host, "]") {
			host = host[:i]
		}
		if host == "" || store == nil {
			return 0
		}
		p, err := store.GetProviderByHost(ctx, host)
		if err != nil || p == nil || p.IsSupertenant {
			return 0
		}
		return p.ID
	}
}
