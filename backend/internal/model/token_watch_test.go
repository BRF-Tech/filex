package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWatchStorage_OnlyAFilexWatchKeyNamingOneStorage(t *testing.T) {
	key := &APIToken{Source: TokenSourceWatch, Scopes: "watch:12"}
	id, ok := key.WatchStorage()
	assert.True(t, ok)
	assert.EqualValues(t, 12, id)
	assert.True(t, key.DoorOnly())

	for name, tok := range map[string]*APIToken{
		"nil":                         nil,
		"an ordinary token":           {Scopes: "read,watch:12"},
		"a desktop token":             {Source: TokenSourceDesktop, Scopes: "watch:12"},
		"no storage":                  {Source: TokenSourceWatch, Scopes: "watch:"},
		"not a number":                {Source: TokenSourceWatch, Scopes: "watch:12a"},
		"zero":                        {Source: TokenSourceWatch, Scopes: "watch:0"},
		"negative":                    {Source: TokenSourceWatch, Scopes: "watch:-3"},
		"two storages":                {Source: TokenSourceWatch, Scopes: "watch:1,watch:2"},
		"an empty list":               {Source: TokenSourceWatch},
		"a number past int64's reach": {Source: TokenSourceWatch, Scopes: "watch:9999999999999999999"},
	} {
		_, ok := tok.WatchStorage()
		assert.False(t, ok, name)
	}
}

// A filex-watch key grants no verb anywhere: HasScope answers false for every
// one, whatever door asks.
func TestWatchKey_HoldsNoVerb(t *testing.T) {
	key := &APIToken{Source: TokenSourceWatch, Scopes: "watch:12"}
	for _, v := range []string{"read", "write", "delete", "mcp", "admin"} {
		assert.False(t, key.HasScope(v), v)
	}
	assert.False(t, (&APIToken{Scopes: "read"}).DoorOnly())
}
