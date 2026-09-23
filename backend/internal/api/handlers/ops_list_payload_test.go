package handlers_test

// What one row of the queue listing costs.
//
// A pending_ops row keeps every path it was given, and GET /api/files/ops
// hands the newest 200 rows to the explorer when it mounts and, while an op
// runs, every 2 s. A bulk delete queued in batches of a few hundred paths left
// rows of up to 82 KB; 200 of them made an 11.5 MB answer that every browser
// opening the drive downloaded and parsed.
//
// RED PROOF (unfixed code): the 600-path row below came back with all 600
// paths, ~16 KB for one op.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpsList_CarriesAPreviewOfEachRowsSources(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()

	many := make([]string, 600)
	for i := range many {
		many[i] = fmt.Sprintf("arsiv/2026/fatura-%03d.pdf", i)
	}
	big, err := f.Ops.SubmitTo(ctx, "delete", f.StA.ID, 0, many, "")
	require.NoError(t, err)
	small, err := f.Ops.SubmitTo(ctx, "delete", f.StA.ID, 0, []string{"tek.txt"}, "")
	require.NoError(t, err)

	status, raw := mtGet(t, f.A, f.URL+"/api/files/ops")
	require.Equal(t, http.StatusOK, status, raw)
	var list struct {
		Ops []map[string]any `json:"ops"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &list))
	rows := map[int64]map[string]any{}
	for _, r := range list.Ops {
		rows[int64(r["id"].(float64))] = r
	}

	b := rows[big.ID]
	require.NotNil(t, b, raw)
	require.Len(t, b["sources"], 5, "a list row carries a preview of its sources, not all 600")
	require.Equal(t, "arsiv/2026/fatura-000.pdf", b["sources"].([]any)[0])
	require.EqualValues(t, 600, b["source_count"])
	require.Equal(t, true, b["sources_truncated"])
	require.Equal(t, "arsiv/2026", b["source_dir"], "the tray labels a delete with the folder it came from")
	require.EqualValues(t, 600, b["total"], "progress still counts every source")

	s := rows[small.ID]
	require.NotNil(t, s, raw)
	require.Equal(t, []any{"tek.txt"}, s["sources"], "a short list is not cut")
	require.EqualValues(t, 1, s["source_count"])
	require.NotContains(t, s, "sources_truncated")
	require.NotContains(t, s, "source_dir", "a file at the storage root has no folder to name")

	require.Less(t, len(raw), 4096, "two rows must cost a few KB, whatever the ops were given")

	// The per-op endpoint is where the whole list still lives.
	status, raw = mtGet(t, f.A, f.URL+"/api/files/ops/"+strconv.FormatInt(big.ID, 10))
	require.Equal(t, http.StatusOK, status, raw)
	var one struct {
		Sources []string `json:"sources"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &one))
	require.Len(t, one.Sources, 600)
}

// The folder a row names is the deepest one holding EVERY source, so a
// selection spread over two folders is labelled with the folder above both.
func TestOpsList_SourceDirIsTheCommonFolder(t *testing.T) {
	f := newMTFix(t, false)
	ctx := context.Background()

	spread, err := f.Ops.SubmitTo(ctx, "delete", f.StA.ID, 0,
		[]string{"müşteri/2026/eylül/a.docx", "müşteri/2026/ekim/b.docx", "müşteri/2026/ekim"}, "")
	require.NoError(t, err)

	status, raw := mtGet(t, f.A, f.URL+"/api/files/ops")
	require.Equal(t, http.StatusOK, status, raw)
	var list struct {
		Ops []struct {
			ID        int64  `json:"id"`
			SourceDir string `json:"source_dir"`
		} `json:"ops"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &list))
	for _, r := range list.Ops {
		if r.ID == spread.ID {
			require.Equal(t, "müşteri/2026", r.SourceDir)
			return
		}
	}
	t.Fatalf("op %d missing from the listing: %s", spread.ID, raw)
}
