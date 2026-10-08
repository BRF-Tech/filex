package apierr

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/srvtext"
)

// A code is said from the server catalogue in the reader's language, filled
// with its params; a code the catalogue does not know says nothing (never a
// raw key on somebody's screen).
func TestText_TheCataloguesSentenceInTheReadersLanguage(t *testing.T) {
	assert.Equal(t, srvtext.Text("tr", "server.error.read_only", nil), Text("tr", "read_only", nil))
	assert.Equal(t, srvtext.Text("en", "server.error.read_only", nil), Text("en", "read_only", nil))
	assert.NotEqual(t, Text("en", "read_only", nil), Text("tr", "read_only", nil))
	assert.Contains(t, Text("en", "too_many", Params{"max": "200"}), "200")
	assert.Equal(t, Text("en", "read_only", nil), Text("xx-unknown", "read_only", nil), "an unknown language falls back")
	assert.Equal(t, "", Text("en", "no_such_code", nil))
	assert.False(t, Known("no_such_code"))
	assert.True(t, Known("quota_exceeded"))
}

func TestEnvelope_CodeMessageParams(t *testing.T) {
	b := Envelope("tr", "reserved_name", Params{"name": ".filex-trash"})
	assert.Equal(t, "reserved_name", b.Error)
	assert.Contains(t, b.Message, ".filex-trash")
	assert.Equal(t, Params{"name": ".filex-trash"}, b.Params)

	m := Map("en", "read_only", nil, map[string]any{"code": "READ_ONLY", "error": "overwritten", "message": "overwritten"})
	assert.Equal(t, "read_only", m["error"], "the three win over an extra field of the same name")
	assert.Equal(t, Text("en", "read_only", nil), m["message"])
	assert.Equal(t, "READ_ONLY", m["code"])
	_, hasParams := m["params"]
	assert.False(t, hasParams, "no params, no field")
}

// What a queue row keeps: the code, its params and the English detail; a row
// an older server wrote reads back as its plain text.
func TestEncodeDecode_ARowKeepsTheCodeNotTheGoText(t *testing.T) {
	s := Encode("name_taken", Params{"name": "a.txt"}, "something already exists at this path: a.txt")
	code, params, detail := Decode(s)
	assert.Equal(t, "name_taken", code)
	assert.Equal(t, Params{"name": "a.txt"}, params)
	assert.Equal(t, "something already exists at this path: a.txt", detail)

	code, params, detail = Decode("something with that name already exists here")
	assert.Equal(t, "", code)
	assert.Nil(t, params)
	assert.Equal(t, "something with that name already exists here", detail)

	assert.Equal(t, "plain", Encode("", nil, "plain"), "no code, the text as before")

	// A JSON-looking detail that is not ours stays text.
	code, _, detail = Decode(`{"error_code":"Bad Code"}`)
	assert.Equal(t, "", code)
	assert.Equal(t, `{"error_code":"Bad Code"}`, detail)
}

func TestError_TheCodeTravelsThroughWrapping(t *testing.T) {
	base := New("not_in_trash", nil, errors.New("trash entry not found"))
	wrapped := fmt.Errorf("restore 12: %w", base)
	code, _ := CodeOf(wrapped)
	assert.Equal(t, "not_in_trash", code)
	assert.True(t, errors.Is(wrapped, base))
	assert.Equal(t, "trash entry not found", base.Error(), "Error() is the English detail")
	require.Equal(t, `{"error_code":"not_in_trash","detail":"restore 12: trash entry not found"}`, EncodeErr(wrapped))
	code, _ = CodeOf(errors.New("plain"))
	assert.Equal(t, "", code)
}
