package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// settingStorageTLSSeeded records that an install's first start on a version
// with a certificate switch per connection has been: the variable below is
// never read again after it.
const settingStorageTLSSeeded = "storage.tls_skip_verify_seeded"

// seedStorageTLS is what is left of FILEX_STORAGE_TLS_SKIP_VERIFY
// (`storage_tls.skip_verify`), the one switch 0.58.2 had for every storage. The
// switch belongs to each connection now (storage/tlsverify.go), and an install
// that relied on the variable must not lose its storages to the upgrade: on the
// first start, with the variable on, every storage and every replication target
// that has the switch gets it on. From then on the variable is inert. The page
// is where a connection is changed, and a value this wrote there is not written
// again.
//
// It answers whether this was that start: the storage FILEX_DEFAULT_STORAGE_*
// creates on a fresh install comes later in the same start and wants the switch
// too (seedDefaultStorage).
//
// ⚠ All of it in one transaction with the record that it was done. A failure
// changes nothing, the start goes on (a storage that cannot connect says so on
// its own), and the next start does the whole of it again.
//
// ⚠ The first start counts with the variable off as well: set later, on an
// install that decided connection by connection, it would take the check away
// from all of them at once, which is the thing the owner removed. It says so in
// the log and does nothing.
func seedStorageTLS(ctx context.Context, store db.Store, variable bool) bool {
	if done, err := store.GetSetting(ctx, settingStorageTLSSeeded); err == nil && done != "" {
		if variable {
			slog.Warn("storage: FILEX_STORAGE_TLS_SKIP_VERIFY does nothing any more: it was read once, on the first start of a version with a certificate switch per connection. Remove it; the switch is in each storage's and replication target's advanced settings")
		}
		return false
	}
	var storages, targets int
	err := store.WithTx(ctx, func(ctx context.Context) error {
		if variable {
			var err error
			if storages, err = seedTLSRows(storagesForTLS(ctx, store)); err != nil {
				return err
			}
			if targets, err = seedTLSRows(targetsForTLS(ctx, store)); err != nil {
				return err
			}
		}
		return store.UpsertSetting(ctx, settingStorageTLSSeeded, "true")
	})
	if err != nil {
		slog.Error("storage: could not move FILEX_STORAGE_TLS_SKIP_VERIFY to the connections; nothing was changed and the next start tries again",
			slog.String("err", err.Error()))
		return variable
	}
	if variable {
		slog.Warn("storage: FILEX_STORAGE_TLS_SKIP_VERIFY moved to the connections, once: the certificate check is off on these, each with a switch of its own. The variable is not read again; remove it",
			slog.Int("storages", storages), slog.Int("replication_targets", targets))
	}
	return variable
}

// tlsConnection is a row that may hold the switch, a storage or a replication
// target: where its configuration is and how it is saved through the store.
type tlsConnection struct {
	kind, name, driver string
	config             *json.RawMessage
	save               func() error
}

func storagesForTLS(ctx context.Context, store db.Store) ([]tlsConnection, error) {
	rows, err := store.ListStorages(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]tlsConnection, 0, len(rows))
	for _, st := range rows {
		out = append(out, tlsConnection{"storage", st.Name, st.Driver, &st.ConfigJSON, func() error { return store.UpdateStorage(ctx, st) }})
	}
	return out, nil
}

func targetsForTLS(ctx context.Context, store db.Store) ([]tlsConnection, error) {
	rows, err := store.ListReplicationTargets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]tlsConnection, 0, len(rows))
	for _, t := range rows {
		out = append(out, tlsConnection{"replication target", t.Name, t.Driver, &t.ConfigJSON, func() error { return store.UpdateReplicationTarget(ctx, t) }})
	}
	return out, nil
}

// seedTLSRows writes the switch on to the rows that take it, and counts them.
func seedTLSRows(rows []tlsConnection, err error) (int, error) {
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range rows {
		next, changed, err := withSkipTLSVerify(c.driver, *c.config)
		if err != nil {
			return 0, err
		}
		if !changed {
			continue
		}
		*c.config = next
		if err := c.save(); err != nil {
			return 0, err
		}
		slog.Info("storage: certificate check switched off on this connection, as FILEX_STORAGE_TLS_SKIP_VERIFY said",
			slog.String("kind", c.kind), slog.String("name", c.name), slog.String("driver", c.driver))
		n++
	}
	return n, nil
}

// withSkipTLSVerify is a driver configuration with the certificate switch on,
// and whether that changed it. Nothing to do for a driver that has no such
// switch (its descriptor says), for a configuration that already answers either
// way, or where the switch means nothing (FTP without `tls`: Field.Requires).
//
// ⚠ Every other value stays the bytes it was: a configuration holds credentials
// and numbers, and read into `any` a large number would come back a float.
func withSkipTLSVerify(driver string, raw json.RawMessage) (json.RawMessage, bool, error) {
	d, ok := storage.DescriptorFor(driver)
	if !ok {
		return raw, false, nil
	}
	f, ok := d.Field(storage.SkipTLSVerifyKey)
	if !ok {
		return raw, false, nil
	}
	cfg := map[string]json.RawMessage{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, false, err
		}
	}
	if _, set := cfg[storage.SkipTLSVerifyKey]; set {
		return raw, false, nil
	}
	if f.Requires != "" && !bytes.Equal(bytes.TrimSpace(cfg[f.Requires]), []byte("true")) {
		return raw, false, nil
	}
	cfg[storage.SkipTLSVerifyKey] = json.RawMessage("true")
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cfg); err != nil {
		return nil, false, err
	}
	return bytes.TrimRight(out.Bytes(), "\n"), true, nil
}
