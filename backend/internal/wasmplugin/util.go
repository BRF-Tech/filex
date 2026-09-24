package wasmplugin

import (
	"crypto/rand"
	"encoding/json"
)

// randReader feeds the guest's WASI random source from crypto/rand.
type randReader struct{}

func (randReader) Read(p []byte) (int, error) { return rand.Read(p) }

func jsonMarshal(v any) ([]byte, error)   { return json.Marshal(v) }
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
