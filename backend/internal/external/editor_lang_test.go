package external_test

// ONLYOFFICE's editor language lives in the row's options blob beside the
// callback address (GitHub Discussion #93): written without losing the other
// options, removed for "auto", and read live by the resolver like the rest.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/external"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

func TestWithEditorLang_KeepsTheOtherOptions(t *testing.T) {
	out, err := external.WithEditorLang(`{"callback_url":"http://filex:5212","other":1}`, "de")
	require.NoError(t, err)
	assert.Equal(t, "de", external.EditorLangFromOptions(out))
	assert.Equal(t, "http://filex:5212", external.CallbackURLFromOptions(out))
	assert.Contains(t, out, `"other":1`)

	out, err = external.WithEditorLang(out, "auto")
	require.NoError(t, err)
	assert.Empty(t, external.EditorLangFromOptions(out), "automatic needs no key")
	assert.NotContains(t, out, "editor_lang")
	assert.Equal(t, "http://filex:5212", external.CallbackURLFromOptions(out))

	_, err = external.WithEditorLang("not json", "de")
	assert.Error(t, err, "an operator's other options are never silently dropped")
	assert.Empty(t, external.EditorLangFromOptions("not json"))
	assert.Empty(t, external.EditorLangFromOptions(""))
}

func TestGet_CarriesTheEditorLanguage(t *testing.T) {
	ctx := context.Background()
	_, store := dbtest.NewTestDB(t)
	r := external.New(store)
	require.NoError(t, store.UpsertExternalService(ctx, external.OnlyOffice, true, "https://docs.example", "s",
		`{"editor_lang":"tr"}`, time.Time{}, "ok"))
	r.Invalidate()
	assert.Equal(t, "tr", r.Get(ctx, external.OnlyOffice).EditorLang)

	require.NoError(t, store.UpsertExternalService(ctx, external.OnlyOffice, true, "https://docs.example", "s",
		`{}`, time.Time{}, "ok"))
	r.Invalidate()
	assert.Empty(t, r.Get(ctx, external.OnlyOffice).EditorLang)
}
