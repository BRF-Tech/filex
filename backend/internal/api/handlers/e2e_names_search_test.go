package handlers_test

// wiring:e2 names — a manager search hit inside an encrypted folder carries
// `e2e_root`, so the client can name it (its name may be ciphertext) or say
// it is locked. A hit anywhere else carries nothing new.

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2eSearch_HitsCarryTheirEncryptedRoot(t *testing.T) {
	fx, _ := seedE2eTree(t)
	rec := callList(t, fx.mh, url.Values{"action": {"search"}, "path": {"alpha://"}, "filter": {"txt"}})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var resp e2eListResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	byName := map[string]map[string]any{}
	for _, f := range resp.Files {
		byName[f["basename"].(string)] = f
	}
	require.Contains(t, byName, "gizli.txt", "search should find the file inside the encrypted folder")
	assert.Equal(t, "alpha://kasa", byName["gizli.txt"]["e2e_root"])
	require.Contains(t, byName, "derin.txt")
	assert.Equal(t, "alpha://kasa", byName["derin.txt"]["e2e_root"], "a subfolder of the encrypted folder is inside it")
	for name, f := range byName {
		if name == "gizli.txt" || name == "derin.txt" {
			continue
		}
		_, has := f["e2e_root"]
		assert.False(t, has, "%s is outside every encrypted folder", name)
	}
}
