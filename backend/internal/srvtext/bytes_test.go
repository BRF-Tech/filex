package srvtext

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ⚠ A size is written ONE way on both sides of the wire: base 1000 like the
// explorer's formatByteSize, the language's decimal mark, the catalogue's
// units. The e-mail used to say "1.4 MB" (1024s, a dot) for a file the listing
// called "1.5 MB" (QA, 2026-09-21).
func TestBytes_TheInterfacesSizes(t *testing.T) {
	assert.Equal(t, "0 B", Bytes("en", 0))
	assert.Equal(t, "512 B", Bytes("en", 512))
	assert.Equal(t, "1.96 KB", Bytes("en", 1960))
	assert.Equal(t, "1,96 KB", Bytes("tr", 1960))
	assert.Equal(t, "1.5 MB", Bytes("en", 1_500_000))
	assert.Equal(t, "1,5 MB", Bytes("tr", 1_500_000))
	assert.Equal(t, "12.5 GB", Bytes("en", 12_500_000_000))
	assert.Equal(t, "2 KB", Bytes("en", 2000))
	assert.Equal(t, "—", Bytes("en", -1))
}

func TestBytes_AUnitWordComesFromThePack(t *testing.T) {
	withPacks(t, StaticPacks{"fr": {"server.unit.mb": "Mo"}})
	assert.Equal(t, "1,5 Mo", Bytes("fr", 1_500_000))
}
