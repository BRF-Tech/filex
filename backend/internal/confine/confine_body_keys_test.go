package confine

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// The archive routes' keys (2026-10-01): `sources`, `dest` and
// `files[].source` were not known here, so a `root:` token packed and
// extracted outside its folder through them. And a key in another case was not
// the key at all to the map this layer reads - while encoding/json gives it to
// the field tagged with it.

func confined(t *testing.T, body string) (map[string]any, error) {
	t.Helper()
	root := Root{Adapter: "main", Rel: "kutu"}
	out, err := confineBody(root, []byte(body))
	if err != nil {
		return nil, err
	}
	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m))
	return m, nil
}

func TestConfineBody_TheArchiveKeysAreChecked(t *testing.T) {
	for _, body := range []string{
		`{"sources":["main://disari/gizli.txt"],"dest":"main://kutu/p.zip"}`,
		`{"sources":["disari/gizli.txt"],"dest":"main://kutu/p.zip"}`,
		`{"sources":["main://kutu/ic.txt"],"dest":"main://disari/p.zip"}`,
		`{"path":"main://kutu/a.zip","dest":"main://disari"}`,
		`{"path":"main://kutu/a.zip","dest":"/"}`,
		`{"path":"main://kutu/b.zip","files":[{"name":"g.txt","source":"disari/gizli.txt"}]}`,
		`{"sources":["baska://kutu/x"]}`,
		`{"target_dir":"main://disari"}`,
	} {
		_, err := confined(t, body)
		require.True(t, errors.Is(err, ErrOutOfRoot), "%s must be refused, got %v", body, err)
	}
}

func TestConfineBody_TheCheckedKeysPassUnchanged(t *testing.T) {
	// A trailing slash is "copy INTO this folder" to the operations queue, and
	// a bare path is relative to the storage the body names: a check, not a
	// rewrite, keeps both.
	m, err := confined(t, `{"kind":"copy","storage_id":1,"sources":["kutu/a.txt"],"dest":"kutu/hedef/"}`)
	require.NoError(t, err)
	require.Equal(t, "kutu/hedef/", m["dest"])
	require.Equal(t, []any{"kutu/a.txt"}, m["sources"])

	m, err = confined(t, `{"path":"main://kutu/b.zip","files":[{"name":"g.txt","source":"kutu/ic.txt"}]}`)
	require.NoError(t, err)
	require.Equal(t, "kutu/ic.txt", m["files"].([]any)[0].(map[string]any)["source"])
}

func TestConfineBody_AKeyInAnotherCaseIsTheSameKey(t *testing.T) {
	for _, body := range []string{
		`{"PATH":"main://disari/yeni.txt","content":"x"}`,
		`{"Path":"main://disari/yeni.txt"}`,
		`{"Items":[{"PATH":"main://disari/a.txt"}]}`,
		`{"SOURCES":["main://disari/a.txt"]}`,
		`{"Dest":"main://disari"}`,
		`{"Files":[{"Source":"disari/a.txt"}]}`,
		`{"path":"main://kutu/a.txt","Path":"main://disari/a.txt"}`,
	} {
		_, err := confined(t, body)
		require.True(t, errors.Is(err, ErrOutOfRoot), "%s must be refused, got %v", body, err)
	}
	// Inside the root it is rewritten like the lower-case key.
	m, err := confined(t, `{"PATH":"kutu/ic.txt"}`)
	require.NoError(t, err)
	require.Equal(t, "main://kutu/ic.txt", m["PATH"], "a bare path is qualified as the lower-case key's is")
}

func TestBodyPathKeys_ListsWhatIsConfined(t *testing.T) {
	keys := map[string]bool{}
	for _, k := range BodyPathKeys() {
		keys[k] = true
	}
	for _, want := range []string{"path", "item", "target", "sourceDir", "dest", "target_dir", "source[]", "paths[]", "sources[]", "items[].path", "files[].source"} {
		require.True(t, keys[want], "BodyPathKeys does not list %s", want)
	}
}
