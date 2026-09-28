package e2e

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fxePrefix frames a header the way a writer does.
func fxePrefix(header string) []byte {
	b := append([]byte("filexfxe"), 1, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(b[9:13], uint32(len(header)))
	return append(append(b, header...), "body bytes"...)
}

const (
	hOne     = `{"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","rk":{"salt":"cms=","blob":"YmxvYg=="},"dek":"ZGVr","name":"bmFtZQ==","chunk":20,"nonce":"bm9uY2U=","size":10}`
	hNewPw   = `{"salt":"bmV3","iter":600000,"verify":"bmV3dg==","fmk":"wrapped","fmk_pw":"bmV3cA==","rk":{"salt":"cms=","blob":"YmxvYg=="},"dek":"ZGVr","name":"bmFtZQ==","chunk":20,"nonce":"bm9uY2U=","size":10}`
	hReorder = `{"size":10,"nonce":"bm9uY2U=","chunk":20,"name":"bmFtZQ==","dek":"ZGVr","rk":{"blob":"YmxvYg==","salt":"cms="},"fmk_pw":"cHc=","fmk":"wrapped","verify":"dmVy","iter":600000,"salt":"c2FsdA=="}`
	hEscrow  = `{"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","rk":{"salt":"cms=","blob":"YmxvYg=="},"esc":{"kid":"abcd","blob":"ZQ=="},"dek":"ZGVr","name":"bmFtZQ==","chunk":20,"nonce":"bm9uY2U=","size":10}`
	hContent = `{"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","rk":{"salt":"cms=","blob":"YmxvYg=="},"dek":"b3RoZXI=","name":"bmFtZQ==","chunk":20,"nonce":"bmV3bg==","size":99}`
)

func TestReadFileHeader_OnlyAFramedJSONObject(t *testing.T) {
	h, ok := ReadFileHeader(fxePrefix(hOne))
	require.True(t, ok)
	assert.Contains(t, h, "fmk_pw")

	for name, b := range map[string][]byte{
		"folder file":   append([]byte("filexe2e"), make([]byte, 20)...),
		"version 2":     append(append([]byte("filexfxe"), 2), fxePrefix(hOne)[9:]...),
		"cut short":     fxePrefix(hOne)[:40],
		"not an object": fxePrefix(`[1,2]`),
		"zero length":   append([]byte("filexfxe"), 1, 0, 0, 0, 0),
		"plain text":    []byte("hello, this is not encrypted at all"),
	} {
		_, ok := ReadFileHeader(b)
		assert.False(t, ok, name)
	}
}

func TestDiffFileHeaders_SaysWhichSlotChanged(t *testing.T) {
	d := DiffFileHeaders(fxePrefix(hOne), fxePrefix(hNewPw))
	assert.True(t, d.Valid)
	assert.True(t, d.Compared)
	assert.Equal(t, []string{"password"}, d.Changes)
	assert.True(t, d.SecretChanged)

	d = DiffFileHeaders(fxePrefix(hOne), fxePrefix(hReorder))
	assert.Empty(t, d.Changes, "key order and spacing are not a change")
	assert.False(t, d.SecretChanged)

	d = DiffFileHeaders(fxePrefix(hOne), fxePrefix(hEscrow))
	assert.Equal(t, []string{"escrow"}, d.Changes)
	assert.False(t, d.SecretChanged, "adding an escrow slot does not retire a secret")

	d = DiffFileHeaders(fxePrefix(hOne), fxePrefix(hContent))
	assert.Equal(t, []string{"content"}, d.Changes)

	d = DiffFileHeaders([]byte("the old plain file"), fxePrefix(hOne))
	assert.True(t, d.Valid)
	assert.False(t, d.Compared, "nothing readable to compare with")

	d = DiffFileHeaders(fxePrefix(hOne), []byte("not a .fxe any more"))
	assert.False(t, d.Valid)
}

func TestSameFileKey_TheWrappedDekDecides(t *testing.T) {
	assert.True(t, SameFileKey(fxePrefix(hOne), fxePrefix(hNewPw)), "a password change keeps the file key")
	assert.False(t, SameFileKey(fxePrefix(hOne), fxePrefix(hContent)), "new content, new key")
	assert.False(t, SameFileKey(fxePrefix(hOne), []byte("plain")))
}

func TestRetiredSecret_SameKeyUnderAnOldPasswordOnly(t *testing.T) {
	assert.True(t, RetiredSecret(fxePrefix(hOne), fxePrefix(hNewPw)), "the old password over the current key")
	assert.False(t, RetiredSecret(fxePrefix(hOne), fxePrefix(hEscrow)), "the same password: nothing retired")
	assert.False(t, RetiredSecret(fxePrefix(hOne), fxePrefix(hContent)), "another key: history, not a liability")
}
