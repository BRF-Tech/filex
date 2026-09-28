package wasmplugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The host's own parameter names never pass from a caller into a job row.
func TestStripHostParams(t *testing.T) {
	got := StripHostParams(map[string]any{
		"__output": map[string]any{"mode": "folder", "dir": "other://"}, "page_token_hash": "x", "share_id": 7, "op": "sign",
	})
	assert.Equal(t, map[string]any{"op": "sign"}, got)
	assert.NotNil(t, StripHostParams(nil), "never nil: a job row stores an object")
}
