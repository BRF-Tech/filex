package server

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/brf-tech/filex/backend/internal/appdata"
	"github.com/brf-tech/filex/backend/internal/config"
)

// openAppData opens where apps keep their own data (internal/appdata,
// docs/CONFIGURATION.md → FILEX_APP_DATA_DSN): a PostgreSQL database of the
// apps' own when the operator gave one, else a file per app under the data
// directory.
//
// ⚠ The file is this node's alone. A filex on PostgreSQL usually runs as more
// than one replica, and each replica would then keep its own short links,
// say: the start says so, and the setup wizard says so beside an app that
// keeps data (SetupData.NodeLocal).
func openAppData(ctx context.Context, cfg *config.Config) (appdata.Store, error) {
	maxBytes := int64(cfg.AppDataMaxMB) << 20
	if dsn := strings.TrimSpace(cfg.AppDataDSN); dsn != "" {
		filexDSN := ""
		if strings.EqualFold(cfg.DB.Driver, "postgres") {
			filexDSN = cfg.DB.DSN
		}
		pg, err := appdata.OpenPostgres(ctx, dsn, filexDSN, maxBytes)
		if err != nil {
			return nil, err
		}
		return pg, nil
	}
	if strings.EqualFold(cfg.DB.Driver, "postgres") || strings.EqualFold(cfg.DB.Driver, "mysql") {
		slog.Warn("app-data: apps keep their data in files on this node (FILEX_APP_DATA_DSN is not set); behind several replicas each keeps its own - give apps a PostgreSQL database of their own")
	}
	lite, err := appdata.OpenSQLite(filepath.Join(cfg.DataDir, "app-plugins", "data"), maxBytes)
	if err != nil {
		return nil, err
	}
	return lite, nil
}
